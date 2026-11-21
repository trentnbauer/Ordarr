package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/portable"
	"github.com/trentnbauer/Ordarr/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// ---- Activity ----

type waitingSource struct {
	CollectionID, Title, PendingHash string
	Revision                         int
	Changes                          portable.Changes
}
type waitingSync struct {
	DestinationID, CollectionID, Collection, Server, Message string
	RunID                                                    int64
}
type jobRow struct {
	When                      int64
	Kind, What, Result, Class string
	Routine                   bool  // a successful check that changed nothing; runs of these fold into one row
	Count                     int   // checks folded into this row
	Oldest                    int64 // time of the earliest folded check
}
type activityView struct {
	Sources           []waitingSource
	Failed, Approvals []waitingSync
	Requests          int
	Jobs              []jobRow
	Waiting           int
}

var jobKinds = map[string]string{"scan": "Library scan", "jellyfin-scan": "Library scan", "sync": "Playlist sync", "publish": "Playlist sync", "source": "Source check", "arr-refresh": "Service refresh", "arr-auto": "Automatic requests", "acquire": "Request"}

func (a *App) activity(ctx context.Context, o *overview) (*activityView, error) {
	v := &activityView{}
	titles, err := a.store.CollectionTitles(ctx)
	if err != nil {
		return nil, err
	}
	for id, s := range o.Sources {
		if !s.Pending {
			continue
		}
		sub, err := a.store.CollectionSubscription(ctx, id)
		if err != nil {
			continue
		}
		before, _ := portable.Decode(sub.LastValid)
		after, _ := portable.Decode(sub.Pending)
		v.Sources = append(v.Sources, waitingSource{CollectionID: id, Title: titles[id], Revision: sub.Revision, PendingHash: fmt.Sprintf("%x", sha256.Sum256(sub.Pending)), Changes: portable.Diff(before, after)})
	}
	sort.Slice(v.Sources, func(i, j int) bool { return v.Sources[i].Title < v.Sources[j].Title })
	destinations := map[string]store.Destination{}
	for id, list := range o.Destinations {
		for _, d := range list {
			destinations[d.ID] = d
			run, ok := o.Latest[d.ID]
			if !ok {
				continue
			}
			w := waitingSync{DestinationID: d.ID, CollectionID: id, Collection: titles[id], Server: o.connectionName(d.ConnectionID), Message: run.Message, RunID: run.ID}
			switch run.State {
			case "failed":
				v.Failed = append(v.Failed, w)
			case "pending":
				v.Approvals = append(v.Approvals, w)
			}
		}
	}
	byTitle := func(list []waitingSync) func(i, j int) bool {
		return func(i, j int) bool {
			if list[i].Collection != list[j].Collection {
				return list[i].Collection < list[j].Collection
			}
			return list[i].DestinationID < list[j].DestinationID
		}
	}
	sort.Slice(v.Failed, byTitle(v.Failed))
	sort.Slice(v.Approvals, byTitle(v.Approvals))
	if v.Requests, err = a.pendingRequests(ctx); err != nil {
		return nil, err
	}
	v.Waiting = len(v.Sources) + len(v.Failed) + len(v.Approvals)
	if v.Requests > 0 {
		v.Waiting++
	}
	jobs, err := a.store.RecentJobs(ctx)
	if err != nil {
		return nil, err
	}
	subscriptions := map[string]string{}
	for id, s := range o.Sources {
		subscriptions[s.SubscriptionID] = id
	}
	names := map[string]string{}
	for _, p := range o.Plex {
		names[p.ID] = p.Name
	}
	for _, j := range o.Jellyfin {
		names[j.ID] = j.Name
	}
	for _, c := range o.Arr {
		names[c.ID] = c.Name
	}
	for _, j := range jobs {
		row := jobRow{When: j.CreatedAt, Kind: jobKinds[j.Kind], What: names[j.Target], Routine: routineSource(j)}
		if row.Kind == "" {
			row.Kind = j.Kind
		}
		switch j.Kind {
		case "sync", "publish":
			if d, ok := destinations[strings.SplitN(j.Target, "@", 2)[0]]; ok {
				row.What = titles[d.CollectionID] + " → " + o.connectionName(d.ConnectionID)
			}
		case "source":
			row.What = titles[subscriptions[j.Target]]
		case "acquire":
			if r, err := a.store.Acquisition(ctx, j.Target); err == nil {
				row.What = r.Title
			}
		}
		if row.What == "" {
			row.What = "Removed item"
		}
		switch j.State {
		case "queued":
			row.Result, row.Class = "Queued", "muted"
		case "running":
			row.Result = "Running"
			if j.Progress > 0 {
				row.Result += " · " + number(j.Progress) + " items"
			}
		case "failed":
			row.Result, row.Class = "Failed · "+j.Message, "warn"
		default:
			row.Result = j.Message
			if j.Progress > 0 {
				row.Result += " · " + number(j.Progress) + " items"
			}
		}
		v.Jobs = append(v.Jobs, row)
	}
	v.Jobs = collapseRoutine(v.Jobs, time.Now())
	if len(v.Jobs) > 50 {
		v.Jobs = v.Jobs[:50]
	}
	return v, nil
}

