package plex

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetadataProviderIDsAndPlayableFileChecks(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("checkFiles") != "1" {
			t.Error("not a verified read")
		}
		io.WriteString(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","type":"movie","title":"Movie","Guid":[{"id":"tmdb://42"},{"id":"imdb://tt1234567"}],"Media":[{"Part":[{"key":"/library/parts/1/file.mkv","exists":true,"accessible":true}]}]}]}}`)
	}))
	defer s.Close()
	item, err := New().Details(context.Background(), s.URL, testToken, "client", true, "1")
	if err != nil || item.IDs["tmdb"] != "42" || !item.Playable {
		t.Fatal(item, err)
	}
	conflicting := []struct {
		ID string `json:"id"`
	}{{"tmdb://1"}, {"tmdb://2"}}
	if len(providerIDs("", conflicting)) != 0 {
		t.Fatal("conflicting IDs accepted")
	}
}
