package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// errorClass is how the executor treats a WhatsApp error (DESIGN.md §10.5).
type errorClass int

const (
	classOK          errorClass = iota
	classTransient              // timeout, disconnected: retry with backoff
	classUnusual                // unexpected: retry with backoff, and a warning sign
	classRateLimited            // 429: pause, then slow down
	classTempBan                // temporary ban: open the breaker, slow down, retry with backoff
	classNotAdmin               // permanent: the bot isn't an admin of the group
	classNotFound               // permanent: the message or participant is gone
	classShutdown               // the executor is stopping, or the call was held back: run again later
	classExpired                // the deadline passed while the call waited: drop it
)

// errInterrupted means a call was held back at the last moment (kill switch, a pause or the
// breaker), so WhatsApp was never called.
var errInterrupted = errors.New("actions: held back before the call")

// errExpired means the action's deadline passed while its call waited for the call lock.
var errExpired = errors.New("actions: deadline passed before the call")

// batchChunks caps how many calls one coalesced batch makes in a row, so a big wave of kicks
// in one group can't hold the executor while other groups wait.
const batchChunks = 4

func classify(ctx context.Context, err error) errorClass {
	switch {
	case err == nil:
		return classOK
	// Signs from WhatsApp count even while shutting down.
	case errors.Is(err, wa.ErrTemporaryBan):
		return classTempBan
	case errors.Is(err, wa.ErrRateLimited):
		return classRateLimited
	case errors.Is(err, errExpired):
		return classExpired
	case errors.Is(err, errInterrupted), ctx.Err() != nil:
		return classShutdown
	case errors.Is(err, wa.ErrNotAdmin):
		return classNotAdmin
	case errors.Is(err, wa.ErrNotFound):
		return classNotFound
	case errors.Is(err, wa.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return classTransient
	default:
		return classUnusual
	}
}

// reachedWhatsApp reports whether a call with this outcome may have been delivered.
func (c errorClass) reachedWhatsApp() bool { return c != classShutdown && c != classExpired }

// execute runs one picked action, or the coalesced batch it starts.
func (e *Executor) execute(ctx context.Context, picked Action) error {
	if picked.Kind.Coalesced() {
		return e.executeBatch(ctx, picked)
	}
	a, ok, err := e.d.Store.Claim(ctx, picked.ID)
	if err != nil || !ok {
		return err
	}
	// After the claim, store writes outlive a shutdown so nothing is left running.
	sctx := context.WithoutCancel(ctx)
	e.rr[a.Priority] = a.Chat
	now := e.clk.Now()
	protectedPost := false
	if a.Kind.Posts() && a.Chat.IsGroup() {
		// Always ask afresh before posting in a group: silent mode is the one rule that must
		// never be missed (DESIGN.md §9.7).
		p, perr := e.d.Groups.Posting(ctx, a.Chat)
		e.postings[a.Chat] = postingEntry{p: p, err: perr, at: now}
		switch {
		case perr != nil:
			return e.refuse(sctx, a, fmt.Sprintf("group state unknown, post refused: %v", perr))
		case p.Protected && p.Silent:
			return e.refuse(sctx, a, "silent: the bot never posts in a silent protected group")
		case p.Protected:
			if d := e.postWindow(a.Chat).wait(now); d > 0 {
				return e.retry(sctx, a.ID, RetryInfo{NotBefore: now.Add(d)})
			}
			protectedPost = true
		}
	}
	var text string
	if a.Kind == KindSend {
		if text, ok = e.chooseText(a); !ok {
			return e.retry(sctx, a.ID, RetryInfo{NotBefore: now})
		}
	}
	if err := e.takeWrite(ctx, a.Chat, a, false); err != nil {
		return e.retry(sctx, a.ID, RetryInfo{NotBefore: now})
	}
	res, callErr := e.call(ctx, a, text)
	class := classify(ctx, callErr)
	if class.reachedWhatsApp() {
		e.wrote(ctx, a.Chat, protectedPost)
		if a.Kind == KindSend && class != classNotAdmin && class != classNotFound {
			// A send that timed out may still have been delivered: count its text as sent.
			e.lastText, e.lastChat, e.sentAny = text, a.Chat, true
		}
	}
	e.react(ctx, a.Chat, class, callErr) // a 429 or ban was already applied inside call
	err = e.dispose(sctx, a, class, callErr, res)
	if class.reachedWhatsApp() {
		e.space(ctx)
	}
	return err
}

// refuse finishes a post that the queue won't make, without calling WhatsApp.
func (e *Executor) refuse(ctx context.Context, a Action, reason string) error {
	e.log.Warn().Int64("action", a.ID).Str("kind", string(a.Kind)).Msg(reason)
	return e.finish(ctx, a.ID, StatusRefused, reason, Result{})
}

// member is one action of a coalesced batch.
type member struct {
	a       Action
	called  bool // some of its JIDs went out in this run
	settled bool // finished or retried
}

// executeBatch merges the pending kicks (or join-request rejections) of the picked action's
// group into calls of at most MaxKickBatch JIDs. One run makes at most batchChunks calls, and
// stops early rather than wait for the group's per-minute window, so a big wave can't hold the
// executor while other groups wait; what's left runs later, keeping the outcomes so far. Every
// JID was enqueued, and so judged, on its own; batching only changes how many calls the same
// decisions take (DESIGN.md §10.4).
func (e *Executor) executeBatch(ctx context.Context, picked Action) error {
	pending, err := e.d.Store.Pending(ctx, e.d.Bot, picked.Chat, picked.Kind)
	if err != nil {
		return fmt.Errorf("read pending %s actions: %w", picked.Kind, err)
	}
	sctx := context.WithoutCancel(ctx)
	now := e.clk.Now()
	e.mu.Lock()
	breaker := e.breakerOpen
	e.mu.Unlock()
	limit := e.cfg.MaxKickBatch * batchChunks
	chosen := []Action{picked}
	jids := unsent(nil, picked)
	for _, o := range pending {
		switch {
		case o.ID == picked.ID, !o.Deadline.After(now), o.NotBefore.After(now), breaker && !e.allowedWhenOpen(o):
			continue
		}
		if merged := unique(jids, unsent(nil, o)); len(merged) <= limit {
			chosen, jids = append(chosen, o), merged
		}
	}
	var members []*member
	for _, c := range chosen {
		a, ok, err := e.d.Store.Claim(ctx, c.ID)
		if err != nil {
			return errors.Join(err, e.settleRest(sctx, members, classShutdown, nil, nil, nil))
		}
		if ok {
			members = append(members, &member{a: a})
		}
	}
	if len(members) == 0 {
		return nil
	}
	e.rr[members[0].a.Priority] = picked.Chat

	// Outcomes from earlier runs carry over, so those JIDs aren't sent again.
	outcomes := map[wa.JID]error{}
	for _, m := range members {
		for _, o := range m.a.Result.Participants {
			outcomes[o.JID] = o.err()
		}
	}
	var errs []error
	for calls := 0; ; calls++ {
		if len(nextChunk(members, outcomes, e.cfg.MaxKickBatch)) == 0 {
			break
		}
		if calls == batchChunks || (calls > 0 && e.mustWait(picked.Chat)) {
			// Yield: the rest of the wave runs later, after other groups' work.
			errs = append(errs, e.settleRest(sctx, members, classShutdown, nil, outcomes, nil))
			return errors.Join(errs...)
		}
		if err := e.takeWrite(ctx, picked.Chat, picked, calls > 0); err != nil {
			errs = append(errs, e.settleRest(sctx, members, classShutdown, nil, outcomes, nil))
			return errors.Join(errs...)
		}
		if !e.lockCall(ctx, picked) {
			errs = append(errs, e.settleRest(sctx, members, classShutdown, nil, outcomes, nil))
			return errors.Join(errs...)
		}
		// Members whose deadline passed meanwhile (the call lock can wait behind a slow read)
		// don't get the rest of their JIDs sent.
		now := e.clk.Now()
		var expired []int64
		for _, m := range members {
			if !m.settled && !m.a.Deadline.After(now) && !resolved(m.a, outcomes) {
				m.settled = true
				errs = append(errs, e.finishQuiet(sctx, m.a.ID, StatusExpired, "deadline passed during a batch", outcomesOf(m.a, outcomes)))
				expired = append(expired, m.a.ID)
			}
		}
		reportExpired := func() {
			for _, id := range expired {
				e.report(sctx, id) // after the call lock is released
			}
		}
		chunk := nextChunk(members, outcomes, e.cfg.MaxKickBatch)
		if len(chunk) == 0 {
			e.callMu.Unlock()
			reportExpired()
			break
		}
		for _, m := range members {
			if !m.settled && slices.ContainsFunc(m.a.Payload.JIDs, func(j wa.JID) bool { return slices.Contains(chunk, j) }) {
				m.called = true
			}
		}
		results, callErr := e.doParticipants(ctx, picked, chunk)
		e.reactUrgent(ctx, classify(ctx, callErr), callErr)
		e.callMu.Unlock()
		reportExpired()
		class := classify(ctx, callErr)
		if class.reachedWhatsApp() {
			e.wrote(ctx, picked.Chat, false)
		}
		e.react(ctx, picked.Chat, class, callErr)
		if class != classOK {
			errs = append(errs, e.settleRest(sctx, members, class, callErr, outcomes, chunk))
			if class.reachedWhatsApp() {
				e.space(ctx)
			}
			return errors.Join(errs...)
		}
		for _, r := range results {
			if slices.Contains(chunk, r.JID) {
				outcomes[r.JID] = r.Err
			}
		}
		for _, j := range chunk {
			if _, ok := outcomes[j]; !ok {
				outcomes[j] = errors.New("no result from WhatsApp")
			}
		}
		errs = append(errs, e.finishResolved(sctx, members, outcomes))
		e.space(ctx)
	}
	errs = append(errs, e.finishResolved(sctx, members, outcomes))
	return errors.Join(errs...)
}

// mustWait reports whether another write to chat would have to wait for the bot's bucket or
// the chat's window: a batch then yields instead of holding the executor.
func (e *Executor) mustWait(chat wa.JID) bool {
	now := e.clk.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.recover(now)
	return e.writes.wait(now) > 0 || e.chatWindow(chat).wait(now) > 0
}

// err turns a stored outcome back into the error it recorded.
func (o ParticipantOutcome) err() error {
	switch {
	case o.Gone:
		return wa.ErrNotFound
	case o.Error != "":
		return errors.New(o.Error)
	default:
		return nil
	}
}

// unsent appends a's JIDs that have no outcome from an earlier run.
func unsent(base []wa.JID, a Action) []wa.JID {
	done := map[wa.JID]bool{}
	for _, o := range a.Result.Participants {
		done[o.JID] = true
	}
	var add []wa.JID
	for _, j := range a.Payload.JIDs {
		if !done[j] {
			add = append(add, j)
		}
	}
	return unique(base, add)
}

// nextChunk returns up to n JIDs of unsettled members that have no outcome yet, in order.
func nextChunk(members []*member, outcomes map[wa.JID]error, n int) []wa.JID {
	var out []wa.JID
	for _, m := range members {
		if m.settled {
			continue
		}
		for _, j := range m.a.Payload.JIDs {
			if _, done := outcomes[j]; !done && !slices.Contains(out, j) {
				if len(out) == n {
					return out
				}
				out = append(out, j)
			}
		}
	}
	return out
}

func resolved(a Action, outcomes map[wa.JID]error) bool {
	for _, j := range a.Payload.JIDs {
		if _, ok := outcomes[j]; !ok {
			return false
		}
	}
	return true
}

func outcomesOf(a Action, outcomes map[wa.JID]error) Result {
	var res Result
	for _, j := range unique(nil, a.Payload.JIDs) {
		if err, ok := outcomes[j]; ok {
			out := ParticipantOutcome{JID: j}
			if err != nil {
				out.Error = err.Error()
				out.Gone = errors.Is(err, wa.ErrNotFound)
			}
			res.Participants = append(res.Participants, out)
		}
	}
	return res
}

// finishResolved finishes the members whose every JID has an outcome. Someone who already left
// counts as removed; any other per-JID error fails the action.
func (e *Executor) finishResolved(ctx context.Context, members []*member, outcomes map[wa.JID]error) error {
	var errs []error
	for _, m := range members {
		if m.settled || !resolved(m.a, outcomes) {
			continue
		}
		m.settled = true
		var failed []string
		for _, j := range unique(nil, m.a.Payload.JIDs) {
			if err := outcomes[j]; err != nil && !errors.Is(err, wa.ErrNotFound) {
				failed = append(failed, fmt.Sprintf("%s: %v", j, err))
			}
		}
		status, errText := StatusDone, ""
		if len(failed) > 0 {
			status, errText = StatusFailed, strings.Join(failed, "; ")
		}
		errs = append(errs, e.finish(ctx, m.a.ID, status, errText, outcomesOf(m.a, outcomes)))
	}
	return errors.Join(errs...)
}

// settleRest disposes of the unsettled members after a failed call (chunk holds its JIDs), a
// held-back call or a yield, keeping their outcomes so far. A member none of whose JIDs were in
// the failed call just goes back to pending, without losing an attempt.
func (e *Executor) settleRest(ctx context.Context, members []*member, class errorClass, err error, outcomes map[wa.JID]error, chunk []wa.JID) error {
	var errs []error
	at := e.clk.Now()
	e.mu.Lock()
	if e.pausedUntil.After(at) {
		at = e.pausedUntil
	}
	e.mu.Unlock()
	for _, m := range members {
		if m.settled {
			continue
		}
		m.settled = true
		res := outcomesOf(m.a, outcomes)
		inCall := slices.ContainsFunc(m.a.Payload.JIDs, func(j wa.JID) bool { return slices.Contains(chunk, j) })
		if inCall {
			errs = append(errs, e.dispose(ctx, m.a, class, err, res))
			continue
		}
		errs = append(errs, e.retry(ctx, m.a.ID, RetryInfo{NotBefore: at, Called: m.called, Result: res}))
	}
	return errors.Join(errs...)
}

// unique appends the JIDs of add that aren't in base yet, keeping order.
func unique(base, add []wa.JID) []wa.JID {
	out := slices.Clone(base)
	for _, j := range add {
		if !slices.Contains(out, j) {
			out = append(out, j)
		}
	}
	return out
}

// stoppedFor reports whether a may not run now: the kill switch, a 429 pause, or the open
// breaker for a kind it doesn't allow. It must be called with e.mu held.
func (e *Executor) stoppedFor(a Action, now time.Time) bool {
	return e.killed || e.lockLost || now.Before(e.pausedUntil) || (e.breakerOpen && !e.allowedWhenOpen(a))
}

// takeWrite waits until the bot's write bucket and the chat's window allow one more write, and
// takes the token. It returns errInterrupted when the kill switch, a pause or the breaker stops
// a meanwhile. With refresh, it re-reads the control state first (between the calls of a batch,
// where no poll runs).
func (e *Executor) takeWrite(ctx context.Context, chat wa.JID, a Action, refresh bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if refresh {
			if err := e.refreshControl(ctx); err != nil {
				if ctx.Err() == nil {
					e.log.Error().Err(err).Msg("re-read control state in a batch")
				}
				return errInterrupted
			}
		}
		now := e.clk.Now()
		e.mu.Lock()
		e.recover(now)
		stopped := e.stoppedFor(a, now)
		d := max(e.writes.wait(now), e.chatWindow(chat).wait(now))
		if !stopped && d == 0 {
			e.writes.take(now)
		}
		e.mu.Unlock()
		switch {
		case stopped:
			return errInterrupted
		case d == 0:
			return nil
		}
		refresh = true // a wait can be long: look again before the call
		e.sleep(ctx, min(d, e.cfg.PollInterval))
	}
}

