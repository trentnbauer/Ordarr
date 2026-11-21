package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestCoverPersistsThroughImportCopyAndSourceUpdates(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	entry := []Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}
	id, err := s.ImportCollection(ctx, Collection{Title: "T", Cover: "https://img.example/a.png", Entries: entry})
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Collection(ctx, id); c.Cover != "https://img.example/a.png" {
		t.Fatalf("cover not stored: %q", c.Cover)
	}
	for _, bad := range []string{"http://img.example/a.png", "javascript:alert(1)", "https://img.example/a b.png", "https://u:p@img.example/a.png"} {
		if _, err = s.ImportCollection(ctx, Collection{Title: "T", Cover: bad, Entries: entry}); err == nil {
			t.Errorf("cover %q accepted", bad)
		}
	}
	// A copy keeps the cover.
	c, _ := s.Collection(ctx, id)
	clone, err := s.ForkCollection(ctx, id, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := s.Collection(ctx, clone); k.Cover != "https://img.example/a.png" {
		t.Fatal("copy lost the cover")
	}
	// A followed source: the cover comes with it and follows later changes (including removal).
	sub, err := s.CreateSubscribedCollection(ctx, "https://example.com/a.yaml", "automatic", []byte("v1"), Collection{Title: "F", Cover: "https://img.example/one.png", Entries: entry}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Collection(ctx, sub.CollectionID); c.Cover != "https://img.example/one.png" {
		t.Fatal("followed collection has no cover")
	}
	if err = s.ReceiveSource(ctx, sub, []byte("v2"), Collection{Title: "F", Cover: "https://img.example/two.png", Entries: entry}, "", ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Collection(ctx, sub.CollectionID); c.Cover != "https://img.example/two.png" {
		t.Fatalf("source update did not change the cover: %q", c.Cover)
	}
	// The downloaded copy: round trip, replace, clear; and the per-playlist poster marker.
	if err = s.SaveCover(ctx, id, "https://img.example/a.png", "image/png", []byte("one")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.CoverFor(ctx, id)
	if err != nil || !ok || string(got.Data) != "one" || got.ContentType != "image/png" || got.SHA == "" || s.CoverURLCached(ctx, id) != "https://img.example/a.png" {
		t.Fatalf("cover round trip: %+v %v %v", got, ok, err)
	}
	if err = s.SaveCover(ctx, id, "https://img.example/b.png", "image/jpeg", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := s.CoverFor(ctx, id); string(again.Data) != "two" || again.SHA == got.SHA {
		t.Fatal("cover not replaced")
	}
	if err = s.ClearCover(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = s.CoverFor(ctx, id); ok {
		t.Fatal("cover not cleared")
	}
	// Deleting the collection removes its downloaded cover (no orphan rows).
	s.SaveCover(ctx, clone, "https://img.example/a.png", "image/png", []byte("x"))
	if _, err = s.DB.Exec("DELETE FROM collections WHERE id=?", clone); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ = s.CoverFor(ctx, clone); ok {
		t.Fatal("orphaned cover row")
	}
}
