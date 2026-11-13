package plex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var ErrPlaylist = errors.New("Plex playlist reconciliation failed. The managed playlist was not cleared; retry after reviewing activity.")
var ErrPlaylistConflict = errors.New("The playlist is missing, ambiguous, or no longer has its Ordarr management marker. Explicit recovery is required.")

type Playlist struct {
	ID    string `json:"ratingKey"`
	Title string `json:"title"`
	Type  string `json:"playlistType"`
	Smart bool   `json:"smart"`
}
type PlaylistItem struct {
	RatingKey string     `json:"ratingKey"`
	ItemID    flexibleID `json:"playlistItemID"`
}
type flexibleID string

func (i *flexibleID) UnmarshalJSON(data []byte) error {
	v := strings.Trim(string(data), "\"")
	if !ValidMetadataID(v) {
		return ErrPlaylist
	}
	*i = flexibleID(v)
	return nil
}
func (c *Client) playlistRequest(ctx context.Context, method, base, path, token, clientID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return ErrPlaylist
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", token)
	req.Header.Set("X-Plex-Client-Identifier", clientID)
	req.Header.Set("X-Plex-Product", "Ordarr")
	req.Header.Set("X-Plex-Version", clientVersion)
	res, err := c.http.Do(req)
	if err != nil {
		return ErrPlaylist
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 498 {
		return ErrAuth
	}
	if res.StatusCode == 404 {
		return ErrPlaylistConflict
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrPlaylist
	}
	if out != nil {
		data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
		if err != nil || len(data) > 4<<20 || json.Unmarshal(data, out) != nil {
			return ErrPlaylist
		}
	}
	return nil
}
func (c *Client) OwnedPlaylist(ctx context.Context, base, id, title, token, clientID string) (Playlist, error) {
	if !ValidMetadataID(id) {
		return Playlist{}, ErrPlaylistConflict
	}
	var body struct {
		Container struct {
			Items []Playlist `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.playlistRequest(ctx, "GET", base, "/playlists/"+id, token, clientID, &body); err != nil {
		return Playlist{}, err
	}
	if len(body.Container.Items) != 1 {
		return Playlist{}, ErrPlaylistConflict
	}
	p := body.Container.Items[0]
	if p.ID != id || p.Title != title || p.Type != "video" || p.Smart {
		return p, ErrPlaylistConflict
	}
	return p, nil
}
func (c *Client) FindManagedPlaylist(ctx context.Context, base, title, token, clientID string) (string, error) {
	var found string
	for offset := 0; offset < 10000; {
		var body struct {
			Container struct {
				Size   *containerCount `json:"size"`
				Total  *containerCount `json:"totalSize"`
				Offset *containerCount `json:"offset"`
				Items  []Playlist      `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		path := "/playlists?playlistType=video&X-Plex-Container-Start=" + strconv.Itoa(offset) + "&X-Plex-Container-Size=100"
		var headers http.Header
		if err := c.get(ctx, base, path, token, clientID, &body, &headers); err != nil {
			return "", err
		}
		mc := body.Container
		if mc.Size == nil || int(*mc.Size) != len(mc.Items) {
			return "", ErrPlaylist
		}
		for _, p := range mc.Items {
			if p.Title == title {
				if found != "" || !ValidMetadataID(p.ID) || p.Type != "video" || p.Smart {
					return "", ErrPlaylistConflict
				}
				found = p.ID
			}
		}
		if mc.Total == nil {
			if offset != 0 {
				return "", ErrPlaylist
			}
			return found, nil
		}
		if mc.Offset == nil || int(*mc.Offset) != offset || len(mc.Items) == 0 && offset < int(*mc.Total) {
			return "", ErrPlaylist
		}
		offset += len(mc.Items)
		if offset == int(*mc.Total) {
			return found, nil
		}
		if offset > int(*mc.Total) {
			return "", ErrPlaylist
		}
	}
	return "", ErrPlaylist
}
func contentURI(machine string, keys []string) (string, error) {
	if !safeValue(machine, 256) || strings.ContainsAny(machine, "/?# ,") {
		return "", ErrPlaylist
	}
	for _, key := range keys {
		if !ValidMetadataID(key) {
			return "", ErrPlaylist
		}
	}
	return "server://" + machine + "/com.plexapp.plugins.library/library/metadata/" + strings.Join(keys, ","), nil
}
func (c *Client) CreateManagedPlaylist(ctx context.Context, base, machine, title, token, clientID string, keys []string) (string, error) {
	if len(keys) == 0 {
		return "", ErrPlaylist
	}
	uri, err := contentURI(machine, keys)
	if err != nil {
		return "", err
	}
	query := url.Values{"type": {"video"}, "smart": {"0"}, "title": {title}, "uri": {uri}}
	var body struct {
		Container struct {
			Items []Playlist `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err = c.playlistRequest(ctx, "POST", base, "/playlists?"+query.Encode(), token, clientID, &body); err != nil {
		return "", err
	}
	if len(body.Container.Items) != 1 {
		return "", ErrPlaylist
	}
	p := body.Container.Items[0]
	if !ValidMetadataID(p.ID) || p.Title != title || p.Type != "video" || p.Smart {
		return "", ErrPlaylist
	}
	return p.ID, nil
}
func (c *Client) PlaylistItems(ctx context.Context, base, id, token, clientID string) ([]PlaylistItem, error) {
	if !ValidMetadataID(id) {
		return nil, ErrPlaylist
	}
	items := []PlaylistItem{}
	seen := map[string]bool{}
	total := -1
	for offset := 0; offset <= 2000; {
		var body struct {
			Container struct {
				Size   *containerCount `json:"size"`
				Offset *containerCount `json:"offset"`
				Total  *containerCount `json:"totalSize"`
				Items  []PlaylistItem  `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		path := "/playlists/" + id + "/items?X-Plex-Container-Start=" + strconv.Itoa(offset) + "&X-Plex-Container-Size=100"
		if err := c.getLimit(ctx, base, path, token, clientID, &body, nil, 4<<20); err != nil {
			return nil, err
		}
		mc := body.Container
		if mc.Size == nil || int(*mc.Size) != len(mc.Items) || len(mc.Items) > 2000 {
			return nil, ErrPlaylist
		}
		if mc.Offset != nil && int(*mc.Offset) != offset {
			return nil, ErrPlaylist
		}
		if mc.Total != nil {
			if total >= 0 && total != int(*mc.Total) {
				return nil, ErrPlaylist
			}
			total = int(*mc.Total)
			if total > 2000 {
				return nil, ErrPlaylist
			}
		}
		for _, item := range mc.Items {
			if !ValidMetadataID(item.RatingKey) || !ValidMetadataID(string(item.ItemID)) || seen[string(item.ItemID)] {
				return nil, ErrPlaylist
			}
			seen[string(item.ItemID)] = true
			items = append(items, item)
		}
		offset += len(mc.Items)
		if mc.Total == nil {
			if mc.Offset != nil || offset != len(mc.Items) {
				return nil, ErrPlaylist
			}
			return items, nil
		}
		if offset == total {
			return items, nil
		}
		if offset > total || len(mc.Items) == 0 || mc.Offset == nil {
			return nil, ErrPlaylist
		}
	}
	return nil, ErrPlaylist
}
func (c *Client) ReconcilePlaylist(ctx context.Context, base, id, title, machine, token, clientID string, desired []string, guard func() error) error {
	if len(desired) == 0 || len(desired) > 1000 {
		return ErrPlaylist
	}
	if _, err := c.OwnedPlaylist(ctx, base, id, title, token, clientID); err != nil {
		return err
	}
	originalGuard := guard
	guard = func() error {
		if err := originalGuard(); err != nil {
			return err
		}
		_, err := c.OwnedPlaylist(ctx, base, id, title, token, clientID)
		return err
	}
	current, err := c.PlaylistItems(ctx, base, id, token, clientID)
	if err != nil {
		return err
	}
	have, need := map[string]int{}, map[string]int{}
	for _, i := range current {
		have[i.RatingKey]++
	}
	for _, key := range desired {
		need[key]++
	}
	// Add before moving/removing, so a failed write never intentionally truncates
	// a previously valid sequence. No clear-all endpoint is ever called.
	for key, count := range need {
		for have[key] < count {
			if err = guard(); err != nil {
				return err
			}
			uri, e := contentURI(machine, []string{key})
			if e != nil {
				return e
			}
			if err = c.playlistRequest(ctx, "PUT", base, "/playlists/"+id+"/items?"+url.Values{"uri": {uri}}.Encode(), token, clientID, nil); err != nil {
				return err
			}
			updated, e := c.PlaylistItems(ctx, base, id, token, clientID)
			if e != nil {
				return e
			}
			n := 0
			for _, item := range updated {
				if item.RatingKey == key {
					n++
				}
			}
			if n <= have[key] {
				return ErrPlaylist
			}
			have[key] = n
			current = updated
		}
	}
	for index, key := range desired {
		if index < len(current) && current[index].RatingKey == key {
			continue
		}
		at := -1
		for j := index; j < len(current); j++ {
			if current[j].RatingKey == key {
				at = j
				break
			}
		}
		if at < 0 {
			return ErrPlaylist
		}
		if err = guard(); err != nil {
			return err
		}
		query := url.Values{}
		if index > 0 {
			query.Set("after", string(current[index-1].ItemID))
		}
		path := "/playlists/" + id + "/items/" + string(current[at].ItemID) + "/move"
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
		if err = c.playlistRequest(ctx, "PUT", base, path, token, clientID, nil); err != nil {
			return err
		}
		current, err = c.PlaylistItems(ctx, base, id, token, clientID)
		if err != nil {
			return err
		}
		for i := 0; i <= index; i++ {
			if i >= len(current) || current[i].RatingKey != desired[i] {
				return ErrPlaylist
			}
		}
	}
	for len(current) > len(desired) {
		if err = guard(); err != nil {
			return err
		}
		if _, err = c.OwnedPlaylist(ctx, base, id, title, token, clientID); err != nil {
			return err
		}
		for i, key := range desired {
			if current[i].RatingKey != key {
				return ErrPlaylist
			}
		}
		tail := current[len(current)-1]
		if err = c.playlistRequest(ctx, "DELETE", base, "/playlists/"+id+"/items/"+string(tail.ItemID), token, clientID, nil); err != nil {
			return err
		}
		current, err = c.PlaylistItems(ctx, base, id, token, clientID)
		if err != nil {
			return err
		}
	}
	if len(current) != len(desired) {
		return ErrPlaylist
	}
	for i, key := range desired {
		if current[i].RatingKey != key {
			return ErrPlaylist
		}
	}
	return guard()
}
