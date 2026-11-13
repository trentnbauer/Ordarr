package portable

import "testing"

func TestSourceDiffIncludesReorderingAndChanges(t *testing.T) {
	old := Document{Version: 1, Title: "Order", Entries: []Entry{{ID: "a", Kind: "movie", Title: "A", Provider: "tmdb", ExternalID: "1"}, {ID: "b", Kind: "movie", Title: "B", Provider: "tmdb", ExternalID: "2"}, {ID: "gone", Kind: "movie", Title: "Old", Provider: "tmdb", ExternalID: "3"}}}
	next := old
	next.Entries = []Entry{old.Entries[1], old.Entries[0], {ID: "new", Kind: "movie", Title: "New", Provider: "tmdb", ExternalID: "4"}}
	next.Entries[0].Title = "Edited"
	diff := Diff(old, next)
	if diff.Added != 1 || diff.Removed != 1 || diff.Changed != 1 || !diff.Reordered {
		t.Fatal(diff)
	}
}
