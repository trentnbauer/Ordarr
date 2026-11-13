// Package arr implements the documented Sonarr/Radarr v3 APIs. Acquisition is
// explicitly whole-movie/whole-series; an episode entry never implies an isolated download.
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrAPI = errors.New("The acquisition service returned an error or invalid response. Credentials and response bodies are withheld.")
var ErrIdentity = errors.New("No unique exact provider match was confirmed. Radarr requires TMDB movies; Sonarr requires TVDB series.")

type Client struct{ http *http.Client }

func New() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
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
			if ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip == netip.MustParseAddr("100.100.100.200") || ip == netip.MustParseAddr("fd00:ec2::254") {
				return nil, ErrAPI
			}
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, ErrAPI
	}
	return &Client{&http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrAPI }}}
}

type Connection struct{ Kind, URL, Key string }
type Choice struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}
type Media struct {
	ID         int      `json:"id"`
	Title      string   `json:"title"`
	TMDB       int      `json:"tmdbId"`
	TVDB       int      `json:"tvdbId"`
	Slug       string   `json:"titleSlug"`
	Monitored  bool     `json:"monitored"`
	HasFile    bool     `json:"hasFile"`
	Seasons    []Season `json:"seasons,omitempty"`
	Statistics struct {
		EpisodeFileCount int `json:"episodeFileCount"`
	} `json:"statistics"`
}
type Season struct {
	Number    int  `json:"seasonNumber"`
	Monitored bool `json:"monitored"`
}
type QueueItem struct {
	ID           int    `json:"id"`
	MovieID      int    `json:"movieId"`
	SeriesID     int    `json:"seriesId"`
	Status       string `json:"status"`
	TrackedState string `json:"trackedDownloadState"`
}
type Snapshot struct {
	Version         string
	Profiles, Roots []Choice
	Media           []Media
	Queue           []QueueItem
}

func resource(c Connection) string {
	if c.Kind == "radarr" {
		return "movie"
	}
	return "series"
}
func valid(c Connection) bool {
	_, err := plex.NormalizeURL(c.URL, true)
	return err == nil && (c.Kind == "sonarr" || c.Kind == "radarr") && len(c.Key) > 0 && len(c.Key) <= 512 && !strings.ContainsAny(c.Key, "\r\n")
}
func (c *Client) request(ctx context.Context, conn Connection, method, path string, in, out any) error {
	if !valid(conn) {
		return ErrAPI
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return ErrAPI
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, conn.URL+"/api/v3/"+path, body)
	if err != nil {
		return ErrAPI
	}
	req.Header.Set("X-Api-Key", conn.Key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return ErrAPI
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ErrAPI
	}
	if out != nil {
		data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
		if err != nil || len(data) > 8<<20 || json.Unmarshal(data, out) != nil {
			return ErrAPI
		}
	}
	return nil
}
func (c *Client) Inspect(ctx context.Context, conn Connection) (Snapshot, error) {
	var s Snapshot
	var status struct{ Version, AppName string }
	if err := c.request(ctx, conn, "GET", "system/status", nil, &status); err != nil {
		return s, err
	}
	if !strings.EqualFold(status.AppName, conn.Kind) || status.Version == "" {
		return s, ErrAPI
	}
	s.Version = status.Version
	for _, req := range []struct {
		path string
		out  any
	}{{"qualityprofile", &s.Profiles}, {"rootfolder", &s.Roots}, {resource(conn), &s.Media}} {
		if err := c.request(ctx, conn, "GET", req.path, nil, req.out); err != nil {
			return s, err
		}
	}
	if len(s.Media) > 50000 || len(s.Profiles) > 1000 || len(s.Roots) > 1000 {
		return s, ErrAPI
	}
	for _, choices := range [][]Choice{s.Profiles, s.Roots} {
		seen := map[int]bool{}
		for _, choice := range choices {
			if choice.ID <= 0 || seen[choice.ID] {
				return s, ErrAPI
			}
			seen[choice.ID] = true
		}
	}
	seenMedia := map[int]bool{}
	for _, item := range s.Media {
		if item.ID <= 0 || seenMedia[item.ID] {
			return s, ErrAPI
		}
		seenMedia[item.ID] = true
	}
	total := -1
	seen := map[int]bool{}
	for page := 1; page <= 500; page++ {
		var q struct {
			Page, PageSize, TotalRecords int
			Records                      []QueueItem
		}
		if err := c.request(ctx, conn, "GET", "queue?page="+strconv.Itoa(page)+"&pageSize=100", nil, &q); err != nil {
			return s, err
		}
		if q.Page != page || q.TotalRecords < 0 || q.TotalRecords > 50000 || q.PageSize < 1 || q.PageSize > 100 || len(q.Records) > q.PageSize || total >= 0 && total != q.TotalRecords {
			return s, ErrAPI
		}
		total = q.TotalRecords
		for _, item := range q.Records {
			if item.ID <= 0 || seen[item.ID] {
				return s, ErrAPI
			}
			seen[item.ID] = true
			s.Queue = append(s.Queue, item)
		}
		if len(s.Queue) == total {
			return s, nil
		}
		if len(s.Queue) > total || len(q.Records) == 0 {
			return s, ErrAPI
		}
	}
	return s, ErrAPI
}
func exact(conn Connection, item Media, id int) bool {
	if conn.Kind == "radarr" {
		return item.TMDB == id
	}
	return item.TVDB == id
}
func (c *Client) Lookup(ctx context.Context, conn Connection, id int) (Media, error) {
	var items []Media
	provider := "tvdb"
	if conn.Kind == "radarr" {
		provider = "tmdb"
	}
	if id < 1 {
		return Media{}, ErrIdentity
	}
	path := resource(conn) + "/lookup?term=" + url.QueryEscape(provider+":"+strconv.Itoa(id))
	if err := c.request(ctx, conn, "GET", path, nil, &items); err != nil {
		return Media{}, err
	}
	var found Media
	count := 0
	for _, item := range items {
		if exact(conn, item, id) {
			found = item
			count++
		}
	}
	if count != 1 || found.Title == "" || found.Slug == "" {
		return Media{}, ErrIdentity
	}
	return found, nil
}
func Existing(conn Connection, items []Media, id int) (Media, bool, error) {
	var found Media
	n := 0
	for _, item := range items {
		if exact(conn, item, id) {
			found = item
			n++
		}
	}
	if n > 1 {
		return found, false, ErrIdentity
	}
	return found, n == 1, nil
}

