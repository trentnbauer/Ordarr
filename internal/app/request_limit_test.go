package app

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trentnbauer/Ordarr/internal/arr"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// A request that is only waiting for the instance's request limit must not read "Requested".
func TestApprovedRequestWaitingOnTheLimitIsNotShownAsRequested(t *testing.T) {
	c := store.ArrConnection{ID: "r", Kind: "radarr", Name: "Radarr 1080", ProfileID: 1, RootID: 1, DailyLimit: 10}
	movie := store.Entry{ID: "m", Kind: "movie", Provider: "tmdb", ExternalID: "42", Title: "Movie"}
	status := func(state string, limit limitState) rowStatus {
		reqs := map[string]store.AcquisitionRequest{"r/42": {State: state}}
		return missingStatus([]store.ArrConnection{c}, reqs, movie, true, false, map[string]bool{"r": true}, nil, map[string]limitState{"r": limit})
	}
	opens := time.Now().Add(5 * time.Hour).Unix()
	full := limitState{Blocked: true, Opens: opens, Used: 10, Limit: 10}
	got := status("approved", full)
	if got.Label != statusLimit || !strings.Contains(got.Detail, "10 requests in the last 24 hours (limit 10)") || !strings.Contains(got.Detail, "switch the limit off") {
		t.Fatalf("approved while full: %+v", got)
	}
	for state, want := range map[string]string{
		"approved":  statusSending, // not full: it is about to go
		"running":   statusSending,
		"completed": statusRequested,
		"uncertain": statusRequested,
		"pending":   statusApproval,
	} {
		if got := status(state, limitState{}); got.Label != want {
			t.Errorf("%s: got %q want %q", state, got.Label, want)
		}
	}
	if got := status("running", full); got.Label != statusSending {
		t.Errorf("a request already being sent is not waiting on the limit: %q", got.Label)
	}
	if got := status("pending", limitState{}); !got.Warn {
		t.Error("a request waiting for approval needs the administrator")
	}
	// History says the same thing.
	row := requestRow(store.AcquisitionRequest{State: "approved", Origin: "manual", Title: "T"}, arrLabel{Name: "Radarr 1080", Kind: "Radarr", BlockedUntil: opens})
	if !strings.Contains(row.Result, "Waiting for Radarr 1080's request limit") || strings.Contains(row.Result, "Being sent") {
		t.Errorf("history: %q", row.Result)
	}
}

func TestRequestLimitsReportWhenTheLimitFreesUp(t *testing.T) {
	a, _ := fixture(t)
	ctx := context.Background()
	for _, c := range []store.ArrConnection{
		{ID: "a", Kind: "radarr", Name: "A", BaseURL: "http://x:1", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
		{ID: "b", Kind: "radarr", Name: "B", BaseURL: "http://x:2", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1},
	} {
		if err := a.store.CreateArr(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		c, _ := a.store.ArrConnection(ctx, id)
		if err := a.store.ArrPolicy(ctx, id, c.Revision, 2, id == "b", ""); err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 2; n++ {
			c, _ = a.store.ArrConnection(ctx, id)
			rid, _ := a.store.ProposeAcquisition(ctx, c, n, "M", "manual", true)
			r, _ := a.store.Acquisition(ctx, rid)
			if err := a.store.BeginAcquisition(ctx, r, c); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.store.DB.Exec("UPDATE acquisition_attempts SET created_at=? WHERE connection_id='a'", time.Now().Add(-20*time.Hour).Unix())
	conns, _ := a.store.ArrConnections(ctx)
	limits, err := a.requestLimits(ctx, conns)
	if err != nil {
		t.Fatal(err)
	}
	l, ok := limits["a"]
	if !ok || !l.Blocked || l.Used != 2 || l.Limit != 2 {
		t.Fatalf("a: %+v", l)
	}
	if opens := time.Until(time.Unix(l.Opens, 0)); opens < 3*time.Hour+50*time.Minute || opens > 4*time.Hour+10*time.Minute {
		t.Fatalf("the oldest request was 20 h ago, so the limit frees in about 4 h, got %v", opens)
	}
	if _, listed := limits["b"]; listed {
		t.Fatal("an instance with the limit off has no limit state")
	}
}

// The instance page can switch the limit off, and says so.
func TestInstancePageCanSwitchTheRequestLimitOff(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	ctx := context.Background()
	if err := a.store.CreateArr(ctx, store.ArrConnection{ID: "r", Kind: "radarr", Name: "Radarr 1080", BaseURL: "http://r:7878", KeyCipher: []byte("c"), ProfileID: 1, RootID: 1, Snapshot: arr.Snapshot{Profiles: []arr.Choice{{ID: 1, Name: "HD"}}, Roots: []arr.Choice{{ID: 2, Path: "/movies"}}}}); err != nil {
		t.Fatal(err)
	}
	c, _ := a.store.ArrConnection(ctx, "r")
	page := func() string {
		return request(h, "GET", "/settings/acquisition?id=r", nil, session, csrf).Body.String()
	}
	if body := page(); !strings.Contains(body, "/ 5</span>") || !strings.Contains(body, `name="no_limit"`) || strings.Contains(body, `name="no_limit" value="yes" checked`) {
		t.Fatal("the default limit should show as 5 with the box unticked")
	}
	form := url.Values{"csrf": {csrf.Value}, "revision": {strconv.Itoa(c.Revision)}, "profile": {"1"}, "root": {"2"}, "no_limit": {"yes"}}
	if w := request(h, "POST", "/settings/acquisition/r/policy", form, session, csrf); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	body := page()
	if c, _ = a.store.ArrConnection(ctx, "r"); !c.LimitOff || !strings.Contains(body, "/ no limit</span>") || !strings.Contains(body, `name="no_limit" value="yes" checked`) {
		t.Fatalf("limit off=%v; page does not say so", c.LimitOff)
	}
}