// ---- Settings ----

type settingsView struct {
	Username, Address, DataDir string
	Secure                     bool
	Defaults                   store.Defaults
	Servers                    []plexView
	Intervals                  []intervalOption
}

func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, status int, message, notice string) {
	name, _, err := a.store.Admin(r.Context())
	if err != nil {
		a.fail(w, "administrator lookup failed")
		return
	}
	o, err := a.loadOverview(r.Context())
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	v := &settingsView{Username: name, Address: a.config.Address, DataDir: a.config.DataDir, Secure: a.config.SecureCookies, Defaults: o.Defaults, Servers: o.Plex, Intervals: intervals(o.Defaults.SyncInterval, true)}
	a.render(w, r, status, page{Title: "Settings", Mode: "settings", Username: name, Address: a.config.Address, DataDir: a.config.DataDir, Secure: a.config.SecureCookies, Preferences: v, Error: message, Notice: notice})
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	notice := map[string]string{"password": "Password changed. Other browsers were signed out.", "defaults": "Defaults saved. They apply to collections you add from now on."}[r.URL.Query().Get("result")]
	a.renderSettings(w, r, 200, "", notice)
}
func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(r) {
		a.renderSettings(w, r, 429, "Too many attempts. Try again in a minute.", "")
		return
	}
	_, hash, err := a.store.Admin(r.Context())
	if err != nil {
		a.fail(w, "administrator lookup failed")
		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(r.PostForm.Get("current"))) != nil {
		a.renderSettings(w, r, 400, "Your current password didn't match.", "")
		return
	}
	password := r.PostForm.Get("password")
	if len(password) < 12 || len(password) > 72 || !utf8.ValidString(password) {
		a.renderSettings(w, r, 400, "Use a new password of 12–72 bytes.", "")
		return
	}
	if password != r.PostForm.Get("confirm") {
		a.renderSettings(w, r, 400, "The new passwords didn't match.", "")
		return
	}
	next, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		a.fail(w, "password hashing failed")
		return
	}
	if err = a.store.ChangePassword(r.Context(), next, sessionValue(r)); err != nil {
		a.fail(w, "password change failed")
		return
	}
	a.logger.Info("administrator password changed")
	http.Redirect(w, r, "/settings?result=password", 303)
}
func (a *App) signOutEverywhere(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAllSessions(r.Context()); err != nil {
		a.fail(w, "session revocation failed")
		return
	}
	a.cookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/login", 303)
}

// ---- Activity page ----

func (a *App) jobsPage(w http.ResponseWriter, r *http.Request) {
	o, err := a.loadOverview(r.Context())
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	v, err := a.activity(r.Context(), o)
	if err != nil {
		a.fail(w, "activity failed")
		return
	}
	jobs, err := a.store.RecentJobs(r.Context())
	if err != nil {
		a.fail(w, "jobs failed")
		return
	}
	a.render(w, r, 200, page{Title: "Activity", Mode: "jobs", Activity: v, Jobs: jobs})
}

// ---- Plex ----

type plexServerView struct {
	plexView
	Ready            bool
	ScannedAt        int64
	Movies, Episodes int
	Destinations     int
}
type plexPageView struct {
	Servers []plexServerView
}

