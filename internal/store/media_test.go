package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

func TestLibraryPagePersistenceConflictsAndFailureRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordarr.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := PlexConnection{ID: "connection", Name: "Home", BaseURL: "https://plex.example", ClientID: "client", MachineID: "machine", ServerName: "Plex", ServerVersion: "1", TokenCipher: []byte("cipher"), Libraries: []plex.Library{{Key: "1", Kind: "movie", Title: "Movies"}}}
	if err = s.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	payload := LibraryPagePayload{Page: plex.MediaPage{Kind: "movie", Offset: 1, Total: 2, Items: []plex.MediaItem{{RatingKey: "2", Kind: "movie", Title: "Two"}}}, History: []int{0}}
	if err = s.SaveLibraryPage(ctx, c.ID, "1", 1, 0, payload); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLibraryPage(ctx, c.ID, "1", 1, 0, payload); !errors.Is(err, ErrConflict) {
		t.Fatal("stale create accepted", err)
	}
	if err = s.LibraryPageFailed(ctx, c.ID, "1", 1, "fetch failed"); err != nil {
		t.Fatal(err)
	}
	page, err := s.LibraryPage(ctx, c.ID, "1")
	if err != nil || page.LastError != "fetch failed" || !reflect.DeepEqual(page.Payload, payload) {
		t.Fatal("failure replaced snapshot", err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	restored, err := s.LibraryPage(ctx, c.ID, "1")
	if err != nil || !reflect.DeepEqual(page, restored) {
		t.Fatal("page lost after reopen", err)
	}
	c.Revision = 1
	if err = s.SavePlexConnection(ctx, c, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLibraryPage(ctx, c.ID, "1", 1, page.Revision, payload); !errors.Is(err, ErrConflict) {
		t.Fatal("stale connection save accepted", err)
	}
	if err = s.SaveLibraryPage(ctx, c.ID, "missing", 2, 0, payload); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown library cached", err)
	}
	// Successful library discovery must remove caches for sections no longer accessible.
	c.Libraries = nil
	if err = s.SavePlexConnection(ctx, c, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LibraryPage(ctx, c.ID, "1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked library cache retained", err)
	}
	// Recreate a valid cached page and verify connection removal cascades it too.
	c.Libraries = []plex.Library{{Key: "1", Kind: "movie", Title: "Movies"}}
	if err = s.SavePlexConnection(ctx, c, 3); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLibraryPage(ctx, c.ID, "1", 4, 0, payload); err != nil {
		t.Fatal(err)
	}
	if err = s.DeletePlexConnection(ctx, c.ID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LibraryPage(ctx, c.ID, "1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cache survived connection removal")
	}
}
