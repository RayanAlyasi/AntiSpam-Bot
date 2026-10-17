package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// AuditedControl is a Store that can change control state and write its audit entry in one
// transaction (the Postgres store). Operator prefers it, so a change is never left unaudited.
type AuditedControl interface {
	UpdateControlAudited(ctx context.Context, scope string, change func(*Control) AuditEntry) (Control, error)
}

// Operator holds the operator's controls over the queue: the global kill switch, closing a
// breaker and changing a bot's ceiling (DESIGN.md §10.3, §10.5). Every change is audited.
// Executors pick changes up from the store within Config.PollInterval.
type Operator struct {
	Store Store
	Audit Auditor     // not needed when Store implements AuditedControl
	Clock clock.Clock // clock.Real when nil
}

// Pause turns the global kill switch on: no action runs on any bot until Resume.
func (o Operator) Pause(ctx context.Context, actor, reason string) error {
	return o.update(ctx, GlobalScope, func(c *Control) AuditEntry {
		c.Paused, c.PausedBy, c.PausedAt = true, actor, o.now()
		return AuditEntry{Actor: actor, Action: "actions.pause", Diff: map[string]any{"paused": true, "reason": reason}}
	})
}

// Resume turns the global kill switch off.
func (o Operator) Resume(ctx context.Context, actor string) error {
	return o.update(ctx, GlobalScope, func(c *Control) AuditEntry {
		c.Paused, c.PausedBy, c.PausedAt = false, actor, o.now()
		return AuditEntry{Actor: actor, Action: "actions.resume", Diff: map[string]any{"paused": false}}
	})
}

// CloseBreaker closes bot's circuit breaker. The rate stays slowed down and recovers as usual.
func (o Operator) CloseBreaker(ctx context.Context, bot wa.JID, actor string) error {
	return o.update(ctx, string(bot), func(c *Control) AuditEntry {
		reason := c.BreakerReason
		c.BreakerOpen, c.BreakerReason, c.BreakerClosedAt = false, "", o.now()
		return AuditEntry{Actor: actor, Action: "actions.breaker_close", Diff: map[string]any{"bot": string(bot), "was": reason}}
	})
}

// SetCeiling sets bot's write ceiling to perMin writes/min (0 returns to the configured one).
// Raising it is risky: it increases the chance of a ban (DESIGN.md §10.3). The current rate
// doesn't jump: it keeps recovering by 1 write/min per calm period, up to the new ceiling.
func (o Operator) SetCeiling(ctx context.Context, bot wa.JID, actor string, perMin int) error {
	if perMin < 0 {
		return fmt.Errorf("%w: ceiling %d is negative", ErrInvalid, perMin)
	}
	return o.update(ctx, string(bot), func(c *Control) AuditEntry {
		old := c.Ceiling
		c.Ceiling = perMin
		return AuditEntry{Actor: actor, Action: "actions.ceiling", Diff: map[string]any{"bot": string(bot), "from": old, "to": perMin}}
	})
}

// update applies change and audits it: in one transaction when the store can, otherwise
// change first and then the audit entry.
func (o Operator) update(ctx context.Context, scope string, change func(*Control) AuditEntry) error {
	if o.Store == nil {
		return errors.New("actions operator needs a store")
	}
	if ac, ok := o.Store.(AuditedControl); ok {
		if _, err := ac.UpdateControlAudited(ctx, scope, change); err != nil {
			return fmt.Errorf("update queue control %s: %w", scope, err)
		}
		return nil
	}
	if o.Audit == nil {
		return errors.New("actions operator needs an auditor: queue controls are always audited")
	}
	var entry AuditEntry
	if _, err := o.Store.UpdateControl(ctx, scope, func(c *Control) { entry = change(c) }); err != nil {
		return fmt.Errorf("update queue control %s: %w", scope, err)
	}
	if err := o.Audit.Audit(ctx, entry); err != nil {
		return fmt.Errorf("audit %s: %w", entry.Action, err)
	}
	return nil
}

func (o Operator) now() time.Time {
	if o.Clock == nil {
		return clock.Real{}.Now()
	}
	return o.Clock.Now()
}
