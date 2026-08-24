package gallery

import (
	"path/filepath"
	"strings"

	"github.com/dustin/go-humanize"
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
			SizeHuman:    humanize.Bytes(uint64(e.Size)),
			URL:          base + "/" + e.Filename,
			ThumbnailURL: base + "/thumbnails/" + thumb,
		})
	}
	return files
}
