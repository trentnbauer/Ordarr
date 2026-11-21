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

func TestConnectionsFlagsAMissingRadarr(t *testing.T) {
	o := &overview{Arr: []store.ArrConnection{{ID: "s", Kind: "sonarr", Name: "Sonarr", ProfileID: 1, RootID: 1}}}
	var radarr, sonarr serviceCard
	for _, c := range connectionCards(o) {
		switch c.Name {
		case "Radarr":
			radarr = c
		case "Sonarr":
			sonarr = c
		}
	}
	if sonarr.State != "Connected" || sonarr.Warn {
		t.Fatalf("sonarr %+v", sonarr)
	}
	if radarr.State != "Not connected" || !radarr.Warn || !strings.Contains(radarr.Consequences, "movies won't be downloaded") {
		t.Fatalf("a missing Radarr must be flagged clearly: %+v", radarr)
	}
	if got, warn := connectionsSummary(&overview{Plex: []plexView{{}}, Arr: o.Arr}); got != "No Radarr" || !warn {
		t.Fatalf("sidebar summary %q", got)
	}
	both := &overview{Plex: []plexView{{}}, Arr: []store.ArrConnection{{Kind: "sonarr"}, {Kind: "radarr"}}}
	if got, warn := connectionsSummary(both); got != "OK" || warn {
		t.Fatalf("sidebar summary %q", got)
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
