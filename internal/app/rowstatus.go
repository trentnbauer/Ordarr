package app

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// rowStatus says what is happening to one entry that is not playable yet.
type rowStatus struct {
	Label, Detail string
	Warn          bool
	RequestVia    string // Sonarr/Radarr connection a "Request now" would use; empty: none
	rank          int
}

const (
	statusRequested = "Requested"
	statusDownload  = "Downloading"
	statusPlexScan  = "Waiting for Plex scan"
	statusNotFound  = "Not found in any downloader"
	statusNotAsked  = "Not requested"
	statusAuto      = "Will be requested automatically"
	statusPick      = "Needs a pick"
)

// missingStatus derives the status of a missing entry from data Ordarr already holds: the last
// Sonarr/Radarr snapshot (library and queue) and its own request log. Nothing is fetched here.
func missingStatus(arrs []store.ArrConnection, requests map[string]store.AcquisitionRequest, e store.Entry, autoOn, optedOut bool, chosen map[string]bool) rowStatus {
	if len(arrs) == 0 {
		return rowStatus{Label: statusNotAsked, Detail: "Connect Sonarr or Radarr to download it.", rank: 1}
	}
	best := rowStatus{Label: statusNotFound, Detail: "No connected downloader handles this kind of ID.", Warn: true, rank: 2}
	for _, c := range arrs {
		id, ok := providerNumber(c, e)
		if !ok {
			continue
		}
		s := statusOn(c, requests, id, autoOn && !optedOut && chosen[c.ID])
		if s.rank > best.rank {
			best = s
		}
	}
	return best
}

func statusOn(c store.ArrConnection, requests map[string]store.AcquisitionRequest, id int, auto bool) rowStatus {
	if media, ok, err := arr.Existing(arr.Connection{Kind: c.Kind}, c.Snapshot.Media, id); ok && err == nil {
		for _, q := range c.Snapshot.Queue {
			if c.Kind == "radarr" && q.MovieID == media.ID || c.Kind == "sonarr" && q.SeriesID == media.ID {
				return rowStatus{Label: statusDownload, Detail: "In the " + c.Name + " download queue.", rank: 8}
			}
		}
		// shortcut: Sonarr only reports file counts per series, so any downloaded episode counts as
		// "waiting for Plex"; upgrade by reading episode files if that proves too coarse.
		if media.HasFile || media.Statistics.EpisodeFileCount > 0 {
			return rowStatus{Label: statusPlexScan, Detail: "Downloaded by " + c.Name + "; Plex hasn't scanned it yet.", rank: 7}
		}
		return rowStatus{Label: statusRequested, Detail: "Added in " + c.Name + "; waiting for a download.", rank: 6}
	}
	if r, ok := requests[c.ID+"/"+strconv.Itoa(id)]; ok {
		switch r.State {
		case "pending", "approved", "running", "completed", "uncertain":
			return rowStatus{Label: statusRequested, Detail: "Requested from " + c.Name + ".", rank: 5}
		case "failed":
			return rowStatus{Label: statusNotFound, Detail: c.Name + " couldn't find or add it. " + r.Message, Warn: true, RequestVia: c.ID, rank: 4}
		}
	}
	if arrReady(c) {
		if auto {
			return rowStatus{Label: statusAuto, Detail: c.Name + " is asked on the next hourly check.", RequestVia: c.ID, rank: 3}
		}
		return rowStatus{Label: statusNotAsked, Detail: "Not downloading from " + c.Name + " for this collection.", RequestVia: c.ID, rank: 3}
	}
	return rowStatus{Label: statusNotAsked, Detail: "Choose a quality profile and root folder for " + c.Name + " first.", rank: 1}
}

// pickData is what the Order tab needs to let the administrator resolve "needs a pick" rows.
type pickData struct {
	Candidates map[string][]plex.IdentifiedMedia // entry ID -> ambiguous matches on the server
	Manual     map[string]bool                   // entry ID -> mapped by the administrator
	Results    []plex.IdentifiedMedia            // library search results for the open picker
}

// loadPicks resolves ambiguous entries against the server's catalogue and, when a picker is
// open (pickEntry and query set), searches the catalogue for items of the same kind.
func (a *App) loadPicks(ctx context.Context, c store.Collection, serverID string, v availability, pickEntry, query string) (pickData, error) {
	d := pickData{Candidates: map[string][]plex.IdentifiedMedia{}, Manual: map[string]bool{}}
	if serverID == "" || !v.Ready {
		return d, nil
	}
	overrides, err := a.store.ManualMatches(ctx, serverID, c)
	if err != nil {
		return d, err
	}
	for id, key := range overrides {
		d.Manual[id] = key != ""
	}
	needItems := pickEntry != ""
	for _, st := range v.States {
		needItems = needItems || st == "unmatched"
	}
	if !needItems {
		return d, nil
	}
	items, _, err := a.store.Catalogue(ctx, serverID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrConflict) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	index := matching.NewIndex(items)
	for _, e := range c.Entries {
		if v.States[e.ID] != "unmatched" {
			continue
		}
		r := index.Resolve(matching.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}, overrides[e.ID])
		d.Candidates[e.ID] = r.Candidates
	}
	if pickEntry != "" && strings.TrimSpace(query) != "" {
		var kind string
		for _, e := range c.Entries {
			if e.ID == pickEntry {
				kind = e.Kind
			}
		}
		q := strings.ToLower(strings.TrimSpace(query))
		for _, item := range items {
			if item.Kind == kind && (strings.Contains(strings.ToLower(item.Title), q) || strings.Contains(strings.ToLower(item.SeriesTitle), q)) {
				d.Results = append(d.Results, item)
			}
		}
		sort.SliceStable(d.Results, func(i, j int) bool { return d.Results[i].Title < d.Results[j].Title })
		if len(d.Results) > 20 {
			d.Results = d.Results[:20]
		}
	}
	return d, nil
}
