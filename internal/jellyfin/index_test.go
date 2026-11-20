package jellyfin

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
)

func TestIndexAgreesWithResolve(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	providers := []string{"tmdb", "tvdb", "imdb"}
	pick := func() map[string]string {
		ids := map[string]string{}
		for _, p := range providers {
			switch r.Intn(4) {
			case 0:
			case 1:
				ids[p] = ""
			default:
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
	var items []Item
	for i := 0; i < 400; i++ {
		item := Item{ID: strconv.Itoa(i % 350), Kind: []string{"movie", "episode"}[r.Intn(2)], Unavailable: r.Intn(4) == 0}
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
		if want, got := Resolve(e, items, manual), x.Resolve(e, manual); !reflect.DeepEqual(want, got) {
			t.Fatalf("entry %+v manual %q: want %+v got %+v", e, manual, want, got)
		}
	}
}
