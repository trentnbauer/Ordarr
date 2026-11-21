package app

import (
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
	if _, err = a.store.PlexConnection(r.Context(), r.PathValue("connection")); err != nil {
		a.collectionError(w, err)
		return
	}
	// The Missing tab is gone: picks happen on the Order tab.
	http.Redirect(w, r, "/collections/"+c.ID, 303)
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
	a.avail.bump()
	http.Redirect(w, r, "/collections/"+c.ID+"#entry-"+entry.ID, 303)
}
