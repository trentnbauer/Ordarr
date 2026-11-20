package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/store"
)

type missingItem struct {
	Key, Used, Status, Class string
	Entry                    store.Entry
	Requestable, Downloading bool
	NotRequested             bool
	SetupLink                string
}
type missingView struct {
	Items                                 []missingItem
	Filter                                string
	All, NotRequested, Downloading, Ready int
	Requestable                           int
	Servers                               []string
	HasPlex, Checked                      bool
}

// arrFor picks the Sonarr (TVDB episodes) or Radarr (TMDB movies) instance
// for an entry, preferring one with a profile and root folder chosen.
func arrFor(list []store.ArrConnection, e store.Entry) (store.ArrConnection, int, bool) {
	var best store.ArrConnection
	found, id := false, 0
	for _, c := range list {
		n, ok := providerNumber(c, e)
		if !ok {
			continue
		}
		if !found || best.ProfileID == 0 && c.ProfileID > 0 {
			best, id, found = c, n, true
		}
	}
	return best, id, found
}

type requestKey struct {
	connection string
	provider   int
}

func (a *App) requestStates(ctx context.Context) (map[requestKey]string, error) {
	rows, err := a.store.DB.QueryContext(ctx, "SELECT connection_id,provider_id,state FROM acquisition_requests")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[requestKey]string{}
	for rows.Next() {
		var k requestKey
		var state string
		if err = rows.Scan(&k.connection, &k.provider, &state); err != nil {
			return nil, err
		}
		result[k] = state
	}
	return result, rows.Err()
}

