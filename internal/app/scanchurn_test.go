package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Downloads landing in Plex change the library while it is being paged. The scan must start that
// library's listing again instead of failing the whole sync, and must still refuse a listing
// that never settles.
func TestScanRetriesALibraryThatChangesWhilePaging(t *testing.T) {
	previous, previousPause := libraryListAttempts, libraryListPause
	libraryListAttempts, libraryListPause = 3, time.Millisecond
	t.Cleanup(func() { libraryListAttempts, libraryListPause = previous, previousPause })
	const items = plex.MediaPageSize + 20 // two pages
	var secondPages atomic.Int32
	var neverSettles atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			fmt.Fprint(w, `{"MediaContainer":{"machineIdentifier":"machine","friendlyName":"Plex","version":"1"}}`)
		case r.URL.Path == "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
		case r.URL.Path == "/library/sections/1/all":
			offset, _ := strconv.Atoi(r.Header.Get("X-Plex-Container-Start"))
			total := items
			if offset > 0 {
				// An import lands between page one and page two: the total grows on the first
				// listing, and (in the second scenario) on every listing.
				if n := secondPages.Add(1); n == 1 || neverSettles.Load() {
					total = items + int(n)
				}
			}
			list := []map[string]any{}
			for i := offset; i < min(offset+plex.MediaPageSize, total); i++ {
				list = append(list, map[string]any{"ratingKey": strconv.Itoa(i + 1), "type": "movie", "title": "Movie " + strconv.Itoa(i+1)})
			}
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"size": len(list), "offset": offset, "totalSize": total, "Metadata": list}})
		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			key := strings.TrimPrefix(r.URL.Path, "/library/metadata/")
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":%q,"type":"movie","title":"Movie %s","Guid":[{"id":"tmdb://%s"}],"Media":[{"Part":[{"key":"/p","exists":true}]}]}]}}`, key, key, key)
		default:
			http.Error(w, "unexpected "+r.URL.Path, 404)
		}
	}))
	defer s.Close()
	a, _ := fixture(t)
	ctx := context.Background()
	c := store.PlexConnection{ID: "one", Name: "Home", BaseURL: s.URL, ClientID: "client", TokenCipher: a.vault.Seal("scan-token", "plex:one:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1", AllowHTTP: true, Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}
	if err := a.store.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.scanCatalogue(ctx, store.Job{ID: "j1", Target: "one"}); err != nil {
		t.Fatalf("a change mid-listing must be retried, not fatal: %v", err)
	}
	got, _, err := a.store.Catalogue(ctx, "one")
	if err != nil || len(got) != items {
		t.Fatalf("catalogue has %d items, want %d (%v)", len(got), items, err)
	}
	// A library that keeps changing is still refused, and the last good catalogue is kept.
	secondPages.Store(0)
	neverSettles.Store(true)
	if err = a.scanCatalogue(ctx, store.Job{ID: "j2", Target: "one"}); err == nil {
		t.Fatal("a listing that never settles was accepted")
	}
	if got, _, err = a.store.Catalogue(ctx, "one"); err != nil || len(got) != items {
		t.Fatalf("last good catalogue lost: %d %v", len(got), err)
	}
}
