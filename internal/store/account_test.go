package store

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestPlexAuthAttemptSessionExpiryThrottleAndBounds(t *testing.T) {
	s, _ := collectionFixture(t)
	ctx := context.Background()
	attempt := PlexAuthAttempt{ID: "attempt", ClientID: "device", Kid: "kid", PrivateCipher: []byte("encrypted-private-key"), PINID: 42, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := s.BeginPlexAuth(ctx, "session-one", attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session-two", attempt.ID, false); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("cross-session attempt read", err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session-one", attempt.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session-one", attempt.ID, true); !errors.Is(err, ErrPollSoon) {
		t.Fatal("unbounded polling", err)
	}
	if err := s.CancelPlexAuth(ctx, "session-two", attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session-one", attempt.ID, false); err != nil {
		t.Fatal("cross-session cancellation", err)
	}
	if _, err := s.DB.Exec("UPDATE plex_auth_attempts SET expires_at=0"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session-one", attempt.ID, false); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("expired PIN read", err)
	}
	for i := 0; i < 5; i++ {
		attempt.ID = "attempt-" + strconv.Itoa(i)
		if err := s.BeginPlexAuth(ctx, "session-"+strconv.Itoa(i), attempt); err != nil {
			t.Fatal(err)
		}
	}
	attempt.ID = "sixth"
	if err := s.BeginPlexAuth(ctx, "sixth-session", attempt); !errors.Is(err, ErrInvalid) {
		t.Fatal("pending attempts unbounded", err)
	}
}
func TestPlexAccountCASAndDisconnectPreserveManualCollections(t *testing.T) {
	s, collectionID := collectionFixture(t)
	ctx := context.Background()
	account := PlexAccount{UserID: 123, Username: "plex-user", ClientID: "device-one", CredentialsCipher: []byte("encrypted"), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.SavePlexAccount(ctx, account, 0, "", ""); err != nil {
		t.Fatal(err)
	}
	manual := PlexConnection{ID: "manual", Name: "Manual", BaseURL: "https://example.com", ClientID: "manual-client", TokenCipher: []byte("cipher"), MachineID: "machine", ServerName: "Server", ServerVersion: "1"}
	if err := s.SavePlexConnection(ctx, manual, 0); err != nil {
		t.Fatal(err)
	}
	linked := manual
	linked.ID = "linked"
	linked.Name = "Linked"
	linked.ClientID = "device-one"
	linked.AccountID = 1
	linked.ResourceID = linked.MachineID
	if err := s.SavePlexConnection(ctx, linked, 0); err != nil {
		t.Fatal(err)
	}
	account.ClientID = "device-two"
	if err := s.SavePlexAccount(ctx, account, 1, "", ""); err != nil {
		t.Fatal(err)
	}
	updated, err := s.PlexConnection(ctx, "linked")
	if err != nil || updated.ClientID != "device-two" || updated.Revision != 2 || updated.LastError == "" {
		t.Fatal("linked client identity not updated atomically", err)
	}
	account.UserID = 456
	if err := s.SavePlexAccount(ctx, account, 2, "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("silently switched Plex user", err)
	}
	if err := s.DisconnectPlexAccount(ctx, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale disconnect allowed", err)
	}
	if err := s.DisconnectPlexAccount(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexConnection(ctx, "manual"); err != nil {
		t.Fatal("manual connection deleted", err)
	}
	if _, err := s.Collection(ctx, collectionID); err != nil {
		t.Fatal("canonical collection deleted", err)
	}
	ids, err := s.PlexConnectionIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != "manual" {
		t.Fatal("linked cleanup failed", err)
	}
}
func TestAccountCommitRequiresAttemptBinding(t *testing.T) {
	s, _ := collectionFixture(t)
	ctx := context.Background()
	attempt := PlexAuthAttempt{ID: "attempt", ClientID: "initiating-device", Kid: "kid", PrivateCipher: []byte("private-cipher"), PINID: 42, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := s.BeginPlexAuth(ctx, "initiating-session", attempt); err != nil {
		t.Fatal(err)
	}
	account := PlexAccount{UserID: 123, Username: "plex-user", ClientID: "other-device", CredentialsCipher: []byte("cipher"), ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.SavePlexAccount(ctx, account, 0, attempt.ID, "initiating-session"); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("device mismatch accepted", err)
	}
	account.ClientID = attempt.ClientID
	if err := s.SavePlexAccount(ctx, account, 0, attempt.ID, "other-session"); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("session mismatch accepted", err)
	}
	if err := s.SavePlexAccount(ctx, account, 0, attempt.ID, "initiating-session"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "initiating-session", attempt.ID, false); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("committed attempt can be replayed", err)
	}
}

func TestLogoutRevokesPendingPlexAuthorisation(t *testing.T) {
	s, _ := collectionFixture(t)
	ctx := context.Background()
	if err := s.CreateAdmin(ctx, "admin", []byte("hash")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSession(ctx, "session", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	attempt := PlexAuthAttempt{ID: "logout-attempt", ClientID: "device", Kid: "kid", PrivateCipher: []byte("private"), PINID: 42, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	if err := s.BeginPlexAuth(ctx, "session", attempt); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession(ctx, "session"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlexAuthAttempt(ctx, "session", attempt.ID, false); !errors.Is(err, ErrAuthAttempt) {
		t.Fatal("logout left a usable authorisation attempt", err)
	}
}
