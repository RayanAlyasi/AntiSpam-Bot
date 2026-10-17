// Package anthropic is the Anthropic (Claude) adapter for the AI tier (DESIGN.md §7.4). It sends
// the stable system prompt as a cacheable block (prompt caching), the untrusted data block as the
// user turn, and asks for schema-constrained JSON output. Only this package knows Anthropic types.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Defaults used when Options leaves a field empty.
const (
	DefaultTimeout   = 5 * time.Second // hosted providers (DESIGN.md §7.4 guardrails)
	DefaultMaxTokens = 4096            // room for adaptive thinking plus a short JSON verdict
)

// Options configures the adapter. The API key comes from configuration (the variable named by
// ai.api_key_env); the adapter never reads ANTHROPIC_* variables itself.
type Options struct {
	BaseURL    string        // empty for the public API
	APIKey     string        // required
	Model      string        // required, e.g. "claude-haiku-5-5"
	Timeout    time.Duration // per call; 0 means DefaultTimeout
	MaxTokens  int           // default for requests that set none; 0 means DefaultMaxTokens
	Effort     string        // optional output_config.effort (low…max); empty sends none
	HTTPClient *http.Client  // optional, for tests and proxies
	Clock      clock.Clock   // measures latency; nil means clock.Real
}

// Client implements llm.Completer.
type Client struct {
	sdk     sdk.Client
	model   string
	timeout time.Duration
	maxTok  int
	effort  sdk.OutputConfigEffort
	clock   clock.Clock
}

var _ llm.Completer = (*Client)(nil)

// New validates the options and builds a client.
func New(o Options) (*Client, error) {
	if strings.TrimSpace(o.APIKey) == "" {
		return nil, errors.New("anthropic: api key is required")
	}
	if strings.TrimSpace(o.Model) == "" {
		return nil, errors.New("anthropic: model is required")
	}
	if o.Timeout < 0 || o.MaxTokens < 0 {
		return nil, errors.New("anthropic: timeout and max tokens must not be negative")
	}
	switch o.Effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return nil, fmt.Errorf("anthropic: effort %q: want low, medium, high, xhigh or max", o.Effort)
	}
	c := &Client{model: o.Model, timeout: o.Timeout, maxTok: o.MaxTokens, effort: sdk.OutputConfigEffort(o.Effort), clock: o.Clock}
	if c.clock == nil {
		c.clock = clock.Real{}
	}
	if c.timeout == 0 {
		c.timeout = DefaultTimeout
	}
	if c.maxTok == 0 {
		c.maxTok = DefaultMaxTokens
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(), // the key comes from our config only
		option.WithAPIKey(o.APIKey),
		option.WithMaxRetries(0), // the validation layer owns retries; a retry here would blow the deadline
		option.WithoutOpenTelemetry(),
	}
	if o.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(o.BaseURL))
	}
	if o.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(o.HTTPClient))
	}
	c.sdk = sdk.NewClient(opts...)
	return c, nil
}

// Caps reports native schema output and prompt caching.
func (c *Client) Caps() llm.Caps {
	return llm.Caps{NativeSchema: true, PromptCaching: true}
}

// Complete sends one classification request.
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if req.Mode == llm.ModeNative && len(req.Schema) == 0 {
		return llm.Response{}, fmt.Errorf("anthropic: native mode needs a schema: %w", llm.ErrInvalidRequest)
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = c.maxTok
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(c.model),
		MaxTokens: int64(maxTok),
		// The system prompt is stable and versioned, so it is the cache prefix; the per-message
		// data block comes after the breakpoint.
		System: []sdk.TextBlockParam{{
			Text:         req.System,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}},
		Messages: []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(req.Data))},
	}
	if req.Mode == llm.ModeNative {
		params.OutputConfig.Format = sdk.JSONOutputFormatParam{Schema: req.Schema}
	}
	if c.effort != "" {
		params.OutputConfig.Effort = c.effort
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	start := c.clock.Now()
	msg, err := c.sdk.Messages.New(ctx, params)
	latency := c.clock.Now().Sub(start)
	if err != nil {
		return llm.Response{Latency: latency}, mapError(err)
	}

	resp := llm.Response{
		Model:   string(msg.Model),
		Latency: latency,
		Usage: llm.Usage{
			InputTokens:      msg.Usage.InputTokens,
			OutputTokens:     msg.Usage.OutputTokens,
			CacheReadTokens:  msg.Usage.CacheReadInputTokens,
			CacheWriteTokens: msg.Usage.CacheCreationInputTokens,
		},
	}
	switch msg.StopReason {
	case sdk.StopReasonRefusal:
		return resp, fmt.Errorf("anthropic: stop reason %s: %w", msg.StopReason, llm.ErrRefused)
	case sdk.StopReasonMaxTokens:
		return resp, fmt.Errorf("anthropic: stop reason %s: %w", msg.StopReason, llm.ErrTruncated)
	default: // end_turn and the rest: read the text; the validation layer judges it
	}
	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	resp.Text = text.String()
	return resp, nil
}

// mapError turns an SDK error into a sentinel, keeping the cause for logs.
func mapError(err error) error {
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("anthropic: status %d: %w", apiErr.StatusCode, llm.StatusError(apiErr.StatusCode))
	}
	return fmt.Errorf("anthropic: %w", llm.TransportError(err))
}
