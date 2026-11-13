package plex

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const MediaPageSize = 50
const MaxMediaTotal = 1000000

var ErrMediaPage = errors.New("Plex returned an invalid or inconsistent media page. The last cached page was retained.")
var ErrMediaChanged = errors.New("This Plex library changed while paging. Load its first page again before continuing.")

type MediaItem struct {
	RatingKey       string `json:"ratingKey"`
	Kind            string `json:"type"`
	Title           string `json:"title"`
	SeriesTitle     string `json:"grandparentTitle,omitempty"`
	SeriesRatingKey string `json:"grandparentRatingKey,omitempty"`
	Season          *int   `json:"parentIndex,omitempty"`
	Episode         *int   `json:"index,omitempty"`
	Year            int    `json:"year,omitempty"`
}
type MediaPage struct {
	Offset, Total int
	Kind          string
	Items         []MediaItem
}

func (p MediaPage) HasNext() bool { return p.Offset+len(p.Items) < p.Total }
func ValidMetadataID(id string) bool {
	if len(id) == 0 || len(id) > 18 || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (l Library) Browsable() bool {
	return (l.Kind == "movie" || l.Kind == "show") && ValidMetadataID(l.Key)
}

// Plex examples include both numeric and quoted-numeric container counts.
// Accept either, but never floats, signs, negatives or unbounded values.
type containerCount int

func (n *containerCount) UnmarshalJSON(data []byte) error {
	raw := string(data)
	if strings.HasPrefix(raw, "\"") {
		if json.Unmarshal(data, &raw) != nil {
			return ErrMediaPage
		}
	}
	if raw == "" || len(raw) > 7 {
		return ErrMediaPage
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return ErrMediaPage
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value > MaxMediaTotal {
		return ErrMediaPage
	}
	*n = containerCount(value)
	return nil
}

// FetchMediaPage lists indexed metadata only; it does not infer provider matches,
// file availability or playability. Only fixed application endpoints are used.
func (c *Client) FetchMediaPage(ctx context.Context, base, token, clientID string, allowHTTP bool, library Library, offset int) (MediaPage, error) {
	if !library.Browsable() || offset < 0 || offset > MaxMediaTotal {
		return MediaPage{}, ErrMediaPage
	}
	base, err := NormalizeURL(base, allowHTTP)
	if err != nil {
		return MediaPage{}, err
	}
	if !ValidToken(token) || !safeValue(clientID, 128) {
		return MediaPage{}, ErrAuth
	}
	kind, typeNumber := "movie", "1"
	if library.Kind == "show" {
		kind = "episode"
		typeNumber = "4"
	}
	query := url.Values{"type": {typeNumber}, "X-Plex-Container-Start": {strconv.Itoa(offset)}, "X-Plex-Container-Size": {strconv.Itoa(MediaPageSize)}}
	route := "/library/sections/" + library.Key + "/all?" + query.Encode()
	var body struct {
		Container *struct {
			Size      *containerCount `json:"size"`
			Offset    *containerCount `json:"offset"`
			Total     *containerCount `json:"totalSize"`
			LibraryID json.RawMessage `json:"librarySectionID"`
			Items     []MediaItem     `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	var headers http.Header
	if err = c.getLimit(ctx, base, route, token, clientID, &body, &headers, 4<<20); err != nil {
		if errors.Is(err, ErrResponse) {
			return MediaPage{}, ErrMediaPage
		}
		return MediaPage{}, err
	}
	mc := body.Container
	if mc == nil || mc.Size == nil || int(*mc.Size) != len(mc.Items) || len(mc.Items) > MediaPageSize {
		return MediaPage{}, ErrMediaPage
	}
	if len(mc.LibraryID) > 0 && string(mc.LibraryID) != "null" {
		var id containerCount
		if id.UnmarshalJSON(mc.LibraryID) != nil || strconv.Itoa(int(id)) != library.Key {
			return MediaPage{}, ErrMediaPage
		}
	}
	for name, field := range map[string]**containerCount{"X-Plex-Container-Start": &mc.Offset, "X-Plex-Container-Total-Size": &mc.Total} {
		if raw := headers.Get(name); raw != "" {
			var n containerCount
			if n.UnmarshalJSON([]byte(raw)) != nil || (*field != nil && **field != n) {
				return MediaPage{}, ErrMediaPage
			}
			*field = &n
		}
	}
	if mc.Offset != nil && int(*mc.Offset) != offset {
		return MediaPage{}, ErrMediaPage
	}
	if offset > 0 && (mc.Offset == nil || mc.Total == nil) {
		return MediaPage{}, ErrMediaPage
	}
	total := len(mc.Items)
	if mc.Total != nil {
		total = int(*mc.Total)
		if total < offset+len(mc.Items) {
			return MediaPage{}, ErrMediaPage
		}
	} else if mc.Offset != nil {
		return MediaPage{}, ErrMediaPage
	}
	if offset+len(mc.Items) < total && (mc.Offset == nil || len(mc.Items) == 0) {
		return MediaPage{}, ErrMediaPage
	}
	if offset > total {
		return MediaPage{}, ErrMediaPage
	}
	seen := map[string]bool{}
	for _, item := range mc.Items {
		if item.Kind != kind || !ValidMetadataID(item.RatingKey) || seen[item.RatingKey] || !safeValue(item.Title, 512) || len(item.SeriesTitle) > 512 || item.Year < 0 || item.Year > 9999 {
			return MediaPage{}, ErrMediaPage
		}
		seen[item.RatingKey] = true
		if item.SeriesRatingKey != "" && !ValidMetadataID(item.SeriesRatingKey) {
			return MediaPage{}, ErrMediaPage
		}
		if item.Season != nil && (*item.Season < 0 || *item.Season > 100000) {
			return MediaPage{}, ErrMediaPage
		}
		if item.Episode != nil && (*item.Episode < 0 || *item.Episode > 100000) {
			return MediaPage{}, ErrMediaPage
		}
		if strings.Contains(item.Title, token) || strings.Contains(item.SeriesTitle, token) || strings.Contains(item.RatingKey, token) || strings.Contains(item.SeriesRatingKey, token) {
			return MediaPage{}, ErrMediaPage
		}
	}
	return MediaPage{Offset: offset, Total: total, Kind: kind, Items: mc.Items}, nil
}
