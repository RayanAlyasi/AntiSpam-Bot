package review

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// okHand is the cheap reaction late voters get (§9.5 "first-wins").
const okHand = "👌"

// codeAttempts is how many random codes Open tries before giving up; after half of them it
// switches to one character more, so a crowded code space can't stall review creation.
const codeAttempts = 8

func newService(d Deps) *Service {
	if d.Texts == nil {
		d.Texts = Texts
	}
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Rand == nil {
		d.Rand = rand.Reader
	}
	return &Service{d: d}
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// effectiveTimeout implements EffectiveTimeout.
func effectiveTimeout(cfg Config, subj Subject) (time.Duration, OnTimeout) {
	dur := cfg.Timeout
	if cfg.Mode == ModeAskBeforeAction && (dur <= 0 || dur == DefaultTimeout) {
		// The message is still visible in mode (b), so the short default wins unless the group
		// chose its own timeout.
		dur = DefaultAskBeforeTimeout
	}
	if dur <= 0 {
		dur = DefaultTimeout
	}
	def := TimeoutKeepDeletedNoBan
	if cfg.Mode == ModeAskBeforeAction {
		def = TimeoutDeleteNoBan
	}
	fb := cfg.OnTimeout
	switch fb {
	case TimeoutKeepDeletedNoBan, TimeoutDeleteNoBan, TimeoutDismiss:
	case TimeoutAutoBan:
		if !autoBanAllowed(cfg.Mode, subj) {
			fb = def
		}
	default:
		fb = def
	}
	return dur, fb
}

// autoBanAllowed reports whether a timeout may ban: never in ask modes, and never someone the
// bot isn't allowed to ban on its own (§9.5 Timeouts, §8.1, §8.6, §8.7).
func autoBanAllowed(m EnforcementMode, subj Subject) bool {
	return !m.AskMode() && !subj.Established && !subj.Hacked && !subj.Admin
}

// checkOnTimeout implements CheckOnTimeout.
func checkOnTimeout(m EnforcementMode, f OnTimeout) error {
	switch f {
	case TimeoutKeepDeletedNoBan, TimeoutDeleteNoBan, TimeoutDismiss:
		return nil
	case TimeoutAutoBan:
		if m.AskMode() {
			return ErrAutoBanInAskMode
		}
		return nil
	default:
		return fmt.Errorf("%q: %w", f, ErrUnknownFallback)
	}
}

// destination returns where an item is posted. v0.1 has review groups and the dashboard; the DM
// destination (v0.3) falls back to the dashboard. A protected group is never its own review
// group, whatever the settings say (§9.7).
func destination(cfg Config, group wa.JID) (wa.JID, bool) {
	if cfg.Destination == DestinationGroup && cfg.ReviewGroup != "" && cfg.ReviewGroup != group {
		return cfg.ReviewGroup, true
	}
	return "", false
}

// postable returns the review chat for a group, refusing a protected group as a destination.
func (s *Service) postable(ctx context.Context, cfg Config, group wa.JID) (wa.JID, bool, error) {
	chat, ok := destination(cfg, group)
	if !ok {
		return "", false, nil
	}
	if s.d.Protected != nil {
		prot, err := s.d.Protected(ctx, chat)
		if err != nil {
			return "", false, fmt.Errorf("review destination %s: %w", chat, err)
		}
		if prot {
			return "", false, nil
		}
	}
	return chat, true, nil
}

// stillNotAdmin re-checks, just before a ban, that the target isn't a group admin now (§8.6).
func (s *Service) stillNotAdmin(ctx context.Context, group, target wa.JID) (bool, error) {
	admin, err := s.d.Admins.IsAdmin(ctx, group, target)
	if err != nil {
		return false, fmt.Errorf("admin check before ban: %w", err)
	}
	return !admin, nil
}

func (s *Service) open(ctx context.Context, n NewItem) (Item, error) {
	cfg, err := s.d.Settings.ReviewConfig(ctx, n.Group)
	if err != nil {
		return Item{}, fmt.Errorf("review config %s: %w", n.Group, err)
	}
	if n.Proposed == "" && n.Kind != KindAlert {
		n.Proposed = DecisionBan
	}
	now := s.now()
	dur, fb := effectiveTimeout(cfg, n.Subject)
	it := Item{NewItem: n, Status: StatusPending, Mode: cfg.Mode, Deadline: now.Add(dur), OnTimeout: fb, CreatedAt: now}
	if n.Kind == KindAlert {
		// Alerts ask for no decision: they close quietly at the next sweep.
		it.Deadline, it.OnTimeout = now, TimeoutKeepDeletedNoBan
	}
	var stored Item
	for attempt := range codeAttempts {
		code, err := newCode(s.d.Rand, 4+attempt/(codeAttempts/2))
		if err != nil {
			return Item{}, err
		}
		it.Code = code
		stored, err = s.d.Store.Create(ctx, it)
		if errors.Is(err, ErrCodeTaken) {
			continue
		}
		if err != nil {
			return Item{}, fmt.Errorf("create review: %w", err)
		}
		break
	}
	if stored.ID == 0 {
		return Item{}, fmt.Errorf("create review: %w after %d attempts", ErrCodeTaken, codeAttempts)
	}
	chat, ok, err := s.postable(ctx, cfg, stored.Group)
	if err != nil {
		return stored, err
	}
	if ok {
		msg := render(stored, cfg, s.d.Texts)
		if err := s.d.Actions.Enqueue(ctx, Send{Chat: chat, Message: msg, ReviewID: stored.ID, Purpose: "review"}); err != nil {
			return stored, fmt.Errorf("post review %s: %w", stored.Code, err)
		}
	}
	return stored, nil
}

func (s *Service) find(ctx context.Context, b Ballot) (Item, error) {
	if b.ReviewID != 0 {
		return s.d.Store.Get(ctx, b.ReviewID)
	}
	return s.d.Store.ByCode(ctx, normalizeCode(b.Code))
}

// allowed reports whether d is a decision this item accepts.
func allowed(it Item, d Decision, cfg Config) bool {
	switch d {
	case DecisionBan, DecisionNotSpam, DecisionDeleteOnly:
		return it.Kind != KindAlert
	case DecisionClear:
		return it.Kind == KindHacked || it.Subject.Hacked
	case DecisionMute:
		return cfg.Ladder && it.Kind != KindAlert
	default:
		return false
	}
}

func (s *Service) vote(ctx context.Context, b Ballot) (VoteResult, error) {
	it, err := s.find(ctx, b)
	if err != nil {
		return VoteResult{}, err
	}
	now := s.now()
	rec := VoteRecord{ReviewID: it.ID, Voter: b.Voter, Method: b.Method, Raw: b.Raw, Decision: b.Decision, At: now}
	if rec.Raw == "" {
		rec.Raw = string(b.Decision)
	}
	cfg, err := s.d.Settings.ReviewConfig(ctx, it.Group)
	if err != nil {
		return VoteResult{}, fmt.Errorf("review config %s: %w", it.Group, err)
	}
	if b.Decision == "" {
		return s.reject(ctx, rec, InvalidUnknownKeyword, OutcomeUnknown, it)
	}
	admin, err := s.d.Admins.IsAdmin(ctx, it.Group, b.Voter)
	if err != nil {
		// Without a fresh admin answer nobody may decide: refusing is the safe side.
		return VoteResult{}, fmt.Errorf("admin check %s: %w", it.Group, err)
	}
	if !admin {
		return s.reject(ctx, rec, InvalidNotAdmin, OutcomeNotAdmin, it)
	}
	if !allowed(it, b.Decision, cfg) {
		return s.reject(ctx, rec, InvalidUnknownKeyword, OutcomeInvalid, it)
	}
	if it.Status != StatusPending {
		return s.late(ctx, rec, it, b)
	}
	cur, won, err := s.d.Store.Decide(ctx, it.ID, Decided{
		Status: StatusDecided, Decision: b.Decision, By: b.Voter, ByName: b.VoterName, Method: b.Method, At: now,
	})
	if err != nil {
		return VoteResult{}, fmt.Errorf("decide review %s: %w", it.Code, err)
	}
	if !won {
		return s.late(ctx, rec, cur, b)
	}
	rec.Valid = true
	// The decision is made: carry it out even if storing the vote record fails.
	var voteErr error
	if err := s.d.Store.AddVote(ctx, rec); err != nil {
		voteErr = fmt.Errorf("store vote on %s: %w", cur.Code, err)
	}
	err = errors.Join(voteErr, s.execute(ctx, cur), s.showOutcome(ctx, cur, cfg))
	return VoteResult{Outcome: OutcomeDecided, Item: cur}, err
}

func (s *Service) reject(ctx context.Context, rec VoteRecord, reason string, o Outcome, it Item) (VoteResult, error) {
	rec.InvalidReason = reason
	if err := s.d.Store.AddVote(ctx, rec); err != nil {
		return VoteResult{}, fmt.Errorf("store vote on %s: %w", it.Code, err)
	}
	return VoteResult{Outcome: o, Item: it}, nil
}

// late stores a vote that lost the race and acknowledges it at most once per voter: a 👌 on
// the voter's message and an "already handled by" note where they voted.
func (s *Service) late(ctx context.Context, rec VoteRecord, it Item, b Ballot) (VoteResult, error) {
	prior, err := s.d.Store.Votes(ctx, it.ID)
	if err != nil {
		return VoteResult{}, fmt.Errorf("votes on %s: %w", it.Code, err)
	}
	res, err := s.reject(ctx, rec, InvalidLate, OutcomeAlreadyHandled, it)
	if err != nil {
		return res, err
	}
	for _, v := range prior {
		if v.Voter == b.Voter && v.InvalidReason == InvalidLate {
			return res, nil
		}
	}
	if b.Chat == "" || b.Chat == it.Group {
		return res, nil
	}
	if s.d.Protected != nil {
		if prot, err := s.d.Protected(ctx, b.Chat); err != nil || prot {
			return res, err
		}
	}
	var errs []error
	if b.Message != nil {
		errs = append(errs, s.d.Actions.Enqueue(ctx, React{Chat: b.Chat, Target: *b.Message, Emoji: okHand}))
	}
	cfg, _ := s.d.Settings.ReviewConfig(ctx, it.Group)
	text := s.d.Texts.T(cfg.Language, "review.already_handled", map[string]string{"name": DeciderName(it, s.d.Texts, cfg.Language), "code": it.Code})
	errs = append(errs, s.d.Actions.Enqueue(ctx, Send{Chat: b.Chat, Message: wa.OutgoingMessage{Text: text, ReplyTo: b.Message}, Purpose: "reply"}))
	return res, errors.Join(errs...)
}

// DeciderName is how a decision's author is shown: "~Sara", or a neutral word when the bot has
// no name for them (a LID is never shown, and it isn't a phone number to mask).
func DeciderName(it Item, texts Localizer, lang string) string {
	switch {
	case it.DecidedByName != "":
		return "~" + redact(it.DecidedByName)
	case it.Method == MethodTimeout:
		return texts.T(lang, "review.by_timeout", nil)
	case it.DecidedBy.Server() == wa.ServerUser:
		return maskNumber("+" + it.DecidedBy.User())
	default:
		return texts.T(lang, "review.an_admin", nil)
	}
}

// target is the person a decision acts on.
func target(it Item) wa.JID {
	if it.Subject.Person != "" {
		return it.Subject.Person
	}
	if it.Message != nil {
		return it.Message.Sender
	}
	return ""
}

// visible reports whether the reviewed message is still in the group.
func visible(it Item) bool { return it.Message != nil && !it.Evidence.MessageDeleted }

// execute carries out a decision through the normal action path (§9.5): deletions and bans go
// to the paced queue and confirmations or overturns to learning. The bot never acts on an admin.
func (s *Service) execute(ctx context.Context, it Item) error {
	var errs []error
	enqueue := func(a Action) { errs = append(errs, s.d.Actions.Enqueue(ctx, a)) }
	switch it.Decision {
	case DecisionBan:
		if visible(it) {
			enqueue(Delete{Group: it.Group, Message: *it.Message, Priority: P1, Reason: "review"})
		}
		if t := target(it); t != "" && !it.Subject.Admin {
			if ok, err := s.stillNotAdmin(ctx, it.Group, t); err != nil || !ok {
				errs = append(errs, err)
			} else {
				enqueue(Ban{Group: it.Group, Target: t, Reason: ReasonSpam, Actor: it.DecidedBy, Via: "review", ReviewID: it.ID})
			}
		}
		if it.Message != nil && s.d.Learner != nil {
			errs = append(errs, s.d.Learner.ConfirmSpam(ctx, Confirmation{
				Group: it.Group, Message: *it.Message, Text: it.Evidence.Text, Thumbnail: it.Evidence.Thumbnail,
				Admin: it.DecidedBy, Source: "review", ReviewID: it.ID, At: it.DecidedAt,
			}))
		}
	case DecisionDeleteOnly:
		if visible(it) {
			enqueue(Delete{Group: it.Group, Message: *it.Message, Priority: P1, Reason: "review"})
		}
	case DecisionNotSpam:
		if it.Message != nil && s.d.Learner != nil {
			errs = append(errs, s.d.Learner.Overturn(ctx, Overturn{
				Group: it.Group, Message: *it.Message, Actor: it.DecidedBy, Method: it.Method,
			}))
		}
	case DecisionClear, DecisionMute, DecisionIgnore:
		// Closed without a ban; the optional ladder (mute) arrives with §9.3.
	}
	return errors.Join(errs...)
}

// showOutcome edits each posted review message while WhatsApp still allows it, and replies
// with a quoting follow-up after that (§9.5 "Showing the outcome").
func (s *Service) showOutcome(ctx context.Context, it Item, cfg Config) error {
	posted, err := s.d.Store.PostedFor(ctx, it.ID)
	if err != nil {
		return fmt.Errorf("posted messages of %s: %w", it.Code, err)
	}
	if len(posted) == 0 {
		return nil
	}
	line := handledLine(it, cfg, s.d.Texts)
	full := render(it, cfg, s.d.Texts).Text + "\n" + line
	now := s.now()
	var errs []error
	for _, p := range posted {
		if now.Before(p.EditableUntil()) {
			errs = append(errs, s.d.Actions.Enqueue(ctx, Edit{Chat: p.Chat, ID: p.ID, Text: full}))
			errs = append(errs, s.d.Store.MarkOutcomeEdited(ctx, p.Chat, p.ID))
			continue
		}
		errs = append(errs, s.d.Actions.Enqueue(ctx, Send{
			Chat: p.Chat, Message: wa.OutgoingMessage{Text: line, ReplyTo: &wa.MessageRef{ID: p.ID}}, ReviewID: 0, Purpose: "outcome",
		}))
	}
	return errors.Join(errs...)
}

// timeoutDecision is what a fallback records as the decision.
func timeoutDecision(f OnTimeout) Decision {
	switch f {
	case TimeoutAutoBan:
		return DecisionBan
	case TimeoutDismiss:
		return DecisionIgnore
	default:
		return DecisionDeleteOnly
	}
}

func (s *Service) expire(ctx context.Context) (int, error) {
	now := s.now()
	due, err := s.d.Store.Due(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("due reviews: %w", err)
	}
	n := 0
	var errs []error
	for _, it := range due {
		cfg, err := s.d.Settings.ReviewConfig(ctx, it.Group)
		if err != nil {
			errs = append(errs, fmt.Errorf("review config %s: %w", it.Group, err))
			continue
		}
		fb := it.OnTimeout
		if fb == TimeoutAutoBan && (!autoBanAllowed(it.Mode, it.Subject) || !autoBanAllowed(cfg.Mode, it.Subject)) {
			fb = TimeoutKeepDeletedNoBan
			if it.Mode == ModeAskBeforeAction || cfg.Mode == ModeAskBeforeAction {
				fb = TimeoutDeleteNoBan
			}
		}
		cur, won, err := s.d.Store.Decide(ctx, it.ID, Decided{
			Status: StatusTimedOut, Decision: timeoutDecision(fb), Method: MethodTimeout, At: now,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("time out %s: %w", it.Code, err))
			continue
		}
		if !won {
			continue
		}
		n++
		if cur.Kind == KindAlert {
			continue
		}
		cur.OnTimeout = fb
		errs = append(errs, s.applyTimeout(ctx, cur, cfg))
	}
	return n, errors.Join(errs...)
}

func (s *Service) applyTimeout(ctx context.Context, it Item, cfg Config) error {
	var errs []error
	enqueue := func(a Action) { errs = append(errs, s.d.Actions.Enqueue(ctx, a)) }
	switch it.OnTimeout {
	case TimeoutDeleteNoBan:
		if visible(it) {
			enqueue(Delete{Group: it.Group, Message: *it.Message, Priority: P1, Reason: "timeout"})
		}
	case TimeoutAutoBan:
		if visible(it) {
			enqueue(Delete{Group: it.Group, Message: *it.Message, Priority: P1, Reason: "timeout"})
		}
		if t := target(it); t != "" {
			if ok, err := s.stillNotAdmin(ctx, it.Group, t); err != nil || !ok {
				errs = append(errs, err)
			} else {
				enqueue(Ban{Group: it.Group, Target: t, Reason: ReasonSpam, Via: "timeout", ReviewID: it.ID})
			}
		}
	case TimeoutDismiss:
		errs = append(errs, s.restoreTrust(ctx, it))
	case TimeoutKeepDeletedNoBan:
		// The message stays deleted: no strike, no ban.
	}
	if chat, ok, err := s.postable(ctx, cfg, it.Group); err != nil {
		errs = append(errs, err)
	} else if ok {
		note := s.d.Texts.T(cfg.Language, "review.timeout_note", map[string]string{
			"code":     it.Code,
			"timeout":  humanDuration(it.Deadline.Sub(it.CreatedAt)),
			"fallback": s.d.Texts.T(cfg.Language, "review.fallback."+string(it.OnTimeout), nil),
		})
		msg := wa.OutgoingMessage{Text: note}
		if posted, err := s.d.Store.PostedFor(ctx, it.ID); err == nil && len(posted) > 0 {
			msg.ReplyTo = &wa.MessageRef{ID: posted[0].ID}
		}
		enqueue(Send{Chat: chat, Message: msg, Purpose: "outcome"})
	}
	return errors.Join(errs...)
}

func (s *Service) handleReaction(ctx context.Context, chat, voter wa.JID, targetID wa.MessageID, emoji string) (VoteResult, bool, error) {
	it, err := s.d.Store.ByMessage(ctx, chat, targetID)
	if errors.Is(err, ErrNotFound) {
		return VoteResult{}, false, nil
	}
	if err != nil {
		return VoteResult{}, false, fmt.Errorf("review for message %s: %w", targetID, err)
	}
	if emoji == "" {
		res, err := s.reject(ctx, VoteRecord{
			ReviewID: it.ID, Voter: voter, Method: MethodReaction, At: s.now(),
		}, InvalidWithdrawn, OutcomeWithdrawn, it)
		return res, true, err
	}
	cfg, err := s.d.Settings.ReviewConfig(ctx, it.Group)
	if err != nil {
		return VoteResult{}, true, fmt.Errorf("review config %s: %w", it.Group, err)
	}
	d, _ := parseReaction(emoji, cfg)
	res, err := s.vote(ctx, Ballot{ReviewID: it.ID, Voter: voter, Decision: d, Method: MethodReaction, Raw: emoji, Chat: chat})
	return res, true, err
}

func (s *Service) handleReply(ctx context.Context, chat, voter wa.JID, reply wa.MessageRef, quoted wa.MessageID, text string) (VoteResult, bool, error) {
	it, err := s.d.Store.ByMessage(ctx, chat, quoted)
	if errors.Is(err, ErrNotFound) {
		return VoteResult{}, false, nil
	}
	if err != nil {
		return VoteResult{}, false, fmt.Errorf("review for message %s: %w", quoted, err)
	}
	cfg, err := s.d.Settings.ReviewConfig(ctx, it.Group)
	if err != nil {
		return VoteResult{}, true, fmt.Errorf("review config %s: %w", it.Group, err)
	}
	d, _ := parseReply(text, cfg.Ladder)
	res, err := s.vote(ctx, Ballot{
		ReviewID: it.ID, Voter: voter, Decision: d, Method: MethodReply, Raw: text, Chat: chat, Message: &reply,
	})
	return res, true, err
}

func (s *Service) restoreTrust(ctx context.Context, it Item) error {
	t := target(it)
	if s.d.Standing == nil || t == "" {
		return nil
	}
	if err := s.d.Standing.RestoreTrust(ctx, it.Group, t); err != nil {
		return fmt.Errorf("restore trust after %s: %w", it.Code, err)
	}
	return nil
}
