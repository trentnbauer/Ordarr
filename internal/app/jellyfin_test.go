package app

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const jfUser = "11111111111111111111111111111111"
const jfLibrary = "22222222222222222222222222222222"
const jfMovie = "33333333333333333333333333333333"
const jfEpisode = "44444444444444444444444444444444"
const jfSeries = "55555555555555555555555555555555"

func TestJellyfinConnectionScanMatchingFailureAndLocalRemoval(t *testing.T) {
	a, h := fixture(t)
	var fail atomic.Bool
	var changed atomic.Bool
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" {
			t.Error("unexpected remote write")
		}
		if r.Header.Get("Authorization") != `MediaBrowser Client="Ordarr", Device="Ordarr", DeviceId="ordarr", Version="0.8.0-dev", Token="jf-user-token"` {
			t.Error("missing scoped auth")
		}
		if fail.Load() {
			http.Error(w, "jf-user-token private response", 503)
			return
		}
		var body any
		switch r.URL.Path {
		case "/System/Info/Public":
			body = map[string]any{"Id": "server", "ServerName": "Home Jellyfin", "ProductName": "Jellyfin", "Version": "10.11"}
		case "/Users/Me":
			id := jfUser
			if changed.Load() {
				id = jfMovie
			}
			body = map[string]any{"Id": id, "Name": "Connected user", "Policy": map[string]any{"IsDisabled": false, "EnableMediaPlayback": true}}
		case "/UserViews":
			body = map[string]any{"Items": []any{map[string]any{"Id": jfLibrary, "Name": "Videos", "CollectionType": "unknown"}}, "TotalRecordCount": 1, "StartIndex": 0}
		case "/Items":
			body = map[string]any{"Items": []any{map[string]any{"Id": jfMovie, "Name": "Movie", "Type": "Movie", "ProviderIds": map[string]string{"Tmdb": "1"}, "Path": "/private/movie"}, map[string]any{"Id": jfEpisode, "Name": "Episode", "Type": "Episode", "SeriesId": jfSeries, "ParentIndexNumber": 1, "IndexNumber": 1}}, "TotalRecordCount": 2, "StartIndex": 0}
		case "/Items/" + jfSeries:
			body = map[string]any{"Id": jfSeries, "Name": "Series", "Type": "Series", "ProviderIds": map[string]string{"Tvdb": "7"}}
		default:
			t.Error("unknown path", r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer remote.Close()
	session, csrf := setup(t, h)
	post := func(path string, v url.Values, want int) string {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want || strings.Contains(w.Body.String(), "jf-user-token") {
			t.Fatal(path, w.Code, w.Body.String())
		}
		return w.Header().Get("Location")
	}
	post("/settings/jellyfin", url.Values{"name": {"Home"}, "url": {remote.URL}, "token": {"jf-user-token"}}, 400)
	if calls.Load() != 0 {
		t.Fatal("HTTP contacted before consent")
	}
	post("/settings/jellyfin", url.Values{"name": {"Home"}, "url": {remote.URL}, "token": {"jf-user-token"}, "allow_http": {"yes"}}, 303)
	ctx := context.Background()
	conns, err := a.store.JellyfinConnections(ctx)
	if err != nil || len(conns) != 1 {
		t.Fatal(conns, err)
	}
	conn := conns[0]
	if strings.Contains(string(conn.TokenCipher), "jf-user-token") {
		t.Fatal("token stored as plaintext")
	}
	if err = a.scanJellyfin(ctx, store.Job{ID: "scan", Target: conn.ID}); err != nil {
		t.Fatal(err)
	}
	items, _, err := a.store.JellyfinCatalogue(ctx, conn.ID)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	payload, _ := json.Marshal(items)
	if strings.Contains(string(payload), "private") {
		t.Fatal("stored server file path")
	}
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Mixed Jellyfin order", Entries: []store.Entry{{ID: "a", Title: "Movie", Kind: "movie", Provider: "tmdb", ExternalID: "1"}, {ID: "b", Title: "Episode", Kind: "episode", Provider: "tvdb", ExternalID: "7", Season: 1, Episode: 1}, {ID: "c", Title: "Missing", Kind: "movie", Provider: "tmdb", ExternalID: "99"}, {ID: "d", Title: "Repeat", Kind: "movie", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	c, _ := a.store.Collection(ctx, id)
	path := "/collections/" + id + "/jellyfin/" + conn.ID
	w := request(h, "GET", path, nil, session, csrf)
	if w.Code != 200 || strings.Count(w.Body.String(), `class="badge">matched`) != 3 || !strings.Contains(w.Body.String(), "Playback has not been verified") {
		t.Fatal(w.Code, w.Body.String())
	}
	post(path, url.Values{"revision": {strconv.Itoa(c.Revision)}, "entry_id": {c.Entries[0].ID}, "item_id": {jfEpisode}}, 400)
	post(path, url.Values{"revision": {strconv.Itoa(c.Revision)}, "entry_id": {c.Entries[2].ID}, "item_id": {jfMovie}}, 303)
	overrides, _ := a.store.JellyfinManualMatches(ctx, conn.ID, c)
	if overrides[c.Entries[2].ID] != jfMovie {
		t.Fatal(overrides)
	}
	fail.Store(true)
	if err = a.scanJellyfin(ctx, store.Job{ID: "failed", Target: conn.ID}); err == nil {
		t.Fatal("failed scan reported success")
	}
	items, _, err = a.store.JellyfinCatalogue(ctx, conn.ID)
	if err != nil || len(items) != 2 {
		t.Fatal("last good catalogue lost", err)
	}
	fail.Store(false)
	changed.Store(true)
	post("/settings/jellyfin/"+conn.ID+"/check", url.Values{"revision": {"1"}}, 502)
	current, _ := a.store.JellyfinConnection(ctx, conn.ID)
	if current.Snapshot.UserID != jfUser {
		t.Fatal("silently changed user")
	}
	changed.Store(false)
	// Jellyfin credentials alone must prevent silently replacing a lost vault key.
	if err = os.Remove(filepath.Join(a.config.DataDir, "credentials.key")); err != nil {
		t.Fatal(err)
	}
	if _, err = New(a.store, a.config, a.logger); err == nil {
		t.Fatal("lost credential key replaced")
	}
	before := calls.Load()
	post("/settings/jellyfin/"+conn.ID+"/delete", url.Values{"revision": {"1"}, "confirm": {conn.ID}}, 303)
	if calls.Load() != before {
		t.Fatal("local removal modified server")
	}
	remaining, _ := a.store.Collection(ctx, id)
	if len(remaining.Entries) != 4 {
		t.Fatal("canonical entries removed")
	}
	conns, _ = a.store.JellyfinConnections(ctx)
	if len(conns) != 0 {
		t.Fatal("connection not removed")
	}
}
