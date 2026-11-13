package arr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocumentedMovieAndWholeSeriesRequests(t *testing.T) {
	for _, kind := range []string{"radarr", "sonarr"} {
		t.Run(kind, func(t *testing.T) {
			posts := 0
			exists := false
			var payload map[string]any
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Api-Key") != "fixture-secret" {
					t.Error("bad key")
				}
				resource := "movie"
				provider := "tmdbId"
				if kind == "sonarr" {
					resource = "series"
					provider = "tvdbId"
				}
				media := map[string]any{"id": 7, "title": "Confirmed", "titleSlug": "confirmed", provider: 42, "seasons": []any{map[string]any{"seasonNumber": 1, "monitored": false}}}
				var out any
				switch r.URL.Path {
				case "/api/v3/system/status":
					out = map[string]any{"appName": kind, "version": "5"}
				case "/api/v3/qualityprofile":
					out = []any{map[string]any{"id": 1, "name": "HD"}}
				case "/api/v3/rootfolder":
					out = []any{map[string]any{"id": 2, "path": "/media"}}
				case "/api/v3/queue":
					out = map[string]any{"page": 1, "pageSize": 100, "totalRecords": 0, "records": []any{}}
				case "/api/v3/" + resource + "/lookup":
					if !strings.Contains(r.URL.Query().Get("term"), ":42") {
						t.Error("fuzzy lookup")
					}
					out = []any{media}
				case "/api/v3/" + resource:
					if r.Method == "POST" {
						posts++
						if json.NewDecoder(r.Body).Decode(&payload) != nil {
							t.Fatal("bad payload")
						}
						exists = true
						out = media
					} else if exists {
						out = []any{media}
					} else {
						out = []any{}
					}
				default:
					t.Errorf("unsafe endpoint %s", r.URL.Path)
					http.Error(w, "bad", 400)
					return
				}
				json.NewEncoder(w).Encode(out)
			}))
			defer s.Close()
			c := New()
			conn := Connection{kind, s.URL, "fixture-secret"}
			if _, err := c.Add(context.Background(), conn, 42, 1, 999); err == nil || posts != 0 {
				t.Fatal("invalid root caused write")
			}
			if _, err := c.Add(context.Background(), conn, 42, 1, 2); err != nil {
				t.Fatal(err)
			}
			if posts != 1 || payload["qualityProfileId"] != float64(1) || payload["rootFolderPath"] != "/media" {
				t.Fatal(payload)
			}
			options := payload["addOptions"].(map[string]any)
			if kind == "sonarr" && (options["monitor"] != "all" || options["searchForMissingEpisodes"] != true) {
				t.Fatal("scope not explicit", payload)
			}
			if _, err := c.Add(context.Background(), conn, 42, 1, 2); err != nil || posts != 1 {
				t.Fatal("retry duplicated request", err)
			}
		})
	}
}
func TestArrRejectsAmbiguityAndRedactsAPIResponse(t *testing.T) {
	for _, body := range []string{`[{"tmdbId":42,"title":"A","titleSlug":"a"},{"tmdbId":42,"title":"B","titleSlug":"b"}]`, `[{"tmdbId":43,"title":"Wrong","titleSlug":"wrong"}]`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if _, err := New().Lookup(context.Background(), Connection{"radarr", s.URL, "secret"}, 42); err == nil {
			t.Fatal("ambiguous identity accepted")
		}
		s.Close()
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "secret-key-and-internal-body", 401) }))
	defer s.Close()
	_, err := New().Inspect(context.Background(), Connection{"radarr", s.URL, "secret-key-and-internal-body"})
	if err == nil || strings.Contains(err.Error(), "secret-key-and-internal-body") {
		t.Fatal("error leak", err)
	}
}
