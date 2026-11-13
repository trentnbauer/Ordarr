package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSubscriptionApprovalIdentityAndFork(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	sub, err := s.CreateSubscription(ctx, "https://example.com/order.yaml", "approval")
	if err != nil {
		t.Fatal(err)
	}
	c := Collection{Title: "Order", Entries: []Entry{{ID: "a", Kind: "movie", Title: "A", Provider: "tmdb", ExternalID: "1"}, {ID: "b", Kind: "movie", Title: "B", Provider: "tmdb", ExternalID: "2"}, {ID: "again", Kind: "movie", Title: "A", Provider: "tmdb", ExternalID: "1"}}}
	data := []byte("version-one")
	if err = s.ReceiveSource(ctx, sub, data, c, "etag", ""); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Collection(ctx, sub.CollectionID)
	if len(before.Entries) != 0 {
		t.Fatal("applied without approval")
	}
	sub, _ = s.Subscription(ctx, sub.ID)
	if err = s.ApproveSource(ctx, sub, c); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Collection(ctx, sub.CollectionID)
	if len(first.Entries) != 3 {
		t.Fatal(first)
	}
	if err = s.ChangeCollection(ctx, first.ID, first.Revision, CollectionChange{Action: "metadata", Title: "Local"}); err == nil {
		t.Fatal("local edits accepted")
	}
	fork, err := s.ForkCollection(ctx, first.ID, first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	fc, _ := s.Collection(ctx, fork)
	if err = s.ChangeCollection(ctx, fork, fc.Revision, CollectionChange{Action: "metadata", Title: "Local"}); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Subscription(ctx, sub.ID)
	c.Entries = []Entry{c.Entries[2], c.Entries[0], c.Entries[1]}
	if err = s.ReceiveSource(ctx, sub, []byte("version-two"), c, "etag2", ""); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Subscription(ctx, sub.ID)
	if err = s.ApproveSource(ctx, sub, c); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Collection(ctx, first.ID)
	if after.Entries[0].ID != first.Entries[2].ID || after.Entries[1].ID != first.Entries[0].ID {
		t.Fatal("identities lost on reorder")
	}
	if err = s.SourceFailure(ctx, sub); err != nil {
		t.Fatal(err)
	}
	retained, _ := s.Collection(ctx, first.ID)
	if len(retained.Entries) != 3 {
		t.Fatal("failure lost valid source")
	}
}
func TestSubscriptionPollingBoundsAndPersistence(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	sub, err := s.CreateSubscription(ctx, "https://example.com/order", "approval")
	if err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Subscription(ctx, sub.ID)
	if sub.IntervalMinutes != 60 {
		t.Fatal(sub)
	}
	if err = s.SubscriptionPolicy(ctx, sub.ID, "approval", sub.Revision, 4); err == nil {
		t.Fatal("tiny interval accepted")
	}
	if err = s.SubscriptionPolicy(ctx, sub.ID, "approval", sub.Revision, 15); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Subscription(ctx, sub.ID)
	if sub.IntervalMinutes != 15 {
		t.Fatal(sub)
	}
	if err = s.DeferSubscription(ctx, sub.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Subscription(ctx, sub.ID)
	if after.NextRun <= sub.NextRun {
		t.Fatal("not scheduled")
	}
}
