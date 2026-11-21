package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// Only the parts of the status line that need attention are flagged, not the whole line.
func TestStatusLineFlagsOnlyWhatNeedsAttention(t *testing.T) {
	v := &collectionView{
		Destinations: []destView{{Destination: store.Destination{RemoteID: "r"}}},
		Avail:        availability{Ready: true, Found: 6},
		Rows:         []orderRow{{Status: rowStatus{Label: statusDownload}}, {Status: rowStatus{Label: statusPick}}},
	}
	parts := statusParts(v, 20)
	flagged := map[string]bool{}
	for _, p := range parts {
		flagged[p.Text] = p.Warn
	}
	if flagged["In Plex as a playlist"] || flagged["6/20 playable"] || flagged["1 downloading"] || !flagged["1 need your input"] {
		t.Fatalf("flags: %v", flagged)
	}
	if line, warn := statusLine(v, 20); !warn || line != "In Plex as a playlist · 6/20 playable · 1 downloading · 1 need your input" {
		t.Fatalf("%q %v", line, warn)
	}
}

// A row only gets a second line when there is something to do or worry about; "Downloading" and
// similar progress states keep their explanation as a tooltip instead of doubling the row's height.
func TestOrderRowsOnlyGetAnExtraLineWhenThereIsSomethingToDo(t *testing.T) {
	a, _ := fixture(t)
	mk := func(id string) store.Entry {
		return store.Entry{ID: id, Kind: "movie", Provider: "tmdb", ExternalID: "1", Title: "Movie " + id}
	}
	c := store.Collection{ID: "c1", Title: "T", Revision: 1, Entries: []store.Entry{mk("a"), mk("b"), mk("c")}}
	v := &collectionView{Tab: "order", HasServer: true, Server: plexView{ID: "p", Name: "Plex"}, Rows: []orderRow{
		{Entry: c.Entries[0], Position: 1, State: "missing", Status: rowStatus{Label: statusDownload, Detail: "In the Sonarr download queue."}},
		{Entry: c.Entries[1], Position: 2, State: "missing", Status: rowStatus{Label: statusNotAsked, Detail: "Not downloading from Radarr for this collection.", RequestVia: "arr1"}},
		{Entry: c.Entries[2], Position: 3, State: "missing", Status: rowStatus{Label: statusNotFound, Detail: "Radarr couldn't find it.", Warn: true}},
	}}
	var out bytes.Buffer
	if err := a.templates.ExecuteTemplate(&out, "tab-order", page{CSRF: "t", Collection: c, Detail: v}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Count(html, `class="rowextra"`) != 2 {
		t.Fatalf("expected an extra line only for the two rows with something to do, got %d", strings.Count(html, `class="rowextra"`))
	}
	if strings.Contains(html, "<span>In the Sonarr download queue.</span>") {
		t.Error("a progress state repeated its explanation as a second line")
	}
	if !strings.Contains(html, `title="In the Sonarr download queue."`) {
		t.Error("the explanation should remain available as a tooltip")
	}
}
