package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
)

var verdictSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []any{"label", "confidence", "categories", "reason"},
	"properties": map[string]any{
		"label":      map[string]any{"type": "string", "enum": []any{"spam", "not_spam", "unsure"}},
		"confidence": map[string]any{"type": "number"},
		"categories": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"reason":     map[string]any{"type": "string"},
	},
}

const okBody = `{"id":"msg_1","type":"message","role":"assistant","model":"claude-test",
"content":[{"type":"text","text":"{\"label\":\"spam\",\"confidence\":0.97,\"categories\":[\"crypto\"],\"reason\":\"r\"}"}],
"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":30,"cache_read_input_tokens":900,"cache_creation_input_tokens":0}}`

type recorded struct {
	path, apiKey, version string
	body                  map[string]any
}

// fakeAPI is a recorded HTTP fake of the Messages endpoint.
func fakeAPI(t *testing.T, status int, body string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.path, rec.apiKey, rec.version = r.URL.Path, r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Version")
		if err := json.Unmarshal(raw, &rec.body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func newClient(t *testing.T, url string, mod func(*Options)) *Client {
	t.Helper()
	o := Options{BaseURL: url, APIKey: "config-key", Model: "claude-test"}
	if mod != nil {
		mod(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func TestCompleteSendsCachedSystemPromptAndSchema(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key-must-not-be-used")
	srv, rec := fakeAPI(t, http.StatusOK, okBody)
	c := newClient(t, srv.URL, nil)

	resp, err := c.Complete(t.Context(), llm.Request{System: "SYSTEM v1", Data: "<data>msg</data>", Schema: verdictSchema, Mode: llm.ModeNative})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if rec.path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", rec.path)
	}
	if rec.apiKey != "config-key" {
		t.Errorf("x-api-key = %q, want the configured key (never ANTHROPIC_API_KEY from the environment)", rec.apiKey)
	}
	if rec.version == "" {
		t.Error("anthropic-version header missing")
	}
	sys, _ := rec.body["system"].([]any)
	if len(sys) != 1 {
		t.Fatalf("system = %v, want one block", rec.body["system"])
	}
	block := sys[0].(map[string]any)
	if block["text"] != "SYSTEM v1" {
		t.Errorf("system text = %v, want the stable system prompt", block["text"])
	}
	if cc, _ := block["cache_control"].(map[string]any); cc["type"] != "ephemeral" {
		t.Errorf("system cache_control = %v, want ephemeral (prompt caching, DESIGN §7.4)", block["cache_control"])
	}
	msgs, _ := rec.body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want only the user data turn", rec.body["messages"])
	}
	m := msgs[0].(map[string]any)
	content := m["content"].([]any)[0].(map[string]any)
	if m["role"] != "user" || content["text"] != "<data>msg</data>" {
		t.Errorf("user turn = %v, want the data block as user text", m)
	}
	oc, _ := rec.body["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config.format = %v, want json_schema with the verdict schema", oc["format"])
	}
	if rec.body["model"] != "claude-test" || rec.body["max_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("model/max_tokens = %v/%v, want claude-test/%d", rec.body["model"], rec.body["max_tokens"], DefaultMaxTokens)
	}
	if _, ok := oc["effort"]; ok {
		t.Errorf("effort sent without being configured: %v (older models reject it)", oc)
	}

	wantText := `{"label":"spam","confidence":0.97,"categories":["crypto"],"reason":"r"}`
	if resp.Text != wantText {
		t.Errorf("Text = %q, want %q", resp.Text, wantText)
	}
	want := llm.Usage{InputTokens: 12, OutputTokens: 30, CacheReadTokens: 900}
	if resp.Usage != want || resp.Model != "claude-test" {
		t.Errorf("usage/model = %+v/%q, want %+v/claude-test", resp.Usage, resp.Model, want)
	}
}

func TestCompleteJSONModeAndOptions(t *testing.T) {
	srv, rec := fakeAPI(t, http.StatusOK, okBody)
	c := newClient(t, srv.URL, func(o *Options) { o.Effort = "low"; o.MaxTokens = 512 })
	if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Mode: llm.ModeJSON}); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	oc, _ := rec.body["output_config"].(map[string]any)
	if _, ok := oc["format"]; ok {
		t.Errorf("output_config.format = %v, want none in JSON mode", oc["format"])
	}
	if oc["effort"] != "low" || rec.body["max_tokens"] != float64(512) {
		t.Errorf("effort/max_tokens = %v/%v, want low/512", oc["effort"], rec.body["max_tokens"])
	}
}

