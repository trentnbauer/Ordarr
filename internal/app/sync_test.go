package app

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestAccountOwnedSyncMissingThenAvailableAndRestart(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	f.pms.Close()
	available := false
	created := false
	title := ""
	keys := []string{}
	writes := 0
	f.pms = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "resource-scoped-token" {
			t.Error("wrong server credential")
			http.Error(w, "bad", 401)
			return
		}
		path := r.URL.Path
		container := map[string]any{}
		switch {
		case path == "/":
			container = map[string]any{"machineIdentifier": "resource-machine", "friendlyName": "Plex", "version": "1"}
		case path == "/library/sections/all":
			container = map[string]any{"size": 1, "Directory": []any{map[string]any{"key": "1", "title": "Movies", "type": "movie"}}}
		case path == "/library/sections/1/all":
			items := []any{map[string]any{"ratingKey": "1", "type": "movie", "title": "A"}}
			if available {
				items = append(items, map[string]any{"ratingKey": "2", "type": "movie", "title": "B"})
			}
			container = map[string]any{"size": len(items), "offset": 0, "totalSize": len(items), "Metadata": items}
		case strings.HasPrefix(path, "/library/metadata/"):
			key := strings.TrimPrefix(path, "/library/metadata/")
			container = map[string]any{"Metadata": []any{map[string]any{"ratingKey": key, "type": "movie", "title": "Movie", "Guid": []any{map[string]any{"id": "tmdb://" + key}}, "Media": []any{map[string]any{"Part": []any{map[string]any{"key": "/library/parts/1/file", "exists": true}}}}}}}
		case path == "/playlists" && r.Method == "GET":
			items := []any{}
			if created {
				items = append(items, map[string]any{"ratingKey": "50", "title": title, "playlistType": "video", "smart": false})
			}
			container = map[string]any{"size": len(items), "Metadata": items}
		case path == "/playlists" && r.Method == "POST":
			writes++
			created = true
			title = r.URL.Query().Get("title")
			uri := r.URL.Query().Get("uri")
			keys = strings.Split(uri[strings.LastIndex(uri, "/")+1:], ",")
			container = map[string]any{"Metadata": []any{map[string]any{"ratingKey": "50", "title": title, "playlistType": "video", "smart": false}}}
		case path == "/playlists/50":
			container = map[string]any{"Metadata": []any{map[string]any{"ratingKey": "50", "title": title, "playlistType": "video", "smart": false}}}
		case path == "/playlists/50/items" && r.Method == "GET":
			items := []any{}
			for i, key := range keys {
				items = append(items, map[string]any{"ratingKey": key, "playlistItemID": i + 100})
			}
			container = map[string]any{"size": len(items), "Metadata": items}
		case path == "/playlists/50/items" && r.Method == "PUT":
			writes++
			uri := r.URL.Query().Get("uri")
			keys = append(keys, uri[strings.LastIndex(uri, "/")+1:])
			return
		default:
			t.Errorf("unexpected request %s %s", r.Method, path)
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": container})
	}))
	defer f.pms.Close()
	session, csrf := setup(t, h)
	post := func(path string, v url.Values) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != 303 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	v := url.Values{"csrf": {csrf.Value}}
	w := request(h, "POST", "/settings/plex/account/start", v, session, csrf)
	f.granted.Store(true)
	post(w.Header().Get("Location"), url.Values{})
	account, _ := a.store.PlexAccount(context.Background())
	post("/settings/plex/account/connect", url.Values{"account_revision": {strconv.Itoa(account.Revision)}, "resource_id": {"resource-machine"}, "uri": {f.pms.URL}, "allow_http": {"yes"}})
	ctx := context.Background()
	connections, _ := a.store.PlexConnectionIDs(ctx)
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "A", Provider: "tmdb", ExternalID: "1"}, {ID: "b", Kind: "movie", Title: "B", Provider: "tmdb", ExternalID: "2"}}})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := a.store.CreateDestination(ctx, id, connections[0], "authoritative", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.syncDestination(ctx, store.Job{ID: "first", Target: dest}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys, ",") != "1" {
		t.Fatal(keys)
	}
	canonical, _ := a.store.Collection(ctx, id)
	if len(canonical.Entries) != 2 {
		t.Fatal("missing entry lost")
	}
	available = true
	if err = a.syncDestination(ctx, store.Job{ID: "second", Target: dest}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(keys, ",") != "1,2" {
		t.Fatal(keys)
	}
	before := writes
	if err = a.syncDestination(ctx, store.Job{ID: "retry", Target: dest}); err != nil {
		t.Fatal(err)
	}
	if writes != before {
		t.Fatal("retry wrote unchanged playlist")
	}
	d, _ := a.store.Destination(ctx, dest)
	if d.RemoteID != "50" || d.UserID != 123 || len(d.LastSequence) != 2 {
		t.Fatal(d)
	}
}
