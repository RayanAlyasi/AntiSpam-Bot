// Package openaicompat is the adapter for any OpenAI-compatible chat completions API (DESIGN.md
// §7.4): hosted services and local servers such as Ollama, llama.cpp server, vLLM and LM Studio,
// selected by base URL. It uses native JSON-schema output where the server supports it and JSON
// mode otherwise. Only this package knows the wire format.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// Defaults used when Options leaves a field empty.
const (
	DefaultTimeout   = 5 * time.Second // hosted default; local deployers raise ai.timeout
	DefaultMaxTokens = 1024
	maxResponseBytes = 1 << 20
)

// StructuredOutput values (config ai.structured_output).
const (
	StructuredAuto   = "auto"   // try native schema, fall back to JSON mode if the server rejects it
	StructuredNative = "native" // always native schema
	StructuredJSON   = "json"   // always JSON mode, never a schema
)

// Options configures the adapter.
type Options struct {
	BaseURL          string        // required, e.g. https://api.openai.com/v1 or http://ollama:11434/v1
	APIKey           string        // optional: most local servers need none
	Model            string        // required
	Timeout          time.Duration // per call; 0 means DefaultTimeout
	MaxTokens        int           // default for requests that set none; 0 means DefaultMaxTokens
	StructuredOutput string        // auto (default), native or json
	HTTPClient       *http.Client  // optional, for tests and proxies
	Clock            clock.Clock   // measures latency; nil means clock.Real
}

// Client implements llm.Completer.
type Client struct {
	endpoint string
	apiKey   string
	model    string
	timeout  time.Duration
	maxTok   int
	mode     string
	http     *http.Client
	clock    clock.Clock
	// jsonOnly is set once the server has rejected a json_schema request in auto mode; every
	// later call in the process then uses JSON mode.
	jsonOnly atomic.Bool
}

var _ llm.Completer = (*Client)(nil)

// New validates the options and builds a client.
func New(o Options) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(o.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("openaicompat: base url %q: want http(s)://host[:port][/path]", o.BaseURL)
	}
	if strings.TrimSpace(o.Model) == "" {
		return nil, errors.New("openaicompat: model is required")
	}
	if o.Timeout < 0 || o.MaxTokens < 0 {
		return nil, errors.New("openaicompat: timeout and max tokens must not be negative")
	}
	mode := o.StructuredOutput
	switch mode {
	case "":
		mode = StructuredAuto
	case StructuredAuto, StructuredNative, StructuredJSON:
	default:
		return nil, fmt.Errorf("openaicompat: structured output %q: want auto, native or json", o.StructuredOutput)
	}
	c := &Client{
		endpoint: strings.TrimRight(u.String(), "/") + "/chat/completions",
		apiKey:   strings.TrimSpace(o.APIKey),
		model:    o.Model,
		timeout:  o.Timeout,
		maxTok:   o.MaxTokens,
		mode:     mode,
		http:     o.HTTPClient,
		clock:    o.Clock,
	}
	if c.clock == nil {
		c.clock = clock.Real{}
	}
	if c.timeout == 0 {
		c.timeout = DefaultTimeout
	}
	if c.maxTok == 0 {
		c.maxTok = DefaultMaxTokens
	}
	if c.http == nil {
		c.http = &http.Client{}
	}
	return c, nil
}

// Caps reports native schema support as currently known: false in json mode, or after an auto
// fallback.
func (c *Client) Caps() llm.Caps {
	return llm.Caps{NativeSchema: c.nativeAllowed()}
}

func (c *Client) nativeAllowed() bool {
	return c.mode != StructuredJSON && (c.mode != StructuredAuto || !c.jsonOnly.Load())
}

