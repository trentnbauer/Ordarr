package store

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

func TestPlexSnapshotAtomicityConflictsAndLimits(t *testing.T) {
	s, _ := collectionFixture(t)
	ctx := context.Background()
	c := PlexConnection{ID: "connection", Name: "Plex", BaseURL: "https://plex.example", ClientID: "client", MachineID: "machine", ServerName: "Server", ServerVersion: "1", TokenCipher: []byte("encrypted"), Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}
	if err := s.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	before, err := s.PlexConnection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c = before
	c.Name = "Should roll back"
	c.TokenCipher = []byte("replacement")
	c.Libraries = append(c.Libraries, c.Libraries[0])
	if err = s.SavePlexConnection(ctx, c, before.Revision); err == nil {
		t.Fatal("duplicate snapshot key accepted")
	}
	after, err := s.PlexConnection(ctx, c.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial snapshot committed", err)
	}
	c = before
	c.Name = "Updated"
	if err = s.SavePlexConnection(ctx, c, before.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlexConnection(ctx, c, before.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	if err = s.PlexCheckFailed(ctx, c.ID, before.Revision, "failure"); !errors.Is(err, ErrConflict) {
		t.Fatal("stale failure overwrote newer state", err)
	}
	current, err := s.PlexConnection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c = current
	c.MachineID = "other"
	if err = s.SavePlexConnection(ctx, c, current.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("machine identity changed", err)
	}
	if !bytes.Equal(current.TokenCipher, before.TokenCipher) {
		t.Fatal("token changed unexpectedly")
	}
	for i := 0; i < 9; i++ {
		c = before
		c.ID = string(rune('a' + i))
		if err = s.SavePlexConnection(ctx, c, 0); err != nil {
			t.Fatal(err)
		}
	}
	c.ID = "eleventh"
	if err = s.SavePlexConnection(ctx, c, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded connections", err)
	}
}