// Add validates configured choices afresh and never updates/deletes existing media.
// An uncertain POST response must be reconciled by reading Existing before retrying.
func (c *Client) Add(ctx context.Context, conn Connection, id, profile, root int) (Media, error) {
	return c.AddGuarded(ctx, conn, id, profile, root, nil)
}
func (c *Client) AddGuarded(ctx context.Context, conn Connection, id, profile, root int, guard func() error) (Media, error) {
	snapshot, err := c.Inspect(ctx, conn)
	if err != nil {
		return Media{}, err
	}
	if item, ok, e := Existing(conn, snapshot.Media, id); e != nil || ok {
		return item, e
	}
	var rootPath string
	profileOK := false
	for _, p := range snapshot.Profiles {
		if p.ID == profile {
			profileOK = true
		}
	}
	for _, r := range snapshot.Roots {
		if r.ID == root {
			rootPath = r.Path
		}
	}
	if !profileOK || rootPath == "" {
		return Media{}, ErrAPI
	}
	item, err := c.Lookup(ctx, conn, id)
	if err != nil {
		return item, err
	}
	payload := map[string]any{"title": item.Title, "titleSlug": item.Slug, "qualityProfileId": profile, "rootFolderPath": rootPath, "monitored": true, "images": []any{}}
	if conn.Kind == "radarr" {
		payload["tmdbId"] = id
		payload["minimumAvailability"] = "released"
		payload["addOptions"] = map[string]any{"searchForMovie": true}
	} else {
		payload["tvdbId"] = id
		payload["seriesType"] = "standard"
		payload["seasonFolder"] = true
		payload["monitorNewItems"] = "all"
		for i := range item.Seasons {
			item.Seasons[i].Monitored = true
		}
		payload["seasons"] = item.Seasons
		payload["addOptions"] = map[string]any{"monitor": "all", "searchForMissingEpisodes": true}
	}
	if guard != nil {
		if err := guard(); err != nil {
			return Media{}, err
		}
	}
	var added Media
	if err = c.request(ctx, conn, "POST", resource(conn), payload, &added); err != nil {
		return Media{}, err
	}
	if !exact(conn, added, id) || added.ID < 1 {
		return added, ErrAPI
	}
	return added, nil
}
func Status(s Snapshot, item Media) string {
	state := "missing"
	if item.Monitored {
		state = "monitored"
	}
	if item.HasFile || item.Statistics.EpisodeFileCount > 0 {
		state = "files present (Plex indexing still required)"
	}
	for _, q := range s.Queue {
		if q.MovieID == item.ID || q.SeriesID == item.ID {
			if strings.EqualFold(q.Status, "downloading") {
				return "downloading"
			}
			return "queued"
		}
	}
	return state
}
