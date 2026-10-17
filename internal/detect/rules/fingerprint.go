package rules

import (
	"fmt"
	"strings"
)

// FingerprintKind is the kind of value in the fingerprints table (§14).
type FingerprintKind string

// Fingerprint kinds. Values use the same normal forms as the matching rule types: lower-case
// hex for hashes, the keyed hash for numbers, the JID or code for channels and invites, and
// the scheme-less canonical URL for urls.
const (
	FPSHA256  FingerprintKind = "sha256"
	FPPHash   FingerprintKind = "phash"
	FPSimHash FingerprintKind = "simhash"
	FPChannel FingerprintKind = "channel"
	FPInvite  FingerprintKind = "invite"
	FPNumber  FingerprintKind = "number"
	FPURL     FingerprintKind = "url"
)

// Label says whether a fingerprint is known spam or allow-listed (an overturned pattern that
// must not be matched again, §7.5).
type Label string

// Labels.
const (
	LabelSpam  Label = "spam"
	LabelAllow Label = "allow"
)

// Source records where a fingerprint came from.
type Source string

// Sources.
const (
	SourceAdmin Source = "admin"
	SourceAuto  Source = "auto"
	SourceFeed  Source = "feed"
)

// Fingerprint is one row of the fingerprints table.
type Fingerprint struct {
	Kind    FingerprintKind
	Value   string
	Label   Label
	Source  Source
	Scope   Scope
	ScopeID string
}

// Normalize puts the value in the form matching compares: lower-case hex for hashes and
// keyed number hashes (call it before saving). Channel JIDs, invite codes and URLs are kept as
// they are, since they're case-sensitive.
func (f *Fingerprint) Normalize() {
	switch f.Kind {
	case FPSHA256, FPPHash, FPSimHash, FPNumber:
		f.Value = strings.ToLower(strings.TrimSpace(f.Value))
	case FPChannel, FPInvite, FPURL:
		f.Value = strings.TrimSpace(f.Value)
	}
}

// Validate checks a fingerprint before it is saved: hash kinds must be lower-case hex of the
// right length (sha256 and number 64 digits, phash and simhash 16).
func (f *Fingerprint) Validate() error {
	switch f.Kind {
	case FPSHA256, FPPHash, FPSimHash, FPChannel, FPInvite, FPNumber, FPURL:
	default:
		return fmt.Errorf("%w: fingerprint kind %q", ErrInvalidType, f.Kind)
	}
	if f.Value == "" {
		return fmt.Errorf("%w: empty fingerprint value", ErrInvalidPattern)
	}
	if n, ok := hexLen[f.Kind]; ok && (len(f.Value) != n || !isHex(f.Value)) {
		return fmt.Errorf("%w: %s fingerprint must be %d lower-case hex digits", ErrInvalidPattern, f.Kind, n)
	}
	if f.Label != LabelSpam && f.Label != LabelAllow {
		return fmt.Errorf("%w: fingerprint label %q", ErrInvalidAction, f.Label)
	}
	switch f.Source {
	case SourceAdmin, SourceAuto, SourceFeed:
	default:
		return fmt.Errorf("%w: fingerprint source %q", ErrInvalidAction, f.Source)
	}
	if (f.Scope == ScopeGlobal) != (f.ScopeID == "") || (f.Scope != ScopeGlobal && f.Scope != ScopeBot && f.Scope != ScopeGroup) {
		return fmt.Errorf("%w: %q/%q", ErrInvalidScope, f.Scope, f.ScopeID)
	}
	return nil
}

var hexLen = map[FingerprintKind]int{FPSHA256: 64, FPNumber: 64, FPPHash: 16, FPSimHash: 16}

// BlacklistScope is the scope of a manual blacklist entry (§9.8).
type BlacklistScope string

// Blacklist scopes.
const (
	BlacklistGroup    BlacklistScope = "group"
	BlacklistInstance BlacklistScope = "instance"
)

// BlacklistEntry is the in-memory form of an active number_blacklist row: only what content
// matching needs. The number itself stays in the table; matching uses its keyed hash.
type BlacklistEntry struct {
	ID       int64
	Scope    BlacklistScope
	GroupJID string // empty for instance entries
	PNHash   string // lower-case hex keyed hash of the E.164 number
}
