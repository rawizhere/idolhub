package download

import (
	"errors"
	"fmt"
	"net/http"
)

var ErrRateLimited = errors.New("rate limited")
var ErrNotFound = errors.New("not found")

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	switch e.code {
	case http.StatusTooManyRequests:
		return fmt.Sprintf("%s: status %d", ErrRateLimited, e.code)
	case http.StatusNotFound:
		return fmt.Sprintf("%s: status %d", ErrNotFound, e.code)
	default:
		return fmt.Sprintf("unexpected status %d", e.code)
	}
}

func (e *statusError) Unwrap() error {
	switch e.code {
	case http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return nil
	}
}

func StatusError(code int) error {
	return &statusError{code: code}
}
