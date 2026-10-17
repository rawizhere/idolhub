package scraper

import "errors"

var ErrAuthExpired = errors.New("authentication token expired or invalid")

// ErrRateLimited: instagram answered 429 — the session is valid, requests are throttled; retry later, do not rotate cookies.
var ErrRateLimited = errors.New("instagram rate limit reached (429)")
