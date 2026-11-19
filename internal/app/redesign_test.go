package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/community"
	"github.com/trentnbauer/Ordarr/internal/store"
	"github.com/trentnbauer/Ordarr/internal/subscription"
)

const tinyOrder = `{"version":1,"id":"tiny","title":"A tiny order","description":"Two films and an episode.","entries":[{"id":"a","kind":"movie","title":"Film A","provider":"tmdb","external_id":"1"},{"id":"b","kind":"episode","title":"Show","provider":"tvdb","external_id":"7","season":1,"episode":1},{"id":"c","kind":"movie","title":"Film C","provider":"tmdb","external_id":"3"}]}`

// fakeSources serves a community repository and raw URLs without a network.
func fakeSources(t *testing.T, a *App, files map[string]string) {
	t.Helper()
	tree := `{"tree":[`
	first := true
	for p := range files {
		if !first {
			tree += ","
		}
		first = false
		tree += `{"path":"` + p + `","type":"blob"}`
	}
	tree += `]}`
	c := community.New(community.DefaultRepository, "main")
	c.Fetch = func(_ context.Context, u string) ([]byte, error) {
		if strings.HasPrefix(u, c.API) {
			return []byte(tree), nil
		}
		for p, body := range files {
			if u == c.FileURL(p) {
				return []byte(body), nil
			}
		}
		return nil, errors.New("not found")
	}
	a.community = c
	previous := fetchURL
	fetchURL = func(ctx context.Context, raw, etag, modified string) (subscription.Result, error) {
		for p, body := range files {
			if raw == c.FileURL(p) || raw == "https://example.com/"+p {
				return subscription.Result{Data: []byte(body), ETag: `"v1"`}, nil
			}
		}
		return subscription.Result{}, subscription.ErrFetch
	}
	t.Cleanup(func() { fetchURL = previous })
}

