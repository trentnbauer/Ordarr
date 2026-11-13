package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrAuthAttempt = errors.New("Plex sign-in attempt expired, is unavailable, or belongs to another browser session.")
var ErrPollSoon = errors.New("Wait two seconds before checking Plex authorisation again.")

type PlexAccount struct {
	UserID                           int64
	Username, FriendlyName, ClientID string
	Restricted                       bool
	CredentialsCipher                []byte `json:"-"`
	ExpiresAt, CheckedAt             int64
	Revision                         int
}
type PlexAuthAttempt struct {
	ID, ClientID, Kid string
	PrivateCipher     []byte `json:"-"`
	PINID, ExpiresAt  int64
	AccountRevision   int
}

func (s *Store) PlexAccount(ctx context.Context) (PlexAccount, error) {
	var a PlexAccount
	err := s.DB.QueryRowContext(ctx, "SELECT user_id,username,friendly_name,restricted,client_id,credentials_cipher,expires_at,checked_at,revision FROM plex_accounts WHERE id=1").Scan(&a.UserID, &a.Username, &a.FriendlyName, &a.Restricted, &a.ClientID, &a.CredentialsCipher, &a.ExpiresAt, &a.CheckedAt, &a.Revision)
	return a, err
}
func (s *Store) BeginPlexAuth(ctx context.Context, session string, a PlexAuthAttempt) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_auth_attempts WHERE expires_at<=? OR session_hash=?", time.Now().Unix(), tokenHash(session)); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO plex_auth_attempts(id,session_hash,private_cipher,client_id,kid,pin_id,expires_at,account_revision)
 SELECT ?,?,?,?,?,?,?,? WHERE (SELECT count(*) FROM plex_auth_attempts)<5`, a.ID, tokenHash(session), a.PrivateCipher, a.ClientID, a.Kid, a.PINID, a.ExpiresAt, a.AccountRevision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return invalid("Too many pending Plex sign-ins. Wait for existing attempts to expire.")
	}
	return tx.Commit()
}
func (s *Store) PlexAuthAttempt(ctx context.Context, session, id string, claimPoll bool) (PlexAuthAttempt, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return PlexAuthAttempt{}, err
	}
	defer tx.Rollback()
	var a PlexAuthAttempt
	var next int64
	err = tx.QueryRowContext(ctx, "SELECT id,private_cipher,client_id,kid,pin_id,expires_at,next_poll_at,account_revision FROM plex_auth_attempts WHERE id=? AND session_hash=? AND expires_at>?", id, tokenHash(session), time.Now().Unix()).Scan(&a.ID, &a.PrivateCipher, &a.ClientID, &a.Kid, &a.PINID, &a.ExpiresAt, &next, &a.AccountRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAuthAttempt
	}
	if err != nil {
		return a, err
	}
	if claimPoll {
		if next > time.Now().Unix() {
			return a, ErrPollSoon
		}
		if _, err = tx.ExecContext(ctx, "UPDATE plex_auth_attempts SET next_poll_at=? WHERE id=?", time.Now().Add(2*time.Second).Unix(), id); err != nil {
			return a, err
		}
	}
	return a, tx.Commit()
}
func (s *Store) CancelPlexAuth(ctx context.Context, session, id string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM plex_auth_attempts WHERE id=? AND session_hash=?", id, tokenHash(session))
	return err
}
func (s *Store) SavePlexAccount(ctx context.Context, a PlexAccount, expectedRevision int, attemptID, session string) error {
	if a.UserID <= 0 || !validTitle(a.Username) || len(a.CredentialsCipher) == 0 || a.ClientID == "" || a.ExpiresAt <= time.Now().Unix() {
		return invalid("Invalid Plex account credentials.")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if attemptID != "" {
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM plex_auth_attempts WHERE id=? AND session_hash=? AND expires_at>? AND account_revision=? AND client_id=?", attemptID, tokenHash(session), time.Now().Unix(), expectedRevision, a.ClientID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return ErrAuthAttempt
		}
	}
	var result sql.Result
	if expectedRevision == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO plex_accounts(id,user_id,username,friendly_name,restricted,client_id,credentials_cipher,expires_at,checked_at,revision)
   SELECT 1,?,?,?,?,?,?,?,?,1 WHERE NOT EXISTS(SELECT 1 FROM plex_accounts)`, a.UserID, a.Username, a.FriendlyName, a.Restricted, a.ClientID, a.CredentialsCipher, a.ExpiresAt, time.Now().Unix())
	} else {
		// Re-authorisation may register a new device key, but may not change the
		// authenticated Plex user beneath existing destination connections.
		result, err = tx.ExecContext(ctx, `UPDATE plex_accounts SET username=?,friendly_name=?,restricted=?,client_id=?,credentials_cipher=?,expires_at=?,checked_at=?,revision=revision+1 WHERE id=1 AND revision=? AND user_id=?`, a.Username, a.FriendlyName, a.Restricted, a.ClientID, a.CredentialsCipher, a.ExpiresAt, time.Now().Unix(), expectedRevision, a.UserID)
	}
	if err != nil {
		return err
	}
	if err = plexChanged(result); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE plex_connections SET client_id=?,revision=revision+1,last_error='Account credentials refreshed; recheck this server.' WHERE account_id=1 AND client_id<>?", a.ClientID, a.ClientID); err != nil {
		return err
	}
	if attemptID != "" {
		if _, err = tx.ExecContext(ctx, "DELETE FROM plex_auth_attempts WHERE id=?", attemptID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) DisconnectPlexAccount(ctx context.Context, revision int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM plex_accounts WHERE id=1").Scan(&current); err != nil {
		return err
	}
	if revision != current {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_connections WHERE account_id=1"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_accounts WHERE id=1"); err != nil {
		return err
	}
	// A pending attempt may otherwise re-add a just-disconnected account.
	if _, err = tx.ExecContext(ctx, "DELETE FROM plex_auth_attempts"); err != nil {
		return err
	}
	return tx.Commit()
}
