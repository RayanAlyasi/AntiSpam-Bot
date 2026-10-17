// Package llm is the provider-neutral seam under the AI classifier (DESIGN.md §7.4): one raw
// completion call that sends a stable system prompt plus an untrusted data block and returns the
// model's text. Adapters (Anthropic, OpenAI-compatible) implement Completer; prompt rendering,
// strict verdict parsing, retries, caching and budgets live above it, so nothing outside the
// adapters sees a provider type.
package llm

import (
	"context"
	"errors"
	"time"
)

// Mode is how the model is asked for structured output.
type Mode int

const (
	// ModeNative asks the provider to constrain output to Request.Schema (JSON schema / tool output).
	ModeNative Mode = iota
	// ModeJSON asks for a JSON object only; the caller validates it strictly.
	ModeJSON
)

func (m Mode) String() string {
	switch m {
	case ModeNative:
		return "native"
	case ModeJSON:
		return "json"
	default:
		return "unknown"
	}
}

// Request is one classification call.
type Request struct {
	// System is the stable, versioned system prompt. Adapters mark it cacheable where the
	// provider supports prompt caching, so it must not contain per-message content.
	System string
	// Data is the fenced, untrusted data block: the message, few-shot examples and context.
	Data string
	// Schema is the JSON schema of the verdict. Used in ModeNative; in ModeJSON it is only
	// described to the model by the system prompt.
	Schema map[string]any
	// Mode selects native schema output or JSON mode.
	Mode Mode
	// MaxTokens caps the response length; 0 means the adapter's default.
	MaxTokens int
}

// Usage is the provider's token accounting for one call.
type Usage struct {
	InputTokens      int64 // uncached input tokens
	OutputTokens     int64
	CacheReadTokens  int64 // input tokens served from the provider's prompt cache
	CacheWriteTokens int64 // input tokens written to the provider's prompt cache
}

// Response is the model's raw answer.
type Response struct {
	Text    string        // the output text, expected to be a JSON verdict
	Model   string        // the model that answered, as reported by the provider
	Usage   Usage         // token usage, zero when the provider reports none
	Latency time.Duration // wall-clock time of the HTTP call
}

// Caps describes what an adapter supports.
type Caps struct {
	NativeSchema  bool // the provider can constrain output to a JSON schema
	PromptCaching bool // the system prompt is cached by the provider
	Vision        bool // images can be sent (T4); no adapter in v0.1 sends them
	MaxContext    int  // context window in tokens, 0 when unknown
}

// Completer is implemented by every provider adapter.
type Completer interface {
	Complete(ctx context.Context, req Request) (Response, error)
	Caps() Caps
}

// Errors returned (wrapped) by adapters. Every one of them means "no verdict": the caller treats
// the message as still uncertain and asks admins; nothing is deleted on failure (DESIGN.md §7.4).
var (
	ErrTimeout        = errors.New("ai: timeout")
	ErrRateLimited    = errors.New("ai: rate limited")
	ErrUnavailable    = errors.New("ai: provider unavailable")
	ErrAuth           = errors.New("ai: authentication failed")
	ErrInvalidRequest = errors.New("ai: request rejected")
	ErrRefused        = errors.New("ai: model refused")
	ErrTruncated      = errors.New("ai: output truncated")
)
