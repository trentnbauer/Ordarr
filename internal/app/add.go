package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/community"
	"github.com/trentnbauer/Ordarr/internal/portable"
	"github.com/trentnbauer/Ordarr/internal/store"
	"github.com/trentnbauer/Ordarr/internal/subscription"
)

type communityItem struct {
	community.Order
	CollectionID string // set when this order is already followed
	Active       bool
}
type addView struct {
	Source, Error                 string
	CommunityEnabled              bool
	RepositoryURL, RepositoryName string
	Orders                        []communityItem
	Total                         int
	Query, Filter, Sort           string
	Picked                        *communityItem
	URL                           string
	Found                         *community.Order
	Document, Title, Description  string
	Servers, ReadOnly             []plexView
	Jellyfin                      []string
	ServerID, Playlist, Policy    string
	Defaults                      store.Defaults
}

var addSources = map[string]bool{"community": true, "paste": true, "url": true, "upload": true, "empty": true}

// fetchURL fetches a public collection file; tests replace it.
var fetchURL = subscription.Fetch

func (a *App) addPage(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "community"
		if a.community == nil {
			source = "url"
		}
	}
	a.renderAdd(w, r, 200, source, "", nil)
}

func (a *App) renderAdd(w http.ResponseWriter, r *http.Request, status int, source, message string, change func(*addView)) {
	ctx := r.Context()
	if !addSources[source] {
		http.Error(w, "Unknown collection source", 400)
		return
	}
	o, err := a.loadOverview(ctx)
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	values, err := a.availabilityAll(ctx, o)
	if err != nil {
		a.fail(w, "availability failed")
		return
	}
	p := page{Title: "Add a collection", Mode: "add"}
	if p.Nav, err = a.navFor(ctx, "collections", o, values); err != nil {
		a.fail(w, "navigation failed")
		return
	}
	// The list behind the modal ignores the modal's own search parameters.
	behind := r.Clone(ctx)
	behind.URL.RawQuery = ""
	if p.List, err = a.collectionList(ctx, behind, o, values, ""); err != nil {
		a.collectionError(w, err)
		return
	}
	q := r.URL.Query()
	v := &addView{Source: source, Error: message, CommunityEnabled: a.community != nil, Query: q.Get("q"), Filter: q.Get("filter"), Sort: q.Get("sort"), URL: strings.TrimSpace(q.Get("url")), Defaults: o.Defaults, Policy: o.Defaults.SourcePolicy}
	for _, s := range o.Plex {
		if s.AccountLinked {
			v.Servers = append(v.Servers, s)
		} else {
			v.ReadOnly = append(v.ReadOnly, s)
		}
	}
	for _, j := range o.Jellyfin {
		v.Jellyfin = append(v.Jellyfin, j.Name)
	}
	v.ServerID = o.Defaults.ConnectionID
	if !isServer(v.Servers, v.ServerID) && len(v.Servers) > 0 {
		v.ServerID = v.Servers[0].ID
	}
	if r.Method == "POST" {
		v.ServerID = r.PostForm.Get("server")
		v.Playlist = r.PostForm.Get("playlist")
		v.Policy = r.PostForm.Get("policy")
		v.URL = strings.TrimSpace(r.PostForm.Get("url"))
		v.Document = r.PostForm.Get("document")
		if p := r.PostForm.Get("path"); p != "" {
			v.Query = q.Get("q")
			q.Set("pick", p)
		}
	}
	if v.Sort == "" {
		v.Sort = "title"
	}
	if source == "community" && a.community != nil {
		v.RepositoryURL, v.RepositoryName = a.community.RepositoryURL(), a.community.Repository
		if err := a.communityOrders(ctx, v, q.Get("pick")); err != nil && v.Error == "" {
			v.Error = err.Error()
		}
		if v.Picked != nil && v.Playlist == "" {
			v.Playlist = v.Picked.Title
		}
	}
	if source == "url" && v.URL != "" && r.Method == "GET" {
		found, err := a.previewURL(ctx, v.URL)
		if err != nil {
			v.Error = err.Error()
		} else {
			v.Found = &found
			if v.Playlist == "" {
				v.Playlist = found.Title
			}
		}
	}
	if change != nil {
		change(v)
	}
	p.Add = v
	a.render(w, r, status, p)
}

