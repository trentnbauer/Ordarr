// Package portable defines the versioned public collection format. Destination
// IDs, credentials and availability never leave the local database.
package portable

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/store"
	"go.yaml.in/yaml/v3"
	"io"
)

const MaxBytes = 1 << 20

var ErrFormat = errors.New("Invalid collection file: use version 1, explicit provider IDs, unique entry IDs, and at most 1,000 expanded entries.")

type Document struct {
	ID          string  `json:"id,omitempty" yaml:"id,omitempty"`
	Version     int     `json:"version" yaml:"version"`
	Title       string  `json:"title" yaml:"title"`
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Entries     []Entry `json:"entries" yaml:"entries"`
}
type Entry struct {
	ID         string `json:"id" yaml:"id"`
	Kind       string `json:"kind" yaml:"kind"`
	Title      string `json:"title" yaml:"title"`
	Provider   string `json:"provider" yaml:"provider"`
	ExternalID string `json:"external_id" yaml:"external_id"`
	Season     int    `json:"season,omitempty" yaml:"season,omitempty"`
	Episode    int    `json:"episode,omitempty" yaml:"episode,omitempty"`
	End        int    `json:"end,omitempty" yaml:"end,omitempty"`
}

func Decode(data []byte) (Document, error) {
	var d Document
	if len(data) > MaxBytes || len(bytes.TrimSpace(data)) == 0 {
		return d, ErrFormat
	}
	// YAML is a superset of JSON. KnownFields and duplicate-key rejection apply to both.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if dec.Decode(&d) != nil {
		return d, ErrFormat
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return d, ErrFormat
	}
	if _, err := d.Collection(); err != nil {
		return d, err
	}
	return d, nil
}
func (d Document) Collection() (store.Collection, error) {
	c := store.Collection{Title: d.Title, Description: d.Description}
	seen := map[string]bool{}
	if d.Version != 1 || len(d.Entries) > store.MaxEntries {
		return c, ErrFormat
	}
	for _, e := range d.Entries {
		if len(e.ID) < 1 || len(e.ID) > 80 {
			return c, ErrFormat
		}
		for _, ch := range e.ID {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
				return c, ErrFormat
			}
		}
		if seen[e.ID] {
			return c, ErrFormat
		}
		seen[e.ID] = true
		if e.Kind == "movie" && (e.Season != 0 || e.Episode != 0 || e.End != 0) || e.Kind == "episode" && e.End != 0 {
			return c, ErrFormat
		}
		entries, err := store.Expand(store.EntrySpec{Kind: e.Kind, Title: e.Title, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Start: e.Episode, End: e.End})
		if err != nil {
			return c, ErrFormat
		}
		for _, entry := range entries {
			entry.ID = e.ID
			if len(entries) > 1 {
				entry.ID = e.ID + "-" + itoa(entry.Episode)
			}
			c.Entries = append(c.Entries, entry)
		}
		if len(c.Entries) > store.MaxEntries {
			return c, ErrFormat
		}
	}
	if store.ValidatePortableCollection(c) != nil {
		return c, ErrFormat
	}
	return c, nil
}
func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
func FromCollection(c store.Collection) Document {
	d := Document{Version: 1, ID: c.ID, Title: c.Title, Description: c.Description, Entries: []Entry{}}
	for _, e := range c.Entries {
		d.Entries = append(d.Entries, Entry{ID: e.ID, Kind: e.Kind, Title: e.Title, Provider: e.Provider, ExternalID: e.ExternalID, Season: e.Season, Episode: e.Episode})
	}
	return d
}
func Encode(d Document, format string) ([]byte, error) {
	if _, err := d.Collection(); err != nil {
		return nil, err
	}
	if format == "json" {
		return json.MarshalIndent(d, "", "  ")
	}
	if format == "yaml" {
		return yaml.Marshal(d)
	}
	return nil, ErrFormat
}
