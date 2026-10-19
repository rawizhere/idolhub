package cookies

import (
	"strings"
	"testing"
)

func TestParseNetscape(t *testing.T) {
	raw := "# Netscape HTTP Cookie File\n" +
		"#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t1804283143\tdatr\tFAKE-DATR-VALUE-0000\n" +
		".instagram.com\tTRUE\t/\tTRUE\t1797416177\tds_user_id\t98765432100\n" +
		"#HttpOnly_.instagram.com\tTRUE\t/\tTRUE\t1821176162\tsessionid\tabc%3Ad ef\n" +
		".example.com\tTRUE\t/\tTRUE\t1797416177\tother\tx\n" +
		"\n"
	cookies, err := ParseNetscape(raw, "instagram.com")
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
		t.Errorf("non-matching domain cookie must be filtered out")
	}
}

func TestParseNetscapeEmpty(t *testing.T) {
	if _, err := ParseNetscape("# just a comment\n\n", "instagram.com"); err == nil {
		t.Fatal("want error for empty cookie set")
	}
}

func TestJSONToNetscapeRoundTrip(t *testing.T) {
	raw := `[
		{"domain":".x.com","expirationDate":1804283143.5,"hostOnly":false,"httpOnly":true,"name":"auth_token","path":"/","secure":true,"value":"tok"},
		{"domain":"x.com","expirationDate":0,"hostOnly":true,"httpOnly":false,"name":"lang","path":"","secure":false,"value":""}
	]`
	netscape, err := JSONToNetscape(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cookies, err := ParseNetscape(netscape, "x.com")
	if err != nil {
		t.Fatalf("parse converted export: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("want 2 cookies, got %d", len(cookies))
	}
	tok := cookies[0]
	if tok.Name != "auth_token" || tok.Value != "tok" || !tok.HttpOnly || !tok.Secure || tok.Expires.Unix() != 1804283143 {
		t.Errorf("auth_token round-trip = %+v", tok)
	}
	if cookies[1].Value != "" || cookies[1].Path != "/" {
		t.Errorf("lang round-trip = %+v", cookies[1])
	}
}

func TestJSONToNetscapeRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"[", "[]", `{"name":"x"}`} {
		if _, err := JSONToNetscape(raw); err == nil {
			t.Errorf("JSONToNetscape(%q) succeeded, want error", raw)
		}
	}
}