// Complete sends one classification request. In auto mode a json_schema request rejected with
// 400/422 is sent once more in JSON mode.
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	native := req.Mode == llm.ModeNative && c.nativeAllowed()
	if native && len(req.Schema) == 0 {
		return llm.Response{}, fmt.Errorf("openaicompat: native mode needs a schema: %w", llm.ErrInvalidRequest)
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	resp, schemaRejected, err := c.send(ctx, req, native)
	if native && c.mode == StructuredAuto && schemaRejected {
		c.jsonOnly.Store(true)
		first := resp.Latency
		resp, _, err = c.send(ctx, req, false)
		resp.Latency += first
	}
	return resp, err
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type jsonSchemaFormat struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type responseFormat struct {
	Type       string            `json:"type"`
	JSONSchema *jsonSchemaFormat `json:"json_schema,omitempty"`
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	MaxTokens      int            `json:"max_tokens"`
	ResponseFormat responseFormat `json:"response_format"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content *string `json:"content"`
			Refusal *string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// send makes one HTTP call. schemaRejected reports a status that triggers the auto fallback.
func (c *Client) send(ctx context.Context, req llm.Request, native bool) (llm.Response, bool, error) {
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = c.maxTok
	}
	body := chatRequest{
		Model:     c.model,
		Messages:  []chatMessage{{Role: "system", Content: req.System}, {Role: "user", Content: req.Data}},
		MaxTokens: maxTok,
	}
	if native {
		body.ResponseFormat = responseFormat{Type: "json_schema", JSONSchema: &jsonSchemaFormat{Name: "verdict", Strict: true, Schema: llm.PortableSchema(req.Schema)}}
	} else {
		body.ResponseFormat = responseFormat{Type: "json_object"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return llm.Response{}, false, fmt.Errorf("openaicompat: encode request: %w: %w", llm.ErrInvalidRequest, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return llm.Response{}, false, fmt.Errorf("openaicompat: build request: %w: %w", llm.ErrInvalidRequest, err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := c.clock.Now()
	hresp, err := c.http.Do(hreq)
	if err != nil {
		return llm.Response{Latency: c.clock.Now().Sub(start)}, false, fmt.Errorf("openaicompat: %w", llm.TransportError(err))
	}
	defer func() { _ = hresp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(hresp.Body, maxResponseBytes))
	resp := llm.Response{Model: c.model, Latency: c.clock.Now().Sub(start)}
	if err != nil {
		return resp, false, fmt.Errorf("openaicompat: read response: %w", llm.TransportError(err))
	}
	if hresp.StatusCode < 200 || hresp.StatusCode > 299 {
		return resp, schemaRejection(hresp.StatusCode, data), fmt.Errorf("openaicompat: status %d: %w", hresp.StatusCode, llm.StatusError(hresp.StatusCode))
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return resp, false, fmt.Errorf("openaicompat: decode response: %w: %w", llm.ErrUnavailable, err)
	}
	if cr.Model != "" {
		resp.Model = cr.Model
	}
	if u := cr.Usage; u != nil {
		resp.Usage = llm.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
		if d := u.PromptTokensDetails; d != nil {
			// OpenAI counts cached tokens inside prompt_tokens; report them separately.
			resp.Usage.CacheReadTokens = d.CachedTokens
			resp.Usage.InputTokens = max(0, u.PromptTokens-d.CachedTokens)
		}
	}
	if len(cr.Choices) == 0 {
		return resp, false, fmt.Errorf("openaicompat: no choices: %w", llm.ErrUnavailable)
	}
	ch := cr.Choices[0]
	switch {
	case ch.Message.Refusal != nil && *ch.Message.Refusal != "", ch.FinishReason == "content_filter":
		return resp, false, fmt.Errorf("openaicompat: refused: %w", llm.ErrRefused)
	case ch.FinishReason == "length":
		return resp, false, fmt.Errorf("openaicompat: finish reason length: %w", llm.ErrTruncated)
	}
	if ch.Message.Content != nil {
		resp.Text = *ch.Message.Content
	}
	return resp, false, nil
}

// schemaRejection reports how a server without schema support refuses a json_schema request:
// 400 or 422. The contract falls back on either status; another 400 cause (e.g. context length)
// also switches to JSON mode, which only costs the provider-side schema check.
func schemaRejection(status int, _ []byte) bool {
	return status == http.StatusBadRequest || status == http.StatusUnprocessableEntity
}
