package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMigrationPersistenceAndSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db", "ordarr.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 11 {
		t.Fatalf("schema: %d %v", version, err)
	}
	var journal string
	s.DB.QueryRow("PRAGMA journal_mode").Scan(&journal)
	if journal != "wal" {
		t.Fatal(journal)
	}
	var fk int
	s.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk)
	if fk != 1 {
		t.Fatal("foreign keys disabled")
	}
	if err = s.CreateAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateAdmin(ctx, "replacement", []byte("other")); err == nil {
		t.Fatal("second administrator accepted")
	}
	if err = s.AddSession(ctx, "secret-session", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var raw string
	s.DB.QueryRow("SELECT token_hash FROM sessions").Scan(&raw)
	if raw == "secret-session" || len(raw) != 64 {
		t.Fatal("raw session stored")
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	name, hash, err := s.Admin(ctx)
	if err != nil || name != "admin" || string(hash) != "hash" {
		t.Fatalf("admin lost: %s %v", name, err)
	}
	ok, err := s.Authenticated(ctx, "secret-session")
	if !ok || err != nil {
		t.Fatal("session lost", err)
	}
	s.DB.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Hour).Unix())
	ok, err = s.Authenticated(ctx, "secret-session")
	if ok || err != nil {
		t.Fatal("expired session accepted", err)
	}
	if err = s.AddSession(ctx, "new-session", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSession(ctx, "new-session"); err != nil {
		t.Fatal(err)
	}
	ok, _ = s.Authenticated(ctx, "new-session")
	if ok {
		t.Fatal("revoked session accepted")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("database permissions", err)
	}
}
func TestConcurrentSetup(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ordarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.CreateAdmin(context.Background(), "admin", []byte("hash")) == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("setup winners: %d", winners.Load())
	}
}
func TestRejectFutureSchemaWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordarr.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Exec("PRAGMA user_version=999")
	s.DB.Close()
	if s, err = Open(path); err == nil {
		s.DB.Close()
		t.Fatal("accepted newer schema")
	}
}
func TestFailedMigrationRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordarr.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a conflicting legacy table at schema zero. The first CREATE must roll back.
	s.DB.Exec("DROP TABLE sessions")
	s.DB.Exec("DROP TABLE administrator")
	s.DB.Exec("CREATE TABLE sessions (existing TEXT)")
	s.DB.Exec("PRAGMA user_version=0")
	if err = s.migrate(); err == nil {
		t.Fatal("expected migration error")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='administrator'").Scan(&n)
	if n != 0 {
		t.Fatal("partial migration committed")
	}
	var v int
	s.DB.QueryRow("PRAGMA user_version").Scan(&v)
	if v != 0 {
		t.Fatal("version advanced after failure")
	}
	s.DB.Close()
}

func TestSessionRetentionBound(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ordarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	if err = s.CreateAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if err = s.AddSession(ctx, fmt.Sprintf("session-%d", i), time.Now().Add(time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 20 {
		t.Fatalf("retained %d sessions: %v", count, err)
	}
	if ok, _ := s.Authenticated(ctx, "session-0"); ok {
		t.Fatal("old session not evicted")
	}
	if ok, _ := s.Authenticated(ctx, "session-24"); !ok {
		t.Fatal("new session missing")
	}
}
