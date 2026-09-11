package download

import (
	"errors"
	"fmt"
	"net/http"
)

var ErrRateLimited = errors.New("rate limited")

type statusError struct {
	code int
}

func (e *statusError) Error() string {
	switch e.code {
	case http.StatusTooManyRequests:
		return fmt.Sprintf("%s: status %d", ErrRateLimited, e.code)
	default:
		return fmt.Sprintf("unexpected status %d", e.code)
	}
}

func (e *statusError) Unwrap() error {
	switch e.code {
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return nil
	}
}

func StatusError(code int) error {
	return &statusError{code: code}
}
