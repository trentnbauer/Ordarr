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

func TestDiffDetail(t *testing.T) {
	doc := func(ids ...string) Document {
		d := Document{Version: 1, Title: "Order"}
		for _, id := range ids {
			title := "Film " + id
			if id == "c2" {
				id, title = "c", "Film c (fixed)"
			}
			d.Entries = append(d.Entries, Entry{ID: id, Kind: "movie", Title: title, Provider: "tmdb", ExternalID: "1"})
		}
		return d
	}
	changes := DiffDetail(doc("a", "b", "c", "d", "e"), doc("a", "x", "c2", "d", "b"))
	want := []Change{{"added", "Film x", 0, 2}, {"changed", "Film c (fixed)", 3, 3}, {"moved", "Film b", 2, 5}, {"removed", "Film e", 5, 0}}
	if len(changes) != len(want) {
		t.Fatalf("%+v", changes)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("change %d: %+v want %+v", i, changes[i], want[i])
		}
	}
}
