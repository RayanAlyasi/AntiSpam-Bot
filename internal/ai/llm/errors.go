package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// StatusError maps an HTTP status from a provider to one of the package's sentinel errors.
func StatusError(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrAuth
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return ErrTimeout
	case status == http.StatusTooManyRequests:
		return ErrRateLimited
	case status >= 400 && status < 500:
		return ErrInvalidRequest
	default: // 5xx, 529 (overloaded) and anything unexpected
		return ErrUnavailable
	}
}

// TransportError maps a failed HTTP round trip (no status received) to a sentinel error,
// keeping the cause in the chain. A deadline, from the context or the client timeout, is
// ErrTimeout; a cancelled context stays context.Canceled so shutdown is not reported as an outage.
func TransportError(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("%w: %w", ErrTimeout, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}
