package plex

import (
	"context"
	"net/url"
	"strings"
)

type IdentifiedMedia struct {
	MediaItem
	IDs       map[string]string `json:"ids"`
	SeriesIDs map[string]string `json:"seriesIds"`
	Playable  bool              `json:"playable"`
}
type metadataDetail struct {
	MediaItem
	GUID  string `json:"guid"`
	GUIDs []struct {
		ID string `json:"id"`
	} `json:"Guid"`
	Media []struct {
		Part []struct {
			Key        string `json:"key"`
			Exists     *bool  `json:"exists"`
			Accessible *bool  `json:"accessible"`
		} `json:"Part"`
	} `json:"Media"`
}

func providerIDs(guid string, guids []struct {
	ID string `json:"id"`
}) map[string]string {
	ids := map[string]string{}
	values := []string{guid}
	for _, g := range guids {
		values = append(values, g.ID)
	}
	ambiguous := map[string]bool{}
	for _, value := range values {
		u, err := url.Parse(value)
		if err != nil {
			continue
		}
		provider := u.Scheme
		switch provider {
		case "com.plexapp.agents.imdb":
			provider = "imdb"
		case "com.plexapp.agents.thetvdb":
			provider = "tvdb"
		case "com.plexapp.agents.themoviedb":
			provider = "tmdb"
		}
		if provider != "tmdb" && provider != "tvdb" && provider != "imdb" {
			continue
		}
		id := u.Host
		if id == "" {
			id = strings.TrimPrefix(u.Path, "/")
		}
		if strings.Contains(id, "/") {
			continue
		}
		valid := ValidMetadataID(id)
		if provider == "imdb" {
			valid = len(id) >= 9 && len(id) <= 12 && strings.HasPrefix(id, "tt")
			if valid {
				for _, c := range id[2:] {
					if c < '0' || c > '9' {
						valid = false
					}
				}
			}
		}
		if !valid {
			continue
		}
		if old := ids[provider]; old != "" && old != id {
			ambiguous[provider] = true
		}
		ids[provider] = id
	}
	for provider := range ambiguous {
		delete(ids, provider)
	}
	return ids
}
func (c *Client) Details(ctx context.Context, base, token, clientID string, allowHTTP bool, ratingKey string) (IdentifiedMedia, error) {
	return c.DetailsCached(ctx, base, token, clientID, allowHTTP, ratingKey, nil)
}
func (c *Client) DetailsCached(ctx context.Context, base, token, clientID string, allowHTTP bool, ratingKey string, cache map[string]map[string]string) (IdentifiedMedia, error) {
	if !ValidMetadataID(ratingKey) {
		return IdentifiedMedia{}, ErrMediaPage
	}
	base, err := NormalizeURL(base, allowHTTP)
	if err != nil {
		return IdentifiedMedia{}, err
	}
	var body struct {
		Container *struct {
			Items []metadataDetail `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err = c.getLimit(ctx, base, "/library/metadata/"+ratingKey+"?checkFiles=1&skipRefresh=1&includeGuids=1", token, clientID, &body, nil, 4<<20); err != nil {
		return IdentifiedMedia{}, err
	}
	if body.Container == nil || len(body.Container.Items) != 1 {
		return IdentifiedMedia{}, ErrMediaPage
	}
	raw := body.Container.Items[0]
	if raw.RatingKey != ratingKey || !safeValue(raw.Title, 512) || strings.Contains(raw.Title, token) || strings.Contains(raw.SeriesTitle, token) {
		return IdentifiedMedia{}, ErrMediaPage
	}
	result := IdentifiedMedia{MediaItem: raw.MediaItem, IDs: providerIDs(raw.GUID, raw.GUIDs)}
	if raw.Kind != "movie" && raw.Kind != "episode" && raw.Kind != "show" {
		return result, ErrMediaPage
	}
	for _, media := range raw.Media {
		usable := len(media.Part) > 0
		for _, part := range media.Part {
			if !strings.HasPrefix(part.Key, "/library/parts/") || strings.Contains(part.Key, token) || part.Exists == nil || !*part.Exists || (part.Accessible != nil && !*part.Accessible) {
				usable = false
			}
		}
		if usable {
			result.Playable = true
		}
	}
	if raw.Kind == "episode" {
		if !ValidMetadataID(raw.SeriesRatingKey) {
			return result, nil
		}
		if ids, ok := cache[raw.SeriesRatingKey]; ok {
			result.SeriesIDs = ids
			return result, nil
		}
		var parent struct {
			Container *struct {
				Items []metadataDetail `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		if err = c.getLimit(ctx, base, "/library/metadata/"+raw.SeriesRatingKey+"?includeGuids=1&skipRefresh=1", token, clientID, &parent, nil, 4<<20); err != nil {
			return IdentifiedMedia{}, err
		}
		if parent.Container == nil || len(parent.Container.Items) != 1 || parent.Container.Items[0].Kind != "show" || parent.Container.Items[0].RatingKey != raw.SeriesRatingKey {
			return result, ErrMediaPage
		}
		show := parent.Container.Items[0]
		result.SeriesIDs = providerIDs(show.GUID, show.GUIDs)
		if cache != nil {
			cache[raw.SeriesRatingKey] = result.SeriesIDs
		}
	}
	return result, nil
}
