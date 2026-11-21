package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

// The migration keeps today's behaviour: collections that were not opted in stay opted out,
// but only on installs that actually use Sonarr/Radarr.
func TestDownloadOptOutMigration(t *testing.T) {
	sqlText, err := migrations.ReadFile("migrations/013_download_optout.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		arr        bool
		automatic  int
		optedIn    bool // collection "a" had an opt-in row
		wantOptOut []string
		wantPref   string
	}{
		{"uses arr, automatic on, a opted in", true, 1, true, []string{"b"}, "1"},
		{"uses arr, automatic off", true, 0, false, []string{"a", "b"}, "0"},
		{"no arr at all", false, 0, false, nil, ""},
	} {
		db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "m.db"))
		if err != nil {
			t.Fatal(err)
		}
		setup := `CREATE TABLE collections(id TEXT PRIMARY KEY);
CREATE TABLE preferences(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE arr_connections(id TEXT PRIMARY KEY, automatic INTEGER);
CREATE TABLE arr_automation(connection_id TEXT, collection_id TEXT);
INSERT INTO collections VALUES('a'),('b');`
		if _, err = db.Exec(setup); err != nil {
			t.Fatal(err)
		}
		if c.arr {
			if _, err = db.Exec("INSERT INTO arr_connections VALUES('x',?)", c.automatic); err != nil {
				t.Fatal(err)
			}
		}
		if c.optedIn {
			if _, err = db.Exec("INSERT INTO arr_automation VALUES('x','a')"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = db.Exec(string(sqlText)); err != nil {
			t.Fatal(c.name, err)
		}
		rows, err := db.Query("SELECT collection_id FROM collection_no_download ORDER BY 1")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			got = append(got, id)
		}
		rows.Close()
		if !slices.Equal(got, c.wantOptOut) {
			t.Fatalf("%s: opt-outs %v want %v", c.name, got, c.wantOptOut)
		}
		var pref string
		db.QueryRow("SELECT value FROM preferences WHERE key='default_auto_download'").Scan(&pref)
		if pref != c.wantPref {
			t.Fatalf("%s: default %q want %q", c.name, pref, c.wantPref)
		}
		db.Close()
	}
}

func TestDefaultsAutoDownloadOnByDefaultAndOptOutToggle(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	d, err := s.Defaults(ctx)
	if err != nil || !d.AutoDownload || d.SourcePolicy != "automatic" {
		t.Fatal("new installs should follow sources and download automatically", d, err)
	}
	id, err := s.ImportCollection(ctx, Collection{Title: "T", Entries: []Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetCollectionDownloads(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if off, _ := s.NoDownload(ctx); !off[id] {
		t.Fatal("opt-out not stored")
	}
	if err = s.SetCollectionDownloads(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if off, _ := s.NoDownload(ctx); off[id] {
		t.Fatal("opt-out not cleared")
	}
}
