package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Stopping Ordarr mid-sync is not a failed sync. It was recorded as one, and the job then ran again on the
// next start and succeeded, so one job had both a "Failed" and a "Synced" record.
func TestAnInterruptedSyncIsNotRecordedAsFailed(t *testing.T) {
	a, _ := fixture(t)
	ctx := context.Background()
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), MachineID: "m", ServerName: "Plex", ServerVersion: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	collection, _ := a.store.ImportCollection(ctx, store.Collection{Title: "T", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	dest, _ := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0)
	d, _ := a.store.Destination(ctx, dest)
	runs := func() int { r, _ := a.store.SyncRuns(ctx, collection); return len(r) }

	cancelled, cancel := context.WithCancel(ctx)
	cancel() // Ordarr is shutting down
	a.recordSyncFailure(cancelled, d, "job", plex.ErrUnavailable, nil)
	if runs() != 0 {
		t.Fatal("an interrupted sync was recorded as failed")
	}
	a.recordSyncFailure(ctx, d, "job", nil, nil)
	if runs() != 0 {
		t.Fatal("a successful sync was recorded as failed")
	}
	// A real failure, and a timeout, still are.
	a.recordSyncFailure(ctx, d, "job", plex.ErrAuth, nil)
	timedOut, stop := context.WithTimeout(ctx, time.Nanosecond)
	defer stop()
	<-timedOut.Done()
	a.recordSyncFailure(timedOut, d, "job", context.DeadlineExceeded, nil)
	if runs() != 2 {
		t.Fatalf("real failures must still be recorded: %d", runs())
	}
}

