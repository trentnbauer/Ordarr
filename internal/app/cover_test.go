package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

var coverPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 128)...)

// A collection's cover is downloaded once, served from Ordarr itself, shown on the page, and a
// dead link is not retried on every page view.
func TestCoverIsDownloadedServedAndNotRetriedWhenBroken(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	calls := 0
	var failing bool
	previous := fetchImage
	fetchImage = func(ctx context.Context, raw string) ([]byte, string, error) {
		calls++
		if failing {
			return nil, "", errors.New("down")
		}
		return coverPNG, "image/png", nil
	}
	t.Cleanup(func() { fetchImage = previous })
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "T", Cover: "https://img.example/c.png", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	queued := func() int {
		var n int
		a.store.DB.QueryRow("SELECT count(*) FROM jobs WHERE kind='cover' AND target=? AND state='queued'", id).Scan(&n)
		return n
	}
	// First view: nothing downloaded yet, a job is queued, no image on the page.
	if body := request(h, "GET", "/collections/"+id, nil, session, csrf).Body.String(); strings.Contains(body, `class="cover"`) || queued() != 1 {
		t.Fatalf("first view: queued=%d", queued())
	}
	if w := request(h, "GET", "/collections/"+id+"/cover", nil, session, csrf); w.Code != 404 {
		t.Fatal("served a cover that was never downloaded", w.Code)
	}
	if err = a.fetchCover(ctx, store.Job{Target: id}); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	body := request(h, "GET", "/collections/"+id, nil, session, csrf).Body.String()
	if !strings.Contains(body, `<img class="cover" src="/collections/`+id+`/cover"`) {
		t.Fatal("cover not shown on the collection page")
	}
	w := request(h, "GET", "/collections/"+id+"/cover", nil, session, csrf)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Header().Get("Cache-Control"), "private") || w.Body.Len() != len(coverPNG) {
		t.Fatalf("cover response %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	// Unchanged cover: no second download, and the browser's copy is revalidated cheaply.
	if err = a.fetchCover(ctx, store.Job{Target: id}); err != nil || calls != 1 {
		t.Fatal("re-downloaded an unchanged cover", calls)
	}
	r2 := request(h, "GET", "/collections/"+id+"/cover", nil, session, csrf)
	if etag := r2.Header().Get("ETag"); etag == "" {
		t.Fatal("no ETag")
	}
	// A cover that cannot be fetched is not queued again on every page view.
	a.store.DB.Exec("UPDATE collections SET cover=? WHERE id=?", "https://img.example/broken.png", id)
	a.store.DB.Exec("DELETE FROM jobs")
	failing = true
	request(h, "GET", "/collections/"+id, nil, session, csrf)
	if queued() != 1 {
		t.Fatal("the changed cover was not queued")
	}
	if err = a.fetchCover(ctx, store.Job{Target: id}); err == nil {
		t.Fatal("a failed download was reported as success")
	}
	a.store.DB.Exec("DELETE FROM jobs")
	request(h, "GET", "/collections/"+id, nil, session, csrf)
	if queued() != 0 {
		t.Fatal("a broken cover link was retried on the next page view")
	}
	// Removing the cover removes the downloaded copy.
	a.store.DB.Exec("UPDATE collections SET cover='' WHERE id=?", id)
	if err = a.fetchCover(ctx, store.Job{Target: id}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.store.CoverFor(ctx, id); ok {
		t.Fatal("cover kept after it was removed from the collection")
	}
}

// A synced playlist gets a clean title, the Ordarr ID in its description and the collection's
// cover as its poster; a failure of either never fails the sync.
func TestSyncMovesTheMarkerIntoTheDescriptionAndSetsThePoster(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	f.pms.Close()
	var mu sync.Mutex
	var title, summary string
	var created bool
	var keys []string
	var posters int
	var posterBody []byte
	var puts []string
	item := func(key string) map[string]any {
		return map[string]any{"ratingKey": key, "type": "movie", "title": "Movie", "Guid": []any{map[string]any{"id": "tmdb://" + key}}, "Media": []any{map[string]any{"Part": []any{map[string]any{"key": "/library/parts/1/file", "exists": true}}}}}
	}
	playlist := func() map[string]any {
		return map[string]any{"ratingKey": "50", "title": title, "summary": summary, "playlistType": "video", "smart": false}
	}
	f.pms = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := r.URL.Path
		container := map[string]any{}
		switch {
		case path == "/":
			container = map[string]any{"machineIdentifier": "resource-machine", "friendlyName": "Plex", "version": "1"}
		case path == "/library/sections/all":
			container = map[string]any{"size": 1, "Directory": []any{map[string]any{"key": "1", "title": "Movies", "type": "movie"}}}
		case path == "/library/sections/1/all":
			container = map[string]any{"size": 1, "offset": 0, "totalSize": 1, "Metadata": []any{map[string]any{"ratingKey": "1", "type": "movie", "title": "Movie"}}}
		case strings.HasPrefix(path, "/library/metadata/") && !strings.HasSuffix(path, "/posters"):
			container = map[string]any{"Metadata": []any{item(strings.TrimPrefix(path, "/library/metadata/"))}}
		case path == "/library/metadata/50/posters" && r.Method == "POST":
			posters++
			posterBody, _ = io.ReadAll(r.Body)
		case path == "/playlists" && r.Method == "GET":
			items := []any{}
			if created {
				items = append(items, playlist())
			}
			container = map[string]any{"size": len(items), "Metadata": items}
		case path == "/playlists" && r.Method == "POST":
			created, title = true, r.URL.Query().Get("title")
			uri := r.URL.Query().Get("uri")
			keys = strings.Split(uri[strings.LastIndex(uri, "/")+1:], ",")
			container = map[string]any{"Metadata": []any{playlist()}}
		case path == "/playlists/50" && r.Method == "PUT":
			puts = append(puts, r.URL.RawQuery)
			if v := r.URL.Query().Get("summary"); v != "" {
				summary = v
			}
			if v := r.URL.Query().Get("title"); v != "" {
				title = v
			}
		case path == "/playlists/50":
			container = map[string]any{"Metadata": []any{playlist()}}
		case path == "/playlists/50/items" && r.Method == "GET":
			items := []any{}
			for i, key := range keys {
				items = append(items, map[string]any{"ratingKey": key, "playlistItemID": i + 100})
			}
			container = map[string]any{"size": len(items), "Metadata": items}
		default:
			t.Errorf("unexpected request %s %s", r.Method, path)
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": container})
	}))
	defer f.pms.Close()
	previous := fetchImage
	fetchImage = func(context.Context, string) ([]byte, string, error) { return coverPNG, "image/png", nil }
	t.Cleanup(func() { fetchImage = previous })
	session, csrf := setup(t, h)
	post := func(path string, v url.Values) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		if w := request(h, "POST", path, v, session, csrf); w.Code != 303 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", "/settings/plex/account/start", url.Values{"csrf": {csrf.Value}}, session, csrf)
	f.granted.Store(true)
	post(w.Header().Get("Location"), url.Values{})
	account, _ := a.store.PlexAccount(context.Background())
	post("/settings/plex/account/connect", url.Values{"account_revision": {strconv.Itoa(account.Revision)}, "resource_id": {"resource-machine"}, "uri": {f.pms.URL}, "allow_http": {"yes"}})
	ctx := context.Background()
	connections, _ := a.store.PlexConnectionIDs(ctx)
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Description: "Watch these in order.", Cover: "https://img.example/c.png", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "Movie", Provider: "tmdb", ExternalID: "1"}}})
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
	marker := "[Ordarr:" + dest + "]"
	if title != "Order" || !strings.Contains(summary, marker) || !strings.HasPrefix(summary, "Watch these in order.") {
		t.Fatalf("title %q summary %q: the ID must be in the description, not the title", title, summary)
	}
	if posters != 1 || string(posterBody) != string(coverPNG) {
		t.Fatalf("poster uploads: %d", posters)
	}
	// A second sync changes nothing and does not upload the poster again.
	before := len(puts)
	if err = a.syncDestination(ctx, store.Job{ID: "second", Target: dest}); err != nil {
		t.Fatal(err)
	}
	if posters != 1 || len(puts) != before {
		t.Fatalf("second sync re-wrote the playlist: posters=%d puts=%v", posters, puts[before:])
	}
	// A changed cover is uploaded; an unreachable cover never fails the sync.
	a.store.DB.Exec("UPDATE collections SET cover=? WHERE id=?", "https://img.example/new.png", id)
	fetchImage = func(context.Context, string) ([]byte, string, error) {
		return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 200)...), "image/png", nil
	}
	if err = a.syncDestination(ctx, store.Job{ID: "third", Target: dest}); err != nil || posters != 2 {
		t.Fatalf("changed cover: %v posters=%d", err, posters)
	}
	a.store.DB.Exec("UPDATE collections SET cover=? WHERE id=?", "https://img.example/gone.png", id)
	fetchImage = func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("down") }
	if err = a.syncDestination(ctx, store.Job{ID: "fourth", Target: dest}); err != nil {
		t.Fatal("an unreachable cover failed the sync:", err)
	}
}
