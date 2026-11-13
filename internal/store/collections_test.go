package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func collectionFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ordarr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	id, err := s.CreateCollection(context.Background(), "Mixed order", "Keep repeats and missing entries")
	if err != nil {
		t.Fatal(err)
	}
	return s, id
}
func movie(title, id string) EntrySpec {
	return EntrySpec{Kind: "movie", Title: title, Provider: "tmdb", ExternalID: id}
}
func current(t *testing.T, s *Store, id string) Collection {
	t.Helper()
	c, err := s.Collection(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func change(t *testing.T, s *Store, id string, ch CollectionChange) Collection {
	t.Helper()
	c := current(t, s, id)
	if err := s.ChangeCollection(context.Background(), id, c.Revision, ch); err != nil {
		t.Fatal(err)
	}
	return current(t, s, id)
}
func ids(c Collection) []string {
	var result []string
	for _, e := range c.Entries {
		result = append(result, e.ID)
	}
	return result
}
func TestRangeExpansionAndValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		spec     EntrySpec
		episodes []int
	}{
		{"movie", movie("Movie", "1771"), []int{0}},
		{"episode", EntrySpec{Kind: "episode", Title: "Series", Provider: "tvdb", ExternalID: "123", Season: 0, Start: 3}, []int{3}},
		{"range", EntrySpec{Kind: "episode_range", Title: "Series", Provider: "tmdb", ExternalID: "83867", Season: 1, Start: 2, End: 4}, []int{2, 3, 4}},
		{"season", EntrySpec{Kind: "season", Title: "Series", Provider: "imdb", ExternalID: "tt1234567", Season: 1, End: 3}, []int{1, 2, 3}},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries, err := Expand(test.spec)
			if err != nil {
				t.Fatal(err)
			}
			var got []int
			seen := map[string]bool{}
			for _, e := range entries {
				got = append(got, e.Episode)
				if e.ID == "" || seen[e.ID] {
					t.Fatal("unstable or duplicate identity")
				}
				seen[e.ID] = true
				if e.Provider != test.spec.Provider || e.ExternalID != test.spec.ExternalID {
					t.Fatal("identifier lost")
				}
			}
			if !reflect.DeepEqual(got, test.episodes) {
				t.Fatal(got)
			}
		})
	}
	base := EntrySpec{Kind: "episode_range", Title: "Series", Provider: "tmdb", ExternalID: "42", Season: 1, Start: 1, End: 2}
	for _, test := range []struct {
		name  string
		alter func(*EntrySpec)
	}{
		{"negative season", func(s *EntrySpec) { s.Season = -1 }}, {"episode zero", func(s *EntrySpec) { s.Start = 0 }},
		{"reverse range", func(s *EntrySpec) { s.Start = 3 }}, {"oversize", func(s *EntrySpec) { s.End = 201 }},
		{"huge number", func(s *EntrySpec) { s.End = 10001 }}, {"bad provider", func(s *EntrySpec) { s.Provider = "other" }},
		{"non-numeric ID", func(s *EntrySpec) { s.ExternalID = "42 OR 1=1" }}, {"bad imdb", func(s *EntrySpec) { s.Provider = "imdb" }},
		{"empty title", func(s *EntrySpec) { s.Title = " " }}, {"unknown kind", func(s *EntrySpec) { s.Kind = "season_guess" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.alter(&spec)
			if _, err := Expand(spec); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted bad input: %v", err)
			}
		})
	}
}
func TestCollectionEditingAndForkIndependence(t *testing.T) {
	s, id := collectionFixture(t)
	ctx := context.Background()
	c := change(t, s, id, CollectionChange{Action: "add", Spec: movie("First movie", "1771")})
	first := c.Entries[0].ID
	c = change(t, s, id, CollectionChange{Action: "add", Spec: EntrySpec{Kind: "episode_range", Title: "Series", Provider: "tmdb", ExternalID: "83867", Season: 1, Start: 2, End: 3}})
	c = change(t, s, id, CollectionChange{Action: "add", Spec: movie("Repeat movie", "1771")})
	last := c.Entries[3].ID
	if first == last || c.Entries[0].ExternalID != c.Entries[3].ExternalID {
		t.Fatal("repeat collapsed")
	}
	original := ids(c)
	// A destination match referencing a retained row must survive ordering changes.
	if _, err := s.DB.Exec("CREATE TABLE test_matches(entry_id TEXT REFERENCES entries(id)); INSERT INTO test_matches VALUES(?)", first); err != nil {
		t.Fatal(err)
	}
	c = change(t, s, id, CollectionChange{Action: "move", IDs: []string{last, original[1]}, Target: 1})
	want := []string{original[1], last, first, original[2]}
	if !reflect.DeepEqual(ids(c), want) {
		t.Fatal(ids(c), want)
	}
	c = change(t, s, id, CollectionChange{Action: "reorder", IDs: original})
	if !reflect.DeepEqual(ids(c), original) {
		t.Fatal("reorder failed")
	}
	c = change(t, s, id, CollectionChange{Action: "edit", IDs: []string{first}, Spec: movie("Renamed label", "1771")})
	if c.Entries[0].ID != first || c.Entries[0].Title != "Renamed label" {
		t.Fatal("edit changed entry identity")
	}
	c = change(t, s, id, CollectionChange{Action: "metadata", Title: "Revised collection", Description: "Description"})
	fork, err := s.ForkCollection(ctx, id, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	copy := current(t, s, fork)
	if copy.ForkedFrom != id || len(copy.Entries) != 4 {
		t.Fatal("fork data missing")
	}
	for i, e := range copy.Entries {
		if e.ID == c.Entries[i].ID || e.ExternalID != c.Entries[i].ExternalID {
			t.Fatal("fork reused ID or lost media")
		}
	}
	if err = s.DeleteCollection(ctx, id, c.Revision-1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete accepted", err)
	}
	if _, err = s.DB.Exec("DROP TABLE test_matches"); err != nil {
		t.Fatal(err)
	}
	c = change(t, s, id, CollectionChange{Action: "remove", IDs: []string{first, last}})
	if len(c.Entries) != 2 {
		t.Fatal("bulk remove failed")
	}
	if err = s.DeleteCollection(ctx, id, c.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Collection(ctx, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted collection exists")
	}
	if len(current(t, s, fork).Entries) != 4 {
		t.Fatal("source deletion modified fork")
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM entries WHERE collection_id=?", id).Scan(&n); err != nil || n != 0 {
		t.Fatal("orphan entries", err)
	}
}
func TestCollectionConflictsInvalidOrdersAndRollback(t *testing.T) {
	s, id := collectionFixture(t)
	ctx := context.Background()
	c := change(t, s, id, CollectionChange{Action: "add", Spec: movie("One", "1")})
	c = change(t, s, id, CollectionChange{Action: "add", Spec: movie("Two", "2")})
	for _, ch := range []CollectionChange{{Action: "reorder", IDs: []string{c.Entries[0].ID, c.Entries[0].ID}}, {Action: "reorder", IDs: []string{c.Entries[0].ID}}, {Action: "reorder", IDs: []string{c.Entries[0].ID, "foreign"}}, {Action: "move", IDs: ids(c), Target: 2}, {Action: "remove"}, {Action: "edit", IDs: ids(c), Spec: movie("Bad", "1")}} {
		if err := s.ChangeCollection(ctx, id, c.Revision, ch); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid mutation: %v", err)
		}
		after := current(t, s, id)
		if !reflect.DeepEqual(c, after) {
			t.Fatal("invalid operation changed collection")
		}
	}
	// Force a failure after positions have been temporarily shifted negative.
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_final_position BEFORE UPDATE OF position ON entries WHEN NEW.position=0 BEGIN SELECT RAISE(ABORT,'forced storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangeCollection(ctx, id, c.Revision, CollectionChange{Action: "move", IDs: []string{c.Entries[1].ID}, Target: 1}); err == nil {
		t.Fatal("injected storage failure not observed")
	}
	if after := current(t, s, id); !reflect.DeepEqual(c, after) {
		t.Fatal("failed write partially committed")
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_final_position"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.ChangeCollection(ctx, id, c.Revision, CollectionChange{Action: "metadata", Title: "Winner"})
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal(success, conflicts)
	}
	if _, err := s.ForkCollection(ctx, id, c.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale fork accepted", err)
	}
}
func TestCollectionsPersistAndUpgradeFoundation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordarr.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrations.ReadFile("migrations/001_foundation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=1; INSERT INTO administrator VALUES(1,'existing',X'68617368')"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO sessions VALUES(?,?,1)", tokenHash("existing-session"), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if name, _, err := s.Admin(ctx); err != nil || name != "existing" {
		t.Fatal("upgrade lost admin", err)
	}
	if ok, err := s.Authenticated(ctx, "existing-session"); !ok || err != nil {
		t.Fatal("upgrade lost session", err)
	}
	id, err := s.CreateCollection(ctx, "Persistent", "Includes specials")
	if err != nil {
		t.Fatal(err)
	}
	c := change(t, s, id, CollectionChange{Action: "add", Spec: EntrySpec{Kind: "season", Title: "Specials", Provider: "tvdb", ExternalID: "42", Season: 0, End: 3}})
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if got := current(t, s, id); !reflect.DeepEqual(c, got) {
		t.Fatal("collection changed across reopen")
	}
}
func TestCollectionSearchFiltersPaginationAndLimits(t *testing.T) {
	s, id := collectionFixture(t)
	ctx := context.Background()
	c := change(t, s, id, CollectionChange{Action: "add", Spec: movie("Movie", "1")})
	if _, err := s.ForkCollection(ctx, id, c.Revision); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		q, kind string
		want    int
	}{{"mixed", "movie", 2}, {"missing", "fork", 1}, {"", "episode", 0}, {"does-not-exist", "", 0}} {
		result, err := s.ListCollections(ctx, test.q, test.kind, 0)
		if err != nil || len(result) != test.want {
			t.Fatal(test, len(result), err)
		}
	}
	for i := 0; i < 23; i++ {
		if _, err := s.CreateCollection(ctx, "Page "+strconv.Itoa(i), ""); err != nil {
			t.Fatal(err)
		}
	}
	one, err := s.ListCollections(ctx, "Page", "", 0)
	if err != nil || len(one) != 21 {
		t.Fatal("first page", err)
	}
	two, err := s.ListCollections(ctx, "Page", "", 20)
	if err != nil || len(two) != 3 {
		t.Fatal("second page", err)
	}
	for _, a := range one[:20] {
		for _, b := range two {
			if a.ID == b.ID {
				t.Fatal("pagination overlap")
			}
		}
	}
	for i := 0; i < 5; i++ {
		c = change(t, s, id, CollectionChange{Action: "add", Spec: EntrySpec{Kind: "season", Title: "Series", Provider: "tmdb", ExternalID: "2", Season: i, End: 199}})
	}
	// 1 movie + 995 episodes. A further season must fail atomically.
	before := current(t, s, id)
	if err := s.ChangeCollection(ctx, id, before.Revision, CollectionChange{Action: "add", Spec: EntrySpec{Kind: "season", Title: "Series", Provider: "tmdb", ExternalID: "2", End: 10}}); !errors.Is(err, ErrInvalid) {
		t.Fatal("limit not enforced", err)
	}
	if !reflect.DeepEqual(before, current(t, s, id)) {
		t.Fatal("limit failure changed entries")
	}
}
