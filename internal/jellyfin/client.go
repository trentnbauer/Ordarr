// Package jellyfin implements read-only, authenticated Jellyfin library discovery.
// A matched indexed record is not proof of a currently playable file.
package jellyfin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrAPI = errors.New("Jellyfin returned an unavailable, incomplete or invalid response; the last complete catalogue was retained.")
var ErrAuth = errors.New("Jellyfin rejected this user access token or disabled media access. Use a token for the intended user, not an administrator API key.")
var ErrChanged = errors.New("The Jellyfin user, server or library changed during scanning. Retry after reviewing the connection.")

var validToken = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

const PageSize = 100
const MaxItems = 50000

// ID accepts only Jellyfin UUID item/user IDs and returns canonical hex form.
func ID(raw string) (string, bool) {
	if len(raw) == 36 {
		if raw[8] != '-' || raw[13] != '-' || raw[18] != '-' || raw[23] != '-' {
			return "", false
		}
		raw = strings.ReplaceAll(raw, "-", "")
	}
	if len(raw) != 32 || raw == strings.Repeat("0", 32) {
		return "", false
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", false
	}
	return strings.ToLower(raw), true
}

type Client struct{ http *http.Client }

func New() *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 2
	tr.MaxResponseHeaderBytes = 64 << 10
	tr.ResponseHeaderTimeout = 5 * time.Second
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrAPI
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrAPI
		}
		for _, ip := range ips {
			ip = ip.Unmap()
			if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip == netip.MustParseAddr("100.100.100.200") || ip == netip.MustParseAddr("fd00:ec2::254") {
				return nil, ErrAPI
			}
		}
		d := net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range ips {
			conn, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, ErrAPI
	}
	return &Client{http: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) get(ctx context.Context, base, token, path string, out any) error {
	if _, err := plex.NormalizeURL(base, true); err != nil || len(token) > 4096 || !validToken.MatchString(token) {
		return ErrAuth
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+path, nil)
	if err != nil {
		return ErrAPI
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", `MediaBrowser Client="Ordarr", Device="Ordarr", DeviceId="ordarr", Version="0.8.0-dev", Token="`+token+`"`)
	res, err := c.http.Do(req)
	if err != nil {
		return ErrAPI
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ErrAuth
	}
	if res.StatusCode != 200 {
		return ErrAPI
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil || len(data) > 4<<20 || json.Unmarshal(data, out) != nil {
		return ErrAPI
	}
	return nil
}

type Library struct{ ID, Name, Kind string }

func (l Library) Browsable() bool {
	switch l.Kind {
	case "movies", "tvshows", "homevideos", "folders", "unknown", "":
		return true
	}
	return false
}

type Snapshot struct {
	ServerID, ServerName, Version, UserID, UserName string
	Libraries                                       []Library
}
type rawItem struct {
	ID             string `json:"Id"`
	Name           string
	Type           string
	CollectionType string
	ProviderIDs    map[string]string `json:"ProviderIds"`
	SeriesID       string            `json:"SeriesId"`
	Season         *int              `json:"ParentIndexNumber"`
	Episode        *int              `json:"IndexNumber"`
	Location       string            `json:"LocationType"`
	Placeholder    *bool             `json:"IsPlaceHolder"`
	PlayAccess     string
}
type itemPage struct {
	Items *[]rawItem
	Total *int `json:"TotalRecordCount"`
	Start *int `json:"StartIndex"`
}

func (c *Client) Inspect(ctx context.Context, base, token string) (Snapshot, error) {
	var out Snapshot
	var server struct{ ID, ServerName, Version, ProductName string }
	if err := c.get(ctx, base, token, "/System/Info/Public", &server); err != nil {
		return out, err
	}
	if server.ID == "" || len(server.ID) > 128 || server.ServerName == "" || server.Version == "" || server.ProductName != "Jellyfin" {
		return out, ErrAPI
	}
	var user struct {
		ID, Name string
		Policy   *struct {
			Disabled *bool `json:"IsDisabled"`
			Playback *bool `json:"EnableMediaPlayback"`
		}
	}
	if err := c.get(ctx, base, token, "/Users/Me", &user); err != nil {
		return out, err
	}
	userID, ok := ID(user.ID)
	if !ok || user.Name == "" || user.Policy == nil || user.Policy.Disabled == nil || *user.Policy.Disabled || user.Policy.Playback == nil || !*user.Policy.Playback {
		return out, ErrAuth
	}
	out = Snapshot{ServerID: server.ID, ServerName: server.ServerName, Version: server.Version, UserID: userID, UserName: user.Name}
	var views itemPage
	path := "/UserViews?" + url.Values{"userId": {userID}, "includeExternalContent": {"false"}, "includeHidden": {"false"}}.Encode()
	if err := c.get(ctx, base, token, path, &views); err != nil {
		return out, err
	}
	if views.Items == nil || views.Total == nil || *views.Total != len(*views.Items) || len(*views.Items) > 1000 {
		return out, ErrAPI
	}
	seen := map[string]bool{}
	for _, view := range *views.Items {
		id, valid := ID(view.ID)
		if !valid || seen[id] || view.Name == "" || len(view.Name) > 1000 {
			return out, ErrAPI
		}
		seen[id] = true
		out.Libraries = append(out.Libraries, Library{ID: id, Name: view.Name, Kind: view.CollectionType})
	}
	return out, nil
}

type Item struct {
	ID, Kind, Title string
	IDs, SeriesIDs  map[string]string
	Season, Episode *int
	Unavailable     bool
}
type Page struct {
	Items        []Item
	Total, Start int
}

var numeric = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var imdb = regexp.MustCompile(`^tt[0-9]{7,10}$`)

func providerIDs(raw map[string]string) map[string]string {
	out := map[string]string{}
	conflicts := map[string]bool{}
	for k, v := range raw {
		k = strings.ToLower(k)
		if k != "tmdb" && k != "tvdb" && k != "imdb" {
			continue
		}
		if k == "imdb" && !imdb.MatchString(v) || k != "imdb" && !numeric.MatchString(v) {
			conflicts[k] = true
			continue
		}
		if old := out[k]; old != "" && old != v {
			conflicts[k] = true
		}
		out[k] = v
	}
	for k := range conflicts {
		delete(out, k)
	}
	return out
}
func normalize(raw rawItem) (Item, error) {
	id, ok := ID(raw.ID)
	if !ok || raw.Name == "" || len(raw.Name) > 1000 {
		return Item{}, ErrAPI
	}
	kind := ""
	switch raw.Type {
	case "Movie":
		kind = "movie"
	case "Episode":
		kind = "episode"
	default:
		return Item{}, ErrAPI
	}
	if raw.Season != nil && (*raw.Season < 0 || *raw.Season > 999) || raw.Episode != nil && (*raw.Episode < 1 || *raw.Episode > 10000) {
		return Item{}, ErrAPI
	}
	return Item{ID: id, Kind: kind, Title: raw.Name, IDs: providerIDs(raw.ProviderIDs), Season: raw.Season, Episode: raw.Episode, Unavailable: raw.Location == "Virtual" || raw.Location == "Offline" || raw.Placeholder != nil && *raw.Placeholder || raw.PlayAccess == "None"}, nil
}
func (c *Client) FetchPage(ctx context.Context, base, token, user, library string, offset int, series map[string]map[string]string) (Page, error) {
	var out Page
	uid, ok := ID(user)
	if !ok {
		return out, ErrAuth
	}
	lib, ok := ID(library)
	if !ok || offset < 0 || offset > MaxItems {
		return out, ErrAPI
	}
	params := url.Values{"userId": {uid}, "parentId": {lib}, "recursive": {"true"}, "includeItemTypes": {"Movie,Episode"}, "fields": {"ProviderIds"}, "enableImages": {"false"}, "enableUserData": {"false"}, "sortBy": {"SortName"}, "sortOrder": {"Ascending"}, "startIndex": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(PageSize)}}
	var body itemPage
	if err := c.get(ctx, base, token, "/Items?"+params.Encode(), &body); err != nil {
		return out, err
	}
	if body.Items == nil || body.Total == nil || body.Start == nil || *body.Start != offset || *body.Total < 0 || *body.Total > MaxItems || len(*body.Items) > PageSize || offset+len(*body.Items) > *body.Total || len(*body.Items) == 0 && offset != *body.Total {
		return out, ErrAPI
	}
	out.Total, out.Start = *body.Total, offset
	seen := map[string]bool{}
	for _, raw := range *body.Items {
		item, err := normalize(raw)
		if err != nil || seen[item.ID] {
			return Page{}, ErrAPI
		}
		seen[item.ID] = true
		if item.Kind == "episode" {
			sid, valid := ID(raw.SeriesID)
			if valid {
				ids, cached := series[sid]
				if !cached {
					var show rawItem
					if err = c.get(ctx, base, token, "/Items/"+sid+"?"+url.Values{"userId": {uid}, "fields": {"ProviderIds"}}.Encode(), &show); err != nil {
						return Page{}, err
					}
					actual, valid := ID(show.ID)
					if !valid || actual != sid || show.Type != "Series" {
						return Page{}, ErrAPI
					}
					ids = providerIDs(show.ProviderIDs)
					if series != nil {
						series[sid] = ids
					}
				}
				item.SeriesIDs = ids
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