func TestCompleteErrorsMapToSentinels(t *testing.T) {
	errBody := func(typ string) string { return `{"type":"error","error":{"type":"` + typ + `","message":"x"}}` }
	stop := func(reason string) string {
		return `{"id":"m","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"{"}],"stop_reason":"` + reason + `","usage":{"input_tokens":1,"output_tokens":1}}`
	}
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"bad key", 401, errBody("authentication_error"), llm.ErrAuth},
		{"forbidden", 403, errBody("permission_error"), llm.ErrAuth},
		{"rate limited", 429, errBody("rate_limit_error"), llm.ErrRateLimited},
		{"bad request", 400, errBody("invalid_request_error"), llm.ErrInvalidRequest},
		{"server error", 500, errBody("api_error"), llm.ErrUnavailable},
		{"overloaded", 529, errBody("overloaded_error"), llm.ErrUnavailable},
		{"refusal", 200, stop("refusal"), llm.ErrRefused},
		{"truncated", 200, stop("max_tokens"), llm.ErrTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := fakeAPI(t, tt.status, tt.body)
			c := newClient(t, srv.URL, nil)
			_, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: verdictSchema})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Complete() error = %v, want %v (a failed call must never look like a verdict)", err, tt.want)
			}
		})
	}
}

func TestCompleteDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, nil)
	_, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: verdictSchema})
	if !errors.Is(err, llm.ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d, want ErrUnavailable after exactly 1 call (retries belong to the validation layer)", err, calls.Load())
	}
}

func TestCompleteTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)
	c := newClient(t, srv.URL, func(o *Options) { o.Timeout = 50 * time.Millisecond })
	_, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: verdictSchema})
	if !errors.Is(err, llm.ErrTimeout) {
		t.Fatalf("Complete() error = %v, want ErrTimeout (a slow provider means still uncertain)", err)
	}
}

func TestCompleteCancelledIsNotAnOutage(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusOK, okBody)
	c := newClient(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := c.Complete(ctx, llm.Request{System: "s", Data: "d", Schema: verdictSchema})
	if !errors.Is(err, context.Canceled) || errors.Is(err, llm.ErrUnavailable) {
		t.Fatalf("Complete() error = %v, want context.Canceled, not ErrUnavailable", err)
	}
}

func TestNewValidates(t *testing.T) {
	tests := []struct {
		name string
		o    Options
	}{
		{"no key", Options{Model: "m"}},
		{"no model", Options{APIKey: "k"}},
		{"negative timeout", Options{APIKey: "k", Model: "m", Timeout: -time.Second}},
		{"bad effort", Options{APIKey: "k", Model: "m", Effort: "extreme"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.o); err == nil {
				t.Fatal("New() error = nil, want a configuration error")
			}
		})
	}
	c, err := New(Options{APIKey: "k", Model: "m"})
	if err != nil || !c.Caps().NativeSchema || !c.Caps().PromptCaching {
		t.Fatalf("New() = %+v, %v; want caps with native schema and prompt caching", c, err)
	}
}

func TestNativeModeNeedsSchema(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusOK, okBody)
	c := newClient(t, srv.URL, nil)
	if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d"}); !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatalf("Complete() error = %v, want ErrInvalidRequest", err)
	}
}
