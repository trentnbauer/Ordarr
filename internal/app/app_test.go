package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func fixture(t *testing.T) (*App, http.Handler) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ordarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	a, err := New(s, Config{Address: "127.0.0.1:8080", DataDir: "test-data"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return a, a.Handler()
}
func request(h http.Handler, method, path string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func cookie(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing cookie %s", name)
	return nil
}
func setup(t *testing.T, h http.Handler) (*http.Cookie, *http.Cookie) {
	t.Helper()
	w := request(h, "GET", "/setup", nil)
	csrf := cookie(t, w, csrfCookie)
	w = request(h, "POST", "/setup", url.Values{"csrf": {csrf.Value}, "username": {"admin"}, "password": {"long-test-password"}}, csrf)
	if w.Code != 303 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	return cookie(t, w, sessionCookie), csrf
}
func TestAuthenticationLifecycle(t *testing.T) {
	a, h := fixture(t)
	if w := request(h, "GET", "/", nil); w.Code != 303 || w.Header().Get("Location") != "/setup" {
		t.Fatal(w.Code, w.Header())
	}
	session, csrf := setup(t, h)
	if !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.MaxAge != 86400 {
		t.Fatal("unsafe session cookie")
	}
	_, hash, err := a.store.Admin(context.Background())
	if err != nil || bcrypt.CompareHashAndPassword(hash, []byte("long-test-password")) != nil {
		t.Fatal("password hash", err)
	}
	for _, path := range []string{"/", "/settings"} {
		w := request(h, "GET", path, nil, session, csrf)
		if w.Code != 200 {
			t.Fatal(path, w.Code)
		}
		if strings.Contains(w.Body.String(), session.Value) || strings.Contains(w.Body.String(), string(hash)) {
			t.Fatal("secret in response")
		}
	}
	if w := request(h, "GET", "/setup", nil); w.Code != 303 {
		t.Fatal("setup still accessible")
	}
	w := request(h, "POST", "/logout", url.Values{"csrf": {csrf.Value}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = request(h, "GET", "/settings", nil, session); w.Code != 303 {
		t.Fatal("logged-out session accepted")
	}
	bad := url.Values{"csrf": {csrf.Value}, "username": {"admin"}, "password": {"wrong"}}
	if w = request(h, "POST", "/login", bad, csrf); w.Code != 401 {
		t.Fatal("bad password accepted", w.Code)
	}
	bad.Set("password", "long-test-password")
	w = request(h, "POST", "/login", bad, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	current := cookie(t, w, sessionCookie)
	if current.Value == session.Value {
		t.Fatal("session reused")
	}
	a.store.DB.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Hour).Unix())
	if w = request(h, "GET", "/", nil, current); w.Code != 303 {
		t.Fatal("expired session accepted")
	}
}
func TestCSRFAndRequestLimits(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	for _, test := range []struct {
		name, token, origin, body string
		want                      int
	}{
		{name: "missing", want: 403}, {name: "wrong", token: strings.Repeat("0", 64), want: 403},
		{name: "cross-origin", token: csrf.Value, origin: "https://evil.example", want: 403},
		{name: "wrong-scheme", token: csrf.Value, origin: "https://example.com", want: 403},
		{name: "oversized", token: csrf.Value, body: strings.Repeat("x", 9000), want: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			form := url.Values{"csrf": {test.token}, "padding": {test.body}}
			r := httptest.NewRequest("POST", "http://example.com/logout", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", test.origin)
			r.AddCookie(csrf)
			r.AddCookie(session)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("got %d want %d", w.Code, test.want)
			}
		})
	}
	if w := request(h, "GET", "/logout", nil, session); w.Code != 405 {
		t.Fatal("GET logout accepted")
	}
	if w := request(h, "GET", "/", nil, session); w.Code != 200 {
		t.Fatal("invalid form logged user out")
	}
}
func TestSetupValidationRateLimitAndEscaping(t *testing.T) {
	a, h := fixture(t)
	w := request(h, "GET", "/setup", nil)
	csrf := cookie(t, w, csrfCookie)
	form := url.Values{"csrf": {csrf.Value}, "username": {"admin"}, "password": {"short"}}
	if w = request(h, "POST", "/setup", form, csrf); w.Code != 400 {
		t.Fatal(w.Code)
	}
	form.Set("username", "<script>alert(1)</script>")
	form.Set("password", "long-test-password")
	w = request(h, "POST", "/setup", form, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	session := cookie(t, w, sessionCookie)
	w = request(h, "GET", "/settings", nil, session)
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("username not escaped")
	}
	form.Set("password", "wrong")
	for i := 0; i < 4; i++ {
		w = request(h, "POST", "/login", form, csrf)
	}
	if w.Code != 429 {
		t.Fatal("rate limit missing", w.Code)
	}
	a.mu.Lock()
	for k, v := range a.attempts {
		v.until = time.Now().Add(-time.Second)
		a.attempts[k] = v
	}
	a.mu.Unlock()
	if w = request(h, "POST", "/login", form, csrf); w.Code != 401 {
		t.Fatal("rate limit did not expire", w.Code)
	}
}
func TestHealthAssetsAndSecureCookies(t *testing.T) {
	a, _ := fixture(t)
	a.config.SecureCookies = true
	h := a.Handler()
	w := request(h, "GET", "/setup", nil)
	if !cookie(t, w, csrfCookie).Secure {
		t.Fatal("secure flag missing")
	}
	for path, text := range map[string]string{"/healthz": "\"status\":\"ok\"", "/static/app.css": "color-scheme"} {
		w = request(h, "GET", path, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), text) {
			t.Fatal(path, w.Code)
		}
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing security headers")
	}
	a.store.DB.Close()
	if w = request(h, "GET", "/healthz", nil); w.Code != 503 {
		t.Fatal("unhealthy database reported ready")
	}
}
func TestConfig(t *testing.T) {
	t.Setenv("ORDARR_ADDR", "127.0.0.1:8088")
	t.Setenv("ORDARR_DATA_DIR", t.TempDir())
	t.Setenv("ORDARR_SECURE_COOKIES", "true")
	c, err := LoadConfig()
	if err != nil || !c.SecureCookies || !filepath.IsAbs(c.DataDir) {
		t.Fatal(c, err)
	}
	t.Setenv("ORDARR_SECURE_COOKIES", "typo")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("invalid boolean accepted")
	}
	t.Setenv("ORDARR_SECURE_COOKIES", "false")
	t.Setenv("ORDARR_ADDR", "bad")
	if _, err = LoadConfig(); err == nil {
		t.Fatal("invalid address accepted")
	}
}
