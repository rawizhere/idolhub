package download

import (
	"errors"
	"fmt"
	"net/http"
)

var ErrRateLimited = errors.New("rate limited")
var ErrNotFound = errors.New("not found")

func StatusError(code int) error {
	switch code {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: status %d", ErrRateLimited, code)
	case http.StatusNotFound:
		return fmt.Errorf("%w: status %d", ErrNotFound, code)
	default:
		return fmt.Errorf("unexpected status %d", code)
	}
}
