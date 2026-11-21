package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// A title that is not in Radarr's library yet is looked up in Radarr, its IMDb ID remembered, and a
// Plex item that only carries that IMDb ID is then recognised. "Not found" is remembered too, and a
// downloader that is down stops the run without remembering anything.
func TestAliasLookupFindsTitlesThatAreNotInTheLibraryYet(t *testing.T) {
	previousPause := aliasPause
	aliasPause = time.Millisecond
	t.Cleanup(func() { aliasPause = previousPause })
	a, h := fixture(t)
	ctx := context.Background()
	var lookups atomic.Int32
	var down atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch r.URL.Path {
		case "/api/v3/system/status":
			out = map[string]any{"appName": "Radarr", "version": "5"}
		case "/api/v3/qualityprofile":
			out = []any{map[string]any{"id": 1, "name": "HD"}}
		case "/api/v3/rootfolder":
			out = []any{map[string]any{"id": 2, "path": "/movies"}}
		case "/api/v3/queue":
			out = map[string]any{"page": 1, "pageSize": 100, "totalRecords": 0, "records": []any{}}
		case "/api/v3/movie":
			out = []any{} // the library is empty: nothing to translate from
		case "/api/v3/movie/lookup":
			lookups.Add(1)
			if down.Load() {
				http.Error(w, "down", 503)
				return
			}
			switch r.URL.Query().Get("term") {
			case "tmdb:1724":
				out = []any{map[string]any{"tmdbId": 1724, "imdbId": "tt0800080", "title": "The Incredible Hulk", "titleSlug": "the-incredible-hulk"}}
			default:
				out = []any{} // not found
			}
		default:
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	session, csrf := setup(t, h)
	if w := request(h, "POST", "/settings/acquisition", url.Values{"csrf": {csrf.Value}, "kind": {"radarr"}, "name": {"Radarr"}, "url": {server.URL}, "api_key": {"k"}, "allow_http": {"yes"}}, session, csrf); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Entries: []store.Entry{
		{ID: "hulk", Kind: "movie", Title: "The Incredible Hulk", Provider: "tmdb", ExternalID: "1724"},
		{ID: "nope", Kind: "movie", Title: "Not A Real Movie", Provider: "tmdb", ExternalID: "999999"},
		{ID: "imdb", Kind: "movie", Title: "Skipped (IMDb entry)", Provider: "imdb", ExternalID: "tt0000001"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Plex has the Hulk, identified only by its IMDb ID.
	if err = a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex", Name: "Plex", BaseURL: "http://127.0.0.1:32400", AllowHTTP: true, ClientID: "client", TokenCipher: a.vault.Seal("token", "plex:plex:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	if err = a.store.ClearStage(ctx, "plex", "g1"); err != nil {
		t.Fatal(err)
	}
	hulk := plex.IdentifiedMedia{MediaItem: plex.MediaItem{RatingKey: "10", Kind: "movie", Title: "The Incredible Hulk"}, IDs: map[string]string{"imdb": "tt0800080"}, Playable: true}
	if err = a.store.StageMedia(ctx, "plex", "g1", hulk); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CommitCatalogue(ctx, "plex", "g1", 1); err != nil {
		t.Fatal(err)
	}
	collection, _ := a.store.Collection(ctx, id)
	states := func() map[string]string {
		t.Helper()
		got, err := a.availabilityFor(ctx, []store.CollectionRef{{ID: id, Revision: collection.Revision}}, "plex")
		if err != nil {
			t.Fatal(err)
		}
		return got[id].States
	}
	entryID := map[string]string{}
	for _, e := range collection.Entries {
		entryID[e.Title] = e.ID
	}
	if got := states()[entryID["The Incredible Hulk"]]; got != "unmatched" {
		t.Fatalf("before the lookup an IMDb-only item can only be offered by title: %q", got)
	}
	// The scheduler queues a run because there are unresolved titles and a Radarr to ask.
	a.scheduleAliases(ctx)
	var queued int
	a.store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='aliases' AND state='queued'").Scan(&queued)
	if queued != 1 {
		t.Fatalf("lookup run not queued: %d", queued)
	}
	if err = a.resolveAliases(ctx); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 2 { // the Hulk and the unknown movie; the IMDb-ID entry is not looked up
		t.Fatalf("lookups: %d", lookups.Load())
	}
	if got := states()[entryID["The Incredible Hulk"]]; got != "available" {
		t.Fatalf("after the lookup the IMDb-only Plex item should match: %q", got)
	}
	// Answers (including "not found") are remembered: a second run asks nothing.
	if err = a.resolveAliases(ctx); err != nil || lookups.Load() != 2 {
		t.Fatalf("remembered answers were looked up again: %v %d", err, lookups.Load())
	}
	// Once everything is resolved the scheduler stops queueing runs.
	a.store.DB.Exec("DELETE FROM jobs")
	a.store.SetPref(ctx, "aliases_next_run", "0")
	a.scheduleAliases(ctx)
	a.store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='aliases'").Scan(&queued)
	if queued != 0 {
		t.Fatal("a run was queued with nothing left to look up")
	}
	// "Not found" expires, so a title that appears later is asked again.
	a.store.DB.Exec("UPDATE id_aliases SET fetched_at=? WHERE found=0", time.Now().Add(-8*24*time.Hour).Unix())
	if err = a.resolveAliases(ctx); err != nil || lookups.Load() != 3 {
		t.Fatalf("an expired 'not found' was not retried: %v %d", err, lookups.Load())
	}
	// A downloader that is down fails the run and remembers nothing.
	a.store.DB.Exec("DELETE FROM id_aliases")
	down.Store(true)
	if err = a.resolveAliases(ctx); err == nil {
		t.Fatal("a downloader outage was reported as success")
	}
	if n, _ := a.store.Aliases(ctx); len(n) != 0 {
		t.Fatalf("an outage was remembered as 'not found': %v", n)
	}
	if !strings.Contains(jobKindsText(), "ID lookup") {
		t.Fatal("the job is not named in Activity")
	}
}

func jobKindsText() string {
	var b strings.Builder
	for _, v := range jobKinds {
		b.WriteString(v + "\n")
	}
	return b.String()
}
