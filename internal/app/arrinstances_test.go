package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// Two Radarr instances: only the collection's chosen instance requests a movie, and the default
// instance is what a new collection starts with.
func TestOnlyChosenInstancesDownloadAndDefaultsApplyToNewCollections(t *testing.T) {
	a, h := fixture(t)
	ctx := context.Background()
	exists := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		media := map[string]any{"id": 7, "tmdbId": 42, "title": "Confirmed movie", "titleSlug": "confirmed", "monitored": true, "hasFile": false}
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
			out = []any{media}
		case "/api/v3/movie":
			if r.Method == "POST" {
				exists[r.Host] = true
				out = media
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
	post := func(path string, v url.Values, want int) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		if w := request(h, "POST", path, v, session, csrf); w.Code != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, name := range []string{"Radarr A", "Radarr B"} {
		post("/settings/acquisition", url.Values{"kind": {"radarr"}, "name": {name}, "url": {server.URL}, "api_key": {"k"}, "allow_http": {"yes"}}, 303)
	}
	conns, _ := a.store.ArrConnections(ctx)
	if len(conns) != 2 {
		t.Fatal(conns)
	}
	byName := map[string]store.ArrConnection{}
	for _, c := range conns {
		byName[c.Name] = c
		post("/settings/acquisition/"+c.ID+"/policy", url.Values{"revision": {strconv.Itoa(c.Revision)}, "profile": {"1"}, "root": {"2"}, "limit": {"5"}}, 303)
	}
	// The first instance of a kind became the default; the second did not.
	defaults, _ := a.store.ArrDefaults(ctx)
	if defaults["radarr"] != byName["Radarr A"].ID {
		t.Fatalf("defaults %v", defaults)
	}
	collection, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "Movie", Provider: "tmdb", ExternalID: "42"}}})
	if err != nil {
		t.Fatal(err)
	}
	if chosen, _ := a.store.CollectionArrs(ctx, collection); len(chosen) != 1 || !chosen[byName["Radarr A"].ID] {
		t.Fatalf("a new collection should start with the default instance only: %v", chosen)
	}
	plexConn := store.PlexConnection{ID: "plex", Name: "Plex", BaseURL: "http://127.0.0.1:32400", AllowHTTP: true, ClientID: "client", TokenCipher: a.vault.Seal("token", "plex:plex:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1"}
	if err = a.store.SavePlexConnection(ctx, plexConn, 0); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CommitCatalogue(ctx, "plex", "empty-generation", 1); err != nil {
		t.Fatal(err)
	}
	requestsFrom := func(name string) int {
		var n int
		a.store.DB.QueryRow("SELECT count(*) FROM acquisition_requests WHERE connection_id=?", byName[name].ID).Scan(&n)
		return n
	}
	for _, name := range []string{"Radarr A", "Radarr B"} {
		if err = a.automaticAcquisition(ctx, byName[name].ID); err != nil {
			t.Fatal(err)
		}
	}
	if requestsFrom("Radarr A") != 1 || requestsFrom("Radarr B") != 0 {
		t.Fatalf("only the chosen instance may request: A=%d B=%d", requestsFrom("Radarr A"), requestsFrom("Radarr B"))
	}
	// Adding B to the collection (the checkbox list) lets it request too.
	if err = a.store.SetCollectionArrs(ctx, collection, []string{byName["Radarr A"].ID, byName["Radarr B"].ID}); err != nil {
		t.Fatal(err)
	}
	if err = a.automaticAcquisition(ctx, byName["Radarr B"].ID); err != nil {
		t.Fatal(err)
	}
	if requestsFrom("Radarr B") != 1 {
		t.Fatal("a ticked instance did not request")
	}
	// The collection page lists both instances with the chosen ones ticked.
	body := request(h, "GET", "/collections/"+collection+"?tab=downloads", nil, session, csrf).Body.String()
	if !strings.Contains(body, `name="instance" value="`+byName["Radarr B"].ID+`" checked`) || !strings.Contains(body, "Download from") {
		t.Fatal("instance checkboxes missing")
	}
	// Saving the form with only B ticked switches the collection to B.
	post("/collections/"+collection+"/downloads", url.Values{"enabled": {"yes"}, "instances_listed": {"1"}, "instance": {byName["Radarr B"].ID}}, 303)
	if chosen, _ := a.store.CollectionArrs(ctx, collection); len(chosen) != 1 || !chosen[byName["Radarr B"].ID] {
		t.Fatalf("form did not save the choice: %v", chosen)
	}
	// An unknown instance is refused, not silently dropped.
	if err = a.store.SetCollectionArrs(ctx, collection, []string{"nope"}); err == nil {
		t.Fatal("unknown instance accepted")
	}
	// Changing the default affects only collections created afterwards.
	post("/settings/acquisition/"+byName["Radarr B"].ID+"/default", url.Values{"default": {"yes"}}, 303)
	later, _ := a.store.ImportCollection(ctx, store.Collection{Title: "Later", Entries: []store.Entry{{ID: "b", Kind: "movie", Title: "M2", Provider: "tmdb", ExternalID: "43"}}})
	if chosen, _ := a.store.CollectionArrs(ctx, later); len(chosen) != 1 || !chosen[byName["Radarr B"].ID] {
		t.Fatalf("new default not applied: %v", chosen)
	}
	if chosen, _ := a.store.CollectionArrs(ctx, collection); !chosen[byName["Radarr B"].ID] || chosen[byName["Radarr A"].ID] {
		t.Fatal("changing the default must not touch existing collections")
	}
}
