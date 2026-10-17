package review

import (
	"io"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Render builds the review message for an item (§9.5 "The review message"): text, or the inline
// thumbnail with the text as its caption. Third-party phone numbers and emails are masked, the
// subject's number is masked except its last digits, and the message text is truncated to about
// MaxEvidenceText characters.
func Render(it Item, cfg Config, texts Localizer) wa.OutgoingMessage {
	if texts == nil {
		texts = Texts
	}
	return render(it, cfg, texts)
}

// Redact masks phone numbers and email addresses in text.
func Redact(text string) string { return redact(text) }

// MaskNumber masks a phone number except its last two digits: "+966 5•• ••• •12".
func MaskNumber(number string) string { return maskNumber(number) }

// NewCode returns a random short code of n characters from an alphabet without look-alikes
// (no 0/O, 1/I/L), such as K7Q2.
func NewCode(r io.Reader, n int) (string, error) { return newCode(r, n) }

// ParseReaction maps a reaction emoji to a decision using the group's reaction mapping (or
// DefaultReactions). ok is false for an emoji that means nothing.
func ParseReaction(emoji string, cfg Config) (d Decision, ok bool) { return parseReaction(emoji, cfg) }

// ParseReply maps a reply's text to a decision: ban/kick/طرد/حظر, ok/not spam/ليس سبام,
// delete only, clear, and mute/كتم only when the ladder is on.
func ParseReply(text string, ladder bool) (d Decision, ok bool) { return parseReply(text, ladder) }

// DefaultReactions is the default reaction mapping: ✅ 👍 ban, ❌ 👎 not spam.
var DefaultReactions = map[Decision][]string{
	DecisionBan:     {"✅", "👍"},
	DecisionNotSpam: {"❌", "👎"},
}
