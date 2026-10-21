package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Account mirrors the accounts table row.
type Account struct {
	Platform       string
	Username       string
	SaveText       bool
	SkipRetweets   bool
	DownloadPhotos *bool
	DownloadVideos *bool
	Filters        []string
	// Paused accounts are skipped by auto-sync; a manual sync still runs.
	Paused bool
	// Cookies is a Netscape export that replaces the platform-wide session for this account.
	Cookies string
	// SinceDate (YYYY-MM-DD) is the oldest post a sync fetches; empty means no bound.
	SinceDate string
	// MaxPosts caps a sync that has no watermark yet (the first or a full one); 0 means no cap.
	MaxPosts int
}

// SyncInfo holds per-account sync bookkeeping.
type SyncInfo struct {
	Status string
	Time   time.Time
}

// AccountStore reads and writes account rows.
type AccountStore struct {
	db *sql.DB
}

func (s *AccountStore) Upsert(ctx context.Context, a Account) error {
	filters, err := encodeFilters(a.Filters)
	if err != nil {
		return fmt.Errorf("store: account upsert: %w", err)
	}
	query := `
INSERT INTO accounts (platform, username, save_text, skip_retweets, download_photos, download_videos, filters, paused, cookies, since_date, max_posts)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, username) DO UPDATE SET
	save_text = excluded.save_text,
	skip_retweets = excluded.skip_retweets,
	download_photos = excluded.download_photos,
	download_videos = excluded.download_videos,
	filters = excluded.filters,
	paused = excluded.paused,
	cookies = excluded.cookies,
	since_date = excluded.since_date,
	max_posts = excluded.max_posts`
	args := []any{
		a.Platform,
		a.Username,
		boolInt(a.SaveText),
		boolInt(a.SkipRetweets),
		nullBool(a.DownloadPhotos),
		nullBool(a.DownloadVideos),
		filters,
		boolInt(a.Paused),
		nullString(a.Cookies),
		nullString(a.SinceDate),
		a.MaxPosts,
	}
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("store: account upsert: %w", err)
	}
	return nil
}

func (s *AccountStore) List(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT platform, username, save_text, skip_retweets, download_photos, download_videos, filters, paused, cookies, since_date, max_posts
FROM accounts
ORDER BY platform, username`)
	if err != nil {
		return nil, fmt.Errorf("store: account list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *AccountStore) Delete(ctx context.Context, platform, username string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE platform = ? AND username = ?`, platform, username); err != nil {
		return fmt.Errorf("store: account delete: %w", err)
	}
	return nil
}

func (s *AccountStore) GetSyncInfo(ctx context.Context, platform, username string) (SyncInfo, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(last_sync_status, 'idle'), last_sync_time FROM accounts WHERE platform = ? AND username = ?`,
		platform, username)
	var (
		info SyncInfo
		at   sql.NullTime
	)
	err := row.Scan(&info.Status, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncInfo{Status: "idle"}, nil
	}
	if err != nil {
		return SyncInfo{}, fmt.Errorf("store: sync info get: %w", err)
	}
	info.Time = at.Time
	return info, nil
}

// SetSyncInfo updates sync bookkeeping of an existing account. It never inserts: a late status write for a removed account must not bring it back on the next load.
func (s *AccountStore) SetSyncInfo(ctx context.Context, platform, username, status string, at time.Time) error {
	query := `UPDATE accounts SET last_sync_status = ?, last_sync_time = ? WHERE platform = ? AND username = ?`
	if _, err := s.db.ExecContext(ctx, query, status, nullTime(at), platform, username); err != nil {
		return fmt.Errorf("store: sync info set: %w", err)
	}
	return nil
}

// Rename moves an account and everything keyed by its handle (posts, sync history) to a new username in one transaction.
func (s *AccountStore) Rename(ctx context.Context, platform, from, to string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: account rename: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`UPDATE accounts SET username = ? WHERE platform = ? AND username = ?`,
		`UPDATE posts SET username = ? WHERE platform = ? AND username = ?`,
		`UPDATE sync_runs SET username = ? WHERE platform = ? AND username = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, to, platform, from); err != nil {
			return fmt.Errorf("store: account rename: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: account rename: %w", err)
	}
	return nil
}

func scanAccount(rs *sql.Rows) (Account, error) {
	var (
		a              Account
		saveText       int
		skipRetweets   int
		downloadPhotos sql.NullBool
		downloadVideos sql.NullBool
		filters        sql.NullString
		paused         int
		cookies        sql.NullString
		sinceDate      sql.NullString
	)
	if err := rs.Scan(&a.Platform, &a.Username, &saveText, &skipRetweets, &downloadPhotos, &downloadVideos, &filters, &paused, &cookies, &sinceDate, &a.MaxPosts); err != nil {
		return Account{}, fmt.Errorf("store: account scan: %w", err)
	}
	a.SaveText = saveText == 1
	a.SkipRetweets = skipRetweets == 1
	a.Paused = paused == 1
	a.Cookies = cookies.String
	a.SinceDate = sinceDate.String
	if downloadPhotos.Valid {
		v := downloadPhotos.Bool
		a.DownloadPhotos = &v
	}
	if downloadVideos.Valid {
		v := downloadVideos.Bool
		a.DownloadVideos = &v
	}
	if filters.Valid {
		parsed, err := decodeFilters(filters.String)
		if err != nil {
			return Account{}, err
		}
		a.Filters = parsed
	}
	return a, nil
}

func encodeFilters(filters []string) (any, error) {
	if len(filters) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(filters)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func decodeFilters(raw string) ([]string, error) {
	var filters []string
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return nil, fmt.Errorf("store: filters decode: %w", err)
	}
	return filters, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullBool(b *bool) any {
	if b == nil {
		return nil
	}
	return boolInt(*b)
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
