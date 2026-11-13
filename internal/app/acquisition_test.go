package app

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAcquisitionApprovalAutomaticOptInAndUncertainRecovery(t *testing.T) {
	a, h := fixture(t)
	ctx := context.Background()
	exists := false
	posts := 0
	failReply := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "private-fixture-key" {
			t.Error("wrong key")
		}
		var out any
		media := map[string]any{"id": 7, "tmdbId": 42, "title": "Confirmed movie", "titleSlug": "confirmed", "monitored": true, "hasFile": false}
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
				posts++
				exists = true
				if failReply {
					http.Error(w, "private-fixture-key", 503)
					return
				}
				out = media
			} else if exists {
				out = []any{media}
			} else {
				out = []any{}
			}
		default:
			t.Errorf("unsafe endpoint %s", r.URL.Path)
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
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "private-fixture-key") {
			t.Fatal("secret leaked")
		}
	}
	post("/settings/acquisition", url.Values{"kind": {"radarr"}, "name": {"Home movies"}, "url": {server.URL}, "api_key": {"private-fixture-key"}, "allow_http": {"yes"}}, 303)
	connections, _ := a.store.ArrConnections(ctx)
	if len(connections) != 1 {
		t.Fatal(connections)
	}
	c := connections[0]
	if c.Automatic || !c.RequireApproval {
		t.Fatal("unsafe defaults")
	}
	if strings.Contains(string(c.KeyCipher), "private-fixture-key") {
		t.Fatal("plaintext secret")
	}
	post("/settings/acquisition/"+c.ID+"/policy", url.Values{"revision": {strconv.Itoa(c.Revision)}, "profile": {"1"}, "root": {"2"}, "limit": {"5"}, "automatic": {"yes"}}, 303)
	c, _ = a.store.ArrConnection(ctx, c.ID)
	collection, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "Movie", Provider: "tmdb", ExternalID: "42"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.automaticAcquisition(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	requests, _ := a.store.Acquisitions(ctx)
	if len(requests) != 0 {
		t.Fatal("automatic requests without collection opt-in")
	}
	plexConn := store.PlexConnection{ID: "plex", Name: "Plex", BaseURL: "http://127.0.0.1:32400", AllowHTTP: true, ClientID: "client", TokenCipher: a.vault.Seal("token", "plex:plex:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1"}
	if err = a.store.SavePlexConnection(ctx, plexConn, 0); err != nil {
		t.Fatal(err)
	}
	if err = a.store.CommitCatalogue(ctx, "plex", "empty-generation", 1); err != nil {
		t.Fatal(err)
	}
	if err = a.store.EnableCollectionRequests(ctx, c.ID, collection, "plex", true); err != nil {
		t.Fatal(err)
	}
	if err = a.automaticAcquisition(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	requests, _ = a.store.Acquisitions(ctx)
	if len(requests) != 1 || requests[0].State != "approved" {
		t.Fatal(requests)
	}
	id := requests[0].ID
	if err = a.executeAcquisition(ctx, id); err == nil {
		t.Fatal("uncertain response ignored")
	}
	r, _ := a.store.Acquisition(ctx, id)
	if r.State != "uncertain" || posts != 1 {
		t.Fatal(r, posts)
	}
	// Read-side recovery observes the already-created item and sends no duplicate POST.
	if err = a.executeAcquisition(ctx, id); err != nil {
		t.Fatal(err)
	}
	r, _ = a.store.Acquisition(ctx, id)
	if r.State != "completed" || posts != 1 {
		t.Fatal(r, posts)
	}
	for _, path := range []string{"/settings/acquisition", "/settings/acquisition?collection=" + collection} {
		w := request(h, "GET", path, nil, session, csrf)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-fixture-key") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if arr.Status(arr.Snapshot{}, arr.Media{ID: 7, Monitored: true}) != "monitored" {
		t.Fatal("status")
	}
}
