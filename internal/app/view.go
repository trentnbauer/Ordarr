package app

import (
	"context"
	"fmt"
	"html/template"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// Version is set at build time with -ldflags "-X .../internal/app.Version=v1.2.3".
var Version = "dev"

// navView feeds the sidebar shown on every signed-in page.
type navView struct {
	Section                         string
	Username                        string
	Collections, Missing            int
	Plex, Jellyfin, Arr             string
	PlexWarn, MissingWarn, ArrAlert bool
}

// overview is the cross-collection state the shell and lists are drawn from.
type overview struct {
	Plex         []plexView
	plexByID     map[string]plexView
	Defaults     store.Defaults
	Destinations map[string][]store.Destination
	Latest       map[string]store.SyncRun
	Sources      map[string]store.SourceState
	Refs         []store.CollectionRef
	Jellyfin     []store.JellyfinConnection
	Arr          []store.ArrConnection
}

func (a *App) loadOverview(ctx context.Context) (*overview, error) {
	o := &overview{plexByID: map[string]plexView{}, Destinations: map[string][]store.Destination{}}
	ids, err := a.store.PlexConnectionIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c, err := a.store.PlexConnection(ctx, id)
		if err != nil {
			continue
		}
		v := plexPublic(c)
		o.Plex = append(o.Plex, v)
		o.plexByID[v.ID] = v
	}
	if o.Defaults, err = a.store.Defaults(ctx); err != nil {
		return nil, err
	}
	all, err := a.store.AllDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		o.Destinations[d.CollectionID] = append(o.Destinations[d.CollectionID], d)
	}
	if o.Latest, err = a.store.LatestSyncRuns(ctx); err != nil {
		return nil, err
	}
	if o.Sources, err = a.store.SourceStates(ctx); err != nil {
		return nil, err
	}
	if o.Refs, err = a.store.CollectionRefs(ctx); err != nil {
		return nil, err
	}
	if o.Jellyfin, err = a.store.JellyfinConnections(ctx); err != nil {
		return nil, err
	}
	if o.Arr, err = a.store.ArrConnections(ctx); err != nil {
		return nil, err
	}
	for i := range o.Arr {
		o.Arr[i].KeyCipher = nil
	}
	for i := range o.Jellyfin {
		o.Jellyfin[i].TokenCipher = nil
	}
	return o, nil
}

// primary is the Plex server a collection is checked against: where it is
// published, else the default for new collections, else the first server.
func (o *overview) primary(collection string) (plexView, bool) {
	for _, d := range o.Destinations[collection] {
		if v, ok := o.plexByID[d.ConnectionID]; ok {
			return v, true
		}
	}
	if v, ok := o.plexByID[o.Defaults.ConnectionID]; ok {
		return v, true
	}
	if len(o.Plex) > 0 {
		return o.Plex[0], true
	}
	return plexView{}, false
}
func (o *overview) connectionName(id string) string {
	if v, ok := o.plexByID[id]; ok {
		return v.Name
	}
	return "Plex"
}

// availabilityAll matches every collection against its primary server.
func (a *App) availabilityAll(ctx context.Context, o *overview) (map[string]availability, error) {
	groups := map[string][]store.CollectionRef{}
	live := map[string]bool{}
	for _, ref := range o.Refs {
		live[ref.ID] = true
		if v, ok := o.primary(ref.ID); ok {
			groups[v.ID] = append(groups[v.ID], ref)
		}
	}
	a.avail.prune(live)
	result := map[string]availability{}
	for conn, refs := range groups {
		values, err := a.availabilityFor(ctx, refs, conn)
		if err != nil {
			return nil, err
		}
		for id, v := range values {
			result[id] = v
		}
	}
	return result, nil
}

// missingIdentities counts distinct missing media across collections.
func missingIdentities(values map[string]availability) int {
	seen := map[string]bool{}
	for _, v := range values {
		for _, e := range v.MissingEntries {
			seen[store.EntryIdentity(e)] = true
		}
	}
	return len(seen)
}

