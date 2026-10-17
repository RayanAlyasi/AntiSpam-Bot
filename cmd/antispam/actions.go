package main

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/alert"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/connector"
)

// actionsAlerter delivers the action queue's operator alerts (breaker open, daily writes) through
// the alert notifier and its sinks (DESIGN.md §10.3, §10.5, §16).
type actionsAlerter struct{ n *alert.Notifier }

func (a actionsAlerter) Alert(_ context.Context, al actions.Alert) error {
	sev, title := alert.Warning, "Action queue"
	switch al.Kind {
	case actions.AlertBreakerOpen:
		sev, title = alert.Critical, "WhatsApp writes stopped (circuit breaker open)"
	case actions.AlertDailyWrites:
		title = "Unusually many WhatsApp writes today"
	case actions.AlertNotAdmin:
		title = "The bot lost admin in a group"
	}
	a.n.Notify(alert.Alert{Key: fmt.Sprintf("actions.%s:%s", al.Kind, al.Bot), Severity: sev, Title: title, Text: al.Message, Bot: al.Bot})
	return nil
}

// managerConnector lets the action runner see the connector's bots once the manager exists.
type managerConnector struct {
	m *atomic.Pointer[connector.Manager]
}

func (c managerConnector) Connected() []wa.JID {
	if m := c.m.Load(); m != nil {
		return m.Connected()
	}
	return nil
}

func (c managerConnector) Client(bot wa.JID) (wa.WAClient, bool) {
	if m := c.m.Load(); m != nil {
		return m.Client(bot)
	}
	return nil, false
}

// markNotAdmin records in the groups table that the bot lost admin in a group: the group goes
// back to pending_admin, like the tracker does, so routing stops treating it as moderated and
// the dashboard shows it (DESIGN.md §10.5).
func markNotAdmin(groups identity.GroupStore) func(ctx context.Context, bot, group wa.JID) error {
	return func(ctx context.Context, _, group wa.JID) error {
		g, ok, err := groups.Group(ctx, group)
		if err != nil || !ok || g.Status == identity.GroupLeft {
			return err
		}
		if !g.BotIsAdmin && g.Status == identity.GroupPendingAdmin {
			return nil
		}
		g.BotIsAdmin, g.Status = false, identity.GroupPendingAdmin
		return groups.UpsertGroup(ctx, g)
	}
}