// lockCall takes the one-call-at-a-time lock and checks, under it, that a may still run. It
// re-reads the control state first: the caller may have waited behind a slow read while the
// operator paused everything. A read or write that got a 429 or a temporary ban applies the
// consequences before releasing the lock. On success the caller must unlock e.callMu.
func (e *Executor) lockCall(ctx context.Context, a Action) bool {
	e.callMu.Lock()
	if !e.checkLock(ctx) {
		e.callMu.Unlock()
		return false
	}
	if err := e.refreshControl(ctx); err != nil {
		if ctx.Err() == nil {
			e.log.Error().Err(err).Msg("re-read control state before a call")
		}
		e.callMu.Unlock()
		return false
	}
	e.mu.Lock()
	stopped := e.stoppedFor(a, e.clk.Now())
	e.mu.Unlock()
	if stopped {
		e.callMu.Unlock()
		return false
	}
	return true
}

// call performs one WhatsApp write for a single action. Calls are strictly serial.
func (e *Executor) call(ctx context.Context, a Action, text string) (Result, error) {
	if !e.lockCall(ctx, a) {
		return Result{}, errInterrupted
	}
	defer e.callMu.Unlock()
	if !a.Deadline.After(e.clk.Now()) {
		return Result{}, errExpired // it waited behind a slow read
	}
	res, err := e.doCall(ctx, a, text)
	e.reactUrgent(ctx, classify(ctx, err), err)
	return res, err
}

