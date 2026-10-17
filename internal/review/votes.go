package review

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// codeAlphabet has no look-alikes (0/O, 1/I/L), so codes survive being read aloud or retyped.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// newCode returns a random short code of n characters from codeAlphabet, without modulo bias.
func newCode(r io.Reader, n int) (string, error) {
	if n <= 0 {
		return "", errors.New("review: code length must be positive")
	}
	if r == nil {
		r = rand.Reader
	}
	const limit = 256 - 256%len(codeAlphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := io.ReadFull(r, buf); err != nil {
			return "", fmt.Errorf("review: read random code: %w", err)
		}
		for _, b := range buf {
			if int(b) < limit && len(out) < n {
				out = append(out, codeAlphabet[int(b)%len(codeAlphabet)])
			}
		}
	}
	return string(out), nil
}

// normalizeCode upper-cases a typed code and drops a leading "#", so "#k7q2" and "K7Q2" find
// the same review.
func normalizeCode(s string) string {
	return strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(s), "#"))
}

// replyKeywords maps reply keywords (normalized: lower case, NFKC, single spaces) to decisions
// (§9.5 "Replies"). "kick" and "طرد" mean ban: a review decides the ban, not a lighter kick.
var replyKeywords = map[string]Decision{
	"ban": DecisionBan, "kick": DecisionBan, "spam": DecisionBan,
	"حظر": DecisionBan, "طرد": DecisionBan, "سبام": DecisionBan,
	"ok": DecisionNotSpam, "okay": DecisionNotSpam, "not spam": DecisionNotSpam, "notspam": DecisionNotSpam,
	"ليس سبام": DecisionNotSpam, "مو سبام": DecisionNotSpam,
	"delete only": DecisionDeleteOnly, "delete": DecisionDeleteOnly, "حذف فقط": DecisionDeleteOnly, "حذف": DecisionDeleteOnly,
	"clear": DecisionClear, "مسح": DecisionClear,
}

// muteKeywords only count when the group enabled the optional ladder (§9.3).
var muteKeywords = map[string]bool{"mute": true, "كتم": true}

// parseReply maps a reply's text to a decision. Leading prefix characters, punctuation and a
// trailing duration ("mute 2h") are ignored.
func parseReply(text string, ladder bool) (Decision, bool) {
	t := strings.ToLower(norm.NFKC.String(text))
	t = strings.TrimLeft(strings.TrimSpace(t), ".!/")
	t = strings.TrimRightFunc(t, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSpace(r) })
	t = strings.Join(strings.Fields(t), " ")
	if d, ok := replyKeywords[t]; ok {
		return d, true
	}
	first, _, _ := strings.Cut(t, " ")
	if muteKeywords[first] && ladder {
		return DecisionMute, true
	}
	return "", false
}

// parseReaction maps an emoji to a decision with the group's mapping. Skin-tone modifiers and
// the variation selector are ignored, so 👍🏽 counts as 👍.
func parseReaction(emoji string, cfg Config) (Decision, bool) {
	e := baseEmoji(emoji)
	if e == "" {
		return "", false
	}
	m := cfg.Reactions
	if len(m) == 0 {
		m = DefaultReactions
	}
	for _, d := range []Decision{DecisionBan, DecisionNotSpam, DecisionDeleteOnly, DecisionClear, DecisionMute} {
		for _, r := range m[d] {
			if baseEmoji(r) == e {
				if d == DecisionMute && !cfg.Ladder {
					return "", false
				}
				return d, true
			}
		}
	}
	return "", false
}

// baseEmoji strips variation selectors and skin-tone modifiers.
func baseEmoji(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '️' || r == '︎' || (r >= 0x1F3FB && r <= 0x1F3FF) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}
