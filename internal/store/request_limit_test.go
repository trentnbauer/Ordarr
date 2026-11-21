package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRequestLimitCanBeSwitchedOff(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	if err = s.CreateArr(ctx, ArrConnection{ID: "arr", Kind: "radarr", Name: "Movies", BaseURL: "https://example.com", KeyCipher: []byte("c"), ProfileID: 1, RootID: 2}); err != nil {
		t.Fatal(err)
	}
	conn := func() ArrConnection { c, _ := s.ArrConnection(ctx, "arr"); return c }
	send := func(id int) error {
		t.Helper()
		c := conn()
		rid, err := s.ProposeAcquisition(ctx, c, id, "Movie", "manual", true)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := s.Acquisition(ctx, rid)
		return s.BeginAcquisition(ctx, r, c)
	}
	// Limit of 1: the first goes out, the second waits.
	if err = s.ArrPolicy(ctx, "arr", conn().Revision, 1, false, ""); err != nil {
		t.Fatal(err)
	}
	if err = send(1); err != nil {
		t.Fatal(err)
	}
	if err = send(2); err == nil {
		t.Fatal("a request past the limit was sent")
	}
	oldest, err := s.OldestAttemptSince(ctx, "arr", time.Now().Add(-24*time.Hour))
	if err != nil || oldest == 0 || time.Since(time.Unix(oldest, 0)) > time.Minute {
		t.Fatalf("oldest attempt: %d %v", oldest, err)
	}
	// Limit off: requests go out regardless, and the number is neither validated nor lost.
	if err = s.ArrPolicy(ctx, "arr", conn().Revision, 0, true, ""); err != nil {
		t.Fatalf("switching the limit off must not need a valid number: %v", err)
	}
	if c := conn(); !c.LimitOff || c.DailyLimit != 1 {
		t.Fatalf("limit off=%v limit=%d (the number should be kept for later)", c.LimitOff, c.DailyLimit)
	}
	for id := 2; id <= 4; id++ {
		if err = send(id); err != nil {
			t.Fatalf("request %d blocked although the limit is off: %v", id, err)
		}
	}
	// Switching it back on enforces the (kept) number again; an invalid number is still refused.
	if err = s.ArrPolicy(ctx, "arr", conn().Revision, 0, false, ""); err == nil {
		t.Fatal("0 accepted as a limit that is on")
	}
	if err = s.ArrPolicy(ctx, "arr", conn().Revision, 3, false, ""); err != nil {
		t.Fatal(err)
	}
	if err = send(5); err == nil {
		t.Fatal("limit not enforced after switching it back on (4 attempts already counted)")
	}
}

// Changing the limit must not strand requests that were already approved and waiting; changing where
// requests go (profile or folder) must, so they are approved again.
func TestWaitingRequestsSurviveALimitChangeButNotAChangeOfDestination(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	if err = s.CreateArr(ctx, ArrConnection{ID: "arr", Kind: "radarr", Name: "Movies", BaseURL: "https://example.com", KeyCipher: []byte("c"), ProfileID: 1, RootID: 2}); err != nil {
		t.Fatal(err)
	}
	conn := func() ArrConnection { c, _ := s.ArrConnection(ctx, "arr"); return c }
	rid, _ := s.ProposeAcquisition(ctx, conn(), 7, "Movie", "manual", true)
	// Same destination, new limit (the handler also re-saves the unchanged profile and folder).
	if err = s.ArrPolicy(ctx, "arr", conn().Revision, 3, false, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ConfigureArrChoices(ctx, "arr", 1, 2); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Acquisition(ctx, rid)
	if r.ConnectionRevision != conn().Revision {
		t.Fatalf("a waiting request was left on revision %d, connection is on %d", r.ConnectionRevision, conn().Revision)
	}
	if err = s.BeginAcquisition(ctx, r, conn()); err != nil {
		t.Fatalf("the waiting request could not be sent after the limit changed: %v", err)
	}
	// A different folder means the old approval no longer applies.
	rid2, _ := s.ProposeAcquisition(ctx, conn(), 8, "Other", "manual", true)
	if err = s.ConfigureArrChoices(ctx, "arr", 1, 9); err != nil {
		t.Fatal(err)
	}
	r2, _ := s.Acquisition(ctx, rid2)
	if err = s.BeginAcquisition(ctx, r2, conn()); err == nil {
		t.Fatal("a request approved for the old folder was sent to the new one")
	}
}
