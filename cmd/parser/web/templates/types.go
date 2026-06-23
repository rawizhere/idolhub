package templates

// GalleryFileData describes a downloaded media file
type GalleryFileData struct {
	Filename, Type, Date, URL, ThumbnailURL string
	Size                                    int64
}

// GalleryPostMediaFile describes a media file in a post
type GalleryPostMediaFile struct {
	Filename, URL, ThumbnailURL string
	IsVideo                     bool
}

// GalleryPostYoutubeURL describes a YouTube URL
type GalleryPostYoutubeURL struct {
	URL, VideoID string
}

// GalleryPostData describes a post with media and text
type GalleryPostData struct {
	TweetID, TweetIDSuffix, DateLabel, CleanText string
	LocalFiles                                   []GalleryPostMediaFile
	YoutubeURLs                                  []GalleryPostYoutubeURL
}
