package jellyfin

import "strconv"

// Index answers Resolve for many entries against one catalogue without
// scanning every item per entry. Its results are identical to Resolve.
type Index struct {
	items      []Item
	byKey      map[string][]int // kind|provider|id[|season|episode]
	scope      map[string]int   // kind[|season|episode] -> items in scope
	scoped     map[string]int   // scope|provider -> items in scope with that provider ID
	unnumbered int              // episodes without season/episode numbering
	unnumProv  map[string]int   // provider -> unnumbered episodes with that provider ID
	unnumIDs   map[string]bool  // provider|id among unnumbered episodes
	byID       map[string][]int // kind|item ID
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

func NewIndex(items []Item) *Index {
	x := &Index{items: items, byKey: map[string][]int{}, scope: map[string]int{}, scoped: map[string]int{}, unnumProv: map[string]int{}, unnumIDs: map[string]bool{}, byID: map[string][]int{}}
	for i, item := range items {
		x.byID[key(item.Kind, item.ID)] = append(x.byID[key(item.Kind, item.ID)], i)
		ids := item.IDs
		scope := []string{item.Kind}
		if item.Kind == "episode" {
			ids = item.SeriesIDs
			if item.Season == nil || item.Episode == nil {
				x.unnumbered++
				for p, v := range ids {
					if v != "" {
						x.unnumProv[p]++
						x.unnumIDs[key(p, v)] = true
					}
				}
				continue
			}
			scope = append(scope, strconv.Itoa(*item.Season), strconv.Itoa(*item.Episode))
		}
		s := key(scope...)
		x.scope[s]++
		for p, v := range ids {
			if v == "" {
				continue
			}
			x.scoped[key(s, p)]++
			k := key(append([]string{item.Kind, p, v}, scope[1:]...)...)
			x.byKey[k] = append(x.byKey[k], i)
		}
	}
	return x
}

// Resolve is equivalent to Resolve(entry, items, manual) for the indexed items.
func (x *Index) Resolve(entry Identity, manual string) Match {
	var positions []int
	unknown := false
	if manual != "" {
		positions = x.byID[key(entry.Kind, manual)]
	} else {
		scope := []string{entry.Kind}
		if entry.Kind == "episode" {
			scope = append(scope, strconv.Itoa(entry.Season), strconv.Itoa(entry.Episode))
			if x.unnumbered > x.unnumProv[entry.Provider] || x.unnumIDs[key(entry.Provider, entry.ExternalID)] {
				unknown = true
			}
		}
		s := key(scope...)
		if x.scope[s] > x.scoped[key(s, entry.Provider)] {
			unknown = true
		}
		if entry.ExternalID != "" {
			positions = x.byKey[key(append([]string{entry.Kind, entry.Provider, entry.ExternalID}, scope[1:]...)...)]
		}
	}
	candidates := make([]Item, 0, len(positions))
	for _, i := range positions {
		candidates = append(candidates, x.items[i])
	}
	return decide(candidates, unknown, manual)
}
