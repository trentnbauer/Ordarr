package plex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestMediaPagesMoviesAndIndividualEpisodes(t *testing.T) {
	for _, kind := range []string{"movie", "show"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/library/sections/1/all" || r.Header.Get("X-Plex-Token") != testToken || strings.Contains(r.URL.String(), testToken) {
					t.Error("unsafe media request")
				}
				mediaType, number := "movie", "1"
				if kind == "show" {
					mediaType = "episode"
					number = "4"
				}
				if r.URL.Query().Get("type") != number || r.Header.Get("X-Plex-Container-Size") != "50" {
					t.Error("wrong media type/page size")
				}
				offset, err := strconv.Atoi(r.Header.Get("X-Plex-Container-Start"))
				if err != nil {
					t.Error(err)
				}
				metadata := []map[string]any{}
				for i := offset; i < min(offset+2, 4); i++ {
					m := map[string]any{"ratingKey": strconv.Itoa(i + 1), "type": mediaType, "title": "Indexed item " + strconv.Itoa(i+1), "Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/private/media/location.mkv"}}}}}
					if kind == "show" {
						m["grandparentTitle"] = "Series"
						m["grandparentRatingKey"] = "100"
						m["parentIndex"] = 0
						m["index"] = i + 1
					}
					metadata = append(metadata, m)
				}
				w.Header().Set("X-Plex-Container-Start", strconv.Itoa(offset))
				w.Header().Set("X-Plex-Container-Total-Size", "4")
				json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"size": strconv.Itoa(len(metadata)), "librarySectionID": "1", "Metadata": metadata}})
			}))
			defer server.Close()
			first, err := New().FetchMediaPage(context.Background(), server.URL, testToken, "client", true, Library{Key: "1", Kind: kind}, 0)
			if err != nil || len(first.Items) != 2 || !first.HasNext() {
				t.Fatal(first, err)
			}
			second, err := New().FetchMediaPage(context.Background(), server.URL, testToken, "client", true, Library{Key: "1", Kind: kind}, 2)
			if err != nil || second.HasNext() || second.Items[0].RatingKey != "3" {
				t.Fatal(second, err)
			}
			if kind == "show" && (first.Items[0].Season == nil || *first.Items[0].Season != 0 || first.Items[0].Episode == nil || *first.Items[0].Episode != 1) {
				t.Fatal("specials numbering lost")
			}
			data, _ := json.Marshal(first)
			if strings.Contains(string(data), "/private/media") {
				t.Fatal("source file paths retained")
			}
		})
	}
}
func TestMediaPageRejectsPartialInvalidAndReflectedData(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"missing container", `{}`},
		{"wrong offset", `{"MediaContainer":{"size":0,"offset":2,"totalSize":4,"Metadata":[]}}`},
		{"no progress", `{"MediaContainer":{"size":0,"offset":0,"totalSize":4,"Metadata":[]}}`},
		{"no total", `{"MediaContainer":{"size":0,"offset":0,"Metadata":[]}}`},
		{"wrong kind", `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"1","type":"show","title":"Series"}]}}`},
		{"wrong library", `{"MediaContainer":{"size":0,"librarySectionID":99,"Metadata":[]}}`},
		{"duplicate ID", `{"MediaContainer":{"size":2,"Metadata":[{"ratingKey":"1","type":"movie","title":"One"},{"ratingKey":"1","type":"movie","title":"Repeated"}]}}`},
		{"token reflection", `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"1","type":"movie","title":"` + testToken + `"}]}}`},
		{"size mismatch", `{"MediaContainer":{"size":2,"Metadata":[]}}`},
		{"count float", `{"MediaContainer":{"size":0.0,"Metadata":[]}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, test.body) }))
			defer s.Close()
			_, err := New().FetchMediaPage(context.Background(), s.URL, testToken, "client", true, Library{Key: "1", Kind: "movie"}, 0)
			if !errors.Is(err, ErrMediaPage) || strings.Contains(err.Error(), testToken) {
				t.Fatal("unsafe media page", err)
			}
		})
	}
}
func TestMediaPageBoundsEmptyResponseAndMissingEpisodeNumbers(t *testing.T) {
	for _, lib := range []Library{{Key: "../all", Kind: "movie"}, {Key: "1", Kind: "artist"}, {Key: "0", Kind: "show"}} {
		if _, err := New().FetchMediaPage(context.Background(), "http://localhost", testToken, "client", true, lib, 0); !errors.Is(err, ErrMediaPage) {
			t.Fatal("invalid library accepted")
		}
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"MediaContainer":{"size":1,"Metadata":[{"ratingKey":"1","type":"episode","title":"Unknown numbering"}]}}`)
	}))
	defer s.Close()
	page, err := New().FetchMediaPage(context.Background(), s.URL, testToken, "client", true, Library{Key: "1", Kind: "show"}, 0)
	if err != nil || page.Items[0].Season != nil || page.Items[0].Episode != nil {
		t.Fatal("missing numbering guessed", err)
	}
	if _, err = New().FetchMediaPage(context.Background(), s.URL, testToken, "client", true, Library{Key: "1", Kind: "show"}, -1); !errors.Is(err, ErrMediaPage) {
		t.Fatal("negative offset accepted")
	}
	if _, err = New().FetchMediaPage(context.Background(), s.URL, testToken, "client", true, Library{Key: "1", Kind: "show"}, 1); !errors.Is(err, ErrMediaPage) {
		t.Fatal("unpaginated response accepted at nonzero offset")
	}
}
