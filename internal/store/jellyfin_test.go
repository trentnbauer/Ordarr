package store

import (
	"context"
	"github.com/trentnbauer/Ordarr/internal/jellyfin"
	"path/filepath"
	"testing"
)

func TestJellyfinCatalogueRevisionAndManualIdentityProtection(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	c := JellyfinConnection{ID: "jellyfin", Name: "Home", BaseURL: "https://example.com", TokenCipher: []byte("encrypted"), Snapshot: jellyfin.Snapshot{ServerID: "server", UserID: "11111111111111111111111111111111"}}
	if err = s.CreateJellyfin(ctx, c); err != nil {
		t.Fatal(err)
	}
	c, _ = s.JellyfinConnection(ctx, c.ID)
	item := jellyfin.Item{ID: "22222222222222222222222222222222", Kind: "movie", Title: "Movie", IDs: map[string]string{"tmdb": "1"}}
	if err = s.StageJellyfin(ctx, c.ID, "good", item); err != nil {
		t.Fatal(err)
	}
	if err = s.CommitJellyfinCatalogue(ctx, c, "good"); err != nil {
		t.Fatal(err)
	}
	id, err := s.ImportCollection(ctx, Collection{Title: "Order", Entries: []Entry{{ID: "first", Kind: "movie", Title: "Movie", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	collection, _ := s.Collection(ctx, id)
	entry := collection.Entries[0]
	if err = s.SetJellyfinMatch(ctx, c.ID, id, entry.ID, item.ID, collection.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeCollection(ctx, id, collection.Revision, CollectionChange{Action: "edit", IDs: []string{entry.ID}, Spec: EntrySpec{Kind: "movie", Title: "Other", Provider: "tmdb", ExternalID: "2"}}); err != nil {
		t.Fatal(err)
	}
	collection, _ = s.Collection(ctx, id)
	manual, _ := s.JellyfinManualMatches(ctx, c.ID, collection)
	if len(manual) != 0 {
		t.Fatal("stale manual identity applied")
	}
	if err = s.SetJellyfinMatch(ctx, c.ID, id, entry.ID, item.ID, collection.Revision-1); err != ErrConflict {
		t.Fatal("stale revision accepted", err)
	}
	// Cleanup cannot delete a generation already made current (including an uncertain commit).
	if err = s.ClearJellyfinStage(ctx, c.ID, "good"); err != nil {
		t.Fatal(err)
	}
	// A failed staged generation never changes the visible complete generation.
	item.ID = "33333333333333333333333333333333"
	if err = s.StageJellyfin(ctx, c.ID, "failed", item); err != nil {
		t.Fatal(err)
	}
	if err = s.ClearJellyfinStage(ctx, c.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	items, _, err := s.JellyfinCatalogue(ctx, c.ID)
	if err != nil || len(items) != 1 || items[0].ID == item.ID {
		t.Fatal(items, err)
	}
	if err = s.RecheckJellyfin(ctx, c, c.Snapshot); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.JellyfinCatalogue(ctx, c.ID); err != ErrConflict {
		t.Fatal("catalogue from old permissions remained current", err)
	}
	if err = s.CommitJellyfinCatalogue(ctx, c, "old-connection"); err != ErrConflict {
		t.Fatal("old scan replaced current state", err)
	}
}
