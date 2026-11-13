package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

const appPlexToken = "private-fixture-plex-token-123456"

func TestPlexConnectionLifecycleAndRedaction(t *testing.T) {
	a, h := fixture(t)
	var logs bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&logs, nil))
	session, csrf := setup(t, h)
	var fail, changed atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Plex-Token") != appPlexToken {
			t.Error("token not sent in header")
		}
		if fail.Load() {
			w.WriteHeader(401)
			io.WriteString(w, appPlexToken)
			return
		}
		if r.URL.Path == "/" {
			machine := "one"
			if changed.Load() {
				machine = "different"
			}
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"machineIdentifier": machine, "friendlyName": "Plex fixture", "version": "1.43.4"}})
		} else {
			io.WriteString(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
		}
	}))
	defer server.Close()
	post := func(path string, v url.Values, want int) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		w := request(h, "POST", path, v, session, csrf)
		if w.Code != want {
			t.Fatalf("%s: got %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), appPlexToken) {
			t.Fatal("response exposed token")
		}
	}
	post("/settings/plex", url.Values{"name": {"Home"}, "url": {server.URL}, "token": {appPlexToken}}, 400)
	if calls.Load() != 0 {
		t.Fatal("sent token without plaintext HTTP consent")
	}
	create := url.Values{"name": {"Home"}, "url": {server.URL}, "token": {appPlexToken}, "allow_http": {"yes"}}
	post("/settings/plex", create, 303)
	ids, err := a.store.PlexConnectionIDs(context.Background())
	if err != nil || len(ids) != 1 {
		t.Fatal("not saved", err)
	}
	id := ids[0]
	current := func() store.PlexConnection {
		t.Helper()
		c, err := a.store.PlexConnection(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := current()
	if bytes.Contains(c.TokenCipher, []byte(appPlexToken)) || len(c.Libraries) != 1 {
		t.Fatal("plaintext token or missing library")
	}
	plain, err := a.vault.Open(c.TokenCipher, "plex:"+id+":token")
	if err != nil || plain != appPlexToken {
		t.Fatal("encrypted credential lost", err)
	}
	w := request(h, "GET", "/settings/plex", nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Movies") || strings.Contains(w.Body.String(), appPlexToken) || strings.Contains(w.Body.String(), string(c.TokenCipher)) {
		t.Fatal("invalid public projection")
	}
	// A failed recheck preserves credential, machine identity and library snapshot.
	fail.Store(true)
	post("/settings/plex/"+id+"/check", url.Values{"revision": {strconv.Itoa(c.Revision)}}, 502)
	after := current()
	if after.LastError == "" || len(after.Libraries) != 1 || !bytes.Equal(c.TokenCipher, after.TokenCipher) || after.CheckedAt != c.CheckedAt {
		t.Fatal("failed check replaced last good state")
	}
	fail.Store(false)
	changed.Store(true)
	post("/settings/plex/"+id+"/check", url.Values{"revision": {strconv.Itoa(after.Revision)}}, 502)
	if current().MachineID != "one" {
		t.Fatal("rebound to different machine")
	}
	changed.Store(false)
	c = current()
	post("/settings/plex/"+id+"/check", url.Values{"revision": {strconv.Itoa(c.Revision)}}, 303)
	c = current()
	if c.LastError != "" {
		t.Fatal("failure not cleared")
	}
	// Empty replacement retains token only at the same server origin.
	callsBefore := calls.Load()
	post("/settings/plex", url.Values{"id": {id}, "revision": {strconv.Itoa(c.Revision)}, "name": {"Home"}, "url": {"http://localhost:9999"}, "allow_http": {"yes"}}, 400)
	if calls.Load() != callsBefore {
		t.Fatal("token was sent to changed URL")
	}
	post("/settings/plex", url.Values{"id": {id}, "revision": {strconv.Itoa(c.Revision)}, "name": {"Renamed"}, "url": {server.URL}, "allow_http": {"yes"}}, 303)
	post("/settings/plex/"+id+"/check", url.Values{"revision": {strconv.Itoa(c.Revision)}}, 409)
	c = current()
	post("/settings/plex/"+id+"/delete", url.Values{"revision": {strconv.Itoa(c.Revision)}, "confirm_name": {"wrong"}}, 400)
	post("/settings/plex/"+id+"/delete", url.Values{"revision": {strconv.Itoa(c.Revision)}, "confirm_name": {"Renamed"}}, 303)
	if ids, err = a.store.PlexConnectionIDs(context.Background()); err != nil || len(ids) != 0 {
		t.Fatal("connection not deleted", err)
	}
	var count int
	if err = a.store.DB.QueryRow("SELECT count(*) FROM plex_libraries").Scan(&count); err != nil || count != 0 {
		t.Fatal("library rows orphaned", err)
	}
	if strings.Contains(logs.String(), appPlexToken) {
		t.Fatal("token logged")
	}
}
func TestPlexRoutesAuthenticationCSRFAndReflection(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	if w := request(h, "GET", "/settings/plex", nil); w.Code != 303 {
		t.Fatal("unauthenticated settings")
	}
	for _, path := range []string{"/settings/plex", "/settings/plex/unknown/check", "/settings/plex/unknown/delete"} {
		if w := request(h, "POST", path, url.Values{}, session); w.Code != 403 {
			t.Fatal("no CSRF enforcement", path, w.Code)
		}
		if w := request(h, "POST", path, url.Values{"csrf": {csrf.Value}}, csrf); w.Code != 303 {
			t.Fatal("no auth enforcement", path, w.Code)
		}
	}
	w := request(h, "POST", "/settings/plex", url.Values{"csrf": {csrf.Value}, "name": {"Test"}, "url": {"https://server.example/?X-Plex-Token=" + appPlexToken}, "token": {appPlexToken}}, session, csrf)
	if w.Code != 400 || strings.Contains(w.Body.String(), appPlexToken) {
		t.Fatal("invalid URL echoed credential")
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"machine","friendlyName":"`+appPlexToken+`","version":"1.0"}}`)
	}))
	defer remote.Close()
	w = request(h, "POST", "/settings/plex", url.Values{"csrf": {csrf.Value}, "name": {"Test"}, "url": {remote.URL}, "token": {appPlexToken}, "allow_http": {"yes"}}, session, csrf)
	if w.Code != 502 || strings.Contains(w.Body.String(), appPlexToken) {
		t.Fatal("remote reflected credential")
	}
}
func TestPlexKeyAndCredentialPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ordarr.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{DataDir: dir, Address: "127.0.0.1:8080"}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(s, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	c := store.PlexConnection{ID: "one", Name: "Home", BaseURL: "https://plex.example:32400", ClientID: "client", MachineID: "machine", ServerName: "Server", ServerVersion: "1.0"}
	c.TokenCipher = a.vault.Seal(appPlexToken, "plex:one:token")
	if err = s.SavePlexConnection(context.Background(), c, 0); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	a, err = New(s, config, logger)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.PlexConnection(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := a.vault.Open(saved.TokenCipher, "plex:one:token")
	if err != nil || plain != appPlexToken {
		t.Fatal("reopen lost credentials", err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(appPlexToken)) {
		t.Fatal("raw database contains token")
	}
	if err = os.Remove(filepath.Join(dir, "credentials.key")); err != nil {
		t.Fatal(err)
	}
	if _, err = New(s, config, logger); err == nil {
		t.Fatal("silently generated a replacement key for encrypted data")
	}
}
