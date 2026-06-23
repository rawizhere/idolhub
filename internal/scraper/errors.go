package scraper

import "errors"

// ErrAuthExpired is returned when a platform cookie or token has expired
var ErrAuthExpired = errors.New("authentication token expired or invalid")

// IsAuthExpiredError reports whether err is (or wraps) ErrAuthExpired
func IsAuthExpiredError(err error) bool {
	return errors.Is(err, ErrAuthExpired)
}
