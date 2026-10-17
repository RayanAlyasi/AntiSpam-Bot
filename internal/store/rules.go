package store

import (
	"context"
	"fmt"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
)

// RulesLoader reads the rules, fingerprints and active blacklist entries for the in-memory
// rules snapshot (rules.Holder).
type RulesLoader struct{ Q db.DBTX }

var _ rules.Loader = RulesLoader{}

// LoadRules implements rules.Loader: every enabled rule.
func (l RulesLoader) LoadRules(ctx context.Context) ([]rules.Rule, error) {
	rows, err := db.New(l.Q).ListEnabledRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	out := make([]rules.Rule, len(rows))
	for i, r := range rows {
		out[i] = rules.Rule{
			ID: r.ID, Scope: rules.Scope(r.Scope), ScopeID: r.ScopeID, Pack: r.Pack, Type: rules.Type(r.Type),
			Pattern: r.Pattern, NormPattern: r.NormPattern, ActionClass: rules.ActionClass(r.ActionClass),
			Weight: r.Weight, Enabled: r.Enabled,
		}
	}
	return out, nil
}

// LoadFingerprints implements rules.Loader.
func (l RulesLoader) LoadFingerprints(ctx context.Context) ([]rules.Fingerprint, error) {
	rows, err := db.New(l.Q).ListFingerprints(ctx)
	if err != nil {
		return nil, fmt.Errorf("list fingerprints: %w", err)
	}
	out := make([]rules.Fingerprint, len(rows))
	for i, r := range rows {
		out[i] = rules.Fingerprint{
			Kind: rules.FingerprintKind(r.Kind), Value: r.Value, Label: rules.Label(r.Label),
			Source: rules.Source(r.Source), Scope: rules.Scope(r.Scope), ScopeID: r.ScopeID,
		}
	}
	return out, nil
}

// LoadBlacklist implements rules.Loader: the active entries, as hashes only.
func (l RulesLoader) LoadBlacklist(ctx context.Context) ([]rules.BlacklistEntry, error) {
	rows, err := db.New(l.Q).ListActiveBlacklistHashes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list blacklist: %w", err)
	}
	out := make([]rules.BlacklistEntry, len(rows))
	for i, r := range rows {
		out[i] = rules.BlacklistEntry{ID: r.ID, Scope: rules.BlacklistScope(r.Scope), GroupJID: r.GroupJid.String, PNHash: r.PnHash}
	}
	return out, nil
}

// InsertRule validates and stores a rule; NormPattern must already be set
// (rules.NormalizePattern, or the keyed hash for phone rules). It returns the rule with its
// ID. Patterns over the regex limits are rejected here, when they're saved (F-043).
func InsertRule(ctx context.Context, q db.DBTX, r rules.Rule, createdBy *int64) (rules.Rule, error) {
	if err := r.Validate(); err != nil {
		return rules.Rule{}, err
	}
	row, err := db.New(q).InsertRule(ctx, db.InsertRuleParams{
		Scope: string(r.Scope), ScopeID: r.ScopeID, Pack: r.Pack, Type: string(r.Type), Pattern: r.Pattern,
		NormPattern: r.NormPattern, ActionClass: string(r.ActionClass), Weight: r.Weight, Enabled: r.Enabled,
		CreatedBy: optInt8(createdBy),
	})
	if err != nil {
		return rules.Rule{}, fmt.Errorf("insert rule: %w", err)
	}
	r.ID = row.ID
	return r, nil
}

// SetRuleEnabled turns a rule on or off; it reports false for an unknown rule.
func SetRuleEnabled(ctx context.Context, q db.DBTX, id int64, enabled bool) (bool, error) {
	n, err := db.New(q).SetRuleEnabled(ctx, db.SetRuleEnabledParams{ID: id, Enabled: enabled})
	if err != nil {
		return false, fmt.Errorf("set rule %d enabled: %w", id, err)
	}
	return n > 0, nil
}

// DeleteRule deletes a rule; it reports false for an unknown rule.
func DeleteRule(ctx context.Context, q db.DBTX, id int64) (bool, error) {
	n, err := db.New(q).DeleteRule(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete rule %d: %w", id, err)
	}
	return n > 0, nil
}

// UpsertFingerprint normalizes and stores a fingerprint, or re-labels the existing one for the same kind,
// value and scope (an overturn turns spam into allow, §7.5).
func UpsertFingerprint(ctx context.Context, q db.DBTX, f rules.Fingerprint, now time.Time) error {
	f.Normalize()
	if err := f.Validate(); err != nil {
		return err
	}
	err := db.New(q).UpsertFingerprint(ctx, db.UpsertFingerprintParams{
		Kind: string(f.Kind), Value: f.Value, Label: string(f.Label), Source: string(f.Source),
		Scope: string(f.Scope), ScopeID: f.ScopeID, Now: now,
	})
	if err != nil {
		return fmt.Errorf("upsert fingerprint %s: %w", f.Kind, err)
	}
	return nil
}

// DeleteFingerprint deletes a fingerprint; it reports false when there was none.
func DeleteFingerprint(ctx context.Context, q db.DBTX, f rules.Fingerprint) (bool, error) {
	n, err := db.New(q).DeleteFingerprint(ctx, db.DeleteFingerprintParams{
		Kind: string(f.Kind), Value: f.Value, Scope: string(f.Scope), ScopeID: f.ScopeID,
	})
	if err != nil {
		return false, fmt.Errorf("delete fingerprint %s: %w", f.Kind, err)
	}
	return n > 0, nil
}