func (a *App) navFor(ctx context.Context, section string, o *overview, values map[string]availability) (*navView, error) {
	n := &navView{Section: section, Collections: len(o.Refs)}
	name, _, err := a.store.Admin(ctx)
	if err != nil {
		return nil, err
	}
	n.Username = name
	n.Missing = missingIdentities(values)
	n.MissingWarn = n.Missing > 0
	switch {
	case len(o.Plex) == 0:
		n.Plex, n.PlexWarn = "Set up", true
	default:
		n.Plex = "OK"
		for _, p := range o.Plex {
			if p.LastError != "" {
				n.Plex, n.PlexWarn = "Check", true
			}
		}
	}
	if len(o.Jellyfin) > 0 {
		n.Jellyfin = "Read-only"
	}
	if len(o.Arr) > 0 {
		n.Arr = "Off"
		if o.Defaults.AutoDownload {
			n.Arr = "Auto"
		}
		pending, err := a.pendingRequests(ctx)
		if err != nil {
			return nil, err
		}
		if pending > 0 {
			n.Arr, n.ArrAlert = strconv.Itoa(pending)+" waiting", true
		}
	}
	return n, nil
}
func (a *App) pendingRequests(ctx context.Context) (int, error) {
	var n int
	err := a.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM acquisition_requests WHERE state='pending'").Scan(&n)
	return n, err
}

// withNav attaches the sidebar to a page, loading shared state when needed.
func (a *App) withNav(ctx context.Context, p *page, section string) error {
	o, err := a.loadOverview(ctx)
	if err != nil {
		return err
	}
	values, err := a.availabilityAll(ctx, o)
	if err != nil {
		return err
	}
	p.Nav, err = a.navFor(ctx, section, o, values)
	return err
}

// listItem is one row of the collection list column.
type listItem struct {
	ID, Title, Status string
	Warn, Active      bool
}

func collectionStatus(o *overview, values map[string]availability, id string) (string, bool) {
	if s, ok := o.Sources[id]; ok {
		if s.Pending {
			return "Source update waiting", true
		}
		if s.Failures > 0 {
			return "Source check failed · last good version kept", true
		}
	}
	for _, d := range o.Destinations[id] {
		if run, ok := o.Latest[d.ID]; ok {
			switch run.State {
			case "failed":
				return "Sync failed · retry", true
			case "pending":
				return "Playlist change waiting for approval", true
			}
		}
	}
	v, checked := values[id]
	for _, d := range o.Destinations[id] {
		if d.RemoteID != "" {
			status := "Live on " + o.connectionName(d.ConnectionID)
			if checked && v.Label() != "" {
				return status + " · " + v.Label(), false
			}
			return status, false
		}
	}
	if len(o.Destinations[id]) > 0 {
		return "Publishing to " + o.connectionName(o.Destinations[id][0].ConnectionID) + " · not synced yet", false
	}
	if checked && v.Label() != "" {
		return "Not published · " + v.Label(), false
	}
	return "Not published", false
}

// Template helpers.
func ago(unix int64) string {
	if unix <= 0 {
		return "never"
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return time.Unix(unix, 0).Format("2 Jan 2006")
}
func since(unix int64) string {
	if unix <= 0 {
		return "never"
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}
func clock(unix int64) string {
	if unix <= 0 {
		return "—"
	}
	t := time.Unix(unix, 0)
	now := time.Now()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 48*time.Hour && t.YearDay() == now.Add(-24*time.Hour).YearDay():
		return "Yesterday"
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	}
	return t.Format("2 Jan")
}
func episodeLabel(e store.Entry) string {
	if e.Kind == "movie" {
		return "Film"
	}
	return fmt.Sprintf("S%dE%d", e.Season, e.Episode)
}
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
func number(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sourceName is the file name of a source URL.
func sourceName(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		if name := path.Base(u.Path); name != "/" && name != "." {
			if unescaped, err := url.PathUnescape(name); err == nil {
				return unescaped
			}
			return name
		}
	}
	return raw
}
func runState(state string) string {
	switch state {
	case "succeeded":
		return "Synced"
	case "failed":
		return "Failed"
	case "pending":
		return "Needs approval"
	case "approved":
		return "Approved"
	}
	return state
}

// playlistName removes Ordarr's ownership marker for display.
func playlistName(title string) string {
	if i := strings.LastIndex(title, " [Ordarr:"); i >= 0 {
		return title[:i]
	}
	return title
}

var templateFuncs = template.FuncMap{
	"inc": func(n int) int { return n + 1 }, "dec": func(n int) int { return n - 1 },
	"ago": ago, "since": since, "clock": clock, "ep": episodeLabel, "plural": plural, "number": number,
	"playlist": playlistName,
	"add":      func(a, b int) int { return a + b },
	"base":     sourceName,
	"runState": runState,
	"version":  func() string { return Version },
}

func section(mode string) string {
	switch mode {
	case "jobs":
		return "activity"
	case "missing":
		return "missing"
	case "plex", "media":
		return "plex"
	case "jellyfin", "jellyfin-matches":
		return "jellyfin"
	case "acquisition":
		return "arr"
	case "settings":
		return "settings"
	}
	return "collections"
}
