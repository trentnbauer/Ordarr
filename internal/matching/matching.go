// Package matching makes conservative canonical-entry to destination decisions.
package matching

import (
	"github.com/trentnbauer/Ordarr/internal/plex"
)

type Identity struct {
	Kind, Provider, ExternalID string
	Season, Episode            int
}
type Result struct {
	State, RatingKey, Reason string
	Candidates               []plex.IdentifiedMedia
}

func Resolve(entry Identity, items []plex.IdentifiedMedia, manual string) Result {
	candidates := []plex.IdentifiedMedia{}
	unknown := false
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
				if ids[entry.Provider] == "" || ids[entry.Provider] == entry.ExternalID {
					unknown = true
				}
				continue
			}
			if *item.Season != entry.Season || *item.Episode != entry.Episode {
				continue
			}
		}
		if ids[entry.Provider] == "" {
			unknown = true
		}
		if ids[entry.Provider] == entry.ExternalID {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		if manual == "" && unknown {
			return Result{State: "unmatched", Reason: "Provider identity is unavailable for possible media; resolve explicitly."}
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
