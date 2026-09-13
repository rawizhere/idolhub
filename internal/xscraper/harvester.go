package xscraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"

	"idolhub/internal/browser"
)

// Harvests queryId hashes from the x.com js bundle at runtime.

// bundleTTL bounds how often a runtime refresh is attempted after a 404.
const bundleTTL = 6 * time.Hour

const homeURL = "https://x.com/?lang=en"

var (
	// mainBundleRe picks the main chunk URL out of the x.com landing page HTML.
	mainBundleRe = regexp.MustCompile(`src="(https://abs\.twimg\.com/responsive-web/client-web/main\.[^"]+\.js)"`)

	// queryIDRe covers both field orders seen in the webpack bundle.
	queryIDRe = regexp.MustCompile(`queryId:"([A-Za-z0-9_-]+)",operationName:"(UserTweets|UserByScreenName|UserMedia)"|operationName:"(UserTweets|UserByScreenName|UserMedia)",queryId:"([A-Za-z0-9_-]+)"`)
)

// parseQueryIDs extracts operation -> queryId from a JS bundle.
func parseQueryIDs(bundle []byte) map[string]string {
	out := map[string]string{}
	for _, m := range queryIDRe.FindAllSubmatch(bundle, -1) {
		if len(m[2]) > 0 {
			out[string(m[2])] = string(m[1])
		} else {
			out[string(m[3])] = string(m[4])
		}
	}
	return out
}

// newAnonClient builds a cookie-less Firefox client for unauthenticated fetches.
func newAnonClient() (tls_client.HttpClient, string, error) {
	tp := browser.FirefoxProfile
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.Profile),
	}...)
	return client, tp.UA, err
}

func anonGet(ctx context.Context, client tls_client.HttpClient, ua, rawURL string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// refreshQueryIDs fetches the x.com bundle and updates the queryId table.
func (s *Scraper) refreshQueryIDs(ctx context.Context) (map[string]string, error) {
	client, ua, err := newAnonClient()
	if err != nil {
		return nil, err
	}
	home, err := anonGet(ctx, client, ua, homeURL)
	if err != nil {
		return nil, err
	}
	m := mainBundleRe.FindSubmatch(home)
	if m == nil {
		return nil, errNoBundle
	}
	bundle, err := anonGet(ctx, client, ua, string(m[1]))
	if err != nil {
		return nil, err
	}
	ops := parseQueryIDs(bundle)
	if len(ops) == 0 {
		return nil, errNoBundle
	}
	// Merge: keep the previous hash for ops the bundle does not mention.
	for op, qid := range ops {
		s.queryIDs[op] = qid
	}
	s.lastHarvest = time.Now()
	slog.Info("Refreshed Twitter queryIds from x.com bundle", "operations", len(ops))
	return ops, nil
}

// harvestDue reports whether a runtime refresh is worth trying now.
func (s *Scraper) harvestDue() bool {
	return s.lastHarvest.IsZero() || time.Since(s.lastHarvest) > bundleTTL
}

type statusError int

func (e statusError) Error() string { return http.StatusText(int(e)) }

var errNoBundle = fmt.Errorf("could not extract queryIds from the x.com main bundle")
