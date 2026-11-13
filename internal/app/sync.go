package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func (a *App) scheduleDestinations(ctx context.Context) {
	destinations, err := a.store.DueDestinations(ctx)
	if err != nil {
		return
	}
	for _, d := range destinations {
		if _, err = a.store.QueueJob(ctx, "sync", d.ID); err == nil {
			a.store.AdvanceDestination(ctx, d)
		}
	}
}
func (a *App) syncDestination(ctx context.Context, j store.Job) (ret error) {
	target := j.Target
	approvedRun := int64(0)
	if j.Kind == "publish" {
		parts := strings.Split(target, "@")
		if len(parts) != 2 {
			return store.ErrInvalid
		}
		target = parts[0]
		approvedRun, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	d, err := a.store.Destination(ctx, target)
	if err != nil {
		return err
	}
	if d.Policy == "paused" {
		return errors.New("Destination is paused")
	}
	var desired []string
	defer func() {
		if ret != nil {
			failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.store.RecordSync(failureCtx, d, j.ID, "failed", "Sync failed safely. Reconcile current state before retrying.", desired)
		}
	}()
	c, err := a.store.PlexConnection(ctx, d.ConnectionID)
	if err != nil {
		return err
	}
	if c.AccountID != 1 {
		return errors.New("Connect this server through the authorised Plex account before publishing")
	}
	a.accountMu.Lock()
	account, result, err := a.verifiedAccount(ctx, false)
	a.accountMu.Unlock()
	if err != nil {
		return err
	}
	if d.UserID != 0 && d.UserID != account.UserID {
		return plex.ErrAccount
	}
	_, token, err := findResource(result, c.ResourceID, c.BaseURL)
	if err != nil {
		return err
	}
	clientID := result.Credentials.Device.ClientID
	if err = a.scanCatalogue(ctx, store.Job{ID: j.ID, Target: c.ID}); err != nil {
		return err
	}
	collection, err := a.store.Collection(ctx, d.CollectionID)
	if err != nil {
		return err
	}
	items, _, err := a.store.Catalogue(ctx, c.ID)
	if err != nil {
		return err
	}
	overrides, err := a.store.ManualMatches(ctx, c.ID, collection)
	if err != nil {
		return err
	}
	verified := map[string]plex.IdentifiedMedia{}
	for _, entry := range collection.Entries {
		identity := matching.Identity{Kind: entry.Kind, Provider: entry.Provider, ExternalID: entry.ExternalID, Season: entry.Season, Episode: entry.Episode}
		match := matching.Resolve(identity, items, overrides[entry.ID])
		if match.State != "available" {
			continue
		}
		fresh, ok := verified[match.RatingKey]
		if !ok {
			fresh, err = a.plex.Details(ctx, c.BaseURL, token, clientID, c.AllowHTTP, match.RatingKey)
			if err != nil {
				return err
			}
			verified[match.RatingKey] = fresh
		}
		if matching.Resolve(identity, []plex.IdentifiedMedia{fresh}, overrides[entry.ID]).State == "available" {
			desired = append(desired, match.RatingKey)
		}
	}
	if len(desired) == 0 {
		return errors.New("No verified playable entries; existing playlist retained")
	}
	if d.Policy == "approval" {
		if approvedRun == 0 {
			return a.store.RecordSync(ctx, d, j.ID, "pending", "Review and approve this exact ordered sequence before publishing.", desired)
		}
		run, err := a.store.SyncRun(ctx, approvedRun)
		if err != nil || run.DestinationID != d.ID || run.State != "approved" || !reflect.DeepEqual(run.Proposed, desired) {
			return store.ErrConflict
		}
	}
	guard := func() error {
		var revision, connectionRevision, destRevision int
		err := a.store.DB.QueryRowContext(ctx, "SELECT c.revision,p.revision,d.revision FROM destinations d JOIN collections c ON c.id=d.collection_id JOIN plex_connections p ON p.id=d.connection_id WHERE d.id=?", d.ID).Scan(&revision, &connectionRevision, &destRevision)
		if err != nil {
			return err
		}
		if revision != collection.Revision || connectionRevision != c.Revision || destRevision != d.Revision {
			return store.ErrConflict
		}
		return nil
	}
	if err = guard(); err != nil {
		return err
	}
	if d.RemoteID == "" {
		id, err := a.plex.FindManagedPlaylist(ctx, c.BaseURL, d.Title, token, clientID)
		if err != nil {
			return err
		}
		if id == "" {
			id, err = a.plex.CreateManagedPlaylist(ctx, c.BaseURL, c.MachineID, d.Title, token, clientID, desired)
			if err != nil {
				return err
			}
		}
		if err = a.store.BindPlaylist(ctx, d.ID, id, account.UserID, d.Revision); err != nil {
			return err
		}
		d.RemoteID = id
		d.UserID = account.UserID
	}
	if err = a.plex.ReconcilePlaylist(ctx, c.BaseURL, d.RemoteID, d.Title, c.MachineID, token, clientID, desired, guard); err != nil {
		return err
	}
	if err = a.store.CompleteSync(ctx, d, collection.Revision, desired); err != nil {
		return err
	}
	return a.store.RecordSync(ctx, d, j.ID, "succeeded", "Exact ordered sequence verified; unrelated playlists and source media were untouched.", desired)
}
func (a *App) createDestination(w http.ResponseWriter, r *http.Request) {
	conn, err := a.store.PlexConnection(r.Context(), r.PostForm.Get("connection_id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if conn.AccountID != 1 {
		http.Error(w, "Use an account-linked Plex server for verified playlist ownership.", 400)
		return
	}
	interval, err := strconv.Atoi(r.PostForm.Get("interval"))
	if err != nil {
		http.Error(w, "Invalid interval", 400)
		return
	}
	if _, err = a.store.CreateDestination(r.Context(), r.PathValue("id"), conn.ID, r.PostForm.Get("policy"), interval); err != nil {
		a.collectionError(w, err)
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/collections/"+r.PathValue("id"), 303)
}
func (a *App) queueSync(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Destination(r.Context(), r.PathValue("destination"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err = a.store.QueueJob(r.Context(), "sync", d.ID); err != nil {
		a.fail(w, "queue sync failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/jobs", 303)
}
func (a *App) destinationPolicy(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Destination(r.Context(), r.PathValue("destination"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	revision, e1 := strconv.Atoi(r.PostForm.Get("revision"))
	interval, e2 := strconv.Atoi(r.PostForm.Get("interval"))
	if e1 != nil || e2 != nil {
		http.Error(w, "Invalid policy form", 400)
		return
	}
	if err = a.store.DestinationPolicy(r.Context(), d.ID, r.PostForm.Get("policy"), interval, revision); err != nil {
		a.collectionError(w, err)
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/collections/"+d.CollectionID, 303)
}
func (a *App) recreateDestination(w http.ResponseWriter, r *http.Request) {
	d, err := a.store.Destination(r.Context(), r.PathValue("destination"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if r.PostForm.Get("confirm") != d.ID {
		http.Error(w, "Type the destination ID to confirm explicit recreation.", 400)
		return
	}
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if err = a.store.RecreateDestination(r.Context(), d.ID, revision); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/collections/"+d.CollectionID, 303)
}
func (a *App) approveSync(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("run"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	run, err := a.store.SyncRun(r.Context(), id)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if err = a.store.ApproveSyncRun(r.Context(), id); err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err = a.store.QueueJob(r.Context(), "publish", fmt.Sprintf("%s@%d", run.DestinationID, id)); err != nil {
		a.fail(w, "queue approved sync failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/jobs", 303)
}
