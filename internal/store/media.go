package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

type LibraryPagePayload struct {
	Page    plex.MediaPage
	History []int
}
type LibraryPage struct {
	Payload                      LibraryPagePayload
	ConnectionRevision, Revision int
	FetchedAt, AttemptedAt       int64
	LastError                    string
}

func (s *Store) LibraryPage(ctx context.Context, connectionID, libraryKey string) (LibraryPage, error) {
	var page LibraryPage
	var data []byte
	err := s.DB.QueryRowContext(ctx, "SELECT payload,connection_revision,revision,fetched_at,attempted_at,last_error FROM plex_media_pages WHERE connection_id=? AND library_key=?", connectionID, libraryKey).Scan(&data, &page.ConnectionRevision, &page.Revision, &page.FetchedAt, &page.AttemptedAt, &page.LastError)
	if err != nil {
		return page, err
	}
	if json.Unmarshal(data, &page.Payload) != nil {
		return page, plex.ErrMediaPage
	}
	return page, nil
}
func (s *Store) SaveLibraryPage(ctx context.Context, connectionID, libraryKey string, connectionRevision, expectedRevision int, payload LibraryPagePayload) error {
	if len(payload.Page.Items) > plex.MediaPageSize || len(payload.History) > 1000 || payload.Page.Offset < 0 || payload.Page.Total < payload.Page.Offset+len(payload.Page.Items) || payload.Page.Total > plex.MaxMediaTotal {
		return invalid("Invalid library page.")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(data) > 256<<10 {
		return invalid("Library page exceeded the local cache limit.")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM plex_connections WHERE id=?", connectionID).Scan(&revision); err != nil {
		return err
	}
	if revision != connectionRevision {
		return ErrConflict
	}
	var kind string
	if err = tx.QueryRowContext(ctx, "SELECT kind FROM plex_libraries WHERE connection_id=? AND library_key=?", connectionID, libraryKey).Scan(&kind); err != nil {
		if err == sql.ErrNoRows {
			return ErrConflict
		}
		return err
	}
	if (kind == "movie" && payload.Page.Kind != "movie") || (kind == "show" && payload.Page.Kind != "episode") || (kind != "movie" && kind != "show") {
		return invalid("Wrong library media type.")
	}
	for _, offset := range payload.History {
		if offset < 0 || offset > plex.MaxMediaTotal {
			return invalid("Invalid page history.")
		}
	}

	now := time.Now().Unix()
	var result sql.Result
	if expectedRevision == 0 {
		result, err = tx.ExecContext(ctx, "INSERT INTO plex_media_pages(connection_id,library_key,payload,connection_revision,revision,fetched_at,attempted_at) SELECT ?,?,?,?,1,?,? WHERE NOT EXISTS(SELECT 1 FROM plex_media_pages WHERE connection_id=? AND library_key=?)", connectionID, libraryKey, data, connectionRevision, now, now, connectionID, libraryKey)
	} else {
		result, err = tx.ExecContext(ctx, "UPDATE plex_media_pages SET payload=?,connection_revision=?,revision=revision+1,fetched_at=?,attempted_at=?,last_error='' WHERE connection_id=? AND library_key=? AND revision=?", data, connectionRevision, now, now, connectionID, libraryKey, expectedRevision)
	}
	if err != nil {
		return err
	}
	if err = plexChanged(result); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) LibraryPageFailed(ctx context.Context, connectionID, libraryKey string, expectedRevision int, message string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE plex_media_pages SET last_error=?,attempted_at=?,revision=revision+1 WHERE connection_id=? AND library_key=? AND revision=?", message, time.Now().Unix(), connectionID, libraryKey, expectedRevision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
