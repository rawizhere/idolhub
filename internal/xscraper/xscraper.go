package xscraper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

const (
	gqlURL    = "https://x.com/i/api/graphql"
	bearer    = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	opUserByScreenName = "Gb-d6r0vxPOADdG62OEBpQ/UserByScreenName"
	opUserTweets       = "eoJ5zbv51Z_KVl81v9PmLQ/UserTweets"
	opUserMedia        = "2tLOJWwGuCTytDrGBg8VwQ/UserMedia"
)

// Scraper fetches user timelines from the x.com frontend GraphQL API.
type Scraper struct {
	client     *xClient
	limiter    *rate.Limiter
	authToken  string
	delayEvery time.Duration
}

// New creates a scraper bound to an auth_token cookie.
func New(authToken string) (*Scraper, error) {
	csrf := make([]byte, 16)
	if _, err := rand.Read(csrf); err != nil {
		return nil, err
	}
	client, err := newXClient(authToken, hex.EncodeToString(csrf))
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:     client,
		limiter:    rate.NewLimiter(rate.Every(5*time.Second), 1),
		authToken:  authToken,
		delayEvery: 5 * time.Second,
	}, nil
}

// TweetResult is one item of a timeline channel.
type TweetResult struct {
	Tweet
	Error error
}

func (s *Scraper) timeline(ctx context.Context, op string, vars map[string]interface{}, max int) <-chan *TweetResult {
	ch := make(chan *TweetResult)
	go func() {
		defer close(ch)
		cursor := ""
		sent := 0
		for sent < max {
			select {
			case <-ctx.Done():
				ch <- &TweetResult{Error: ctx.Err()}
				return
			default:
			}
			if cursor != "" {
				vars["cursor"] = cursor
			}
			tweets, next, err := s.doTimelinePage(ctx, op, vars)
			if err != nil {
				ch <- &TweetResult{Error: err}
				return
			}
			if len(tweets) == 0 {
				return
			}
			for i := range tweets {
				if sent >= max {
					return
				}
				select {
				case ch <- &TweetResult{Tweet: *tweets[i]}:
					sent++
				case <-ctx.Done():
					ch <- &TweetResult{Error: ctx.Err()}
					return
				}
			}
			cursor = next
			if cursor == "" {
				return
			}
		}
	}()
	return ch
}

// GetTweets returns own tweets of a user, oldest pagination handled by cursor.
func (s *Scraper) GetTweets(ctx context.Context, screenName string, max int) <-chan *TweetResult {
	return s.userTimeline(ctx, opUserTweets, screenName, max)
}

// GetMediaTweets returns tweets with media attached.
func (s *Scraper) GetMediaTweets(ctx context.Context, screenName string, max int) <-chan *TweetResult {
	return s.userTimeline(ctx, opUserMedia, screenName, max)
}

func (s *Scraper) userTimeline(ctx context.Context, op, screenName string, max int) <-chan *TweetResult {
	ch := make(chan *TweetResult)
	go func() {
		defer close(ch)
		uid, err := s.userID(ctx, screenName)
		if err != nil {
			ch <- &TweetResult{Error: err}
			return
		}
		vars := map[string]interface{}{
			"userId":                 uid,
			"count":                  50,
			"includePromotedContent": false,
			"withClientEventToken":   false,
			"withBirdwatchNotes":     false,
			"withVoice":              true,
			"withV2Timeline":         true,
		}
		if op == opUserTweets {
			delete(vars, "withClientEventToken")
			delete(vars, "withBirdwatchNotes")
			vars["includePromotedContent"] = true
			vars["withCommunity"] = true
			vars["withQuickPromoteEligibilityTweetFields"] = false
		}
		for r := range s.timeline(ctx, op, vars, max) {
			ch <- r
			if r.Error != nil {
				return
			}
		}
	}()
	return ch
}

func (s *Scraper) userID(ctx context.Context, screenName string) (string, error) {
	vars := map[string]interface{}{"screen_name": screenName}
	body, err := s.doGet(ctx, gqlURL+"/"+opUserByScreenName, vars)
	if err != nil {
		return "", err
	}
	return parseUserID(body)
}

const maxRateLimitRetries = 5

func (s *Scraper) doGet(ctx context.Context, endpoint string, vars map[string]interface{}) ([]byte, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	varsJSON, _ := json.Marshal(vars)
	featsJSON, _ := json.Marshal(timelineFeatures())
	u := endpoint + "?variables=" + url.QueryEscape(string(varsJSON)) + "&features=" + url.QueryEscape(string(featsJSON))
	return s.client.get(ctx, u)
}

// doTimelinePage fetches one timeline page and retries while x.com reports a rate limit,
// either as HTTP 429 or as a graphql error payload with HTTP 200.
func (s *Scraper) doTimelinePage(ctx context.Context, op string, vars map[string]interface{}) ([]*Tweet, string, error) {
	backoff := 15 * time.Second
	for attempt := 0; ; attempt++ {
		body, err := s.doGet(ctx, gqlURL+"/"+op, vars)
		if err == nil {
			var tweets []*Tweet
			var next string
			tweets, next, err = parseTimeline(body)
			var prle *RateLimitError
			if !errors.As(err, &prle) {
				return tweets, next, err
			}
		}
		var rle *RateLimitError
		if !errors.As(err, &rle) || attempt >= maxRateLimitRetries {
			return nil, "", err
		}
		wait := rle.RetryAfter
		if wait <= 0 {
			wait = backoff
			backoff *= 2
		}
		slog.Warn("x.com rate limited, backing off", "attempt", attempt+1, "wait", wait)
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(wait):
		}
	}
}
