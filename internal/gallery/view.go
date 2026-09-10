package gallery

import (
	"fmt"
	"path/filepath"
	"strings"
)

type File struct {
	Filename     string `json:"filename"`
	Type         string `json:"type"`
	Date         string `json:"date"`
	Size         int64  `json:"size"`
	SizeHuman    string `json:"size_human"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// View returns populated file cards for a target.
func (idx *Index) View(platform, username string) []File {
	entries := idx.Get(platform, username)
	files := make([]File, 0, len(entries))
	base := "/media/" + platform + "/" + username
	for _, e := range entries {
		date := ""
		if len(e.Filename) >= 10 {
			date = e.Filename[:10]
		}
		thumb := strings.TrimSuffix(e.Filename, filepath.Ext(e.Filename)) + ".jpg"
		files = append(files, File{
			Filename:     e.Filename,
			Type:         e.Type,
			Date:         date,
			Size:         e.Size,
			SizeHuman:    humanBytes(e.Size),
			URL:          base + "/" + e.Filename,
			ThumbnailURL: base + "/thumbnails/" + thumb,
		})
	}
	return files
}

// humanBytes formats a byte count in SI units (like humanize.Bytes).
func humanBytes(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}
