package blacklist

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// normalizeAll normalizes raw inputs, reporting invalid ones and dropping duplicates.
func (s *Service) normalizeAll(ctx context.Context, group wa.JID, raws []string) (valid, invalid []string, err error) {
	country := ""
	if group != "" {
		gs, err := s.d.Settings.Blacklist(ctx, group)
		if err != nil {
			return nil, nil, fmt.Errorf("blacklist settings %s: %w", group, err)
		}
		country = gs.DefaultCountry
	}
	for _, raw := range raws {
		e, err := s.d.Normalize.Normalize(raw, country)
		if err != nil {
			invalid = append(invalid, raw)
			continue
		}
		if !slices.Contains(valid, e) {
			valid = append(valid, e)
		}
	}
	return valid, invalid, nil
}

func (s *Service) add(ctx context.Context, r AddRequest) (AddResult, error) {
	if r.Instance && !r.Operator {
		return AddResult{}, ErrNotOperator
	}
	scope, group := ScopeGroup, r.Group
	if r.Instance {
		scope, group = ScopeInstance, ""
	}
	var res AddResult
	numbers, invalid, err := s.normalizeAll(ctx, r.Group, r.Numbers)
	if err != nil {
		return res, err
	}
	res.Invalid = invalid
	now := s.now()
	for _, n := range numbers {
		e, added, err := s.d.List.Add(ctx, Entry{Scope: scope, Group: group, E164: n, Reason: r.Reason, AddedBy: r.By, AddedAt: now})
		if err != nil {
			return res, fmt.Errorf("blacklist add: %w", err)
		}
		if !added {
			res.Already = append(res.Already, n)
			continue
		}
		res.Added = append(res.Added, e)
	}
	if len(res.Added) == 0 {
		return res, nil
	}
	var errs []error
	if s.d.Audit != nil {
		errs = append(errs, s.d.Audit.Record(ctx, review.AuditEntry{
			Actor: r.By, Group: r.Group, Action: "blacklist.add", At: now,
			Diff: map[string]any{"scope": string(scope), "numbers": e164s(res.Added), "reason": r.Reason},
		}))
	}
	// Resolution failures don't undo the entry: it still works as a content signal (§9.8).
	if s.d.Resolver != nil {
		if _, err := s.d.Resolver.Resolve(ctx, e164s(res.Added)); err != nil {
			errs = append(errs, fmt.Errorf("resolve blacklisted numbers: %w", err))
		}
	}
	errs = append(errs, s.enforceNow(ctx, r, scope, &res))
	return res, errors.Join(errs...)
}

func e164s(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.E164
	}
	return out
}

