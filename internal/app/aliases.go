package app

import (
	"context"
	"regexp"
	"strconv"
	"time"

	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// aliasInfo is what Radarr/Sonarr know about one title: its IDs in every provider, and its name.
type aliasInfo struct {
	IDs     map[string]string
	Title   string
	Missing bool // looked up and not found: the downloader does not know this ID
}

// aliasIndex finds that information from any one of a title's IDs. Keys are
// "kind|provider:id"; movies come from Radarr and series (kind "episode") from Sonarr.
type aliasIndex map[string]aliasInfo

type aliasCache struct {
	stamp int64
	value aliasIndex
}

// aliasStamp changes whenever an instance is added, removed or refreshed (a refresh re-reads its library).
func (a *App) aliasStamp(ctx context.Context) (int64, error) {
	var n, sum int64
	if err := a.store.DB.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(checked_at),0) FROM arr_connections").Scan(&n, &sum); err != nil {
		return 0, err
	}
	looked, err := a.store.AliasStamp(ctx)
	return sum*100 + n + looked*7, err
}

// aliases returns the ID aliases known from the connected Radarr/Sonarr libraries. They let a
// Plex item that only carries an IMDb ID be recognised as an entry listed by TMDB or TVDB ID, and
// they supply the title that flags a Plex item with no IDs at all. It is rebuilt only when an
// instance changed. The stamp is also what invalidates cached availability.
func (a *App) aliases(ctx context.Context) (aliasIndex, int64, error) {
	stamp, err := a.aliasStamp(ctx)
	if err != nil {
		return nil, 0, err
	}
	a.aliasMu.Lock()
	defer a.aliasMu.Unlock()
	if a.aliasCached != nil && a.aliasCached.stamp == stamp {
		return a.aliasCached.value, stamp, nil
	}
	conns, err := a.store.ArrConnections(ctx)
	if err != nil {
		return nil, 0, err
	}
	stored, err := a.store.Aliases(ctx)
	if err != nil {
		return nil, 0, err
	}
	idx := buildAliases(conns, stored)
	a.aliasCached = &aliasCache{stamp: stamp, value: idx}
	return idx, stamp, nil
}

func buildAliases(conns []store.ArrConnection, stored []store.StoredAlias) aliasIndex {
	idx := aliasIndex{}
	for _, c := range conns {
		kind := "movie"
		if c.Kind == "sonarr" {
			kind = "episode"
		}
		for _, m := range c.Snapshot.Media {
			ids := map[string]string{}
			if m.TMDB > 0 {
				ids["tmdb"] = strconv.Itoa(m.TMDB)
			}
			if kind == "episode" && m.TVDB > 0 {
				ids["tvdb"] = strconv.Itoa(m.TVDB)
			}
			if m.IMDB != "" {
				ids["imdb"] = m.IMDB
			}
			if len(ids) < 2 {
				continue // nothing to translate
			}
			info := aliasInfo{IDs: ids, Title: m.Title}
			for provider, id := range ids {
				idx[kind+"|"+provider+":"+id] = info
			}
		}
	}
	// Titles that are not in a library yet were looked up in Radarr/Sonarr and remembered.
	for _, l := range stored {
		if !l.Found {
			if time.Since(time.Unix(l.FetchedAt, 0)) < aliasNegativeTTL {
				if key := l.Kind + "|" + l.Provider + ":" + l.ID; idx[key].IDs == nil {
					idx[key] = aliasInfo{Missing: true}
				}
			}
			continue
		}
		ids := map[string]string{}
		for provider, v := range map[string]string{"imdb": l.IMDB, "tmdb": l.TMDB, "tvdb": l.TVDB} {
			if v != "" {
				ids[provider] = v
			}
		}
		if len(ids) < 2 {
			continue
		}
		info := aliasInfo{IDs: ids, Title: l.Title}
		for provider, id := range ids {
			key := l.Kind + "|" + provider + ":" + id
			if _, ok := idx[key]; !ok {
				idx[key] = info
			}
		}
	}
	return idx
}

var episodeTitle = regexp.MustCompile(`^(.*?) - S\d+E\d+`)

// identityFor is how an entry is matched: its own ID, the same title's IDs in other providers
// (from Radarr/Sonarr), and its title as a label for flagging ID-less Plex items.
func identityFor(e store.Entry, al aliasIndex) matching.Identity {
	id := matching.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}
	info := al[e.Kind+"|"+e.Provider+":"+e.ExternalID]
	if len(info.IDs) > 0 {
		id.Aliases = map[string]string{}
		for provider, v := range info.IDs {
			if provider != e.Provider {
				id.Aliases[provider] = v
			}
		}
	}
	own := e.Title
	if e.Kind == "episode" { // an episode entry is titled "Series - S01E02: Name"
		own = ""
		if m := episodeTitle.FindStringSubmatch(e.Title); m != nil {
			own = m[1]
		}
	}
	for _, t := range []string{own, info.Title} {
		if t != "" {
			id.Titles = append(id.Titles, t)
		}
	}
	return id
}

// idState says whether Ordarr has the ID a downloader needs for an entry.
type idState int

const (
	idNone     idState = iota // this downloader cannot handle the entry (wrong kind of title)
	idOK                      // the ID is known
	idPending                 // listed under another provider; its ID has not been looked up yet
	idNotFound                // looked up: the downloader does not know this title
)

// requestID is the ID a downloader needs for an entry: the TMDB ID for Radarr, the TVDB ID for
// Sonarr. An entry listed by another provider (many lists use IMDb IDs for movies) is translated
// with the aliases Radarr/Sonarr supplied. Before this, such entries had no downloader at all.
func requestID(c store.ArrConnection, e store.Entry, al aliasIndex) (int, idState) {
	native, kind := "tvdb", "episode"
	if c.Kind == "radarr" {
		native, kind = "tmdb", "movie"
	}
	if e.Kind != kind {
		return 0, idNone
	}
	parse := func(raw string) (int, bool) {
		id, err := strconv.Atoi(raw)
		return id, err == nil && id > 0 && id <= 2147483647
	}
	if e.Provider == native {
		if id, ok := parse(e.ExternalID); ok {
			return id, idOK
		}
		return 0, idNone
	}
	switch e.Provider {
	case "imdb", "tmdb", "tvdb":
	default:
		return 0, idNone
	}
	info := al[e.Kind+"|"+e.Provider+":"+e.ExternalID]
	if id, ok := parse(info.IDs[native]); ok {
		return id, idOK
	}
	if info.Missing {
		return 0, idNotFound
	}
	return 0, idPending
}
