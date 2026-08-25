-- +goose Up
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE accounts (
    platform TEXT NOT NULL,
    username TEXT NOT NULL,
    save_text INTEGER NOT NULL DEFAULT 0,
    skip_retweets INTEGER NOT NULL DEFAULT 0,
    download_photos INTEGER,
    download_videos INTEGER,
    filters TEXT,
    last_sync_status TEXT DEFAULT 'idle',
    last_sync_time TIMESTAMP,
    PRIMARY KEY (platform, username)
);

CREATE TABLE posts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    platform TEXT NOT NULL,
    username TEXT NOT NULL,
    external_id TEXT NOT NULL,
    posted_at TIMESTAMP,
    text TEXT NOT NULL DEFAULT '',
    UNIQUE (platform, username, external_id)
);

CREATE INDEX idx_posts_account ON posts (platform, username);

CREATE TABLE post_media (
    post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    kind TEXT NOT NULL,
    PRIMARY KEY (post_id, url)
);

CREATE INDEX idx_post_media_url ON post_media (url);

CREATE VIRTUAL TABLE posts_fts USING fts5(text, content='posts', content_rowid='id');

-- +goose StatementBegin
CREATE TRIGGER posts_ai AFTER INSERT ON posts BEGIN
    INSERT INTO posts_fts(rowid, text) VALUES (new.id, new.text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER posts_ad AFTER DELETE ON posts BEGIN
    INSERT INTO posts_fts(posts_fts, rowid, text) VALUES ('delete', old.id, old.text);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER posts_au AFTER UPDATE ON posts BEGIN
    INSERT INTO posts_fts(posts_fts, rowid, text) VALUES ('delete', old.id, old.text);
    INSERT INTO posts_fts(rowid, text) VALUES (new.id, new.text);
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS posts_au;
DROP TRIGGER IF EXISTS posts_ad;
DROP TRIGGER IF EXISTS posts_ai;
DROP TABLE IF EXISTS posts_fts;
DROP TABLE IF EXISTS post_media;
DROP TABLE IF EXISTS posts;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS settings;
