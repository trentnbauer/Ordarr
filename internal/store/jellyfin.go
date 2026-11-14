package store

import (
	"context"
	"encoding/json"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"time"
)

type JellyfinConnection struct {
	ID, Name, BaseURL string
	TokenCipher       []byte `json:"-"`
	AllowHTTP         bool
	Revision          int
	Snapshot          jellyfin.Snapshot
	CheckedAt         int64
}

func scanJellyfin(row interface{ Scan(...any) error }) (JellyfinConnection, error) {
	var c JellyfinConnection
	var data []byte
	err := row.Scan(&c.ID, &c.Name, &c.BaseURL, &c.TokenCipher, &c.AllowHTTP, &c.Revision, &data, &c.CheckedAt)
	if err == nil {
		err = json.Unmarshal(data, &c.Snapshot)
	}
	return c, err
}
func (s *Store) JellyfinConnection(ctx context.Context, id string) (JellyfinConnection, error) {
	return scanJellyfin(s.DB.QueryRowContext(ctx, "SELECT * FROM jellyfin_connections WHERE id=?", id))
}
func (s *Store) JellyfinConnections(ctx context.Context) ([]JellyfinConnection, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM jellyfin_connections ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []JellyfinConnection{}
	for rows.Next() {
		c, err := scanJellyfin(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}
func (s *Store) CreateJellyfin(ctx context.Context, c JellyfinConnection) error {
	if !validTitle(c.Name) {
		return ErrInvalid
	}
	data, err := json.Marshal(c.Snapshot)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO jellyfin_connections(id,name,base_url,token_cipher,allow_http,snapshot,checked_at) VALUES(?,?,?,?,?,?,?)", c.ID, c.Name, c.BaseURL, c.TokenCipher, c.AllowHTTP, data, time.Now().Unix())
	return err
}

// Rechecking can refresh topology but never silently switch the connected user/server.
func (s *Store) RecheckJellyfin(ctx context.Context, c JellyfinConnection, snapshot jellyfin.Snapshot) error {
	if snapshot.ServerID != c.Snapshot.ServerID || snapshot.UserID != c.Snapshot.UserID {
		return jellyfin.ErrChanged
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE jellyfin_connections SET snapshot=?,checked_at=?,revision=revision+1 WHERE id=? AND revision=?", data, time.Now().Unix(), c.ID, c.Revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}
func (s *Store) DeleteJellyfin(ctx context.Context, id string, revision int) error {
	res, err := s.DB.ExecContext(ctx, "DELETE FROM jellyfin_connections WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	return plexChanged(res)
}
func (s *Store) StageJellyfin(ctx context.Context, connection, generation string, item jellyfin.Item) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO jellyfin_catalogue_items VALUES(?,?,?,?)", connection, generation, item.ID, data)
	return err
}
func (s *Store) ClearJellyfinStage(ctx context.Context, connection, generation string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM jellyfin_catalogue_items WHERE connection_id=? AND generation=? AND NOT EXISTS(SELECT 1 FROM jellyfin_catalogues WHERE connection_id=? AND generation=?)", connection, generation, connection, generation)
	return err
}
func (s *Store) CommitJellyfinCatalogue(ctx context.Context, c JellyfinConnection, generation string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM jellyfin_connections WHERE id=?", c.ID).Scan(&revision); err != nil {
		return err
	}
	if revision != c.Revision {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO jellyfin_catalogues VALUES(?,?,?,?) ON CONFLICT(connection_id) DO UPDATE SET connection_revision=excluded.connection_revision,generation=excluded.generation,scanned_at=excluded.scanned_at", c.ID, c.Revision, generation, time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM jellyfin_catalogue_items WHERE connection_id=? AND generation<>?", c.ID, generation); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) JellyfinCatalogue(ctx context.Context, connection string) ([]jellyfin.Item, int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var generation string
	var at int64
	var revision, current int
	if err = tx.QueryRowContext(ctx, "SELECT x.generation,x.scanned_at,x.connection_revision,c.revision FROM jellyfin_catalogues x JOIN jellyfin_connections c ON c.id=x.connection_id WHERE c.id=?", connection).Scan(&generation, &at, &revision, &current); err != nil {
		return nil, 0, err
	}
	if revision != current {
		return nil, at, ErrConflict
	}
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM jellyfin_catalogue_items WHERE connection_id=? AND generation=? ORDER BY item_id", connection, generation)
	if err != nil {
		return nil, at, err
	}
	list := []jellyfin.Item{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			rows.Close()
			return nil, at, err
		}
		var item jellyfin.Item
		if err = json.Unmarshal(data, &item); err != nil {
			rows.Close()
			return nil, at, err
		}
		list = append(list, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, at, err
	}
	return list, at, tx.Commit()
}
func (s *Store) JellyfinManualMatches(ctx context.Context, connection string, c Collection) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT entry_id,identity,item_id FROM jellyfin_manual_matches WHERE connection_id=?", connection)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fingerprints := map[string]string{}
	for _, e := range c.Entries {
		fingerprints[e.ID] = EntryIdentity(e)
	}
	result := map[string]string{}
	for rows.Next() {
		var id, identity, item string
		if err = rows.Scan(&id, &identity, &item); err != nil {
			return nil, err
		}
		if fingerprints[id] == identity {
			result[id] = item
		}
	}
	return result, rows.Err()
}

// Validate against the current catalogue and collection revision atomically.
func (s *Store) SetJellyfinMatch(ctx context.Context, connection, collection, entryID, key string, revision int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := loadCollection(ctx, tx, collection)
	if err != nil {
		return err
	}
	if c.Revision != revision {
		return ErrConflict
	}
	var entry Entry
	for _, e := range c.Entries {
		if e.ID == entryID {
			entry = e
		}
	}
	if entry.ID == "" {
		return ErrInvalid
	}
	if key == "" {
		_, err = tx.ExecContext(ctx, "DELETE FROM jellyfin_manual_matches WHERE connection_id=? AND entry_id=?", connection, entry.ID)
	} else {
		key, ok := jellyfin.ID(key)
		if !ok {
			return ErrInvalid
		}
		var data []byte
		if err = tx.QueryRowContext(ctx, "SELECT i.payload FROM jellyfin_catalogue_items i JOIN jellyfin_catalogues x ON x.connection_id=i.connection_id AND x.generation=i.generation JOIN jellyfin_connections c ON c.id=x.connection_id AND c.revision=x.connection_revision WHERE i.connection_id=? AND i.item_id=?", connection, key).Scan(&data); err != nil {
			return err
		}
		var item jellyfin.Item
		if json.Unmarshal(data, &item) != nil || item.Kind != entry.Kind {
			return ErrInvalid
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO jellyfin_manual_matches VALUES(?,?,?,?) ON CONFLICT(connection_id,entry_id) DO UPDATE SET identity=excluded.identity,item_id=excluded.item_id", connection, entry.ID, EntryIdentity(entry), key)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
