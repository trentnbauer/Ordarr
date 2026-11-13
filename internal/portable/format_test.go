package portable

import (
	"reflect"
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
