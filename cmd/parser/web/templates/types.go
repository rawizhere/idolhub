package templates

type GalleryFileData struct {
	Filename     string
	Type         string
	Date         string
	Size         int64
	URL          string
	ThumbnailURL string
}

type GalleryPostMediaFile struct {
	Filename     string
	URL          string
	ThumbnailURL string
	IsVideo      bool
}

type GalleryPostYoutubeURL struct {
	URL     string
	VideoID string
}

type GalleryPostData struct {
	TweetID       string
	TweetIDSuffix string
	DateLabel     string
	CleanText     string
	LocalFiles    []GalleryPostMediaFile
	YoutubeURLs   []GalleryPostYoutubeURL
}
