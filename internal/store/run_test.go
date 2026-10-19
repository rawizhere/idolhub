package store

import (
	"context"
	"testing"
	"time"
)

func TestAccountControlsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	in := Account{Platform: "twitter", Username: "alice", Paused: true, Cookies: "# Netscape", SinceDate: "2024-01-31", MaxPosts: 200}
	if err := s.Accounts.Upsert(ctx, in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.Accounts.List(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("list = %v, %v", got, err)
	}
	a := got[0]
	if !a.Paused || a.Cookies != in.Cookies || a.SinceDate != in.SinceDate || a.MaxPosts != 200 {
		t.Errorf("round-trip = %+v", a)
	}
}

func TestRenameMovesPostsAndRuns(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.Accounts.Upsert(ctx, Account{Platform: "twitter", Username: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Posts.UpsertPost(ctx, Post{Platform: "twitter", Username: "old", ExternalID: "1", Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs.Add(ctx, Run{Platform: "twitter", Username: "old", StartedAt: time.Now(), FinishedAt: time.Now(), Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts.Rename(ctx, "twitter", "old", "new"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	accs, _ := s.Accounts.List(ctx)
	if len(accs) != 1 || accs[0].Username != "new" {
		t.Errorf("accounts after rename = %+v", accs)
	}
	if n, _ := s.Posts.CountByAccount(ctx, "twitter", "new"); n != 1 {
		t.Errorf("posts under new name = %d, want 1", n)
	}
	if runs, _ := s.Runs.List(ctx, "twitter", "new", 10); len(runs) != 1 {
		t.Errorf("runs under new name = %d, want 1", len(runs))
	}
}

func TestRunsArePrunedAndNewestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < runsKept+5; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		if err := s.Runs.Add(ctx, Run{Platform: "tiktok", Username: "u", StartedAt: at, FinishedAt: at, Status: "completed", NewFiles: i}); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.Runs.List(ctx, "tiktok", "u", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != runsKept {
		t.Fatalf("kept %d runs, want %d", len(runs), runsKept)
	}
	if runs[0].NewFiles != runsKept+4 {
		t.Errorf("newest run first: got new_files=%d", runs[0].NewFiles)
	}
}
