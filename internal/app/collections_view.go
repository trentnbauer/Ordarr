package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/portable"
	"github.com/trentnbauer/Ordarr/internal/store"
)

type listView struct {
	Items                           []listItem
	Count                           int
	Query, Filter, Sort, Prev, Next string
	Active                          string
	Filtered                        bool
}

type orderRow struct {
	Entry    store.Entry
	Position int
	State    string // available, unmatched, missing or "" when not checked
	Repeat   bool
}
type destView struct {
	store.Destination
	Server    plexView
	Latest    store.SyncRun
	HasLatest bool
	Waiting   int
}
type syncRunView struct {
	store.SyncRun
	Destination string
}
type missingRow struct {
	Entry    store.Entry
	Position int
	Result   matching.Result
	Manual   bool
}
type intervalOption struct {
	Minutes int
	Label   string
}
type collectionView struct {
	Tab                   string
	Server                plexView
	HasServer             bool
	Avail                 availability
	Rows                  []orderRow
	Source                *sourceView
	Changes               []portable.Change
	Destinations          []destView
	Eligible              []plexView
	Runs                  []syncRunView
	Jobs                  []store.Job
	MissingRows           []missingRow
	MatchServer           plexView
	MatchReady            bool
	MatchScanned          int64
	OtherPlex             []plexView
	Jellyfin              []jellyfinView
	Adding                bool
	SyncTarget            string
	LastSync              int64
	NotPlayable           int
	SourceIntervals       []intervalOption
	SyncIntervals         []intervalOption
	Created               bool
	Added, Moved, Changed int
	Removed               int
}

var collectionTabs = map[string]bool{"order": true, "missing": true, "source": true, "publishing": true, "history": true, "settings": true}

func intervals(current int, manual bool) []intervalOption {
	list := []intervalOption{{15, "15 minutes"}, {30, "30 minutes"}, {60, "1 hour"}, {360, "6 hours"}, {720, "12 hours"}, {1440, "1 day"}, {10080, "1 week"}}
	if manual {
		list = append([]intervalOption{{0, "Manually only"}}, list...)
	}
	for _, o := range list {
		if o.Minutes == current {
			return list
		}
	}
	return append(list, intervalOption{current, strconv.Itoa(current) + " minutes"})
}

// collectionList builds the list column from the request's search state.
func (a *App) collectionList(ctx context.Context, r *http.Request, o *overview, values map[string]availability, active string) (*listView, error) {
	q := r.URL.Query()
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 100000 {
			return nil, fmt.Errorf("%w: Invalid page", store.ErrInvalid)
		}
		offset = n
	}
	l := &listView{Query: q.Get("q"), Filter: q.Get("filter"), Sort: q.Get("sort"), Active: active, Count: len(o.Refs)}
	if l.Sort == "" {
		l.Sort = "recent"
	}
	l.Filtered = l.Query != "" || l.Filter != ""
	result, err := a.store.ListCollectionsSorted(ctx, l.Query, l.Filter, l.Sort, offset)
	if err != nil {
		return nil, err
	}
	link := func(n int) string {
		v := url.Values{"q": {l.Query}, "filter": {l.Filter}, "sort": {l.Sort}, "offset": {strconv.Itoa(n)}}
		return "/?" + v.Encode()
	}
	if offset > 0 {
		l.Prev = link(max(0, offset-store.PageSize))
	}
	if len(result) > store.PageSize {
		result = result[:store.PageSize]
		l.Next = link(offset + store.PageSize)
	}
	for _, c := range result {
		status, warn := collectionStatus(o, values, c.ID)
		l.Items = append(l.Items, listItem{ID: c.ID, Title: c.Title, Status: status, Warn: warn, Active: c.ID == active})
	}
	return l, nil
}

