package matching

import (
	"github.com/trentnbauer/Ordarr/internal/plex"
	"testing"
)

func TestExactIdentityAmbiguityMissingAndManualResolution(t *testing.T) {
	e := Identity{Kind: "movie", Provider: "tmdb", ExternalID: "1"}
	items := []plex.IdentifiedMedia{{MediaItem: plex.MediaItem{RatingKey: "1", Kind: "movie", Title: "Same name"}, IDs: map[string]string{"tmdb": "1"}, Playable: true}}
	if r := Resolve(e, items, ""); r.State != "available" || r.RatingKey != "1" {
		t.Fatal(r)
	}
	if r := Resolve(e, append(items, items[0]), ""); r.State != "unmatched" {
		t.Fatal("ambiguous match guessed", r)
	}
	items[0].IDs = nil
	if r := Resolve(e, items, ""); r.State != "unmatched" {
		t.Fatal("unknown provider identity reported missing", r)
	}
	if r := Resolve(e, items, "1"); r.State != "available" {
		t.Fatal("explicit resolution failed", r)
	}
	items[0].Playable = false
	if r := Resolve(e, items, "1"); r.State != "missing" {
		t.Fatal(r)
	}
	if r := Resolve(e, nil, ""); r.State != "missing" {
		t.Fatal(r)
	}
	season, ep := 0, 2
	items = []plex.IdentifiedMedia{{MediaItem: plex.MediaItem{RatingKey: "2", Kind: "episode", Season: &season, Episode: &ep}, SeriesIDs: map[string]string{"tvdb": "42"}, Playable: true}}
	if r := Resolve(Identity{Kind: "episode", Provider: "tvdb", ExternalID: "42", Season: 0, Episode: 2}, items, ""); r.State != "available" {
		t.Fatal("series identity/specials lost", r)
	}
	if r := Resolve(Identity{Kind: "episode", Provider: "tvdb", ExternalID: "42", Season: 1, Episode: 2}, items, ""); r.State != "missing" {
		t.Fatal("wrong season matched", r)
	}
}
