-- +goose Up
ALTER TABLE accounts ADD COLUMN twitter_auth_token TEXT;

-- +goose Down
ALTER TABLE accounts DROP COLUMN twitter_auth_token;
