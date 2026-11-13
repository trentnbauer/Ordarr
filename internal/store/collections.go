package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxEntries = 1000
const PageSize = 20

var ErrConflict = errors.New("This collection changed in another tab. Reload before editing again.")
var ErrInvalid = errors.New("invalid collection input")

type Entry struct {
	ID, Kind, Title, Provider, ExternalID string
	Season, Episode                       int
}
type Collection struct {
	ID, Title, Description, ForkedFrom string
	Revision                           int
	CreatedAt, UpdatedAt               int64
	Entries                            []Entry
}
type CollectionSummary struct {
	Collection
	Count int
}

// EntrySpec represents explicit provider identity. Episode IDs identify the series;
// no title matching, metadata lookup or destination availability is inferred here.
type EntrySpec struct {
	Kind, Title, Provider, ExternalID string
	Season, Start, End                int
}
type CollectionChange struct {
	Action, Title, Description string
	Spec                       EntrySpec
	IDs                        []string
	// Target is the one-based final position of the first selected entry.
	Target int
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func validText(s string, max int) bool {
	return utf8.ValidString(s) && len([]rune(s)) <= max && !strings.ContainsRune(s, 0)
}
func validTitle(title string) bool {
	return strings.TrimSpace(title) != "" && validText(title, 200) && strings.IndexFunc(title, unicode.IsControl) == -1
}
func validateMetadata(title, description string) error {
	if !validTitle(title) || !validText(description, 2000) {
		return invalid("A title of 1–200 characters and description up to 2,000 characters are required.")
	}
	return nil
}

var numericID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var imdbID = regexp.MustCompile(`^tt[0-9]{7,10}$`)

func Expand(spec EntrySpec) ([]Entry, error) {
	spec.Title = strings.TrimSpace(spec.Title)
	spec.ExternalID = strings.TrimSpace(spec.ExternalID)
	if !validTitle(spec.Title) {
		return nil, invalid("Enter a media title of 1–200 characters.")
	}
	switch spec.Provider {
	case "tmdb", "tvdb":
		if !numericID.MatchString(spec.ExternalID) {
			return nil, invalid("TMDB/TVDB IDs must be positive numeric IDs (up to 18 digits).")
		}
	case "imdb":
		if !imdbID.MatchString(spec.ExternalID) {
			return nil, invalid("IMDb IDs must start with tt followed by 7–10 digits.")
		}
	default:
		return nil, invalid("Choose a supported identifier provider.")
	}
	first, last := 1, 1
	switch spec.Kind {
	case "movie":
	case "episode":
		first = spec.Start
		last = first
	case "episode_range":
		first = spec.Start
		last = spec.End
	case "season":
		last = spec.End // Explicit episode count; no invented season length.
	default:
		return nil, invalid("Choose movie, episode, episode range or whole season.")
	}
	if spec.Kind != "movie" && (spec.Season < 0 || spec.Season > 999 || first < 1 || last < first || last > 10000 || last-first+1 > 200) {
		return nil, invalid("Use season 0–999 and episode numbers 1–10,000; expand at most 200 episodes per addition.")
	}
	result := make([]Entry, 0, last-first+1)
	for i := first; i <= last; i++ {
		e := Entry{ID: newID(), Kind: "movie", Title: spec.Title, Provider: spec.Provider, ExternalID: spec.ExternalID}
		if spec.Kind != "movie" {
			e.Kind = "episode"
			e.Season = spec.Season
			e.Episode = i
		}
		result = append(result, e)
	}
	return result, nil
}
func (s *Store) CreateCollection(ctx context.Context, title, description string) (string, error) {
	title = strings.TrimSpace(title)
	if err := validateMetadata(title, description); err != nil {
		return "", err
	}
	id := newID()
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO collections(id,title,description,created_at,updated_at) VALUES(?,?,?,?,?)", id, title, description, now, now)
	return id, err
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadCollection(ctx context.Context, q queryer, id string) (Collection, error) {
	var c Collection
	err := q.QueryRowContext(ctx, "SELECT id,title,description,forked_from,revision,created_at,updated_at FROM collections WHERE id=?", id).Scan(&c.ID, &c.Title, &c.Description, &c.ForkedFrom, &c.Revision, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	rows, err := q.QueryContext(ctx, "SELECT id,kind,title,provider,external_id,season,episode FROM entries WHERE collection_id=? ORDER BY position", id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.Kind, &e.Title, &e.Provider, &e.ExternalID, &e.Season, &e.Episode); err != nil {
			return c, err
		}
		c.Entries = append(c.Entries, e)
	}
	return c, rows.Err()
}
func (s *Store) Collection(ctx context.Context, id string) (Collection, error) {
	// Read metadata and entries in the same snapshot, including across other processes.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Collection{}, err
	}
	defer tx.Rollback()
	c, err := loadCollection(ctx, tx, id)
	if err != nil {
		return c, err
	}
	return c, tx.Commit()
}
func (s *Store) ListCollections(ctx context.Context, search, kind string, offset int) ([]CollectionSummary, error) {
	if offset < 0 || offset > 100000 || !validText(search, 200) {
		return nil, invalid("Invalid search or page.")
	}
	if kind != "" && kind != "movie" && kind != "episode" && kind != "fork" {
		return nil, invalid("Invalid filter.")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.title,c.description,c.forked_from,c.revision,c.created_at,c.updated_at,(SELECT count(*) FROM entries e WHERE e.collection_id=c.id) FROM collections c
 WHERE instr(lower(c.title || ' ' || c.description),lower(?))>0
 AND (?='' OR (?='fork' AND c.forked_from<>'') OR EXISTS(SELECT 1 FROM entries e WHERE e.collection_id=c.id AND e.kind=?))
 ORDER BY c.created_at DESC,c.id LIMIT ? OFFSET ?`, search, kind, kind, kind, PageSize+1, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []CollectionSummary
	for rows.Next() {
		var c CollectionSummary
		if err = rows.Scan(&c.ID, &c.Title, &c.Description, &c.ForkedFrom, &c.Revision, &c.CreatedAt, &c.UpdatedAt, &c.Count); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) ChangeCollection(ctx context.Context, id string, revision int, change CollectionChange) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	c, err := loadCollection(ctx, tx, id)
	if err != nil {
		return err
	}
	if revision != c.Revision {
		return ErrConflict
	}
	oldIDs := map[string]bool{}
	for _, e := range c.Entries {
		oldIDs[e.ID] = true
	}
	selected := map[string]bool{}
	for _, id := range change.IDs {
		if !oldIDs[id] || selected[id] {
			return invalid("Selection contains an unknown or repeated entry ID.")
		}
		selected[id] = true
	}
	switch change.Action {
	case "metadata":
		c.Title = strings.TrimSpace(change.Title)
		c.Description = change.Description
		if err = validateMetadata(c.Title, c.Description); err != nil {
			return err
		}
	case "add":
		added, err := Expand(change.Spec)
		if err != nil {
			return err
		}
		if len(c.Entries)+len(added) > MaxEntries {
			return invalid("A collection can contain at most 1,000 entries.")
		}
		c.Entries = append(c.Entries, added...)
	case "edit":
		if len(selected) != 1 {
			return invalid("Choose exactly one entry to edit.")
		}
		entries, err := Expand(change.Spec)
		if err != nil {
			return err
		}
		if len(entries) != 1 {
			return invalid("Edit one movie or episode at a time.")
		}
		for i, e := range c.Entries {
			if selected[e.ID] {
				entries[0].ID = e.ID
				c.Entries[i] = entries[0]
			}
		}
	case "remove":
		if len(selected) == 0 {
			return invalid("Choose entries to remove.")
		}
		c.Entries = slices.DeleteFunc(c.Entries, func(e Entry) bool { return selected[e.ID] })
	case "move":
		if len(selected) == 0 {
			return invalid("Choose entries to move.")
		}
		var moving, rest []Entry
		for _, e := range c.Entries {
			if selected[e.ID] {
				moving = append(moving, e)
			} else {
				rest = append(rest, e)
			}
		}
		if change.Target < 1 || change.Target > len(rest)+1 {
			return invalid("Choose a final position between 1 and the remaining entry count plus 1.")
		}
		index := change.Target - 1
		ordered := append([]Entry{}, rest[:index]...)
		ordered = append(ordered, moving...)
		c.Entries = append(ordered, rest[index:]...)
	case "reorder":
		if len(selected) != len(c.Entries) {
			return invalid("Reordering must contain every entry exactly once.")
		}
		lookup := map[string]Entry{}
		for _, e := range c.Entries {
			lookup[e.ID] = e
		}
		c.Entries = nil
		for _, id := range change.IDs {
			c.Entries = append(c.Entries, lookup[id])
		}
	default:
		return invalid("Unknown collection action.")
	}
	// Negative temporary positions free the uniqueness constraint without deleting
	// retained rows, keeping their stable identities available for future matches.
	if _, err = tx.ExecContext(ctx, "UPDATE entries SET position=-position-1 WHERE collection_id=?", id); err != nil {
		return err
	}
	kept := map[string]bool{}
	for _, e := range c.Entries {
		kept[e.ID] = true
	}
	for old := range oldIDs {
		if !kept[old] {
			if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE collection_id=? AND id=?", id, old); err != nil {
				return err
			}
		}
	}
	for pos, e := range c.Entries {
		if oldIDs[e.ID] {
			_, err = tx.ExecContext(ctx, "UPDATE entries SET position=?,kind=?,title=?,provider=?,external_id=?,season=?,episode=? WHERE id=? AND collection_id=?", pos, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode, e.ID, id)
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO entries(id,collection_id,position,kind,title,provider,external_id,season,episode) VALUES(?,?,?,?,?,?,?,?,?)", e.ID, id, pos, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode)
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE collections SET title=?,description=?,revision=revision+1,updated_at=? WHERE id=?", c.Title, c.Description, time.Now().Unix(), id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ForkCollection(ctx context.Context, id string, revision int) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	c, err := loadCollection(ctx, tx, id)
	if err != nil {
		return "", err
	}
	if revision != c.Revision {
		return "", ErrConflict
	}
	cloneID := newID()
	now := time.Now().Unix()
	title := c.Title
	if len([]rune(title)) > 193 {
		title = string([]rune(title)[:193])
	}
	title += " (copy)"
	if _, err = tx.ExecContext(ctx, "INSERT INTO collections(id,title,description,forked_from,created_at,updated_at) VALUES(?,?,?,?,?,?)", cloneID, title, c.Description, c.ID, now, now); err != nil {
		return "", err
	}
	for pos, e := range c.Entries {
		if _, err = tx.ExecContext(ctx, "INSERT INTO entries VALUES(?,?,?,?,?,?,?,?,?)", newID(), cloneID, pos, e.Kind, e.Title, e.Provider, e.ExternalID, e.Season, e.Episode); err != nil {
			return "", err
		}
	}
	return cloneID, tx.Commit()
}
func (s *Store) DeleteCollection(ctx context.Context, id string, revision int) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM collections WHERE id=? AND revision=?", id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}
