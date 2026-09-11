package gallery

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0:        "0 B",
		999:      "999 B",
		1000:     "1.0 kB",
		1537:     "1.5 kB",
		1048576:  "1.0 MB",
		28110210: "28.1 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