func (e *Executor) doCall(ctx context.Context, a Action, text string) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, e.cfg.CallTimeout)
	defer cancel()
	start := e.clk.Now()
	defer e.checkAck(ctx, start)
	p := a.Payload
	c := e.d.Client
	switch a.Kind {
	case KindRevoke:
		return Result{}, c.Revoke(cctx, a.Chat, p.Target)
	case KindSend:
		sent, err := c.Send(cctx, a.Chat, wa.OutgoingMessage{Text: text, ReplyTo: p.ReplyTo, Image: p.Image})
		return Result{MessageID: sent.ID, SentText: text}, err
	case KindEdit:
		return Result{}, c.Edit(cctx, a.Chat, p.Target.ID, p.Text)
	case KindReact:
		return Result{}, c.React(cctx, a.Chat, p.Target, p.Emoji)
	case KindAnnounce:
		return Result{}, c.SetAnnounce(cctx, a.Chat, p.On)
	case KindJoinApproval:
		return Result{}, c.SetJoinApproval(cctx, a.Chat, p.On)
	case KindResetInvite:
		link, err := c.ResetInviteLink(cctx, a.Chat)
		return Result{InviteLink: link}, err
	case KindKick, KindRejectJoin:
		return Result{}, fmt.Errorf("%s is coalesced, not called alone", a.Kind)
	default:
		return Result{}, fmt.Errorf("unknown action kind %q", a.Kind)
	}
}

