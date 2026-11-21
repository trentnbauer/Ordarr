// Package matching makes conservative canonical-entry to destination decisions.
package matching

import (
	"strings"
	"unicode"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

// Identity is what is known about one entry of a collection.
type Identity struct {
	Kind, Provider, ExternalID string
	Season, Episode            int
	// Aliases are the same title's IDs in other providers (for example the IMDb ID that Radarr
	// reports for a TMDB ID), so a Plex item that only carries one of them is still recognised.
	Aliases map[string]string
	// Titles are labels for the title. They never make a match on their own: they only decide
	// whether a Plex item with no usable ID could plausibly be this entry and should therefore
	// be offered for a pick instead of being ignored.
	Titles []string
}
type Result struct {
	State, RatingKey, Reason string
	Candidates               []plex.IdentifiedMedia
}

// idSet is every provider ID known for the entry.
func (e Identity) idSet() map[string]string {
	ids := map[string]string{}
	for p, v := range e.Aliases {
		if v != "" {
			ids[p] = v
		}
	}
	ids[e.Provider] = e.ExternalID
	return ids
}

const (
	idMatch    = iota // at least one provider ID agrees and none disagrees
	idConflict        // an ID disagrees: this is a different title
	idUnknown         // no shared provider to compare: the item says nothing either way
)

// compare says what an item's IDs say about the entry's.
func compare(item, entry map[string]string) int {
	matched, conflict := false, false
	for provider, want := range entry {
		have := item[provider]
		if have == "" {
			continue
		}
		if have == want {
			matched = true
		} else {
			conflict = true
		}
	}
	switch {
	case matched && !conflict:
		return idMatch
	case conflict:
		return idConflict
	}
	return idUnknown
}

// Normalize makes titles comparable: case, punctuation and spacing are ignored.
func Normalize(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// itemTitle is the title compared with an entry's: a movie's own, or an episode's series.
func itemTitle(item plex.IdentifiedMedia) string {
	if item.Kind == "episode" {
		return item.SeriesTitle
	}
	return item.Title
}

// related reports whether an item with no usable ID is plausibly the entry, by exact
// normalised title. This only ever raises "needs a pick"; it never accepts a match.
func related(entry Identity, item plex.IdentifiedMedia) bool {
	have := Normalize(itemTitle(item))
	if have == "" {
		return false
	}
	for _, t := range entry.Titles {
		if Normalize(t) == have {
			return true
		}
	}
	return false
}

func Resolve(entry Identity, items []plex.IdentifiedMedia, manual string) Result {
	candidates := []plex.IdentifiedMedia{}
	var possible []plex.IdentifiedMedia // no usable ID, but the same title
	unknown := false                    // cannot be told apart from the entry, with nothing to offer
	want := entry.idSet()
	for _, item := range items {
		if item.Kind != entry.Kind {
			continue
		}
		if manual != "" {
			if item.RatingKey == manual {
				candidates = append(candidates, item)
			}
			continue
		}
		ids := item.IDs
		if entry.Kind == "episode" {
			ids = item.SeriesIDs
			if item.Season == nil || item.Episode == nil {
				// An episode without numbering could be any episode of its series.
				if rel := compare(ids, want); rel == idMatch || rel == idUnknown && related(entry, item) {
					unknown = true
				}
				continue
			}
			if *item.Season != entry.Season || *item.Episode != entry.Episode {
				continue
			}
		}
		switch compare(ids, want) {
		case idMatch:
			candidates = append(candidates, item)
		case idUnknown:
			if related(entry, item) {
				possible = append(possible, item)
			}
		}
	}
	return decide(candidates, possible, unknown, manual)
}
func decide(candidates, possible []plex.IdentifiedMedia, unknown bool, manual string) Result {
	if len(candidates) == 0 {
		if manual == "" && (len(possible) > 0 || unknown) {
			return Result{State: "unmatched", Reason: "A Plex item with the same title has no provider IDs; choose it if it is this one.", Candidates: possible}
		}
		state := "missing"
		reason := "No exact provider identity was found in the complete catalogue."
		if manual != "" {
			reason = "The manually selected Plex item is no longer indexed."
		}
		return Result{State: state, Reason: reason}
	}
	if len(candidates) > 1 {
		return Result{State: "unmatched", Reason: "Several exact candidates exist; choose one explicitly.", Candidates: candidates}
	}
	item := candidates[0]
	if !item.Playable {
		return Result{State: "missing", RatingKey: item.RatingKey, Reason: "Plex did not confirm an accessible playable file.", Candidates: candidates}
	}
	return Result{State: "available", RatingKey: item.RatingKey, Reason: "Verified exact provider identity or explicit manual mapping.", Candidates: candidates}
}
