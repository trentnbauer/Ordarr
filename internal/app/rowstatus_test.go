package app

import (
	"testing"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/store"
)

func TestMissingRowStatuses(t *testing.T) {
	movie := store.Entry{ID: "m", Kind: "movie", Provider: "tmdb", ExternalID: "42", Title: "Movie"}
	episode := store.Entry{ID: "e", Kind: "episode", Provider: "tvdb", ExternalID: "7", Title: "Show"}
	radarr := func(snap arr.Snapshot) store.ArrConnection {
		return store.ArrConnection{ID: "r", Kind: "radarr", Name: "Radarr", ProfileID: 1, RootID: 1, Snapshot: snap}
	}
	var none map[string]store.AcquisitionRequest
	for _, c := range []struct {
		name     string
		arrs     []store.ArrConnection
		requests map[string]store.AcquisitionRequest
		entry    store.Entry
		auto     bool
		optedOut bool
		want     string
		request  bool // a "Request now" is offered
	}{
		{"no downloader connected", nil, none, movie, true, false, statusNotAsked, false},
		{"in the download queue", []store.ArrConnection{radarr(arr.Snapshot{Media: []arr.Media{{ID: 5, TMDB: 42}}, Queue: []arr.QueueItem{{ID: 1, MovieID: 5}}})}, none, movie, true, false, statusDownload, false},
		{"downloaded, waiting for Plex", []store.ArrConnection{radarr(arr.Snapshot{Media: []arr.Media{{ID: 5, TMDB: 42, HasFile: true}}})}, none, movie, true, false, statusPlexScan, false},
		{"added in Radarr, no file yet", []store.ArrConnection{radarr(arr.Snapshot{Media: []arr.Media{{ID: 5, TMDB: 42}}})}, none, movie, true, false, statusRequested, false},
		{"request recorded, not in snapshot yet", []store.ArrConnection{radarr(arr.Snapshot{})}, map[string]store.AcquisitionRequest{"r/42": {State: "completed"}}, movie, true, false, statusRequested, false},
		{"lookup failed", []store.ArrConnection{radarr(arr.Snapshot{})}, map[string]store.AcquisitionRequest{"r/42": {State: "failed", Message: "x"}}, movie, true, false, statusNotFound, true},
		{"will be requested automatically", []store.ArrConnection{radarr(arr.Snapshot{})}, none, movie, true, false, statusAuto, true},
		{"collection opted out", []store.ArrConnection{radarr(arr.Snapshot{})}, none, movie, true, true, statusNotAsked, true},
		{"automatic downloads off globally", []store.ArrConnection{radarr(arr.Snapshot{})}, none, movie, false, false, statusNotAsked, true},
		{"no downloader for this kind of entry", []store.ArrConnection{radarr(arr.Snapshot{})}, none, episode, true, false, statusNotFound, false},
	} {
		got := missingStatus(c.arrs, c.requests, c.entry, c.auto, c.optedOut)
		if got.Label != c.want || (got.RequestVia != "") != c.request {
			t.Errorf("%s: got %q (request via %q) want %q (request %v)", c.name, got.Label, got.RequestVia, c.want, c.request)
		}
	}
}
