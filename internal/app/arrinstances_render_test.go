package app

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// The Add dialog lists the instances with the defaults ticked, the instance page has the default
// box, and the dialog's choice is what a followed collection starts with.
func TestAddDialogListsInstancesAndSavesTheChoice(t *testing.T) {
	a, h := fixture(t)
	fakeSources(t, a, map[string]string{"tiny.json": tinyOrder})
	session, csrf := setup(t, h)
	ctx := context.Background()
	for _, c := range []store.ArrConnection{
		{ID: "a", Kind: "sonarr", Name: "Sonarr 1080", BaseURL: "http://x:1", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
		{ID: "b", Kind: "sonarr", Name: "Sonarr 4K", BaseURL: "http://x:2", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
	} {
		if err := a.store.CreateArr(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	a.store.DB.Exec("INSERT INTO plex_accounts VALUES(1,42,'u','U',0,'client-1',?,?,?,1)", []byte("c"), now+86400, now)
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{AccountID: 1, ResourceID: "m", ID: "plex-1", Name: "Home", BaseURL: "https://plex.example:32400", ClientID: "client-1", MachineID: "m", ServerName: "Plex", ServerVersion: "1", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}, 0); err != nil {
		t.Fatal(err)
	}
	body := request(h, "GET", "/add", nil, session, csrf).Body.String()
	if !strings.Contains(body, "Download missing items from") || !strings.Contains(body, `name="instance" value="a" checked`) || strings.Contains(body, `name="instance" value="b" checked`) {
		t.Fatal("the dialog should list both instances with only the default ticked")
	}
	page := request(h, "GET", "/settings/acquisition?id=a", nil, session, csrf).Body.String()
	if !strings.Contains(page, `name="default" value="yes" checked> Default Sonarr for new collections`) || !strings.Contains(page, "Connected · default") {
		t.Fatal("the default instance is not marked")
	}
	if p := request(h, "GET", "/settings/acquisition?id=b", nil, session, csrf).Body.String(); strings.Contains(p, `name="default" value="yes" checked`) {
		t.Fatal("a non-default instance is marked as the default")
	}
	// Follow with both ticked.
	w := request(h, "POST", "/add", url.Values{"csrf": {csrf.Value}, "source": {"community"}, "path": {"tiny.json"}, "instances_listed": {"1"}, "instance": {"a", "b"}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/collections/"), "?created=1")
	if got, _ := a.store.CollectionArrs(ctx, id); len(got) != 2 {
		t.Fatalf("dialog choice not saved: %v", got)
	}
	// Following with nothing ticked means no instance (explicitly chosen), not the defaults.
	w = request(h, "POST", "/add", url.Values{"csrf": {csrf.Value}, "source": {"community"}, "path": {"tiny.json"}, "instances_listed": {"1"}}, session, csrf)
	id = strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/collections/"), "?created=1")
	if got, _ := a.store.CollectionArrs(ctx, id); len(got) != 0 {
		t.Fatalf("unticking everything must be respected: %v", got)
	}
}
