package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
)

func TestDefaultInstanceAdoptionBackfillAndNewCollections(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	entry := []Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}
	before, err := s.ImportCollection(ctx, Collection{Title: "Before", Entries: entry})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, kind, name string) ArrConnection {
		c := ArrConnection{ID: id, Kind: kind, Name: name, BaseURL: "https://x", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1}
		if err := s.CreateArr(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	sonarrA := mk("sa", "sonarr", "Sonarr A")
	// Connecting the first Sonarr after collections exist adds it to them (set and forget).
	if got, _ := s.CollectionArrs(ctx, before); !got[sonarrA.ID] {
		t.Fatalf("first instance not added to existing collections: %v", got)
	}
	mk("sb", "sonarr", "Sonarr B")
	radarr := mk("r", "radarr", "Radarr")
	d, _ := s.ArrDefaults(ctx)
	if d["sonarr"] != "sa" || d["radarr"] != "r" {
		t.Fatalf("defaults %v", d)
	}
	// A second instance of a kind is not added anywhere by itself.
	if got, _ := s.CollectionArrs(ctx, before); got["sb"] {
		t.Fatal("a non-default instance was added to an existing collection")
	}
	// Every way of creating a collection starts with the defaults.
	imported, _ := s.ImportCollection(ctx, Collection{Title: "Imported", Entries: entry})
	empty, _ := s.CreateCollection(ctx, "Empty", "")
	sub, err := s.CreateSubscribedCollection(ctx, "https://example.com/a.yaml", "automatic", []byte("x"), Collection{Title: "Followed", Entries: entry}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"import": imported, "empty": empty, "follow": sub.CollectionID} {
		got, _ := s.CollectionArrs(ctx, id)
		if len(got) != 2 || !got["sa"] || !got[radarr.ID] {
			t.Fatalf("%s: new collection did not start with the defaults: %v", name, got)
		}
	}
	// Switching the default affects new collections only; unknown IDs and wrong kinds are refused.
	if err = s.SetArrDefault(ctx, "sonarr", "sb"); err != nil {
		t.Fatal(err)
	}
	later, _ := s.ImportCollection(ctx, Collection{Title: "Later", Entries: entry})
	if got, _ := s.CollectionArrs(ctx, later); !got["sb"] || got["sa"] {
		t.Fatalf("new default not used: %v", got)
	}
	if got, _ := s.CollectionArrs(ctx, imported); !got["sa"] || got["sb"] {
		t.Fatalf("existing collection changed: %v", got)
	}
	if s.SetArrDefault(ctx, "sonarr", "r") == nil || s.SetArrDefault(ctx, "sonarr", "nope") == nil {
		t.Fatal("wrong kind or unknown ID accepted as a default")
	}
	// Removing an instance drops it from collections and from the defaults.
	if _, err = s.DB.Exec("DELETE FROM arr_connections WHERE id='sb'"); err != nil {
		t.Fatal(err)
	}
	if d, _ = s.ArrDefaults(ctx); d["sonarr"] != "" {
		t.Fatalf("a removed instance is still the default: %v", d)
	}
	if got, _ := s.CollectionArrs(ctx, later); got["sb"] {
		t.Fatal("removed instance still ticked")
	}
}

// The migration keeps behaviour: every existing collection used every instance, and the default of
// each kind is its first instance by name.
func TestInstanceMigrationKeepsBehaviour(t *testing.T) {
	sqlText, err := migrations.ReadFile("migrations/014_arr_instances.sql")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setup := `CREATE TABLE collections(id TEXT PRIMARY KEY);
CREATE TABLE preferences(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE arr_connections(id TEXT PRIMARY KEY, kind TEXT, name TEXT);
CREATE TABLE collection_no_download(collection_id TEXT PRIMARY KEY);
INSERT INTO collections VALUES('c1'),('c2');
INSERT INTO arr_connections VALUES('s2','sonarr','Zed'),('s1','sonarr','Alpha'),('r1','radarr','Radarr');`
	if _, err = db.Exec(setup); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(sqlText)); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Query("SELECT collection_id||'/'||connection_id FROM collection_arr ORDER BY 1")
	var got []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		got = append(got, v)
	}
	rows.Close()
	want := []string{"c1/r1", "c1/s1", "c1/s2", "c2/r1", "c2/s1", "c2/s2"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	var sonarr, radarr string
	db.QueryRow("SELECT value FROM preferences WHERE key='default_sonarr'").Scan(&sonarr)
	db.QueryRow("SELECT value FROM preferences WHERE key='default_radarr'").Scan(&radarr)
	if sonarr != "s1" || radarr != "r1" {
		t.Fatalf("defaults %q %q", sonarr, radarr)
	}
}
