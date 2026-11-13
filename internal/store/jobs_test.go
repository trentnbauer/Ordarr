package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDurableJobsRecoverInterruptedAndDeduplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.QueueJob(ctx, "scan", "server")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := s.QueueJob(ctx, "scan", "server")
	if again != id {
		t.Fatal("not deduplicated")
	}
	j, err := s.ClaimJob(ctx)
	if err != nil || j.ID != id {
		t.Fatal(j, err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.RecoverJobs(ctx); err != nil {
		t.Fatal(err)
	}
	j, err = s.ClaimJob(ctx)
	if err != nil || j.ID != id {
		t.Fatal("interrupted job lost", err)
	}
	if err = s.FinishJob(ctx, id, "succeeded", "done"); err != nil {
		t.Fatal(err)
	}
	j, _ = s.Job(ctx, id)
	if j.State != "succeeded" {
		t.Fatal(j)
	}
}