// doParticipants performs one batched kick or join-request rejection for picked's group. The
// caller holds the call lock.
func (e *Executor) doParticipants(ctx context.Context, picked Action, jids []wa.JID) ([]wa.ParticipantResult, error) {
	cctx, cancel := context.WithTimeout(ctx, e.cfg.CallTimeout)
	defer cancel()
	start := e.clk.Now()
	defer e.checkAck(ctx, start)
	if picked.Kind == KindRejectJoin {
		return e.d.Client.UpdateJoinRequests(cctx, picked.Chat, jids, wa.JoinRequestReject)
	}
	return e.d.Client.UpdateParticipants(cctx, picked.Chat, jids, wa.ParticipantRemove)
}

// checkAck treats a call slower than Config.SlowAck as a warning sign.
func (e *Executor) checkAck(ctx context.Context, start time.Time) {
	if took := e.clk.Now().Sub(start); took > e.cfg.SlowAck && ctx.Err() == nil {
		e.log.Warn().Dur("took", took).Msg("slow WhatsApp ack")
		e.warn(ctx, WarnSlowAck)
	}
}

// wrote records a write that reached WhatsApp (its token was taken in takeWrite).
func (e *Executor) wrote(ctx context.Context, chat wa.JID, protectedPost bool) {
	now := e.clk.Now()
	e.mu.Lock()
	e.lastWrite = now
	e.mu.Unlock()
	e.chatWindow(chat).add(now)
	if protectedPost {
		e.postWindow(chat).add(now)
	}
	if day := utcDay(now); !day.Equal(e.day) {
		e.day, e.dayWrites, e.dayAlert = day, 0, false
	}
	e.dayWrites++
	if e.cfg.DailyAlert > 0 && e.dayWrites > e.cfg.DailyAlert && !e.dayAlert {
		e.dayAlert = true
		e.alert(ctx, AlertDailyWrites, fmt.Sprintf(
			"the bot passed %d WhatsApp writes today; check for a raid, a bad rule or a misconfigured group", e.cfg.DailyAlert))
	}
}

