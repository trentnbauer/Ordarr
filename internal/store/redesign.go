package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SkipSource discards one pending upstream version. The same content is not
// proposed again; a later, different upstream version is.
func (s *Store) SkipSource(ctx context.Context, sub Subscription) error {
	if len(sub.Pending) == 0 {
		return ErrConflict
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE subscriptions SET skipped_hash=?,pending=NULL,revision=revision+1,message='Skipped this upstream version; your order was left unchanged.' WHERE id=? AND revision=? AND pending=?", contentHash(sub.Pending), sub.ID, sub.Revision, sub.Pending)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// Unsubscribe stops following a source. The collection and its entries stay,
// and become an ordinary editable local collection.
func (s *Store) Unsubscribe(ctx context.Context, sub Subscription) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM subscriptions WHERE id=? AND revision=?", sub.ID, sub.Revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// CreateSubscribedCollection creates a collection that follows url, starting
// from an already fetched and validated document.
func (s *Store) CreateSubscribedCollection(ctx context.Context, url, policy string, data []byte, c Collection, etag, modified string) (Subscription, error) {
	var sub Subscription
	if policy != "automatic" && policy != "approval" {
		return sub, ErrInvalid
	}
	if err := ValidatePortableCollection(c); err != nil {
		return sub, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return sub, err
	}
	defer tx.Rollback()
	sub = Subscription{ID: newID(), CollectionID: newID(), URL: url, Policy: policy, Revision: 1}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "INSERT INTO collections(id,title,description,created_at,updated_at) VALUES(?,?,?,?,?)", sub.CollectionID, c.Title, c.Description, now, now); err != nil {
		return sub, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO subscriptions(id,collection_id,url,policy,last_valid,etag,modified,checked_at,next_run,message) VALUES(?,?,?,?,?,?,?,?,?,'Source applied.')", sub.ID, sub.CollectionID, url, policy, data, etag, modified, now, now+60*60); err != nil {
		return sub, err
	}
	if err = replaceSource(ctx, tx, sub, c); err != nil {
		return sub, err
	}
	return sub, tx.Commit()
}

// SubscriptionURLs maps each followed source URL to its collection.
func (s *Store) SubscriptionURLs(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT url,collection_id FROM subscriptions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var u, id string
		if err = rows.Scan(&u, &id); err != nil {
			return nil, err
		}
		result[u] = id
	}
	return result, rows.Err()
}

// ValidPlaylistName reports whether name is usable as a playlist name; empty
// means "use the collection title".
func ValidPlaylistName(name string) bool {
	return strings.TrimSpace(name) == "" || validTitle(name)
}

// CreateDestinationNamed is CreateDestination with an explicit playlist name.
// The Ordarr ownership marker is always appended.
func (s *Store) CreateDestinationNamed(ctx context.Context, collection, connection, policy string, interval int, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return s.CreateDestination(ctx, collection, connection, policy, interval)
	}
	if !validTitle(name) {
		return "", invalid("Use a playlist name of 1–200 characters.")
	}
	if policy != "authoritative" && policy != "approval" && policy != "paused" || interval < 0 || interval > 10080 || interval > 0 && interval < 5 {
		return "", invalid("Use a supported sync policy and interval (0, or 5–10,080 minutes).")
	}
	if _, err := s.Collection(ctx, collection); err != nil {
		return "", err
	}
	id := newID()
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO destinations(id,collection_id,connection_id,title,policy,interval_minutes) VALUES(?,?,?,?,?,?)", id, collection, connection, name+" [Ordarr:"+id+"]", policy, interval)
	return id, err
}

