package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// Many collection files (the Star Wars one) list movies by IMDb ID. Radarr needs the TMDB ID, so such
// movies used to show "Not found in any downloader" and were never requested. Now the TMDB ID is looked
// up in Radarr, the movie is requested with it, and a title Radarr does not know is reported as such.
func TestMoviesListedByIMDbIDAreIdentifiedAndRequested(t *testing.T) {
	previousPause := aliasPause
	aliasPause = time.Millisecond
	t.Cleanup(func() { aliasPause = previousPause })
	a, h := fixture(t)
	ctx := context.Background()
	var mu sync.Mutex
	var posted []int
	inLibrary := false
	media := map[string]any{"id": 5, "tmdbId": 1893, "imdbId": "tt0120915", "title": "Star Wars: Episode I", "titleSlug": "star-wars-1", "monitored": true, "hasFile": false}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
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
		case "/api/v3/movie/lookup":
			if term := r.URL.Query().Get("term"); term == "imdb:tt0120915" || term == "tmdb:1893" {
				out = []any{media}
			} else {
				out = []any{} // Radarr does not know the bogus ID
			}
		case "/api/v3/movie":
			if r.Method == "POST" {
				var body struct{ TmdbID int }
				json.NewDecoder(r.Body).Decode(&body)
				posted = append(posted, body.TmdbID)
				inLibrary = true
				out = media
			} else if inLibrary {
				out = []any{media}
			} else {
				out = []any{}
			}
		default:
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	defer server.Close()
	session, csrf := setup(t, h)
	post := func(path string, v url.Values) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		if w := request(h, "POST", path, v, session, csrf); w.Code != 303 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	post("/settings/acquisition", url.Values{"kind": {"radarr"}, "name": {"Radarr"}, "url": {server.URL}, "api_key": {"k"}, "allow_http": {"yes"}})
	conns, _ := a.store.ArrConnections(ctx)
	c := conns[0]
	post("/settings/acquisition/"+c.ID+"/policy", url.Values{"revision": {strconv.Itoa(c.Revision)}, "profile": {"1"}, "root": {"2"}, "limit": {"5"}})
	c, _ = a.store.ArrConnection(ctx, c.ID)
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Star Wars", Entries: []store.Entry{
		{ID: "phantom", Kind: "movie", Title: "Star Wars: Episode I - The Phantom Menace", Provider: "imdb", ExternalID: "tt0120915"},
		{ID: "bogus", Kind: "movie", Title: "Not A Real Movie", Provider: "imdb", ExternalID: "tt0000009"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex", Name: "Plex", BaseURL: "http://127.0.0.1:32400", AllowHTTP: true, ClientID: "client", TokenCipher: a.vault.Seal("token", "plex:plex:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CommitCatalogue(ctx, "plex", "empty-generation", 1); err != nil {
		t.Fatal(err)
	}
	collection, _ := a.store.Collection(ctx, id)
	status := func(imdb string) rowStatus {
		t.Helper()
		c, _ := a.store.ArrConnection(ctx, c.ID)
		al, _, _ := a.aliases(ctx)
		for _, e := range collection.Entries {
			if e.ExternalID == imdb {
				return missingStatus([]store.ArrConnection{c}, nil, e, true, false, map[string]bool{c.ID: true}, al)
			}
		}
		t.Fatal("no entry", imdb)
		return rowStatus{}
	}
	// Before anything is looked up the title is "being identified", not "not found".
	if s := status("tt0120915"); s.Label != statusLookup {
		t.Fatalf("before the lookup: %q (%s)", s.Label, s.Detail)
	}
	// The automatic run identifies both, then requests only the one Radarr knows, by TMDB ID.
	if err = a.automaticAcquisition(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	requests, _ := a.store.Acquisitions(ctx)
	if len(requests) != 1 || requests[0].ProviderID != 1893 || requests[0].State != "approved" {
		t.Fatalf("expected one request for TMDB 1893: %+v", requests)
	}
	if err = a.executeAcquisition(ctx, requests[0].ID); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 1 || posted[0] != 1893 {
		t.Fatalf("Radarr was sent %v, want TMDB 1893", posted)
	}
	// The unknown IMDb ID is reported for what it is, with an action, not as "no downloader".
	s := status("tt0000009")
	if s.Label != statusNotFound || !strings.Contains(s.Detail, "IMDB ID") || !s.Warn {
		t.Fatalf("bogus IMDb ID: %+v", s)
	}
	if s := status("tt0120915"); s.Label != statusRequested && s.Label != statusPlexScan {
		t.Fatalf("after requesting: %+v", s)
	}
	// "Request now" works for an IMDb-listed entry too (no duplicate: it is already requested).
	post("/settings/acquisition/"+c.ID+"/requests", url.Values{"collection_id": {id}, "entry_id": {collection.Entries[0].ID}})
	if len(posted) != 1 {
		t.Fatalf("a second POST was sent for an existing movie: %v", posted)
	}
	// The pre-send check accepts the alias-derived request and still refuses an unrelated ID.
	if err = a.store.ValidateAcquisition(ctx, requests[0], c); err != nil {
		t.Fatalf("an alias-derived request failed its pre-send check: %v", err)
	}
	other := requests[0]
	other.ProviderID = 424242
	if err = a.store.ValidateAcquisition(ctx, other, c); err == nil {
		t.Fatal("a request for an ID in no collection passed the pre-send check")
	}
}
