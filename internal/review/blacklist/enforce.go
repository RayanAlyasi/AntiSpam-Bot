package blacklist

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// reviewCooldown bounds how often one person can produce a blacklist review item in one group,
// so a listed sender who keeps posting doesn't flood the review destination.
const reviewCooldown = 24 * time.Hour

// phoneJID returns the phone-number JID for an E.164 number.
func phoneJID(e164 string) wa.JID {
	return wa.JID(strings.TrimPrefix(e164, "+") + "@" + wa.ServerUser)
}

// reviewMemo remembers recent blacklist review items per group and person.
type reviewMemo struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (r *reviewMemo) release(group, person wa.JID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.last, string(group)+"|"+string(person))
}

func (r *reviewMemo) take(group, person wa.JID, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	k := string(group) + "|" + string(person)
	if t, ok := r.last[k]; ok && now.Sub(t) < reviewCooldown {
		return false
	}
	r.last[k] = now
	return true
}

func (s *Service) now() time.Time { return s.d.Clock.Now() }

// matches reports whether an entry identifies p.
func matches(e Entry, p Person) bool {
	if e.Person != "" && (e.Person == p.LID || e.Person == p.JID) {
		return true
	}
	ph := phoneJID(e.E164)
	return ph == p.Phone || ph == p.JID
}

// applies reports whether an entry applies in group with settings gs.
func applies(e Entry, group wa.JID, gs GroupSettings) bool {
	if e.Scope == ScopeInstance {
		return gs.UseInstance
	}
	return e.Group == group
}

func (s *Service) listed(ctx context.Context, group wa.JID, p Person) (Entry, bool, error) {
	gs, err := s.d.Settings.Blacklist(ctx, group)
	if err != nil {
		return Entry{}, false, fmt.Errorf("blacklist settings %s: %w", group, err)
	}
	var cands []Entry
	for _, lid := range []wa.JID{p.LID, p.JID} {
		if lid.IsLID() {
			es, err := s.d.List.ByPerson(ctx, lid)
			if err != nil {
				return Entry{}, false, fmt.Errorf("blacklist by person: %w", err)
			}
			cands = append(cands, es...)
		}
	}
	for _, ph := range []wa.JID{p.Phone, p.JID} {
		if ph.Server() == wa.ServerUser {
			es, err := s.d.List.ByNumber(ctx, "+"+ph.User())
			if err != nil {
				return Entry{}, false, fmt.Errorf("blacklist by number: %w", err)
			}
			cands = append(cands, es...)
		}
	}
	// The group's own entry wins over an instance-wide one: it is that group's admins' decision.
	slices.SortStableFunc(cands, func(a, b Entry) int {
		if a.Scope == b.Scope {
			return 0
		}
		if a.Scope == ScopeGroup {
			return -1
		}
		return 1
	})
	for _, e := range cands {
		if applies(e, group, gs) && matches(e, p) {
			return e, true, nil
		}
	}
	return Entry{}, false, nil
}

// subject is the review subject for a listed person.
func subject(p Person) review.Subject {
	id := p.LID
	if id == "" {
		id = p.JID
	}
	return review.Subject{Person: id, Phone: p.Phone}
}

// actOn bans a listed member, or opens a review item when the entry is instance-wide and the
// group asks its admins. It reports what it did: "ban", "review" or "".
func (s *Service) actOn(ctx context.Context, group wa.JID, gs GroupSettings, e Entry, p Person, actor wa.JID, msg *wa.MessageRef) (string, string, error) {
	if gs.Inactive {
		return "", "", nil
	}
	if e.Scope == ScopeInstance && gs.Mode.AskMode() {
		if !s.memo.take(group, subject(p).Person, s.now()) || s.d.Reviews == nil {
			return "", "", nil
		}
		it, err := s.d.Reviews.Open(ctx, review.NewItem{
			Group: group, Kind: review.KindNetworkIncoming, Subject: subject(p), Message: msg, Proposed: review.DecisionBan,
			Evidence: review.Evidence{
				Source: "known", WhyAsking: "instance blacklist",
				MessageDeleted: msg != nil && gs.Mode == review.ModeAskDeleteFirst,
			},
		})
		if err != nil {
			s.memo.release(group, subject(p).Person)
			return "", "", fmt.Errorf("open blacklist review in %s: %w", group, err)
		}
		return "review", it.Code, nil
	}
	target := p.JID
	if target == "" {
		target = p.LID
	}
	if err := s.d.Actions.Enqueue(ctx, review.Ban{
		Group: group, Target: target, Reason: review.ReasonOther, Actor: actor, Via: "blacklist",
	}); err != nil {
		return "", "", fmt.Errorf("ban listed member in %s: %w", group, err)
	}
	return "ban", "", nil
}

