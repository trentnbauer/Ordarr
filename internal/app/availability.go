package app

import (
	"context"
	"database/sql"
	"errors"
	"sync"

	"github.com/trentnbauer/Ordarr/internal/matching"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// availability is the cached outcome of matching one collection revision
// against one complete Plex catalogue generation. It is derived data only:
// publishing always re-verifies against a fresh scan.
type availability struct {
	Ready                    bool // a complete current catalogue exists
	ScannedAt                int64
	Found, NeedPick, Missing int
	States                   map[string]string // entry ID -> available|unmatched|missing
	MissingEntries           []store.Entry     // state "missing", in order
}

type availabilityKey struct{ collection, connection string }
type availabilityEntry struct {
	revision   int
	generation string
	manual     int64
	value      availability
}
type availabilityCache struct {
	mu     sync.Mutex
	manual int64 // bumped whenever a manual match changes
	values map[availabilityKey]availabilityEntry
}

func (c *availabilityCache) bump() {
	c.mu.Lock()
	c.manual++
	c.mu.Unlock()
}

// availabilityFor matches collections against a connection's catalogue,
// loading the catalogue only when a cached result is stale.
func (a *App) availabilityFor(ctx context.Context, collections []store.CollectionRef, connection string) (map[string]availability, error) {
	result := map[string]availability{}
	if connection == "" || len(collections) == 0 {
		return result, nil
	}
	generation, scanned, err := a.store.CatalogueGeneration(ctx, connection)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrConflict) {
		return result, nil // no complete current scan: nothing is inferred
	}
	if err != nil {
		return nil, err
	}
	a.avail.mu.Lock()
	if a.avail.values == nil {
		a.avail.values = map[availabilityKey]availabilityEntry{}
	}
	manual := a.avail.manual
	var stale []store.CollectionRef
	for _, c := range collections {
		v, ok := a.avail.values[availabilityKey{c.ID, connection}]
		if ok && v.revision == c.Revision && v.generation == generation && v.manual == manual {
			result[c.ID] = v.value
		} else {
			stale = append(stale, c)
		}
	}
	a.avail.mu.Unlock()
	if len(stale) == 0 {
		return result, nil
	}
	items, _, err := a.store.Catalogue(ctx, connection)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrConflict) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	index := matching.NewIndex(items)
	for _, ref := range stale {
		c, err := a.store.Collection(ctx, ref.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		overrides, err := a.store.ManualMatches(ctx, connection, c)
		if err != nil {
			return nil, err
		}
		v := availability{Ready: true, ScannedAt: scanned, States: map[string]string{}}
		for _, e := range c.Entries {
			r := index.Resolve(matching.Identity{Kind: e.Kind, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode}, overrides[e.ID])
			v.States[e.ID] = r.State
			switch r.State {
			case "available":
				v.Found++
			case "unmatched":
				v.NeedPick++
			default:
				v.Missing++
				v.MissingEntries = append(v.MissingEntries, e)
			}
		}
		result[c.ID] = v
		a.avail.mu.Lock()
		a.avail.values[availabilityKey{c.ID, connection}] = availabilityEntry{revision: c.Revision, generation: generation, manual: manual, value: v}
		a.avail.mu.Unlock()
	}
	return result, nil
}
