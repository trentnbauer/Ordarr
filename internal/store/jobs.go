package store

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"time"
)

type Job struct {
	ID, Kind, Target, State, Message string
	Progress                         int
	CreatedAt, FinishedAt            int64
}

func (s *Store) QueueJob(ctx context.Context, kind, target string) (string, error) {
	id := newID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO jobs(id,kind,target,state,created_at) VALUES(?,?,?,'queued',?)", id, kind, target, time.Now().Unix())
	if err != nil {
		var existing string
		if e := s.DB.QueryRowContext(ctx, "SELECT id FROM jobs WHERE kind=? AND target=? AND state IN ('queued','running')", kind, target).Scan(&existing); e == nil {
			return existing, nil
		}
		return "", err
	}
	return id, nil
}
func (s *Store) ClaimJob(ctx context.Context) (Job, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	var j Job
	err = tx.QueryRowContext(ctx, "SELECT id,kind,target,state,progress,message,created_at,finished_at FROM jobs WHERE state='queued' ORDER BY created_at,id LIMIT 1").Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.Progress, &j.Message, &j.CreatedAt, &j.FinishedAt)
	if err != nil {
		return j, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE jobs SET state='running',message='' WHERE id=?", j.ID); err != nil {
		return j, err
	}
	j.State = "running"
	return j, tx.Commit()
}
func (s *Store) Job(ctx context.Context, id string) (Job, error) {
	var j Job
	err := s.DB.QueryRowContext(ctx, "SELECT id,kind,target,state,progress,message,created_at,finished_at FROM jobs WHERE id=?", id).Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.Progress, &j.Message, &j.CreatedAt, &j.FinishedAt)
	return j, err
}
func (s *Store) RecentJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,kind,target,state,progress,message,created_at,finished_at FROM jobs ORDER BY created_at DESC,id DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.Kind, &j.Target, &j.State, &j.Progress, &j.Message, &j.CreatedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *Store) JobProgress(ctx context.Context, id string, n int) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET progress=? WHERE id=?", n, id)
	return err
}
func (s *Store) FinishJob(ctx context.Context, id, state, message string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET state=?,message=?,finished_at=? WHERE id=?", state, message, time.Now().Unix(), id)
	return err
}
func (s *Store) RecoverJobs(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE jobs SET state='queued' WHERE state='running'")
	return err
}
func (s *Store) StageMedia(ctx context.Context, connection, generation string, item plex.IdentifiedMedia) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO plex_catalogue_items VALUES(?,?,?,?)", connection, generation, item.RatingKey, data)
	return err
}
func (s *Store) ClearStage(ctx context.Context, connection, generation string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM plex_catalogue_items WHERE connection_id=? AND generation=?", connection, generation)
	return err
}
func (s *Store) CommitCatalogue(ctx context.Context, connection, generation string, revision int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM plex_connections WHERE id=?", connection).Scan(&current); err != nil {
		return err
	}
	if current != revision {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO plex_catalogues VALUES(?,?,?,?,'') ON CONFLICT(connection_id) DO UPDATE SET connection_revision=excluded.connection_revision,generation=excluded.generation,scanned_at=excluded.scanned_at,last_error=''", connection, revision, generation, time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_catalogue_items WHERE connection_id=? AND generation<>?", connection, generation); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Catalogue(ctx context.Context, connection string) ([]plex.IdentifiedMedia, int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var generation string
	var scanned int64
	var revision, current int
	err = tx.QueryRowContext(ctx, "SELECT x.generation,x.scanned_at,x.connection_revision,c.revision FROM plex_catalogues x JOIN plex_connections c ON c.id=x.connection_id WHERE c.id=?", connection).Scan(&generation, &scanned, &revision, &current)
	if err != nil {
		return nil, 0, err
	}
	if revision != current {
		return nil, scanned, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM plex_catalogue_items WHERE connection_id=? AND generation=? ORDER BY rating_key", connection, generation)
	if err != nil {
		return nil, scanned, err
	}
	var result []plex.IdentifiedMedia
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return nil, scanned, err
		}
		var item plex.IdentifiedMedia
		if err = json.Unmarshal(data, &item); err != nil {
			rows.Close()
			return nil, scanned, err
		}
		result = append(result, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, scanned, err
	}
	return result, scanned, tx.Commit()
}
func EntryIdentity(e Entry) string {
	data, _ := json.Marshal([]any{e.Kind, e.Provider, e.ExternalID, e.Season, e.Episode})
	return string(data)
}
func (s *Store) ManualMatches(ctx context.Context, connection string, c Collection) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT entry_id,identity,rating_key FROM manual_matches WHERE connection_id=?", connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identity := map[string]string{}
	for _, e := range c.Entries {
		identity[e.ID] = EntryIdentity(e)
	}
	result := map[string]string{}
	for rows.Next() {
		var id, saved, key string
		if err = rows.Scan(&id, &saved, &key); err != nil {
			return nil, err
		}
		if identity[id] == saved {
			result[id] = key
		}
	}
	return result, rows.Err()
}
func (s *Store) SetManualMatch(ctx context.Context, connection string, e Entry, key string) error {
	if key == "" {
		_, err := s.DB.ExecContext(ctx, "DELETE FROM manual_matches WHERE connection_id=? AND entry_id=?", connection, e.ID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO manual_matches VALUES(?,?,?,?) ON CONFLICT(connection_id,entry_id) DO UPDATE SET identity=excluded.identity,rating_key=excluded.rating_key", connection, e.ID, EntryIdentity(e), key)
	return err
}
