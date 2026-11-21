package plex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakePlaylists is one Plex playlist whose title and description can be edited, with switches to
// simulate a Plex that accepts a write but does not apply it.
type fakePlaylists struct {
	mu                         sync.Mutex
	title, summary             string
	ignoreSummary, ignoreTitle bool
	puts                       []string
	posterBody                 []byte
	posterType                 string
	posters                    int
}

func (f *fakePlaylists) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == "GET" && r.URL.Path == "/playlists/50":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Metadata": []map[string]any{{"ratingKey": "50", "title": f.title, "summary": f.summary, "playlistType": "video", "smart": false}}}})
		case r.Method == "GET" && r.URL.Path == "/playlists":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"size": 1, "Metadata": []map[string]any{{"ratingKey": "50", "title": f.title, "summary": f.summary, "playlistType": "video", "smart": false}}}})
		case r.Method == "PUT" && r.URL.Path == "/playlists/50":
			q := r.URL.Query()
			f.puts = append(f.puts, r.URL.RawQuery)
			if v := q.Get("summary"); v != "" && !f.ignoreSummary {
				f.summary = v
			}
			if v := q.Get("title"); v != "" && !f.ignoreTitle {
				f.title = v
			}
			w.Write([]byte(`{}`))
		case r.Method == "POST" && r.URL.Path == "/library/metadata/50/posters":
			f.posters++
			f.posterType = r.Header.Get("Content-Type")
			f.posterBody, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.Error(w, "no", 404)
		}
	}))
}

const legacy = "My Order [Ordarr:abc123]"

func TestOwnershipAcceptsTheOldTitleMarkerAndTheNewDescriptionMarker(t *testing.T) {
	name, marker := SplitMarker(legacy)
	if name != "My Order" || marker != "[Ordarr:abc123]" {
		t.Fatal(name, marker)
	}
	if n, m := SplitMarker("Plain"); n != "Plain" || m != "" {
		t.Fatal("a title without a marker")
	}
	for _, c := range []struct {
		p    Playlist
		want bool
	}{
		{Playlist{Title: legacy}, true}, // not migrated yet
		{Playlist{Title: "My Order", Summary: "text\n\nManaged by Ordarr. [Ordarr:abc123]"}, true}, // migrated
		{Playlist{Title: "My Order"}, false},                                  // someone else's playlist with the same name
		{Playlist{Title: "My Order", Summary: "[Ordarr:someoneelse]"}, false}, // another Ordarr's marker
		{Playlist{Title: "Renamed", Summary: "[Ordarr:abc123]"}, false},       // title changed: not provably ours
	} {
		if ownedAs(c.p, legacy) != c.want {
			t.Errorf("%+v: want %v", c.p, c.want)
		}
	}
}

func TestFinalizeMovesTheMarkerIntoTheDescription(t *testing.T) {
	f := &fakePlaylists{title: legacy}
	s := f.server(t)
	defer s.Close()
	c := New()
	ctx := context.Background()
	if err := c.FinalizePlaylist(ctx, s.URL, "50", legacy, "Watch these in order.", "token", "client"); err != nil {
		t.Fatal(err)
	}
	if f.title != "My Order" || !strings.Contains(f.summary, "[Ordarr:abc123]") || !strings.HasPrefix(f.summary, "Watch these in order.") {
		t.Fatalf("title %q summary %q", f.title, f.summary)
	}
	if len(f.puts) != 2 || !strings.HasPrefix(f.puts[0], "summary=") || !strings.HasPrefix(f.puts[1], "title=") {
		t.Fatalf("the description must be written before the title is changed: %v", f.puts)
	}
	// Still provably ours afterwards, and a second pass changes nothing.
	if _, err := c.OwnedPlaylist(ctx, s.URL, "50", legacy, "token", "client"); err != nil {
		t.Fatal("playlist no longer recognised after the migration", err)
	}
	if err := c.FinalizePlaylist(ctx, s.URL, "50", legacy, "Watch these in order.", "token", "client"); err != nil || len(f.puts) != 2 {
		t.Fatalf("not idempotent: %v %v", err, f.puts)
	}
	// An edited collection description is carried over without touching the title again.
	if err := c.FinalizePlaylist(ctx, s.URL, "50", legacy, "New words.", "token", "client"); err != nil || !strings.HasPrefix(f.summary, "New words.") || len(f.puts) != 3 {
		t.Fatalf("description update: %v %q %v", err, f.summary, f.puts)
	}
}

func TestFinalizeNeverStrandsAPlaylistWithoutAMarker(t *testing.T) {
	// Plex accepts the description write but does not apply it: the title must keep its marker.
	f := &fakePlaylists{title: legacy, ignoreSummary: true}
	s := f.server(t)
	defer s.Close()
	c := New()
	if err := c.FinalizePlaylist(context.Background(), s.URL, "50", legacy, "d", "token", "client"); err == nil {
		t.Fatal("a description that did not take was treated as success")
	}
	if f.title != legacy {
		t.Fatalf("the title was changed although the marker was not in the description: %q", f.title)
	}
	for _, p := range f.puts {
		if strings.HasPrefix(p, "title=") {
			t.Fatal("a rename was attempted")
		}
	}
	// A playlist that is not ours is never edited.
	other := &fakePlaylists{title: "My Order", summary: "mine"}
	s2 := other.server(t)
	defer s2.Close()
	if err := c.FinalizePlaylist(context.Background(), s2.URL, "50", legacy, "d", "token", "client"); !errors.Is(err, ErrPlaylistConflict) || len(other.puts) != 0 {
		t.Fatalf("edited a playlist that is not ours: %v %v", err, other.puts)
	}
	// Adoption by name does not pick up someone else's same-named playlist either.
	if id, err := c.FindManagedPlaylist(context.Background(), s2.URL, legacy, "token", "client"); err != nil || id != "" {
		t.Fatalf("adopted a foreign playlist: %q %v", id, err)
	}
}

func TestPosterUploadSendsTheImageToTheOwnedPlaylistOnly(t *testing.T) {
	f := &fakePlaylists{title: "My Order", summary: "[Ordarr:abc123]"}
	s := f.server(t)
	defer s.Close()
	c := New()
	ctx := context.Background()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)
	if err := c.UploadPlaylistPoster(ctx, s.URL, "50", legacy, png, "image/png", "token", "client"); err != nil {
		t.Fatal(err)
	}
	if f.posters != 1 || f.posterType != "image/png" || string(f.posterBody) != string(png) {
		t.Fatalf("poster not sent as raw image bytes: %d %q", f.posters, f.posterType)
	}
	for _, bad := range []struct {
		data []byte
		kind string
	}{{png, "image/svg+xml"}, {png, "text/html"}, {nil, "image/png"}, {make([]byte, 2<<20+1), "image/png"}} {
		if err := c.UploadPlaylistPoster(ctx, s.URL, "50", legacy, bad.data, bad.kind, "token", "client"); err == nil {
			t.Errorf("accepted %q (%d bytes)", bad.kind, len(bad.data))
		}
	}
	other := &fakePlaylists{title: "My Order"} // not ours
	s2 := other.server(t)
	defer s2.Close()
	if err := c.UploadPlaylistPoster(ctx, s2.URL, "50", legacy, png, "image/png", "token", "client"); !errors.Is(err, ErrPlaylistConflict) || other.posters != 0 {
		t.Fatalf("uploaded a poster to a playlist that is not ours: %v", err)
	}
}
