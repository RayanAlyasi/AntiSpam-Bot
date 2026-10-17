package ai

// Stub for F-103: declares only the exported names the scoring acceptance tests use.
// The builder replaces this file.

// Label is the AI's answer (DESIGN.md §7.4).
type Label string

// The three labels in the verdict schema.
const (
	LabelSpam    Label = "spam"
	LabelNotSpam Label = "not_spam"
	LabelUnsure  Label = "unsure"
)

// Verdict is the structured AI output {label, confidence, categories, reason}.
type Verdict struct {
	Label      Label
	Confidence float64
	Categories []string
	Reason     string
}
