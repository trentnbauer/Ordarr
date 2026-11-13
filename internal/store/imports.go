package store

import (
	"context"
	"database/sql"
	"time"
)

// ValidatePortableCollection also guards callers other than the public decoder.
func ValidatePortableCollection(c Collection) error {
	if err := validateMetadata(c.Title, c.Description); err != nil {
		return err
	}
	if len(c.Entries) > MaxEntries {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, e := range c.Entries {
		if e.ID == "" || seen[e.ID] {
			return ErrInvalid
		}
		seen[e.ID] = true
		expanded, err := Expand(EntrySpec{Kind: e.Kind, Title: e.Title, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Start: e.Episode})
		if err != nil || len(expanded) != 1 {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Store) ImportCollection(ctx context.Context, c Collection) (string, error) {
	if err := ValidatePortableCollection(c); err != nil {
		return "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id := newID()
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "INSERT INTO collections(id,title,description,created_at,updated_at) VALUES(?,?,?,?,?)", id, c.Title, c.Description, now, now); err != nil {
		return "", err
	}
	for i, e := range c.Entries {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entries VALUES(?,?,?,?,?,?,?,?,?)", newID(), id, i, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

// replaceSource preserves destination-independent identities across upstream reorders.
func replaceSource(ctx context.Context, tx *sql.Tx, sub Subscription, c Collection) error {
	if err := ValidatePortableCollection(c); err != nil {
		return err
	}
	old := map[string]string{}
	rows, err := tx.QueryContext(ctx, "SELECT source_id,entry_id FROM source_entries WHERE subscription_id=?", sub.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var source, entry string
		if err = rows.Scan(&source, &entry); err != nil {
			rows.Close()
			return err
		}
		old[source] = entry
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE entries SET position=-position-1 WHERE collection_id=?", sub.CollectionID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM source_entries WHERE subscription_id=?", sub.ID); err != nil {
		return err
	}
	kept := map[string]bool{}
	for i, e := range c.Entries {
		id := old[e.ID]
		if id == "" {
			id = newID()
			_, err = tx.ExecContext(ctx, "INSERT INTO entries VALUES(?,?,?,?,?,?,?,?,?)", id, sub.CollectionID, i, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode)
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE entries SET position=?,kind=?,title=?,provider=?,external_id=?,season=?,episode=? WHERE id=?", i, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode, id)
		}
		if err != nil {
			return err
		}
		kept[id] = true
		if _, err = tx.ExecContext(ctx, "INSERT INTO source_entries VALUES(?,?,?)", sub.ID, e.ID, id); err != nil {
			return err
		}
	}
	for _, id := range old {
		if !kept[id] {
			if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE id=? AND collection_id=?", id, sub.CollectionID); err != nil {
				return err
			}
		}
	}
	// Remove the initial imported rows on first fetch; they have no source mapping.
	if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE collection_id=? AND position<0", sub.CollectionID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE collections SET title=?,description=?,revision=revision+1,updated_at=? WHERE id=?", c.Title, c.Description, time.Now().Unix(), sub.CollectionID)
	return err
}
