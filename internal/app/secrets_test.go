package app

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// No page, export or failed form submission may reveal a stored credential
// (Sonarr/Radarr API keys, Jellyfin and Plex tokens, the Plex account token)
// or its ciphertext, whatever the signed-in administrator requests.
func TestStoredCredentialsNeverReachResponses(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	secrets := []string{"sonarr-api-key-0123456789abcdef", "radarr-api-key-fedcba9876543210", "jellyfin-user-token-abcdef0123", "plex-server-token-xyz123", "plex-account-token-acct42", "plex-resource-token-res42", "plex-device-private-key"}
	var ciphers [][]byte
	seal := func(value, binding string) []byte {
		c := a.vault.Seal(value, binding)
		ciphers = append(ciphers, c)
		return c
	}
	// Plex account (with its resource token) and an account-linked server.
	creds, _ := json.Marshal(plex.AccountCredentials{Device: plex.Device{ClientID: "client-1", Kid: "kid", PrivateKey: []byte(secrets[6])}, Token: secrets[4], Resources: []plex.Resource{{MachineID: "machine-1", Name: "Home Plex", Provides: "server", AccessToken: secrets[5], Owned: true, Connections: []plex.ResourceConnection{{URI: "https://plex.example:32400", Local: true}}}}})
	now := time.Now().Unix()
	if _, err := a.store.DB.Exec("INSERT INTO plex_accounts VALUES(1,42,'plex-user','Plex User',0,'client-1',?,?,?,1)", seal(string(creds), "plex:account:42"), now+86400, now); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{AccountID: 1, ResourceID: "machine-1", ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "client-1", MachineID: "machine-1", ServerName: "Plex", ServerVersion: "1", TokenCipher: seal(secrets[3], "plex:plex-1:token"), Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}, 0); err != nil {
		t.Fatal(err)
	}
	// Sonarr, Radarr and Jellyfin.
	for i, kind := range []string{"sonarr", "radarr"} {
		c := store.ArrConnection{ID: kind, Kind: kind, Name: kind, BaseURL: "https://" + kind + ".example", KeyCipher: seal(secrets[i], "arr:"+kind+":key"), Snapshot: arr.Snapshot{Version: "4", Profiles: []arr.Choice{{ID: 1, Name: "HD"}}, Roots: []arr.Choice{{ID: 1, Path: "/media"}}}}
		if err := a.store.CreateArr(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.CreateJellyfin(ctx, store.JellyfinConnection{ID: "jf-1", Name: "Jellyfin", BaseURL: "https://jellyfin.example", TokenCipher: seal(secrets[2], "jellyfin:jf-1:token"), Snapshot: jellyfin.Snapshot{ServerName: "Jellyfin", UserName: "viewer"}}); err != nil {
		t.Fatal(err)
	}
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "Order", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "Film", Provider: "tmdb", ExternalID: "1"}, {ID: "b", Kind: "episode", Title: "Show", Provider: "tvdb", ExternalID: "7", Season: 1, Episode: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := a.store.ArrConnection(ctx, "sonarr")
	if _, err = a.store.ProposeAcquisition(ctx, sc, 7, "Show", "manual", false); err != nil {
		t.Fatal(err)
	}
	leaks := func(where, body string) {
		t.Helper()
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s reveals the credential %q", where, s)
			}
		}
		for _, c := range ciphers {
			for _, form := range []string{string(c), base64.StdEncoding.EncodeToString(c), base64.RawURLEncoding.EncodeToString(c), hex.EncodeToString(c), fmt.Sprint(c)} {
				if strings.Contains(body, form) {
					t.Errorf("%s reveals a credential ciphertext", where)
				}
			}
		}
	}
	paths := []string{"/", "/collections/" + id, "/needs-input", "/welcome", "/jobs", "/settings", "/settings/export.env", "/settings/plex", "/settings/plex/plex-1/libraries/1/media", "/settings/jellyfin", "/settings/jellyfin?id=jf-1", "/settings/jellyfin?new=1", "/settings/acquisition", "/settings/acquisition?id=sonarr", "/settings/acquisition?id=radarr", "/settings/acquisition?new=1", "/collections/" + id + "/matches/plex-1", "/collections/" + id + "/jellyfin/jf-1", "/collections/" + id + "/export?format=yaml", "/collections/" + id + "/export?format=json", "/add", "/add?source=paste", "/add?source=url"}
	for _, tab := range []string{"order", "advanced", "source", "publishing", "history", "settings"} {
		paths = append(paths, "/collections/"+id+"?tab="+tab)
	}
	for _, p := range paths {
		w := request(h, "GET", p, nil, session, csrf)
		if w.Code >= 500 {
			t.Errorf("GET %s: %d", p, w.Code)
		}
		leaks("GET "+p, w.Body.String()+fmt.Sprint(w.Header()))
	}
	// Failed submissions never echo a submitted key, and never reveal the saved one.
	posts := []struct {
		path string
		form url.Values
	}{
		{"/settings/acquisition", url.Values{"kind": {"sonarr"}, "name": {"x"}, "url": {"ftp://bad"}, "api_key": {secrets[0]}}},
		{"/settings/acquisition/sonarr/edit", url.Values{"revision": {"999"}, "name": {"x"}, "url": {"https://sonarr.example"}, "api_key": {secrets[0]}}},
		{"/settings/acquisition/sonarr/edit", url.Values{"revision": {"1"}, "name": {"x"}, "url": {"https://other.example"}}},
		{"/settings/jellyfin", url.Values{"name": {"x"}, "url": {"ftp://bad"}, "token": {secrets[2]}}},
		{"/settings/jellyfin/jf-1/edit", url.Values{"revision": {"1"}, "name": {"x"}, "url": {"https://other.example"}}},
		{"/settings/plex", url.Values{"name": {"x"}, "url": {"ftp://bad"}, "token": {secrets[3]}}},
		{"/settings/plex", url.Values{"name": {secrets[3]}, "url": {"https://plex.example:32400"}, "token": {secrets[3]}}},
		{"/settings/acquisition/sonarr/delete", url.Values{"confirm": {"wrong"}}},
	}
	for _, p := range posts {
		p.form.Set("csrf", csrf.Value)
		w := request(h, "POST", p.path, p.form, session, csrf)
		leaks("POST "+p.path, w.Body.String()+fmt.Sprint(w.Header()))
	}
	// Signed-out requests get nothing but a redirect.
	for _, p := range paths {
		if w := request(h, "GET", p, nil); w.Code != 303 {
			t.Errorf("signed-out GET %s: %d", p, w.Code)
		}
	}
}
