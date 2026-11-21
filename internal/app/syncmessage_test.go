package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Every sync failure says what happened and what to do, in plain words; none is the old
// "Reconcile current state" text.
func TestSyncFailureMessagesSayWhatHappenedAndWhatToDo(t *testing.T) {
	for _, c := range []struct {
		err  error
		want []string // all must appear
	}{
		{plex.ErrMediaChanged, []string{"changing its library", "downloads", "Nothing was changed", "Retry"}},
		{fmt.Errorf("scan: %w", plex.ErrMediaChanged), []string{"changing its library"}}, // wrapped errors are recognised
		{plex.ErrAuth, []string{"Home Plex rejected", "Connections > Plex", "Retry"}},
		{plex.ErrUnavailable, []string{"couldn't reach Home Plex", "address under Connections", "Retry"}},
		{plex.ErrRateLimit, []string{"slow down", "Retry"}},
		{plex.ErrPlaylistConflict, []string{"missing, or was changed or renamed", "Target tab", "Recreate"}},
		{plex.ErrPlaylist, []string{"left as it was", "Retry", "logs"}},
		{plex.ErrResponse, []string{"couldn't understand", "Retry"}},
		{store.ErrConflict, []string{"changed while the sync was running", "Retry"}},
		{context.DeadlineExceeded, []string{"didn't finish in time", "Retry"}},
		{errors.New("No verified playable entries; existing playlist retained"), []string{"None of this collection's items are playable", "fills in by itself"}},
		{errors.New("Destination is paused"), []string{"paused", "Target tab"}},
		{errors.New("Connect this server through the authorised Plex account before publishing"), []string{"URL and token", "Sign in with Plex"}},
		{errors.New("something nobody planned for"), []string{"unexpected problem", "left as it was", "Retry", "Activity"}},
	} {
		got := syncFailureMessage(c.err, "Home Plex")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%v: %q is missing %q", c.err, got, w)
			}
		}
		if strings.Contains(got, "Reconcile") || strings.Contains(got, "failed safely") {
			t.Errorf("%v: jargon is back: %q", c.err, got)
		}
	}
	if got := syncFailureMessage(plex.ErrAuth, ""); !strings.HasPrefix(got, "Plex rejected") {
		t.Errorf("an unknown server name should read as 'Plex': %q", got)
	}
	// Failures recorded before the reason was kept are shown honestly instead of as jargon.
	old := syncText(legacySyncFailure)
	if strings.Contains(old, "Reconcile") || strings.Contains(old, "migration") || !strings.Contains(old, "no reason was kept") || !strings.Contains(old, "left as it was") {
		t.Errorf("legacy text: %q", old)
	}
	if syncText("Exact ordered sequence verified.") != "Exact ordered sequence verified." {
		t.Error("an ordinary message must be shown unchanged")
	}
}

// The new wording reaches the screens where the failure is shown.
func TestSyncFailureWordingIsShownOnActivityAndNeedsInput(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), MachineID: "m", ServerName: "Plex", ServerVersion: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	collection, _ := a.store.ImportCollection(ctx, store.Collection{Title: "Marvel", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	dest, _ := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0)
	d, _ := a.store.Destination(ctx, dest)
	// Two failures in a row so it is listed: one with the real reason, one recorded the old way.
	a.store.RecordSync(ctx, d, "j1", "failed", legacySyncFailure, nil)
	a.store.RecordSync(ctx, d, "j2", "failed", syncFailureMessage(plex.ErrMediaChanged, "Home Plex"), nil)
	for _, path := range []string{"/jobs", "/needs-input"} {
		body := request(h, "GET", path, nil, session, csrf).Body.String()
		if !strings.Contains(body, "Plex was changing its library") || strings.Contains(body, "Reconcile") || strings.Contains(body, "The existing playlist was left as it was.") {
			t.Errorf("%s: %s", path, body[strings.Index(body, "didn't sync"):min(len(body), strings.Index(body, "didn't sync")+400)])
		}
	}
	// The collection's Target tab shows the reason, and a legacy run reads sensibly in the history.
	target := request(h, "GET", "/collections/"+collection+"?tab=target", nil, session, csrf).Body.String()
	if !strings.Contains(target, "The last sync failed. Plex was changing its library") || !strings.Contains(target, "no reason was kept") || strings.Contains(target, "Reconcile") {
		t.Error("target tab wording")
	}
}

// The Target tab says "Retry", so it has a Retry button next to the message.
func TestTargetTabHasARetryButtonWhenTheLastSyncFailed(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), MachineID: "m", ServerName: "Plex", ServerVersion: "1"}, 0)
	collection, _ := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	dest, _ := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0)
	d, _ := a.store.Destination(ctx, dest)
	a.store.RecordSync(ctx, d, "j", "failed", syncFailureMessage(plex.ErrUnavailable, "Home Plex"), nil)
	body := request(h, "GET", "/collections/"+collection+"?tab=target", nil, session, csrf).Body.String()
	if !strings.Contains(body, `action="/destinations/`+dest+`/sync"><input type="hidden" name="csrf"`) || !strings.Contains(body, ">Retry</button>") {
		t.Fatal("no Retry button in the failed-sync box")
	}
}