// space waits the pause after a write: Spacing plus a random 0–Jitter (DESIGN.md §10.3).
func (e *Executor) space(ctx context.Context) {
	e.mu.Lock()
	d := e.cfg.Spacing + time.Duration(e.rnd.Float64()*float64(e.cfg.Jitter))
	e.mu.Unlock()
	e.sleep(ctx, d)
}

// reactUrgent applies the consequences of a 429 or a temporary ban: the pause or the breaker.
// It runs while the call lock is still held, so no other call slips in first.
func (e *Executor) reactUrgent(ctx context.Context, class errorClass, err error) {
	switch class {
	case classRateLimited:
		e.rateLimited(ctx)
	case classTempBan:
		if berr := e.openBreaker(ctx, fmt.Sprintf("temporary ban: %v", err)); berr != nil {
			e.log.Error().Err(berr).Msg("persist breaker state")
		}
	case classOK, classTransient, classUnusual, classNotAdmin, classNotFound, classShutdown, classExpired:
	}
}

// react applies the other bot-wide consequences of a call's outcome once (slow-down, lost admin).
func (e *Executor) react(ctx context.Context, chat wa.JID, class errorClass, err error) {
	switch class {
	case classUnusual:
		e.log.Warn().Err(err).Msg("unusual WhatsApp error")
		e.warn(ctx, WarnUnusual)
	case classNotAdmin:
		sctx := context.WithoutCancel(ctx)
		if failed, ferr := e.d.Store.FailChat(sctx, e.d.Bot, chat, adminKinds, "bot is not an admin of the group"); ferr != nil {
			e.log.Error().Err(ferr).Msg("fail pending admin actions")
		} else if len(failed) > 0 {
			e.log.Warn().Int("actions", len(failed)).Msg("pending admin actions failed: bot is no longer admin")
			for _, a := range failed {
				e.finished(sctx, a)
			}
		}
		if nerr := e.d.Groups.NotAdmin(sctx, e.d.Bot, chat); nerr != nil {
			e.log.Error().Err(nerr).Msg("record lost admin")
		}
	case classOK, classTransient, classNotFound, classShutdown, classRateLimited, classTempBan, classExpired:
	}
}