// enforceNow bans current members who were just listed, in the group or (instance-wide) in
// every protected group that uses the instance list.
func (s *Service) enforceNow(ctx context.Context, r AddRequest, scope Scope, res *AddResult) error {
	groups := []wa.JID{r.Group}
	if scope == ScopeInstance {
		var err error
		if groups, err = s.d.Groups.Protected(ctx); err != nil {
			return fmt.Errorf("protected groups: %w", err)
		}
	}
	var errs []error
	for _, g := range groups {
		if g == "" {
			continue
		}
		gs, err := s.d.Settings.Blacklist(ctx, g)
		if err != nil {
			errs = append(errs, fmt.Errorf("blacklist settings %s: %w", g, err))
			continue
		}
		members, err := s.d.Groups.Members(ctx, g)
		if err != nil {
			errs = append(errs, fmt.Errorf("members of %s: %w", g, err))
			continue
		}
		for _, m := range members {
			p := Person{JID: m.JID, LID: m.LID, Phone: m.Phone}
			e, ok, err := s.listed(ctx, g, p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !ok || !slices.Contains(e164s(res.Added), e.E164) {
				continue
			}
			if m.Admin {
				// Never act on admins (§8.6): the adder is told instead.
				res.Skipped = append(res.Skipped, m.JID)
				if s.d.Notifier != nil && r.By != "" {
					errs = append(errs, s.d.Notifier.Notify(ctx, r.By, g, "blacklist.admin_skipped", map[string]string{
						"number": review.MaskNumber(e.E164),
					}))
				}
				continue
			}
			did, code, err := s.actOn(ctx, g, gs, e, p, r.By, nil)
			errs = append(errs, err)
			switch did {
			case "ban":
				res.Banned = append(res.Banned, m.JID)
			case "review":
				res.Review = append(res.Review, code)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) remove(ctx context.Context, r RemoveRequest) (RemoveResult, error) {
	if r.Instance && !r.Operator {
		return RemoveResult{}, ErrNotOperator
	}
	scope, group := ScopeGroup, r.Group
	if r.Instance {
		scope, group = ScopeInstance, ""
	}
	var res RemoveResult
	numbers, invalid, err := s.normalizeAll(ctx, r.Group, r.Numbers)
	if err != nil {
		return res, err
	}
	res.Invalid = invalid
	var errs []error
	for _, n := range numbers {
		before, err := s.d.List.ByNumber(ctx, n)
		if err != nil {
			return res, fmt.Errorf("blacklist lookup: %w", err)
		}
		removed, err := s.d.List.Remove(ctx, scope, group, n, r.By)
		if err != nil {
			return res, fmt.Errorf("blacklist remove: %w", err)
		}
		if !removed {
			res.NotFound = append(res.NotFound, n)
			continue
		}
		res.Removed = append(res.Removed, n)
		errs = append(errs, s.liftBlocks(ctx, r, scope, n, before))
	}
	if len(res.Removed) > 0 && s.d.Audit != nil {
		errs = append(errs, s.d.Audit.Record(ctx, review.AuditEntry{
			Actor: r.By, Group: r.Group, Action: "blacklist.remove", At: s.now(),
			Diff: map[string]any{"scope": string(scope), "numbers": res.Removed},
		}))
	}
	return res, errors.Join(errs...)
}

// liftBlocks lifts the rejoin block the entry caused, without re-adding anyone (§9.8 Undo).
func (s *Service) liftBlocks(ctx context.Context, r RemoveRequest, scope Scope, n string, before []Entry) error {
	target := phoneJID(n)
	for _, e := range before {
		if e.Person != "" {
			target = e.Person
		}
	}
	groups := []wa.JID{r.Group}
	if scope == ScopeInstance {
		var err error
		if groups, err = s.d.Groups.Protected(ctx); err != nil {
			return fmt.Errorf("protected groups: %w", err)
		}
	}
	var errs []error
	for _, g := range groups {
		if g == "" {
			continue
		}
		if scope == ScopeInstance {
			gs, err := s.d.Settings.Blacklist(ctx, g)
			if err != nil {
				errs = append(errs, fmt.Errorf("blacklist settings %s: %w", g, err))
				continue
			}
			if !gs.UseInstance || gs.Mode.AskMode() {
				// The instance entry never banned here (opted out, or it only opened a review).
				continue
			}
		}
		// Another entry that still applies keeps the block.
		if _, still, err := s.listed(ctx, g, Person{JID: target, Phone: phoneJID(n), LID: lidOf(target)}); err != nil || still {
			errs = append(errs, err)
			continue
		}
		// Via "blacklist" lifts only a block the blacklist placed, never an admin's own ban.
		errs = append(errs, s.d.Actions.Enqueue(ctx, review.Unban{Group: g, Target: target, Actor: r.By, Via: "blacklist"}))
	}
	return errors.Join(errs...)
}

func lidOf(j wa.JID) wa.JID {
	if j.IsLID() {
		return j
	}
	return ""
}

func (s *Service) check(ctx context.Context, group wa.JID, number string) (CheckResult, error) {
	valid, _, err := s.normalizeAll(ctx, group, []string{number})
	if err != nil {
		return CheckResult{}, err
	}
	if len(valid) == 0 {
		return CheckResult{}, fmt.Errorf("%q: %w", number, ErrInvalid)
	}
	res := CheckResult{E164: valid[0]}
	gs, err := s.d.Settings.Blacklist(ctx, group)
	if err != nil {
		return res, fmt.Errorf("blacklist settings %s: %w", group, err)
	}
	es, err := s.d.List.ByNumber(ctx, res.E164)
	if err != nil {
		return res, fmt.Errorf("blacklist lookup: %w", err)
	}
	p := Person{Phone: phoneJID(res.E164)}
	for _, e := range es {
		switch {
		case e.Scope == ScopeGroup && e.Group == group && res.Group == nil:
			res.Group = &e
		case e.Scope == ScopeInstance && gs.UseInstance && res.Instance == nil:
			res.Instance = &e
		}
		if e.Person != "" {
			p.LID = e.Person
		}
	}
	_, res.Member, err = s.member(ctx, group, p)
	return res, err
}
