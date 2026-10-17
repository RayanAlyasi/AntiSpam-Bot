package wa

import (
	"errors"
	"fmt"
	"time"
)

// Errors a WAClient returns, wrapped or as is. Callers match them with errors.Is; the action
// executor uses them to tell transient, rate-limited and permanent failures apart (DESIGN.md §10.5).
var (
	// ErrRateLimited is the server's 429 rate-overlimit answer. The executor pauses and slows down.
	ErrRateLimited = errors.New("wa: rate limited (429)")
	// ErrTemporaryBan means the account is temporarily banned. The executor opens the breaker.
	ErrTemporaryBan = errors.New("wa: account temporarily banned")
	// ErrNotAdmin means the bot isn't an admin of the group, so it can't act there.
	ErrNotAdmin = errors.New("wa: bot is not a group admin")
	// ErrTimeout means the request got no answer in time. It's transient.
	ErrTimeout = errors.New("wa: request timed out")
	// ErrNotFound means the target doesn't exist: a message already gone, a participant not
	// in the group, an unknown group, channel or invite code.
	ErrNotFound = errors.New("wa: not found")
)

// TemporaryBanError carries the details of a temporary ban (DESIGN.md §2.4). It matches
// ErrTemporaryBan with errors.Is.
type TemporaryBanError struct {
	Code   int           // e.g. 101: sent to too many strangers
	Expire time.Duration // how long until the ban lifts
}

func (e *TemporaryBanError) Error() string {
	return fmt.Sprintf("wa: account temporarily banned (code %d, expires in %s)", e.Code, e.Expire)
}

// Is makes errors.Is(err, ErrTemporaryBan) true for a *TemporaryBanError.
func (e *TemporaryBanError) Is(target error) bool { return target == ErrTemporaryBan }
