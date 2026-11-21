package matching

import (
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

func movie(rk, title string, ids map[string]string) plex.IdentifiedMedia {
	return plex.IdentifiedMedia{MediaItem: plex.MediaItem{RatingKey: rk, Kind: "movie", Title: title}, IDs: ids, Playable: true}
}

func TestExactIdentityAmbiguityMissingAndManualResolution(t *testing.T) {
	e := Identity{Kind: "movie", Provider: "tmdb", ExternalID: "1", Titles: []string{"Same name"}}
	items := []plex.IdentifiedMedia{movie("1", "Same name", map[string]string{"tmdb": "1"})}
	if r := Resolve(e, items, ""); r.State != "available" || r.RatingKey != "1" {
		t.Fatal(r)
	}
	if r := Resolve(e, append(items, items[0]), ""); r.State != "unmatched" {
		t.Fatal("ambiguous match guessed", r)
	}
	// An item with no IDs and the same title could be this movie: offered for a pick, not ignored.
	items[0].IDs = nil
	r := Resolve(e, items, "")
	if r.State != "unmatched" || len(r.Candidates) != 1 || r.Candidates[0].RatingKey != "1" {
		t.Fatal("a same-titled item without IDs should be offered", r)
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

// The bug this replaces: one Plex movie with no IDs made every entry "needs a pick".
func TestUnrelatedItemsWithoutIDsDoNotBlockAnEntry(t *testing.T) {
	items := []plex.IdentifiedMedia{
		movie("1", "Saw VI", nil),
		movie("2", "Die Hard 2", nil),
		movie("3", "Black Panther", map[string]string{"tmdb": "284054"}),
	}
	panther := Identity{Kind: "movie", Provider: "tmdb", ExternalID: "284054", Titles: []string{"Black Panther"}}
	if r := Resolve(panther, items, ""); r.State != "available" || r.RatingKey != "3" {
		t.Fatal("an exact ID match must win over unrelated ID-less items", r)
	}
	hulk := Identity{Kind: "movie", Provider: "tmdb", ExternalID: "1724", Titles: []string{"The Incredible Hulk"}}
	if r := Resolve(hulk, items, ""); r.State != "missing" {
		t.Fatal("ID-less items with other titles must not make a missing movie ambiguous", r)
	}
}

// Radarr/Sonarr tell us a title's other IDs, so a Plex item that only carries one of them matches.
func TestAliasesMatchAcrossProviders(t *testing.T) {
	items := []plex.IdentifiedMedia{movie("9", "The First Video Game", map[string]string{"imdb": "tt12382278"})}
	e := Identity{Kind: "movie", Provider: "tmdb", ExternalID: "777", Titles: []string{"The First Video Game"}}
	if r := Resolve(e, items, ""); r.State != "unmatched" {
		t.Fatal("without an alias the item can only be offered by title", r)
	}
	e.Aliases = map[string]string{"imdb": "tt12382278"}
	if r := Resolve(e, items, ""); r.State != "available" || r.RatingKey != "9" {
		t.Fatal("IMDb alias did not match", r)
	}
	// A different IMDb ID is a different movie, whatever the title says.
	e.Aliases = map[string]string{"imdb": "tt0000001"}
	if r := Resolve(e, items, ""); r.State != "missing" {
		t.Fatal("a conflicting ID must rule the item out", r)
	}
	// A conflict on any shared provider rules an item out even if another provider agrees.
	items = []plex.IdentifiedMedia{movie("5", "X", map[string]string{"tmdb": "1", "imdb": "tt2"})}
	e = Identity{Kind: "movie", Provider: "tmdb", ExternalID: "1", Aliases: map[string]string{"imdb": "tt3"}}
	if r := Resolve(e, items, ""); r.State == "available" {
		t.Fatal("contradictory IDs were accepted", r)
	}
}

func TestEpisodesUseSeriesAliasesAndSeriesTitles(t *testing.T) {
	s, e := 1, 1
	ep := plex.IdentifiedMedia{MediaItem: plex.MediaItem{RatingKey: "7", Kind: "episode", SeriesTitle: "Eyes of Wakanda", Season: &s, Episode: &e}, SeriesIDs: map[string]string{"imdb": "tt99"}, Playable: true}
	entry := Identity{Kind: "episode", Provider: "tvdb", ExternalID: "443358", Season: 1, Episode: 1, Titles: []string{"Eyes of Wakanda"}}
	if r := Resolve(entry, []plex.IdentifiedMedia{ep}, ""); r.State != "unmatched" {
		t.Fatal("same series title, no shared ID: offer, don't accept", r)
	}
	entry.Aliases = map[string]string{"imdb": "tt99"}
	if r := Resolve(entry, []plex.IdentifiedMedia{ep}, ""); r.State != "available" {
		t.Fatal("series IMDb alias did not match", r)
	}
	// An ID-less series with another title at the same numbers is not a conflict.
	other := ep
	other.SeriesTitle, other.SeriesIDs, other.RatingKey = "Some Other Show", nil, "8"
	entry.Aliases = nil
	if r := Resolve(entry, []plex.IdentifiedMedia{other}, ""); r.State != "missing" {
		t.Fatal("an unrelated ID-less episode blocked the entry", r)
	}
}

func TestNormalizeIgnoresCasePunctuationAndSpacing(t *testing.T) {
	if Normalize("Ant-Man and the Wasp: Quantumania") != Normalize("ant man AND the wasp quantumania") {
		t.Fatal("titles differing only in punctuation should compare equal")
	}
	if Normalize("") != "" || Normalize("!!!") != "" {
		t.Fatal("empty titles must not compare equal to anything")
	}
}
