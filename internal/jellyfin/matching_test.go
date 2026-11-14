package jellyfin

import "testing"

func TestExactMatchingAmbiguityMissingNumberingAndManualOverride(t *testing.T) {
	season, episode := 0, 1
	identity := Identity{Kind: "episode", Provider: "tvdb", ExternalID: "77", Season: 0, Episode: 1}
	item := Item{ID: libID, Kind: "episode", SeriesIDs: map[string]string{"tvdb": "77"}, Season: &season, Episode: &episode}
	if got := Resolve(identity, []Item{item}, ""); got.State != "matched" || got.ItemID != libID {
		t.Fatal(got)
	}
	if got := Resolve(identity, []Item{item, item}, ""); got.State != "unmatched" {
		t.Fatal(got)
	}
	item.Unavailable = true
	if got := Resolve(identity, []Item{item}, ""); got.State != "missing" {
		t.Fatal(got)
	}
	item.Unavailable = false
	item.Season = nil
	if got := Resolve(identity, []Item{item}, ""); got.State != "unmatched" {
		t.Fatal(got)
	}
	if got := Resolve(identity, []Item{item}, libID); got.State != "matched" {
		t.Fatal(got)
	}
	if got := Resolve(identity, nil, ""); got.State != "missing" {
		t.Fatal(got)
	}
	ids := providerIDs(map[string]string{"Tmdb": "1", "tmdb": "2", "Imdb": "tt1234567"})
	if ids["tmdb"] != "" || ids["imdb"] != "tt1234567" {
		t.Fatal(ids)
	}
}
