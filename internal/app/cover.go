package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
	"github.com/trentnbauer/Ordarr/internal/subscription"
)

// fetchImage downloads a cover; tests replace it.
var fetchImage = subscription.FetchImage

// coverRetry is how long a failed cover download is left alone before trying again.
const coverRetry = 6 * time.Hour

// fetchCover is the "cover" job: it keeps the downloaded copy of a collection's cover in step with
// the cover URL in its collection file (and drops it when the file no longer has one).
func (a *App) fetchCover(ctx context.Context, j store.Job) error {
	c, err := a.store.Collection(ctx, j.Target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.Cover == "" {
		return a.store.ClearCover(ctx, c.ID)
	}
	if a.store.CoverURLCached(ctx, c.ID) == c.Cover {
		return nil
	}
	data, kind, err := fetchImage(ctx, c.Cover)
	if err != nil {
		a.store.SetPref(ctx, "cover_failed_"+c.ID, c.Cover+"|"+strconv.FormatInt(time.Now().Unix(), 10))
		return err
	}
	return a.store.SaveCover(ctx, c.ID, c.Cover, kind, data)
}

// ensureCover reports whether the collection has a downloaded cover to show, and queues the
// download when its cover is missing or changed. A URL that failed recently is not retried at once.
func (a *App) ensureCover(ctx context.Context, c store.Collection) bool {
	cached := a.store.CoverURLCached(ctx, c.ID)
	if cached == c.Cover { // both empty, or the copy is current
		return c.Cover != ""
	}
	if c.Cover != "" {
		if parts := strings.SplitN(a.store.Pref(ctx, "cover_failed_"+c.ID), "|", 2); len(parts) == 2 && parts[0] == c.Cover {
			if at, err := strconv.ParseInt(parts[1], 10, 64); err == nil && time.Since(time.Unix(at, 0)) < coverRetry {
				return false
			}
		}
	}
	if _, err := a.store.QueueJob(ctx, "cover", c.ID); err == nil {
		a.wakeWorker()
	}
	return false
}

// coverImage serves a collection's downloaded cover from Ordarr itself, because the pages may
// only load images from their own origin.
func (a *App) coverImage(w http.ResponseWriter, r *http.Request) {
	cover, ok, err := a.store.CoverFor(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, "cover failed")
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", cover.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("ETag", `"`+cover.SHA+`"`)
	if r.Header.Get("If-None-Match") == `"`+cover.SHA+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(cover.Data)
}

// polishPlaylist makes the Plex playlist presentable after a successful sync: it moves Ordarr's
// ownership marker from the playlist title into its description and uploads the collection's
// cover as the poster. These are cosmetic, so a failure is logged and never fails the sync.
func (a *App) polishPlaylist(ctx context.Context, conn store.PlexConnection, token string, d store.Destination, c store.Collection) {
	if err := a.plex.FinalizePlaylist(ctx, conn.BaseURL, d.RemoteID, d.Title, c.Description, token, conn.ClientID); err != nil {
		a.logger.Warn("playlist description and name could not be updated", "destination", d.ID, "error", err.Error())
	}
	if c.Cover == "" {
		return
	}
	cover, ok, err := a.store.CoverFor(ctx, c.ID)
	if err == nil && (!ok || cover.URL != c.Cover) {
		if data, kind, ferr := fetchImage(ctx, c.Cover); ferr == nil && a.store.SaveCover(ctx, c.ID, c.Cover, kind, data) == nil {
			cover, ok = store.Cover{URL: c.Cover, ContentType: kind, Data: data}, true
			sum := sha256.Sum256(data)
			cover.SHA = hex.EncodeToString(sum[:])
		}
	}
	if !ok || cover.URL != c.Cover || a.store.PosterApplied(ctx, d.ID) == cover.SHA {
		return
	}
	if err = a.plex.UploadPlaylistPoster(ctx, conn.BaseURL, d.RemoteID, d.Title, cover.Data, cover.ContentType, token, conn.ClientID); err != nil {
		a.logger.Warn("playlist cover could not be uploaded", "destination", d.ID, "error", err.Error())
		return
	}
	a.store.SetPosterApplied(ctx, d.ID, cover.SHA)
}
