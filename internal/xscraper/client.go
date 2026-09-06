package xscraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// xTLSProfile pairs a tls fingerprint with a matching User-Agent.
type xTLSProfile struct {
	profile profiles.ClientProfile
	ua      string
}

var xTLSProfiles = []xTLSProfile{
	{profiles.Chrome_131, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	{profiles.Chrome_133, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"},
	{profiles.Chrome_146, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"},
	{profiles.Chrome_152, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"},
	{profiles.Firefox_135, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:135.0) Gecko/20100101 Firefox/135.0"},
	{profiles.Firefox_148, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0"},
}

// RateLimitError is returned on HTTP 429 with the server-provided delay.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited, retry after %s", e.RetryAfter)
	}
	return "rate limited"
}

// xClient issues requests with a rotated browser TLS fingerprint.
type xClient struct {
	http      tls_client.HttpClient
	ua        string
	authToken string
	csrfToken string
}

func newXClient(authToken, csrfToken string) (*xClient, error) {
	tp := xTLSProfiles[rand.Intn(len(xTLSProfiles))]
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tp.profile),
	}...)
	if err != nil {
		return nil, err
	}
	slog.Info("Twitter client using rotated TLS profile", "tls_fingerprint", tp.profile.GetClientHelloStr())
	return &xClient{http: client, ua: tp.ua, authToken: authToken, csrfToken: csrfToken}, nil
}

func (c *xClient) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = fhttp.Header{
		"user-agent":                []string{c.ua},
		"accept":                    []string{"*/*"},
		"authorization":             []string{"Bearer " + bearer},
		"cookie":                    []string{"auth_token=" + c.authToken + "; ct0=" + c.csrfToken},
		"x-csrf-token":              []string{c.csrfToken},
		"x-twitter-active-user":     []string{"yes"},
		"x-twitter-client-language": []string{"en"},
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == fhttp.StatusTooManyRequests {
		return nil, &RateLimitError{RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if resp.StatusCode != fhttp.StatusOK {
		return nil, fmt.Errorf("response status %s: %s", resp.Status, body)
	}
	return body, nil
}

func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}
