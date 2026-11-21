package store

import (
	"context"
	"database/sql"
)

// ArrDefaults returns the default instance ID per kind ("sonarr", "radarr"); a kind with no
// valid default is absent.
func (s *Store) ArrDefaults(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT a.kind,a.id FROM preferences p JOIN arr_connections a ON a.id=p.value AND p.key='default_'||a.kind")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			return nil, err
		}
		out[kind] = id
	}
	return out, rows.Err()
}

// SetArrDefault makes id the default instance of its kind for new collections; an empty id
// clears the default for kind. Existing collections are not changed.
func (s *Store) SetArrDefault(ctx context.Context, kind, id string) error {
	if kind != "sonarr" && kind != "radarr" {
		return ErrInvalid
	}
	if id == "" {
		_, err := s.DB.ExecContext(ctx, "DELETE FROM preferences WHERE key=?", "default_"+kind)
		return err
	}
	var found string
	if err := s.DB.QueryRowContext(ctx, "SELECT id FROM arr_connections WHERE id=? AND kind=?", id, kind).Scan(&found); err != nil {
		if err == sql.ErrNoRows {
			return ErrInvalid
		}
		return err
	}
	return s.SetPref(ctx, "default_"+kind, id)
}

// CollectionArrs is the set of instance IDs a collection downloads from.
func (s *Store) CollectionArrs(ctx context.Context, collection string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT connection_id FROM collection_arr WHERE collection_id=?", collection)
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

// SetCollectionArrs replaces the instances a collection downloads from. Unknown IDs are refused.
func (s *Store) SetCollectionArrs(ctx context.Context, collection string, ids []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM collections WHERE id=?", collection).Scan(&n); err != nil || n == 0 {
		if err == nil {
			err = sql.ErrNoRows
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM collection_arr WHERE collection_id=?", collection); err != nil {
		return err
	}
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO collection_arr SELECT ?,id FROM arr_connections WHERE id=?", collection, id)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			var exists int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM arr_connections WHERE id=?", id).Scan(&exists); err != nil || exists == 0 {
				return ErrInvalid
			}
		}
	}
	return tx.Commit()
}

// CollectionsForArr lists the collections that download from an instance.
func (s *Store) CollectionsForArr(ctx context.Context, connection string) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT collection_id FROM collection_arr WHERE connection_id=?", connection)
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
