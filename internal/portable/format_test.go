package portable

import (
	"reflect"
	"strings"
	"testing"
)

func TestStrictFormatRoundTripsAndRanges(t *testing.T) {
	raw := []byte(`version: 1
title: Order
entries:
  - {id: film, kind: movie, title: Movie, provider: tmdb, external_id: "42"}
  - {id: run, kind: episode_range, title: Show, provider: tvdb, external_id: "7", season: 0, episode: 1, end: 3}
  - {id: repeat, kind: movie, title: Movie, provider: tmdb, external_id: "42"}
`)
	d, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.Collection()
	if err != nil || len(c.Entries) != 5 || c.Entries[1].Episode != 1 || c.Entries[3].Episode != 3 {
		t.Fatal(c, err)
	}
	for _, format := range []string{"json", "yaml"} {
		data, err := Encode(FromCollection(c), format)
		if err != nil {
			t.Fatal(err)
		}
		read, err := Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		back, _ := read.Collection()
		if !reflect.DeepEqual(c, back) {
			t.Fatal("roundtrip changed collection", format)
		}
	}
	for _, bad := range []string{`{"version":2,"title":"x","entries":[]}`, `version: 1\ntitle: x\nwat: nope`, "version: 1\ntitle: x\nentries: []\n---\nversion: 1", `{"version":1,"title":"x","title":"y","entries":[]}`, `{"version":1,"title":"","entries":[]}`} {
		if _, err = Decode([]byte(bad)); err == nil {
			t.Fatal("accepted malformed", bad)
		}
	}
}

func TestRangeSourceIDAlwaysExportsWithinBounds(t *testing.T) {
	d := Document{Version: 1, Title: "Order", Entries: []Entry{{ID: strings.Repeat("a", 80), Kind: "episode_range", Title: "Show", Provider: "tvdb", ExternalID: "1", Season: 1, Episode: 1, End: 2}}}
	if _, err := d.Collection(); err == nil {
		t.Fatal("expanded IDs exceed public schema")
	}
}

func TestCoverIsAnOptionalHTTPSURLThatRoundTrips(t *testing.T) {
	doc := func(cover string) []byte {
		return []byte("version: 1\ntitle: T\ncover: \"" + cover + "\"\nentries:\n  - id: a\n    kind: movie\n    title: M\n    provider: tmdb\n    external_id: \"1\"\n")
	}
	d, err := Decode(doc("https://img.example/c.png"))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := d.Collection()
	if c.Cover != "https://img.example/c.png" {
		t.Fatal("cover lost", c.Cover)
	}
	out, err := Encode(FromCollection(c), "yaml")
	if err != nil || !strings.Contains(string(out), "cover: https://img.example/c.png") {
		t.Fatalf("cover not exported: %v\n%s", err, out)
	}
	if _, err = Decode([]byte("version: 1\ntitle: T\nentries: []\n")); err != nil {
		t.Fatal("a collection without a cover must still be valid", err)
	}
	for _, bad := range []string{"http://img.example/c.png", "javascript:alert(1)", "https://img.example/a b.png", "https://user:pw@img.example/c.png", "/relative.png", "https://img.example/" + strings.Repeat("a", 2100)} {
		if _, err := Decode(doc(bad)); err == nil {
			t.Errorf("cover %q accepted", bad[:min(len(bad), 40)])
		}
	}
}
