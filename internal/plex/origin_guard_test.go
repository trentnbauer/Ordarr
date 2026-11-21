package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The server address is administrator-supplied, so every request re-checks that it is a bare
// http(s) origin before anything is sent (code scanning: go/request-forgery).
func TestRequestsRefuseAnythingButABareOrigin(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"MediaContainer":{}}`))
	}))
	defer s.Close()
	c := New()
	var out map[string]any
	if err := c.getLimit(context.Background(), s.URL, "/", "token", "client", &out, nil, 1<<20); err != nil || hits.Load() != 1 {
		t.Fatalf("a bare origin must work: %v (%d hits)", err, hits.Load())
	}
	host := strings.TrimPrefix(s.URL, "http://")
	for _, base := range []string{
		"http://user:secret@" + host,       // credentials
		s.URL + "/library",                 // path
		s.URL + "?x=1",                     // query
		s.URL + "#fragment",                // fragment
		s.URL + "/../",                     // traversal
		"file:///etc/passwd",               // wrong scheme
		"http://" + host + "@evil.example", // userinfo trick
		"",                                 // empty
		"http://[::1",                      // malformed
	} {
		before := hits.Load()
		if err := c.getLimit(context.Background(), base, "/", "token", "client", &out, nil, 1<<20); !errors.Is(err, ErrURL) {
			t.Errorf("get %q: got %v, want ErrURL", base, err)
		}
		if err := c.playlistRequest(context.Background(), "GET", base, "/playlists", "token", "client", nil); !errors.Is(err, ErrURL) {
			t.Errorf("playlist %q: got %v, want ErrURL", base, err)
		}
		if hits.Load() != before {
			t.Errorf("%q: a request was sent", base)
		}
	}
}
