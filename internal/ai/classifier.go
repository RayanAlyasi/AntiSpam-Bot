package ai

import "context"

// AIClassifier classifies one message (DESIGN.md §7.4). An error, a timeout or invalid output
// means "still uncertain": the message goes to admins and nothing is deleted. The name is the
// one DESIGN.md uses throughout.
type AIClassifier interface {
	Classify(ctx context.Context, req ClassifyRequest) (Verdict, error)
	Capabilities() Caps
}

// Caps is what a provider and model can do.
type Caps struct {
	NativeSchema     bool // JSON-schema or tool output is enforced by the provider
	Vision           bool // accepts images (T4)
	PromptCaching    bool // the stable system prompt can be cached
	MaxContextTokens int  // 0 when unknown
}

// ClassifyRequest is everything the AI sees. Every string in it is untrusted data: the renderer
// JSON-encodes it into a delimited data block (RenderData), never into the instructions.
// Whoever builds a request redacts it first (phone numbers, emails, other people's names).
type ClassifyRequest struct {
	Message  Message
	Examples []string // related confirmed-spam examples (few-shot, spam only, redacted)
	Context  *Context // nil when the group's ai.context is off
}

// Message is the message being judged.
type Message struct {
	Kind     string   `json:"kind"`              // envelope kind: "text", "image", "contact", ...
	Texts    []Text   `json:"texts"`             // normalized, redacted text surfaces
	URLs     []string `json:"urls,omitempty"`    // canonical URLs
	Signals  []string `json:"signals,omitempty"` // the rules signals that made it suspicious, with weights
	IsEdit   bool     `json:"is_edit,omitempty"`
	Original string   `json:"original,omitempty"` // the text before the edit, when IsEdit
	Images   []Image  `json:"-"`                  // for vision models only; never rendered as text
}

// Text is one text surface ("body", "caption", "link_title", ...).
type Text struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}

// Image is an image for a vision model (T4).
type Image struct {
	Mimetype string
	Data     []byte
}

// Context is the context bundle (DESIGN.md §7.4): built from the short-lived inbox, minimal,
// redacted and length-capped by its builder.
type Context struct {
	Group    Group            `json:"group"`
	Sender   Sender           `json:"sender"`
	ReplyTo  *ContextMessage  `json:"reply_to,omitempty"`
	Previous []ContextMessage `json:"previous,omitempty"` // oldest first
	Replies  []ContextMessage `json:"replies,omitempty"`  // replies the message already got
}

// Group describes the group the message was posted in.
type Group struct {
	Name    string `json:"name,omitempty"`
	Topic   string `json:"topic,omitempty"`
	Allowed string `json:"allowed,omitempty"` // allowed-content summary from the group's settings
}

// Sender describes the sender without identifying them.
type Sender struct {
	Tenure         string `json:"tenure,omitempty"` // new | weeks | months | years
	Role           string `json:"role,omitempty"`   // member | admin
	RecentMessages int    `json:"recent_messages"`
	JustJoined     bool   `json:"just_joined"`
}

// ContextMessage is one message around the judged one, truncated by the bundle builder.
type ContextMessage struct {
	Text     string `json:"text"`
	Flagged  bool   `json:"flagged,omitempty"`   // this message was itself flagged
	IsStatus bool   `json:"is_status,omitempty"` // a quoted status
}
