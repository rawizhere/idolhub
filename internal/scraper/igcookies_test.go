package scraper

import (
	"strings"
	"testing"
)

func TestParseNetscapeCookies(t *testing.T) {
	raw := "# Netscape HTTP Cookie File\n" +
		"#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t1804283143\tdatr\tFAKE-DATR-VALUE-0000\n" +
		".instagram.com\tTRUE\t/\tTRUE\t1797416177\tds_user_id\t98765432100\n" +
		"#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t1821176162\tsessionid\tabc%3Ad ef\n" +
		".example.com\tTRUE\t/\tTRUE\t1797416177\tother\tx\n" +
		"\n"
	cookies, err := parseNetscapeCookies(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 3 {
		t.Fatalf("want 3 instagram cookies, got %d", len(cookies))
	}
	byName := map[string]string{}
	for _, c := range cookies {
		byName[c.Name] = c.Value
	}
	if byName["datr"] != "FAKE-DATR-VALUE-0000" {
		t.Errorf("datr = %q", byName["datr"])
	}
	if byName["ds_user_id"] != "98765432100" {
		t.Errorf("ds_user_id = %q", byName["ds_user_id"])
	}
	if !strings.Contains(byName["sessionid"], "%3A") {
		t.Errorf("sessionid must keep url encoding, got %q", byName["sessionid"])
	}
	if _, ok := byName["other"]; ok {
		t.Errorf("non-instagram cookie must be filtered out")
	}
}

func TestParseNetscapeCookiesEmpty(t *testing.T) {
	if _, err := parseNetscapeCookies("# just a comment\n\n"); err == nil {
		t.Fatal("want error for empty cookie set")
	}
}
