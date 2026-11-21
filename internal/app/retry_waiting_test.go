package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// A failed playlist sync only asks for you once it has failed repeatedly, and pressing Retry takes it
// out of "Waiting on you" while the retry is queued or running.
func TestFailedSyncLeavesWaitingOnYouWhileRetrying(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), MachineID: "m", ServerName: "Plex", ServerVersion: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	collection, err := a.store.ImportCollection(ctx, store.Collection{Title: "Marvel", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := a.store.Destination(ctx, dest)
	fail := func() {
		t.Helper()
		if err := a.store.RecordSync(ctx, d, "job", "failed", "Sync failed safely.", nil); err != nil {
			t.Fatal(err)
		}
	}
	waiting := func() (items int, status string) {
		t.Helper()
		o, _ := a.loadOverview(ctx)
		values, _ := a.availabilityAll(ctx, o)
		n, err := a.inputCount(ctx, o, values)
		if err != nil {
			t.Fatal(err)
		}
		status, _ = collectionStatus(o, values, collection)
		return n, status
	}
	page := func(path string) string { return request(h, "GET", path, nil, session, csrf).Body.String() }

	// One failed attempt retries itself on the next schedule: nothing to do, nothing listed.
	fail()
	if n, status := waiting(); n != 0 || !strings.Contains(status, "trying again soon") {
		t.Fatalf("one failure: %d %q", n, status)
	}
	if strings.Contains(page("/jobs"), "didn't sync to") || strings.Contains(page("/needs-input"), "didn't sync to") {
		t.Fatal("a single failed sync was listed as waiting on you")
	}
	// The same failure again: now it is listed.
	fail()
	if n, status := waiting(); n != 1 || status != "Sync failed · retry" {
		t.Fatalf("repeated failure: %d %q", n, status)
	}
	if !strings.Contains(page("/jobs"), "didn't sync to") || !strings.Contains(page("/needs-input"), "didn't sync to") {
		t.Fatal("a repeated failure was not listed")
	}
	// Retry: while the retry is queued it is no longer waiting on you.
	if w := request(h, "POST", "/destinations/"+dest+"/sync", url.Values{"csrf": {csrf.Value}}, session, csrf); w.Code != 303 {
		t.Fatal("retry", w.Code)
	}
	if n, status := waiting(); n != 0 || !strings.Contains(status, "Syncing again") {
		t.Fatalf("retry queued: %d %q", n, status)
	}
	if strings.Contains(page("/jobs"), "didn't sync to") || strings.Contains(page("/needs-input"), "didn't sync to") {
		t.Fatal("a failed sync stayed in 'Waiting on you' after Retry")
	}
	if strings.Contains(page("/collections/"+collection), `class="count">!`) {
		t.Fatal("the Target tab badge stayed after Retry")
	}
	// A retry that fails again brings it back; one that succeeds clears it.
	a.store.DB.Exec("UPDATE jobs SET state='failed' WHERE kind='sync'")
	fail()
	if n, _ := waiting(); n != 1 {
		t.Fatalf("a retry that failed again should be listed: %d", n)
	}
	if err = a.store.RecordSync(ctx, d, "job", "succeeded", "ok", nil); err != nil {
		t.Fatal(err)
	}
	if n, _ := waiting(); n != 0 {
		t.Fatalf("a successful sync should clear it: %d", n)
	}
	// An approval is something to act on, whatever else is going on.
	if err = a.store.RecordSync(ctx, d, "job", "pending", "needs approval", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := waiting(); n != 1 {
		t.Fatalf("a pending approval should be listed: %d", n)
	}
}
