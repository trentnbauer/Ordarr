package matching

import (
	"sort"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

// Index answers Resolve for many entries against one catalogue without scanning every item per
// entry. Only items that share an ID with the entry, or have the same title, can change the
// answer, so the index finds those and hands them to Resolve: its results are identical.
type Index struct {
	byID     map[string][]int // kind|provider|id -> items carrying that ID (an episode's series ID)
	byTitle  map[string][]int // kind|normalised title (an episode's series title) -> items
	byRating map[string][]int // kind|ratingKey -> items
	items    []plex.IdentifiedMedia
}

func key(parts ...string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\x00"
		}
		out += p
	}
	return out
}
func NewIndex(items []plex.IdentifiedMedia) *Index {
	x := &Index{byID: map[string][]int{}, byTitle: map[string][]int{}, byRating: map[string][]int{}, items: items}
	for i, item := range items {
		x.byRating[key(item.Kind, item.RatingKey)] = append(x.byRating[key(item.Kind, item.RatingKey)], i)
		ids := item.IDs
		if item.Kind == "episode" {
			ids = item.SeriesIDs
		}
		for provider, id := range ids {
			if id != "" {
				k := key(item.Kind, provider, id)
				x.byID[k] = append(x.byID[k], i)
			}
		}
		if t := Normalize(itemTitle(item)); t != "" {
			k := key(item.Kind, t)
			x.byTitle[k] = append(x.byTitle[k], i)
		}
	}
	return x
}

// Resolve is equivalent to Resolve(entry, items, manual) for the indexed items.
func (x *Index) Resolve(entry Identity, manual string) Result {
	var positions []int
	if manual != "" {
		positions = x.byRating[key(entry.Kind, manual)]
	} else {
		seen := map[int]bool{}
		add := func(list []int) {
			for _, i := range list {
				if !seen[i] {
					seen[i] = true
					positions = append(positions, i)
				}
			}
		}
		for provider, id := range entry.idSet() {
			add(x.byID[key(entry.Kind, provider, id)])
		}
		for _, t := range entry.Titles {
			if n := Normalize(t); n != "" {
				add(x.byTitle[key(entry.Kind, n)])
			}
		}
		sort.Ints(positions) // Resolve walks items in catalogue order
	}
	subset := make([]plex.IdentifiedMedia, 0, len(positions))
	for _, i := range positions {
		subset = append(subset, x.items[i])
	}
	return Resolve(entry, subset, manual)
}
