package store

import (
	"context"
	"time"
)

// aliasSchema is created at startup rather than in a numbered migration, so it does not depend on
// the order in which parallel changes that add migrations are merged.
const aliasSchema = `CREATE TABLE IF NOT EXISTS id_aliases (
 kind TEXT NOT NULL,
 provider TEXT NOT NULL,
 id TEXT NOT NULL,
 imdb TEXT NOT NULL DEFAULT '',
 tmdb TEXT NOT NULL DEFAULT '',
 tvdb TEXT NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '',
 found INTEGER NOT NULL,
 fetched_at INTEGER NOT NULL,
 PRIMARY KEY(kind,provider,id)
)`

// StoredAlias is what a Radarr/Sonarr lookup said about one title (or that it found nothing).
type StoredAlias struct {
	Kind, Provider, ID string // kind is "movie" or "episode" (a series); provider/id is the entry's own ID
	IMDB, TMDB, TVDB   string
	Title              string
	Found              bool
	FetchedAt          int64
}

func (s *Store) SaveAlias(ctx context.Context, a StoredAlias) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO id_aliases(kind,provider,id,imdb,tmdb,tvdb,title,found,fetched_at) VALUES(?,?,?,?,?,?,?,?,?)
 ON CONFLICT(kind,provider,id) DO UPDATE SET imdb=excluded.imdb,tmdb=excluded.tmdb,tvdb=excluded.tvdb,title=excluded.title,found=excluded.found,fetched_at=excluded.fetched_at`,
		a.Kind, a.Provider, a.ID, a.IMDB, a.TMDB, a.TVDB, a.Title, a.Found, time.Now().Unix())
	return err
}

func (s *Store) Aliases(ctx context.Context) ([]StoredAlias, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT kind,provider,id,imdb,tmdb,tvdb,title,found,fetched_at FROM id_aliases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredAlias
	for rows.Next() {
		var a StoredAlias
		if err = rows.Scan(&a.Kind, &a.Provider, &a.ID, &a.IMDB, &a.TMDB, &a.TVDB, &a.Title, &a.Found, &a.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AliasStamp changes whenever a lookup result is added or refreshed.
func (s *Store) AliasStamp(ctx context.Context) (int64, error) {
	var n, newest int64
	err := s.DB.QueryRowContext(ctx, "SELECT count(*), coalesce(max(fetched_at),0) FROM id_aliases").Scan(&n, &newest)
	return newest*1000 + n, err
}
