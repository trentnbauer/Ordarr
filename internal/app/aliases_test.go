package app

import (
	"testing"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Radarr/Sonarr libraries translate between providers, so a Plex item that only has an IMDb ID is
// found for an entry listed by TMDB or TVDB ID, and an unrelated ID-less item no longer blocks it.
func TestAliasesFromRadarrAndSonarrDriveMatching(t *testing.T) {
	al := buildAliases([]store.ArrConnection{
		{Kind: "radarr", Snapshot: arr.Snapshot{Media: []arr.Media{{TMDB: 1724, IMDB: "tt0800080", Title: "The Incredible Hulk"}, {TMDB: 5}}}},
		{Kind: "sonarr", Snapshot: arr.Snapshot{Media: []arr.Media{{TVDB: 443358, TMDB: 9, IMDB: "tt99", Title: "Eyes of Wakanda"}}}},
	}, nil)
	hulk := store.Entry{Kind: "movie", Provider: "tmdb", ExternalID: "1724", Title: "The Incredible Hulk"}
	id := identityFor(hulk, al)
	if id.Aliases["imdb"] != "tt0800080" || len(id.Aliases) != 1 || id.Aliases["tmdb"] != "" {
		t.Fatalf("aliases %v (the entry's own provider must not be repeated)", id.Aliases)
	}
	if len(id.Titles) == 0 || id.Titles[0] != "The Incredible Hulk" {
		t.Fatalf("titles %v", id.Titles)
	}
	// A movie and a series can share a numeric ID: they live in separate namespaces.
	series := store.Entry{Kind: "episode", Provider: "tvdb", ExternalID: "443358", Season: 1, Episode: 1, Title: "Eyes of Wakanda - S01E01: Into the Lion's Den"}
	sid := identityFor(series, al)
	if sid.Aliases["imdb"] != "tt99" || sid.Aliases["tmdb"] != "9" || sid.Titles[0] != "Eyes of Wakanda" {
		t.Fatalf("series identity %+v", sid)
	}
	clash := store.Entry{Kind: "movie", Provider: "tmdb", ExternalID: "9", Title: "Other"}
	if got := identityFor(clash, al); len(got.Aliases) != 0 {
		t.Fatalf("a series' TMDB ID leaked into movie matching: %v", got.Aliases)
	}
	// End to end through the index: IMDb-only Plex item is found; an ID-less other title is ignored.
	items := []plex.IdentifiedMedia{
		{MediaItem: plex.MediaItem{RatingKey: "1", Kind: "movie", Title: "The Incredible Hulk"}, IDs: map[string]string{"imdb": "tt0800080"}, Playable: true},
		{MediaItem: plex.MediaItem{RatingKey: "2", Kind: "movie", Title: "Saw VI"}, Playable: true},
	}
	x := matching.NewIndex(items)
	if r := x.Resolve(id, ""); r.State != "available" || r.RatingKey != "1" {
		t.Fatal("IMDb-only Plex item not found through the Radarr alias", r)
	}
	other := identityFor(store.Entry{Kind: "movie", Provider: "tmdb", ExternalID: "424242", Title: "Something Else"}, al)
	if r := x.Resolve(other, ""); r.State != "missing" {
		t.Fatal("an ID-less item with another title blocked the entry", r)
	}
}