// Activity lists a job when it finished, a playlist sync says how long the playlist is (not how many
// library items were scanned), and a scan still says how many items it read.
func TestActivityShowsFinishTimesAndPlaylistLengths(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "plex-1", Name: "Home Plex", BaseURL: "https://plex.example:32400", ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:plex-1:token"), MachineID: "m", ServerName: "Plex", ServerVersion: "1"}, 0)
	collection, _ := a.store.ImportCollection(ctx, store.Collection{Title: "Star Wars", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	dest, _ := a.store.CreateDestination(ctx, collection, "plex-1", "authoritative", 0)
	started := time.Date(2026, 11, 21, 22, 22, 0, 0, time.Local)
	finished := time.Date(2026, 11, 21, 22, 31, 0, 0, time.Local)
	sj, _ := a.store.QueueJob(ctx, "sync", dest)
	a.store.DB.Exec("UPDATE jobs SET state='succeeded',progress=28,message=?,created_at=?,finished_at=? WHERE id=?", "Playlist up to date · 28 items (Plex was scanned first)", started.Unix(), finished.Unix(), sj)
	cj, _ := a.store.QueueJob(ctx, "scan", "plex-1")
	a.store.DB.Exec("UPDATE jobs SET state='succeeded',progress=11718,message='Completed',created_at=?,finished_at=? WHERE id=?", started.Unix(), finished.Unix(), cj)
	// An old sync job from before this change still carries the library count as its progress.
	oj, _ := a.store.QueueJob(ctx, "sync", dest+"-old")
	a.store.DB.Exec("UPDATE jobs SET state='succeeded',progress=11718,message='Completed',created_at=?,finished_at=? WHERE id=?", started.Unix()-3600, finished.Unix()-3600, oj)
	body := request(h, "GET", "/jobs", nil, session, csrf).Body.String()
	if !strings.Contains(body, "Playlist up to date · 28 items") {
		t.Error("the sync does not say how long the playlist is")
	}
	if !strings.Contains(body, "Completed · 11,718 items") {
		t.Error("a library scan should still say how many items it read")
	}
	if strings.Count(body, "11,718 items") != 1 {
		t.Errorf("a playlist sync showed the library scan count: %d occurrences", strings.Count(body, "11,718 items"))
	}
	if !strings.Contains(body, clock(finished.Unix())) {
		t.Errorf("finish time %q missing", clock(finished.Unix()))
	}
	// The collection's History lists the same sync at the same (finish) time.
	hist := request(h, "GET", "/collections/"+collection+"?tab=history", nil, session, csrf).Body.String()
	if !strings.Contains(hist, clock(finished.Unix())) || strings.Contains(hist, clock(started.Unix())+"</span><span class=\"what") {
		t.Error("History and Activity must list a sync under the same time")
	}
}

// A scan that finds Plex changing underneath it is tried again before anyone hears about it.
func TestScanTriesAgainWhenPlexChangedUnderIt(t *testing.T) {
	previous, previousPause := scanRetries, scanRetryPause
	scanRetries, scanRetryPause = 3, time.Millisecond
	t.Cleanup(func() { scanRetries, scanRetryPause = previous, previousPause })
	var details atomic.Int32
	var wrongFor atomic.Int32 // how many detail requests answer with the wrong kind of item
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			fmt.Fprint(w, `{"MediaContainer":{"machineIdentifier":"machine","friendlyName":"Plex","version":"1"}}`)
		case r.URL.Path == "/library/sections/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","type":"movie"}]}}`)
		case r.URL.Path == "/library/sections/1/all":
			fmt.Fprint(w, `{"MediaContainer":{"size":1,"offset":0,"totalSize":1,"Metadata":[{"ratingKey":"1","type":"movie","title":"Movie"}]}}`)
		case r.URL.Path == "/library/metadata/1":
			kind := "movie"
			if details.Add(1) <= wrongFor.Load() {
				kind = "episode" // the item changed under the scan
			}
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","type":%q,"title":"Movie","Guid":[{"id":"tmdb://1"}],"Media":[{"Part":[{"key":"/library/parts/1/file","exists":true}]}]}]}}`, kind)
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()
	a, _ := fixture(t)
	ctx := context.Background()
	if err := a.store.SavePlexConnection(ctx, store.PlexConnection{ID: "one", Name: "Home", BaseURL: srv.URL, AllowHTTP: true, ClientID: "c", TokenCipher: a.vault.Seal("t", "plex:one:token"), MachineID: "machine", ServerName: "Plex", ServerVersion: "1", Libraries: []plex.Library{{Key: "1", Title: "Movies", Kind: "movie"}}}, 0); err != nil {
		t.Fatal(err)
	}
	a.store.DB.Exec("UPDATE plex_connections SET checked_at=?", time.Now().Add(-7*24*time.Hour).Unix())
	wrongFor.Store(1) // the first attempt hits a changed item, the second is fine
	if err := a.scanWithRetry(ctx, store.Job{ID: "j", Target: "one"}); err != nil {
		t.Fatalf("a scan that only met a change once must succeed: %v", err)
	}
	if got, _, err := a.store.Catalogue(ctx, "one"); err != nil || len(got) != 1 {
		t.Fatalf("catalogue: %d %v", len(got), err)
	}
	// A successful scan means the server was just reached: "last check" must not say days ago.
	c, _ := a.store.PlexConnection(ctx, "one")
	if time.Since(time.Unix(c.CheckedAt, 0)) > time.Minute {
		t.Fatalf("last check is still %v old after a successful scan", time.Since(time.Unix(c.CheckedAt, 0)))
	}
	// Changing every time is still reported, in words the reader can act on.
	wrongFor.Store(100)
	err := a.scanWithRetry(ctx, store.Job{ID: "j2", Target: "one"})
	if !errors.Is(err, plex.ErrMediaChanged) {
		t.Fatalf("a scan that never settles must still fail: %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, "first page") || strings.Contains(msg, "paging") || !strings.Contains(msg, "downloads") {
		t.Fatalf("the message must not be internal wording: %q", msg)
	}
}

// Following a collection fetches it once, outside the job system; that fetch is logged so History
// has a source check to show straight away.
func TestFollowingACollectionIsLoggedAsItsFirstSourceCheck(t *testing.T) {
	a, h := fixture(t)
	fakeSources(t, a, map[string]string{"tiny.json": tinyOrder})
	session, csrf := setup(t, h)
	w := request(h, "POST", "/add", url.Values{"csrf": {csrf.Value}, "source": {"community"}, "path": {"tiny.json"}, "policy": {"automatic"}}, session, csrf)
	if w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := strings.TrimSuffix(strings.TrimPrefix(w.Header().Get("Location"), "/collections/"), "?created=1")
	body := request(h, "GET", "/collections/"+id+"?tab=history", nil, session, csrf).Body.String()
	if !strings.Contains(body, "Source check") || !strings.Contains(body, "Source applied.") {
		t.Fatal("the first fetch is not in History")
	}
}
