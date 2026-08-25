package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PostMedia is one media item attached to a post.
type PostMedia struct {
	URL  string
	Kind string
}

// Post mirrors a posts row with its media.
type Post struct {
	ID         int64
	Platform   string
	Username   string
	ExternalID string
	PostedAt   time.Time
	Text       string
	Media      []PostMedia
}

// PostStore reads and writes posts and their media.
type PostStore struct {
	db *sql.DB
}

func (s *PostStore) UpsertPost(ctx context.Context, p Post) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: post upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO posts (platform, username, external_id, posted_at, text) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (platform, username, external_id) DO UPDATE SET
	posted_at = excluded.posted_at,
	text = excluded.text
RETURNING id`,
		p.Platform, p.Username, p.ExternalID, nullTime(p.PostedAt), p.Text).Scan(&id)
	if err != nil {
		return fmt.Errorf("store: post upsert: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM post_media WHERE post_id = ?`, id); err != nil {
		return fmt.Errorf("store: post upsert: %w", err)
	}
	for _, m := range p.Media {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO post_media (post_id, url, kind) VALUES (?, ?, ?)`, id, m.URL, m.Kind); err != nil {
			return fmt.Errorf("store: post upsert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: post upsert: %w", err)
	}
	return nil
}

func (s *PostStore) CountByAccount(ctx context.Context, platform, username string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM posts WHERE platform = ? AND username = ?`, platform, username).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: post count: %w", err)
	}
	return count, nil
}

func (s *PostStore) ListByAccount(ctx context.Context, platform, username string, limit int) ([]Post, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, platform, username, external_id, posted_at, text
FROM posts WHERE platform = ? AND username = ?
ORDER BY posted_at DESC LIMIT ?`, platform, username, limit)
	if err != nil {
		return nil, fmt.Errorf("store: post list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: post list: %w", err)
	}
	if err := s.attachMedia(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchFullText returns account posts whose text matches an FTS5 query.
func (s *PostStore) SearchFullText(ctx context.Context, platform, username, query string, limit int) ([]Post, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT p.id, p.platform, p.username, p.external_id, p.posted_at, p.text
FROM posts_fts f JOIN posts p ON p.id = f.rowid
WHERE posts_fts MATCH ? AND p.platform = ? AND p.username = ?
ORDER BY rank LIMIT ?`, query, platform, username, limit)
	if err != nil {
		return nil, fmt.Errorf("store: post search: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Post
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: post search: %w", err)
	}
	if err := s.attachMedia(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostStore) attachMedia(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}
	stmt, err := s.db.PrepareContext(ctx,
		`SELECT url, kind FROM post_media WHERE post_id = ? ORDER BY url`)
	if err != nil {
		return fmt.Errorf("store: media load: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for i := range posts {
		rows, err := stmt.QueryContext(ctx, posts[i].ID)
		if err != nil {
			return fmt.Errorf("store: media load: %w", err)
		}
		for rows.Next() {
			var m PostMedia
			if err := rows.Scan(&m.URL, &m.Kind); err != nil {
				_ = rows.Close()
				return fmt.Errorf("store: media scan: %w", err)
			}
			posts[i].Media = append(posts[i].Media, m)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: media load: %w", err)
		}
		_ = rows.Close()
	}
	return nil
}

func scanPost(rs rowScanner) (Post, error) {
	var (
		p        Post
		postedAt sql.NullTime
	)
	if err := rs.Scan(&p.ID, &p.Platform, &p.Username, &p.ExternalID, &postedAt, &p.Text); err != nil {
		return Post{}, fmt.Errorf("store: post scan: %w", err)
	}
	if postedAt.Valid {
		p.PostedAt = postedAt.Time
	}
	return p, nil
}