func (a *App) missingItems(ctx context.Context) (*missingView, *overview, map[string]availability, error) {
	o, err := a.loadOverview(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	values, err := a.availabilityAll(ctx, o)
	if err != nil {
		return nil, nil, nil, err
	}
	titles, err := a.store.CollectionTitles(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	requests, err := a.requestStates(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	v := &missingView{HasPlex: len(o.Plex) > 0, Checked: len(values) > 0}
	servers := map[string]bool{}
	type group struct {
		item  missingItem
		uses  map[string]int
		order []string
	}
	groups := map[string]*group{}
	var keys []string
	for _, ref := range o.Refs {
		value, ok := values[ref.ID]
		if !ok {
			continue
		}
		if s, ok := o.primary(ref.ID); ok && !servers[s.Name] {
			servers[s.Name] = true
			v.Servers = append(v.Servers, s.Name)
		}
		for _, e := range value.MissingEntries {
			identity := store.EntryIdentity(e)
			g := groups[identity]
			if g == nil {
				g = &group{item: missingItem{Key: ref.ID + ":" + e.ID, Entry: e}, uses: map[string]int{}}
				groups[identity] = g
				keys = append(keys, identity)
			}
			if g.uses[ref.ID] == 0 {
				g.order = append(g.order, ref.ID)
			}
			g.uses[ref.ID]++
		}
	}
	for _, k := range keys {
		g := groups[k]
		var used []string
		for _, id := range g.order {
			label := titles[id]
			if g.uses[id] > 1 {
				label += " · " + strconv.Itoa(g.uses[id]) + "×"
			}
			used = append(used, label)
		}
		item := g.item
		item.Used = strings.Join(used, ", ")
		describe(&item, o.Arr, requests)
		v.Items = append(v.Items, item)
	}
	sort.SliceStable(v.Items, func(i, j int) bool { return v.Items[i].Entry.Title < v.Items[j].Entry.Title })
	for _, item := range v.Items {
		v.All++
		if item.NotRequested {
			v.NotRequested++
		}
		if item.Requestable {
			v.Requestable++
		}
		if item.Downloading {
			v.Downloading++
		}
	}
	return v, o, values, nil
}

func describe(item *missingItem, services []store.ArrConnection, requests map[requestKey]string) {
	e := item.Entry
	service := "Sonarr"
	if e.Kind == "movie" {
		service = "Radarr"
	}
	c, id, ok := arrFor(services, e)
	if !ok {
		item.Class = "muted"
		if e.Kind == "movie" && e.Provider != "tmdb" || e.Kind == "episode" && e.Provider != "tvdb" {
			item.Status = "Needs a " + map[string]string{"Sonarr": "TVDB", "Radarr": "TMDB"}[service] + " ID to request"
			return
		}
		item.Status = "No " + service + " connected"
		item.SetupLink = "/settings/acquisition"
		return
	}
	if media, exists, err := arr.Existing(arr.Connection{Kind: c.Kind}, c.Snapshot.Media, id); err == nil && exists {
		switch state := arr.Status(c.Snapshot, media); {
		case state == "downloading" || state == "queued":
			item.Status, item.Class, item.Downloading = strings.ToUpper(state[:1])+state[1:]+" in "+c.Name, "", true
		case strings.HasPrefix(state, "files present"):
			item.Status, item.Class = "Files in "+c.Name+" · waiting for Plex", ""
		default:
			item.Status, item.Class = "Monitored in "+c.Name, "muted"
		}
		return
	}
	switch requests[requestKey{c.ID, id}] {
	case "pending":
		item.Status, item.Class = "Waiting for your approval", "warn"
	case "approved", "running", "completed":
		item.Status, item.Class = "Requested from "+c.Name, ""
	case "uncertain", "failed":
		item.Status, item.Class = "Request needs a retry", "warn"
	case "declined":
		item.Status, item.Class = "Declined", "muted"
	default:
		item.NotRequested = true
		item.Status, item.Class = "Not in "+c.Name, "muted"
		if c.ProfileID > 0 && c.RootID > 0 {
			item.Requestable = true
		} else {
			item.SetupLink = "/settings/acquisition?id=" + c.ID
		}
	}
}

func (a *App) missingPage(w http.ResponseWriter, r *http.Request) {
	v, o, values, err := a.missingItems(r.Context())
	if err != nil {
		a.fail(w, "missing media failed")
		return
	}
	v.Filter = r.URL.Query().Get("show")
	if v.Filter == "not-requested" || v.Filter == "downloading" {
		var kept []missingItem
		for _, item := range v.Items {
			if v.Filter == "not-requested" && item.NotRequested || v.Filter == "downloading" && item.Downloading {
				kept = append(kept, item)
			}
		}
		v.Items = kept
	} else {
		v.Filter = ""
	}
	p := page{Title: "Missing media", Mode: "missing", MissingMedia: v}
	if p.Nav, err = a.navFor(r.Context(), "missing", o, values); err != nil {
		a.fail(w, "navigation failed")
		return
	}
	switch r.URL.Query().Get("result") {
	case "requested":
		p.Notice = "Requests sent. Progress shows in Activity and on the Sonarr / Radarr page."
	}
	a.render(w, r, 200, p)
}

// requestMissing requests whole movies or series for missing entries. The
// confirmation checkbox acknowledges the whole-media effect.
func (a *App) requestMissing(w http.ResponseWriter, r *http.Request) {
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Confirm that requests cover the whole movie or series.", 400)
		return
	}
	v, o, _, err := a.missingItems(r.Context())
	if err != nil {
		a.fail(w, "missing media failed")
		return
	}
	wanted := map[string]bool{}
	for _, k := range r.PostForm["item"] {
		wanted[k] = true
	}
	all := r.PostForm.Get("all") == "yes"
	queued := 0
	for _, item := range v.Items {
		if !item.Requestable || !all && !wanted[item.Key] || queued >= 25 {
			continue
		}
		c, id, ok := arrFor(o.Arr, item.Entry)
		if !ok {
			continue
		}
		full, err := a.store.ArrConnection(r.Context(), c.ID)
		if err != nil {
			continue
		}
		conn, err := a.arrAccess(full)
		if err != nil {
			a.fail(w, "credential access failed")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		media, err := arr.New().Lookup(ctx, conn, id)
		cancel()
		if err != nil {
			continue
		}
		request, err := a.store.ProposeAcquisition(r.Context(), full, id, media.Title, "manual", true)
		if err != nil {
			continue
		}
		if _, err = a.store.QueueJob(r.Context(), "acquire", request); err != nil {
			a.fail(w, "queue acquisition failed")
			return
		}
		queued++
	}
	if queued == 0 {
		http.Error(w, fmt.Sprintf("No requests could be sent. Check that each service is reachable and has a quality profile and root folder chosen."), 400)
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/missing?result=requested", 303)
}
