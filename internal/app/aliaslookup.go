package app

import (
	"context"
	"errors"
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

type aliasWant struct {
	kind, provider string
	id             int
	title          string
}

// unresolvedAliases lists titles in collections whose other IDs are not known yet: not in a
// connected Radarr/Sonarr library, not looked up before (or "not found" long enough ago to try again).
func (a *App) unresolvedAliases(ctx context.Context, al aliasIndex) ([]aliasWant, error) {
	stored, err := a.store.Aliases(ctx)
	if err != nil {
		return nil, err
	}
	recentlyMissing := map[string]bool{}
	for _, l := range stored {
		if !l.Found && time.Since(time.Unix(l.FetchedAt, 0)) < aliasNegativeTTL {
			recentlyMissing[l.Kind+"|"+l.Provider+":"+l.ID] = true
		}
	}
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
			// Movies are looked up in Radarr by TMDB ID, series in Sonarr by TVDB ID.
			if !(e.Kind == "movie" && e.Provider == "tmdb" || e.Kind == "episode" && e.Provider == "tvdb") {
				continue
			}
			key := e.Kind + "|" + e.Provider + ":" + e.ExternalID
			id, err := strconv.Atoi(e.ExternalID)
			if _, known := al[key]; known || recentlyMissing[key] || seen[key] || err != nil || id < 1 {
				continue
			}
			seen[key] = true
			out = append(out, aliasWant{kind: e.Kind, provider: e.Provider, id: id, title: e.Title})
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

// resolveAliases is the "aliases" job. It asks Radarr/Sonarr (their lookup, which knows titles that
// are not in the library yet) for the other IDs of titles Ordarr cannot cross-match, and remembers
// the answers, including "not found". A downloader that is down stops the run without remembering
// anything, so the same titles are asked again next time.
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
		conn, err := a.arrAccess(c)
		if err != nil {
			return err
		}
		item, err := arr.New().Lookup(ctx, conn, w.id)
		switch {
		case errors.Is(err, arr.ErrIdentity):
			err = a.store.SaveAlias(ctx, store.StoredAlias{Kind: w.kind, Provider: w.provider, ID: strconv.Itoa(w.id), Title: w.title})
		case err != nil:
			return err
		default:
			stored := store.StoredAlias{Kind: w.kind, Provider: w.provider, ID: strconv.Itoa(w.id), IMDB: item.IMDB, Title: item.Title, Found: true}
			if item.TMDB > 0 {
				stored.TMDB = strconv.Itoa(item.TMDB)
			}
			if w.kind == "episode" && item.TVDB > 0 {
				stored.TVDB = strconv.Itoa(item.TVDB)
			}
			err = a.store.SaveAlias(ctx, stored)
		}
		if err != nil {
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
