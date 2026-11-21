package app

import (
	"context"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

func TestSidebarAndNeedsInputBadgeMatchThePage(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	body := request(h, "GET", "/", nil, session, csrf).Body.String()
	for _, want := range []string{"Needs your input", "Connections", "Activity", "Settings"} {
		if !strings.Contains(body, want) {
			t.Errorf("sidebar missing %q", want)
		}
	}
	for _, gone := range []string{"Missing media", "Sonarr / Radarr", "Publish to</div>"} {
		if strings.Contains(body, gone) {
			t.Errorf("old sidebar item %q still present", gone)
		}
	}
	// Happy path: nothing needs the administrator.
	if page := request(h, "GET", "/needs-input", nil, session, csrf).Body.String(); !strings.Contains(page, "Nothing needs you") {
		t.Fatal("expected an empty page in the happy path")
	}
	// A downloader that couldn't find an item is exactly one thing needing input, on the page and the badge.
	sonarr := store.ArrConnection{ID: "s", Kind: "sonarr", Name: "Sonarr 1080", BaseURL: "http://x", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1}
	if err := a.store.CreateArr(ctx, sonarr); err != nil {
		t.Fatal(err)
	}
	sonarr, _ = a.store.ArrConnection(ctx, "s")
	if err := a.store.RecordFailedRequest(ctx, sonarr, 77, "Lost Show", "Lookup found no exact match."); err != nil {
		t.Fatal(err)
	}
	o, _ := a.loadOverview(ctx)
	values, _ := a.availabilityAll(ctx, o)
	count, err := a.inputCount(ctx, o, values)
	view, err2 := a.needsInput(ctx, o, values)
	if err != nil || err2 != nil || count != 1 || view.Total != 1 {
		t.Fatalf("badge %d page %d (%v %v)", count, view.Total, err, err2)
	}
	page := request(h, "GET", "/needs-input", nil, session, csrf).Body.String()
	if !strings.Contains(page, "Lost Show") || !strings.Contains(page, "Not found in any downloader") {
		t.Fatal("failed lookup not listed")
	}
	if side := request(h, "GET", "/", nil, session, csrf).Body.String(); !strings.Contains(side, `side-meta warn">1</span>`) {
		t.Fatal("sidebar badge not shown")
	}
	// The old address still works.
	if w := request(h, "GET", "/missing", nil, session, csrf); w.Code != 303 || w.Header().Get("Location") != "/needs-input" {
		t.Fatal("old Missing media address", w.Code)
	}
}

func TestConnectionListShowsEveryServiceAndFlagsAMissingRadarr(t *testing.T) {
	o := &overview{
		Plex:     []plexView{{ID: "p", Name: "Home Plex", BaseURL: "https://plex.example:32400", AccountLinked: true}},
		Jellyfin: []store.JellyfinConnection{{ID: "j", Name: "Jelly", BaseURL: "http://jelly.lan:8096"}},
		Arr:      []store.ArrConnection{{ID: "s", Kind: "sonarr", Name: "Sonarr", BaseURL: "http://sonarr.lan:8989", ProfileID: 1, RootID: 1}},
	}
	byName := map[string]connItem{}
	for _, i := range connList(o, "s").Items {
		byName[i.Name] = i
	}
	for _, name := range []string{"Home Plex", "Jelly", "Sonarr", "Radarr"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s missing from the list: %+v", name, byName)
		}
	}
	if !byName["Sonarr"].Active || byName["Jelly"].Active || byName["Home Plex"].Active {
		t.Fatal("only the selected connection is active")
	}
	r := byName["Radarr"]
	if !r.Placeholder || !r.Warn || !strings.Contains(r.Status, "movies won't be downloaded") {
		t.Fatalf("a missing Radarr must be flagged plainly: %+v", r)
	}
	// A downloader that isn't finished is not "OK", in the sidebar or the badge.
	unfinished := []store.ArrConnection{{ID: "r", Kind: "radarr", Name: "Radarr 1080"}}
	if got, warn := connectionsSummary(&overview{Plex: []plexView{{}}, Arr: unfinished}); got != "Finish setup" || !warn {
		t.Fatalf("sidebar summary %q", got)
	}
	if got, warn := connectionsSummary(&overview{Plex: []plexView{{}}, Arr: o.Arr}); got != "No Radarr" || !warn {
		t.Fatalf("sidebar summary %q", got)
	}
	both := &overview{Plex: []plexView{{}}, Arr: []store.ArrConnection{{Kind: "sonarr", ProfileID: 1, RootID: 1}, {Kind: "radarr", ProfileID: 1, RootID: 1}}}
	if got, warn := connectionsSummary(both); got != "OK" || warn {
		t.Fatalf("sidebar summary %q", got)
	}
}

func TestConnectionPagesShareOneListAndConnectionsOpensIt(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	sonarr := store.ArrConnection{ID: "s", Kind: "sonarr", Name: "Sonarr 1080", BaseURL: "http://x:8989", KeyCipher: []byte("c")} // no profile/root yet
	if err := a.store.CreateArr(ctx, sonarr); err != nil {
		t.Fatal(err)
	}
	// /connections lands on the first connection; every connection page carries the same list.
	w := request(h, "GET", "/connections", nil, session, csrf)
	if w.Code != 303 || w.Header().Get("Location") != "/settings/plex#add-server" {
		t.Fatalf("connections: %d %q", w.Code, w.Header().Get("Location"))
	}
	for _, path := range []string{"/settings/plex", "/settings/acquisition?id=s", "/settings/jellyfin"} {
		body := request(h, "GET", path, nil, session, csrf).Body.String()
		for _, want := range []string{`aria-label="Connections"`, "Sonarr 1080", "Choose a quality profile and root folder", "Radarr", "Not connected"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: list missing %q", path, want)
			}
		}
	}
	// The unfinished downloader is something that needs the administrator.
	if page := request(h, "GET", "/needs-input", nil, session, csrf).Body.String(); !strings.Contains(page, "Sonarr 1080: choose a quality profile and root folder") {
		t.Fatal("unfinished downloader not listed on Needs your input")
	}
}

func TestSetupWizardStepsAndFreshInstallRedirect(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	if !a.wizardActive(ctx) || a.setupNext(ctx, "/x") != "/welcome" {
		t.Fatal("a fresh install should be in the wizard")
	}
	if body := request(h, "GET", "/welcome", nil, session, csrf).Body.String(); !strings.Contains(body, "Connect your Plex server") {
		t.Fatal("step 1 should ask for Plex", body)
	}
	// Existing installs (they already have collections) never see the wizard.
	if _, err := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if a.wizardActive(ctx) || a.setupNext(ctx, "/x") != "/x" {
		t.Fatal("an install with collections must not be sent to the wizard")
	}
}
