package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// The History tab lists the download requests made for the collection's items, newest first among the
// syncs and checks, each with the Radarr/Sonarr instance it went to and who asked.
func TestHistoryTabListsRequestsWithTheirInstance(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	for _, c := range []store.ArrConnection{
		{ID: "r", Kind: "radarr", Name: "Radarr 1080", BaseURL: "http://r:7878", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
		{ID: "r4", Kind: "radarr", Name: "Radarr 4K", BaseURL: "http://r4:7878", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
		{ID: "s", Kind: "sonarr", Name: "Sonarr 1080", BaseURL: "http://s:8989", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
	} {
		if err := a.store.CreateArr(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	collection, err := a.store.ImportCollection(ctx, store.Collection{Title: "Saga", Entries: []store.Entry{
		{ID: "a", Kind: "movie", Title: "Direct Movie", Provider: "tmdb", ExternalID: "11"},
		{ID: "b", Kind: "movie", Title: "Listed By IMDb", Provider: "imdb", ExternalID: "tt0120915"},
		{ID: "c", Kind: "episode", Title: "Show - S01E01: Pilot", Provider: "tvdb", ExternalID: "700", Season: 1, Episode: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The IMDb-listed movie was looked up to TMDB 1893, so a request for 1893 belongs to this collection.
	if err = a.store.SaveAlias(ctx, store.StoredAlias{Kind: "movie", Provider: "imdb", ID: "tt0120915", IMDB: "tt0120915", TMDB: "1893", Title: "Listed By IMDb", Found: true}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	conn := func(id string) store.ArrConnection { c, _ := a.store.ArrConnection(ctx, id); return c }
	req := func(connection string, id int, title, origin, state, message string, at int64) {
		t.Helper()
		rid, err := a.store.ProposeAcquisition(ctx, conn(connection), id, title, origin, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.store.DB.Exec("UPDATE acquisition_requests SET state=?,message=?,created_at=?,attempted_at=? WHERE id=?", state, message, at, at, rid); err != nil {
			t.Fatal(err)
		}
	}
	req("r", 11, "Direct Movie", "automatic", "completed", "", now-300)
	req("r4", 1893, "Listed By IMDb", "manual", "completed", "", now-200) // sent to the 4K instance, by hand
	req("s", 700, "Show", "automatic", "failed", "Lookup found no exact match.", now-100)
	req("r", 999, "Unrelated Movie", "automatic", "completed", "", now-50) // not in this collection

	body := request(h, "GET", "/collections/"+collection+"?tab=history", nil, session, csrf).Body.String()
	for _, want := range []string{
		"Direct Movie", "Radarr 1080", "Requested from Radarr 1080 (Radarr) · asked automatically",
		"Listed By IMDb", "Radarr 4K", "Requested from Radarr 4K (Radarr) · you asked",
		"Show", "Sonarr 1080", "Sonarr 1080 couldn&#39;t find or add it", "Lookup found no exact match", "Request now",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("history is missing %q", want)
		}
	}
	if strings.Contains(body, "Unrelated Movie") {
		t.Error("a request for a title that is not in this collection was listed")
	}
	// Newest first: the failed Sonarr request, then the 4K one, then the first.
	if i, j, k := strings.Index(body, "Sonarr 1080</span>"), strings.Index(body, "Radarr 4K</span>"), strings.Index(body, "Radarr 1080</span>"); !(i >= 0 && i < j && j < k) {
		t.Errorf("not newest first: %d %d %d", i, j, k)
	}
	// Requests are not mixed into other collections.
	other, _ := a.store.ImportCollection(ctx, store.Collection{Title: "Other", Entries: []store.Entry{{ID: "z", Kind: "movie", Title: "Different", Provider: "tmdb", ExternalID: "555"}}})
	if b := request(h, "GET", "/collections/"+other+"?tab=history", nil, session, csrf).Body.String(); strings.Contains(b, "Direct Movie") || strings.Contains(b, "Radarr 4K") {
		t.Error("another collection shows these requests")
	}
}

func TestRequestRowWording(t *testing.T) {
	inst := arrLabel{Name: "Radarr 1080", Kind: "Radarr"}
	for state, want := range map[string]string{
		"completed": "Requested from Radarr 1080 (Radarr)",
		"approved":  "Being sent to Radarr 1080",
		"running":   "Being sent to Radarr 1080",
		"pending":   "Waiting for your approval to send to Radarr 1080",
		"uncertain": "outcome is uncertain: check Radarr 1080",
		"failed":    "Radarr 1080 couldn't find or add it",
		"declined":  "Declined: not sent to Radarr 1080",
	} {
		got := requestRow(store.AcquisitionRequest{State: state, Origin: "automatic", Title: "T"}, inst)
		if !strings.Contains(got.Result, want) || got.Instance != "Radarr 1080" || got.What != "T" || got.Kind != "Request" {
			t.Errorf("%s: %+v", state, got)
		}
	}
	if got := requestRow(store.AcquisitionRequest{State: "completed", Origin: "manual", Title: "T"}, arrLabel{}); !strings.Contains(got.Result, "a removed instance") || !strings.Contains(got.Result, "you asked") {
		t.Errorf("removed instance: %+v", got)
	}
}
