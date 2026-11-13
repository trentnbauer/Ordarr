package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ DB *sql.DB }

func Open(path string) (*Store, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	f.Close()
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_synchronous=FULL&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) migrate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	if version > len(files) {
		return fmt.Errorf("database schema %d is newer than supported %d", version, len(files))
	}
	for i := version; i < len(files); i++ {
		data, err := migrations.ReadFile("migrations/" + files[i].Name())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("migration %s: %w", files[i].Name(), err)
		}
		if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", i+1)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Admin(ctx context.Context) (string, []byte, error) {
	var name string
	var hash []byte
	err := s.DB.QueryRowContext(ctx, "SELECT username,password_hash FROM administrator WHERE id=1").Scan(&name, &hash)
	return name, hash, err
}
func (s *Store) CreateAdmin(ctx context.Context, name string, hash []byte) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO administrator VALUES(1,?,?)", name, hash)
	return err
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Store) AddSession(ctx context.Context, token string, expiry time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash IN (SELECT token_hash FROM sessions ORDER BY expires_at DESC LIMIT -1 OFFSET 19)"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions VALUES(?,?,1)", tokenHash(token), expiry.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Authenticated(ctx context.Context, token string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT 1 FROM sessions WHERE token_hash=? AND expires_at>?", tokenHash(token), time.Now().Unix()).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n == 1, err
}
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash(token))
	return err
}
