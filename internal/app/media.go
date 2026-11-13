package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

type libraryMediaView struct {
	Connection        plexView
	Library           plex.Library
	Cached            *store.LibraryPage
	FetchedAt         string
	ChangedConnection bool
}

func (a *App) mediaContext(r *http.Request) (store.PlexConnection, plex.Library, error) {
	c, err := a.store.PlexConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		return c, plex.Library{}, err
	}
	for _, lib := range c.Libraries {
		if lib.Key == r.PathValue("library") && lib.Browsable() {
			return c, lib, nil
		}
	}
	return c, plex.Library{}, sql.ErrNoRows
}
func (a *App) renderMedia(w http.ResponseWriter, r *http.Request, c store.PlexConnection, lib plex.Library, status int, message string) {
	cached, err := a.store.LibraryPage(r.Context(), c.ID, lib.Key)
	view := &libraryMediaView{Connection: plexPublic(c), Library: lib}
	if err == nil {
		view.Cached = &cached
		view.FetchedAt = time.Unix(cached.FetchedAt, 0).UTC().Format(time.RFC3339)
		view.ChangedConnection = cached.ConnectionRevision != c.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.fail(w, "cached Plex media page failed")
		return
	}
	a.render(w, r, status, page{Title: lib.Title, Mode: "media", Error: message, LibraryMedia: view})
}
func (a *App) libraryMedia(w http.ResponseWriter, r *http.Request) {
	c, lib, err := a.mediaContext(r)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	a.renderMedia(w, r, c, lib, 200, "")
}
func mediaError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrConflict):
		return 409, "This connection or cached page changed. Reload before trying again."
	case errors.Is(err, store.ErrInvalid):
		return 400, err.Error()
	case errors.Is(err, plex.ErrMediaChanged):
		return 409, err.Error()
	case errors.Is(err, plex.ErrMediaPage) || errors.Is(err, plex.ErrAuth) || errors.Is(err, plex.ErrUnavailable) || errors.Is(err, plex.ErrResponse) || errors.Is(err, plex.ErrRateLimit) || errors.Is(err, plex.ErrRedirect) || errors.Is(err, plex.ErrAccount) || errors.Is(err, plex.ErrAccountResponse):
		return 502, err.Error()
	default:
		return 502, "The Plex media fetch failed. The last cached page was retained."
	}
}
func (a *App) fetchLibraryMedia(w http.ResponseWriter, r *http.Request) {
	c, lib, err := a.mediaContext(r)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	connectionRevision, err := strconv.Atoi(r.PostForm.Get("connection_revision"))
	if err != nil || connectionRevision != c.Revision {
		a.renderMedia(w, r, c, lib, 409, "This connection changed. Reload before fetching media.")
		return
	}
	cached, err := a.store.LibraryPage(r.Context(), c.ID, lib.Key)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.fail(w, "cached Plex media lookup failed")
		return
	}
	cacheRevision, parseErr := strconv.Atoi(r.PostForm.Get("cache_revision"))
	if parseErr != nil || cacheRevision != cached.Revision {
		a.renderMedia(w, r, c, lib, 409, "This cached page changed. Reload before trying again.")
		return
	}
	action := r.PostForm.Get("action")
	offset := 0
	history := append([]int{}, cached.Payload.History...)
	if action != "first" && (cached.Revision == 0 || cached.ConnectionRevision != c.Revision) {
		a.renderMedia(w, r, c, lib, 409, "Load the first page after a connection change.")
		return
	}
	switch action {
	case "first":
		history = nil
	case "reload":
		offset = cached.Payload.Page.Offset
	case "next":
		if !cached.Payload.Page.HasNext() {
			a.renderMedia(w, r, c, lib, 400, "There is no next page.")
			return
		}
		offset = cached.Payload.Page.Offset + len(cached.Payload.Page.Items)
		history = append(history, cached.Payload.Page.Offset)
		if len(history) > 1000 {
			history = history[len(history)-1000:]
		}
	case "previous":
		if len(history) == 0 {
			a.renderMedia(w, r, c, lib, 400, "There is no previous page in this browsing session.")
			return
		}
		offset = history[len(history)-1]
		history = history[:len(history)-1]
	default:
		a.renderMedia(w, r, c, lib, 400, "Choose a supported page action.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	fail := func(fetchErr error) {
		status, message := mediaError(fetchErr)
		if cached.Revision > 0 {
			if saveErr := a.store.LibraryPageFailed(r.Context(), c.ID, lib.Key, cached.Revision, message); saveErr != nil {
				status, message = mediaError(saveErr)
			}
		}
		a.renderMedia(w, r, c, lib, status, message)
	}
	if c.AccountID == 1 {
		if !a.lockAccount(w, r) {
			return
		}
		defer a.accountMu.Unlock()
		c, err = a.refreshLinked(ctx, c)
		if err != nil {
			fail(err)
			return
		}
	}
	token, err := a.vault.Open(c.TokenCipher, "plex:"+c.ID+":token")
	if err != nil {
		fail(err)
		return
	}
	snapshot, err := a.plex.Inspect(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP)
	if err != nil {
		fail(err)
		return
	}
	if snapshot.MachineID != c.MachineID {
		fail(plex.ErrMediaChanged)
		return
	}
	accessible := false
	for _, current := range snapshot.Libraries {
		if current.Key == lib.Key && current.Kind == lib.Kind {
			accessible = true
			break
		}
	}
	if !accessible {
		fail(plex.ErrMediaChanged)
		return
	}
	result, err := a.plex.FetchMediaPage(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP, lib, offset)
	if err != nil {
		fail(err)
		return
	}
	if offset > 0 && cached.Payload.Page.Total != result.Total {
		fail(plex.ErrMediaChanged)
		return
	}
	if action == "next" || action == "previous" {
		previousKeys := map[string]bool{}
		for _, item := range cached.Payload.Page.Items {
			previousKeys[item.RatingKey] = true
		}
		for _, item := range result.Items {
			if previousKeys[item.RatingKey] {
				fail(plex.ErrMediaChanged)
				return
			}
		}
	}
	payload := store.LibraryPagePayload{Page: result, History: history}
	if err = a.store.SaveLibraryPage(r.Context(), c.ID, lib.Key, connectionRevision, cacheRevision, payload); err != nil {
		status, message := mediaError(err)
		a.renderMedia(w, r, c, lib, status, message)
		return
	}
	http.Redirect(w, r, "/settings/plex/"+c.ID+"/libraries/"+lib.Key+"/media", 303)
}
