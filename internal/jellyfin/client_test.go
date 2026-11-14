package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

const userID = "11111111111111111111111111111111"
const libID = "22222222222222222222222222222222"
const seriesID = "33333333333333333333333333333333"

func fixture(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("remote write %s", r.Method)
			http.Error(w, "bad", 400)
			return
		}
		if !strings.Contains(r.Header.Get("Authorization"), `Token="user-token"`) {
			t.Error("missing auth header")
		}
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}
func TestJellyfinUserLibraryPaginationAndSeriesIdentity(t *testing.T) {
	showCalls := 0
	s := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch r.URL.Path {
		case "/System/Info/Public":
			out = map[string]any{"Id": "server", "ServerName": "Home", "Version": "10.11", "ProductName": "Jellyfin"}
		case "/Users/Me":
			out = map[string]any{"Id": userID, "Name": "Owner", "Policy": map[string]any{"IsDisabled": false, "EnableMediaPlayback": true}}
		case "/UserViews":
			if r.URL.Query().Get("userId") != userID {
				t.Error("unscoped library discovery")
			}
			out = map[string]any{"Items": []any{map[string]any{"Id": libID, "Name": "Videos", "CollectionType": "unknown"}}, "TotalRecordCount": 1, "StartIndex": 0}
		case "/Items/" + seriesID:
			showCalls++
			out = map[string]any{"Id": seriesID, "Type": "Series", "ProviderIds": map[string]string{"Tvdb": "77"}}
		case "/Items":
			q := r.URL.Query()
			if q.Get("userId") != userID || q.Get("parentId") != libID || q.Get("sortBy") != "SortName" || q.Get("includeItemTypes") != "Movie,Episode" {
				t.Error("unscoped query", q)
			}
			offset, _ := strconv.Atoi(q.Get("startIndex"))
			count := 100
			if offset == 100 {
				count = 1
			}
			items := []any{}
			for i := 0; i < count; i++ {
				items = append(items, map[string]any{"Id": fmt.Sprintf("%032x", offset+i+100), "Name": "Episode", "Type": "Episode", "SeriesId": seriesID, "ParentIndexNumber": 0, "IndexNumber": offset + i + 1, "ProviderIds": map[string]string{"Tvdb": "WRONG-EPISODE-ID"}, "Path": "/private/never-store"})
			}
			out = map[string]any{"Items": items, "TotalRecordCount": 101, "StartIndex": offset}
		default:
			t.Errorf("unknown path %s", r.URL.Path)
			http.Error(w, "bad", 400)
			return
		}
		json.NewEncoder(w).Encode(out)
	})
	client := New()
	defer client.Close()
	snapshot, err := client.Inspect(context.Background(), s.URL, "user-token")
	if err != nil || snapshot.UserID != userID || len(snapshot.Libraries) != 1 {
		t.Fatal(snapshot, err)
	}
	series := map[string]map[string]string{}
	first, err := client.FetchPage(context.Background(), s.URL, "user-token", userID, libID, 0, series)
	if err != nil || len(first.Items) != 100 {
		t.Fatal(first, err)
	}
	last, err := client.FetchPage(context.Background(), s.URL, "user-token", userID, libID, 100, series)
	if err != nil || len(last.Items) != 1 || showCalls != 1 {
		t.Fatal(last, showCalls, err)
	}
	if last.Items[0].SeriesIDs["tvdb"] != "77" || last.Items[0].IDs["tvdb"] != "" {
		t.Fatal("episode ID substituted for series identity")
	}
	encoded, _ := json.Marshal(last)
	if strings.Contains(string(encoded), "private") {
		t.Fatal("file path persisted")
	}
}
func TestJellyfinRejectsErrorsRedirectsAndInconsistentPages(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	for _, mode := range []string{"auth", "redirect", "wrong-offset", "missing-total", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "auth":
					http.Error(w, "user-token", 401)
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
				case "wrong-offset":
					w.Write([]byte(`{"Items":[],"TotalRecordCount":0,"StartIndex":1}`))
				case "missing-total":
					w.Write([]byte(`{"Items":[],"StartIndex":0}`))
				case "duplicate":
					item := map[string]any{"Id": libID, "Name": "Movie", "Type": "Movie"}
					json.NewEncoder(w).Encode(map[string]any{"Items": []any{item, item}, "TotalRecordCount": 2, "StartIndex": 0})
				}
			})
			client := New()
			defer client.Close()
			_, err := client.FetchPage(context.Background(), s.URL, "user-token", userID, libID, 0, nil)
			if err == nil || strings.Contains(err.Error(), "user-token") {
				t.Fatal("unsafe error", err)
			}
		})
	}
	if targetCalls != 0 {
		t.Fatal("credentials forwarded through redirect")
	}
	if _, ok := ID("../Users/Me"); ok {
		t.Fatal("invalid ID accepted")
	}
}
func TestJellyfinRejectsDisabledAndUnboundUsers(t *testing.T) {
	for _, policy := range []any{nil, map[string]any{"IsDisabled": true, "EnableMediaPlayback": true}, map[string]any{"IsDisabled": false}} {
		s := fixture(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/System/Info/Public" {
				w.Write([]byte(`{"Id":"server","ServerName":"Home","Version":"10","ProductName":"Jellyfin"}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"Id": userID, "Name": "Owner", "Policy": policy})
		})
		client := New()
		_, err := client.Inspect(context.Background(), s.URL, "user-token")
		client.Close()
		if err != ErrAuth {
			t.Fatal(err)
		}
	}
}
