package download

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func box(typ string, payload []byte) []byte {
	b := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
	copy(b[4:], typ)
	return append(b, payload...)
}

func encodeJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func encodePNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func webp(declared uint32, body int) []byte {
	b := []byte("RIFF\x00\x00\x00\x00WEBP")
	binary.LittleEndian.PutUint32(b[4:8], declared)
	return append(b, make([]byte, body)...)
}

func TestCheckComplete(t *testing.T) {
	jpg := encodeJPEG(t)
	pngData := encodePNG(t)
	mp4 := append(append(box("ftyp", []byte("isom\x00\x00\x02\x00")), box("moov", make([]byte, 40))...), box("mdat", make([]byte, 100))...)
	heic := append(box("ftyp", []byte("heic\x00\x00\x00\x00")), box("meta", make([]byte, 20))...)

	tests := []struct {
		name       string
		data       []byte
		incomplete bool
	}{
		{"jpeg whole", jpg, false},
		{"jpeg truncated", jpg[:len(jpg)/2], true},
		{"png whole", pngData, false},
		{"png truncated", pngData[:len(pngData)-20], true},
		{"webp whole", webp(4+20, 20), false},
		{"webp truncated", webp(4+200, 20), true},
		{"mp4 whole", mp4, false},
		{"mp4 truncated mdat", mp4[:len(mp4)-30], true},
		{"mp4 without moov", append(box("ftyp", []byte("isom\x00\x00\x02\x00")), box("mdat", make([]byte, 10))...), true},
		{"heic without moov", heic, false},
		{"empty", nil, true},
		{"unknown format", []byte("plain text, not media"), false},
	}
	dir := t.TempDir()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(dir, string(rune('a'+i)))
			if err := os.WriteFile(p, tt.data, 0644); err != nil {
				t.Fatal(err)
			}
			err := CheckComplete(p)
			if got := errors.Is(err, ErrIncomplete); got != tt.incomplete {
				t.Errorf("incomplete = %v (err %v), want %v", got, err, tt.incomplete)
			}
		})
	}
}

func TestQuarantineIncomplete(t *testing.T) {
	root := filepath.Join(t.TempDir(), "downloads")
	thumbs := t.TempDir()
	jpg := encodeJPEG(t)
	write := func(rel string, data []byte) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("twitter/idol/good.jpg", jpg)
	bad := write("instagram/idol/bad.jpg", jpg[:len(jpg)/2])
	part := write("instagram/idol/clip.mp4.part", []byte("\x00\x00\x00\x20ftypisom"))
	legacyThumb := write("instagram/idol/thumbnails/bad.jpg", jpg)
	fastThumb := filepath.Join(thumbs, "instagram", "idol", "thumbnails", "bad.jpg")
	_ = os.MkdirAll(filepath.Dir(fastThumb), 0755)
	_ = os.WriteFile(fastThumb, jpg, 0644)

	affected, err := QuarantineIncomplete(root, thumbs)
	if err != nil {
		t.Fatal(err)
	}
	if len(affected) != 1 || affected[0] != (AffectedTarget{Platform: "instagram", Username: "idol"}) {
		t.Fatalf("affected = %+v, want instagram/idol only", affected)
	}
	for _, p := range []string{bad, legacyThumb, fastThumb} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still present", p)
		}
	}
	for _, p := range []string{good, part, filepath.Join(root, QuarantineDir, "instagram", "idol", "bad.jpg")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}

	// Runs once: the marker makes the second call a no-op.
	write("instagram/idol/bad2.jpg", jpg[:10])
	affected, err = QuarantineIncomplete(root, thumbs)
	if err != nil || len(affected) != 0 {
		t.Errorf("second run = %+v, %v; want no-op", affected, err)
	}
}

// A flood of "incomplete" files means the checker is wrong: nothing may be moved.
func TestQuarantineIncompleteRefusesMassFlagging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "downloads")
	dir := filepath.Join(root, "twitter", "idol")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".jpg"), []byte{0xFF, 0xD8, 0x00}, 0644)
	}
	if _, err := QuarantineIncomplete(root, ""); err == nil {
		t.Fatal("want error on mass flagging")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 60 {
		t.Errorf("files left = %d, want 60", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, integrityMarker)); !os.IsNotExist(err) {
		t.Error("marker written despite refusal")
	}
}
