package gallery

import (
	"path/filepath"
	"strings"
)

type File struct {
	Filename     string `json:"filename"`
	Type         string `json:"type"`
	Date         string `json:"date"`
	Size         int64  `json:"size"`
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
			URL:          base + "/" + e.Filename,
			ThumbnailURL: base + "/thumbnails/" + thumb,
		})
	}
	return files
}
