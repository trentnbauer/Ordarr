package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

type accountFixture struct {
	client              *plex.AccountClient
	granted, fail       atomic.Bool
	userID              atomic.Int64
	refreshes, pmsCalls atomic.Int32
	pms                 *httptest.Server
	accountToken        string
}
type accountFixtureTransport struct{ base *url.URL }

func (f accountFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || (r.URL.Host != "plex.tv" && r.URL.Host != "clients.plex.tv") {
		return nil, errors.New("invalid account origin")
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = f.base.Scheme
	u.Host = f.base.Host
	copy.URL = &u
	return http.DefaultTransport.RoundTrip(copy)
}
func newAccountFixture(t *testing.T) *accountFixture {
	t.Helper()
	f := &accountFixture{}
	f.userID.Store(123)
	payload, _ := json.Marshal(map[string]any{"exp": time.Now().Add(7 * 24 * time.Hour).Unix()})
	f.accountToken = "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".provider-fixture-signature"
	f.pms = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.pmsCalls.Add(1)
		if r.Method != "GET" || r.Header.Get("X-Plex-Token") != "resource-scoped-token" {
			t.Error("account token leaked to PMS or Plex write attempted")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/" {
			io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"resource-machine","friendlyName":"Account server","version":"1.43.4"}}`)
		} else if r.URL.Path == "/library/sections/1/all" {
			io.WriteString(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"1","type":"movie","title":"Account movie"}]}}`)
		} else {
			io.WriteString(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Account movies","type":"movie"}]}}`)
		}
	}))
	t.Cleanup(f.pms.Close)
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.fail.Load() {
			w.WriteHeader(401)
			io.WriteString(w, f.accountToken)
			return
		}
		switch r.URL.Path {
		case "/api/v2/pins":
			var body struct {
				Strong bool              `json:"strong"`
				JWK    map[string]string `json:"jwk"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Strong || body.JWK["crv"] != "Ed25519" {
				t.Error("invalid device registration")
			}
			io.WriteString(w, `{"id":42,"code":"strong-fixture-pin","expiresIn":600}`)
		case "/api/v2/pins/42":
			if f.granted.Load() {
				json.NewEncoder(w).Encode(map[string]any{"id": 42, "code": "strong-fixture-pin", "authToken": f.accountToken})
			} else {
				io.WriteString(w, `{"id":42,"code":"strong-fixture-pin","authToken":null}`)
			}
		case "/api/v2/user":
			if r.Header.Get("X-Plex-Token") != f.accountToken {
				t.Error("wrong account token")
			}
			json.NewEncoder(w).Encode(map[string]any{"id": f.userID.Load(), "username": "plex-user", "friendlyName": "Plex User"})
		case "/api/v2/resources":
			json.NewEncoder(w).Encode([]any{map[string]any{"clientIdentifier": "resource-machine", "name": "Account server", "provides": "server", "owned": false, "accessToken": "resource-scoped-token", "connections": []any{map[string]any{"uri": f.pms.URL, "local": true}}}})
		case "/api/v2/auth/nonce":
			io.WriteString(w, `{"nonce":"nonce-fixture"}`)
		case "/api/v2/auth/token":
			f.refreshes.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"auth_token": f.accountToken})
		default:
			t.Error("unknown account path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(cloud.Close)
	base, _ := url.Parse(cloud.URL)
	f.client = plex.NewAccountClient(accountFixtureTransport{base})
	return f
}
func TestAccountSignInRefreshServerScopeAndDisconnect(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	session, csrf := setup(t, h)
	post := func(path string, v url.Values, want int) string {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), f.accountToken) || strings.Contains(w.Body.String(), "resource-scoped-token") {
			t.Fatal("account/server token rendered")
		}
		return w.Header().Get("Location")
	}
	attempt := post("/settings/plex/account/start", url.Values{}, 303)
	w := request(h, "GET", attempt, nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "https://app.plex.tv/auth#?") {
		t.Fatal("authorisation link missing")
	}
	post(attempt, url.Values{}, 202)
	post(attempt, url.Values{}, 429)
	if _, err := a.store.DB.Exec("UPDATE plex_auth_attempts SET next_poll_at=0"); err != nil {
		t.Fatal(err)
	}
	f.granted.Store(true)
	post(attempt, url.Values{}, 303)
	post(attempt, url.Values{}, 410)
	account, err := a.store.PlexAccount(context.Background())
	if err != nil || account.UserID != 123 {
		t.Fatal("verified identity missing", err)
	}
	credentials, err := a.accountCredentials(account)
	if err != nil || len(credentials.Device.PrivateKey) != ed25519.PrivateKeySize || credentials.Token != f.accountToken {
		t.Fatal("encrypted device credentials missing", err)
	}
	w = request(h, "GET", "/settings/plex", nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "plex-user") || !strings.Contains(w.Body.String(), "Connect Account server") {
		t.Fatal("verified resources missing")
	}
	// An arbitrary resource URI must never receive an account or server token.
	before := f.pmsCalls.Load()
	post("/settings/plex/account/connect", url.Values{"account_revision": {strconv.Itoa(account.Revision)}, "resource_id": {"resource-machine"}, "uri": {"http://127.0.0.1:9999"}, "allow_http": {"yes"}}, 400)
	if f.pmsCalls.Load() != before {
		t.Fatal("unexpected PMS request")
	}
	account, err = a.store.PlexAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	post("/settings/plex/account/connect", url.Values{"account_revision": {strconv.Itoa(account.Revision)}, "resource_id": {"resource-machine"}, "uri": {f.pms.URL}, "allow_http": {"yes"}}, 303)
	ids, err := a.store.PlexConnectionIDs(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("account server not saved", err)
	}
	connection, err := a.store.PlexConnection(context.Background(), ids[0])
	if err != nil || connection.AccountID != 1 || connection.ResourceID != "resource-machine" {
		t.Fatal("missing account binding", err)
	}
	account, err = a.store.PlexAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	oldCipher := append([]byte{}, account.CredentialsCipher...)
	f.fail.Store(true)
	post("/settings/plex/account/refresh", url.Values{"revision": {strconv.Itoa(account.Revision)}}, 502)
	unchanged, err := a.store.PlexAccount(context.Background())
	if err != nil || string(unchanged.CredentialsCipher) != string(oldCipher) || unchanged.Revision != account.Revision {
		t.Fatal("failed refresh replaced working credentials")
	}
	f.fail.Store(false)
	post("/settings/plex/account/refresh", url.Values{"revision": {strconv.Itoa(account.Revision)}}, 303)
	if f.refreshes.Load() != 1 {
		t.Fatal("nonce refresh not exercised")
	}
	// Cloud verification failure must also mark the linked server snapshot stale,
	// without sending a PMS request or changing its encrypted token/library list.
	f.fail.Store(true)
	pmsBefore := f.pmsCalls.Load()
	post("/settings/plex/"+connection.ID+"/check", url.Values{"revision": {strconv.Itoa(connection.Revision)}}, 502)
	failedConnection, err := a.store.PlexConnection(context.Background(), connection.ID)
	if err != nil || failedConnection.LastError == "" || len(failedConnection.Libraries) != 1 || !bytes.Equal(failedConnection.TokenCipher, connection.TokenCipher) || f.pmsCalls.Load() != pmsBefore {
		t.Fatal("cloud failure did not retain and mark stale snapshot", err)
	}
	f.fail.Store(false)
	post("/settings/plex/"+connection.ID+"/check", url.Values{"revision": {strconv.Itoa(failedConnection.Revision)}}, 303)
	connection, err = a.store.PlexConnection(context.Background(), connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Force expiry to prove linked-server use refreshes before sending a PMS token.
	if _, err = a.store.DB.Exec("UPDATE plex_accounts SET expires_at=?", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	post("/settings/plex/"+connection.ID+"/check", url.Values{"revision": {strconv.Itoa(connection.Revision)}}, 303)
	if f.refreshes.Load() != 2 {
		t.Fatal("expired account token not refreshed")
	}
	account, err = a.store.PlexAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	post("/settings/plex/account/disconnect", url.Values{"revision": {strconv.Itoa(account.Revision)}, "confirm_username": {"wrong"}}, 400)
	post("/settings/plex/account/disconnect", url.Values{"revision": {strconv.Itoa(account.Revision)}, "confirm_username": {"plex-user"}}, 303)
	if _, err = a.store.PlexAccount(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("account not disconnected")
	}
	if ids, err = a.store.PlexConnectionIDs(context.Background()); err != nil || len(ids) != 0 {
		t.Fatal("linked server not removed", err)
	}
}
func TestAccountAttemptsAreBoundToSessionAndExpire(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	session, csrf := setup(t, h)
	w := request(h, "POST", "/settings/plex/account/start", url.Values{"csrf": {csrf.Value}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	path := w.Header().Get("Location")
	other := randomToken()
	if err := a.store.AddSession(context.Background(), other, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	otherCookie := &http.Cookie{Name: sessionCookie, Value: other}
	if w = request(h, "GET", path, nil, otherCookie, csrf); w.Code != 410 || strings.Contains(w.Body.String(), "strong-fixture-pin") {
		t.Fatal("attempt exposed to another browser", w.Code)
	}
	if w = request(h, "POST", path, url.Values{"csrf": {csrf.Value}}, otherCookie, csrf); w.Code != 410 {
		t.Fatal("cross-session PIN poll allowed")
	}
	if w = request(h, "POST", path+"/cancel", url.Values{"csrf": {csrf.Value}}, otherCookie, csrf); w.Code != 303 {
		t.Fatal(w.Code)
	}
	if w = request(h, "GET", path, nil, session, csrf); w.Code != 200 {
		t.Fatal("another session cancelled owner attempt")
	}
	if _, err := a.store.DB.Exec("UPDATE plex_auth_attempts SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if w = request(h, "POST", path, url.Values{"csrf": {csrf.Value}}, session, csrf); w.Code != 410 {
		t.Fatal("expired attempt accepted")
	}
	for _, route := range []string{"/settings/plex/account/start", "/settings/plex/account/refresh", "/settings/plex/account/disconnect", "/settings/plex/account/connect", path, path + "/cancel"} {
		if w = request(h, "POST", route, nil, session); w.Code != 403 {
			t.Fatal("account action lacks CSRF protection", route, w.Code)
		}
	}
}
func TestAccountReauthorisationCannotSwitchUsers(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	session, csrf := setup(t, h)
	f.granted.Store(true)
	signIn := func() string {
		t.Helper()
		w := request(h, "POST", "/settings/plex/account/start", url.Values{"csrf": {csrf.Value}}, session, csrf)
		if w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Header().Get("Location")
	}
	first := signIn()
	if w := request(h, "POST", first, url.Values{"csrf": {csrf.Value}}, session, csrf); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	second := signIn()
	f.userID.Store(456)
	if w := request(h, "POST", second, url.Values{"csrf": {csrf.Value}}, session, csrf); w.Code != 409 {
		t.Fatal("account switched without explicit disconnect", w.Code)
	}
	saved, err := a.store.PlexAccount(context.Background())
	if err != nil || saved.UserID != 123 {
		t.Fatal("old identity changed")
	}
	f.userID.Store(123)
	if _, err = a.store.DB.Exec("UPDATE plex_auth_attempts SET next_poll_at=0"); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "POST", second, url.Values{"csrf": {csrf.Value}}, session, csrf); w.Code != 303 {
		t.Fatal("same user cannot reauthorise", w.Code, w.Body.String())
	}
	updated, err := a.store.PlexAccount(context.Background())
	if err != nil || updated.ClientID == saved.ClientID || updated.Revision <= saved.Revision {
		t.Fatal("device key not replaced")
	}
}

func TestBrowserPlexAccountFlow(t *testing.T) {
	if os.Getenv("ORDARR_ACCOUNT_BROWSER") != "1" {
		t.Skip("optional browser check: set ORDARR_ACCOUNT_BROWSER=1 and provide Playwright/Chromium")
	}
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	f.granted.Store(true)
	server := httptest.NewServer(h)
	defer server.Close()
	command := exec.Command("node", "../../scripts/account-browser-smoke.cjs", server.URL)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser check: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
	if f.refreshes.Load() < 1 || f.pmsCalls.Load() < 4 {
		t.Fatal("browser did not exercise refresh and scoped PMS reads")
	}
}

func TestAccountDeviceCredentialsPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ordarr.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: dir, Address: "127.0.0.1:8080"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(s, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	f := newAccountFixture(t)
	a.accounts = f.client
	credentials := plex.AccountCredentials{Device: plex.NewDevice(), Token: f.accountToken, Resources: []plex.Resource{{MachineID: "machine", Name: "Server", AccessToken: "private-server-token"}}}
	result := plex.AccountResult{User: plex.AccountUser{ID: 123, Username: "plex-user"}, Credentials: credentials, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err = a.saveAccountResult(context.Background(), result, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	a, err = New(s, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	a.accounts = f.client
	saved, err := s.PlexAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := a.accountCredentials(saved)
	if err != nil || !bytes.Equal(restored.Device.PrivateKey, credentials.Device.PrivateKey) || restored.Token != credentials.Token || restored.Resources[0].AccessToken != "private-server-token" {
		t.Fatal("device/account/resource credentials lost on reopen", err)
	}
	if _, _, err = a.verifiedAccount(context.Background(), true); err != nil {
		t.Fatal("restored device cannot refresh", err)
	}
	if err = os.Remove(filepath.Join(dir, "credentials.key")); err != nil {
		t.Fatal(err)
	}
	if _, err = New(s, config, logger); err == nil {
		t.Fatal("account-only credentials did not prevent key regeneration")
	}
}
