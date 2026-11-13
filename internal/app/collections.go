package app

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/trentnbauer/Ordarr/internal/store"
)

type entryInput struct{ Kind, Title, Provider, ExternalID, Season, Start, End string }

func defaultEntryInput() entryInput {
	return entryInput{Kind: "movie", Provider: "tmdb", Season: "0", Start: "1", End: "1"}
}
func inputFromForm(v url.Values) entryInput {
	return entryInput{Kind: v.Get("kind"), Title: v.Get("entry_title"), Provider: v.Get("provider"), ExternalID: v.Get("external_id"), Season: v.Get("season"), Start: v.Get("start"), End: v.Get("end")}
}
func (i entryInput) spec() (store.EntrySpec, error) {
	spec := store.EntrySpec{Kind: i.Kind, Title: i.Title, Provider: i.Provider, ExternalID: i.ExternalID}
	if i.Kind == "movie" {
		return spec, nil
	}
	var err error
	if spec.Season, err = strconv.Atoi(i.Season); err != nil {
		return spec, errors.New("Enter a valid season number.")
	}
	if i.Kind != "season" {
		if spec.Start, err = strconv.Atoi(i.Start); err != nil {
			return spec, errors.New("Enter a valid first episode number.")
		}
	}
	if i.Kind == "season" || i.Kind == "episode_range" {
		if spec.End, err = strconv.Atoi(i.End); err != nil {
			return spec, errors.New("Enter a valid last episode number or season episode count.")
		}
	}
	return spec, nil
}
func (a *App) collectionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, "Collection not found", http.StatusNotFound)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, err.Error(), 409)
	case errors.Is(err, store.ErrInvalid):
		http.Error(w, err.Error(), 400)
	default:
		a.fail(w, "collection operation failed")
	}
}
func (a *App) listCollections(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100000 {
			http.Error(w, "Invalid page", 400)
			return
		}
		offset = n
	}
	q := r.URL.Query().Get("q")
	filter := r.URL.Query().Get("filter")
	result, err := a.store.ListCollections(r.Context(), q, filter, offset)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	p := page{Title: "Your watch orders", Mode: "dashboard", Collections: result, Query: q, Filter: filter, Prev: "", Next: ""}
	link := func(n int) string {
		v := url.Values{"q": {q}, "filter": {filter}, "offset": {strconv.Itoa(n)}}
		return "/?" + v.Encode()
	}
	if offset > 0 {
		previous := max(0, offset-store.PageSize)
		p.Prev = link(previous)
	}
	if len(result) > store.PageSize {
		p.Collections = result[:store.PageSize]
		p.Next = link(offset + store.PageSize)
	}
	a.render(w, r, 200, p)
}
func (a *App) newCollection(w http.ResponseWriter, r *http.Request) {
	p := page{Title: "Create a watch order", Mode: "new"}
	if r.Method == "GET" {
		a.render(w, r, 200, p)
		return
	}
	title, description := r.PostForm.Get("title"), r.PostForm.Get("description")
	id, err := a.store.CreateCollection(r.Context(), title, description)
	if errors.Is(err, store.ErrInvalid) {
		p.Error = err.Error()
		p.Collection = store.Collection{Title: title, Description: description}
		a.render(w, r, 400, p)
		return
	}
	if err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/collections/"+id, 303)
}
func (a *App) collectionDetail(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	p := page{Title: c.Title, Mode: "collection", Collection: c, Input: defaultEntryInput()}
	if id := r.PathValue("entry"); id != "" {
		found := false
		for _, e := range c.Entries {
			if e.ID == id {
				p.EntryID = id
				p.Input = entryInput{Kind: e.Kind, Title: e.Title, Provider: e.Provider, ExternalID: e.ExternalID, Season: strconv.Itoa(e.Season), Start: strconv.Itoa(e.Episode), End: strconv.Itoa(e.Episode)}
				if e.Kind == "movie" {
					p.Input.Start = "1"
					p.Input.End = "1"
				}
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	ids, err := a.store.PlexConnectionIDs(r.Context())
	if err != nil {
		a.fail(w, "connection list failed")
		return
	}
	for _, id := range ids {
		conn, err := a.store.PlexConnection(r.Context(), id)
		if err != nil {
			a.fail(w, "connection lookup failed")
			return
		}
		p.PlexConnections = append(p.PlexConnections, plexPublic(conn))
	}
	p.Destinations, err = a.store.Destinations(r.Context(), c.ID)
	if err != nil {
		a.fail(w, "destinations failed")
		return
	}
	p.SyncRuns, err = a.store.SyncRuns(r.Context(), c.ID)
	if err != nil {
		a.fail(w, "sync history failed")
		return
	}
	p.Source, err = a.optionalSource(r.Context(), c.ID)
	if err != nil {
		a.fail(w, "subscription lookup failed")
		return
	}
	a.render(w, r, 200, p)
}
func (a *App) changeCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	revision, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil || revision < 1 {
		http.Error(w, "A valid collection revision is required. Reload the page.", 400)
		return
	}
	action := r.PostForm.Get("action")
	if action == "fork" {
		clone, err := a.store.ForkCollection(r.Context(), id, revision)
		if err != nil {
			a.collectionError(w, err)
			return
		}
		http.Redirect(w, r, "/collections/"+clone, 303)
		return
	}
	if action == "delete" {
		c, err := a.store.Collection(r.Context(), id)
		if err != nil {
			a.collectionError(w, err)
			return
		}
		if r.PostForm.Get("confirm_title") != c.Title {
			http.Error(w, "Type the collection title exactly to confirm deletion.", 400)
			return
		}
		if err = a.store.DeleteCollection(r.Context(), id, revision); err != nil {
			a.collectionError(w, err)
			return
		}
		http.Redirect(w, r, "/", 303)
		return
	}
	change := store.CollectionChange{Action: action, Title: r.PostForm.Get("title"), Description: r.PostForm.Get("description"), IDs: r.PostForm["entry_id"]}
	input := inputFromForm(r.PostForm)
	switch action {
	case "add", "edit":
		change.Spec, err = input.spec()
	case "move":
		change.Target, err = strconv.Atoi(r.PostForm.Get("target"))
		if err != nil {
			err = errors.New("Enter a valid final position.")
		}
	case "remove":
		if r.PostForm.Get("confirm_remove") != "yes" {
			err = errors.New("Confirm removal of the selected entries.")
		}
	case "reorder":
		change.IDs = strings.Fields(r.PostForm.Get("order"))
	}
	if err != nil {
		err = fmt.Errorf("%w: %s", store.ErrInvalid, err)
	}
	if err == nil {
		err = a.store.ChangeCollection(r.Context(), id, revision, change)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrInvalid) || errors.Is(err, store.ErrConflict) {
			c, loadErr := a.store.Collection(r.Context(), id)
			if loadErr != nil {
				a.collectionError(w, loadErr)
				return
			}
			status := 400
			if errors.Is(err, store.ErrConflict) {
				status = 409
			}
			p := page{Title: c.Title, Mode: "collection", Collection: c, Error: err.Error(), Input: defaultEntryInput()}
			if action == "add" || action == "edit" {
				p.Input = input
				if action == "edit" && len(change.IDs) == 1 {
					p.EntryID = change.IDs[0]
				}
			}
			a.render(w, r, status, p)
			return
		}
		a.collectionError(w, err)
		return
	}
	location := "/collections/" + id
	if (action == "move" || action == "edit") && len(change.IDs) == 1 {
		location += "#entry-" + change.IDs[0]
	}
	http.Redirect(w, r, location, 303)
}
