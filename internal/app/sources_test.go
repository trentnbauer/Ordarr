package app

import (
	"net/url"
	"strings"
	"testing"
)

func TestImportExportAndSourcePages(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	raw := `{"version":1,"title":"Imported","entries":[{"id":"a","kind":"movie","title":"Movie","provider":"tmdb","external_id":"1"},{"id":"repeat","kind":"movie","title":"Movie","provider":"tmdb","external_id":"1"}]}`
	w := request(h, "POST", "/import", url.Values{"csrf": {csrf.Value}, "document": {raw}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	for _, url := range []string{path, path + "/export?format=json", "/import", "/jobs"} {
		w = request(h, "GET", url, nil, session, csrf)
		if w.Code != 200 {
			t.Fatal(url, w.Code, w.Body.String())
		}
	}
	w = request(h, "GET", path+"/export?format=json", nil, session, csrf)
	if strings.Count(w.Body.String(), `"external_id": "1"`) != 2 {
		t.Fatal("repeats lost", w.Body.String())
	}
	w = request(h, "POST", "/subscriptions", url.Values{"csrf": {csrf.Value}, "url": {"https://127.0.0.1/private"}, "policy": {"automatic"}}, session, csrf)
	if w.Code != 400 {
		t.Fatal("private source accepted")
	}
}
