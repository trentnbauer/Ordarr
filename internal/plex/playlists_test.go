package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The fixture stores independent playlist item IDs, including intentional repeats.
func TestPlaylistReconciliationRecoveryAndProtection(t *testing.T) {
	for _, failAt := range []int{0, 1, 2, 3, 4} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			items := []PlaylistItem{{"9", "101"}, {"2", "102"}, {"1", "103"}}
			next := 104
			writes := 0
			fail := failAt
			title := "Order [Ordarr:owned]"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if !strings.HasPrefix(path, "/playlists/50") {
					t.Errorf("unrelated path %s", path)
					http.Error(w, "bad", 400)
					return
				}
				if r.Method == "GET" {
					if path == "/playlists/50" {
						json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Metadata": []Playlist{{"50", title, "video", false}}}})
					} else {
						json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"size": len(items), "Metadata": items}})
					}
					return
				}
				writes++
				if writes == fail {
					http.Error(w, "temporary", 503)
					return
				}
				switch {
				case path == "/playlists/50/items" && r.Method == "PUT":
					uri := r.URL.Query().Get("uri")
					key := uri[strings.LastIndex(uri, "/")+1:]
					items = append(items, PlaylistItem{key, flexibleID(strconv.Itoa(next))})
					next++
				case strings.HasSuffix(path, "/move") && r.Method == "PUT":
					var id string
					fmt.Sscanf(path, "/playlists/50/items/%s", &id)
					id = strings.TrimSuffix(id, "/move")
					at := -1
					for i, item := range items {
						if string(item.ItemID) == id {
							at = i
						}
					}
					if at < 0 {
						t.Fatal("unknown item")
					}
					item := items[at]
					items = append(items[:at], items[at+1:]...)
					index := 0
					after := r.URL.Query().Get("after")
					if after != "" {
						for i, item := range items {
							if string(item.ItemID) == after {
								index = i + 1
							}
						}
					}
					items = append(items, PlaylistItem{})
					copy(items[index+1:], items[index:])
					items[index] = item
				case r.Method == "DELETE" && strings.HasPrefix(path, "/playlists/50/items/"):
					id := strings.TrimPrefix(path, "/playlists/50/items/")
					for i, item := range items {
						if string(item.ItemID) == id {
							items = append(items[:i], items[i+1:]...)
							break
						}
					}
				default:
					t.Errorf("unsafe write %s %s", r.Method, path)
					http.Error(w, "bad", 400)
				}
			}))
			defer server.Close()
			desired := []string{"1", "2", "1"}
			c := New()
			guard := func() error { return nil }
			err := c.ReconcilePlaylist(context.Background(), server.URL, "50", title, "machine", testToken, "client", desired, guard)
			if failAt > 0 && err == nil {
				t.Fatal("injected failure not reported")
			}
			fail = 0
			if err = c.ReconcilePlaylist(context.Background(), server.URL, "50", title, "machine", testToken, "client", desired, guard); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, item := range items {
				got = append(got, item.RatingKey)
			}
			if !reflect.DeepEqual(got, desired) {
				t.Fatal(got)
			}
			before := writes
			if err = c.ReconcilePlaylist(context.Background(), server.URL, "50", "unrelated", "machine", testToken, "client", desired, guard); err == nil || writes != before {
				t.Fatal("unrelated playlist modified")
			}
			if err = c.ReconcilePlaylist(context.Background(), server.URL, "50", title, "machine", testToken, "client", nil, guard); err == nil || writes != before {
				t.Fatal("empty sequence cleared playlist")
			}
		})
	}
}
