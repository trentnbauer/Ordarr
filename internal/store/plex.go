package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

type PlexConnection struct {
	AccountID                                                         int
	ResourceID                                                        string
	ID, Name, BaseURL, ClientID, MachineID, ServerName, ServerVersion string
	TokenCipher                                                       []byte `json:"-"`
	AllowHTTP                                                         bool
	Revision                                                          int
	CheckedAt, AttemptedAt                                            int64
	LastError                                                         string
	Libraries                                                         []plex.Library
}

func (p PlexConnection) String() string { return "Plex connection " + p.ID }
func (s *Store) HasPlexCredentials(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM plex_connections)+(SELECT count(*) FROM arr_connections)+(SELECT count(*) FROM plex_accounts)+(SELECT count(*) FROM plex_auth_attempts WHERE expires_at>?)", time.Now().Unix()).Scan(&n)
	return n > 0, err
}
func loadPlexConnection(ctx context.Context, q queryer, id string) (PlexConnection, error) {
	var c PlexConnection
	err := q.QueryRowContext(ctx, `SELECT id,name,base_url,client_id,token_cipher,machine_id,server_name,server_version,allow_http,revision,checked_at,attempted_at,last_error,coalesce(account_id,0),resource_id FROM plex_connections WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.BaseURL, &c.ClientID, &c.TokenCipher, &c.MachineID, &c.ServerName, &c.ServerVersion, &c.AllowHTTP, &c.Revision, &c.CheckedAt, &c.AttemptedAt, &c.LastError, &c.AccountID, &c.ResourceID)
	if err != nil {
		return c, err
	}
	rows, err := q.QueryContext(ctx, "SELECT library_key,title,kind FROM plex_libraries WHERE connection_id=? ORDER BY title,library_key", id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var lib plex.Library
		if err = rows.Scan(&lib.Key, &lib.Title, &lib.Kind); err != nil {
			return c, err
		}
		c.Libraries = append(c.Libraries, lib)
	}
	return c, rows.Err()
}
func (s *Store) PlexConnection(ctx context.Context, id string) (PlexConnection, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PlexConnection{}, err
	}
	defer tx.Rollback()
	c, err := loadPlexConnection(ctx, tx, id)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) PlexConnectionIDs(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM plex_connections ORDER BY name,id")
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
func (s *Store) SavePlexConnection(ctx context.Context, c PlexConnection, expectedRevision int) error {
	if !validTitle(c.Name) || c.ID == "" || len(c.TokenCipher) == 0 || c.MachineID == "" || len(c.Libraries) > 1000 {
		return invalid("Invalid Plex connection.")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var accountID any
	if c.AccountID != 0 {
		if c.AccountID != 1 || c.ResourceID != c.MachineID {
			return invalid("Invalid account destination.")
		}
		var client string
		if err = tx.QueryRowContext(ctx, "SELECT client_id FROM plex_accounts WHERE id=1").Scan(&client); err != nil {
			return err
		}
		if client != c.ClientID {
			return ErrConflict
		}
		accountID = 1
	}
	var result sql.Result
	if expectedRevision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO plex_connections(id,name,base_url,client_id,token_cipher,machine_id,server_name,server_version,allow_http,revision,checked_at,attempted_at,account_id,resource_id)
   SELECT ?,?,?,?,?,?,?,?,?,1,?,?,?,? WHERE (SELECT count(*) FROM plex_connections)<10`, c.ID, c.Name, c.BaseURL, c.ClientID, c.TokenCipher, c.MachineID, c.ServerName, c.ServerVersion, c.AllowHTTP, now, now, accountID, c.ResourceID)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE plex_connections SET name=?,base_url=?,token_cipher=?,machine_id=?,server_name=?,server_version=?,allow_http=?,revision=revision+1,checked_at=?,attempted_at=?,last_error='' WHERE id=? AND revision=? AND machine_id=? AND client_id=?`, c.Name, c.BaseURL, c.TokenCipher, c.MachineID, c.ServerName, c.ServerVersion, c.AllowHTTP, now, now, c.ID, expectedRevision, c.MachineID, c.ClientID)
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		if expectedRevision == 0 {
			return invalid("At most ten Plex connections are supported.")
		}
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_libraries WHERE connection_id=?", c.ID); err != nil {
		return err
	}
	for _, lib := range c.Libraries {
		if _, err = tx.ExecContext(ctx, "INSERT INTO plex_libraries VALUES(?,?,?,?)", c.ID, lib.Key, lib.Title, lib.Kind); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_media_pages WHERE connection_id=? AND library_key NOT IN (SELECT library_key FROM plex_libraries WHERE connection_id=?)", c.ID, c.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PlexCheckFailed(ctx context.Context, id string, revision int, message string) error {
	// The application supplies only fixed adapter error messages, never remote bodies.
	result, err := s.DB.ExecContext(ctx, "UPDATE plex_connections SET last_error=?,attempted_at=?,revision=revision+1 WHERE id=? AND revision=?", message, time.Now().Unix(), id, revision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
func (s *Store) DeletePlexConnection(ctx context.Context, id string, revision int) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM plex_connections WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
func plexChanged(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
