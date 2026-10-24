package scraper

import "testing"

func TestYouTubeShortsURL(t *testing.T) {
	cases := map[string]string{
		"RESCENE_official":               "https://www.youtube.com/@RESCENE_official/shorts",
		"@dayeon_you":                    "https://www.youtube.com/@dayeon_you/shorts",
		"https://www.youtube.com/@x.y-z": "https://www.youtube.com/@x.y-z/shorts",
		"UCaHwGJXFb28TekZMf7iFg5Q":       "https://www.youtube.com/channel/UCaHwGJXFb28TekZMf7iFg5Q/shorts",
		"https://www.youtube.com/channel/UCabcd1234abcd1234abcd": "https://www.youtube.com/channel/UCabcd1234abcd1234abcd/shorts",
	}
	for in, want := range cases {
		if got := youtubeShortsURL(in); got != want {
			t.Errorf("youtubeShortsURL(%q) = %s, want %s", in, got, want)
		}
	}
}
