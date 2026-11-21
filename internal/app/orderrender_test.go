package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Renders the Order tab with one row per status so template errors surface here, not in production.
func TestOrderTabRendersEveryRowStatus(t *testing.T) {
	a, _ := fixture(t)
	mk := func(id, title string) store.Entry {
		return store.Entry{ID: id, Kind: "movie", Provider: "tmdb", ExternalID: "1", Title: title}
	}
	candidate := plex.IdentifiedMedia{MediaItem: plex.MediaItem{RatingKey: "99", Title: "Candidate", Year: 1999}}
	c := store.Collection{ID: "c1", Title: "T", Revision: 3, Entries: []store.Entry{mk("a", "Playable"), mk("b", "Pick me"), mk("c", "Downloading"), mk("d", "Not asked")}}
	v := &collectionView{
		Tab: "order", HasServer: true, Server: plexView{ID: "p1", Name: "Home Plex"},
		Rows: []orderRow{
			{Entry: c.Entries[0], Position: 1, State: "available"},
			{Entry: c.Entries[1], Position: 2, State: "unmatched", Status: rowStatus{Label: statusPick, Detail: "More than one", Warn: true}, Picks: []plex.IdentifiedMedia{candidate}, Manual: true},
			{Entry: c.Entries[2], Position: 3, State: "missing", Status: rowStatus{Label: statusDownload, Detail: "In the queue"}},
			{Entry: c.Entries[3], Position: 4, State: "missing", Status: rowStatus{Label: statusNotAsked, RequestVia: "arr1"}},
		},
	}
	var out bytes.Buffer
	if err := a.templates.ExecuteTemplate(&out, "tab-order", page{CSRF: "tok", Collection: c, Detail: v}); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{"Needs a pick", "Downloading", "Not requested", "Request now", `action="/settings/acquisition/arr1/requests"`, "Candidate (1999)", "Clear pick", `name="rating_key" value="99"`} {
		if !strings.Contains(html, want) {
			t.Errorf("order tab missing %q", want)
		}
	}
	if strings.Contains(html, "Map to a Plex item") || strings.Contains(html, "rating key (find it") || strings.Contains(html, "Search Home Plex") || strings.Contains(html, `name="pick"`) {
		t.Error("the rating-key form or the library search is still present")
	}
}
