package app

import (
	"context"
	"database/sql"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"reflect"
	"strconv"
	"time"
)

type jellyfinView struct {
	ID, Name, BaseURL string
	Revision          int
	Snapshot          jellyfin.Snapshot
	CheckedAt         string
}

func jellyfinPublic(c store.JellyfinConnection) jellyfinView {
	return jellyfinView{ID: c.ID, Name: c.Name, BaseURL: c.BaseURL, Revision: c.Revision, Snapshot: c.Snapshot, CheckedAt: time.Unix(c.CheckedAt, 0).UTC().Format(time.RFC3339)}
}

type jellyfinMatchView struct {
	Collection store.Collection
	Connection jellyfinView
	Ready      bool
	ScannedAt  string
	Entries    []jellyfinMatchedEntry
	Items      []jellyfin.Item
}
type jellyfinMatchedEntry struct {
	Entry  store.Entry
	Result jellyfin.Match
}

func (a *App) jellyfinSettings(w http.ResponseWriter, r *http.Request) {
	connections, err := a.store.JellyfinConnections(r.Context())
	if err != nil {
		a.fail(w, "Jellyfin settings failed")
		return
	}
	p := page{Title: "Jellyfin connections", Mode: "jellyfin"}
	for _, c := range connections {
		p.JellyfinConnections = append(p.JellyfinConnections, jellyfinPublic(c))
	}
	a.render(w, r, 200, p)
}
func (a *App) jellyfinError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jellyfin.ErrAPI), errors.Is(err, jellyfin.ErrAuth), errors.Is(err, jellyfin.ErrChanged):
		http.Error(w, err.Error(), 502)
	case errors.Is(err, plex.ErrURL), errors.Is(err, plex.ErrHTTPConsent):
		http.Error(w, err.Error(), 400)
	default:
		a.collectionError(w, err)
	}
}
func (a *App) connectJellyfin(w http.ResponseWriter, r *http.Request) {
	allow := r.PostForm.Get("allow_http") == "yes"
	base, err := plex.NormalizeURL(r.PostForm.Get("url"), allow)
	if err != nil {
		a.jellyfinError(w, err)
		return
	}
	token := r.PostForm.Get("token")
	client := jellyfin.New()
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := client.Inspect(ctx, base, token)
	if err != nil {
		a.jellyfinError(w, err)
		return
	}
	id := randomToken()[:32]
	c := store.JellyfinConnection{ID: id, Name: r.PostForm.Get("name"), BaseURL: base, AllowHTTP: allow, TokenCipher: a.vault.Seal(token, "jellyfin:"+id+":token"), Snapshot: snapshot}
	if err = a.store.CreateJellyfin(r.Context(), c); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/jellyfin", 303)
}
func (a *App) recheckJellyfin(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.JellyfinConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if revision != c.Revision {
		a.collectionError(w, store.ErrConflict)
		return
	}
	token, err := a.vault.Open(c.TokenCipher, "jellyfin:"+c.ID+":token")
	if err != nil {
		a.fail(w, "credential access failed")
		return
	}
	client := jellyfin.New()
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := client.Inspect(ctx, c.BaseURL, token)
	if err == nil {
		err = a.store.RecheckJellyfin(r.Context(), c, snapshot)
	}
	if err != nil {
		a.jellyfinError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/jellyfin", 303)
}
func (a *App) deleteJellyfin(w http.ResponseWriter, r *http.Request) {
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if r.PostForm.Get("confirm") != r.PathValue("id") {
		http.Error(w, "Type the connection ID to confirm local removal", 400)
		return
	}
	if err := a.store.DeleteJellyfin(r.Context(), r.PathValue("id"), revision); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/jellyfin", 303)
}
func (a *App) queueJellyfinScan(w http.ResponseWriter, r *http.Request) {
	if _, err := a.store.JellyfinConnection(r.Context(), r.PathValue("id")); err != nil {
		a.collectionError(w, err)
		return
	}
	if _, err := a.store.QueueJob(r.Context(), "jellyfin-scan", r.PathValue("id")); err != nil {
		a.fail(w, "queue Jellyfin scan failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/jobs", 303)
}
func sameJellyfinSnapshot(a, b jellyfin.Snapshot) bool {
	if a.ServerID != b.ServerID || a.UserID != b.UserID || len(a.Libraries) != len(b.Libraries) {
		return false
	}
	libs := map[string]jellyfin.Library{}
	for _, l := range a.Libraries {
		libs[l.ID] = l
	}
	for _, l := range b.Libraries {
		if !reflect.DeepEqual(libs[l.ID], l) {
			return false
		}
	}
	return true
}
func (a *App) scanJellyfin(ctx context.Context, j store.Job) error {
	c, err := a.store.JellyfinConnection(ctx, j.Target)
	if err != nil {
		return err
	}
	token, err := a.vault.Open(c.TokenCipher, "jellyfin:"+c.ID+":token")
	if err != nil {
		return err
	}
	client := jellyfin.New()
	defer client.Close()
	snapshot, err := client.Inspect(ctx, c.BaseURL, token)
	if err != nil {
		return err
	}
	if !sameJellyfinSnapshot(c.Snapshot, snapshot) {
		return jellyfin.ErrChanged
	}
	generation := randomToken()
	complete := false
	defer func() {
		if !complete {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.store.ClearJellyfinStage(cleanup, c.ID, generation)
		}
	}()
	seen := map[string]bool{}
	series := map[string]map[string]string{}
	for _, lib := range snapshot.Libraries {
		if !lib.Browsable() {
			continue
		}
		offset, total := 0, -1
		for {
			p, err := client.FetchPage(ctx, c.BaseURL, token, snapshot.UserID, lib.ID, offset, series)
			if err != nil {
				return err
			}
			if total >= 0 && p.Total != total {
				return jellyfin.ErrChanged
			}
			total = p.Total
			for _, item := range p.Items {
				if seen[item.ID] || len(seen) >= jellyfin.MaxItems {
					return jellyfin.ErrChanged
				}
				seen[item.ID] = true
				if err = a.store.StageJellyfin(ctx, c.ID, generation, item); err != nil {
					return err
				}
			}
			if err = a.store.JobProgress(ctx, j.ID, len(seen)); err != nil {
				return err
			}
			offset += len(p.Items)
			if offset == p.Total {
				break
			}
		}
	}
	end, err := client.Inspect(ctx, c.BaseURL, token)
	if err != nil {
		return err
	}
	if !sameJellyfinSnapshot(snapshot, end) {
		return jellyfin.ErrChanged
	}
	if err = a.store.CommitJellyfinCatalogue(ctx, c, generation); err != nil {
		return err
	}
	complete = true
	return nil
}
func (a *App) jellyfinMatches(w http.ResponseWriter, r *http.Request) {
	collection, err := a.store.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	c, err := a.store.JellyfinConnection(r.Context(), r.PathValue("connection"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	view := &jellyfinMatchView{Collection: collection, Connection: jellyfinPublic(c)}
	items, at, err := a.store.JellyfinCatalogue(r.Context(), c.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, store.ErrConflict) {
		a.fail(w, "Jellyfin catalogue lookup failed")
		return
	}
	if err == nil {
		manual, e := a.store.JellyfinManualMatches(r.Context(), c.ID, collection)
		if e != nil {
			a.fail(w, "Jellyfin mapping lookup failed")
			return
		}
		view.Ready = true
		view.Items = items
		if len(view.Items) > 200 {
			view.Items = view.Items[:200]
		}
		view.ScannedAt = time.Unix(at, 0).UTC().Format(time.RFC3339)
		for _, entry := range collection.Entries {
			result := jellyfin.Resolve(jellyfin.Identity{Kind: entry.Kind, Provider: entry.Provider, ExternalID: entry.ExternalID, Season: entry.Season, Episode: entry.Episode}, items, manual[entry.ID])
			view.Entries = append(view.Entries, jellyfinMatchedEntry{Entry: entry, Result: result})
		}
	}
	a.render(w, r, 200, page{Title: "Match " + collection.Title + " with Jellyfin", Mode: "jellyfin-matches", JellyfinMatches: view})
}
func (a *App) jellyfinManualMatch(w http.ResponseWriter, r *http.Request) {
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	err := a.store.SetJellyfinMatch(r.Context(), r.PathValue("connection"), r.PathValue("id"), r.PostForm.Get("entry_id"), r.PostForm.Get("item_id"), revision)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/collections/"+r.PathValue("id")+"/jellyfin/"+r.PathValue("connection"), 303)
}
