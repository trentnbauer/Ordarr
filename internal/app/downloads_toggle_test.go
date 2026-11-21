package app

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// "Download missing items for this collection" is ticked by default; unticking opts the collection out.
func TestDownloadToggleIsTickedByDefault(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	if err := a.store.CreateArr(ctx, store.ArrConnection{ID: "s", Kind: "sonarr", Name: "Sonarr", BaseURL: "http://x:8989", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1}); err != nil {
		t.Fatal(err)
	}
	id, err := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "episode", Title: "E", Provider: "tvdb", ExternalID: "1", Season: 1, Episode: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	box := regexp.MustCompile(`<input type="checkbox" name="enabled" value="yes" (checked)?> Download missing items for this collection`)
	check := func(wantTicked bool) {
		t.Helper()
		body := request(h, "GET", "/collections/"+id+"?tab=downloads", nil, session, csrf).Body.String()
		m := box.FindStringSubmatch(body)
		if m == nil {
			t.Fatal("checkbox missing or reworded")
		}
		if (m[1] == "checked") != wantTicked {
			t.Fatalf("ticked=%v want %v", m[1] == "checked", wantTicked)
		}
		if strings.Contains(body, "Don&#39;t download missing items") {
			t.Fatal("old backwards wording still present")
		}
	}
	check(true) // new collections download by default
	post := func(v url.Values) {
		t.Helper()
		v.Set("csrf", csrf.Value)
		if w := request(h, "POST", "/collections/"+id+"/downloads", v, session, csrf); w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	post(url.Values{}) // an unticked box sends nothing
	if off, _ := a.store.NoDownload(ctx); !off[id] {
		t.Fatal("unticking did not opt the collection out")
	}
	check(false)
	post(url.Values{"enabled": {"yes"}})
	if off, _ := a.store.NoDownload(ctx); off[id] {
		t.Fatal("ticking did not turn downloads back on")
	}
	check(true)
}
