package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

// BlacklistEntry is an active number_blacklist row (DESIGN.md §9.8). E164 is personal data:
// show it masked to group admins (blacklist.Mask) and in full only to the operator.
type BlacklistEntry struct {
	ID         int64
	Scope      rules.BlacklistScope
	GroupJID   string // empty for instance entries
	E164       string
	Hash       string // keyed hash used for content matching
	Reason     string
	PersonID   *int64 // set once the number resolves to a person
	OnWhatsApp *bool
	AddedBy    *int64
	AddedAt    time.Time
}

// BlacklistAdd is one number to add.
type BlacklistAdd struct {
	Scope    rules.BlacklistScope
	GroupJID string // required for group entries, empty for instance entries
	E164     string
	Hash     string // blacklist.Hasher.Hash(E164)
	Reason   string
	AddedBy  *int64
	Now      time.Time
}

// ErrInvalidBlacklistEntry is returned for an entry with a bad scope, number or hash.
var ErrInvalidBlacklistEntry = errors.New("invalid blacklist entry")

var (
	e164Re = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func checkScope(scope rules.BlacklistScope, groupJID string) error {
	switch {
	case scope == rules.BlacklistGroup && groupJID == "":
		return fmt.Errorf("%w: group entry without a group", ErrInvalidBlacklistEntry)
	case scope == rules.BlacklistInstance && groupJID != "":
		return fmt.Errorf("%w: instance entry with a group", ErrInvalidBlacklistEntry)
	case scope != rules.BlacklistGroup && scope != rules.BlacklistInstance:
		return fmt.Errorf("%w: scope %q", ErrInvalidBlacklistEntry, scope)
	}
	return nil
}

// AddBlacklist adds a number. Adding a number that is already active in the same scope
// returns the existing entry and false, so repeating `.blacklist add` changes nothing.
func AddBlacklist(ctx context.Context, q db.DBTX, a BlacklistAdd) (BlacklistEntry, bool, error) {
	if err := checkScope(a.Scope, a.GroupJID); err != nil {
		return BlacklistEntry{}, false, err
	}
	if !e164Re.MatchString(a.E164) {
		return BlacklistEntry{}, false, fmt.Errorf("%w: number is not E.164", ErrInvalidBlacklistEntry)
	}
	if !hashRe.MatchString(a.Hash) {
		return BlacklistEntry{}, false, fmt.Errorf("%w: hash is not 64 hex digits", ErrInvalidBlacklistEntry)
	}
	params := db.AddBlacklistEntryParams{
		Scope: string(a.Scope), GroupJid: optText(a.GroupJID), PnE164: a.E164, PnHash: a.Hash,
		Reason: a.Reason, AddedByPerson: optInt8(a.AddedBy), Now: a.Now,
	}
	row, err := db.New(q).AddBlacklistEntry(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent identical add committed between this statement's insert attempt and its
		// lookup of the existing row; a second try sees it.
		row, err = db.New(q).AddBlacklistEntry(ctx, params)
	}
	if err != nil {
		return BlacklistEntry{}, false, fmt.Errorf("add blacklist entry: %w", err)
	}
	return BlacklistEntry{
		ID: row.ID, Scope: rules.BlacklistScope(row.Scope), GroupJID: row.GroupJid.String, E164: row.PnE164,
		Hash: row.PnHash, Reason: row.Reason, PersonID: ptr8(row.PersonID), OnWhatsApp: fromBool(row.OnWhatsapp),
		AddedBy: ptr8(row.AddedByPerson), AddedAt: row.AddedAt,
	}, row.Inserted, nil
}

// RemoveBlacklist removes an active number (the undo, §9.8 step 5). It reports false when the
// number wasn't active in that scope. The row is kept, with who removed it and when, for audit.
func RemoveBlacklist(ctx context.Context, q db.DBTX, scope rules.BlacklistScope, groupJID, e164 string, by *int64, now time.Time) (bool, error) {
	if err := checkScope(scope, groupJID); err != nil {
		return false, err
	}
	_, err := db.New(q).RemoveBlacklistEntry(ctx, db.RemoveBlacklistEntryParams{
		Now: now, RemovedByPerson: optInt8(by), Scope: string(scope), GroupJid: optText(groupJID), PnE164: e164,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remove blacklist entry: %w", err)
	}
	return true, nil
}

// ListGroupBlacklist returns a group's active entries, newest first.
func ListGroupBlacklist(ctx context.Context, q db.DBTX, groupJID string) ([]BlacklistEntry, error) {
	rows, err := db.New(q).ListGroupBlacklist(ctx, groupJID)
	if err != nil {
		return nil, fmt.Errorf("list group blacklist: %w", err)
	}
	out := make([]BlacklistEntry, len(rows))
	for i, r := range rows {
		out[i] = blacklistEntry(db.FindBlacklistEntriesRow(r))
	}
	return out, nil
}

// CheckBlacklist returns the active entries for a number that apply in a group: the group's
// own entry and any instance-wide one.
func CheckBlacklist(ctx context.Context, q db.DBTX, groupJID, e164 string) ([]BlacklistEntry, error) {
	rows, err := db.New(q).FindBlacklistEntries(ctx, db.FindBlacklistEntriesParams{PnE164: e164, GroupJid: groupJID})
	if err != nil {
		return nil, fmt.Errorf("check blacklist: %w", err)
	}
	out := make([]BlacklistEntry, len(rows))
	for i, r := range rows {
		out[i] = blacklistEntry(r)
	}
	return out, nil
}

// SetBlacklistIdentity links an entry to the person the number resolved to (§9.8 step 1).
func SetBlacklistIdentity(ctx context.Context, q db.DBTX, id int64, personID *int64, onWhatsApp *bool) error {
	err := db.New(q).SetBlacklistIdentity(ctx, db.SetBlacklistIdentityParams{
		ID: id, PersonID: optInt8(personID), OnWhatsapp: optBool(onWhatsApp),
	})
	if err != nil {
		return fmt.Errorf("set blacklist identity: %w", err)
	}
	return nil
}

func blacklistEntry(r db.FindBlacklistEntriesRow) BlacklistEntry {
	return BlacklistEntry{
		ID: r.ID, Scope: rules.BlacklistScope(r.Scope), GroupJID: r.GroupJid.String, E164: r.PnE164, Hash: r.PnHash,
		Reason: r.Reason, PersonID: ptr8(r.PersonID), OnWhatsApp: fromBool(r.OnWhatsapp), AddedBy: ptr8(r.AddedByPerson),
		AddedAt: r.AddedAt,
	}
}

func optText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func optBool(p *bool) pgtype.Bool {
	if p == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *p, Valid: true}
}

func fromBool(v pgtype.Bool) *bool {
	if !v.Valid {
		return nil
	}
	return &v.Bool
}
