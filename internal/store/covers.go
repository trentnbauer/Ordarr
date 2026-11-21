package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

// MaxCoverBytes caps a downloaded cover image.
const MaxCoverBytes = 2 << 20

// validCover accepts an empty cover or a plain https URL. Whether it is safe to fetch is decided
// again at fetch time (public hosts only); this just keeps junk out of the database and files.
func validCover(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	return !strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f })
}

// Cover is the downloaded copy of a collection's cover image.
type Cover struct {
	URL, ContentType, SHA string
	Data                  []byte
	FetchedAt             int64
}

func (s *Store) SaveCover(ctx context.Context, collection, url, contentType string, data []byte) error {
	sum := sha256.Sum256(data)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO collection_covers(collection_id,url,content_type,data,sha,fetched_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(collection_id) DO UPDATE SET url=excluded.url,content_type=excluded.content_type,data=excluded.data,sha=excluded.sha,fetched_at=excluded.fetched_at`,
		collection, url, contentType, data, hex.EncodeToString(sum[:]), time.Now().Unix())
	return err
}

// CoverFor returns the downloaded cover, or ok=false when there is none.
func (s *Store) CoverFor(ctx context.Context, collection string) (Cover, bool, error) {
	var c Cover
	err := s.DB.QueryRowContext(ctx, "SELECT url,content_type,data,sha,fetched_at FROM collection_covers WHERE collection_id=?", collection).Scan(&c.URL, &c.ContentType, &c.Data, &c.SHA, &c.FetchedAt)
	if err == sql.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

// CoverURLCached reports the URL the stored image was downloaded from ("" when none).
func (s *Store) CoverURLCached(ctx context.Context, collection string) string {
	var u string
	s.DB.QueryRowContext(ctx, "SELECT url FROM collection_covers WHERE collection_id=?", collection).Scan(&u)
	return u
}

// ClearCover drops the downloaded image (the collection no longer has a cover).
func (s *Store) ClearCover(ctx context.Context, collection string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM collection_covers WHERE collection_id=?", collection)
	return err
}

// PosterApplied is the hash of the cover last pushed to a destination's Plex playlist.
func (s *Store) PosterApplied(ctx context.Context, destination string) string {
	var sum string
	s.DB.QueryRowContext(ctx, "SELECT sha FROM destination_cover WHERE destination_id=?", destination).Scan(&sum)
	return sum
}

func (s *Store) SetPosterApplied(ctx context.Context, destination, sha string) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO destination_cover VALUES(?,?) ON CONFLICT(destination_id) DO UPDATE SET sha=excluded.sha", destination, sha)
	return err
}
