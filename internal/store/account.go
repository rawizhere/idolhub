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
INSERT INTO accounts (platform, username, save_text, skip_retweets, download_photos, download_videos, filters)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (platform, username) DO UPDATE SET
	save_text = excluded.save_text,
	skip_retweets = excluded.skip_retweets,
	download_photos = excluded.download_photos,
	download_videos = excluded.download_videos,
	filters = excluded.filters`
	args := []any{
		a.Platform,
		a.Username,
		boolInt(a.SaveText),
		boolInt(a.SkipRetweets),
		nullBool(a.DownloadPhotos),
		nullBool(a.DownloadVideos),
		filters,
	}
	if err := execContext(ctx, s.db, query, args...); err != nil {
		return fmt.Errorf("store: account upsert: %w", err)
	}
	return nil
}

func (s *AccountStore) List(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT platform, username, save_text, skip_retweets, download_photos, download_videos, filters
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
	if err := execContext(ctx, s.db, `DELETE FROM accounts WHERE platform = ? AND username = ?`, platform, username); err != nil {
		return fmt.Errorf("store: account delete: %w", err)
	}
	return nil
}

func (s *AccountStore) GetSyncInfo(ctx context.Context, platform, username string) (SyncInfo, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(last_sync_status, 'idle'), last_sync_time FROM accounts WHERE platform = ? AND username = ?`,
		platform, username)
	info, err := scanSyncInfo(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncInfo{Status: "idle"}, nil
	}
	if err != nil {
		return SyncInfo{}, fmt.Errorf("store: sync info get: %w", err)
	}
	return info, nil
}

func (s *AccountStore) SetSyncInfo(ctx context.Context, platform, username, status string, at time.Time) error {
	query := `
INSERT INTO accounts (platform, username, last_sync_status, last_sync_time) VALUES (?, ?, ?, ?)
ON CONFLICT (platform, username) DO UPDATE SET last_sync_status = excluded.last_sync_status, last_sync_time = excluded.last_sync_time`
	if err := execContext(ctx, s.db, query, platform, username, status, nullTime(at)); err != nil {
		return fmt.Errorf("store: sync info set: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(rs rowScanner) (Account, error) {
	var (
		a              Account
		saveText       int
		skipRetweets   int
		downloadPhotos sql.NullBool
		downloadVideos sql.NullBool
		filters        sql.NullString
	)
	if err := rs.Scan(&a.Platform, &a.Username, &saveText, &skipRetweets, &downloadPhotos, &downloadVideos, &filters); err != nil {
		return Account{}, fmt.Errorf("store: account scan: %w", err)
	}
	a.SaveText = saveText == 1
	a.SkipRetweets = skipRetweets == 1
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

func scanSyncInfo(scan func(dest ...any) error) (SyncInfo, error) {
	var (
		status string
		at     sql.NullTime
	)
	if err := scan(&status, &at); err != nil {
		return SyncInfo{}, err
	}
	info := SyncInfo{Status: status}
	if at.Valid {
		info.Time = at.Time
	}
	return info, nil
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

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC()
}