func isServer(list []plexView, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

func (a *App) communityOrders(ctx context.Context, v *addView, pick string) error {
	orders, err := a.community.Orders(ctx)
	if err != nil {
		return err
	}
	followed, err := a.store.SubscriptionURLs(ctx)
	if err != nil {
		return err
	}
	v.Total = len(orders)
	query := strings.ToLower(strings.TrimSpace(v.Query))
	for _, o := range orders {
		switch v.Filter {
		case "mixed":
			if o.Movies == 0 || o.Episodes == 0 {
				continue
			}
		case "movies":
			if o.Episodes > 0 {
				continue
			}
		}
		if query != "" && !strings.Contains(strings.ToLower(o.Title+" "+o.Description+" "+o.Path), query) {
			continue
		}
		item := communityItem{Order: o, CollectionID: followed[o.URL], Active: o.Path == pick}
		v.Orders = append(v.Orders, item)
	}
	if v.Sort == "largest" {
		sort.SliceStable(v.Orders, func(i, j int) bool { return v.Orders[i].Entries > v.Orders[j].Entries })
	}
	if pick == "" && len(v.Orders) > 0 {
		v.Orders[0].Active = true
	}
	for i := range v.Orders {
		if v.Orders[i].Active {
			picked := v.Orders[i]
			v.Picked = &picked
		}
	}
	return nil
}

func (a *App) previewURL(ctx context.Context, raw string) (community.Order, error) {
	if err := subscription.ValidateURL(raw); err != nil {
		return community.Order{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res, err := fetchURL(ctx, raw, "", "")
	if err != nil {
		return community.Order{}, err
	}
	return community.Summarise(res.Data)
}

// addCollection is the single "create" action of the add-collection modal:
// it creates the collection, optionally follows its source, and when a server
// is chosen creates the playlist destination and queues its first sync.
func (a *App) addCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	source := r.PostForm.Get("source")
	if !addSources[source] || source == "empty" {
		http.Error(w, "Unknown collection source", 400)
		return
	}
	retry := func(status int, err error) {
		a.renderAdd(w, r, status, source, err.Error(), nil)
	}
	policy := r.PostForm.Get("policy")
	if policy != "approval" && policy != "automatic" && policy != "none" {
		policy = "none"
	}
	defaults, err := a.store.Defaults(ctx)
	if err != nil {
		a.fail(w, "defaults failed")
		return
	}
	server := r.PostForm.Get("server")
	if !store.ValidPlaylistName(r.PostForm.Get("playlist")) {
		retry(400, errors.New("Use a playlist name of 1–200 characters, or leave it blank to use the collection title."))
		return
	}
	if server != "" {
		conn, err := a.store.PlexConnection(ctx, server)
		if err != nil || conn.AccountID != 1 {
			retry(400, errors.New("Choose a Plex server connected through your Plex sign-in, or don't publish yet."))
			return
		}
	}
	var data []byte
	var raw, etag, modified string
	switch source {
	case "community", "url":
		raw = strings.TrimSpace(r.PostForm.Get("url"))
		if source == "community" {
			if a.community == nil {
				http.Error(w, "The community catalogue is disabled", 400)
				return
			}
			order, err := a.community.Order(ctx, r.PostForm.Get("path"))
			if err != nil {
				retry(502, err)
				return
			}
			raw = order.URL
		}
		if err = subscription.ValidateURL(raw); err != nil {
			retry(400, err)
			return
		}
		fetchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		res, err := fetchURL(fetchCtx, raw, "", "")
		cancel()
		if err != nil {
			retry(502, err)
			return
		}
		data, etag, modified = res.Data, res.ETag, res.Modified
	case "paste":
		data = []byte(r.PostForm.Get("document"))
	case "upload":
		file, _, err := r.FormFile("file")
		if err != nil {
			retry(400, errors.New("Choose a YAML or JSON collection file to upload."))
			return
		}
		data, err = io.ReadAll(io.LimitReader(file, portable.MaxBytes+1))
		file.Close()
		if err != nil {
			retry(400, errors.New("The file could not be read."))
			return
		}
		policy = "none"
	}
	if source == "paste" {
		policy = "none"
	}
	d, err := portable.Decode(data)
	if err != nil {
		retry(400, err)
		return
	}
	c, err := d.Collection()
	if err != nil {
		retry(400, err)
		return
	}
	var id string
	if policy == "none" {
		id, err = a.store.ImportCollection(ctx, c)
	} else {
		canonical, e := portable.Encode(d, "yaml")
		if e != nil {
			retry(400, e)
			return
		}
		var sub store.Subscription
		sub, err = a.store.CreateSubscribedCollection(ctx, raw, policy, canonical, c, etag, modified)
		id = sub.CollectionID
	}
	if errors.Is(err, store.ErrInvalid) {
		retry(400, err)
		return
	}
	if err != nil {
		a.collectionError(w, err)
		return
	}
	if server != "" {
		destination, err := a.store.CreateDestinationNamed(ctx, id, server, "authoritative", defaults.SyncInterval, r.PostForm.Get("playlist"))
		if err != nil {
			// The collection exists; send the administrator to finish publishing,
			// and say why the playlist wasn't created.
			a.logger.Error("playlist destination for a new collection failed", "error", err)
			http.Redirect(w, r, "/collections/"+id+"?tab=publishing&publish=failed", 303)
			return
		}
		if _, err = a.store.QueueJob(ctx, "sync", destination); err != nil {
			a.fail(w, "queue sync failed")
			return
		}
	} else if a.queueCheck(ctx, id) != nil {
		a.fail(w, "queue scan failed")
		return
	}
	a.wakeWorker()
	http.Redirect(w, r, "/collections/"+id+"?created=1", 303)
}

// queueCheck scans the collection's primary server when it has no complete
// catalogue yet, so the new collection's availability appears.
func (a *App) queueCheck(ctx context.Context, collection string) error {
	o, err := a.loadOverview(ctx)
	if err != nil {
		return err
	}
	s, ok := o.primary(collection)
	if !ok {
		return nil
	}
	if _, _, err = a.store.CatalogueGeneration(ctx, s.ID); err == nil {
		return nil
	}
	_, err = a.store.QueueJob(ctx, "scan", s.ID)
	return err
}
