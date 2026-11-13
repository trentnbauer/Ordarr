package app

import (
	"context"
	"database/sql"
	"errors"
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
	if err != nil {
		status = "failed"
		message = "The operation failed; last verified data was retained."
		if errors.Is(err, plex.ErrAuth) || errors.Is(err, plex.ErrUnavailable) || errors.Is(err, plex.ErrMediaPage) || errors.Is(err, plex.ErrMediaChanged) {
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
		offset, total := 0, -1
		for {
			page, err := a.plex.FetchMediaPage(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP, lib, offset)
			if err != nil {
				return err
			}
			if total >= 0 && total != page.Total {
				return plex.ErrMediaChanged
			}
			total = page.Total
			if total > 50000 {
				return store.ErrInvalid
			}
			for _, item := range page.Items {
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
				if n > 50000 {
					return store.ErrInvalid
				}
				if err = a.store.JobProgress(ctx, j.ID, n); err != nil {
					return err
				}
			}
			if !page.HasNext() {
				break
			}
			offset += len(page.Items)
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
func (a *App) jobsPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := a.store.RecentJobs(r.Context())
	if err != nil {
		a.fail(w, "job history failed")
		return
	}
	a.render(w, r, 200, page{Title: "Activity", Mode: "jobs", Jobs: jobs})
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
