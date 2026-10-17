package blacklist

import (
	"errors"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/links"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/redact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// MinKeyLen is the shortest accepted number-hash key. The E.164 space is small, so a short
// key would let anyone holding the hashes recover the numbers by brute force.
const MinKeyLen = 16

// ErrShortKey is returned by NewHasher for a key under MinKeyLen bytes.
var ErrShortKey = errors.New("number hash key is too short")

// Hasher computes the keyed hash that number_blacklist.pn_hash, number rules and number
// fingerprints store (§7.5, §9.8): lower-case hex HMAC-SHA256 of the E.164 string, the same
// as redact.PhoneHash, so a number in content can be matched without the tables holding it
// in the clear.
type Hasher struct{ key []byte }

// NewHasher returns a hasher for the deployment's key.
func NewHasher(key []byte) (Hasher, error) {
	if len(key) < MinKeyLen {
		return Hasher{}, ErrShortKey
	}
	return Hasher{key: append([]byte(nil), key...)}, nil
}

// Hash returns the keyed hash of an E.164 number.
func (h Hasher) Hash(e164 string) string { return redact.PhoneHash(h.key, e164) }

// Mask hides the middle of a number for evidence and group-admin views (§9.8): the plus,
// the first three digits and the last two stay ("+966•••••••67").
func Mask(e164 string) string {
	if len(e164) <= 6 {
		return strings.Repeat("•", len(e164))
	}
	return e164[:4] + strings.Repeat("•", len(e164)-6) + e164[len(e164)-2:]
}

// SourceWaMe is the Number source of numbers from wa.me and api.whatsapp.com links.
const SourceWaMe = "wa.me link"

// Numbers returns every number the message advertises, each once: e.Phones (text and contact
// cards, filled by textnorm.Prepare), then the numbers of wa.me links.
func Numbers(e *envelope.Envelope) []envelope.PhoneRef {
	out := append([]envelope.PhoneRef(nil), e.Phones...)
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.E164] = true
	}
	for _, l := range links.Collect(e) {
		if l.Target != nil && l.Target.Kind == urls.TargetPhone && !seen[l.Target.ID] {
			seen[l.Target.ID] = true
			out = append(out, envelope.PhoneRef{E164: l.Target.ID, Source: SourceWaMe})
		}
	}
	return out
}
