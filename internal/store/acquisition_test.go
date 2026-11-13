package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAcquisitionOptInBudgetDedupExclusionsAndRecovery(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	c := ArrConnection{ID: "arr", Kind: "radarr", Name: "Movies", BaseURL: "https://example.com", KeyCipher: []byte("cipher"), ProfileID: 1, RootID: 2}
	if err = s.CreateArr(ctx, c); err != nil {
		t.Fatal(err)
	}
	c, _ = s.ArrConnection(ctx, c.ID)
	if c.Automatic || !c.RequireApproval || c.DailyLimit != 5 {
		t.Fatal("unsafe defaults", c)
	}
	if err = s.ArrPolicy(ctx, c.ID, c.Revision, true, false, 1, "99"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.ArrConnection(ctx, c.ID)
	if _, err = s.ProposeAcquisition(ctx, c, 99, "Excluded", "automatic", true); err == nil {
		t.Fatal("exclusion ignored")
	}
	id, err := s.ProposeAcquisition(ctx, c, 42, "Movie", "manual", false)
	if err != nil {
		t.Fatal(err)
	}
	same, _ := s.ProposeAcquisition(ctx, c, 42, "Movie", "manual", true)
	if same != id {
		t.Fatal("duplicate request")
	}
	r, _ := s.Acquisition(ctx, id)
	if r.State != "pending" {
		t.Fatal("approval bypassed")
	}
	if err = s.BeginAcquisition(ctx, r, c); err == nil {
		t.Fatal("pending attempt started")
	}
	s.ApproveAcquisition(ctx, id)
	r, _ = s.Acquisition(ctx, id)
	if err = s.BeginAcquisition(ctx, r, c); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Acquisition(ctx, id)
	if r.State != "running" {
		t.Fatal("reservation not durable")
	}
	if err = s.BeginAcquisition(ctx, r, c); err == nil {
		t.Fatal("running request replayed")
	}
	next, _ := s.ProposeAcquisition(ctx, c, 43, "Another", "manual", true)
	r, _ = s.Acquisition(ctx, next)
	if err = s.BeginAcquisition(ctx, r, c); err == nil {
		t.Fatal("daily limit ignored")
	}
	var attempts int
	s.DB.QueryRow("SELECT count(*) FROM acquisition_attempts").Scan(&attempts)
	if attempts != 1 {
		t.Fatal(attempts)
	}
}
