package matching

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/plex"
)

func TestIndexAgreesWithResolve(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	providers := []string{"tmdb", "tvdb", "imdb"}
	pick := func() map[string]string {
		ids := map[string]string{}
		for _, p := range providers {
			if r.Intn(3) > 0 {
				ids[p] = strconv.Itoa(r.Intn(6))
			}
		}
		return ids
	}
	num := func() *int {
		if r.Intn(8) == 0 {
			return nil
		}
		n := r.Intn(3)
		return &n
	}
	var items []plex.IdentifiedMedia
	for i := 0; i < 400; i++ {
		item := plex.IdentifiedMedia{MediaItem: plex.MediaItem{RatingKey: strconv.Itoa(i % 350), Kind: []string{"movie", "episode"}[r.Intn(2)]}, Playable: r.Intn(4) > 0}
		if item.Kind == "episode" {
			item.SeriesIDs = pick()
			item.Season, item.Episode = num(), num()
		} else {
			item.IDs = pick()
		}
		items = append(items, item)
	}
	x := NewIndex(items)
	for i := 0; i < 3000; i++ {
		e := Identity{Kind: []string{"movie", "episode"}[r.Intn(2)], Provider: providers[r.Intn(3)], ExternalID: strconv.Itoa(r.Intn(7)), Season: r.Intn(3), Episode: r.Intn(3)}
		manual := ""
		if r.Intn(5) == 0 {
			manual = strconv.Itoa(r.Intn(400))
		}
		want, got := Resolve(e, items, manual), x.Resolve(e, manual)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("entry %+v manual %q: want %+v got %+v", e, manual, want, got)
		}
	}
}
