package review

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Deps are the Service's collaborators.
type Deps struct {
	Store    Store
	Settings Settings
	Admins   Admins
	Actions  Actions
	Learner  Learner
	Standing Standing    // trust restore for the "dismiss" timeout; optional
	Texts    Localizer   // defaults to Texts
	Clock    clock.Clock // defaults to clock.Real
	Rand     io.Reader   // source for short codes; defaults to crypto/rand
	// Protected reports whether a chat is a protected group. When set, the service never posts
	// there, even if the settings point a review destination at one (§9.7). Optional.
	Protected func(ctx context.Context, chat wa.JID) (bool, error)
}

// Service runs reviews: opening and posting items, votes, first-wins decisions, outcomes and
// timeouts (DESIGN.md §9.5).
type Service struct {
	d Deps
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	return newService(d)
}

// Ballot is one vote on an item, identified by ID or short code.
type Ballot struct {
	ReviewID  int64
	Code      string
	Voter     wa.JID
	VoterName string
	Decision  Decision
	Method    Method
	Raw       string         // the emoji or text, stored with the vote
	Chat      wa.JID         // where the vote was cast (review group or DM)
	Message   *wa.MessageRef // the voter's own message, for the 👌 on late votes
}

// Outcome is what happened to a vote.
type Outcome int

// Vote outcomes.
const (
	OutcomeDecided        Outcome = iota + 1 // this vote decided the item
	OutcomeAlreadyHandled                    // someone decided first; the vote is stored as late
	OutcomeNotAdmin                          // the voter isn't a current admin of the protected group
	OutcomeWithdrawn                         // a reaction was removed
	OutcomeUnknown                           // not a decision keyword
	OutcomeInvalid                           // a decision not allowed for this item
)

// VoteResult reports a vote's outcome and the item as it now stands.
type VoteResult struct {
	Outcome Outcome
	Item    Item
}

// Open creates a review item with a unique short code, its deadline and its timeout fallback,
// and posts it to the group's review destination.
func (s *Service) Open(ctx context.Context, n NewItem) (Item, error) {
	return s.open(ctx, n)
}

// Vote applies a ballot. Only a current admin of the item's group can decide, and the first
// valid vote wins atomically; every vote is stored.
func (s *Service) Vote(ctx context.Context, b Ballot) (VoteResult, error) {
	return s.vote(ctx, b)
}

// HandleReaction treats a reaction on one of the bot's review messages as a vote. ok is false
// when target isn't a review message. An empty emoji withdraws the voter's vote.
func (s *Service) HandleReaction(ctx context.Context, chat, voter wa.JID, target wa.MessageID, emoji string) (VoteResult, bool, error) {
	return s.handleReaction(ctx, chat, voter, target, emoji)
}

// HandleReply treats a reply quoting one of the bot's review messages as a vote. ok is false
// when quoted isn't a review message.
func (s *Service) HandleReply(ctx context.Context, chat, voter wa.JID, reply wa.MessageRef, quoted wa.MessageID, text string) (VoteResult, bool, error) {
	return s.handleReply(ctx, chat, voter, reply, quoted, text)
}

// RecordPosted maps a sent review message to its item; the action queue calls it after sending.
func (s *Service) RecordPosted(ctx context.Context, p Posted) error {
	if p.SentAt.IsZero() {
		p.SentAt = s.now()
	}
	if err := s.d.Store.AddPosted(ctx, p); err != nil {
		return fmt.Errorf("record posted review message %s: %w", p.ID, err)
	}
	return nil
}

// Expire applies the timeout fallback to every pending item past its deadline and returns how
// many it handled.
func (s *Service) Expire(ctx context.Context) (int, error) {
	return s.expire(ctx)
}

// Pending lists a group's open items, oldest first.
func (s *Service) Pending(ctx context.Context, group wa.JID) ([]Item, error) {
	return s.d.Store.Pending(ctx, group)
}

// ByCode returns the item with a short code.
func (s *Service) ByCode(ctx context.Context, code string) (Item, error) {
	return s.d.Store.ByCode(ctx, normalizeCode(code))
}

// EffectiveTimeout returns the timeout and fallback an item gets: the configured values, the
// mode's defaults when unset, and never auto_ban in an ask-admins mode or for an established
// member, a possibly hacked account or an admin.
func EffectiveTimeout(cfg Config, s Subject) (time.Duration, OnTimeout) {
	return effectiveTimeout(cfg, s)
}

// CheckOnTimeout reports whether a fallback may be configured for a group in mode m: auto_ban
// is refused in the ask-admins modes. It returns ErrAutoBanInAskMode or ErrUnknownFallback.
func CheckOnTimeout(m EnforcementMode, f OnTimeout) error {
	return checkOnTimeout(m, f)
}

// ItemForMessage returns the item one of the bot's review messages belongs to, or ErrNotFound.
func (s *Service) ItemForMessage(ctx context.Context, chat wa.JID, id wa.MessageID) (Item, error) {
	return s.d.Store.ByMessage(ctx, chat, id)
}
