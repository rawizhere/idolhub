package scraper

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	ytdlp "github.com/lrstanley/go-ytdlp"
)

func TestSnowflakeToTime(t *testing.T) {
	const epoch int64 = 1288834974657
	ms := time.Date(2023, 1, 2, 15, 4, 5, 0, time.UTC).UnixMilli()
	id := ((ms - epoch) << 22) + 123
	got, err := snowflakeToTime(strconv.FormatInt(id, 10))
	if err != nil {
		t.Fatal(err)
	}
	if got.UnixMilli() != ms {
		t.Errorf("snowflakeToTime = %v, want ms %d", got, ms)
	}
	if _, err := snowflakeToTime("not-a-number"); err == nil {
		t.Error("expected error for non-numeric id")
	}
}

func TestCollectYTDLPPosts(t *testing.T) {
	raw := []byte(`[
	  {
	    "id": "playlist1",
	    "entries": [
	      {"id": "vid1", "title": "Title One", "description": "Desc One", "upload_date": "20230102"},
	      {"id": "vid2", "title": "Same", "description": "Same", "upload_date": "bad-date"},
	      null,
	      {"id": "", "title": "no id"},
	      {"id": "vid3"}
	    ]
	  }
	]`)
	var infos []*ytdlp.ExtractedInfo
	if err := json.Unmarshal(raw, &infos); err != nil {
		t.Fatal(err)
	}
	posts := collectYTDLPPosts(infos, "tiktok")
	if len(posts) != 3 {
		t.Fatalf("got %d posts, want 3", len(posts))
	}
	first := posts[0]
	if first.Platform != "tiktok" || first.ExternalID != "vid1" {
		t.Errorf("post[0] = %+v", first)
	}
	want := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	if !first.PostedAt.Equal(want) {
		t.Errorf("post[0].PostedAt = %v, want %v", first.PostedAt, want)
	}
	if first.Text != "Title One\n\nDesc One" {
		t.Errorf("post[0].Text = %q", first.Text)
	}
	if len(first.Media) != 1 || first.Media[0].URL != "vid1" || first.Media[0].Kind != "video" {
		t.Errorf("post[0].Media = %+v", first.Media)
	}
	if posts[1].Text != "Same" || !posts[1].PostedAt.IsZero() {
		t.Errorf("post[1] = %+v", posts[1])
	}
	if posts[2].Text != "" {
		t.Errorf("post[2].Text = %q", posts[2].Text)
	}
}