// AllDestinations lists every playlist destination.
func (s *Store) AllDestinations(ctx context.Context) ([]Destination, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,collection_id,connection_id,title,remote_id,user_id,policy,interval_minutes,next_run,revision,last_sequence,last_collection_revision,last_success FROM destinations ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Destination
	for rows.Next() {
		d, err := scanDestination(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

// LatestSyncRuns returns the newest sync run of each destination.
func (s *Store) LatestSyncRuns(ctx context.Context) (map[string]SyncRun, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,destination_id,job_id,state,message,created_at FROM sync_runs WHERE id IN (SELECT max(id) FROM sync_runs GROUP BY destination_id)")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]SyncRun{}
	for rows.Next() {
		var r SyncRun
		if err = rows.Scan(&r.ID, &r.DestinationID, &r.JobID, &r.State, &r.Message, &r.CreatedAt); err != nil {
			return nil, err
		}
		result[r.DestinationID] = r
	}
	return result, rows.Err()
}

// SourceState is a light projection of a subscription, without documents.
type SourceState struct {
	CollectionID, SubscriptionID, URL, Policy, Message string
	Pending                                            bool
	Failures                                           int
	CheckedAt                                          int64
}

func (s *Store) SourceStates(ctx context.Context) (map[string]SourceState, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT collection_id,id,url,policy,message,pending IS NOT NULL,failures,checked_at FROM subscriptions")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]SourceState{}
	for rows.Next() {
		var v SourceState
		if err = rows.Scan(&v.CollectionID, &v.SubscriptionID, &v.URL, &v.Policy, &v.Message, &v.Pending, &v.Failures, &v.CheckedAt); err != nil {
			return nil, err
		}
		result[v.CollectionID] = v
	}
	return result, rows.Err()
}

// CollectionTitles maps every collection ID to its title.
func (s *Store) CollectionTitles(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,title FROM collections")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, title string
		if err = rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		result[id] = title
	}
	return result, rows.Err()
}

// CollectionIDs lists all collection IDs, newest first.
func (s *Store) CollectionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM collections ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CatalogueGeneration identifies the current complete catalogue of a connection.
func (s *Store) CatalogueGeneration(ctx context.Context, connection string) (string, int64, error) {
	var generation string
	var scanned int64
	var revision, current int
	err := s.DB.QueryRowContext(ctx, "SELECT x.generation,x.scanned_at,x.connection_revision,c.revision FROM plex_catalogues x JOIN plex_connections c ON c.id=x.connection_id WHERE c.id=?", connection).Scan(&generation, &scanned, &revision, &current)
	if err == nil && revision != current {
		err = ErrConflict
	}
	return generation, scanned, err
}

// CatalogueSize counts the items of a connection's current catalogue by kind.
func (s *Store) CatalogueSize(ctx context.Context, connection string) (movies, episodes int, err error) {
	err = s.DB.QueryRowContext(ctx, "SELECT coalesce(sum(json_extract(i.payload,'$.type')='movie'),0),coalesce(sum(json_extract(i.payload,'$.type')='episode'),0) FROM plex_catalogue_items i JOIN plex_catalogues x ON x.connection_id=i.connection_id AND x.generation=i.generation WHERE i.connection_id=?", connection).Scan(&movies, &episodes)
	return
}

// DeclineAcquisition rejects a request waiting for approval. The declined row is
// kept so automatic proposals for the same media are not made again.
func (s *Store) DeclineAcquisition(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE acquisition_requests SET state='declined',message='Declined by the administrator.' WHERE id=? AND state='pending'", id)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// AttemptsSince counts request attempts reserved against a service's rolling limit.
func (s *Store) AttemptsSince(ctx context.Context, connection string, since time.Time) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM acquisition_attempts WHERE connection_id=? AND created_at>=?", connection, since.Unix()).Scan(&n)
	return n, err
}

// UpdateArrConnection changes a service's label, address and key after the
// caller has validated the new address against the same service kind.
func (s *Store) UpdateArrConnection(ctx context.Context, c ArrConnection, revision int) error {
	if !validTitle(c.Name) {
		return invalid("Use a name of 1–200 characters.")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE arr_connections SET name=?,base_url=?,key_cipher=?,revision=revision+1 WHERE id=? AND revision=?", c.Name, c.BaseURL, c.KeyCipher, c.ID, revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// UpdateJellyfin changes a Jellyfin connection's label, address and token after
// the caller verified the same user on the new address.
func (s *Store) UpdateJellyfin(ctx context.Context, c JellyfinConnection, revision int) error {
	if !validTitle(c.Name) {
		return invalid("Use a connection name of 1–200 characters.")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE jellyfin_connections SET name=?,base_url=?,token_cipher=?,allow_http=?,revision=revision+1 WHERE id=? AND revision=?", c.Name, c.BaseURL, c.TokenCipher, c.AllowHTTP, c.ID, revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// Defaults are the administrator's choices for newly added collections.
type Defaults struct {
	ConnectionID string // empty: do not publish automatically
	SourcePolicy string // approval or automatic
	SyncInterval int    // minutes; 0 means manual
	AutoDownload bool   // request missing items from Sonarr/Radarr unless a collection opts out
}

func (s *Store) Defaults(ctx context.Context) (Defaults, error) {
	d := Defaults{SourcePolicy: "automatic", SyncInterval: 60, AutoDownload: true}
	rows, err := s.DB.QueryContext(ctx, "SELECT key,value FROM preferences WHERE key IN ('default_connection','default_source_policy','default_sync_interval','default_auto_download')")
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err = rows.Scan(&k, &v); err != nil {
			return d, err
		}
		switch k {
		case "default_connection":
			d.ConnectionID = v
		case "default_source_policy":
			d.SourcePolicy = v
		case "default_auto_download":
			d.AutoDownload = v != "0"
		case "default_sync_interval":
			if n, e := strconv.Atoi(v); e == nil {
				d.SyncInterval = n
			}
		}
	}
	if err = rows.Err(); err != nil {
		return d, err
	}
	if d.ConnectionID != "" {
		var n int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM plex_connections WHERE id=?", d.ConnectionID).Scan(&n); err != nil {
			return d, err
		}
		if n == 0 {
			d.ConnectionID = ""
		}
	}
	return d, nil
}
func (s *Store) SaveDefaults(ctx context.Context, d Defaults) error {
	if d.SourcePolicy != "approval" && d.SourcePolicy != "automatic" || d.SyncInterval < 0 || d.SyncInterval > 10080 || d.SyncInterval > 0 && d.SyncInterval < 5 {
		return invalid("Use a supported update policy and sync interval.")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.ConnectionID != "" {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM plex_connections WHERE id=?", d.ConnectionID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return invalid("Choose a connected Plex server.")
		}
	}
	for k, v := range map[string]string{"default_connection": d.ConnectionID, "default_source_policy": d.SourcePolicy, "default_sync_interval": strconv.Itoa(d.SyncInterval), "default_auto_download": map[bool]string{true: "1", false: "0"}[d.AutoDownload]} {
		if _, err = tx.ExecContext(ctx, "INSERT INTO preferences VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ChangePassword replaces the administrator password and revokes every other
// session. The session identified by keep stays signed in.
func (s *Store) ChangePassword(ctx context.Context, hash []byte, keep string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE administrator SET password_hash=? WHERE id=1", hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_auth_attempts WHERE session_hash<>?", tokenHash(keep)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash<>?", tokenHash(keep)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteAllSessions signs the administrator out of every browser.
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_auth_attempts"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		return err
	}
	return tx.Commit()
}

// CollectionCount counts all collections.
func (s *Store) CollectionCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM collections").Scan(&n)
	return n, err
}

// CollectionJobs lists recent source checks and syncs for one collection.
func (s *Store) CollectionJobs(ctx context.Context, collection string) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,kind,target,state,progress,message,created_at,finished_at FROM jobs
 WHERE (kind='source' AND target IN (SELECT id FROM subscriptions WHERE collection_id=?))
 OR (kind='sync' AND target IN (SELECT id FROM destinations WHERE collection_id=?))
 OR (kind='publish' AND substr(target,1,instr(target,'@')-1) IN (SELECT id FROM destinations WHERE collection_id=?))
 ORDER BY created_at DESC,id DESC LIMIT 300`, collection, collection, collection)
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
	if errors.Is(rows.Err(), sql.ErrNoRows) {
		return nil, nil
	}
	return jobs, rows.Err()
}

// CollectionRef identifies one revision of a collection.
type CollectionRef struct {
	ID       string
	Revision int
}

// CollectionRefs lists every collection's current revision, newest first.
func (s *Store) CollectionRefs(ctx context.Context) ([]CollectionRef, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,revision FROM collections ORDER BY created_at DESC,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var refs []CollectionRef
	for rows.Next() {
		var r CollectionRef
		if err = rows.Scan(&r.ID, &r.Revision); err != nil {
			return nil, err
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}

// PendingAcquisitions lists every request waiting for approval on one service,
// oldest first, without the history listing's cap.
func (s *Store) PendingAcquisitions(ctx context.Context, connection string) ([]AcquisitionRequest, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM acquisition_requests WHERE connection_id=? AND state='pending' ORDER BY created_at,id", connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []AcquisitionRequest
	for rows.Next() {
		r, err := scanAcquisition(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// Pref reads a small free-form preference ("" when unset).
func (s *Store) Pref(ctx context.Context, key string) string {
	var v string
	s.DB.QueryRowContext(ctx, "SELECT value FROM preferences WHERE key=?", key).Scan(&v)
	return v
}

// SetPref stores a preference.
func (s *Store) SetPref(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO preferences VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}

// FailedRequestCount counts items a downloader could not find or add.
func (s *Store) FailedRequestCount(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM acquisition_requests WHERE state='failed'").Scan(&n)
	return n, err
}
