package xscraper

// Tweet is one timeline item; the media lists hold download URLs.
type Tweet struct {
	ID        string
	Text      string
	IsRetweet bool
	Photos    []string
	Videos    []string
	GIFs      []string
	URLs      []string
}

// gqlTimeline is the timeline shape under both "timeline" and "timeline_v2".
type gqlTimeline struct {
	Timeline struct {
		Instructions []struct {
			Type        string  `json:"type"`
			Entries     []entry `json:"entries"`
			ModuleItems []item  `json:"moduleItems"`
		} `json:"instructions"`
	} `json:"timeline"`
}

type gqlResponse struct {
	Data struct {
		User struct {
			Result struct {
				Timeline   gqlTimeline `json:"timeline"`
				TimelineV2 gqlTimeline `json:"timeline_v2"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

type entry struct {
	Content struct {
		CursorType  string `json:"cursorType"`
		Value       string `json:"value"`
		ItemArray   []item `json:"items"`
		ItemContent struct {
			TweetResults struct {
				Result result `json:"result"`
			} `json:"tweet_results"`
		} `json:"itemContent"`
	} `json:"content"`
}

type item struct {
	Item struct {
		ItemContent struct {
			TweetResults struct {
				Result result `json:"result"`
			} `json:"tweet_results"`
		} `json:"itemContent"`
	} `json:"item"`
}

type result struct {
	Typename  string      `json:"__typename"`
	Legacy    legacyTweet `json:"legacy"`
	NoteTweet struct {
		NoteTweetResults struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
	Core struct {
		UserResults struct {
			Result struct {
				Legacy struct {
					ScreenName string `json:"screen_name"`
				} `json:"legacy"`
			} `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
	RetweetedStatusResult *struct {
		Result *result `json:"result"`
	} `json:"retweeted_status_result"`
	Tweet struct {
		Legacy legacyTweet `json:"legacy"`
		Core   struct {
			UserResults struct {
				Result struct {
					Legacy struct {
						ScreenName string `json:"screen_name"`
					} `json:"legacy"`
				} `json:"result"`
			} `json:"user_results"`
		} `json:"core"`
	} `json:"tweet"`
}

type legacyTweet struct {
	FullText string `json:"full_text"`
	IDStr    string `json:"id_str"`
	Entities struct {
		URLs []struct {
			ExpandedURL string `json:"expanded_url"`
		} `json:"urls"`
	} `json:"entities"`
	ExtendedEntities struct {
		Media []extendedMedia `json:"media"`
	} `json:"extended_entities"`
}

type videoVariant struct {
	Bitrate     int    `json:"bitrate"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}

type extendedMedia struct {
	Type          string `json:"type"`
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     struct {
		Variants []videoVariant `json:"variants"`
	} `json:"video_info"`
}
