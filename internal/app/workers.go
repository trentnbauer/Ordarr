package app

import (
	"context"
	"database/sql"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"time"
)

func (a *App) StartBackground(ctx context.Context) error {
	if err := a.store.RecoverJobs(ctx); err != nil {
		return err
	}
	go func() {
		defer close(a.workersDone)
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			a.scheduleDestinations(ctx)
			a.scheduleSources(ctx)
			a.scheduleAcquisition(ctx)
			if ctx.Err() != nil {
				return
			}
			for {
				j, err := a.store.ClaimJob(ctx)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					break
				}
				a.executeJob(ctx, j)
				if ctx.Err() != nil {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-a.wake:
			case <-tick.C:
			}
		}
	}()
	return nil
}
func (a *App) WaitBackground() { <-a.workersDone }
func (a *App) wakeWorker() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}
func (a *App) executeJob(parent context.Context, j store.Job) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	var err error
	switch j.Kind {
	case "jellyfin-scan":
		err = a.scanJellyfin(ctx, j)
	case "arr-refresh":
		err = a.refreshArr(ctx, j.Target)
	case "arr-auto":
		err = a.automaticAcquisition(ctx, j.Target)
	case "acquire":
		err = a.executeAcquisition(ctx, j.Target)
	case "source":
		err = a.fetchSource(ctx, j)
	case "scan":
		err = a.scanCatalogue(ctx, j)
	case "sync", "publish":
		err = a.syncDestination(ctx, j)
	default:
		err = errors.New("Unsupported job type")
	}
	if parent.Err() != nil {
		return
	}
	status, message := "succeeded", "Completed"
	if err == nil && j.Kind == "source" {
		// Record what the check found so routine "no changes" checks can be folded in history.
		if sub, e := a.store.Subscription(ctx, j.Target); e == nil && sub.Message != "" {
			message = sub.Message
		}
	}
	if err != nil {
		status = "failed"
		message = "The operation failed; last verified data was retained."
		if errors.Is(err, jellyfin.ErrAPI) || errors.Is(err, jellyfin.ErrAuth) || errors.Is(err, jellyfin.ErrChanged) || errors.Is(err, arr.ErrAPI) || errors.Is(err, arr.ErrIdentity) || errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrConflict) || errors.Is(err, plex.ErrAuth) || errors.Is(err, plex.ErrUnavailable) || errors.Is(err, plex.ErrMediaPage) || errors.Is(err, plex.ErrMediaChanged) {
			message = err.Error()
		}
	}
	update, cancelUpdate := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelUpdate()
	a.store.FinishJob(update, j.ID, status, message)
}
func (a *App) connectionAccess(ctx context.Context, id string) (store.PlexConnection, string, error) {
	c, err := a.store.PlexConnection(ctx, id)
	if err != nil {
		return c, "", err
	}
	if c.AccountID == 1 {
		a.accountMu.Lock()
		c, err = a.refreshLinked(ctx, c)
		a.accountMu.Unlock()
		if err != nil {
			return c, "", err
		}
	}
	token, err := a.vault.Open(c.TokenCipher, "plex:"+c.ID+":token")
	return c, token, err
}
func (a *App) scanCatalogue(ctx context.Context, j store.Job) error {
	generation := randomToken()
	c, token, err := a.connectionAccess(ctx, j.Target)
	if err != nil {
		return err
	}
	snapshot, err := a.plex.Inspect(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP)
	if err != nil {
		return err
	}
	if snapshot.MachineID != c.MachineID {
		return plex.ErrMediaChanged
	}
	if err = a.store.ClearStage(ctx, c.ID, generation); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.store.ClearStage(cleanup, c.ID, generation)
		}
	}()
	seen := map[string]bool{}
	series := map[string]map[string]string{}
	n := 0
	for _, lib := range snapshot.Libraries {
		if !lib.Browsable() {
			continue
		}
		items, err := a.listLibrary(ctx, c, token, lib)
		if err != nil {
			return err
		}
		if len(seen)+len(items) > 50000 {
			return store.ErrInvalid
		}
		// The slow part: details for each item of a library list that was consistent when taken.
		for _, item := range items {
			if seen[item.RatingKey] {
				return plex.ErrMediaChanged
			}
			seen[item.RatingKey] = true
			detail, err := a.plex.DetailsCached(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP, item.RatingKey, series)
			if err != nil {
				return err
			}
			if detail.Kind != item.Kind {
				return plex.ErrMediaChanged
			}
			if err = a.store.StageMedia(ctx, c.ID, generation, detail); err != nil {
				return err
			}
			n++
			if err = a.store.JobProgress(ctx, j.ID, n); err != nil {
				return err
			}
		}
	}
	// Revalidate topology and identity before making a complete generation visible.
	end, err := a.plex.Inspect(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP)
	if err != nil {
		return err
	}
	if end.MachineID != snapshot.MachineID || !sameLibraries(snapshot.Libraries, end.Libraries) {
		return plex.ErrMediaChanged
	}
	if err = a.store.CommitCatalogue(ctx, c.ID, generation, c.Revision); err != nil {
		return err
	}
	complete = true
	return nil
}

// listLibrary lists a library's items page by page and insists the listing is consistent (the
// total never changes and no item repeats). When Plex changes mid-listing, which happens whenever
// downloads are being imported, it starts that library's listing again after a short pause. The
// listing takes seconds, so a quiet moment is easy to find; the slow per-item details are fetched
// afterwards from the finished list. A listing that never settles still fails the scan, so a
// half-consistent catalogue is never accepted.
func (a *App) listLibrary(ctx context.Context, c store.PlexConnection, token string, lib plex.Library) ([]plex.MediaItem, error) {
	var last error
	for attempt := 0; attempt < libraryListAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * libraryListPause):
			}
		}
		items, err := a.pageLibrary(ctx, c, token, lib)
		if !errors.Is(err, plex.ErrMediaChanged) {
			return items, err
		}
		last = err
	}
	return nil, last
}

var (
	libraryListAttempts = 6
	libraryListPause    = 4 * time.Second
)

func (a *App) pageLibrary(ctx context.Context, c store.PlexConnection, token string, lib plex.Library) ([]plex.MediaItem, error) {
	var items []plex.MediaItem
	local := map[string]bool{}
	offset, total := 0, -1
	for {
		page, err := a.plex.FetchMediaPage(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP, lib, offset)
		if err != nil {
			return nil, err
		}
		if total >= 0 && total != page.Total {
			return nil, plex.ErrMediaChanged
		}
		total = page.Total
		if total > 50000 {
			return nil, store.ErrInvalid
		}
		for _, item := range page.Items {
			if local[item.RatingKey] {
				return nil, plex.ErrMediaChanged
			}
			local[item.RatingKey] = true
			items = append(items, item)
		}
		if !page.HasNext() {
			return items, nil
		}
		offset += len(page.Items)
	}
}
func sameLibraries(a, b []plex.Library) bool {
	if len(a) != len(b) {
		return false
	}
	keys := map[string]string{}
	for _, l := range a {
		keys[l.Key] = l.Kind
	}
	for _, l := range b {
		if keys[l.Key] != l.Kind {
			return false
		}
	}
	return true
}
func (a *App) queueScan(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.PlexConnection(r.Context(), r.PathValue("id")); err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err := a.store.QueueJob(r.Context(), "scan", r.PathValue("id")); err != nil {
		a.fail(w, "queue scan failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/jobs", 303)
}
