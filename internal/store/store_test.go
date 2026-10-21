package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestPostStoreUpsertIdempotency(t *testing.T) {
	tests := []struct {
		name      string
		first     Post
		second    Post
		wantCount int
		wantText  string
		wantMedia []string
	}{
		{
			name: "same post keeps one row",
			first: Post{
				Platform: "twitter", Username: "alice", ExternalID: "1",
				PostedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
				Text:     "hello world",
			},
			second: Post{
				Platform: "twitter", Username: "alice", ExternalID: "1",
				Text: "hello world edited",
			},
			wantCount: 1,
			wantText:  "hello world edited",
		},
		{
			name: "media is replaced on re-upsert",
			first: Post{
				Platform: "twitter", Username: "bob", ExternalID: "2",
				Text:  "with media",
				Media: []PostMedia{{URL: "https://x/a.jpg", Kind: "photo"}, {URL: "https://x/b.mp4", Kind: "video"}},
			},
			second: Post{
				Platform: "twitter", Username: "bob", ExternalID: "2",
				Text:  "with media",
				Media: []PostMedia{{URL: "https://x/b.mp4", Kind: "video"}},
			},
			wantCount: 1,
			wantText:  "with media",
			wantMedia: []string{"https://x/b.mp4"},
		},
		{
			name: "distinct external ids create separate rows",
			first: Post{
				Platform: "twitter", Username: "carol", ExternalID: "3", Text: "one",
			},
			second: Post{
				Platform: "twitter", Username: "carol", ExternalID: "4", Text: "two",
			},
			wantCount: 2,
			wantText:  "two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := openTestStore(t)

			if err := s.Posts.UpsertPost(ctx, tt.first); err != nil {
				t.Fatalf("first upsert: %v", err)
			}
			if err := s.Posts.UpsertPost(ctx, tt.second); err != nil {
				t.Fatalf("second upsert: %v", err)
			}

			count, err := s.Posts.CountByAccount(ctx, tt.second.Platform, tt.second.Username)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tt.wantCount {
				t.Fatalf("count = %d, want %d", count, tt.wantCount)
			}

			got, err := s.Posts.ListByAccount(ctx, tt.second.Platform, tt.second.Username, 10)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("no posts returned")
			}
			var found bool
			for _, p := range got {
				if p.ExternalID == tt.second.ExternalID {
					found = true
					if p.Text != tt.wantText {
						t.Errorf("text = %q, want %q", p.Text, tt.wantText)
					}
					urls := make([]string, 0, len(p.Media))
					for _, m := range p.Media {
						urls = append(urls, m.URL)
					}
					if !equalStrings(urls, tt.wantMedia) {
						t.Errorf("media urls = %v, want %v", urls, tt.wantMedia)
					}
				}
			}
			if !found {
				t.Errorf("external id %q not found in results", tt.second.ExternalID)
			}
		})
	}
}

func TestAccountStoreRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   Account
	}{
		{
			name: "full account",
			in: Account{
				Platform: "twitter", Username: "alice",
				SaveText: true, SkipRetweets: true,
				DownloadPhotos: boolPtr(false), DownloadVideos: boolPtr(true),
				Filters: []string{"giveaway", "art"},
				Cookies: "x.com\tTRUE\t/\tTRUE\t0\tauth_token\ttok\n",
			},
		},
		{
			name: "nil booleans and no filters",
			in: Account{
				Platform: "instagram", Username: "bob",
			},
		},
		{
			name: "upsert overwrites fields",
			in: Account{
				Platform: "instagram", Username: "bob",
				SaveText: true,
			},
		},
	}

	ctx := context.Background()
	s := openTestStore(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Accounts.Upsert(ctx, tt.in); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			got, err := s.Accounts.List(ctx)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			var found *Account
			for i := range got {
				if got[i].Platform == tt.in.Platform && got[i].Username == tt.in.Username {
					found = &got[i]
				}
			}
			if found == nil {
				t.Fatalf("account %s/%s not found", tt.in.Platform, tt.in.Username)
			}
			if found.SaveText != tt.in.SaveText || found.SkipRetweets != tt.in.SkipRetweets {
				t.Errorf("bools = %+v, want %+v", found, tt.in)
			}
			if !sameBoolPtr(found.DownloadPhotos, tt.in.DownloadPhotos) ||
				!sameBoolPtr(found.DownloadVideos, tt.in.DownloadVideos) {
				t.Errorf("nullable bools mismatch: got %+v, want %+v", found, tt.in)
			}
			if found.Cookies != tt.in.Cookies {
				t.Errorf("cookies = %q, want %q", found.Cookies, tt.in.Cookies)
			}
			if !equalStrings(found.Filters, tt.in.Filters) {
				t.Errorf("filters = %v, want %v", found.Filters, tt.in.Filters)
			}
		})
	}
}

func TestAccountSyncInfoRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	at := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC).Truncate(time.Second)
	if err := s.Accounts.Upsert(ctx, Account{Platform: "twitter", Username: "dave"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := s.Accounts.SetSyncInfo(ctx, "twitter", "dave", "ok", at); err != nil {
		t.Fatalf("set sync info: %v", err)
	}
	info, err := s.Accounts.GetSyncInfo(ctx, "twitter", "dave")
	if err != nil {
		t.Fatalf("get sync info: %v", err)
	}
	if info.Status != "ok" || !info.Time.Equal(at) {
		t.Errorf("info = %+v, want status ok at %v", info, at)
	}

	if err := s.Accounts.Upsert(ctx, Account{Platform: "twitter", Username: "erin"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	info, err = s.Accounts.GetSyncInfo(ctx, "twitter", "erin")
	if err != nil {
		t.Fatalf("get sync info: %v", err)
	}
	if info.Status != "idle" || !info.Time.IsZero() {
		t.Errorf("default info = %+v, want idle with zero time", info)
	}

	info, err = s.Accounts.GetSyncInfo(ctx, "twitter", "missing")
	if err != nil {
		t.Fatalf("get sync info for missing account: %v", err)
	}
	if info.Status != "idle" || !info.Time.IsZero() {
		t.Errorf("missing account info = %+v, want idle with zero time", info)
	}
}

func TestSettingsStore(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, err := s.Settings.Get(ctx, "cursor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := s.Settings.Set(ctx, "cursor", `{"after":"abc"}`); err != nil {
		t.Fatalf("set: %v", err)
	}
	value, err := s.Settings.Get(ctx, "cursor")
	if err != nil || value != `{"after":"abc"}` {
		t.Fatalf("get = %q, %v", value, err)
	}
}
func TestSearchFullText(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	posts := []Post{
		{Platform: "twitter", Username: "alice", ExternalID: "1", Text: "quick brown fox"},
		{Platform: "twitter", Username: "alice", ExternalID: "2", Text: "lazy dog sleeps"},
		{Platform: "twitter", Username: "bob", ExternalID: "3", Text: "brown bear roams"},
	}
	for _, p := range posts {
		if err := s.Posts.UpsertPost(ctx, p); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got, err := s.Posts.SearchFullText(ctx, "twitter", "alice", "brown", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0] != "1" {
		t.Fatalf("search = %+v, want only alice's brown fox", got)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func boolPtr(b bool) *bool { return &b }

// A status write for a removed account must not recreate its row (it came back after restart).
func TestSetSyncInfoDoesNotResurrectAccount(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.Accounts.SetSyncInfo(ctx, "twitter", "ghost", "completed", time.Now()); err != nil {
		t.Fatalf("set sync info: %v", err)
	}
	rows, err := s.Accounts.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("accounts = %+v, want none", rows)
	}
}

// 00005 turns the bare-token settings into cookie lines without changing which session a scraper uses.
func TestLegacyTokensMigrate(t *testing.T) {
	cases := []struct {
		export, name, value string
		override            bool
		want                string
	}{
		{"", "auth_token", "tok", true, "# Netscape HTTP Cookie File\n.x.com\tTRUE\t/\tTRUE\t2147483647\tauth_token\ttok\n"},
		{".x.com\tTRUE\t/\tTRUE\t1\tauth_token\told\n.x.com\tTRUE\t/\tTRUE\t1\tkdt\tk\n", "auth_token", "new", true,
			".x.com\tTRUE\t/\tTRUE\t1\tauth_token\tnew\n.x.com\tTRUE\t/\tTRUE\t1\tkdt\tk\n"},
		{".instagram.com\tTRUE\t/\tTRUE\t1\tsessionid\tkeep\n", "sessionid", "legacy", false, ".instagram.com\tTRUE\t/\tTRUE\t1\tsessionid\tkeep\n"},
	}
	for _, c := range cases {
		domain := ".x.com"
		if c.name == "sessionid" {
			domain = ".instagram.com"
		}
		if got := withCookie(c.export, domain, c.name, c.value, c.override); got != c.want {
			t.Errorf("withCookie(%q) = %q, want %q", c.export, got, c.want)
		}
	}
}

func TestLegacyTokensMigration(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "old.db")+"?"+dsnOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 4); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO settings (key, value) VALUES ('twitter_auth_token', '"TW"'), ('instagram_session_id', '"IG"')`,
		`INSERT INTO accounts (platform, username, twitter_auth_token) VALUES ('twitter', 'own', 'ACC')`,
		`INSERT INTO accounts (platform, username) VALUES ('twitter', 'shared')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db, Accounts: &AccountStore{db: db}, Settings: &SettingsStore{db: db}}
	ctx := context.Background()
	if v, _ := s.Settings.Get(ctx, "twitter_cookies"); !strings.Contains(v, `auth_token\tTW`) {
		t.Errorf("twitter_cookies = %s", v)
	}
	if v, _ := s.Settings.Get(ctx, "instagram_cookies"); !strings.Contains(v, `sessionid\tIG`) {
		t.Errorf("instagram_cookies = %s", v)
	}
	if _, err := s.Settings.Get(ctx, "twitter_auth_token"); !errors.Is(err, ErrNotFound) {
		t.Errorf("legacy key left behind: %v", err)
	}
	accs, err := s.Accounts.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accs {
		switch a.Username {
		case "own":
			if !strings.Contains(a.Cookies, "auth_token\tACC") || strings.Contains(a.Cookies, "TW") {
				t.Errorf("own cookies = %q", a.Cookies)
			}
		case "shared":
			if a.Cookies != "" {
				t.Errorf("shared account got cookies %q", a.Cookies)
			}
		}
	}
}
