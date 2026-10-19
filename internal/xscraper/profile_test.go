package xscraper

import (
	"errors"
	"testing"
)

func TestParseProfileReadsBothShapes(t *testing.T) {
	current := `{"data":{"user":{"result":{"__typename":"User","rest_id":"42",
		"core":{"name":"Alice","screen_name":"alice"},"avatar":{"image_url":"https://pbs.twimg.com/a.jpg"},
		"privacy":{"protected":true},"legacy":{"statuses_count":10,"media_count":3,"followers_count":7}}}}}`
	p, err := parseProfile([]byte(current))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.ID != "42" || p.ScreenName != "alice" || p.Name != "Alice" || p.Avatar == "" || p.Tweets != 10 || p.Media != 3 || p.Followers != 7 || !p.Protected {
		t.Errorf("current shape = %+v", p)
	}

	legacy := `{"data":{"user":{"result":{"rest_id":"7","legacy":{"name":"Bob","screen_name":"bob","profile_image_url_https":"https://pbs.twimg.com/b.jpg"}}}}}`
	p, err = parseProfile([]byte(legacy))
	if err != nil || p.Name != "Bob" || p.ScreenName != "bob" || p.Avatar == "" {
		t.Errorf("legacy shape = %+v, %v", p, err)
	}
}

func TestParseProfileNotFound(t *testing.T) {
	for _, body := range []string{`{"data":{}}`, `{"data":{"user":{"result":{"__typename":"UserUnavailable","rest_id":"1"}}}}`} {
		if _, err := parseProfile([]byte(body)); !errors.Is(err, ErrNotFound) {
			t.Errorf("parseProfile(%s) err = %v, want ErrNotFound", body, err)
		}
	}
}
