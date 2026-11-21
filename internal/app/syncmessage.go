package app

import (
	"context"
	"errors"
	"strings"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// legacySyncFailure is the only text older failed runs recorded; it said nothing about the cause.
const legacySyncFailure = "Sync failed safely. Reconcile current state before retrying."

// syncFailureMessage says, in plain words, what went wrong with a playlist sync and what to do about
// it. Every message states that the playlist was left alone, so the reader never has to wonder.
func syncFailureMessage(err error, server string) string {
	if server == "" {
		server = "Plex"
	}
	text := ""
	if err != nil {
		text = err.Error()
	}
	switch {
	case errors.Is(err, plex.ErrMediaChanged):
		return "Plex was changing its library while Ordarr was reading it, usually because new downloads are being imported. Nothing was changed in Plex. Ordarr tries again automatically; if it keeps failing, press Retry once the imports have settled."
	case errors.Is(err, plex.ErrAuth):
		return server + " rejected Ordarr's Plex sign-in. Open Connections > Plex, refresh or re-authorise the account, then press Retry. Nothing was changed in Plex."
	case errors.Is(err, plex.ErrRateLimit):
		return server + " asked Ordarr to slow down. Nothing was changed in Plex. Wait a few minutes, then press Retry."
	case errors.Is(err, plex.ErrUnavailable), errors.Is(err, plex.ErrRedirect), errors.Is(err, plex.ErrURL), errors.Is(err, plex.ErrHTTPConsent):
		return "Ordarr couldn't reach " + server + ". Check that Plex is running and that its address under Connections is still right, then press Retry. Nothing was changed in Plex."
	case errors.Is(err, plex.ErrPlaylistConflict):
		return "The playlist in Plex is missing, or was changed or renamed there, so Ordarr can't prove it is the one it created and won't touch it. On this collection's Target tab, open \"Playlist deleted or renamed in Plex? Recreate it\"."
	case errors.Is(err, plex.ErrPlaylist):
		return "Plex refused, or only partly applied, a change to the playlist. The playlist was left as it was. Press Retry; if it keeps failing, check the Plex server's logs."
	case errors.Is(err, plex.ErrResponse):
		return server + " sent an answer Ordarr couldn't understand. Nothing was changed in Plex. Press Retry; if it keeps happening, update Plex or check its logs."
	case errors.Is(err, store.ErrConflict):
		return "This collection or its Plex connection was changed while the sync was running. Nothing was lost. Press Retry."
	case errors.Is(err, context.DeadlineExceeded):
		return "The sync didn't finish in time (a large library or a slow Plex). The playlist was left as it was. Press Retry."
	case strings.Contains(text, "No verified playable entries"):
		return "None of this collection's items are playable in Plex yet, so the playlist was left alone. It fills in by itself once Plex has some of them."
	case strings.Contains(text, "is paused"):
		return "Syncing is paused for this playlist. Choose another option under \"If the playlist in Plex differs\" on the collection's Target tab to sync again."
	case strings.Contains(text, "authorised Plex account"):
		return "This server was added by URL and token, which can't publish playlists. Sign in with Plex under Connections > Plex to publish."
	}
	return "Ordarr hit an unexpected problem while syncing. The playlist in Plex was left as it was. Press Retry; if it fails again, the Activity page shows the details."
}

// syncText is how a recorded sync message is shown. Runs recorded before the reason was kept all
// carry one generic sentence, which is replaced by an honest version of the same thing.
func syncText(message string) string {
	if message == legacySyncFailure {
		return "This older sync didn't finish, and no reason was kept for it. The playlist in Plex was left as it was."
	}
	return message
}

// serverNameFor is the Plex connection's name for a destination, for messages ("" if unknown).
func (a *App) serverNameFor(ctx context.Context, d store.Destination) string {
	if c, err := a.store.PlexConnection(ctx, d.ConnectionID); err == nil {
		return c.Name
	}
	return ""
}
