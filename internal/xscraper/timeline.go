package xscraper

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// parseTimeline extracts tweets and the bottom cursor from a timeline response.
func parseTimeline(body []byte) ([]*Tweet, string, error) {
	var resp gqlResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", err
	}

	instructions := resp.Data.User.Result.Timeline.Timeline.Instructions
	if len(instructions) == 0 {
		instructions = resp.Data.User.Result.TimelineV2.Timeline.Instructions
	}

	var tweets []*Tweet
	cursor := ""
	for _, instruction := range instructions {
		for _, e := range instruction.Entries {
			if e.Content.CursorType == "Bottom" {
				cursor = e.Content.Value
				continue
			}
			if r := e.Content.ItemContent.TweetResults.Result; r.Legacy.IDStr != "" {
				if tw := r.parse(); tw != nil {
					tweets = append(tweets, tw)
				}
			}
			for _, it := range e.Content.ItemArray {
				if r := it.Item.ItemContent.TweetResults.Result; r.Legacy.IDStr != "" {
					if tw := r.parse(); tw != nil {
						tweets = append(tweets, tw)
					}
				}
			}
		}
		for _, it := range instruction.ModuleItems {
			if r := it.Item.ItemContent.TweetResults.Result; r.Legacy.IDStr != "" {
				if tw := r.parse(); tw != nil {
					tweets = append(tweets, tw)
				}
			}
		}
	}
	return tweets, cursor, nil
}

func (r *result) parse() *Tweet {
	legacy := &r.Legacy
	if r.Typename == "TweetWithVisibilityResults" {
		legacy = &r.Tweet.Legacy
	}
	if legacy.IDStr == "" {
		return nil
	}

	text := legacy.FullText
	if text == "" && r.NoteTweet.NoteTweetResults.Result.Text != "" {
		text = r.NoteTweet.NoteTweetResults.Result.Text
	}

	tw := &Tweet{
		ID:         legacy.IDStr,
		Text:       text,
		IsReply:    legacy.InReplyToStatusIDStr != "",
		TimeParsed: parseCreatedAt(legacy.CreatedAt),
	}

	retweeted := false
	if r.RetweetedStatusResult != nil && r.RetweetedStatusResult.Result != nil {
		retweeted = true
		if inner := r.RetweetedStatusResult.Result.parse(); inner != nil {
			tw.Photos = inner.Photos
			tw.Videos = inner.Videos
			tw.GIFs = inner.GIFs
		}
	}
	tw.IsRetweet = retweeted || strings.HasPrefix(text, "RT @")

	for _, m := range legacy.ExtendedEntities.Media {
		switch m.Type {
		case "photo":
			tw.Photos = append(tw.Photos, Photo{ID: m.IDStr, URL: m.MediaURLHTTPS})
		case "video":
			v := Video{ID: m.IDStr, URL: bestVariant(m.VideoInfo.Variants, false)}
			if v.URL != "" {
				tw.Videos = append(tw.Videos, v)
			}
		case "animated_gif":
			g := GIF{ID: m.IDStr, URL: bestVariant(m.VideoInfo.Variants, true)}
			if g.URL != "" {
				tw.GIFs = append(tw.GIFs, g)
			}
		}
	}

	for _, u := range legacy.Entities.URLs {
		tw.URLs = append(tw.URLs, u.ExpandedURL)
	}
	return tw
}

func bestVariant(variants []struct {
	Bitrate     int    `json:"bitrate"`
	ContentType string `json:"content_type"`
	URL         string `json:"url"`
}, gif bool) string {
	best := ""
	maxBitrate := 0
	for _, v := range variants {
		if !gif && v.ContentType != "video/mp4" {
			continue
		}
		if gif && v.Bitrate < maxBitrate {
			continue
		}
		if v.Bitrate >= maxBitrate && v.URL != "" {
			best = strings.TrimSuffix(v.URL, "?tag=10")
			maxBitrate = v.Bitrate
		}
	}
	return best
}

func parseUserID(body []byte) (string, error) {
	var resp struct {
		Data struct {
			User struct {
				Result struct {
					RestID string `json:"rest_id"`
				} `json:"result"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}
	if resp.Data.User.Result.RestID == "" {
		return "", fmt.Errorf("empty user id in response")
	}
	return resp.Data.User.Result.RestID, nil
}

func parseCreatedAt(s string) time.Time {
	t, _ := time.Parse("Mon Jan 02 15:04:05 -0700 2006", s)
	return t
}

func timelineFeatures() map[string]interface{} {
	return map[string]interface{}{
		"rweb_lists_timeline_redesign_enabled":                                    true,
		"responsive_web_graphql_exclude_directive_enabled":                        true,
		"verified_phone_label_enabled":                                            false,
		"creator_subscriptions_tweet_preview_api_enabled":                         true,
		"responsive_web_graphql_timeline_navigation_enabled":                      true,
		"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
		"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
		"tweetypie_unmention_optimization_enabled":                                true,
		"responsive_web_edit_tweet_api_enabled":                                   true,
		"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
		"view_counts_everywhere_api_enabled":                                      true,
		"longform_notetweets_consumption_enabled":                                 true,
		"responsive_web_twitter_article_tweet_consumption_enabled":                true,
		"tweet_awards_web_tipping_enabled":                                        false,
		"freedom_of_speech_not_reach_fetch_enabled":                               true,
		"standardized_nudges_misinfo":                                             true,
		"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
		"rweb_video_timestamps_enabled":                                           true,
		"longform_notetweets_rich_text_read_enabled":                              true,
		"longform_notetweets_inline_media_enabled":                                true,
		"responsive_web_media_download_video_enabled":                             false,
		"responsive_web_enhance_cards_enabled":                                    false,
	}
}
