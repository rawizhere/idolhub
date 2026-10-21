package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/pressly/goose/v3"
)

// 00005 retires the bare-token settings (twitter_auth_token, instagram_session_id, the per-account twitter token):
// each one becomes a cookie line in the matching cookie export, so the scrapers see the same session as before.
func init() {
	goose.AddNamedMigrationContext("00005_legacy_tokens.go", upLegacyTokens, downLegacyTokens)
}

func upLegacyTokens(ctx context.Context, tx *sql.Tx) error {
	get := func(key string) (string, error) {
		var raw, v string
		err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		_ = json.Unmarshal([]byte(raw), &v)
		return strings.TrimSpace(v), nil
	}
	set := func(key, v string) error {
		b, _ := json.Marshal(v)
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, string(b))
		return err
	}

	twToken, err := get("twitter_auth_token")
	if err != nil {
		return err
	}
	twCookies, err := get("twitter_cookies")
	if err != nil {
		return err
	}
	// the bare token used to override the auth_token of the export
	if twToken != "" {
		twCookies = withCookie(twCookies, ".x.com", "auth_token", twToken, true)
		// the client always sends its own random ct0; a line here only keeps the settings check from flagging it missing
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		twCookies = withCookie(twCookies, ".x.com", "ct0", hex.EncodeToString(b), false)
		if err := set("twitter_cookies", twCookies); err != nil {
			return err
		}
	}

	igSession, err := get("instagram_session_id")
	if err != nil {
		return err
	}
	if igSession != "" {
		igCookies, err := get("instagram_cookies")
		if err != nil {
			return err
		}
		// the bare sessionid was only a fallback for an export without one
		if err := set("instagram_cookies", withCookie(igCookies, ".instagram.com", "sessionid", igSession, false)); err != nil {
			return err
		}
	}

	// a per-account token swapped the auth_token inside the platform export; give the account that export as its own
	rows, err := tx.QueryContext(ctx, `SELECT platform, username, twitter_auth_token FROM accounts
WHERE platform = 'twitter' AND COALESCE(twitter_auth_token, '') <> '' AND COALESCE(cookies, '') = ''`)
	if err != nil {
		return err
	}
	type acct struct{ platform, username, token string }
	var accs []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.platform, &a.username, &a.token); err != nil {
			_ = rows.Close()
			return err
		}
		accs = append(accs, a)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range accs {
		own := withCookie(twCookies, ".x.com", "auth_token", a.token, true)
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET cookies = ? WHERE platform = ? AND username = ?`, own, a.platform, a.username); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN ('twitter_auth_token', 'instagram_session_id')`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `ALTER TABLE accounts DROP COLUMN twitter_auth_token`)
	return err
}

func downLegacyTokens(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE accounts ADD COLUMN twitter_auth_token TEXT`)
	return err
}

// withCookie puts name=value into a Netscape export: it replaces an existing line when override is set, and
// appends a line when the export has none.
func withCookie(export, domain, name, value string, override bool) string {
	if strings.TrimSpace(export) == "" {
		export = "# Netscape HTTP Cookie File\n"
	}
	lines := strings.Split(strings.TrimRight(export, "\n"), "\n")
	found := false
	for i, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 && parts[5] == name {
			found = true
			if override {
				lines[i] = strings.Join(append(parts[:6], value), "\t")
			}
		}
	}
	if !found {
		lines = append(lines, strings.Join([]string{domain, "TRUE", "/", "TRUE", "2147483647", name, value}, "\t"))
	}
	return strings.Join(lines, "\n") + "\n"
}
