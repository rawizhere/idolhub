package gallery

import "testing"

func TestURLBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=large": "abc.jpg",
		"https://scontent.cdninstagram.com/v/t51/123_n.mp4?efg=1":   "123_n.mp4",
		"https://example.com/dir/":                                  "",
		"https://example.com":                                       "",
	} {
		if got := urlBase(in); got != want {
			t.Errorf("urlBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKeepAndCache(t *testing.T) {
	got := Keep([]int{1, 2, 3, 4}, func(n int) bool { return n%2 == 0 })
	if len(got) != 2 || got[0] != 2 || got[1] != 4 {
		t.Errorf("Keep = %v", got)
	}
	var c ttlCache[int]
	if _, ok := c.Get("a"); ok {
		t.Error("empty cache hit")
	}
	c.Add("a", 1)
	if v, ok := c.Get("a"); !ok || v != 1 {
		t.Errorf("Get = %v, %v", v, ok)
	}
	c.Remove("a")
	if _, ok := c.Get("a"); ok {
		t.Error("removed entry still cached")
	}
}