// dispose moves one claimed action on after its call.
func (e *Executor) dispose(ctx context.Context, a Action, class errorClass, err error, res Result) error {
	now := e.clk.Now()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	called := class.reachedWhatsApp()
	switch class {
	case classOK:
		return e.finish(ctx, a.ID, StatusDone, "", res)
	case classNotAdmin, classNotFound:
		return e.finish(ctx, a.ID, StatusFailed, errText, res)
	case classRateLimited:
		e.mu.Lock()
		until := e.pausedUntil
		e.mu.Unlock()
		return e.retry(ctx, a.ID, RetryInfo{NotBefore: until, Error: errText, Called: called, Attempted: true, Result: res})
	case classShutdown:
		return e.retry(ctx, a.ID, RetryInfo{NotBefore: now, Error: errText, Result: res})
	case classExpired:
		e.log.Info().Int64("action", a.ID).Str("kind", string(a.Kind)).Msg("action expired while waiting for its call")
		return e.finish(ctx, a.ID, StatusExpired, "deadline passed", res)
	case classTransient, classUnusual, classTempBan:
		// A temporary ban retries like a transient error: an action the open breaker still
		// allows (a lockdown) must not hammer a banned account.
		if a.Attempts >= e.cfg.MaxAttempts {
			return e.finish(ctx, a.ID, StatusFailed, fmt.Sprintf("gave up after %d attempts: %s", a.Attempts, errText), res)
		}
		e.mu.Lock()
		d := backoff(a.Attempts, e.cfg.BackoffBase, e.cfg.BackoffMax, e.rnd.Float64)
		e.mu.Unlock()
		return e.retry(ctx, a.ID, RetryInfo{NotBefore: now.Add(d), Error: errText, Called: called, Attempted: true, Result: res})
	default:
		return fmt.Errorf("unhandled error class %d", class)
	}
}

// rateLimited pauses every call for RateLimitPause, persists the pause, and counts the 429 as
// a warning sign.
func (e *Executor) rateLimited(ctx context.Context) {
	now := e.clk.Now()
	e.mu.Lock()
	if until := now.Add(e.cfg.RateLimitPause); until.After(e.pausedUntil) {
		e.pausedUntil = until
	}
	until := e.pausedUntil
	e.mu.Unlock()
	e.log.Warn().Dur("pause", e.cfg.RateLimitPause).Msg("rate limited (429): pausing")
	if _, err := e.d.Store.UpdateControl(context.WithoutCancel(ctx), string(e.d.Bot), func(c *Control) {
		c.RateLimitedUntil = until
	}); err != nil {
		e.log.Error().Err(err).Msg("persist 429 pause")
	}
	e.warn(ctx, WarnRateLimited)
}

// warn applies a warning sign: the rate drops to a quarter (at least MinRatePerMin), calm time
// restarts, and repeated signs open the breaker (DESIGN.md §10.3, §10.5).
func (e *Executor) warn(ctx context.Context, w Warning) {
	now := e.clk.Now()
	e.mu.Lock()
	e.slowDown(now)
	cutoff := now.Add(-e.cfg.BreakerWindow)
	e.warnings = slices.DeleteFunc(e.warnings, func(t time.Time) bool { return !t.After(cutoff) })
	e.warnings = append(e.warnings, now)
	trip := !e.breakerOpen && len(e.warnings) >= e.cfg.BreakerWarnings
	rate, n := e.rate, len(e.warnings)
	e.mu.Unlock()
	e.log.Warn().Str("sign", string(w)).Float64("rate_per_min", rate).Msg("warning sign: slowing down")
	e.persistRate(ctx)
	if trip {
		if err := e.openBreaker(ctx, fmt.Sprintf("%d warning signs within %s, last: %s", n, e.cfg.BreakerWindow, w)); err != nil {
			e.log.Error().Err(err).Msg("persist breaker state")
		}
	}
}