func (a *App) plexServers(ctx context.Context, list []plexView) (*plexPageView, error) {
	v := &plexPageView{}
	all, err := a.store.AllDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range list {
		sv := plexServerView{plexView: s}
		if _, scanned, err := a.store.CatalogueGeneration(ctx, s.ID); err == nil {
			sv.Ready, sv.ScannedAt = true, scanned
			if sv.Movies, sv.Episodes, err = a.store.CatalogueSize(ctx, s.ID); err != nil {
				return nil, err
			}
		}
		for _, d := range all {
			if d.ConnectionID == s.ID {
				sv.Destinations++
			}
		}
		v.Servers = append(v.Servers, sv)
	}
	return v, nil
}

// ---- Jellyfin ----

type jellyfinCollectionMatch struct {
	ID, Title    string
	Found, Total int
}
type jellyfinPageView struct {
	Connections []jellyfinView
	Selected    *jellyfinView
	Ready       bool
	Items       int
	ScannedAt   int64
	Matches     []jellyfinCollectionMatch
	Adding      bool
}

func (a *App) jellyfinSettings(w http.ResponseWriter, r *http.Request) {
	connections, err := a.store.JellyfinConnections(r.Context())
	if err != nil {
		a.fail(w, "Jellyfin settings failed")
		return
	}
	v := &jellyfinPageView{Adding: r.URL.Query().Get("new") == "1" || len(connections) == 0}
	p := page{Title: "Jellyfin", Mode: "jellyfin", JellyfinPage: v}
	selected := r.URL.Query().Get("id")
	for _, c := range connections {
		view := jellyfinPublic(c)
		v.Connections = append(v.Connections, view)
		p.JellyfinConnections = append(p.JellyfinConnections, view)
	}
	for i := range v.Connections {
		if !v.Adding && (v.Connections[i].ID == selected || selected == "" && i == 0) {
			v.Selected = &v.Connections[i]
		}
	}
	if selected != "" && v.Selected == nil && !v.Adding {
		http.NotFound(w, r)
		return
	}
	if v.Selected != nil {
		items, scanned, err := a.store.JellyfinCatalogue(r.Context(), v.Selected.ID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, store.ErrConflict) {
			a.fail(w, "Jellyfin catalogue failed")
			return
		}
		if err == nil {
			v.Ready, v.Items, v.ScannedAt = true, len(items), scanned
			index := jellyfin.NewIndex(items)
			ids, err := a.store.CollectionIDs(r.Context())
			if err != nil {
				a.fail(w, "collections failed")
				return
			}
			for i, id := range ids {
				if i >= 50 {
					break
				}
				c, err := a.store.Collection(r.Context(), id)
				if err != nil {
					continue
				}
				manual, err := a.store.JellyfinManualMatches(r.Context(), v.Selected.ID, c)
				if err != nil {
					a.fail(w, "Jellyfin mapping failed")
					return
				}
				m := jellyfinCollectionMatch{ID: c.ID, Title: c.Title, Total: len(c.Entries)}
				for _, e := range c.Entries {
					result := index.Resolve(jellyfin.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}, manual[e.ID])
					if result.State != "missing" && result.State != "unmatched" {
						m.Found++
					}
				}
				v.Matches = append(v.Matches, m)
			}
		}
	}
	a.render(w, r, 200, p)
}
func (a *App) editJellyfin(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.JellyfinConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if revision != c.Revision {
		a.collectionError(w, store.ErrConflict)
		return
	}
	allow := r.PostForm.Get("allow_http") == "yes"
	base, err := plex.NormalizeURL(r.PostForm.Get("url"), allow)
	if err != nil {
		a.jellyfinError(w, err)
		return
	}
	token := r.PostForm.Get("token")
	if token == "" {
		if base != c.BaseURL {
			http.Error(w, "Supply the token again when changing the server URL; the saved token is never sent to a different address.", 400)
			return
		}
		if token, err = a.vault.Open(c.TokenCipher, "jellyfin:"+c.ID+":token"); err != nil {
			a.fail(w, "credential access failed")
			return
		}
	}
	client := jellyfin.New()
	defer client.Close()
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := client.Inspect(ctx, base, token)
	if err != nil {
		a.jellyfinError(w, err)
		return
	}
	if snapshot.ServerID != c.Snapshot.ServerID || snapshot.UserID != c.Snapshot.UserID {
		http.Error(w, "That address or token belongs to a different Jellyfin server or user. Connect it separately.", 409)
		return
	}
	c.Name, c.BaseURL, c.AllowHTTP = strings.TrimSpace(r.PostForm.Get("name")), base, allow
	c.TokenCipher = a.vault.Seal(token, "jellyfin:"+c.ID+":token")
	if err = a.store.UpdateJellyfin(r.Context(), c, revision); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/jellyfin?id="+c.ID, 303)
}

