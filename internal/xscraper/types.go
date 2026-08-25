package xscraper

import "time"

type Photo struct {
	ID  string
	URL string
}

type Video struct {
	ID  string
	URL string
}

type GIF struct {
	ID  string
	URL string
}

type Tweet struct {
	ID         string
	Text       string
	IsRetweet  bool
	IsReply    bool
	TimeParsed time.Time
	Photos     []Photo
	Videos     []Video
	GIFs       []GIF
	URLs       []string
}

type gqlResponse struct {
	Data struct {
		User struct {
			Result struct {
				Timeline struct {
					Timeline struct {
						Instructions []struct {
							Type        string  `json:"type"`
							Entries     []entry `json:"entries"`
							ModuleItems []item  `json:"moduleItems"`
						} `json:"instructions"`
					} `json:"timeline"`
				} `json:"timeline"`
				TimelineV2 struct {
					Timeline struct {
						Instructions []struct {
							Type        string  `json:"type"`
							Entries     []entry `json:"entries"`
							ModuleItems []item  `json:"moduleItems"`
						} `json:"instructions"`
					} `json:"timeline"`
				} `json:"timeline_v2"`
			} `json:"result"`
		} `json:"user"`
	} `json:"data"`
}

type entry struct {
	EntryID string `json:"entryId"`
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
	EntryID string `json:"entryId"`
	Item    struct {
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
	FullText             string `json:"full_text"`
	IDStr                string `json:"id_str"`
	InReplyToStatusIDStr string `json:"in_reply_to_status_id_str"`
	CreatedAt            string `json:"created_at"`
	UserIDStr            string `json:"user_id_str"`
	Entities             struct {
		URLs []struct {
			ExpandedURL string `json:"expanded_url"`
		} `json:"urls"`
	} `json:"entities"`
	ExtendedEntities struct {
		Media []extendedMedia `json:"media"`
	} `json:"extended_entities"`
}

type extendedMedia struct {
	IDStr         string `json:"id_str"`
	Type          string `json:"type"`
	MediaURLHTTPS string `json:"media_url_https"`
	VideoInfo     struct {
		Variants []struct {
			Bitrate     int    `json:"bitrate"`
			ContentType string `json:"content_type"`
			URL         string `json:"url"`
		} `json:"variants"`
	} `json:"video_info"`
}