// slowDown cuts the rate by SlowdownFactor (at least to MinRatePerMin) and restarts calm time. It must be
// called with e.mu held.
func (e *Executor) slowDown(now time.Time) {
	e.recover(now)
	e.rate = math.Max(e.cfg.MinRatePerMin, e.rate*e.cfg.SlowdownFactor)
	e.rate = math.Min(e.rate, float64(e.ceiling))
	e.lastWarning, e.lastRecovery = now, now
	e.resize(now)
}

// persistRate saves the slowed-down rate and its calm-time marks, so a restart doesn't reset
// the bot to full speed.
func (e *Executor) persistRate(ctx context.Context) {
	e.mu.Lock()
	rate, warned, recovered, lost := e.rate, e.lastWarning, e.lastRecovery, e.lockLost
	e.mu.Unlock()
	if lost {
		return // a standing-by executor mustn't overwrite the owner's rate
	}
	if _, err := e.d.Store.UpdateControl(context.WithoutCancel(ctx), string(e.d.Bot), func(c *Control) {
		c.Rate, c.LastWarning, c.LastRecovery = rate, warned, recovered
	}); err != nil {
		e.log.Error().Err(err).Msg("persist write rate")
	}
}

// recover raises a slowed-down rate by RecoveryStep writes/min per RecoverEvery of calm, never
// above the ceiling. It must be called with e.mu held.
func (e *Executor) recover(now time.Time) {
	if e.rate >= float64(e.ceiling) {
		return
	}
	base := e.lastRecovery
	if e.lastWarning.After(base) {
		base = e.lastWarning
	}
	steps := math.Floor(float64(now.Sub(base)) / float64(e.cfg.RecoverEvery))
	if steps < 1 {
		return
	}
	e.rate = math.Min(float64(e.ceiling), e.rate+steps*e.cfg.RecoveryStep)
	e.lastRecovery = base.Add(time.Duration(steps) * e.cfg.RecoverEvery)
	e.resize(now)
}

// resize fits the write bucket to the current rate: a slowed-down bot, or one whose ceiling was
// cut, also gets a proportionally smaller burst. It must be called with e.mu held.
func (e *Executor) resize(now time.Time) {
	full := math.Max(float64(e.ceiling), float64(e.cfg.CeilingPerMin))
	capacity := math.Max(1, math.Floor(float64(e.cfg.Burst)*e.rate/full))
	e.writes.set(capacity, e.rate, now)
}

// openBreaker opens the circuit breaker, slows the rate down for when it closes, and alerts the
// operator, once.
func (e *Executor) openBreaker(ctx context.Context, reason string) error {
	now := e.clk.Now()
	e.mu.Lock()
	already := e.breakerOpen
	if !already {
		e.breakerOpen, e.breakerAt = true, now
		e.slowDown(now)
	}
	e.mu.Unlock()
	if already {
		return nil
	}
	e.persistRate(ctx)
	_, err := e.d.Store.UpdateControl(context.WithoutCancel(ctx), string(e.d.Bot), func(c *Control) {
		c.BreakerOpen, c.BreakerReason, c.BreakerAt = true, reason, now
	})
	e.log.Error().Str("reason", reason).Msg("circuit breaker open: only P0 lockdowns run until an operator closes it")
	e.alert(ctx, AlertBreakerOpen, "WhatsApp writes stopped (circuit breaker open): "+reason+
		". Detections continue; close the breaker on the operator page once the cause is gone.")
	if err != nil {
		return fmt.Errorf("persist breaker: %w", err)
	}
	return nil
}

func (e *Executor) alert(ctx context.Context, kind AlertKind, msg string) {
	if e.d.Alerts == nil {
		e.log.Warn().Str("alert", string(kind)).Msg(msg)
		return
	}
	if err := e.d.Alerts.Alert(context.WithoutCancel(ctx), Alert{Bot: e.d.Bot, Kind: kind, Message: msg}); err != nil {
		e.log.Error().Err(err).Str("alert", string(kind)).Msg("operator alert failed")
	}
}

// OpenBreaker opens the bot's circuit breaker (a temporary ban, logout or replaced stream seen by
// the connector) and alerts the operator. Only an operator closes it again.
func (e *Executor) OpenBreaker(ctx context.Context, reason string) error {
	return e.openBreaker(ctx, reason)
}

// Warn reports a warning sign seen outside the executor (e.g. the connector's disconnects).
// WarnDisconnect only counts when a write went out within Config.DisconnectWindow.
func (e *Executor) Warn(ctx context.Context, w Warning) {
	if w == WarnDisconnect {
		e.mu.Lock()
		recent := !e.lastWrite.IsZero() && e.clk.Now().Sub(e.lastWrite) <= e.cfg.DisconnectWindow
		e.mu.Unlock()
		if !recent {
			return
		}
	}
	if w == WarnRateLimited {
		e.rateLimited(ctx)
		return
	}
	e.warn(ctx, w)
}

