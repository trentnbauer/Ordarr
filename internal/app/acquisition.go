package app

import (
	"context"
	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"strconv"
	"time"
)

func (a *App) arrAccess(c store.ArrConnection) (arr.Connection, error) {
	key, err := a.vault.Open(c.KeyCipher, "arr:"+c.ID+":key")
	return arr.Connection{Kind: c.Kind, URL: c.BaseURL, Key: key}, err
}
func (a *App) connectArr(w http.ResponseWriter, r *http.Request) {
	base, err := plex.NormalizeBaseURL(r.PostForm.Get("url"), r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	conn := arr.Connection{Kind: r.PostForm.Get("kind"), URL: base, Key: r.PostForm.Get("api_key")}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := arr.New().Inspect(ctx, conn)
	if err != nil {
		http.Error(w, arr.ErrAPI.Error(), 400)
		return
	}
	id := randomToken()
	c := store.ArrConnection{ID: id, Kind: conn.Kind, Name: r.PostForm.Get("name"), BaseURL: base, KeyCipher: a.vault.Seal(conn.Key, "arr:"+id+":key"), Snapshot: snapshot}
	if err = a.store.CreateArr(r.Context(), c); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/acquisition?id="+id, 303)
}
func (a *App) arrPolicy(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.ArrConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if revision != c.Revision {
		a.collectionError(w, store.ErrConflict)
		return
	}
	profile, _ := strconv.Atoi(r.PostForm.Get("profile"))
	root, _ := strconv.Atoi(r.PostForm.Get("root"))
	validProfile, validRoot := false, false
	for _, p := range c.Snapshot.Profiles {
		validProfile = validProfile || p.ID == profile
	}
	for _, p := range c.Snapshot.Roots {
		validRoot = validRoot || p.ID == root
	}
	if !validProfile || !validRoot {
		http.Error(w, "Choose a discovered profile and root folder", 400)
		return
	}
	limit, _ := strconv.Atoi(r.PostForm.Get("limit"))
	if err = a.store.ArrPolicy(r.Context(), c.ID, revision, limit, r.PostForm.Get("exclusions")); err != nil {
		a.collectionError(w, err)
		return
	}
	if err = a.store.ConfigureArrChoices(r.Context(), c.ID, profile, root); err != nil {
		a.collectionError(w, err)
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/settings/acquisition?id="+c.ID, 303)
}
func (a *App) arrRefresh(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.ArrConnection(r.Context(), r.PathValue("id")); err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err := a.store.QueueJob(r.Context(), "arr-refresh", r.PathValue("id")); err != nil {
		a.fail(w, "queue service refresh failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/jobs", 303)
}
func (a *App) refreshArr(ctx context.Context, id string) error {
	c, err := a.store.ArrConnection(ctx, id)
	if err != nil {
		return err
	}
	conn, err := a.arrAccess(c)
	if err != nil {
		return err
	}
	snapshot, err := arr.New().Inspect(ctx, conn)
	if err != nil {
		return err
	}
	return a.store.UpdateArrSnapshot(ctx, c, snapshot)
}
func providerNumber(c store.ArrConnection, e store.Entry) (int, bool) {
	provider, kind := "tvdb", "episode"
	if c.Kind == "radarr" {
		provider, kind = "tmdb", "movie"
	}
	id, err := strconv.Atoi(e.ExternalID)
	return id, err == nil && id > 0 && id <= 2147483647 && e.Provider == provider && e.Kind == kind
}
func (a *App) proposeRequest(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.ArrConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	collection, err := a.store.Collection(r.Context(), r.PostForm.Get("collection_id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	var entry store.Entry
	for _, e := range collection.Entries {
		if e.ID == r.PostForm.Get("entry_id") {
			entry = e
		}
	}
	id, ok := providerNumber(c, entry)
	if !ok {
		http.Error(w, "Choose an exact TMDB movie or TVDB episode entry", 400)
		return
	}
	back := "/collections/" + collection.ID + "#entry-" + entry.ID
	conn, err := a.arrAccess(c)
	if err != nil {
		a.fail(w, "credential access failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	item, err := arr.New().Lookup(ctx, conn, id)
	if err != nil {
		// Shown on the row as "Not found in any downloader"; Request now retries.
		a.store.RecordFailedRequest(r.Context(), c, id, entry.Title, "Lookup found no exact match.")
		http.Redirect(w, r, back, 303)
		return
	}
	// "Request now" is the explicit approval: send it straight away.
	requestID, err := a.store.ProposeAcquisition(r.Context(), c, id, item.Title, "manual", true)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	request, err := a.store.Acquisition(r.Context(), requestID)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if request.State == "pending" || request.State == "uncertain" || request.State == "failed" {
		if err = a.store.RetryAcquisition(r.Context(), requestID, c.Revision); err != nil {
			a.collectionError(w, err)
			return
		}
		request.State = "approved"
	}
	if request.State == "approved" {
		if _, err = a.store.QueueJob(r.Context(), "acquire", requestID); err != nil {
			a.fail(w, "queue acquisition failed")
			return
		}
		a.wakeWorker()
	}
	http.Redirect(w, r, back, 303)
}
func (a *App) approveRequest(w http.ResponseWriter, r *http.Request) {
	request, err := a.store.Acquisition(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	c, err := a.store.ArrConnection(r.Context(), request.ConnectionID)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Confirm the whole-media request effect before approving or retrying", 400)
		return
	}
	if request.State == "uncertain" || request.State == "failed" || request.ConnectionRevision != c.Revision {
		err = a.store.RetryAcquisition(r.Context(), request.ID, c.Revision)
	} else {
		err = a.store.ApproveAcquisition(r.Context(), request.ID)
	}
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err = a.store.QueueJob(r.Context(), "acquire", request.ID); err != nil {
		a.fail(w, "queue acquisition failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/settings/acquisition?id="+c.ID, 303)
}
func (a *App) executeAcquisition(ctx context.Context, id string) (ret error) {
	r, err := a.store.Acquisition(ctx, id)
	if err != nil {
		return err
	}
	if r.State == "completed" {
		return nil
	}
	c, err := a.store.ArrConnection(ctx, r.ConnectionID)
	if err != nil {
		return err
	}
	conn, err := a.arrAccess(c)
	if err != nil {
		return err
	}
	client := arr.New()
	snapshot, err := client.Inspect(ctx, conn)
	if err != nil {
		return err
	}
	if _, exists, e := arr.Existing(conn, snapshot.Media, r.ProviderID); e != nil {
		return e
	} else if exists {
		return a.store.FinishAcquisition(ctx, r.ID, "completed", "Exact media already exists in service; existing settings were untouched.")
	}
	if r.State == "running" {
		a.store.FinishAcquisition(ctx, r.ID, "uncertain", "Interrupted attempt: media not found. Review service and explicitly approve a retry.")
		return arr.ErrAPI
	}
	if r.State != "approved" {
		return store.ErrConflict
	}
	if err = a.store.BeginAcquisition(ctx, r, c); err != nil {
		return err
	}
	// No automatic retry after an uncertain POST. The durable reservation survives restart.
	_, err = client.AddGuarded(ctx, conn, r.ProviderID, c.ProfileID, c.RootID, func() error { return a.store.ValidateAcquisition(ctx, r, c) })
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err != nil {
		a.store.FinishAcquisition(cleanup, r.ID, "uncertain", "Request outcome uncertain. Refresh service status, then explicitly approve a retry if needed.")
		return err
	}
	if err = a.store.FinishAcquisition(cleanup, r.ID, "completed", "Whole movie/series requested. Playability still requires Plex indexing."); err != nil {
		return err
	}
	return a.refreshArr(ctx, c.ID)
}

// collectionDownloads is the per-collection switch for automatic downloads of missing items.
func (a *App) collectionDownloads(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.Collection(r.Context(), id); err != nil {
		a.collectionError(w, err)
		return
	}
	// The form sends "disable=yes" for "Don't download missing items for this collection".
	if err := a.store.SetCollectionDownloads(r.Context(), id, r.PostForm.Get("disable") != "yes"); err != nil {
		a.collectionError(w, err)
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/collections/"+id+"?tab=advanced", 303)
}

// arrReady reports whether a connection has somewhere to send requests.
func arrReady(c store.ArrConnection) bool { return c.ProfileID > 0 && c.RootID > 0 }

func (a *App) scheduleAcquisition(ctx context.Context) {
	if d, err := a.store.Defaults(ctx); err != nil || !d.AutoDownload {
		return
	}
	connections, err := a.store.ArrConnections(ctx)
	if err != nil {
		return
	}
	for _, c := range connections {
		if arrReady(c) && c.NextRun <= time.Now().Unix() {
			if _, err = a.store.QueueJob(ctx, "arr-auto", c.ID); err == nil {
				a.store.DB.ExecContext(ctx, "UPDATE arr_connections SET next_run=? WHERE id=?", time.Now().Add(time.Hour).Unix(), c.ID)
			}
		}
	}
}
func (a *App) automaticAcquisition(ctx context.Context, id string) error {
	c, err := a.store.ArrConnection(ctx, id)
	if err != nil {
		return err
	}
	defaults, err := a.store.Defaults(ctx)
	if err != nil {
		return err
	}
	if !defaults.AutoDownload || !arrReady(c) {
		return nil
	}
	conn, err := a.arrAccess(c)
	if err != nil {
		return err
	}
	snapshot, err := arr.New().Inspect(ctx, conn)
	if err != nil {
		return err
	}
	if err = a.store.UpdateArrSnapshot(ctx, c, snapshot); err != nil {
		return err
	}
	o, err := a.loadOverview(ctx)
	if err != nil {
		return err
	}
	optedOut, err := a.store.NoDownload(ctx)
	if err != nil {
		return err
	}
	var bindings [][2]string // collection, Plex server it is checked against
	for _, ref := range o.Refs {
		if server, ok := o.primary(ref.ID); ok && !optedOut[ref.ID] {
			bindings = append(bindings, [2]string{ref.ID, server.ID})
		}
	}
	known, err := a.store.AcquisitionIndex(ctx)
	if err != nil {
		return err
	}
	proposed := 0
	for _, b := range bindings {
		collection, err := a.store.Collection(ctx, b[0])
		if err != nil {
			return err
		}
		items, scanned, err := a.store.Catalogue(ctx, b[1])
		if err != nil || scanned < time.Now().Add(-24*time.Hour).Unix() {
			continue
		}
		manual, err := a.store.ManualMatches(ctx, b[1], collection)
		if err != nil {
			return err
		}
		for _, e := range collection.Entries {
			providerID, ok := providerNumber(c, e)
			if !ok || store.Excluded(c, providerID) {
				continue
			}
			if _, exists, err := arr.Existing(conn, snapshot.Media, providerID); exists || err != nil {
				continue
			}
			result := matching.Resolve(matching.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}, items, manual[e.ID])
			if result.State != "missing" {
				continue
			}
			// A failed lookup is retried only by "Request now", not every hour.
			if r, seen := known[c.ID+"/"+strconv.Itoa(providerID)]; seen && r.State == "failed" {
				continue
			}
			if proposed >= c.DailyLimit {
				return nil
			}
			item, err := arr.New().Lookup(ctx, conn, providerID)
			if err != nil {
				// Remember it so the UI can say "not found" and we don't ask every hour.
				a.store.RecordFailedRequest(ctx, c, providerID, e.Title, "Lookup found no exact match.")
				continue
			}
			requestID, err := a.store.ProposeAcquisition(ctx, c, providerID, item.Title, "automatic", !c.RequireApproval)
			if err != nil {
				return err
			}
			r, err := a.store.Acquisition(ctx, requestID)
			if err != nil {
				return err
			}
			if r.State == "approved" {
				if _, err = a.store.QueueJob(ctx, "acquire", r.ID); err != nil {
					return err
				}
				a.wakeWorker()
			}
			proposed++
		}
	}
	return nil
}
func (a *App) deleteArr(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := a.store.ArrConnection(r.Context(), id)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if confirm := r.PostForm.Get("confirm"); confirm == "" || confirm != id && confirm != c.Name {
		http.Error(w, "Type the instance name to confirm local removal", 400)
		return
	}
	if _, err := a.store.DB.ExecContext(r.Context(), "DELETE FROM arr_connections WHERE id=?", id); err != nil {
		a.fail(w, "connection removal failed")
		return
	}
	http.Redirect(w, r, "/settings/acquisition", 303)
}
