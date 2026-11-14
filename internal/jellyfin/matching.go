package jellyfin

// Identity uses series provider IDs for episode entries, never episode provider IDs.
type Identity struct {
	Kind, Provider, ExternalID string
	Season, Episode            int
}
type Match struct{ State, ItemID, Reason string }

func Resolve(entry Identity, items []Item, manual string) Match {
	candidates := []Item{}
	unknown := false
	for _, item := range items {
		if item.Kind != entry.Kind {
			continue
		}
		if manual != "" {
			if item.ID == manual {
				candidates = append(candidates, item)
			}
			continue
		}
		ids := item.IDs
		if item.Kind == "episode" {
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
	if len(candidates) > 1 {
		return Match{State: "unmatched", Reason: "Several exact candidates exist. Choose an explicit item ID."}
	}
	if len(candidates) == 0 {
		if unknown && manual == "" {
			return Match{State: "unmatched", Reason: "Possible media lacks provider identity or episode numbering. Resolve explicitly."}
		}
		return Match{State: "missing", Reason: "No exact identity exists in the complete catalogue; the canonical entry is retained."}
	}
	item := candidates[0]
	if item.Unavailable {
		return Match{State: "missing", ItemID: item.ID, Reason: "Jellyfin reports a placeholder, offline item or denied playback."}
	}
	return Match{State: "matched", ItemID: item.ID, Reason: "Exact identity or explicit mapping found. Playback has not been verified."}
}
