package plex

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "fixture-opaque-plex-token"

func root(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"server-machine-1","friendlyName":"Fixture Plex","version":"1.43.4"}}`)
}
func TestInspectPaginatedAndLegacyLibraries(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "documented", true: "legacy"}[legacy], func(t *testing.T) {
			var pages atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("write request sent")
				}
				if r.Header.Get("X-Plex-Token") != testToken || r.Header.Get("X-Plex-Client-Identifier") != "client-1" || r.Header.Get("Accept") != "application/json" {
					t.Error("missing auth/identity/JSON headers")
				}
				if strings.Contains(r.URL.String(), testToken) {
					t.Error("token leaked into URL")
				}
				if r.URL.Path == "/" {
					root(w)
					return
				}
				if legacy && r.URL.Path == "/library/sections/all" {
					http.NotFound(w, r)
					return
				}
				expected := "/library/sections/all"
				if legacy {
					expected = "/library/sections"
				}
				if r.URL.Path != expected {
					t.Error("unexpected endpoint", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				offset := r.Header.Get("X-Plex-Container-Start")
				if offset != r.URL.Query().Get("X-Plex-Container-Start") || r.Header.Get("X-Plex-Container-Size") != "100" {
					t.Error("pagination headers missing")
				}
				pages.Add(1)
				if offset == "0" {
					io.WriteString(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":2,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
				} else if offset == "1" {
					w.Header().Set("X-Plex-Container-Start", "1")
					w.Header().Set("X-Plex-Container-Total-Size", "2")
					io.WriteString(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"2","title":"Shows","type":"show"}]}}`)
				} else {
					t.Error("wrong offset", offset)
				}
			}))
			defer server.Close()
			snapshot, err := New().Inspect(context.Background(), server.URL, testToken, "client-1", true)
			if err != nil || len(snapshot.Libraries) != 2 || snapshot.MachineID != "server-machine-1" || pages.Load() != 2 {
				t.Fatal(snapshot, err, pages.Load())
			}
		})
	}
}
func TestInspectRejectsMalformedPartialAndReflectedData(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"missing container", `{}`}, {"missing size", `{"MediaContainer":{"Directory":[]}}`},
		{"truncated pagination", `{"MediaContainer":{"size":0,"offset":0,"totalSize":3,"Directory":[]}}`},
		{"wrong size", `{"MediaContainer":{"size":2,"Directory":[]}}`},
		{"no total for paged response", `{"MediaContainer":{"size":1,"offset":0,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`},
		{"duplicate keys", `{"MediaContainer":{"size":2,"Directory":[{"key":"1","title":"Movies","type":"movie"},{"key":"1","title":"Shows","type":"show"}]}}`},
		{"token reflection", `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"` + testToken + `","type":"movie"}]}}`},
		{"malformed JSON", `{bad}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" {
					root(w)
					return
				}
				io.WriteString(w, test.body)
			}))
			defer s.Close()
			snapshot, err := New().Inspect(context.Background(), s.URL, testToken, "client-1", true)
			if !errors.Is(err, ErrResponse) || len(snapshot.Libraries) != 0 {
				t.Fatalf("accepted invalid snapshot: %#v %v", snapshot, err)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatal("error reflected token")
			}
		})
	}
}
func TestHeadersCannotHidePartialSnapshot(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			root(w)
			return
		}
		w.Header().Set("X-Plex-Container-Start", "0")
		w.Header().Set("X-Plex-Container-Total-Size", "2")
		io.WriteString(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
	}))
	defer s.Close()
	if _, err := New().Inspect(context.Background(), s.URL, testToken, "client-1", true); !errors.Is(err, ErrResponse) {
		t.Fatal("repeated page not rejected", err)
	}
}
func TestAuthenticationRedirectRateLimitRetriesAndTimeout(t *testing.T) {
	for _, test := range []struct {
		status, wantCalls int
		want              error
	}{{401, 1, ErrAuth}, {403, 1, ErrAuth}, {498, 1, ErrAuth}, {429, 1, ErrRateLimit}, {302, 1, ErrRedirect}, {503, 2, ErrUnavailable}, {502, 2, ErrUnavailable}, {500, 1, ErrResponse}} {
		calls := 0
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("Location", "https://untrusted.example/")
			w.WriteHeader(test.status)
			io.WriteString(w, testToken)
		}))
		_, err := New().Inspect(context.Background(), s.URL, testToken, "client-1", true)
		s.Close()
		if !errors.Is(err, test.want) || calls != test.wantCalls {
			t.Fatalf("status %d: %v calls=%d", test.status, err, calls)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatal("error leaked token")
		}
	}
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(503)
	}))
	defer s.Close()
	if _, err := New().Inspect(context.Background(), s.URL, testToken, "client-1", true); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("retried before Retry-After", err, calls)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := New().Inspect(ctx, slow.URL, testToken, "client-1", true); !errors.Is(err, ErrUnavailable) {
		t.Fatal("timeout not bounded", err)
	}
}
func TestTLSVerificationAndResponseLimit(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root(w) }))
	defer s.Close()
	if _, err := New().Inspect(context.Background(), s.URL, testToken, "client-1", false); !errors.Is(err, ErrUnavailable) {
		t.Fatal("untrusted certificate accepted", err)
	}
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat(" ", (1<<20)+2)) }))
	defer big.Close()
	if _, err := New().Inspect(context.Background(), big.URL, testToken, "client-1", true); !errors.Is(err, ErrResponse) {
		t.Fatal("unbounded response accepted", err)
	}
}
func TestURLAndTokenValidation(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.com", "https://example.com/?X-Plex-Token=secret", "https://example.com/path", "http://169.254.169.254", "https://[::ffff:169.254.169.254]", "http://0.0.0.0", "http://[::]", "https://224.0.0.1", "http://proxy.", "http://metadata.google.internal", "https://[fd00:ec2::254]", "https://example.com:99999", "https://example.com:0", "https://example.com/#fragment"} {
		if _, err := NormalizeURL(raw, true); err == nil {
			t.Fatal("unsafe URL accepted", raw)
		}
	}
	for _, raw := range []string{"https://plex.example:32400/", "http://192.168.1.10:32400", "http://127.0.0.1:32400", "http://[::1]:32400"} {
		if _, err := NormalizeURL(raw, true); err != nil {
			t.Fatal(raw, err)
		}
	}
	if _, err := NormalizeURL("http://localhost:32400", false); !errors.Is(err, ErrHTTPConsent) {
		t.Fatal("no HTTP consent", err)
	}
	for _, token := range []string{"", "token\r\nHeader: injected", "token with space", strings.Repeat("a", 4097)} {
		if ValidToken(token) {
			t.Fatal("invalid token accepted")
		}
	}

}
