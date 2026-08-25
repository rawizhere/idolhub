package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const dsnOptions = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"

// Store is the SQLite-backed persistence layer.
type Store struct {
	DB       *sql.DB
	Accounts *AccountStore
	Settings *SettingsStore
	Posts    *PostStore
}

// Open opens or creates the database at path and applies pending migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?%s", path, dsnOptions))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{
		DB:       db,
		Accounts: &AccountStore{db: db},
		Settings: &SettingsStore{db: db},
	}
	s.Posts = &PostStore{db: db}
	return s, nil
}

func migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("store: dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.DB.Close()
}

func execContext(ctx context.Context, db *sql.DB, query string, args ...any) error {
	_, err := db.ExecContext(ctx, query, args...)
	return err
}
