package app

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Removing a Plex server deletes the playlist links Ordarr keeps for it, so that needs an explicit
// yes (and the page says what will be lost). A server with nothing linked removes as before.
func TestRemovingAPlexServerNeedsConfirmationWhenPlaylistsAreLinked(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	mk := func(id, name string) store.PlexConnection {
		c := store.PlexConnection{ID: id, Name: name, BaseURL: "https://" + id + ".example:32400", ClientID: "client", MachineID: id, ServerName: "Plex", ServerVersion: "1", TokenCipher: a.vault.Seal("t", "plex:"+id+":token")}
		if err := a.store.SavePlexConnection(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
		c, _ = a.store.PlexConnection(ctx, id)
		return c
	}
	linked, empty := mk("linked", "Linked Plex"), mk("empty", "Empty Plex")
	collection, err := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.CreateDestination(ctx, collection, linked.ID, "authoritative", 0); err != nil {
		t.Fatal(err)
	}
	loaded, _ := a.store.Collection(ctx, collection)
	if _, err = a.store.DB.Exec("INSERT INTO manual_matches VALUES(?,?,?,?)", linked.ID, loaded.Entries[0].ID, "tmdb:1", "99"); err != nil {
		t.Fatal(err)
	}
	remove := func(c store.PlexConnection, extra url.Values) int {
		v := url.Values{"csrf": {csrf.Value}, "revision": {strconv.Itoa(c.Revision)}, "confirm_name": {c.Name}}
		for k, vals := range extra {
			v[k] = vals
		}
		return request(h, "POST", "/settings/plex/"+c.ID+"/delete", v, session, csrf).Code
	}
	// The page says what removing the server takes with it, and asks for the extra tick.
	page := request(h, "GET", "/settings/plex", nil, session, csrf).Body.String()
	for _, want := range []string{"the links to 1 playlist", "1 match you chose by hand", `name="confirm_playlists"`, "stays in Plex"} {
		if !strings.Contains(page, want) {
			t.Errorf("remove box missing %q", want)
		}
	}
	// Typing the name is not enough when playlists are linked: nothing is deleted.
	if code := remove(linked, nil); code != 400 {
		t.Fatalf("removed without confirming the playlists: %d", code)
	}
	if n, _ := a.store.PlexImpact(ctx, linked.ID); n.Playlists != 1 || n.Picks != 1 {
		t.Fatalf("the refusal still deleted something: %+v", n)
	}
	// With the explicit confirmation it goes, and the links go with it.
	if code := remove(linked, url.Values{"confirm_playlists": {"yes"}}); code != 303 {
		t.Fatalf("confirmed removal failed: %d", code)
	}
	if all, _ := a.store.AllDestinations(ctx); len(all) != 0 {
		t.Fatal("playlist links survived the removal")
	}
	// A server with nothing linked needs no extra tick.
	if code := remove(empty, nil); code != 303 {
		t.Fatalf("removing an empty server: %d", code)
	}
}

func TestDisconnectingThePlexAccountNeedsConfirmationWhenPlaylistsAreLinked(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	now := time.Now().Unix()
	creds, _ := json.Marshal(plex.AccountCredentials{Device: plex.Device{ClientID: "client-1", Kid: "kid", PrivateKey: []byte("key")}, Token: "account-token", Resources: []plex.Resource{{MachineID: "m", Name: "Home", Provides: "server", AccessToken: "resource-token", Owned: true}}})
	if _, err := a.store.DB.Exec("INSERT INTO plex_accounts VALUES(1,42,'plex-user','Plex User',0,'client-1',?,?,?,1)", a.vault.Seal(string(creds), "plex:account:42"), now+86400, now); err != nil {
		t.Fatal(err)
	}
	conn := store.PlexConnection{AccountID: 1, ResourceID: "m", ID: "plex-1", Name: "Home", BaseURL: "https://plex.example:32400", ClientID: "client-1", MachineID: "m", ServerName: "Plex", ServerVersion: "1", TokenCipher: a.vault.Seal("t", "plex:plex-1:token")}
	if err := a.store.SavePlexConnection(ctx, conn, 0); err != nil {
		t.Fatal(err)
	}
	collection, _ := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if _, err := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0); err != nil {
		t.Fatal(err)
	}
	account, _ := a.store.PlexAccount(ctx)
	disconnect := func(extra url.Values) int {
		v := url.Values{"csrf": {csrf.Value}, "revision": {strconv.Itoa(account.Revision)}, "confirm_username": {"plex-user"}}
		for k, vals := range extra {
			v[k] = vals
		}
		return request(h, "POST", "/settings/plex/account/disconnect", v, session, csrf).Code
	}
	if code := disconnect(nil); code != 400 {
		t.Fatalf("disconnected without confirming the playlists: %d", code)
	}
	if _, err := a.store.PlexConnection(ctx, "plex-1"); err != nil {
		t.Fatal("the refusal removed the server anyway", err)
	}
	if code := disconnect(url.Values{"confirm_playlists": {"yes"}}); code != 303 {
		t.Fatalf("confirmed disconnect failed: %d", code)
	}
	if all, _ := a.store.AllDestinations(ctx); len(all) != 0 {
		t.Fatal("playlist links survived the disconnect")
	}
}
