package scraper

import (
	"errors"
	"testing"
)

func TestParseInstagramSearchMatchesExactHandle(t *testing.T) {
	body := `{"users":[{"user":{"username":"alice.fan","full_name":"Fan"}},{"user":{"username":"Alice","full_name":"Alice A","is_private":true,"follower_count":12}}]}`
	p, err := parseInstagramSearch([]byte(body), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "Alice" || p.Name != "Alice A" || !p.Private || p.Followers != 12 {
		t.Errorf("profile = %+v", p)
	}
	if _, err := parseInstagramSearch([]byte(body), "bob"); !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("missing handle err = %v, want ErrProfileNotFound", err)
	}
}
