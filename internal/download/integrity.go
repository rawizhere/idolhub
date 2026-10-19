package download

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ErrIncomplete marks a media file whose container structure ends before the declared data does.
var ErrIncomplete = errors.New("incomplete media file")

// CheckComplete reports whether a media file is structurally whole. The format comes from the magic bytes, not the extension; unknown formats pass.
func CheckComplete(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()

	head := make([]byte, 12)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return err
	}
	head = head[:n]

	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8}):
		return checkTail(f, size, []byte{0xFF, 0xD9}, 4096)
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return checkTail(f, size, []byte("IEND"), 64)
	case len(head) == 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		// RIFF size counts everything after the 8-byte header; odd sizes get one pad byte.
		want := int64(binary.LittleEndian.Uint32(head[4:8])) + 8
		if size < want {
			return fmt.Errorf("%w: webp has %d of %d bytes", ErrIncomplete, size, want)
		}
		return nil
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return checkISOBMFF(f, size, !imageBrands[string(head[8:12])])
	case size == 0:
		return fmt.Errorf("%w: empty file", ErrIncomplete)
	}
	return nil
}

// checkTail looks for an end marker in the last window bytes. JPEG entropy data never contains FFD9 (FF is byte-stuffed), so a hit means the EOI marker is there.
func checkTail(f *os.File, size int64, marker []byte, window int64) error {
	if window > size {
		window = size
	}
	buf := make([]byte, window)
	if _, err := f.ReadAt(buf, size-window); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !bytes.Contains(buf, marker) {
		return fmt.Errorf("%w: end marker %q missing", ErrIncomplete, marker)
	}
	return nil
}

// imageBrands are ISOBMFF major brands of still images (HEIF/AVIF): they carry a meta box, never moov.
var imageBrands = map[string]bool{"heic": true, "heix": true, "mif1": true, "msf1": true, "avif": true, "avis": true}

// checkISOBMFF walks the top-level boxes of an mp4/mov: a truncated file has a last box that claims bytes past EOF, or (for video) no moov at all.
func checkISOBMFF(f *os.File, size int64, needMoov bool) error {
	var off int64
	hdr := make([]byte, 16)
	hasMoov := false
	for off < size {
		if size-off < 8 {
			return fmt.Errorf("%w: %d stray bytes at offset %d", ErrIncomplete, size-off, off)
		}
		if _, err := f.ReadAt(hdr[:8], off); err != nil {
			return err
		}
		boxSize := int64(binary.BigEndian.Uint32(hdr[:4]))
		boxType := string(hdr[4:8])
		switch boxSize {
		case 0: // box runs to EOF
			boxSize = size - off
		case 1: // 64-bit largesize follows the type
			if _, err := f.ReadAt(hdr[8:16], off+8); err != nil {
				return fmt.Errorf("%w: largesize header cut at offset %d", ErrIncomplete, off)
			}
			boxSize = int64(binary.BigEndian.Uint64(hdr[8:16]))
		}
		if boxSize < 8 {
			return fmt.Errorf("%w: bad box size %d at offset %d", ErrIncomplete, boxSize, off)
		}
		if off+boxSize > size {
			return fmt.Errorf("%w: box %q at offset %d needs %d bytes, file has %d", ErrIncomplete, boxType, off, boxSize, size-off)
		}
		if boxType == "moov" {
			hasMoov = true
		}
		off += boxSize
	}
	if needMoov && !hasMoov {
		return fmt.Errorf("%w: no moov box", ErrIncomplete)
	}
	return nil
}

// AffectedTarget is a platform/username whose media dir had incomplete files moved away.
type AffectedTarget struct {
	Platform, Username string
}

const (
	integrityMarker = ".integrity_v1"
	// QuarantineDir sits under downloads/; dot dirs are skipped by the gallery and the migrations.
	QuarantineDir = ".quarantine"
)

// QuarantineIncomplete runs once per downloads root: incomplete media left by pre-atomic downloads is moved to downloads/.quarantine and its thumbnails are removed, so the next full sync downloads it again. It returns the targets that need that full sync.
func QuarantineIncomplete(root, thumbsDir string) ([]AffectedTarget, error) {
	marker := filepath.Join(root, integrityMarker)
	if _, err := os.Stat(marker); err == nil {
		return nil, nil
	}

	type suspect struct {
		path, rel, platform, username, name string
		reason                              error
	}
	var suspects []suspect
	checked := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "thumbnails") {
				return filepath.SkipDir
			}
			return nil
		}
		if inProgressName(name) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 {
			return nil
		}
		checked++
		if cerr := CheckComplete(path); errors.Is(cerr, ErrIncomplete) {
			suspects = append(suspects, suspect{path, rel, parts[0], parts[1], name, cerr})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Truncated downloads are rare; a large share means the checker misreads some format. Move nothing and leave the marker unset so a fixed build retries.
	if limit := max(50, checked/20); len(suspects) > limit {
		for _, s := range suspects[:min(len(suspects), 10)] {
			slog.Error("Integrity check sample", "file", s.rel, "reason", s.reason)
		}
		return nil, fmt.Errorf("integrity check flagged %d of %d files (limit %d); nothing moved, check the samples", len(suspects), checked, limit)
	}

	affected := map[AffectedTarget]bool{}
	moved := 0
	for _, s := range suspects {
		dst := filepath.Join(root, QuarantineDir, s.rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return nil, err
		}
		if err := os.Rename(s.path, dst); err != nil {
			slog.Warn("Failed to quarantine incomplete media", "file", s.path, "error", err)
			continue
		}
		removeThumbnails(root, thumbsDir, s.platform, s.username, s.name)
		moved++
		affected[AffectedTarget{Platform: s.platform, Username: s.username}] = true
		slog.Warn("Quarantined incomplete media file; it will be downloaded again", "file", s.rel, "reason", s.reason)
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0644); err != nil {
		return nil, err
	}

	out := make([]AffectedTarget, 0, len(affected))
	for t := range affected {
		out = append(out, t)
	}
	slog.Info("Media integrity check completed", "checked", checked, "quarantined", moved, "targets", len(out))
	return out, nil
}

// inProgressName skips files another writer may still own: our temp files, yt-dlp parts, transcoder outputs.
func inProgressName(name string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.HasSuffix(name, ".part") ||
		strings.HasSuffix(name, ".ytdl") ||
		strings.Contains(name, ".tmp.") ||
		strings.Contains(name, ".transcoding.") ||
		name == "posts.json" || strings.HasSuffix(name, ".bak")
}

// removeThumbnails drops every known preview location of a media file so a stale preview of the broken file is not served.
func removeThumbnails(root, thumbsDir, platform, username, name string) {
	thumb := strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
	paths := []string{filepath.Join(root, platform, username, "thumbnails", thumb)}
	if thumbsDir != "" {
		paths = append(paths,
			filepath.Join(thumbsDir, platform, username, "thumbnails", thumb),
			filepath.Join(thumbsDir, root, platform, username, "thumbnails", thumb),
		)
	}
	for _, p := range paths {
		_ = os.Remove(p)
	}
}
