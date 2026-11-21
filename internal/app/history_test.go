package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
)

func TestCollapseRoutineGroupsChecksButNeverAcrossAChange(t *testing.T) {
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.Local)
	at := func(h int) int64 { return time.Date(2026, 11, 21, h, 0, 0, 0, time.Local).Unix() }
	check := func(h int) jobRow {
		return jobRow{When: at(h), Kind: "Source check", What: "MCU", Result: "Checked, no changes", Routine: true}
	}
	change := jobRow{When: at(12), Kind: "Source check", What: "MCU", Result: "Source applied."}
	other := jobRow{When: at(14), Kind: "Source check", What: "Other", Result: "Checked, no changes", Routine: true}
	failed := jobRow{When: at(15), Kind: "Playlist sync", What: "MCU", Result: "Failed · boom", Class: "warn"}
	// newest first: 17,16 checks, failure, other's check, 13 check, the change, 11,10,9 checks
	in := []jobRow{check(17), check(16), failed, other, check(13), change, check(11), check(10), check(9)}
	out := collapseRoutine(in, now)
	var got []string
	for _, r := range out {
		got = append(got, r.Result)
	}
	want := []string{
		"Checked 3× today, no changes", // 17, 16 and 13 fold: the failure is a different kind, the other collection is a different key
		"Failed · boom",
		"Checked, no changes", // the other collection's single check keeps its own row
		"Source applied.",     // the change ends the first run, so it is never folded in
		"Checked 3× today, no changes",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if out[0].Count != 3 || out[0].When != at(17) || out[0].Oldest != at(13) {
		t.Fatalf("group bounds wrong: %+v", out[0])
	}
}

func TestSpanWording(t *testing.T) {
	now := time.Date(2026, 11, 21, 18, 0, 0, 0, time.Local)
	d := func(day, h int) int64 { return time.Date(2026, 11, day, h, 0, 0, 0, time.Local).Unix() }
	for _, c := range []struct {
		o, n int64
		want string
	}{{d(21, 1), d(21, 9), "today"}, {d(19, 1), d(19, 9), "on 19 Nov"}, {d(14, 1), d(21, 9), "since 14 Nov"}} {
		if got := span(c.o, c.n, now); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestRoutineSourceClassification(t *testing.T) {
	for _, c := range []struct {
		j    store.Job
		want bool
	}{
		{store.Job{Kind: "source", State: "succeeded", Message: "Source unchanged."}, true},
		{store.Job{Kind: "source", State: "succeeded", Message: "Completed"}, true}, // recorded before outcomes were stored
		{store.Job{Kind: "source", State: "succeeded", Message: "Source applied."}, false},
		{store.Job{Kind: "source", State: "succeeded", Message: "Update awaits approval."}, false},
		{store.Job{Kind: "source", State: "failed", Message: "Completed"}, false},
		{store.Job{Kind: "sync", State: "succeeded", Message: "Completed"}, false},
	} {
		if routineSource(c.j) != c.want {
			t.Errorf("%+v: want %v", c.j, c.want)
		}
	}
}

func TestStatusLine(t *testing.T) {
	v := &collectionView{
		Destinations: []destView{{Destination: store.Destination{RemoteID: "r"}}},
		Avail:        availability{Ready: true, Found: 67},
		Rows: []orderRow{
			{Status: rowStatus{Label: statusDownload}}, {Status: rowStatus{Label: statusDownload}},
			{Status: rowStatus{Label: statusPick}}, {Status: rowStatus{Label: statusNotFound}},
		},
	}
	got, warn := statusLine(v, 320)
	want := "In Plex as a playlist · 67/320 playable · 2 downloading · 2 need your input"
	if got != want || !warn {
		t.Fatalf("got %q (warn %v)", got, warn)
	}
	// Nothing published and not yet checked.
	v = &collectionView{HasServer: true, Server: plexView{Name: "Home"}}
	if got, warn = statusLine(v, 5); got != "Not in Plex yet · checking Home" || !warn {
		t.Fatalf("got %q", got)
	}
}

// The merged Advanced tab renders, and the old tab names still resolve to it.
func TestAdvancedTabRendersAndOldTabsRedirectThere(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	id, err := a.store.ImportCollection(context.Background(), store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range []string{"advanced", "source", "publishing", "history", "settings"} {
		w := request(h, "GET", "/collections/"+id+"?tab="+tab, nil, session, csrf)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, "Recent activity") || !strings.Contains(body, "Delete this collection") || !strings.Contains(body, "Connect Sonarr or Radarr") {
			t.Fatalf("tab %s: %d", tab, w.Code)
		}
	}
	body := request(h, "GET", "/collections/"+id, nil, session, csrf).Body.String()
	if !strings.Contains(body, `id="collection-status"`) || strings.Contains(body, `class="stats top"`) || strings.Contains(body, ">Missing<") {
		t.Fatal("header still has stat tiles or a Missing tab")
	}
}
