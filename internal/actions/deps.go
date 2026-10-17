package actions

import (
	"context"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Groups is what the executor needs to know about, and tell, the group state (DESIGN.md §9.7,
// §10.5). The group-state module implements it.
type Groups interface {
	// Posting says whether chat is a protected group and whether it is silent. Chats that aren't
	// groups (DMs) are never protected. An error makes the executor refuse the post.
	Posting(ctx context.Context, chat wa.JID) (Posting, error)
	// NotAdmin records that bot is no longer an admin of group, so the dashboard can show it.
	NotAdmin(ctx context.Context, bot, group wa.JID) error
}

// Posting is the part of a chat's settings that decides whether the bot may post there.
type Posting struct {
	Protected bool // the bot protects this group
	Silent    bool // nothing is posted in the group (the default for protected groups)
}

// AlertKind names an operator alert raised by the queue.
type AlertKind string

// Alert kinds.
const (
	AlertBreakerOpen AlertKind = "breaker_open" // writes stopped (temporary ban, logout, repeated warnings)
	AlertDailyWrites AlertKind = "daily_writes" // the bot passed Config.DailyAlert writes today
	AlertNotAdmin    AlertKind = "not_admin"    // the bot lost admin in a group
)

// Alert is an operator alert (dashboard banner, webhook, Telegram, email).
type Alert struct {
	Bot     wa.JID
	Kind    AlertKind
	Message string
}

// Alerter delivers operator alerts. The alerts module implements it.
type Alerter interface {
	Alert(ctx context.Context, a Alert) error
}

// AuditEntry is one audit-log record of an operator decision about the queue.
type AuditEntry struct {
	Actor  string
	Action string // e.g. "actions.pause", "actions.ceiling"
	Diff   map[string]any
}

// Auditor writes audit-log entries (DESIGN.md §14 audit_log). The audit module implements it.
type Auditor interface {
	Audit(ctx context.Context, e AuditEntry) error
}

// Warning is a sign that WhatsApp may be unhappy with the bot's rate (DESIGN.md §10.3).
type Warning string

// Warning signs.
const (
	WarnRateLimited Warning = "rate_limited" // a 429 (rate-overlimit)
	WarnUnusual     Warning = "unusual"      // an unusual or unexpected error
	WarnSlowAck     Warning = "slow_ack"     // a server ack well above normal
	WarnDisconnect  Warning = "disconnect"   // a disconnect shortly after writes
)
