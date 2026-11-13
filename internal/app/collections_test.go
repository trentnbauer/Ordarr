package app

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

func TestCollectionHTTPWorkflow(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	post := func(path string, v url.Values, want int) string {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want {
			t.Fatalf("POST %s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Header().Get("Location")
	}
	path := post("/collections", url.Values{"title": {"<script>Watch order</script>"}, "description": {"Description"}}, 303)
	id := strings.TrimPrefix(path, "/collections/")
	rev := func() string {
		c, err := a.store.Collection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return strconv.Itoa(c.Revision)
	}
	form := url.Values{"action": {"add"}, "revision": {rev()}, "kind": {"movie"}, "entry_title": {"Movie"}, "provider": {"tmdb"}, "external_id": {"1771"}}
	post(path, form, 303)
	form.Set("revision", rev())
	post(path, form, 303)
	w := request(h, "GET", path, nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;script&gt;Watch order&lt;/script&gt;") || strings.Count(w.Body.String(), "data-entry-id=") != 2 {
		t.Fatal("editor failed", w.Code, w.Body.String())
	}
	c, err := a.store.Collection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// Stale writes must not remove a repeated entry.
	post(path, url.Values{"action": {"remove"}, "revision": {"1"}, "entry_id": {c.Entries[0].ID}, "confirm_remove": {"yes"}}, 409)
	post(path, url.Values{"action": {"remove"}, "revision": {rev()}, "entry_id": {c.Entries[0].ID}}, 400)
	post(path, url.Values{"action": {"move"}, "revision": {rev()}, "entry_id": {c.Entries[1].ID}, "target": {"1"}}, 303)
	edit := url.Values{"action": {"edit"}, "revision": {rev()}, "entry_id": {c.Entries[1].ID}, "kind": {"episode"}, "entry_title": {"Series"}, "provider": {"tvdb"}, "external_id": {"42"}, "season": {"0"}, "start": {"2"}}
	post(path, edit, 303)
	w = request(h, "GET", path+"/entries/"+c.Entries[1].ID+"/edit", nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Save entry") {
		t.Fatal("edit form failed", w.Code)
	}
	edit.Set("revision", rev())
	edit.Set("start", "invalid")
	post(path, edit, 400)
	post(path, url.Values{"action": {"reorder"}, "revision": {rev()}, "order": {c.Entries[0].ID + " " + c.Entries[1].ID}}, 303)
	clone := post(path, url.Values{"action": {"fork"}, "revision": {rev()}}, 303)
	post(path, url.Values{"action": {"delete"}, "revision": {rev()}, "confirm_title": {"wrong"}}, 400)
	post(path, url.Values{"action": {"delete"}, "revision": {rev()}, "confirm_title": {"<script>Watch order</script>"}}, 303)
	if w = request(h, "GET", clone, nil, session, csrf); w.Code != 200 {
		t.Fatal("source delete removed copy")
	}
	if w = request(h, "GET", path, nil, session, csrf); w.Code != 404 {
		t.Fatal("deleted collection still reachable", w.Code)
	}
}
func TestCollectionRoutesRequireAuthenticationAndCSRF(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	w := request(h, "POST", "/collections", url.Values{"title": {"Unauthorised"}, "csrf": {csrf.Value}}, csrf)
	if w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal("unauthenticated creation", w.Code)
	}
	w = request(h, "POST", "/collections", url.Values{"title": {"No CSRF"}}, session)
	if w.Code != 403 {
		t.Fatal("creation without CSRF", w.Code)
	}
	for _, path := range []string{"/collections/new", "/collections/unknown", "/collections/unknown/entries/unknown/edit"} {
		w = request(h, "GET", path, nil)
		if w.Code != 303 {
			t.Fatal("unprotected collection route", path, w.Code)
		}
	}
	for _, path := range []string{"/?filter=bad", "/?offset=-1", "/?offset=nope"} {
		w = request(h, "GET", path, nil, session, csrf)
		if w.Code != 400 {
			t.Fatal("bad query accepted", path, w.Code)
		}
	}
	w = request(h, "GET", "/static/editor.js", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "requestSubmit") {
		t.Fatal("editor enhancement missing")
	}
}
func TestCollectionListSearchAndPagination(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	for i := 0; i < 21; i++ {
		if _, err := a.store.CreateCollection(context.Background(), "Matching collection", ""); err != nil {
			t.Fatal(err)
		}
	}
	w := request(h, "GET", "/?q=Matching", nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Next page") {
		t.Fatal("pagination link missing", w.Code)
	}
	w = request(h, "GET", "/?q=Matching&offset="+strconv.Itoa(store.PageSize), nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Previous page") {
		t.Fatal("second page missing", w.Code)
	}
	w = request(h, "GET", "/?q=absent", nil, session, csrf)
	if !strings.Contains(w.Body.String(), "No collections found") {
		t.Fatal("empty search failed")
	}
}