func (s *Service) onJoin(ctx context.Context, group wa.JID, p Person) (bool, error) {
	e, ok, err := s.listed(ctx, group, p)
	if err != nil || !ok {
		return false, err
	}
	gs, err := s.d.Settings.Blacklist(ctx, group)
	if err != nil {
		return true, fmt.Errorf("blacklist settings %s: %w", group, err)
	}
	_, _, err = s.actOn(ctx, group, gs, e, p, "", nil)
	return true, err
}

func (s *Service) onJoinRequests(ctx context.Context, group wa.JID, people []Person) ([]wa.JID, error) {
	gs, err := s.d.Settings.Blacklist(ctx, group)
	if err != nil {
		return nil, fmt.Errorf("blacklist settings %s: %w", group, err)
	}
	if gs.Inactive {
		return nil, nil
	}
	var rejected []wa.JID
	for _, p := range people {
		e, ok, err := s.listed(ctx, group, p)
		if err != nil {
			return nil, err
		}
		if !ok || (e.Scope == ScopeInstance && gs.Mode.AskMode()) {
			// Ask-admins groups decide instance-wide entries themselves; the request stays pending.
			continue
		}
		t := p.JID
		if t == "" {
			t = p.LID
		}
		rejected = append(rejected, t)
	}
	if len(rejected) == 0 {
		return nil, nil
	}
	if err := s.d.Actions.Enqueue(ctx, review.RejectJoin{Group: group, Targets: rejected, Reason: "blacklist"}); err != nil {
		return nil, fmt.Errorf("reject blacklisted join requests in %s: %w", group, err)
	}
	return rejected, nil
}

// member finds p among a group's members.
func (s *Service) member(ctx context.Context, group wa.JID, p Person) (Member, bool, error) {
	ms, err := s.d.Groups.Members(ctx, group)
	if err != nil {
		return Member{}, false, fmt.Errorf("members of %s: %w", group, err)
	}
	for _, m := range ms {
		if (p.JID != "" && (m.JID == p.JID || m.LID == p.JID || m.Phone == p.JID)) ||
			(p.LID != "" && (m.LID == p.LID || m.JID == p.LID)) ||
			(p.Phone != "" && (m.Phone == p.Phone || m.JID == p.Phone)) {
			return m, true, nil
		}
	}
	return Member{}, false, nil
}

func (s *Service) onMessage(ctx context.Context, group wa.JID, msg wa.MessageRef, sender Person) (bool, error) {
	if sender.JID == "" {
		sender.JID = msg.Sender
	}
	e, ok, err := s.listed(ctx, group, sender)
	if err != nil || !ok {
		return false, err
	}
	m, isMember, err := s.member(ctx, group, sender)
	if err != nil {
		return true, err
	}
	if isMember && m.Admin {
		// The bot never acts on group admins (§8.6).
		return true, nil
	}
	gs, err := s.d.Settings.Blacklist(ctx, group)
	if err != nil {
		return true, fmt.Errorf("blacklist settings %s: %w", group, err)
	}
	if gs.Inactive {
		return true, nil
	}
	askInstance := e.Scope == ScopeInstance && gs.Mode.AskMode()
	var errs []error
	if !askInstance || gs.Mode == review.ModeAskDeleteFirst {
		errs = append(errs, s.d.Actions.Enqueue(ctx, review.Delete{
			Group: group, Message: msg, Priority: review.P1, Reason: "blacklist",
		}))
	}
	if isMember || askInstance {
		p := sender
		if isMember {
			p = Person{JID: m.JID, LID: m.LID, Phone: m.Phone}
		}
		_, _, err := s.actOn(ctx, group, gs, e, p, "", &msg)
		errs = append(errs, err)
	}
	return true, errors.Join(errs...)
}
