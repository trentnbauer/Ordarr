package app

import (
	"database/sql"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"strconv"
)

type matchedEntry struct {
	Entry  store.Entry
	Result matching.Result
}
type matchView struct {
	Collection store.Collection
	Connection plexView
	Entries    []matchedEntry
	Catalogue  []plex.IdentifiedMedia
	ScannedAt  int64
	Ready      bool
}

func (a *App) matchesPage(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	conn, err := a.store.PlexConnection(r.Context(), r.PathValue("connection"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	view := &matchView{Collection: c, Connection: plexPublic(conn)}
	items, scanned, err := a.store.Catalogue(r.Context(), conn.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, store.ErrConflict) {
		a.fail(w, "catalogue lookup failed")
		return
	}
	if err == nil {
		view.Ready = true
		view.Catalogue = items
		view.ScannedAt = scanned
		overrides, e := a.store.ManualMatches(r.Context(), conn.ID, c)
		if e != nil {
			a.fail(w, "manual matches failed")
			return
		}
		for _, entry := range c.Entries {
			identity := matching.Identity{Kind: entry.Kind, Provider: entry.Provider, ExternalID: entry.ExternalID, Season: entry.Season, Episode: entry.Episode}
			view.Entries = append(view.Entries, matchedEntry{Entry: entry, Result: matching.Resolve(identity, items, overrides[entry.ID])})
		}
	}
	a.render(w, r, 200, page{Title: "Match " + c.Title, Mode: "matches", MatchView: view})
}
func (a *App) manualMatch(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	rev, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil || rev != c.Revision {
		a.collectionError(w, store.ErrConflict)
		return
	}
	conn := r.PathValue("connection")
	items, _, err := a.store.Catalogue(r.Context(), conn)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	var entry store.Entry
	found := false
	for _, e := range c.Entries {
		if e.ID == r.PostForm.Get("entry_id") {
			entry = e
			found = true
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	key := r.PostForm.Get("rating_key")
	valid := key == ""
	for _, item := range items {
		if item.RatingKey == key && item.Kind == entry.Kind {
			valid = true
		}
	}
	if !valid {
		http.Error(w, "Choose an indexed item of the same media type", 400)
		return
	}
	if err = a.store.SetManualMatch(r.Context(), conn, entry, key); err != nil {
		a.fail(w, "manual match update failed")
		return
	}
	http.Redirect(w, r, "/collections/"+c.ID+"/matches/"+conn, 303)
}
