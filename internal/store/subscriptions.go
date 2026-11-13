package store

import (
	"bytes"
	"context"
	"time"
)

type Subscription struct {
	ID, CollectionID, URL, Policy string
	Revision                      int
	ETag, Modified                string
	LastValid, Pending            []byte
	NextRun                       int64
	Failures                      int
	Message                       string
	CheckedAt                     int64
	IntervalMinutes               int
}

func scanSubscription(row interface{ Scan(...any) error }) (Subscription, error) {
	var s Subscription
	err := row.Scan(&s.ID, &s.CollectionID, &s.URL, &s.Policy, &s.Revision, &s.ETag, &s.Modified, &s.LastValid, &s.Pending, &s.NextRun, &s.Failures, &s.Message, &s.CheckedAt, &s.IntervalMinutes)
	return s, err
}
func (s *Store) Subscription(ctx context.Context, id string) (Subscription, error) {
	return scanSubscription(s.DB.QueryRowContext(ctx, "SELECT * FROM subscriptions WHERE id=?", id))
}
func (s *Store) CollectionSubscription(ctx context.Context, id string) (Subscription, error) {
	return scanSubscription(s.DB.QueryRowContext(ctx, "SELECT * FROM subscriptions WHERE collection_id=?", id))
}
func (s *Store) CreateSubscription(ctx context.Context, url, policy string) (Subscription, error) {
	var sub Subscription
	if policy != "automatic" && policy != "approval" {
		return sub, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return sub, err
	}
	defer tx.Rollback()
	id := newID()
	sub = Subscription{ID: newID(), CollectionID: id, URL: url, Policy: policy, Revision: 1}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "INSERT INTO collections(id,title,created_at,updated_at) VALUES(?,?,?,?)", id, "New subscription", now, now); err != nil {
		return sub, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO subscriptions(id,collection_id,url,policy) VALUES(?,?,?,?)", sub.ID, id, url, policy); err != nil {
		return sub, err
	}
	return sub, tx.Commit()
}
func (s *Store) DueSubscriptions(ctx context.Context) ([]Subscription, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM subscriptions WHERE policy<>'paused' AND next_run<=?", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, sub)
	}
	return result, rows.Err()
}
func (s *Store) DeferSubscription(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE subscriptions SET next_run=?+interval_minutes*60 WHERE id=?", time.Now().Unix(), id)
	return err
}
func (s *Store) SourceFailure(ctx context.Context, sub Subscription) error {
	delay := time.Duration(sub.IntervalMinutes) * time.Minute
	if delay < 5*time.Minute {
		delay = time.Hour
	}
	for i := 0; i < sub.Failures && i < 5; i++ {
		delay *= 2
	}
	if delay > 7*24*time.Hour {
		delay = 7 * 24 * time.Hour
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE subscriptions SET failures=failures+1,next_run=?,checked_at=?,message='Fetch or validation failed; last valid source retained.' WHERE id=? AND revision=?", time.Now().Add(delay).Unix(), time.Now().Unix(), sub.ID, sub.Revision)
	return err
}
func (s *Store) SourceUnchanged(ctx context.Context, sub Subscription) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE subscriptions SET failures=0,checked_at=?,message='Source unchanged.' WHERE id=? AND revision=?", time.Now().Unix(), sub.ID, sub.Revision)
	return err
}
func (s *Store) ReceiveSource(ctx context.Context, sub Subscription, data []byte, c Collection, etag, modified string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanSubscription(tx.QueryRowContext(ctx, "SELECT * FROM subscriptions WHERE id=?", sub.ID))
	if err != nil {
		return err
	}
	if current.Revision != sub.Revision || current.Policy == "paused" {
		return ErrConflict
	}
	if bytes.Equal(data, current.LastValid) {
		_, err = tx.ExecContext(ctx, "UPDATE subscriptions SET pending=NULL,etag=?,modified=?,failures=0,checked_at=?,message='Source unchanged.' WHERE id=?", etag, modified, time.Now().Unix(), sub.ID)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if sub.Policy == "approval" {
		_, err = tx.ExecContext(ctx, "UPDATE subscriptions SET pending=?,etag=?,modified=?,failures=0,checked_at=?,message='Update awaits approval.' WHERE id=?", data, etag, modified, time.Now().Unix(), sub.ID)
	} else {
		if err = replaceSource(ctx, tx, sub, c); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE subscriptions SET last_valid=?,pending=NULL,etag=?,modified=?,failures=0,checked_at=?,message='Source applied.' WHERE id=?", data, etag, modified, time.Now().Unix(), sub.ID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ApproveSource(ctx context.Context, sub Subscription, c Collection) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := scanSubscription(tx.QueryRowContext(ctx, "SELECT * FROM subscriptions WHERE id=?", sub.ID))
	if err != nil {
		return err
	}
	if current.Revision != sub.Revision || !bytes.Equal(current.Pending, sub.Pending) || len(sub.Pending) == 0 {
		return ErrConflict
	}
	if err = replaceSource(ctx, tx, sub, c); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE subscriptions SET last_valid=pending,pending=NULL,revision=revision+1,message='Approved source applied.' WHERE id=?", sub.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SubscriptionPolicy(ctx context.Context, id, policy string, revision, interval int) error {
	if interval < 5 || interval > 10080 {
		return ErrInvalid
	}
	if policy != "automatic" && policy != "approval" && policy != "paused" {
		return ErrInvalid
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE subscriptions SET policy=?,interval_minutes=?,revision=revision+1,etag='',modified='',next_run=0 WHERE id=? AND revision=?", policy, interval, id, revision)
	if err != nil {
		return err
	}
	return plexChanged(result)
}
