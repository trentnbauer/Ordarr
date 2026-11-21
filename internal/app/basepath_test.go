package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBasePath(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "ordarr": "/ordarr", "/ordarr/": "/ordarr", "https://x.tv/a/b": "/a/b"} {
		if got, err := normalizeBasePath(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	if _, err := normalizeBasePath("/a b"); err == nil {
		t.Error("expected error")
	}
}

func TestWithBasePath(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<a href="/x?a=1">x</a><form action="/y"></form><a href="//cdn/z">z</a><a href="https://e.com/">e</a>`))
	})
	inner.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/page", 303) })
	inner.HandleFunc("/app.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.Write([]byte(`a{src:url(/static/f.woff2)}`))
	})
	inner.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	h := withBasePath("/ordarr", inner)
	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		return w
	}
	if b := get("/ordarr/page").Body.String(); !strings.Contains(b, `href="/ordarr/x?a=1"`) || !strings.Contains(b, `action="/ordarr/y"`) || !strings.Contains(b, `href="//cdn/z"`) || !strings.Contains(b, `href="https://e.com/"`) {
		t.Errorf("html: %s", b)
	}
	if l := get("/ordarr/go").Header().Get("Location"); l != "/ordarr/page" {
		t.Errorf("location %q", l)
	}
	if b := get("/ordarr/app.css").Body.String(); b != `a{src:url(/ordarr/static/f.woff2)}` {
		t.Errorf("css %s", b)
	}
	if get("/page").Code != 404 || get("/ordarr").Code != 308 || get("/healthz").Body.String() != "ok" {
		t.Error("routing")
	}
}
