// Package gateway is the AI tier's front door (DESIGN.md §7.4): an ai.AIClassifier built on a
// provider adapter, and the Verifier that sends only UNCERTAIN messages to it, through the
// result cache, cluster reuse and budgets, and resolves the band with the AI's answer. Every
// failure ends as "still uncertain": the message goes to admins and nothing is deleted.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aimetrics"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/anthropic"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/openaicompat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
)

// retryNote is appended to the data block on the one retry after invalid output (outside the
// data fence, so it reads as part of the fixed instructions, not as message content).
const retryNote = "\n\nYour previous answer was not a valid JSON object for the schema. Answer with only that JSON object."

// ErrInputTooLong means the rendered request is over ai.max_input_chars even without context
// and examples; the message goes to admins.
var ErrInputTooLong = errors.New("ai: request too long")

var verdictSchema = func() map[string]any {
	var m map[string]any
	if err := json.Unmarshal(ai.VerdictSchema(), &m); err != nil {
		panic(fmt.Sprintf("ai verdict schema: %v", err))
	}
	return m
}()

// Classifier implements ai.AIClassifier over a provider adapter.
type Classifier struct {
	completer llm.Completer
	provider  string
	model     string
	setting   ai.OutputSetting
	maxTokens int // 0: the adapter's default
	maxInput  int // 0: no cap
	metrics   *aimetrics.Metrics
}

var _ ai.AIClassifier = (*Classifier)(nil)

// Options configure a Classifier around an existing adapter.
type Options struct {
	Provider         string // label for metrics
	Model            string // label for metrics and the cache key
	StructuredOutput ai.OutputSetting
	MaxOutputTokens  int // 0 means the adapter's default
	MaxInputChars    int // 0 means no cap
	Metrics          *aimetrics.Metrics
}

// NewClassifier wraps an adapter.
func NewClassifier(c llm.Completer, o Options) *Classifier {
	return &Classifier{
		completer: c, provider: o.Provider, model: o.Model, setting: o.StructuredOutput,
		maxTokens: o.MaxOutputTokens, maxInput: o.MaxInputChars, metrics: o.Metrics,
	}
}

// Model returns the configured model, for cache keys.
func (c *Classifier) Model() string { return c.model }

// Capabilities implements ai.AIClassifier.
func (c *Classifier) Capabilities() ai.Caps {
	k := c.completer.Caps()
	return ai.Caps{NativeSchema: k.NativeSchema, Vision: k.Vision, PromptCaching: k.PromptCaching, MaxContextTokens: k.MaxContext}
}

// with returns a copy that sends its calls through completer (the Verifier wraps the adapter
// with the group's budget this way).
func (c *Classifier) with(completer llm.Completer) *Classifier {
	cp := *c
	cp.completer = completer
	return &cp
}

// Classify implements ai.AIClassifier: render, call, validate strictly, retry once on invalid
// output. On any failure it returns an unsure verdict with the error.
func (c *Classifier) Classify(ctx context.Context, req ai.ClassifyRequest) (ai.Verdict, error) {
	mode, err := ai.SelectMode(c.setting, c.Capabilities())
	if err != nil {
		return ai.Unsure("the AI is misconfigured"), fmt.Errorf("select output mode: %w", err)
	}
	data, err := c.render(req)
	if err != nil {
		return ai.Unsure("the message could not be sent to the AI"), err
	}
	lreq := llm.Request{System: ai.SystemPrompt(), Schema: verdictSchema, MaxTokens: c.maxTokens, Mode: llm.ModeJSON}
	if mode == ai.OutputNative {
		lreq.Mode = llm.ModeNative
	}
	gen := func(ctx context.Context, retry bool) ([]byte, error) {
		r := lreq
		r.Data = data
		if retry {
			r.Data += retryNote
			if c.metrics != nil {
				c.metrics.InvalidRetries.Inc()
			}
		}
		resp, err := c.completer.Complete(ctx, r)
		c.observe(resp, err)
		if err != nil {
			return nil, err
		}
		return []byte(resp.Text), nil
	}
	comp, err := ai.Complete(ctx, gen)
	if err != nil {
		return ai.Unsure("the AI did not answer"), err
	}
	if c.metrics != nil {
		c.metrics.Verdicts.WithLabelValues(string(comp.Verdict.Label)).Inc()
	}
	return comp.Verdict, nil
}

// render renders the data block within ai.max_input_chars, dropping the context and then the
// examples if needed. The message itself is never cut.
func (c *Classifier) render(req ai.ClassifyRequest) (string, error) {
	attempts := []ai.ClassifyRequest{req}
	if req.Context != nil {
		r := req
		r.Context = nil
		attempts = append(attempts, r)
	}
	if len(req.Examples) > 0 {
		r := req
		r.Context, r.Examples = nil, nil
		attempts = append(attempts, r)
	}
	for _, r := range attempts {
		data, err := ai.RenderData(r)
		if err != nil {
			return "", err
		}
		if c.maxInput <= 0 || len([]rune(data)) <= c.maxInput {
			return data, nil
		}
	}
	return "", ErrInputTooLong
}

func (c *Classifier) observe(resp llm.Response, err error) {
	if c.metrics == nil {
		return
	}
	outcome := aimetrics.OutcomeOK
	if err != nil {
		outcome = aimetrics.OutcomeError
	}
	c.metrics.ObserveCall(c.provider, c.model, outcome, resp.Latency, map[string]int64{
		"input": resp.Usage.InputTokens, "output": resp.Usage.OutputTokens,
		"cache_read": resp.Usage.CacheReadTokens, "cache_write": resp.Usage.CacheWriteTokens,
	}, 0)
}

// New builds the classifier the configuration asks for. It returns nil, nil when the AI is off:
// the UNCERTAIN band then goes straight to admins (DESIGN.md §7.4, fail safe).
func New(cfg ai.Config, getenv func(string) string, clk clock.Clock, m *aimetrics.Metrics) (*Classifier, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		return nil, nil
	}
	key := cfg.APIKey(getenv)
	var completer llm.Completer
	var err error
	switch cfg.Provider {
	case ai.ProviderAnthropic:
		// cfg.MaxOutputTokens defaults to a value sized for non-thinking models; Claude's
		// adaptive thinking counts against max_tokens, so the adapter keeps its own default
		// unless the operator raised the setting above it.
		maxTok := 0
		if cfg.MaxOutputTokens > anthropic.DefaultMaxTokens {
			maxTok = cfg.MaxOutputTokens
		}
		completer, err = anthropic.New(anthropic.Options{
			BaseURL: cfg.BaseURL, APIKey: key, Model: cfg.Model,
			Timeout: cfg.Timeout, MaxTokens: maxTok, Clock: clk,
		})
	case ai.ProviderOpenAICompatible:
		completer, err = openaicompat.New(openaicompat.Options{
			BaseURL: cfg.BaseURL, APIKey: key, Model: cfg.Model,
			Timeout: cfg.Timeout, MaxTokens: cfg.MaxOutputTokens, StructuredOutput: string(cfg.StructuredOutput), Clock: clk,
		})
	default:
		return nil, fmt.Errorf("ai.provider %q: no adapter in this version (anthropic or openai_compatible)", cfg.Provider)
	}
	if err != nil {
		return nil, err
	}
	return NewClassifier(completer, Options{
		Provider: cfg.Provider, Model: cfg.Model, StructuredOutput: cfg.StructuredOutput,
		MaxInputChars: cfg.MaxInputChars, Metrics: m,
	}), nil
}
