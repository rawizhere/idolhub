package xscraper

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite golden files")

type tweetSnapshot struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	RT     bool     `json:"is_retweet,omitempty"`
	Date   string   `json:"date"`
	Photos []string `json:"photos,omitempty"`
	Videos []string `json:"videos,omitempty"`
	GIFs   []string `json:"gifs,omitempty"`
	URLs   []string `json:"urls,omitempty"`
}

type timelineSnapshot struct {
	Cursor string          `json:"cursor"`
	Tweets []tweetSnapshot `json:"tweets"`
}

func snapshot(tweets []*Tweet, cursor string) ([]byte, error) {
	s := timelineSnapshot{Cursor: cursor, Tweets: []tweetSnapshot{}}
	for _, tw := range tweets {
		ts := tweetSnapshot{ID: tw.ID, Text: tw.Text, RT: tw.IsRetweet, Date: formatUTC(tw.TimeParsed)}
		for _, p := range tw.Photos {
			ts.Photos = append(ts.Photos, p.URL)
		}
		for _, v := range tw.Videos {
			ts.Videos = append(ts.Videos, v.URL)
		}
		for _, g := range tw.GIFs {
			ts.GIFs = append(ts.GIFs, g.URL)
		}
		ts.URLs = tw.URLs
		s.Tweets = append(s.Tweets, ts)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func formatUTC(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func TestParseTimelineGolden(t *testing.T) {
	for _, name := range []string{"conversation", "profile"} {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			tweets, cursor, err := parseTimeline(body)
			if err != nil {
				t.Fatal(err)
			}
			got, err := snapshot(tweets, cursor)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(got)) {
				t.Errorf("golden mismatch\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}

func TestBestVariant(t *testing.T) {
	variants := []struct {
		Bitrate     int    `json:"bitrate"`
		ContentType string `json:"content_type"`
		URL         string `json:"url"`
	}{
		{632000, "video/mp4", "https://video.twimg.com/small.mp4?tag=10"},
		{2176000, "video/mp4", "https://video.twimg.com/big.mp4?tag=10"},
		{256000, "application/x-mpegURL", "https://video.twimg.com/pl.m3u8"},
		{3200000, "video/mp4", ""},
	}
	if got := bestVariant(variants, false); got != "https://video.twimg.com/big.mp4" {
		t.Errorf("bestVariant(video) = %q", got)
	}
	if got := bestVariant(variants[:1], false); got != "https://video.twimg.com/small.mp4" {
		t.Errorf("bestVariant(single) = %q", got)
	}
	if got := bestVariant(nil, true); got != "" {
		t.Errorf("bestVariant(empty) = %q", got)
	}
	gifVariants := []struct {
		Bitrate     int    `json:"bitrate"`
		ContentType string `json:"content_type"`
		URL         string `json:"url"`
	}{
		{0, "image/gif", "https://video.twimg.com/g.gif"},
		{1000000, "video/mp4", "https://video.twimg.com/g.mp4"},
	}
	if got := bestVariant(gifVariants, true); got != "https://video.twimg.com/g.mp4" {
		t.Errorf("bestVariant(gif) = %q", got)
	}
}

func TestParseCreatedAt(t *testing.T) {
	tm := parseCreatedAt("Mon Jan 02 15:04:05 +0000 2023")
	if tm.Year() != 2023 || tm.Month() != time.January || tm.Day() != 2 || tm.Hour() != 15 {
		t.Errorf("parseCreatedAt = %v", tm)
	}
	if !parseCreatedAt("garbage").IsZero() {
		t.Error("parseCreatedAt should return zero time on bad input")
	}
}

func TestParseUserID(t *testing.T) {
	id, err := parseUserID([]byte(`{"data":{"user":{"result":{"rest_id":"12345"}}}}`))
	if err != nil || id != "12345" {
		t.Errorf("parseUserID = %q, %v", id, err)
	}
	if _, err := parseUserID([]byte(`{"data":{"user":{"result":{}}}}`)); err == nil {
		t.Error("expected error for empty rest_id")
	}
	if _, err := parseUserID([]byte(`not json`)); err == nil {
		t.Error("expected error for invalid json")
	}
}

func TestTimelineFeatures(t *testing.T) {
	f := timelineFeatures()
	if len(f) == 0 {
		t.Fatal("timelineFeatures returned empty map")
	}
	for k, v := range f {
		if k == "" {
			t.Error("empty feature key")
		}
		switch v.(type) {
		case bool:
		default:
			t.Errorf("feature %s has non-bool value %v", k, v)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"17":   17 * time.Second,
		"  5 ": 5 * time.Second,
		"":     0,
		"abc":  0,
		"-3":   0,
	}
	for in, want := range cases {
		if got := parseRetryAfter(in); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}
