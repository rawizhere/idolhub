package xscraper

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	mrand "math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	gqlURL = "https://x.com/i/api/graphql"
	bearer = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"

	opUserByScreenName = "UserByScreenName"
	opUserTweets       = "UserTweets"
	opUserMedia        = "UserMedia"
)

// defaultQueryIDs are the built-in queryId fallbacks.
var defaultQueryIDs = map[string]string{
	opUserByScreenName: "Gb-d6r0vxPOADdG62OEBpQ",
	opUserTweets:       "eoJ5zbv51Z_KVl81v9PmLQ",
	opUserMedia:        "2tLOJWwGuCTytDrGBg8VwQ",
}

// Session carries X credentials and metadata; zero values use built-ins.
type Session struct {
	AuthToken string
	CSRFToken string            // ct0; generated when empty
	Cookies   string            // full Netscape cookie export from the logged-in browser; preferred
	QueryIDs  map[string]string // operation name -> queryId
	Features  map[string]any    // graphql features dictionary
}

// Scraper fetches user timelines from the x.com frontend GraphQL API.
type Scraper struct {
	client      *xClient
	limiter     *rate.Limiter
	queryIDs    map[string]string
	features    map[string]any
	lastHarvest time.Time
}

// xSharedMu guards the process-level twitter scraper. Like the instagram
// client, it is reused across targets and sync windows so the account keeps
// one stable client identity; it is rebuilt only when credentials change.
var (
	xSharedMu  sync.Mutex
	xShared    *Scraper
	xSharedKey string
)

// New creates a scraper bound to an X session. Repeated calls with the same
// credentials return the shared instance.
func New(sess Session) (*Scraper, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(sess.AuthToken+"\x00"+sess.Cookies+"\x00"+sess.CSRFToken)))
	xSharedMu.Lock()
	defer xSharedMu.Unlock()
	if xShared != nil && xSharedKey == key {
		return xShared, nil
	}
	s, err := newScraper(sess)
	if err != nil {
		return nil, err
	}
	xShared, xSharedKey = s, key
	return s, nil
}

func newScraper(sess Session) (*Scraper, error) {
	if sess.AuthToken == "" && sess.Cookies == "" {
		return nil, errors.New("twitter auth token is empty")
	}
	csrf := sess.CSRFToken
	if csrf == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		csrf = hex.EncodeToString(b)
	}
	queryIDs := sess.QueryIDs
	if queryIDs == nil {
		queryIDs = defaultQueryIDs
	}
	features := sess.Features
	if features == nil {
		features = timelineFeatures()
	}
	client, err := newXClient(sess.AuthToken, csrf, sess.Cookies)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		client:   client,
		limiter:  rate.NewLimiter(rate.Every(5*time.Second), 1),
		queryIDs: queryIDs,
		features: features,
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
	body, err := s.doGet(ctx, opUserByScreenName, vars)
	if err != nil {
		return "", err
	}
	return parseUserID(body)
}

const maxRateLimitRetries = 5

func (s *Scraper) doGet(ctx context.Context, op string, vars map[string]interface{}) ([]byte, error) {
	body, err := s.doGetOnce(ctx, op, vars)
	// Deploy rotated the hashes: re-harvest once and retry.
	if err != nil && isUnknownQueryErr(err) && s.harvestDue() {
		if _, herr := s.refreshQueryIDs(ctx); herr == nil {
			body, err = s.doGetOnce(ctx, op, vars)
		} else {
			slog.Warn("Twitter queryId harvest failed", "operation", op, "error", herr)
		}
	}
	return body, err
}

func (s *Scraper) doGetOnce(ctx context.Context, op string, vars map[string]interface{}) ([]byte, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return nil, err
	}
	// Random extra pause: fixed intervals are a clean automation signal.
	time.Sleep(time.Duration(mrand.Int63n(2500)) * time.Millisecond)
	qid := s.queryIDs[op]
	if qid == "" {
		return nil, fmt.Errorf("no queryId known for operation %s", op)
	}
	varsJSON, _ := json.Marshal(vars)
	featsJSON, _ := json.Marshal(s.features)
	u := gqlURL + "/" + qid + "/" + op + "?variables=" + url.QueryEscape(string(varsJSON)) + "&features=" + url.QueryEscape(string(featsJSON))
	return s.client.get(ctx, u)
}

// isUnknownQueryErr reports whether X rejected the queryId hash.
func isUnknownQueryErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Cannot find query with id")
}

// doTimelinePage fetches one timeline page, retrying on graphql rate limits.
func (s *Scraper) doTimelinePage(ctx context.Context, op string, vars map[string]interface{}) ([]*Tweet, string, error) {
	backoff := 15 * time.Second
	for attempt := 0; ; attempt++ {
		body, err := s.doGet(ctx, op, vars)
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
