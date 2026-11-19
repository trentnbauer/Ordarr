package community

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogueListsValidFilesAndCaches(t *testing.T) {
	mcu, err := os.ReadFile("testdata/mcu-main-timeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	fail := false
	c := New(DefaultRepository, "main")
	c.Fetch = func(ctx context.Context, u string) ([]byte, error) {
		calls.Add(1)
		if fail {
			return nil, errors.New("offline")
		}
		switch u {
		case "https://api.github.com/repos/trentnbauer/OrdarrCollections/git/trees/main?recursive=1":
			return []byte(`{"tree":[{"path":"README.md","type":"blob"},{"path":".github/x.yaml","type":"blob"},{"path":"mcu-main-timeline.yaml","type":"blob"},{"path":"broken.yaml","type":"blob"},{"path":"sub dir/tiny.json","type":"blob"}]}`), nil
		case "https://raw.githubusercontent.com/trentnbauer/OrdarrCollections/main/mcu-main-timeline.yaml":
			return mcu, nil
		case "https://raw.githubusercontent.com/trentnbauer/OrdarrCollections/main/broken.yaml":
			return []byte("version: 2"), nil
		case "https://raw.githubusercontent.com/trentnbauer/OrdarrCollections/main/sub%20dir/tiny.json":
			return []byte(`{"version":1,"title":"A tiny order","entries":[{"id":"a","kind":"movie","title":"Film","provider":"tmdb","external_id":"1"}]}`), nil
		}
		t.Errorf("unexpected fetch %s", u)
		return nil, errors.New("unexpected")
	}
	orders, err := c.Orders(context.Background())
	if err != nil || len(orders) != 2 {
		t.Fatalf("orders %+v %v", orders, err)
	}
	if orders[0].Title != "A tiny order" || orders[0].Movies != 1 || orders[0].Path != "sub dir/tiny.json" {
		t.Fatalf("tiny order %+v", orders[0])
	}
	m := orders[1]
	if !strings.HasPrefix(m.Title, "Marvel") || m.Entries < 50 || m.Movies == 0 || m.Episodes == 0 || m.Series == 0 || m.Entries != m.Movies+m.Episodes {
		t.Fatalf("mcu summary %+v", m)
	}
	before := calls.Load()
	if _, err = c.Orders(context.Background()); err != nil || calls.Load() != before {
		t.Fatal("cache not used", err)
	}
	// A failed refresh keeps the last good list.
	c.fetched = time.Now().Add(-2 * time.Hour)
	fail = true
	if again, err := c.Orders(context.Background()); err != nil || len(again) != 2 {
		t.Fatal("last good list lost", err)
	}
	if _, err = c.Order(context.Background(), "missing.yaml"); err == nil {
		t.Fatal("unknown path accepted")
	}
}

func TestCatalogueUnavailable(t *testing.T) {
	c := New(DefaultRepository, "main")
	c.Fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	if _, err := c.Orders(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if !ValidRepository("trentnbauer/OrdarrCollections") || ValidRepository("../x") || ValidRepository("a/b/c") || ValidBranch("a/../b") || !ValidBranch("main") {
		t.Fatal("repository validation")
	}
}
