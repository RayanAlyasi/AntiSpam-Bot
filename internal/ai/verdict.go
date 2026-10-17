package ai

// Label is the AI's answer (DESIGN.md §7.4).
type Label string

// The three labels in the verdict schema.
const (
	LabelSpam    Label = "spam"
	LabelNotSpam Label = "not_spam"
	LabelUnsure  Label = "unsure" // also what invalid output, timeouts and errors amount to
)

// Verdict is the structured AI output {label, confidence, categories, reason}. The confidence
// is a signal, not an action: the decision engine decides what happens (§7.3, §9.2).
type Verdict struct {
	Label      Label    `json:"label"`
	Confidence float64  `json:"confidence"` // 0..1
	Categories []string `json:"categories"`
	Reason     string   `json:"reason"` // short, for admins
}

// Unsure returns the verdict that stands in for an answer the model couldn't give.
func Unsure(reason string) Verdict {
	return Verdict{Label: LabelUnsure, Categories: []string{}, Reason: reason}
}
