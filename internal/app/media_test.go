package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

func TestLibraryBrowserPaginationAndStaleFailureRetention(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	var calls atomic.Int32
	var fail, changeTotal atomic.Bool
	const token = "private-media-fixture-token"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "GET" || r.Header.Get("X-Plex-Token") != token {
			t.Error("unsafe media access")
		}
		if fail.Load() {
			w.WriteHeader(401)
			io.WriteString(w, token)
			return
		}
		switch r.URL.Path {
		case "/":
			io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"machine","friendlyName":"Media Plex","version":"1.43.4"}}`)
		case "/library/sections/all":
			io.WriteString(w, `{"MediaContainer":{"size":2,"Directory":[{"key":"1","title":"Movies","type":"movie"},{"key":"2","title":"Shows","type":"show"}]}}`)
		default:
			offset, _ := strconv.Atoi(r.Header.Get("X-Plex-Container-Start"))
			kind := "movie"
			if r.URL.Query().Get("type") == "4" {
				kind = "episode"
			}
			items := []map[string]any{}
			for i := offset; i < min(offset+2, 4); i++ {
				item := map[string]any{"ratingKey": strconv.Itoa(i + 1), "title": "Item " + strconv.Itoa(i+1), "type": kind}
				if kind == "episode" {
					item["grandparentTitle"] = "Series"
					item["parentIndex"] = 0
					item["index"] = i + 1
				}
				items = append(items, item)
			}
			total := 4
			if changeTotal.Load() {
				total = 5
			}
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"size": len(items), "offset": offset, "totalSize": total, "Metadata": items}})
		}
	}))
	defer remote.Close()
	c := store.PlexConnection{ID: "media-connection", Name: "Home", BaseURL: remote.URL, AllowHTTP: true, ClientID: "client", MachineID: "machine", ServerName: "Plex", ServerVersion: "1", TokenCipher: a.vault.Seal(token, "plex:media-connection:token"), Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}, {Key: "2", Title: "Shows", Kind: "show"}}}
	if err := a.store.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	path := "/settings/plex/" + c.ID + "/libraries/1/media"
	w := request(h, "GET", path, nil, session, csrf)
	if w.Code != 200 || calls.Load() != 0 || !strings.Contains(w.Body.String(), "No cached media page") {
		t.Fatal("GET contacted Plex or page missing", w.Code)
	}
	post := func(action string, revision int, want int) {
		t.Helper()
		w := request(h, "POST", path, url.Values{"csrf": {csrf.Value}, "connection_revision": {"1"}, "cache_revision": {strconv.Itoa(revision)}, "action": {action}}, session, csrf)
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", action, w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("media error exposed token")
		}
	}
	post("first", 0, 303)
	one, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || len(one.Payload.Page.Items) != 2 {
		t.Fatal(err)
	}
	post("next", one.Revision, 303)
	two, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || two.Payload.Page.Offset != 2 || len(two.Payload.History) != 1 {
		t.Fatal("next page failed", err)
	}
	post("previous", two.Revision, 303)
	back, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || back.Payload.Page.Offset != 0 {
		t.Fatal("previous failed", err)
	}
	beforeCalls := calls.Load()
	post("next", two.Revision, 409)
	if calls.Load() != beforeCalls {
		t.Fatal("stale edit contacted Plex")
	}
	fail.Store(true)
	post("next", back.Revision, 502)
	failed, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || failed.LastError == "" || !reflect.DeepEqual(back.Payload, failed.Payload) {
		t.Fatal("failed fetch lost old page", err)
	}
	fail.Store(false)
	changeTotal.Store(true)
	post("next", failed.Revision, 409)
	changed, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || changed.Payload.Page.Total != 4 {
		t.Fatal("changed total overwrote page", err)
	}
	post("first", changed.Revision, 303)
	fresh, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || fresh.Payload.Page.Total != 5 || fresh.LastError != "" {
		t.Fatal("first-page recovery failed", err)
	}
	path = "/settings/plex/" + c.ID + "/libraries/2/media"
	post("first", 0, 303)
	w = request(h, "GET", path, nil, session, csrf)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Season 0") || !strings.Contains(w.Body.String(), "Episode 1") {
		t.Fatal("individual episodes not rendered", w.Code, w.Body.String())
	}
	if w = request(h, "POST", path, url.Values{}, session); w.Code != 403 {
		t.Fatal("missing CSRF allowed")
	}
	if w = request(h, "GET", path, nil); w.Code != 303 {
		t.Fatal("library browser unprotected")
	}
}

func TestLibraryBrowserUsesAccountScopedTokensAndRetainsFailedPages(t *testing.T) {
	a, h := fixture(t)
	f := newAccountFixture(t)
	a.accounts = f.client
	session, csrf := setup(t, h)
	ctx := context.Background()
	device := plex.NewDevice()
	if err := a.saveAccountResult(ctx, plex.AccountResult{User: plex.AccountUser{ID: 123, Username: "plex-user"}, Credentials: plex.AccountCredentials{Device: device, Token: f.accountToken}, ExpiresAt: time.Now().Add(time.Hour).Unix()}, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	c := store.PlexConnection{ID: "linked-media", Name: "Linked", BaseURL: f.pms.URL, ClientID: device.ClientID, AccountID: 1, ResourceID: "resource-machine", MachineID: "resource-machine", ServerName: "Plex", ServerVersion: "1", AllowHTTP: true, TokenCipher: a.vault.Seal("old-resource-token", "plex:linked-media:token"), Libraries: []plex.Library{{Key: "1", Kind: "movie", Title: "Account movies"}}}
	if err := a.store.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	path := "/settings/plex/linked-media/libraries/1/media"
	post := func(rev int, action string, want int) {
		t.Helper()
		w := request(h, "POST", path, url.Values{"csrf": {csrf.Value}, "connection_revision": {"1"}, "cache_revision": {strconv.Itoa(rev)}, "action": {action}}, session, csrf)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	post(0, "first", 303)
	cached, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || len(cached.Payload.Page.Items) != 1 {
		t.Fatal("scoped page not fetched", err)
	}
	before := f.pmsCalls.Load()
	f.fail.Store(true)
	post(cached.Revision, "reload", 502)
	failed, err := a.store.LibraryPage(ctx, c.ID, "1")
	if err != nil || failed.LastError == "" || !reflect.DeepEqual(cached.Payload, failed.Payload) || f.pmsCalls.Load() != before {
		t.Fatal("account failure contacted PMS or lost cached page", err)
	}
}
