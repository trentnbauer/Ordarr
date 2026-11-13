package app

import (
	"context"
	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCompleteCatalogueScanAndFailureRetainsGeneration(t *testing.T) {
	a, _ := fixture(t)
	var fail atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case "/":
			io.WriteString(w, `{"MediaContainer":{"machineIdentifier":"machine","friendlyName":"Plex","version":"1"}}`)
		case "/library/sections/all":
			io.WriteString(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
		case "/library/sections/1/all":
			io.WriteString(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"1","type":"movie","title":"Movie"}]}}`)
		case "/library/metadata/1":
			io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","type":"movie","title":"Movie","Guid":[{"id":"tmdb://42"}],"Media":[{"Part":[{"key":"/library/parts/1/file","exists":true}]}]}]}}`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	ctx := context.Background()
	c := store.PlexConnection{ID: "one", Name: "Home", BaseURL: s.URL, ClientID: "client", TokenCipher: a.vault.Seal("scan-token", "plex:one:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1", AllowHTTP: true, Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}
	if err := a.store.SavePlexConnection(ctx, c, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.scanCatalogue(ctx, store.Job{ID: "generation-one", Target: "one"}); err != nil {
		t.Fatal(err)
	}
	items, _, err := a.store.Catalogue(ctx, "one")
	if err != nil || len(items) != 1 || !items[0].Playable || items[0].IDs["tmdb"] != "42" {
		t.Fatal(items, err)
	}
	fail.Store(true)
	if err = a.scanCatalogue(ctx, store.Job{ID: "failed-generation", Target: "one"}); err == nil {
		t.Fatal("failure ignored")
	}
	items, _, err = a.store.Catalogue(ctx, "one")
	if err != nil || len(items) != 1 {
		t.Fatal("last generation lost", err)
	}
}
