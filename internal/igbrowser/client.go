// Package igbrowser talks to the camoufox sidecar that runs authenticated instagram requests inside a real browser context.
package igbrowser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client calls the sidecar over plain HTTP on the docker network.
type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 90 * time.Second},
	}
}

// FetchResult is a page-context fetch response.
type FetchResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// Fetch runs a fetch() inside the instagram.com page and returns the raw body. navigate optionally loads a page first (lets the sidecar capture fresh request identity from instagram's own requests).
func (c *Client) Fetch(ctx context.Context, url, method, body, navigate string) (*FetchResult, error) {
	payload, err := json.Marshal(map[string]string{"url": url, "method": method, "body": body, "navigate": navigate})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/fetch", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	raw, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var res FetchResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ImportCookies loads a Netscape cookie export into the browser profile.
func (c *Client) ImportCookies(ctx context.Context, netscape string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/session/import", strings.NewReader(netscape))
	if err != nil {
		return err
	}
	_, err = c.do(req)
	return err
}

// Status reports the sidecar session health.
type Status struct {
	Ready            bool    `json:"ready"`
	Session          string  `json:"session"`
	SessionidExpires float64 `json:"sessionid_expires,omitempty"`
	Cookies          int     `json:"cookies,omitempty"`
	DsUserID         string  `json:"ds_user_id,omitempty"`
	DocID            string  `json:"doc_id,omitempty"`
	AsbdID           string  `json:"asbd_id,omitempty"`
	WwwClaim         string  `json:"www_claim,omitempty"`
	LastError        *string `json:"last_error"`
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/session/status", nil)
	if err != nil {
		return nil, err
	}
	raw, err := c.do(req)
	if err != nil {
		return nil, err
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("camoufox sidecar: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("camoufox sidecar returned %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	return buf.Bytes(), nil
}
