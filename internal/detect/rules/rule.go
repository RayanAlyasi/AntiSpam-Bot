// Package rules is the in-memory side of the rules, fingerprints and number_blacklist tables
// (DESIGN.md §12.3, §14): the entry types, their validation and pattern normalization, an
// immutable compiled Snapshot that answers "does this text, domain, hash or number match a
// rule in this group's scope", and a Holder that swaps snapshots when the tables change.
//
// Storage lives elsewhere (internal/store); this package receives entries through the Loader
// interface and never imports pgx.
package rules

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"golang.org/x/net/idna"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textmatch"
)

// Scope says whom an entry applies to (§12.3): every group, the groups of one bot number, or
// one group.
type Scope string

// Scopes.
const (
	ScopeGlobal Scope = "global"
	ScopeBot    Scope = "bot"
	ScopeGroup  Scope = "group"
)

// Type is the kind of pattern a rule holds (§12.3).
type Type string

// Rule types. Keyword matches whole words, phrase matches a word sequence and is also
// fuzzy-matched, regex is an RE2 pattern; the rest are exact values.
const (
	TypeKeyword   Type = "keyword"
	TypePhrase    Type = "phrase"
	TypeRegex     Type = "regex"
	TypeDomain    Type = "domain"
	TypeURLPrefix Type = "url_prefix"
	TypePhone     Type = "phone_number" // pattern: E.164; norm_pattern: keyed hash
	TypeChannel   Type = "channel"      // newsletter JID
	TypeInvite    Type = "invite"       // group invite code
	TypeSHA256    Type = "sha256"       // media or sticker SHA256, lowercase hex
	TypePHash     Type = "phash"        // thumbnail perceptual hash, 16 hex digits
	TypeSimHash   Type = "simhash"      // text SimHash, 16 hex digits
)

// ActionClass is what a match means (§12.3).
type ActionClass string

// Action classes. Hard rules give CERTAIN evidence for exact targets (domains, numbers,
// channels, invites, hashes: confidence source known, §7.3) and a policy violation for text
// patterns (keywords and regexes are not on §7.3's list of known evidence). Weighted rules
// add their weight to the score. Allow rules exempt a domain, URL prefix or value from deny
// rules in the same or a wider scope.
const (
	ActionHard     ActionClass = "hard"
	ActionWeighted ActionClass = "weighted"
	ActionAllow    ActionClass = "allow"
)

// Rule is one row of the rules table.
type Rule struct {
	ID          int64
	Scope       Scope
	ScopeID     string // bot or group JID; empty for global
	Pack        string // curated pack name for global rules ("crypto"); empty = always on
	Type        Type
	Pattern     string // as entered, shown to admins
	NormPattern string // normalized form used for matching (NormalizePattern)
	ActionClass ActionClass
	Weight      float64
	Enabled     bool
}

// Errors from Validate and NormalizePattern.
var (
	ErrInvalidScope   = errors.New("invalid rule scope")
	ErrInvalidType    = errors.New("invalid rule type")
	ErrInvalidAction  = errors.New("invalid rule action class")
	ErrInvalidWeight  = errors.New("invalid rule weight")
	ErrInvalidPattern = errors.New("invalid rule pattern")
)

// Validate checks a rule before it is saved. NormPattern must already be set (by
// NormalizePattern); Validate re-checks it so a stored row can't bypass the limits.
func (r *Rule) Validate() error {
	switch r.Scope {
	case ScopeGlobal:
		if r.ScopeID != "" {
			return fmt.Errorf("%w: global rule with scope id %q", ErrInvalidScope, r.ScopeID)
		}
	case ScopeBot, ScopeGroup:
		if r.ScopeID == "" {
			return fmt.Errorf("%w: %s rule without scope id", ErrInvalidScope, r.Scope)
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidScope, r.Scope)
	}
	switch r.ActionClass {
	case ActionHard, ActionWeighted:
	case ActionAllow:
		switch r.Type {
		case TypeDomain, TypeURLPrefix, TypePhone, TypeChannel, TypeInvite, TypeSHA256:
		default:
			return fmt.Errorf("%w: %s rules can't be allow rules", ErrInvalidAction, r.Type)
		}
	default:
		return fmt.Errorf("%w: %q", ErrInvalidAction, r.ActionClass)
	}
	if math.IsNaN(r.Weight) || math.IsInf(r.Weight, 0) || math.Abs(r.Weight) > MaxWeight {
		return fmt.Errorf("%w: %v (limit ±%v)", ErrInvalidWeight, r.Weight, MaxWeight)
	}
	if r.Type == TypePhone {
		// The pattern is the E.164 number; its keyed hash is computed by the caller.
		if r.NormPattern == "" || !isHex(r.NormPattern) {
			return fmt.Errorf("%w: phone rule needs its keyed hash", ErrInvalidPattern)
		}
		return nil
	}
	want, err := NormalizePattern(r.Type, r.Pattern, nil)
	if err != nil {
		return err
	}
	if r.Type == TypeKeyword || r.Type == TypePhrase {
		// The text normalizer that produced NormPattern is the caller's; it must be in
		// Words form and keep a keyword to one word.
		if r.NormPattern == "" || textmatch.Words(r.NormPattern) != r.NormPattern ||
			(r.Type == TypeKeyword && strings.Contains(r.NormPattern, " ")) {
			return fmt.Errorf("%w: norm pattern %q", ErrInvalidPattern, r.NormPattern)
		}
		return nil
	}
	if r.NormPattern != want {
		return fmt.Errorf("%w: norm pattern %q, want %q", ErrInvalidPattern, r.NormPattern, want)
	}
	return nil
}

