package adapter

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"go.mau.fi/whatsmeow"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// errorMap lists the whatsmeow errors that mean one of the wa error values. The executor tells
// transient, rate-limited and permanent failures apart by the wa value (DESIGN.md §10.5).
var errorMap = []struct {
	from []error
	to   error
}{
	{[]error{whatsmeow.ErrIQRateOverLimit}, wa.ErrRateLimited},
	{[]error{
		// A disconnect is transient like a timeout: the executor backs off and retries.
		context.DeadlineExceeded, whatsmeow.ErrIQTimedOut, whatsmeow.ErrMessageTimedOut,
		whatsmeow.ErrIQDisconnected, whatsmeow.ErrNotConnected,
	}, wa.ErrTimeout},
	{[]error{
		whatsmeow.ErrIQForbidden, whatsmeow.ErrIQNotAuthorized, whatsmeow.ErrNoPermissionInGroup,
		whatsmeow.ErrGroupInviteLinkUnauthorized,
		// The bot can't act in a group it isn't in.
		whatsmeow.ErrNotInGroup,
	}, wa.ErrNotAdmin},
	{[]error{
		whatsmeow.ErrIQNotFound, whatsmeow.ErrIQGone, whatsmeow.ErrGroupNotFound,
		whatsmeow.ErrInviteLinkInvalid, whatsmeow.ErrInviteLinkRevoked,
		whatsmeow.ErrProfilePictureNotSet, whatsmeow.ErrProfilePictureUnauthorized,
	}, wa.ErrNotFound},
}

// serverErrorCode reads the code from whatsmeow's "server returned error <code>" send errors.
var serverErrorCode = regexp.MustCompile(`server returned error (\d+)`)

// mapError wraps err with op and, when it's a case the executor handles, the matching wa error
// value. The whatsmeow error stays in the chain for logs.
func mapError(op string, err error) error {
	if err == nil {
		return nil
	}
	if target := classify(err); target != nil {
		return fmt.Errorf("%s: %w: %w", op, target, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// classify returns the wa error value err means, or nil.
func classify(err error) error {
	if errors.Is(err, wa.ErrRateLimited) || errors.Is(err, wa.ErrTemporaryBan) ||
		errors.Is(err, wa.ErrNotAdmin) || errors.Is(err, wa.ErrTimeout) || errors.Is(err, wa.ErrNotFound) {
		return nil // already a wa error
	}
	for _, m := range errorMap {
		for _, from := range m.from {
			if errors.Is(err, from) {
				return m.to
			}
		}
	}
	// Any other IQ error: go by its code alone (whatsmeow's errors.Is also compares the text).
	var iq *whatsmeow.IQError
	if errors.As(err, &iq) {
		return codeError(iq.Code)
	}
	if errors.Is(err, whatsmeow.ErrServerReturnedError) {
		if sm := serverErrorCode.FindStringSubmatch(err.Error()); sm != nil {
			code, _ := strconv.Atoi(sm[1])
			return codeError(code)
		}
	}
	return nil
}

// codeError maps a WhatsApp status code (a send error or a per-participant result) to a wa
// error value, or nil when none fits.
func codeError(code int) error {
	switch code {
	case 429:
		return wa.ErrRateLimited
	case 401, 403:
		return wa.ErrNotAdmin
	case 404, 410:
		return wa.ErrNotFound
	case 408, 500, 503:
		return wa.ErrTimeout // transient: the executor backs off and retries (DESIGN.md §10.5)
	}
	return nil
}

// participantError is the wa error for one participant's result code (0 is success).
func participantError(code int) error {
	if code == 0 {
		return nil
	}
	if err := codeError(code); err != nil {
		return fmt.Errorf("participant error %d: %w", code, err)
	}
	return fmt.Errorf("participant error %d", code)
}
