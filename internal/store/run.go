package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Run is one finished sync of an account.
type Run struct {
	Platform   string    `json:"-"`
	Username   string    `json:"-"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Status is "completed", "failed" or "cancelled".
	Status   string `json:"status"`
	FullSync bool   `json:"full_sync"`
	NewFiles int    `json:"new_files"`
	Error    string `json:"error,omitempty"`
}

// runsKept bounds the history per account; older rows are pruned on insert.
const runsKept = 50

// RunStore records sync history.
type RunStore struct {
	db *sql.DB
}

func (s *RunStore) Add(ctx context.Context, r Run) error {
	query := `INSERT INTO sync_runs (platform, username, started_at, finished_at, status, full_sync, new_files, error) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, query, r.Platform, r.Username, r.StartedAt.UTC(), r.FinishedAt.UTC(), r.Status, boolInt(r.FullSync), r.NewFiles, nullString(r.Error)); err != nil {
		return fmt.Errorf("store: run add: %w", err)
	}
	prune := `DELETE FROM sync_runs WHERE platform = ? AND username = ? AND id NOT IN (
	SELECT id FROM sync_runs WHERE platform = ? AND username = ? ORDER BY started_at DESC LIMIT ?)`
	if _, err := s.db.ExecContext(ctx, prune, r.Platform, r.Username, r.Platform, r.Username, runsKept); err != nil {
		return fmt.Errorf("store: run prune: %w", err)
	}
	return nil
}

// List returns an account's most recent runs, newest first.
func (s *RunStore) List(ctx context.Context, platform, username string, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT started_at, finished_at, status, full_sync, new_files, error
FROM sync_runs WHERE platform = ? AND username = ?
ORDER BY started_at DESC LIMIT ?`, platform, username, limit)
	if err != nil {
		return nil, fmt.Errorf("store: run list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Run{}
	for rows.Next() {
		r := Run{Platform: platform, Username: username}
		var full int
		var errText sql.NullString
		if err := rows.Scan(&r.StartedAt, &r.FinishedAt, &r.Status, &full, &r.NewFiles, &errText); err != nil {
			return nil, fmt.Errorf("store: run scan: %w", err)
		}
		r.FullSync = full == 1
		r.Error = errText.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteAccount drops the history of a removed account.
func (s *RunStore) DeleteAccount(ctx context.Context, platform, username string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sync_runs WHERE platform = ? AND username = ?`, platform, username); err != nil {
		return fmt.Errorf("store: run delete: %w", err)
	}
	return nil
}