// ---- Sonarr / Radarr ----

// arrView is the only Sonarr/Radarr projection templates receive. It has no
// key field, so no template can render the API key or its ciphertext.
type arrView struct {
	ID, Kind, Name, BaseURL     string
	ProfileID, RootID, Revision int
	Snapshot                    arr.Snapshot
	CheckedAt                   int64
	DailyLimit                  int
	Exclusions                  string
}

func arrPublic(c store.ArrConnection) arrView {
	return arrView{ID: c.ID, Kind: c.Kind, Name: c.Name, BaseURL: c.BaseURL, ProfileID: c.ProfileID, RootID: c.RootID, Revision: c.Revision, Snapshot: c.Snapshot, CheckedAt: c.CheckedAt, DailyLimit: c.DailyLimit, Exclusions: c.Exclusions}
}

type arrListItem struct {
	ID, Name, Kind, Host, Status string
	Warn, Active                 bool
}
type arrDetail struct {
	IsDefault                     bool // the default instance of its kind for new collections
	Connection                    arrView
	Waiting, History              []store.AcquisitionRequest
	Queued, Downloading, Attempts int
}
type arrPageView struct {
	Items    []arrListItem
	Selected *arrDetail
	Adding   bool
}

func queueCounts(s arr.Snapshot) (queued, downloading int) {
	for _, q := range s.Queue {
		if strings.EqualFold(q.Status, "downloading") {
			downloading++
		} else {
			queued++
		}
	}
	return
}
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func (a *App) arrPage(ctx context.Context, r *http.Request) (*arrPageView, error) {
	o, err := a.loadOverview(ctx)
	if err != nil {
		return nil, err
	}
	requests, err := a.store.Acquisitions(ctx)
	if err != nil {
		return nil, err
	}
	pending := map[string]int{}
	counts, err := a.store.DB.QueryContext(ctx, "SELECT connection_id,count(*) FROM acquisition_requests WHERE state='pending' GROUP BY connection_id")
	if err != nil {
		return nil, err
	}
	for counts.Next() {
		var id string
		var n int
		if err = counts.Scan(&id, &n); err != nil {
			counts.Close()
			return nil, err
		}
		pending[id] = n
	}
	err = counts.Err()
	counts.Close()
	if err != nil {
		return nil, err
	}
	v := &arrPageView{Adding: r.URL.Query().Get("new") == "1" || len(o.Arr) == 0}
	selected := r.URL.Query().Get("id")
	if selected == "" && r.URL.Query().Get("collection") == "" && len(o.Arr) > 0 && !v.Adding {
		selected = o.Arr[0].ID
	}
	if selected == "" && !v.Adding && len(o.Arr) > 0 {
		selected = o.Arr[0].ID
	}
	for _, c := range o.Arr {
		item := arrListItem{ID: c.ID, Name: c.Name, Kind: map[string]string{"sonarr": "TV", "radarr": "Movies"}[c.Kind], Host: hostOf(c.BaseURL), Active: c.ID == selected && !v.Adding}
		queued, downloading := queueCounts(c.Snapshot)
		switch {
		case c.ProfileID == 0 || c.RootID == 0:
			item.Status, item.Warn = "Choose a quality profile and root folder", true
		case pending[c.ID] > 0:
			item.Status, item.Warn = plural(pending[c.ID], "waiting for approval", "waiting for approval"), true
		case downloading > 0:
			item.Status = plural(downloading, "downloading", "downloading")
		case queued > 0:
			item.Status = plural(queued, "queued", "queued")
		default:
			item.Status = "Connected"
		}
		v.Items = append(v.Items, item)
	}
	if v.Adding || selected == "" {
		return v, nil
	}
	var conn store.ArrConnection
	found := false
	for _, c := range o.Arr {
		if c.ID == selected {
			conn, found = c, true
		}
	}
	if !found {
		return nil, sql.ErrNoRows
	}
	defaults, err := a.store.ArrDefaults(ctx)
	if err != nil {
		return nil, err
	}
	d := &arrDetail{Connection: arrPublic(conn), IsDefault: defaults[conn.Kind] == conn.ID}
	d.Queued, d.Downloading = queueCounts(conn.Snapshot)
	if d.Attempts, err = a.store.AttemptsSince(ctx, conn.ID, time.Now().Add(-24*time.Hour)); err != nil {
		return nil, err
	}
	if d.Waiting, err = a.store.PendingAcquisitions(ctx, conn.ID); err != nil {
		return nil, err
	}
	for _, req := range requests {
		if req.ConnectionID == conn.ID && req.State != "pending" {
			d.History = append(d.History, req)
		}
	}
	v.Selected = d
	return v, nil
}
func (a *App) acquisitionPage(w http.ResponseWriter, r *http.Request) {
	v, err := a.arrPage(r.Context(), r)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.fail(w, "acquisition settings failed")
		return
	}
	a.render(w, r, 200, page{Title: "Sonarr / Radarr", Mode: "acquisition", ArrPage: v})
}
func (a *App) approveAll(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.ArrConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		http.Error(w, "Confirm the whole-media request effect before approving.", 400)
		return
	}
	requests, err := a.store.PendingAcquisitions(r.Context(), c.ID)
	if err != nil {
		a.fail(w, "request history failed")
		return
	}
	for _, req := range requests {
		if req.ConnectionRevision != c.Revision {
			err = a.store.RetryAcquisition(r.Context(), req.ID, c.Revision)
		} else {
			err = a.store.ApproveAcquisition(r.Context(), req.ID)
		}
		if err != nil {
			continue
		}
		if _, err = a.store.QueueJob(r.Context(), "acquire", req.ID); err != nil {
			a.fail(w, "queue acquisition failed")
			return
		}
	}
	a.wakeWorker()
	http.Redirect(w, r, "/settings/acquisition?id="+c.ID, 303)
}
func (a *App) declineRequest(w http.ResponseWriter, r *http.Request) {
	request, err := a.store.Acquisition(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if err = a.store.DeclineAcquisition(r.Context(), request.ID); err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/acquisition?id="+request.ConnectionID, 303)
}
func (a *App) editArr(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.ArrConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
	if revision != c.Revision {
		a.collectionError(w, store.ErrConflict)
		return
	}
	base, err := plex.NormalizeBaseURL(r.PostForm.Get("url"), r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	key := r.PostForm.Get("api_key")
	if key == "" {
		if base != c.BaseURL {
			http.Error(w, "Supply the API key again when changing the address; the saved key is never sent to a different server.", 400)
			return
		}
		if key, err = a.vault.Open(c.KeyCipher, "arr:"+c.ID+":key"); err != nil {
			a.fail(w, "credential access failed")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := arr.New().Inspect(ctx, arr.Connection{Kind: c.Kind, URL: base, Key: key})
	if err != nil {
		http.Error(w, arr.ErrAPI.Error(), 400)
		return
	}
	c.Name, c.BaseURL, c.KeyCipher = strings.TrimSpace(r.PostForm.Get("name")), base, a.vault.Seal(key, "arr:"+c.ID+":key")
	if err = a.store.UpdateArrConnection(r.Context(), c, revision); err != nil {
		a.collectionError(w, err)
		return
	}
	if fresh, err := a.store.ArrConnection(r.Context(), c.ID); err == nil {
		a.store.UpdateArrSnapshot(r.Context(), fresh, snapshot)
	}
	http.Redirect(w, r, "/settings/acquisition?id="+c.ID, 303)
}

// busyStatus feeds the spinner next to Activity; the page polls it so it updates without a reload.
func (a *App) busyStatus(w http.ResponseWriter, r *http.Request) {
	busy, label, err := a.busy(r.Context())
	if err != nil {
		a.fail(w, "activity status failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"busy": busy, "label": label})
}