// Read runs fn, a WhatsApp read (an IQ), under the bot's separate read budget. It waits for a
// read token, returns ErrPaused or ErrBreakerOpen at once while those hold, and treats a 429
// from fn like one from a write.
func (e *Executor) Read(ctx context.Context, fn func(context.Context) error) error {
	for {
		now := e.clk.Now()
		e.mu.Lock()
		var stop error
		switch {
		case e.killed, now.Before(e.pausedUntil):
			stop = ErrPaused
		case e.breakerOpen:
			stop = ErrBreakerOpen
		}
		d := e.reads.wait(now)
		if stop == nil && d == 0 {
			e.reads.take(now)
		}
		e.mu.Unlock()
		if stop != nil {
			return stop
		}
		if d == 0 {
			break
		}
		if err := e.clk.Sleep(ctx, d); err != nil {
			return err
		}
	}
	e.callMu.Lock()
	defer e.callMu.Unlock()
	// Re-check under the call lock: the executor must still own the bot, a write may have just
	// hit a 429 or a ban, or the operator may have paused everything while this read waited.
	e.mu.Lock()
	locked, lost := e.locked, e.lockLost
	e.mu.Unlock()
	if lost || (locked && !e.checkLock(ctx)) {
		return fmt.Errorf("read for %s: %w", maskJID(e.d.Bot), ErrLockLost)
	}
	if err := e.refreshControl(ctx); err != nil {
		return fmt.Errorf("re-read control state before a read: %w", err)
	}
	e.mu.Lock()
	now := e.clk.Now()
	stopped, open := e.killed || now.Before(e.pausedUntil), e.breakerOpen
	e.mu.Unlock()
	switch {
	case stopped:
		return ErrPaused
	case open:
		return ErrBreakerOpen
	}
	cctx, cancel := context.WithTimeout(ctx, e.cfg.CallTimeout)
	err := fn(cctx)
	cancel()
	// The consequences are applied before the call lock is released, so no write slips in
	// between a 429 and its pause.
	e.reactUrgent(ctx, classify(ctx, err), err)
	return err
}

// settleAttempts and settleBackoff: a settle write that fails (a database blip) is tried again
// before the action is left running for Recover, which would make WhatsApp do it twice.
const (
	settleAttempts = 4
	settleBackoff  = 100 * time.Millisecond
)

// finish settles an action as final, retrying the store write.
func (e *Executor) finish(ctx context.Context, id int64, status Status, errText string, res Result) error {
	err := e.finishQuiet(ctx, id, status, errText, res)
	e.report(ctx, id)
	return err
}

// finishQuiet settles an action as final without telling Deps.OnFinished; the caller reports it
// with report, e.g. once it no longer holds the call lock.
func (e *Executor) finishQuiet(ctx context.Context, id int64, status Status, errText string, res Result) error {
	return e.settle(ctx, func() error { return e.d.Store.Finish(ctx, id, status, errText, res) })
}

// report tells Deps.OnFinished about action id if it is final. It looks even after a failed
// settle write, which may have committed before its error came back.
func (e *Executor) report(ctx context.Context, id int64) {
	if e.d.OnFinished == nil {
		return
	}
	a, err := e.d.Store.Get(ctx, id)
	if err != nil {
		e.log.Error().Err(err).Int64("action", id).Msg("read finished action for OnFinished")
		return
	}
	if a.Status.Final() {
		e.finished(ctx, a)
	}
}

// finished tells Deps.OnFinished about a final action.
func (e *Executor) finished(ctx context.Context, a Action) {
	if e.d.OnFinished == nil {
		return
	}
	hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), e.cfg.CallTimeout)
	defer cancel()
	if err := e.d.OnFinished(hctx, a); err != nil {
		e.log.Error().Err(err).Int64("action", a.ID).Msg("OnFinished hook failed")
	}
}

// retry puts an action back to pending, retrying the store write.
func (e *Executor) retry(ctx context.Context, id int64, r RetryInfo) error {
	return e.settle(ctx, func() error { return e.d.Store.Retry(ctx, id, r) })
}

func (e *Executor) settle(ctx context.Context, write func() error) error {
	var err error
	d := settleBackoff
	for i := range settleAttempts {
		if err = write(); err == nil {
			return nil
		}
		if i < settleAttempts-1 {
			e.sleep(ctx, d)
			d *= 4
		}
	}
	return err
}
