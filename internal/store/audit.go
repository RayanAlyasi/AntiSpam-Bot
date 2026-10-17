package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

// Audit actors (DESIGN.md §14: actor is a phone, "system" or "operator"). A person acting is
// recorded by their identifier; these constants cover the rest.
const (
	ActorSystem   = "system"
	ActorOperator = "operator"
)

// AuditEntry is one audit_log row: a settings change, command, manual action or automated action
// (DESIGN.md §8.6, §13.3).
type AuditEntry struct {
	ID       int64
	Actor    string // who did it: a phone or LID, ActorSystem or ActorOperator
	GroupJID string // the group it touched; empty for instance-wide entries
	Action   string // what was done, e.g. "settings.update", "command.blacklist", "action.ban"
	Diff     json.RawMessage
	At       time.Time
}

// ErrInvalidAuditEntry is returned for an entry without an actor or action, or whose diff isn't
// a JSON object.
var ErrInvalidAuditEntry = errors.New("invalid audit entry")

// WriteAudit appends e to audit_log through q, which may be a transaction so the entry commits
// or rolls back with the change it records. A nil Diff is stored as {}. It returns the stored
// entry with its ID and time.
func WriteAudit(ctx context.Context, q db.DBTX, e AuditEntry) (AuditEntry, error) {
	if e.Actor == "" || e.Action == "" {
		return AuditEntry{}, fmt.Errorf("%w: actor and action are required", ErrInvalidAuditEntry)
	}
	if len(e.Diff) == 0 {
		e.Diff = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(e.Diff, &obj); err != nil || obj == nil {
		return AuditEntry{}, fmt.Errorf("%w: diff must be a JSON object", ErrInvalidAuditEntry)
	}
	row, err := db.New(q).InsertAuditLog(ctx, db.InsertAuditLogParams{
		Actor:    e.Actor,
		GroupJid: pgtype.Text{String: e.GroupJID, Valid: e.GroupJID != ""},
		Action:   e.Action,
		Diff:     e.Diff,
	})
	if err != nil {
		return AuditEntry{}, fmt.Errorf("insert audit_log %s: %w", e.Action, err)
	}
	e.ID, e.At = row.ID, row.At
	return e, nil
}

// ListAudit returns up to limit entries for groupJID, newest first; an empty groupJID lists the
// instance-wide entries (those written without a group). Pass beforeID 0 for the first page, then
// the smallest ID seen to get the next one.
func ListAudit(ctx context.Context, q db.DBTX, groupJID string, beforeID int64, limit int32) ([]AuditEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	var rows []db.AppAuditLog
	var err error
	if groupJID == "" {
		rows, err = db.New(q).ListAuditLogInstance(ctx, db.ListAuditLogInstanceParams{BeforeID: beforeID, RowLimit: limit})
	} else {
		rows, err = db.New(q).ListAuditLogByGroup(ctx, db.ListAuditLogByGroupParams{
			GroupJid: groupJID, BeforeID: beforeID, RowLimit: limit,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("list audit_log: %w", err)
	}
	out := make([]AuditEntry, len(rows))
	for i, r := range rows {
		out[i] = AuditEntry{ID: r.ID, Actor: r.Actor, GroupJID: r.GroupJid.String, Action: r.Action, Diff: r.Diff, At: r.At}
	}
	return out, nil
}
