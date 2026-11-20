package matching

import (
	"strconv"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

// Index answers Resolve for many entries against one catalogue without
// scanning every item per entry. Its results are identical to Resolve.
type Index struct {
	byKey map[string][]int // kind|provider|id[|season|episode] -> candidate items
	// Items of a kind (and, for episodes, at an exact season/episode) that lack
	// a provider identity. They make a candidate-free entry "unmatched".
	lacking map[string]bool
	// Episodes without season/episode numbering, keyed by series provider
	// identity ("" when the provider identity itself is missing).
	unnumbered map[string]bool
	byRating   map[string][]int // kind|ratingKey -> items
	items      []plex.IdentifiedMedia
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
	x := &Index{byKey: map[string][]int{}, lacking: map[string]bool{}, unnumbered: map[string]bool{}, byRating: map[string][]int{}, items: items}
	for i, item := range items {
		x.byRating[key(item.Kind, item.RatingKey)] = append(x.byRating[key(item.Kind, item.RatingKey)], i)
		ids := item.IDs
		position := []string{}
		if item.Kind == "episode" {
			ids = item.SeriesIDs
			if item.Season == nil || item.Episode == nil {
				for _, provider := range []string{"tmdb", "tvdb", "imdb"} {
					x.unnumbered[key(provider, ids[provider])] = true
				}
				continue
			}
			position = []string{strconv.Itoa(*item.Season), strconv.Itoa(*item.Episode)}
		}
		for _, provider := range []string{"tmdb", "tvdb", "imdb"} {
			scope := append([]string{item.Kind, provider}, position...)
			if ids[provider] == "" {
				x.lacking[key(scope...)] = true
				continue
			}
			k := key(append([]string{item.Kind, provider, ids[provider]}, position...)...)
			x.byKey[k] = append(x.byKey[k], i)
		}
	}
	return x
}

// Resolve is equivalent to Resolve(entry, items, manual) for the indexed items.
func (x *Index) Resolve(entry Identity, manual string) Result {
	var positions []int
	unknown := false
	if manual != "" {
		positions = x.byRating[key(entry.Kind, manual)]
	} else {
		position := []string{}
		if entry.Kind == "episode" {
			position = []string{strconv.Itoa(entry.Season), strconv.Itoa(entry.Episode)}
			if x.unnumbered[key(entry.Provider, "")] || x.unnumbered[key(entry.Provider, entry.ExternalID)] {
				unknown = true
			}
		}
		if x.lacking[key(append([]string{entry.Kind, entry.Provider}, position...)...)] {
			unknown = true
		}
		positions = x.byKey[key(append([]string{entry.Kind, entry.Provider, entry.ExternalID}, position...)...)]
	}
	candidates := make([]plex.IdentifiedMedia, 0, len(positions))
	for _, i := range positions {
		candidates = append(candidates, x.items[i])
	}
	return decide(candidates, unknown, manual)
}