// MaxWeight bounds a rule's log-odds weight.
const MaxWeight = 20

// DefaultKnownWeight is the weight of known-spam evidence from a hard rule or a spam
// fingerprint that has no positive weight of its own. Known evidence is CERTAIN whatever its
// weight (§7.0); the weight only orders the explanation.
const DefaultKnownWeight = 5.0

// KnownWeight is the signal weight for known evidence from r (nil for a fingerprint).
func KnownWeight(r *Rule) float64 {
	if r != nil && r.Weight > 0 {
		return r.Weight
	}
	return DefaultKnownWeight
}

// NormalizePattern returns the matching form of a pattern. Keyword and phrase patterns go
// through norm (the text normalizer, normally normalize.Text's Norm; nil keeps the text)
// and then textmatch.Words; regexes are validated against the RE2 limits; domains become
// lower-case ASCII (punycode) without a scheme, path or trailing dot; hashes become
// lower-case hex. Phone rules are hashed by the caller.
func NormalizePattern(t Type, pattern string, norm func(string) string) (string, error) {
	p := strings.TrimSpace(pattern)
	switch t {
	case TypeKeyword, TypePhrase:
		if norm != nil {
			p = norm(p)
		}
		w := textmatch.Words(p)
		if w == "" {
			return "", fmt.Errorf("%w: %q has no letters or digits", ErrInvalidPattern, pattern)
		}
		if t == TypeKeyword && strings.Contains(w, " ") {
			return "", fmt.Errorf("%w: keyword %q has several words; use a phrase rule", ErrInvalidPattern, pattern)
		}
		return w, nil
	case TypeRegex:
		if _, err := textmatch.CompileRegex(pattern); err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalidPattern, err)
		}
		return pattern, nil
	case TypeDomain:
		return NormalizeDomain(p)
	case TypeURLPrefix:
		return normalizeURLPrefix(p)
	case TypeChannel, TypeInvite:
		if p == "" || strings.ContainsAny(p, " \t\n/") {
			return "", fmt.Errorf("%w: %q", ErrInvalidPattern, pattern)
		}
		return p, nil // JIDs and invite codes are case-sensitive
	case TypeSHA256:
		return normalizeHex(p, 64)
	case TypePHash, TypeSimHash:
		return normalizeHex(p, 16)
	case TypePhone:
		return "", fmt.Errorf("%w: phone rules are hashed by the caller", ErrInvalidPattern)
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidType, t)
	}
}

// NormalizeDomain turns "HTTPS://Www.Example.COM./path" or "пример.рф" into the host form
// domain rules match on: lower-case ASCII, no scheme, port, path or trailing dot. A leading
// "*." is dropped, since a domain rule always covers its subdomains.
func NormalizeDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(d, "://"); i >= 0 {
		d = d[i+3:]
	}
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	if i := strings.LastIndexByte(d, ':'); i >= 0 {
		d = d[:i]
	}
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimSuffix(d, ".")
	if d == "" {
		return "", fmt.Errorf("%w: empty domain %q", ErrInvalidPattern, s)
	}
	ascii, err := idna.Lookup.ToASCII(d)
	if err != nil || !strings.Contains(ascii, ".") {
		return "", fmt.Errorf("%w: domain %q", ErrInvalidPattern, s)
	}
	return ascii, nil
}

func normalizeURLPrefix(s string) (string, error) {
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, "http://") && !strings.HasPrefix(low, "https://") {
		s = "https://" + s
	}
	i := strings.Index(s, "://") + 3
	rest := s[i:]
	host, path := rest, ""
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		host, path = rest[:j], rest[j:]
	}
	h, err := NormalizeDomain(host)
	if err != nil {
		return "", err
	}
	if path == "" {
		path = "/"
	}
	return h + path, nil // scheme-less: http and https share a prefix
}

func normalizeHex(s string, n int) (string, error) {
	h := strings.ToLower(s)
	if len(h) != n || !isHex(h) {
		return "", fmt.Errorf("%w: want %d hex digits, got %q", ErrInvalidPattern, n, s)
	}
	return h, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
}
