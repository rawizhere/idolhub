package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a settings key does not exist.
var ErrNotFound = errors.New("store: not found")

// SettingsStore reads and writes the settings key-value table.
type SettingsStore struct {
	db *sql.DB
}

func (s *SettingsStore) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: settings get: %w", err)
	}
	return value, nil
}

func (s *SettingsStore) Set(ctx context.Context, key, value string) error {
	query := `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value`
	if err := execContext(ctx, s.db, query, key, value); err != nil {
		return fmt.Errorf("store: settings set: %w", err)
	}
	return nil
}
