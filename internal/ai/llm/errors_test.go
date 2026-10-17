package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestStatusError(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{400, ErrInvalidRequest},
		{401, ErrAuth},
		{403, ErrAuth},
		{404, ErrInvalidRequest},
		{408, ErrTimeout},
		{413, ErrInvalidRequest},
		{422, ErrInvalidRequest},
		{429, ErrRateLimited},
		{500, ErrUnavailable},
		{502, ErrUnavailable},
		{503, ErrUnavailable},
		{504, ErrTimeout},
		{529, ErrUnavailable},
	}
	for _, tt := range tests {
		if got := StatusError(tt.status); !errors.Is(got, tt.want) {
			t.Errorf("StatusError(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"deadline", fmt.Errorf("post: %w", context.DeadlineExceeded), ErrTimeout},
		{"net timeout", timeoutErr{}, ErrTimeout},
		{"refused", errors.New("connection refused"), ErrUnavailable},
		{"cancelled", fmt.Errorf("post: %w", context.Canceled), context.Canceled},
	}
	for _, tt := range tests {
		got := TransportError(tt.err)
		if !errors.Is(got, tt.want) || !errors.Is(got, tt.err) {
			t.Errorf("%s: TransportError() = %v, want %v with the cause kept", tt.name, got, tt.want)
		}
	}
	if errors.Is(TransportError(context.Canceled), ErrUnavailable) {
		t.Error("a cancelled call must not count as a provider outage")
	}
}
