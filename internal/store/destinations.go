package store

import (
	"context"
	"encoding/json"
	"time"
)

type Destination struct {
	ID, CollectionID, ConnectionID, Title, RemoteID, Policy string
	UserID                                                  int64
	IntervalMinutes, Revision, LastCollectionRevision       int
	NextRun, LastSuccess                                    int64
	LastSequence                                            []string
}
type SyncRun struct {
	ID                                   int64
	DestinationID, JobID, State, Message string
	Proposed                             []string
	CreatedAt                            int64
}

func scanDestination(row interface{ Scan(...any) error }) (Destination, error) {
	var d Destination
	var data []byte
	err := row.Scan(&d.ID, &d.CollectionID, &d.ConnectionID, &d.Title, &d.RemoteID, &d.UserID, &d.Policy, &d.IntervalMinutes, &d.NextRun, &d.Revision, &data, &d.LastCollectionRevision, &d.LastSuccess)
	if err == nil {
		err = json.Unmarshal(data, &d.LastSequence)
	}
	return d, err
}
func (s *Store) Destination(ctx context.Context, id string) (Destination, error) {
	return scanDestination(s.DB.QueryRowContext(ctx, "SELECT * FROM destinations WHERE id=?", id))
}
func (s *Store) Destinations(ctx context.Context, collection string) ([]Destination, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM destinations WHERE (?='' OR collection_id=?) ORDER BY id", collection, collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Destination
	for rows.Next() {
		d, err := scanDestination(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Store) CreateDestination(ctx context.Context, collection, connection, policy string, interval int) (string, error) {
	if policy != "authoritative" && policy != "approval" && policy != "paused" || interval < 0 || interval > 10080 || interval > 0 && interval < 5 {
		return "", invalid("Use a supported sync policy and interval (0, or 5–10,080 minutes).")
	}
	c, err := s.Collection(ctx, collection)
	if err != nil {
		return "", err
	}
	id := newID()
	title := c.Title
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	title += " [Ordarr:" + id + "]"
	_, err = s.DB.ExecContext(ctx, "INSERT INTO destinations(id,collection_id,connection_id,title,policy,interval_minutes) VALUES(?,?,?,?,?,?)", id, collection, connection, title, policy, interval)
	return id, err
}
func (s *Store) BindPlaylist(ctx context.Context, id, remote string, userID int64, revision int) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE destinations SET remote_id=?,user_id=? WHERE id=? AND revision=? AND (user_id=0 OR user_id=?)", remote, userID, id, revision, userID)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
func (s *Store) RecordSync(ctx context.Context, d Destination, job, state, message string, proposed []string) error {
	data, err := json.Marshal(proposed)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO sync_runs(destination_id,job_id,state,proposed,message,created_at) VALUES(?,?,?,?,?,?)", d.ID, job, state, data, message, time.Now().Unix())
	return err
}
func (s *Store) CompleteSync(ctx context.Context, d Destination, collectionRevision int, sequence []string) error {
	data, err := json.Marshal(sequence)
	if err != nil {
		return err
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE destinations SET last_sequence=?,last_collection_revision=?,last_success=? WHERE id=? AND revision=? AND EXISTS(SELECT 1 FROM collections WHERE id=? AND revision=?)", data, collectionRevision, time.Now().Unix(), d.ID, d.Revision, d.CollectionID, collectionRevision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
func (s *Store) SyncRuns(ctx context.Context, collection string) ([]SyncRun, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT r.id,r.destination_id,r.job_id,r.state,r.proposed,r.message,r.created_at FROM sync_runs r JOIN destinations d ON d.id=r.destination_id WHERE d.collection_id=? ORDER BY r.id DESC LIMIT 50", collection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []SyncRun
	for rows.Next() {
		var r SyncRun
		var data []byte
		if err = rows.Scan(&r.ID, &r.DestinationID, &r.JobID, &r.State, &data, &r.Message, &r.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &r.Proposed); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}
func (s *Store) DueDestinations(ctx context.Context) ([]Destination, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM destinations WHERE interval_minutes>0 AND policy<>'paused' AND next_run<=?", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Destination
	for rows.Next() {
		d, err := scanDestination(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Store) AdvanceDestination(ctx context.Context, d Destination) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE destinations SET next_run=? WHERE id=?", time.Now().Add(time.Duration(d.IntervalMinutes)*time.Minute).Unix(), d.ID)
	return err
}
func (s *Store) DestinationPolicy(ctx context.Context, id, policy string, interval, revision int) error {
	if policy != "authoritative" && policy != "approval" && policy != "paused" || interval < 0 || interval > 10080 || interval > 0 && interval < 5 {
		return invalid("Invalid policy or interval")
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE destinations SET policy=?,interval_minutes=?,revision=revision+1 WHERE id=? AND revision=?", policy, interval, id, revision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
func (s *Store) RecreateDestination(ctx context.Context, id string, revision int) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE destinations SET remote_id='',revision=revision+1 WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}

func (s *Store) SyncRun(ctx context.Context, id int64) (SyncRun, error) {
	var r SyncRun
	var data []byte
	err := s.DB.QueryRowContext(ctx, "SELECT id,destination_id,job_id,state,proposed,message,created_at FROM sync_runs WHERE id=?", id).Scan(&r.ID, &r.DestinationID, &r.JobID, &r.State, &data, &r.Message, &r.CreatedAt)
	if err == nil {
		err = json.Unmarshal(data, &r.Proposed)
	}
	return r, err
}
func (s *Store) ApproveSyncRun(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE sync_runs SET state='approved' WHERE id=? AND state IN ('pending','approved')", id)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
