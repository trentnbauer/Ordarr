package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The sidebar shows a spinner next to Activity while a scan, sync or request is running or waiting.
func TestActivitySpinnerShowsWhileJobsRun(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	spinnerHidden := func() bool {
		t.Helper()
		body := request(h, "GET", "/", nil, session, csrf).Body.String()
		i := strings.Index(body, `id="activity-spinner"`)
		if i < 0 {
			t.Fatal("no spinner element in the sidebar")
		}
		tag := body[i : i+strings.Index(body[i:], ">")]
		return strings.Contains(tag, "hidden")
	}
	status := func() (bool, string) {
		t.Helper()
		w := request(h, "GET", "/activity/busy", nil, session, csrf)
		var out struct {
			Busy  bool
			Label string
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("status endpoint", w.Code, w.Body.String())
		}
		return out.Busy, out.Label
	}
	if !spinnerHidden() {
		t.Fatal("spinner visible while idle")
	}
	if busy, _ := status(); busy {
		t.Fatal("busy while idle")
	}
	// A queued job is "waiting"; a running one is named.
	id, err := a.store.QueueJob(ctx, "scan", "plex-1")
	if err != nil {
		t.Fatal(err)
	}
	if spinnerHidden() {
		t.Fatal("spinner hidden with a job waiting")
	}
	if busy, label := status(); !busy || label != "Library scan waiting" {
		t.Fatalf("queued: %v %q", busy, label)
	}
	if _, err = a.store.DB.Exec("UPDATE jobs SET state='running' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.QueueJob(ctx, "sync", "dest-1"); err != nil {
		t.Fatal(err)
	}
	if busy, label := status(); !busy || label != "Library scan running · 1 more waiting" {
		t.Fatalf("running: %v %q", busy, label)
	}
	// Finished jobs stop the spinner.
	if _, err = a.store.DB.Exec("UPDATE jobs SET state='succeeded'"); err != nil {
		t.Fatal(err)
	}
	if !spinnerHidden() {
		t.Fatal("spinner still visible after the jobs finished")
	}
	// The page may poll its own status endpoint (the CSP allows same-origin requests only).
	w := request(h, "GET", "/", nil, session, csrf)
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") {
		t.Fatal("CSP blocks the status poll", csp)
	}
}
