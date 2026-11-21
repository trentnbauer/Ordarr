package app

import (
	"context"
	"net/http"
	"strconv"
)

// connItem is one row of the shared connections list: a connected service, or a placeholder
// for one that isn't connected yet (so a missing Radarr is visible, not implied).
type connItem struct {
	Name, Kind, Host, Status, Href string
	Warn, Active, Placeholder      bool
}

type connListView struct {
	Items   []connItem
	Connect []connLink // "+ Connect" choices
}
type connLink struct{ Label, Href string }

// connList builds the list shown on the left of every connection page. active is the ID of
// the selected connection ("" for none; the Plex page passes "plex").
func connList(o *overview, active string) *connListView {
	v := &connListView{Connect: []connLink{
		{"Plex server", "/settings/plex#add-server"}, {"Sonarr", "/settings/acquisition?new=1"},
		{"Radarr", "/settings/acquisition?new=1"}, {"Jellyfin", "/settings/jellyfin?new=1"}}}
	if len(o.Plex) == 0 {
		v.Items = append(v.Items, connItem{Name: "Plex", Kind: "Media server", Status: "Not connected · needed to publish playlists", Warn: true, Href: "/settings/plex#add-server", Placeholder: true})
	}
	for _, p := range o.Plex {
		item := connItem{Name: p.Name, Kind: "Plex", Host: hostOf(p.BaseURL), Href: "/settings/plex#server-" + p.ID, Active: active == "plex"}
		switch {
		case p.LastError != "":
			item.Status, item.Warn = "Last check failed", true
		case p.AccountLinked:
			item.Status = "Connected · can publish"
		default:
			item.Status = "Connected · read-only"
		}
		v.Items = append(v.Items, item)
	}
	for _, j := range o.Jellyfin {
		v.Items = append(v.Items, connItem{Name: j.Name, Kind: "Jellyfin", Host: hostOf(j.BaseURL), Status: "Matching only", Href: "/settings/jellyfin?id=" + j.ID, Active: active == j.ID})
	}
	have := map[string]bool{}
	for _, c := range o.Arr {
		have[c.Kind] = true
	}
	for _, c := range o.Arr {
		queued, downloading := queueCounts(c.Snapshot)
		item := connItem{Name: c.Name, Kind: map[string]string{"sonarr": "Sonarr", "radarr": "Radarr"}[c.Kind], Host: hostOf(c.BaseURL), Href: "/settings/acquisition?id=" + c.ID, Active: active == c.ID}
		switch {
		case !arrReady(c):
			item.Status, item.Warn = "Choose a quality profile and root folder", true
		case downloading > 0:
			item.Status = strconv.Itoa(downloading) + " downloading"
		case queued > 0:
			item.Status = strconv.Itoa(queued) + " queued"
		default:
			item.Status = "Connected"
		}
		v.Items = append(v.Items, item)
	}
	// A missing downloader is shown plainly, and flagged when the other one is set up.
	for _, k := range []struct{ kind, name, what string }{{"sonarr", "Sonarr", "TV episodes"}, {"radarr", "Radarr", "movies"}} {
		if !have[k.kind] {
			other := map[string]string{"sonarr": "radarr", "radarr": "sonarr"}[k.kind]
			v.Items = append(v.Items, connItem{Name: k.name, Kind: "Downloader", Status: "Not connected · missing " + k.what + " won't be downloaded", Warn: have[other], Href: "/settings/acquisition?new=1", Placeholder: true})
		}
	}
	return v
}

// connectionsPage opens the first connection; the list on the left shows the rest.
func (a *App) connectionsPage(w http.ResponseWriter, r *http.Request) {
	o, err := a.loadOverview(r.Context())
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	target := "/settings/plex"
	if items := connList(o, "").Items; len(items) > 0 {
		target = items[0].Href
	}
	http.Redirect(w, r, target, 303)
}

// wizardActive: a fresh install (no collections yet) that hasn't finished the setup wizard.
// Installs that already have collections never see the wizard.
func (a *App) wizardActive(ctx context.Context) bool {
	if a.store.Pref(ctx, "wizard_done") != "" {
		return false
	}
	n, err := a.store.CollectionCount(ctx)
	return err == nil && n == 0
}

// setupNext is where to go after connecting a service: the setup wizard until it is finished,
// the service's own page afterwards.
func (a *App) setupNext(ctx context.Context, fallback string) string {
	if a.wizardActive(ctx) {
		return "/welcome"
	}
	return fallback
}
