package app

import (
	"context"
	"net/http"
	"sort"
)

// pickItem is one entry that matched more than one Plex item.
type pickItem struct {
	CollectionID, Collection, EntryID, Entry string
	Position                                 int
}

// failedItem is something a downloader could not find or add.
type failedItem struct{ Title, Service, Message string }

// inputView is everything that genuinely needs the administrator. In the happy path it is empty.
type inputView struct {
	Picks       []pickItem
	NotFound    []failedItem
	Sources     []waitingSource
	Failed      []waitingSync
	Approvals   []waitingSync
	Requests    int      // legacy download requests still waiting for approval
	Connections []string // servers that failed their last check
	Total       int
}

// inputCount is the sidebar badge. It counts exactly what the Needs your input page lists:
// picks, items no downloader could find, source updates, failed or waiting playlist
// changes, requests waiting for approval and servers that failed their last check.
func (a *App) inputCount(ctx context.Context, o *overview, values map[string]availability) (int, error) {
	n := 0
	for _, v := range values {
		n += v.NeedPick
	}
	failed, err := a.store.FailedRequestCount(ctx)
	if err != nil {
		return 0, err
	}
	n += failed
	for _, s := range o.Sources {
		if s.Pending {
			n++
		}
	}
	for _, list := range o.Destinations {
		for _, d := range list {
			if run, ok := o.Latest[d.ID]; ok && (run.State == "failed" || run.State == "pending") {
				n++
			}
		}
	}
	pending, err := a.pendingRequests(ctx)
	if err != nil {
		return 0, err
	}
	n += pending
	for _, p := range o.Plex {
		if p.LastError != "" {
			n++
		}
	}
	return n, nil
}

func (a *App) needsInput(ctx context.Context, o *overview, values map[string]availability) (*inputView, error) {
	v := &inputView{}
	act, err := a.activity(ctx, o)
	if err != nil {
		return nil, err
	}
	v.Sources, v.Failed, v.Approvals, v.Requests = act.Sources, act.Failed, act.Approvals, act.Requests
	for _, ref := range o.Refs {
		av := values[ref.ID]
		if av.NeedPick == 0 {
			continue
		}
		c, err := a.store.Collection(ctx, ref.ID)
		if err != nil {
			continue
		}
		for i, e := range c.Entries {
			if av.States[e.ID] == "unmatched" {
				v.Picks = append(v.Picks, pickItem{CollectionID: c.ID, Collection: c.Title, EntryID: e.ID, Entry: e.Title, Position: i + 1})
			}
		}
	}
	index, err := a.store.AcquisitionIndex(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, c := range o.Arr {
		names[c.ID] = c.Name
	}
	for _, r := range index {
		if r.State == "failed" {
			v.NotFound = append(v.NotFound, failedItem{Title: r.Title, Service: names[r.ConnectionID], Message: r.Message})
		}
	}
	sort.Slice(v.NotFound, func(i, j int) bool { return v.NotFound[i].Title < v.NotFound[j].Title })
	for _, p := range o.Plex {
		if p.LastError != "" {
			v.Connections = append(v.Connections, p.Name+": "+p.LastError)
		}
	}
	v.Total = len(v.Picks) + len(v.NotFound) + len(v.Sources) + len(v.Failed) + len(v.Approvals) + v.Requests + len(v.Connections)
	return v, nil
}

func (a *App) needsInputPage(w http.ResponseWriter, r *http.Request) {
	o, err := a.loadOverview(r.Context())
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	values, err := a.availabilityAll(r.Context(), o)
	if err != nil {
		a.fail(w, "availability failed")
		return
	}
	v, err := a.needsInput(r.Context(), o, values)
	if err != nil {
		a.fail(w, "needs input failed")
		return
	}
	a.render(w, r, 200, page{Title: "Needs your input", Mode: "needs-input", NeedsInput: v})
}

// retired pages keep their old addresses working.
func redirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, 303) }
}
