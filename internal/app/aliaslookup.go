package app

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// How the ID lookups are paced, so Radarr/Sonarr (and the services behind them) are not hammered.
const (
	aliasBatch       = 60                 // lookups per run
	aliasNegativeTTL = 7 * 24 * time.Hour // how long "not found" is remembered
	aliasInterval    = 30 * time.Minute   // between runs while titles are still unresolved
)

var aliasPause = 250 * time.Millisecond // between lookups; a variable so tests can shorten it

var imdbRef = regexp.MustCompile(`^tt[0-9]{7,10}$`)

type aliasWant struct {
	kind, provider, ext string
	title               string
}

// lookupKinds says which downloader looks an entry up and by which of its IDs: movies go to Radarr
// by TMDB or IMDb ID, series to Sonarr by TVDB or IMDb ID.
func lookupKind(e store.Entry) (arrKind string, ok bool) {
	switch {
	case e.Kind == "movie" && (e.Provider == "tmdb" && numericID(e.ExternalID) || e.Provider == "imdb" && imdbRef.MatchString(e.ExternalID)):
		return "radarr", true
	case e.Kind == "episode" && (e.Provider == "tvdb" && numericID(e.ExternalID) || e.Provider == "imdb" && imdbRef.MatchString(e.ExternalID)):
		return "sonarr", true
	}
	return "", false
}

func numericID(s string) bool {
	id, err := strconv.Atoi(s)
	return err == nil && id > 0 && id <= 2147483647
}

// unresolvedAliases lists titles in collections whose other IDs are not known yet: not in a
// connected Radarr/Sonarr library, not looked up before (or "not found" long enough ago to try again).
func (a *App) unresolvedAliases(ctx context.Context, al aliasIndex) ([]aliasWant, error) {
	refs, err := a.store.CollectionRefs(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []aliasWant
	for _, ref := range refs {
		c, err := a.store.Collection(ctx, ref.ID)
		if err != nil {
			continue
		}
		for _, e := range c.Entries {
			if _, ok := lookupKind(e); !ok {
				continue
			}
			key := e.Kind + "|" + e.Provider + ":" + e.ExternalID
			if _, known := al[key]; known || seen[key] { // known includes "looked up, not found" while it is fresh
				continue
			}
			seen[key] = true
			out = append(out, aliasWant{kind: e.Kind, provider: e.Provider, ext: e.ExternalID, title: e.Title})
		}
	}
	return out, nil
}

// scheduleAliases queues a lookup run when there are unresolved titles and a downloader to ask.
func (a *App) scheduleAliases(ctx context.Context) {
	if next, _ := strconv.ParseInt(a.store.Pref(ctx, "aliases_next_run"), 10, 64); next > time.Now().Unix() {
		return
	}
	conns, err := a.store.ArrConnections(ctx)
	if err != nil || len(conns) == 0 {
		return
	}
	al, _, err := a.aliases(ctx)
	if err != nil {
		return
	}
	if want, err := a.unresolvedAliases(ctx, al); err != nil || len(want) == 0 {
		a.store.SetPref(ctx, "aliases_next_run", strconv.FormatInt(time.Now().Add(6*time.Hour).Unix(), 10))
		return
	}
	if _, err = a.store.QueueJob(ctx, "aliases", "all"); err == nil {
		a.store.SetPref(ctx, "aliases_next_run", strconv.FormatInt(time.Now().Add(aliasInterval).Unix(), 10))
	}
}

// lookupAlias asks one downloader about one title and remembers the answer, including "not found".
// Any other failure (the downloader is down) is returned and nothing is remembered.
func (a *App) lookupAlias(ctx context.Context, c store.ArrConnection, w aliasWant) error {
	conn, err := a.arrAccess(c)
	if err != nil {
		return err
	}
	var item arr.Media
	if w.provider == "imdb" {
		item, err = arr.New().LookupIMDB(ctx, conn, w.ext)
	} else {
		id, _ := strconv.Atoi(w.ext)
		item, err = arr.New().Lookup(ctx, conn, id)
	}
	switch {
	case errors.Is(err, arr.ErrIdentity):
		return a.store.SaveAlias(ctx, store.StoredAlias{Kind: w.kind, Provider: w.provider, ID: w.ext, Title: w.title})
	case err != nil:
		return err
	}
	stored := store.StoredAlias{Kind: w.kind, Provider: w.provider, ID: w.ext, IMDB: item.IMDB, Title: item.Title, Found: true}
	if item.TMDB > 0 {
		stored.TMDB = strconv.Itoa(item.TMDB)
	}
	if w.kind == "episode" && item.TVDB > 0 {
		stored.TVDB = strconv.Itoa(item.TVDB)
	}
	return a.store.SaveAlias(ctx, stored)
}

// resolveAliases is the "aliases" job. It asks Radarr/Sonarr (their lookup, which knows titles that
// are not in the library yet) for the other IDs of titles Ordarr cannot cross-match, and remembers
// the answers. This is also how entries listed by IMDb ID get the TMDB/TVDB ID needed to request
// them. A downloader that is down stops the run without remembering anything.
func (a *App) resolveAliases(ctx context.Context) error {
	conns, err := a.store.ArrConnections(ctx)
	if err != nil {
		return err
	}
	byKind := map[string]store.ArrConnection{} // first instance of each kind
	for _, c := range conns {
		if _, ok := byKind[c.Kind]; !ok {
			byKind[c.Kind] = c
		}
	}
	al, _, err := a.aliases(ctx)
	if err != nil {
		return err
	}
	want, err := a.unresolvedAliases(ctx, al)
	if err != nil {
		return err
	}
	done := 0
	for _, w := range want {
		kind := "radarr"
		if w.kind == "episode" {
			kind = "sonarr"
		}
		c, ok := byKind[kind]
		if !ok {
			continue
		}
		if done >= aliasBatch {
			break
		}
		if err = a.lookupAlias(ctx, c, w); err != nil {
			return err
		}
		done++
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(aliasPause):
		}
	}
	return nil
}

// ensureAlias makes sure a request for an entry listed by a non-native ID (IMDb) can be sent: the
// title is looked up now if it has not been. It returns the resulting state.
func (a *App) ensureAlias(ctx context.Context, c store.ArrConnection, e store.Entry) (int, idState, error) {
	al, _, err := a.aliases(ctx)
	if err != nil {
		return 0, idNone, err
	}
	id, state := requestID(c, e, al)
	if state != idPending {
		return id, state, nil
	}
	if err = a.lookupAlias(ctx, c, aliasWant{kind: e.Kind, provider: e.Provider, ext: e.ExternalID, title: e.Title}); err != nil {
		return 0, idPending, err
	}
	if al, _, err = a.aliases(ctx); err != nil {
		return 0, idNone, err
	}
	id, state = requestID(c, e, al)
	return id, state, nil
}

// rememberAlias stores an alias that came from a connected library, so the request that depends on
// it can be validated against the database later (a request is checked again right before it is sent).
func (a *App) rememberAlias(ctx context.Context, e store.Entry, al aliasIndex) {
	info := al[e.Kind+"|"+e.Provider+":"+e.ExternalID]
	if info.Missing || len(info.IDs) == 0 {
		return
	}
	stored := store.StoredAlias{Kind: e.Kind, Provider: e.Provider, ID: e.ExternalID, IMDB: info.IDs["imdb"], TMDB: info.IDs["tmdb"], TVDB: info.IDs["tvdb"], Title: info.Title, Found: true}
	a.store.SaveAlias(ctx, stored)
}
