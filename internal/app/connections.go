package app

import (
	"context"
	"net/http"
)

// serviceCard is one service on the Connections page.
type serviceCard struct {
	Name, State  string
	Warn         bool
	Items        []string
	Manage, Add  string
	AddLabel     string
	Optional     bool
	Consequences string // what happens without it
}

type connectionsView struct{ Cards []serviceCard }

// connectionCards builds one card per service. Plex, Sonarr and Radarr are always listed, so a
// missing Radarr is visible instead of being implied by "Sonarr / Radarr".
func connectionCards(o *overview) []serviceCard {
	plexCard := serviceCard{Name: "Plex", Manage: "/settings/plex", Add: "/settings/plex", AddLabel: "Connect Plex"}
	if len(o.Plex) == 0 {
		plexCard.State, plexCard.Warn = "Not connected", true
		plexCard.Consequences = "Needed to publish playlists and to see what you can already watch."
	} else {
		plexCard.State = "Connected"
		plexCard.AddLabel = "Add another server"
		for _, p := range o.Plex {
			line := p.Name
			switch {
			case p.LastError != "":
				line += " · needs attention: " + p.LastError
				plexCard.State, plexCard.Warn = "Needs attention", true
			case !p.AccountLinked:
				line += " · read-only (added by URL and token; can't publish)"
			}
			plexCard.Items = append(plexCard.Items, line)
		}
	}
	cards := []serviceCard{plexCard}
	kinds := map[string][]string{}
	for _, c := range o.Arr {
		line := c.Name
		if !arrReady(c) {
			line += " · choose a quality profile and root folder"
		}
		kinds[c.Kind] = append(kinds[c.Kind], line)
	}
	arrCard := func(kind, name, what string, other bool) serviceCard {
		card := serviceCard{Name: name, Manage: "/settings/acquisition", Add: "/settings/acquisition?new=1", AddLabel: "Connect " + name, Optional: true}
		if items := kinds[kind]; len(items) > 0 {
			card.State, card.Items, card.AddLabel = "Connected", items, "Add another "+name
			return card
		}
		card.State = "Not connected"
		card.Consequences = "Missing " + what + " won't be downloaded."
		card.Warn = other // flagged when the other downloader is set up, so the gap is clear
		return card
	}
	cards = append(cards,
		arrCard("sonarr", "Sonarr", "TV episodes", len(kinds["radarr"]) > 0),
		arrCard("radarr", "Radarr", "movies", len(kinds["sonarr"]) > 0))
	jf := serviceCard{Name: "Jellyfin", Manage: "/settings/jellyfin", Add: "/settings/jellyfin?new=1", AddLabel: "Connect Jellyfin", Optional: true}
	if len(o.Jellyfin) == 0 {
		jf.State = "Not connected"
		jf.Consequences = "Optional. Jellyfin can only check availability for now; it can't publish playlists."
	} else {
		jf.State = "Connected · read-only"
		jf.AddLabel = "Add another server"
		for _, j := range o.Jellyfin {
			jf.Items = append(jf.Items, j.Name)
		}
	}
	return append(cards, jf)
}

func (a *App) connectionsPage(w http.ResponseWriter, r *http.Request) {
	o, err := a.loadOverview(r.Context())
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	a.render(w, r, 200, page{Title: "Connections", Mode: "connections", Connections: &connectionsView{Cards: connectionCards(o)}})
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
