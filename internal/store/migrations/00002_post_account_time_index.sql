-- +goose Up
CREATE INDEX idx_posts_account_time ON posts (platform, username, posted_at);

-- +goose Down
DROP INDEX IF EXISTS idx_posts_account_time;