func TestAddCollectionFromCommunityPasteAndURL(t *testing.T) {
	a, h := fixture(t)
	mcu, err := os.ReadFile("../community/testdata/mcu-main-timeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	fakeSources(t, a, map[string]string{"tiny.json": tinyOrder, "mcu-main-timeline.yaml": string(mcu)})
	session, csrf := setup(t, h)
	get := func(path string) string {
		t.Helper()
		w := request(h, "GET", path, nil, session, csrf)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	post := func(path string, v url.Values, want int) *http.Response {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want {
			t.Fatalf("POST %s: %d want %d %s", path, w.Code, want, w.Body.String())
		}
		return w.Result()
	}
	// The modal opens on the community repository with the first order picked.
	body := get("/add")
	if !strings.Contains(body, "Search 2 community watch orders") || !strings.Contains(body, "A tiny order") || !strings.Contains(body, "Marvel Cinematic Universe") {
		t.Fatal("community list missing", body)
	}
	if body = get("/add?source=community&filter=movies"); strings.Contains(body, "Marvel Cinematic Universe") || !strings.Contains(body, "Movies only") {
		t.Fatal("movies-only filter")
	}
	if body = get("/add?source=community&q=marvel"); strings.Contains(body, "A tiny order") {
		t.Fatal("search did not filter")
	}
	// One action creates the followed collection.
	res := post("/add", url.Values{"source": {"community"}, "path": {"tiny.json"}, "policy": {"approval"}, "server": {""}}, 303)
	location := res.Header.Get("Location")
	if !strings.HasSuffix(location, "?created=1") {
		t.Fatal(location)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(location, "/collections/"), "?created=1")
	c, err := a.store.Collection(context.Background(), id)
	if err != nil || c.Title != "A tiny order" || len(c.Entries) != 3 {
		t.Fatalf("collection %+v %v", c, err)
	}
	sub, err := a.store.CollectionSubscription(context.Background(), id)
	if err != nil || sub.Policy != "approval" || len(sub.LastValid) == 0 || sub.URL != a.community.FileURL("tiny.json") || sub.ETag != `"v1"` {
		t.Fatalf("subscription %+v %v", sub, err)
	}
	if body = get(location); !strings.Contains(body, "Collection added.") || !strings.Contains(body, "Follows tiny.json") {
		t.Fatal("created notice", body)
	}
	if body = get("/add?source=community&pick=tiny.json"); !strings.Contains(body, "Already added") {
		t.Fatal("already added marker")
	}
	// An unchanged upstream file is not proposed as an update.
	if err = a.fetchSource(context.Background(), store.Job{Target: sub.ID}); err != nil {
		t.Fatal(err)
	}
	if sub, _ = a.store.CollectionSubscription(context.Background(), id); len(sub.Pending) != 0 {
		t.Fatal("unchanged source proposed as an update")
	}
	// A server must be account-linked.
	post("/add", url.Values{"source": {"community"}, "path": {"tiny.json"}, "policy": {"none"}, "server": {"nope"}}, 400)
	// Paste creates an independent collection; invalid YAML re-renders the modal.
	post("/add", url.Values{"source": {"paste"}, "document": {tinyOrder}}, 303)
	res = post("/add", url.Values{"source": {"paste"}, "document": {"version: 2"}}, 400)
	_ = res
	// URL preview reads the file before anything is created.
	if body = get("/add?source=url&url=" + url.QueryEscape("https://example.com/tiny.json")); !strings.Contains(body, "Found") || !strings.Contains(body, "3 entries") {
		t.Fatal("url preview", body)
	}
	if body = get("/add?source=url&url=" + url.QueryEscape("https://localhost/x.yaml")); !strings.Contains(body, `role="alert"`) {
		t.Fatal("unsafe URL accepted")
	}
	post("/add", url.Values{"source": {"url"}, "url": {"https://example.com/tiny.json"}, "policy": {"automatic"}}, 303)
	// Start empty.
	if w := request(h, "GET", "/collections/new", nil, session, csrf); w.Code != 303 || w.Header().Get("Location") != "/add?source=empty" {
		t.Fatal("new collection redirect")
	}
	if body = get("/add?source=empty"); !strings.Contains(body, "Create collection") {
		t.Fatal("start empty")
	}
	if body = get("/"); !strings.Contains(body, "3 collections") {
		t.Fatal("collection count", body)
	}
}

func TestSourceSkipAndUnfollow(t *testing.T) {
	a, h := fixture(t)
	fakeSources(t, a, map[string]string{"tiny.json": tinyOrder})
	session, csrf := setup(t, h)
	w := request(h, "POST", "/add", url.Values{"csrf": {csrf.Value}, "source": {"url"}, "url": {"https://example.com/tiny.json"}, "policy": {"approval"}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/collections/"), "?created=1")
	changed := strings.Replace(tinyOrder, `"Film C"`, `"Film C (fixed)"`, 1)
	fakeSources(t, a, map[string]string{"tiny.json": changed})
	sub, _ := a.store.CollectionSubscription(context.Background(), id)
	if err := a.fetchSource(context.Background(), store.Job{Target: sub.ID}); err != nil {
		t.Fatal(err)
	}
	sub, _ = a.store.CollectionSubscription(context.Background(), id)
	if len(sub.Pending) == 0 {
		t.Fatal("update not pending")
	}
	page := request(h, "GET", "/collections/"+id+"?tab=source", nil, session, csrf).Body.String()
	if !strings.Contains(page, "Apply update") || !strings.Contains(page, "Film C (fixed)") || !strings.Contains(page, "Changed") {
		t.Fatal("source tab", page)
	}
	if !strings.Contains(request(h, "GET", "/jobs", nil, session, csrf).Body.String(), "A tiny order has a source update") {
		t.Fatal("activity waiting item")
	}
	sv := sourceViewFor(t, a, id)
	w = request(h, "POST", "/collections/"+id+"/source", url.Values{"csrf": {csrf.Value}, "action": {"skip"}, "revision": {sv[0]}, "pending_hash": {sv[1]}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	sub, _ = a.store.CollectionSubscription(context.Background(), id)
	if len(sub.Pending) != 0 || sub.SkippedHash == "" {
		t.Fatal("skip did not clear pending")
	}
	// The skipped version is not proposed again.
	if err := a.fetchSource(context.Background(), store.Job{Target: sub.ID}); err != nil {
		t.Fatal(err)
	}
	if sub, _ = a.store.CollectionSubscription(context.Background(), id); len(sub.Pending) != 0 {
		t.Fatal("skipped version proposed again")
	}
	c, _ := a.store.Collection(context.Background(), id)
	if c.Entries[2].Title != "Film C" {
		t.Fatal("skipped update was applied")
	}
	w = request(h, "POST", "/collections/"+id+"/source", url.Values{"csrf": {csrf.Value}, "action": {"unfollow"}, "revision": {"1"}}, session, csrf)
	if w.Code != 409 {
		t.Fatal("stale unfollow accepted", w.Code)
	}
	w = request(h, "POST", "/collections/"+id+"/source", url.Values{"csrf": {csrf.Value}, "action": {"unfollow"}, "revision": {itoa(sub.Revision)}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := a.store.CollectionSubscription(context.Background(), id); err == nil {
		t.Fatal("still subscribed")
	}
	if c, err := a.store.Collection(context.Background(), id); err != nil || len(c.Entries) != 3 {
		t.Fatal("collection lost on unfollow")
	}
	// The collection is now editable: repeat an entry right after the selection.
	c, _ = a.store.Collection(context.Background(), id)
	w = request(h, "POST", "/collections/"+id, url.Values{"csrf": {csrf.Value}, "revision": {itoa(c.Revision)}, "action": {"repeat"}, "entry_id": {c.Entries[0].ID}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	c, _ = a.store.Collection(context.Background(), id)
	if len(c.Entries) != 4 || c.Entries[1].ExternalID != c.Entries[0].ExternalID || c.Entries[1].ID == c.Entries[0].ID {
		t.Fatalf("repeat %+v", c.Entries)
	}
}

func sourceViewFor(t *testing.T, a *App, id string) [2]string {
	t.Helper()
	sub, err := a.store.CollectionSubscription(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	v := sourceView{}
	_ = v
	return [2]string{itoa(sub.Revision), hashOf(sub.Pending)}
}

func TestAccountSettingsAndDefaults(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	post := func(path string, v url.Values, cookies ...*http.Cookie) int {
		v.Set("csrf", csrf.Value)
		return request(h, "POST", path, v, append(cookies, csrf)...).Code
	}
	if code := post("/settings/password", url.Values{"current": {"wrong-password-here"}, "password": {"a-new-long-password"}, "confirm": {"a-new-long-password"}}, session); code != 400 {
		t.Fatal("wrong current password accepted", code)
	}
	if code := post("/settings/password", url.Values{"current": {"long-test-password"}, "password": {"short"}, "confirm": {"short"}}, session); code != 400 {
		t.Fatal("short password accepted", code)
	}
	// A second browser is signed out when the password changes.
	other := request(h, "POST", "/login", url.Values{"csrf": {csrf.Value}, "username": {"admin"}, "password": {"long-test-password"}}, csrf)
	otherSession := cookie(t, other, sessionCookie)
	if code := post("/settings/password", url.Values{"current": {"long-test-password"}, "password": {"a-new-long-password"}, "confirm": {"a-new-long-password"}}, session); code != 303 {
		t.Fatal("password change failed", code)
	}
	if w := request(h, "GET", "/settings?result=password", nil, session, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), "Password changed") {
		t.Fatal("current session lost", w.Code)
	}
	if w := request(h, "GET", "/", nil, otherSession, csrf); w.Code != 303 {
		t.Fatal("other session survived a password change")
	}
	if code := post("/settings/defaults", url.Values{"connection_id": {"missing"}, "policy": {"automatic"}, "interval": {"60"}}, session); code != 400 {
		t.Fatal("unknown default server accepted", code)
	}
	if code := post("/settings/defaults", url.Values{"connection_id": {""}, "policy": {"automatic"}, "interval": {"1440"}}, session); code != 303 {
		t.Fatal("defaults not saved", code)
	}
	if d, err := a.store.Defaults(context.Background()); err != nil || d.SourcePolicy != "automatic" || d.SyncInterval != 1440 {
		t.Fatal("defaults", d, err)
	}
	if code := post("/settings/sessions/revoke", url.Values{}, session); code != 303 {
		t.Fatal("sign out everywhere", code)
	}
	if w := request(h, "GET", "/", nil, session, csrf); w.Code != 303 {
		t.Fatal("session survived sign out everywhere")
	}
}

func TestShellPagesRender(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	for _, path := range []string{"/", "/missing", "/jobs", "/settings", "/settings/plex", "/settings/jellyfin", "/settings/acquisition", "/add?source=paste", "/add?source=upload", "/add?source=url", "/import"} {
		w := request(h, "GET", path, nil, session, csrf)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `class="sidebar"`) {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	// Without a community repository the modal opens on URL.
	if w := request(h, "GET", "/add", nil, session, csrf); !strings.Contains(w.Body.String(), `href="/add?source=url" aria-current="page"`) {
		t.Fatal("default source without catalogue")
	}
}

func itoa(n int) string      { return strconv.Itoa(n) }
func hashOf(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