// collectionPage assembles the list + detail shell for one collection.
func (a *App) collectionPage(ctx context.Context, r *http.Request, c store.Collection, tab, matchConnection string) (page, error) {
	p := page{Title: c.Title, Mode: "collection", Collection: c, Input: defaultEntryInput()}
	o, err := a.loadOverview(ctx)
	if err != nil {
		return p, err
	}
	values, err := a.availabilityAll(ctx, o)
	if err != nil {
		return p, err
	}
	if p.Nav, err = a.navFor(ctx, "collections", o, values); err != nil {
		return p, err
	}
	if p.List, err = a.collectionList(ctx, r, o, values, c.ID); err != nil {
		return p, err
	}
	if !collectionTabs[tab] {
		tab = "order"
	}
	v := &collectionView{Tab: tab, Adding: r.URL.Query().Get("add") == "1", Created: r.URL.Query().Get("created") == "1"}
	p.Detail = v
	p.PlexConnections = o.Plex
	v.Server, v.HasServer = o.primary(c.ID)
	if v.HasServer {
		refs := []store.CollectionRef{{ID: c.ID, Revision: c.Revision}}
		got, err := a.availabilityFor(ctx, refs, v.Server.ID)
		if err != nil {
			return p, err
		}
		v.Avail = got[c.ID]
	}
	v.NotPlayable = v.Avail.Missing + v.Avail.NeedPick
	seen := map[string]bool{}
	for i, e := range c.Entries {
		identity := store.EntryIdentity(e)
		v.Rows = append(v.Rows, orderRow{Entry: e, Position: i + 1, State: v.Avail.States[e.ID], Repeat: seen[identity]})
		seen[identity] = true
	}
	// Destinations and their latest sync.
	used := map[string]bool{}
	for _, d := range o.Destinations[c.ID] {
		dv := destView{Destination: d, Server: o.plexByID[d.ConnectionID]}
		dv.Latest, dv.HasLatest = o.Latest[d.ID]
		if d.ConnectionID == v.Server.ID {
			dv.Waiting = v.NotPlayable
		}
		if v.SyncTarget == "" {
			v.SyncTarget = d.ID
		}
		if d.LastSuccess > v.LastSync {
			v.LastSync = d.LastSuccess
		}
		used[d.ConnectionID] = true
		v.Destinations = append(v.Destinations, dv)
	}
	for _, s := range o.Plex {
		if s.AccountLinked && !used[s.ID] {
			v.Eligible = append(v.Eligible, s)
		}
		if s.ID != v.Server.ID {
			v.OtherPlex = append(v.OtherPlex, s)
		}
	}
	for _, j := range o.Jellyfin {
		v.Jellyfin = append(v.Jellyfin, jellyfinPublic(j))
	}
	interval := o.Defaults.SyncInterval
	if len(v.Destinations) > 0 {
		interval = v.Destinations[0].IntervalMinutes
	}
	v.SyncIntervals = intervals(interval, true)
	// Source subscription.
	sub, err := a.store.CollectionSubscription(ctx, c.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err == nil {
		sv := &sourceView{Subscription: sub, PendingHash: fmt.Sprintf("%x", sha256.Sum256(sub.Pending))}
		if len(sub.Pending) > 0 {
			before, _ := portable.Decode(sub.LastValid)
			after, _ := portable.Decode(sub.Pending)
			sv.Changes = portable.Diff(before, after)
			if tab == "source" {
				v.Changes = portable.DiffDetail(before, after)
				for _, change := range v.Changes {
					switch change.Kind {
					case "added":
						v.Added++
					case "moved":
						v.Moved++
					case "changed":
						v.Changed++
					case "removed":
						v.Removed++
					}
				}
			}
			sv.Proposed = "pending"
		}
		v.Source = sv
		p.Source = sv
		v.SourceIntervals = intervals(sub.IntervalMinutes, false)
	}
	switch tab {
	case "missing":
		if err = a.missingTab(ctx, c, v, o, matchConnection); err != nil {
			return p, err
		}
	case "history", "publishing":
		runs, err := a.store.SyncRuns(ctx, c.ID)
		if err != nil {
			return p, err
		}
		names := map[string]string{}
		for _, d := range v.Destinations {
			names[d.ID] = d.Server.Name
		}
		for _, run := range runs {
			v.Runs = append(v.Runs, syncRunView{SyncRun: run, Destination: names[run.DestinationID]})
		}
		if tab == "history" {
			if v.Jobs, err = a.store.CollectionJobs(ctx, c.ID); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

func (a *App) missingTab(ctx context.Context, c store.Collection, v *collectionView, o *overview, connection string) error {
	v.MatchServer = v.Server
	if connection != "" {
		s, ok := o.plexByID[connection]
		if !ok {
			return sql.ErrNoRows
		}
		v.MatchServer = s
	}
	if v.MatchServer.ID == "" {
		return nil
	}
	v.OtherPlex = nil
	for _, s := range o.Plex {
		if s.ID != v.MatchServer.ID {
			v.OtherPlex = append(v.OtherPlex, s)
		}
	}
	items, scanned, err := a.store.Catalogue(ctx, v.MatchServer.ID)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrConflict) {
		return nil
	}
	if err != nil {
		return err
	}
	v.MatchReady, v.MatchScanned = true, scanned
	overrides, err := a.store.ManualMatches(ctx, v.MatchServer.ID, c)
	if err != nil {
		return err
	}
	index := matching.NewIndex(items)
	for i, e := range c.Entries {
		result := index.Resolve(matching.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}, overrides[e.ID])
		if result.State != "available" || overrides[e.ID] != "" {
			v.MissingRows = append(v.MissingRows, missingRow{Entry: e, Position: i + 1, Result: result, Manual: overrides[e.ID] != ""})
		}
	}
	return nil
}

func (a *App) renderCollection(w http.ResponseWriter, r *http.Request, status int, c store.Collection, tab, connection string, change func(*page)) {
	p, err := a.collectionPage(r.Context(), r, c, tab, connection)
	if errors.Is(err, store.ErrInvalid) {
		http.Error(w, err.Error(), 400)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, "collection page failed")
		return
	}
	if change != nil {
		change(&p)
	}
	a.render(w, r, status, p)
}
