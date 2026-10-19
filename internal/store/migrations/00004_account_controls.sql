-- +goose Up
ALTER TABLE accounts ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN cookies TEXT;
ALTER TABLE accounts ADD COLUMN since_date TEXT;
ALTER TABLE accounts ADD COLUMN max_posts INTEGER NOT NULL DEFAULT 0;

CREATE TABLE sync_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT NOT NULL,
    username TEXT NOT NULL,
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP NOT NULL,
    status TEXT NOT NULL,
    full_sync INTEGER NOT NULL DEFAULT 0,
    new_files INTEGER NOT NULL DEFAULT 0,
    error TEXT
);

CREATE INDEX idx_sync_runs_account ON sync_runs (platform, username, started_at);

-- +goose Down
DROP INDEX IF EXISTS idx_sync_runs_account;
DROP TABLE IF EXISTS sync_runs;
ALTER TABLE accounts DROP COLUMN max_posts;
ALTER TABLE accounts DROP COLUMN since_date;
ALTER TABLE accounts DROP COLUMN cookies;
ALTER TABLE accounts DROP COLUMN paused;
