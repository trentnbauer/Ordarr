package store

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/arr"
	"strconv"
	"strings"
	"time"
)

type ArrConnection struct {
	ID, Kind, Name, BaseURL     string
	KeyCipher                   []byte `json:"-"`
	ProfileID, RootID, Revision int
	Snapshot                    arr.Snapshot
	CheckedAt                   int64
	RequireApproval             bool // legacy "ask me first"; new connections send immediately
	DailyLimit                  int
	Exclusions                  string
	NextRun                     int64
}

func scanArr(row interface{ Scan(...any) error }) (ArrConnection, error) {
	var c ArrConnection
	var data []byte
	var legacyAutomatic bool // superseded by Defaults.AutoDownload and the per-collection opt-out
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.BaseURL, &c.KeyCipher, &c.ProfileID, &c.RootID, &c.Revision, &data, &c.CheckedAt, &legacyAutomatic, &c.RequireApproval, &c.DailyLimit, &c.Exclusions, &c.NextRun)
	if err == nil {
		err = json.Unmarshal(data, &c.Snapshot)
	}
	return c, err
}
func (s *Store) ArrConnection(ctx context.Context, id string) (ArrConnection, error) {
	return scanArr(s.DB.QueryRowContext(ctx, "SELECT * FROM arr_connections WHERE id=?", id))
}
func (s *Store) ArrConnections(ctx context.Context) ([]ArrConnection, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM arr_connections ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ArrConnection
	for rows.Next() {
		c, err := scanArr(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) CreateArr(ctx context.Context, c ArrConnection) error {
	if !validTitle(c.Name) || c.Kind != "sonarr" && c.Kind != "radarr" {
		return ErrInvalid
	}
	data, err := json.Marshal(c.Snapshot)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO arr_connections(id,kind,name,base_url,key_cipher,profile_id,root_id,snapshot,checked_at,require_approval) VALUES(?,?,?,?,?,?,?,?,?,0)", c.ID, c.Kind, c.Name, c.BaseURL, c.KeyCipher, c.ProfileID, c.RootID, data, time.Now().Unix())
	if err != nil {
		return err
	}
	return s.adoptAsDefault(ctx, c)
}

// adoptAsDefault makes the first instance of a kind the default for new collections, and adds it
// to every existing collection that has no instance of that kind yet, so connecting Sonarr after
// collections exist still works without any further step.
func (s *Store) adoptAsDefault(ctx context.Context, c ArrConnection) error {
	defaults, err := s.ArrDefaults(ctx)
	if err != nil || defaults[c.Kind] != "" {
		return err
	}
	if err = s.SetArrDefault(ctx, c.Kind, c.ID); err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO collection_arr(collection_id,connection_id)
 SELECT col.id,? FROM collections col WHERE NOT EXISTS (SELECT 1 FROM collection_arr ca JOIN arr_connections a ON a.id=ca.connection_id WHERE ca.collection_id=col.id AND a.kind=?)`, c.ID, c.Kind)
	return err
}
func (s *Store) UpdateArrSnapshot(ctx context.Context, c ArrConnection, snapshot arr.Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE arr_connections SET snapshot=?,checked_at=? WHERE id=? AND revision=?", data, time.Now().Unix(), c.ID, c.Revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}
func Excluded(c ArrConnection, id int) bool {
	needle := strconv.Itoa(id)
	for _, part := range strings.FieldsFunc(c.Exclusions, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		if part == needle {
			return true
		}
	}
	return false
}
func (s *Store) ArrPolicy(ctx context.Context, id string, revision int, limit int, exclude string) error {
	if limit < 1 || limit > 100 || len(exclude) > 4000 {
		return ErrInvalid
	}
	for _, part := range strings.FieldsFunc(exclude, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		if n, err := strconv.Atoi(part); err != nil || n < 1 {
			return ErrInvalid
		}
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE arr_connections SET daily_limit=?,exclusions=?,revision=revision+1,next_run=0 WHERE id=? AND revision=?", limit, exclude, id, revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

type AcquisitionRequest struct {
	ID, ConnectionID       string
	ProviderID             int
	Title, State, Origin   string
	ConnectionRevision     int
	CreatedAt, AttemptedAt int64
	Message                string
}

func scanAcquisition(row interface{ Scan(...any) error }) (AcquisitionRequest, error) {
	var r AcquisitionRequest
	err := row.Scan(&r.ID, &r.ConnectionID, &r.ProviderID, &r.Title, &r.State, &r.Origin, &r.ConnectionRevision, &r.CreatedAt, &r.AttemptedAt, &r.Message)
	return r, err
}
func (s *Store) Acquisition(ctx context.Context, id string) (AcquisitionRequest, error) {
	return scanAcquisition(s.DB.QueryRowContext(ctx, "SELECT * FROM acquisition_requests WHERE id=?", id))
}
func (s *Store) Acquisitions(ctx context.Context) ([]AcquisitionRequest, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM acquisition_requests ORDER BY created_at DESC,id DESC LIMIT 100")
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

// CollectionRequests lists the download requests made for a collection's items, newest first: a
// request belongs to a collection when one of its entries is that title, listed by the ID the
// downloader uses (TMDB for movies, TVDB for series) or by another ID that was looked up to it.
func (s *Store) CollectionRequests(ctx context.Context, collection string) ([]AcquisitionRequest, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.* FROM acquisition_requests r JOIN arr_connections a ON a.id=r.connection_id
 WHERE EXISTS (SELECT 1 FROM entries e WHERE e.collection_id=? AND (
   (a.kind='radarr' AND e.kind='movie' AND ((e.provider='tmdb' AND e.external_id=CAST(r.provider_id AS TEXT))
     OR EXISTS (SELECT 1 FROM id_aliases x WHERE x.found=1 AND x.kind='movie' AND x.provider=e.provider AND x.id=e.external_id AND x.tmdb=CAST(r.provider_id AS TEXT))))
   OR (a.kind='sonarr' AND e.kind='episode' AND ((e.provider='tvdb' AND e.external_id=CAST(r.provider_id AS TEXT))
     OR EXISTS (SELECT 1 FROM id_aliases x WHERE x.found=1 AND x.kind='episode' AND x.provider=e.provider AND x.id=e.external_id AND x.tvdb=CAST(r.provider_id AS TEXT))))))
 ORDER BY COALESCE(NULLIF(r.attempted_at,0), r.created_at) DESC, r.id DESC LIMIT 100`, collection)
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

// AcquisitionIndex lists every request keyed "connection/providerID", for per-row status.
func (s *Store) AcquisitionIndex(ctx context.Context) (map[string]AcquisitionRequest, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM acquisition_requests")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AcquisitionRequest{}
	for rows.Next() {
		r, err := scanAcquisition(rows)
		if err != nil {
			return nil, err
		}
		out[r.ConnectionID+"/"+strconv.Itoa(r.ProviderID)] = r
	}
	return out, rows.Err()
}

// RecordFailedRequest remembers that a downloader could not find an item, so the UI can say so
// and the automatic job does not look it up again every hour. Request now retries it.
func (s *Store) RecordFailedRequest(ctx context.Context, c ArrConnection, id int, title, message string) error {
	if id < 1 {
		return ErrInvalid
	}
	if !validTitle(title) {
		title = "Unknown title"
	}
	_, err := s.DB.ExecContext(ctx, "INSERT INTO acquisition_requests(id,connection_id,provider_id,title,state,origin,connection_revision,created_at,message) VALUES(?,?,?,?,'failed','automatic',?,?,?) ON CONFLICT(connection_id,provider_id) DO NOTHING", newID(), c.ID, id, title, c.Revision, time.Now().Unix(), message)
	return err
}

func (s *Store) ProposeAcquisition(ctx context.Context, c ArrConnection, id int, title, origin string, approved bool) (string, error) {
	if id < 1 || Excluded(c, id) || !validTitle(title) {
		return "", ErrInvalid
	}
	state := "pending"
	if approved {
		state = "approved"
	}
	requestID := newID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO acquisition_requests(id,connection_id,provider_id,title,state,origin,connection_revision,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(connection_id,provider_id) DO NOTHING", requestID, c.ID, id, title, state, origin, c.Revision, time.Now().Unix())
	if err != nil {
		return "", err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT id FROM acquisition_requests WHERE connection_id=? AND provider_id=?", c.ID, id).Scan(&requestID)
	return requestID, err
}
func (s *Store) ApproveAcquisition(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE acquisition_requests SET state='approved' WHERE id=? AND state IN ('pending','approved')", id)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// Reserve the attempt and daily budget in one transaction before external writes.
func (s *Store) BeginAcquisition(ctx context.Context, r AcquisitionRequest, c ArrConnection) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, revision int
	var exclusions string
	if err = tx.QueryRowContext(ctx, "SELECT revision,exclusions FROM arr_connections WHERE id=?", c.ID).Scan(&revision, &exclusions); err != nil {
		return err
	}
	c.Exclusions = exclusions
	if revision != r.ConnectionRevision || Excluded(c, r.ProviderID) {
		return ErrConflict
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM acquisition_attempts WHERE connection_id=? AND created_at>=?", c.ID, time.Now().Add(-24*time.Hour).Unix()).Scan(&count); err != nil {
		return err
	}
	if count >= c.DailyLimit {
		return invalid("Rolling 24-hour request limit reached")
	}
	res, err := tx.ExecContext(ctx, "UPDATE acquisition_requests SET state='running',attempted_at=?,message='Attempt reserved; uncertain writes require reconciliation.' WHERE id=? AND state='approved'", time.Now().Unix(), r.ID)
	if err != nil {
		return err
	}
	if err = plexChanged(res); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO acquisition_attempts(request_id,connection_id,created_at) VALUES(?,?,?)", r.ID, c.ID, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FinishAcquisition(ctx context.Context, id, state, message string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE acquisition_requests SET state=?,message=? WHERE id=?", state, message, id)
	return err
}

// SetCollectionDownloads turns automatic downloading of a collection's missing items on or off.
func (s *Store) SetCollectionDownloads(ctx context.Context, collection string, enabled bool) error {
	if enabled {
		_, err := s.DB.ExecContext(ctx, "DELETE FROM collection_no_download WHERE collection_id=?", collection)
		return err
	}
	_, err := s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO collection_no_download SELECT id FROM collections WHERE id=?", collection)
	return err
}

// NoDownload is the set of collections that opted out of automatic downloads.
func (s *Store) NoDownload(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT collection_id FROM collection_no_download")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
func (s *Store) ConfigureArrChoices(ctx context.Context, id string, profile, root int) error {
	if profile < 1 || root < 1 {
		return ErrInvalid
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE arr_connections SET profile_id=?,root_id=?,revision=revision+1 WHERE id=?", profile, root, id)
	return err
}
func (s *Store) RetryAcquisition(ctx context.Context, id string, revision int) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE acquisition_requests SET state='approved',connection_revision=? WHERE id=? AND state IN ('uncertain','failed','pending')", revision, id)
	if err != nil {
		return err
	}
	return plexChanged(res)
}

// Recheck opt-in and identity exclusions immediately before the external POST.
func (s *Store) ValidateAcquisition(ctx context.Context, r AcquisitionRequest, c ArrConnection) error {
	current, err := s.ArrConnection(ctx, c.ID)
	if err != nil {
		return err
	}
	if current.Revision != r.ConnectionRevision || Excluded(current, r.ProviderID) {
		return ErrConflict
	}
	if r.Origin == "automatic" {
		d, err := s.Defaults(ctx)
		if err != nil {
			return err
		}
		if !d.AutoDownload {
			return ErrConflict
		}
		provider, kind := "tvdb", "episode"
		if c.Kind == "radarr" {
			provider, kind = "tmdb", "movie"
		}
		var count int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM entries e JOIN collection_arr ca ON ca.collection_id=e.collection_id AND ca.connection_id=? WHERE e.collection_id NOT IN (SELECT collection_id FROM collection_no_download) AND e.provider=? AND e.kind=? AND e.external_id=?", c.ID, provider, kind, strconv.Itoa(r.ProviderID)).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			return ErrConflict
		}
	}
	return nil
}
