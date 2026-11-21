package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// One "Follow" with nothing else chosen: follows the source (applying updates automatically),
// publishes to the only Plex server and leaves automatic downloads on.
func TestFollowDoesEverythingByDefault(t *testing.T) {
	a, h := fixture(t)
	fakeSources(t, a, map[string]string{"tiny.json": tinyOrder})
	session, csrf := setup(t, h)
	ctx := context.Background()
	now := time.Now().Unix()
	if _, err := a.store.DB.Exec("INSERT INTO plex_accounts VALUES(1,42,'plex-user','Plex User',0,'client-1',?,?,?,1)", []byte("cipher"), now+86400, now); err != nil {
		t.Fatal(err)
	}
	conn := store.PlexConnection{AccountID: 1, ResourceID: "machine-1", ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "client-1", MachineID: "machine-1", ServerName: "Plex", ServerVersion: "1", TokenCipher: a.vault.Seal("token", "plex:plex-1:token"), Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}
	if err := a.store.SavePlexConnection(ctx, conn, 0); err != nil {
		t.Fatal(err)
	}
	// The dialog shows a single "Follow" button and no server picker for one server.
	page := request(h, "GET", "/add", nil, session, csrf).Body.String()
	if !strings.Contains(page, "<span>Follow</span>") || strings.Contains(page, `id="publish-server"`) || !strings.Contains(page, "Appears in Plex as a playlist on Home Plex") {
		t.Fatal("dialog not collapsed to Follow", page)
	}
	// Posting only the picked order (no server, no policy) does everything.
	w := request(h, "POST", "/add", url.Values{"csrf": {csrf.Value}, "source": {"community"}, "path": {"tiny.json"}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/collections/"), "?created=1")
	sub, err := a.store.CollectionSubscription(ctx, id)
	if err != nil || sub.Policy != "automatic" {
		t.Fatalf("source not followed automatically: %+v %v", sub, err)
	}
	destinations, _ := a.store.AllDestinations(ctx)
	if len(destinations) != 1 || destinations[0].CollectionID != id || destinations[0].ConnectionID != "plex-1" {
		t.Fatalf("playlist not created: %+v", destinations)
	}
	var queued int
	a.store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='sync' AND target=?", destinations[0].ID).Scan(&queued)
	if queued != 1 {
		t.Fatal("first sync not queued")
	}
	if off, _ := a.store.NoDownload(ctx); off[id] {
		t.Fatal("new collections must download by default")
	}
}
