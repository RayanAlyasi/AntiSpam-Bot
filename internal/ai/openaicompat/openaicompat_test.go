package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
)

var schema = map[string]any{"type": "object", "properties": map[string]any{"label": map[string]any{"type": "string"}}}

const okBody = `{"id":"c1","model":"served-model","choices":[{"index":0,"finish_reason":"stop",
"message":{"role":"assistant","content":"{\"label\":\"not_spam\",\"confidence\":0.95,\"categories\":[],\"reason\":\"chat\"}"}}],
"usage":{"prompt_tokens":120,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":100}}}`

type call struct {
	path, auth string
	body       map[string]any
}

// server is a recorded HTTP fake of /chat/completions; respond picks the status and body per call.
type server struct {
	*httptest.Server
	mu    sync.Mutex
	calls []call
}

func newServer(t *testing.T, respond func(c call) (int, string)) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := call{path: r.URL.Path, auth: r.Header.Get("Authorization")}
		if err := json.Unmarshal(raw, &c.body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		s.mu.Lock()
		s.calls = append(s.calls, c)
		s.mu.Unlock()
		status, body := respond(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) recorded() []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]call(nil), s.calls...)
}

func formatType(c call) string {
	rf, _ := c.body["response_format"].(map[string]any)
	typ, _ := rf["type"].(string)
	return typ
}

func ok(call) (int, string) { return http.StatusOK, okBody }

func newClient(t *testing.T, url string, mod func(*Options)) *Client {
	t.Helper()
	o := Options{BaseURL: url + "/v1/", Model: "llama3"}
	if mod != nil {
		mod(&o)
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return c
}

func TestCompleteNativeRequestShape(t *testing.T) {
	s := newServer(t, ok)
	c := newClient(t, s.URL, func(o *Options) { o.APIKey = "sk-test" })
	resp, err := c.Complete(t.Context(), llm.Request{System: "SYS", Data: "DATA", Schema: schema, Mode: llm.ModeNative})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	got := s.recorded()[0]
	if got.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions (base_url + /chat/completions)", got.path)
	}
	if got.auth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", got.auth)
	}
	msgs := got.body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "SYS" ||
		msgs[1].(map[string]any)["role"] != "user" || msgs[1].(map[string]any)["content"] != "DATA" {
		t.Errorf("messages = %v, want [system SYS, user DATA]", msgs)
	}
	rf := got.body["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if rf["type"] != "json_schema" || js["strict"] != true || js["schema"] == nil {
		t.Errorf("response_format = %v, want strict json_schema with the verdict schema", rf)
	}
	if got.body["model"] != "llama3" || got.body["max_tokens"] != float64(DefaultMaxTokens) {
		t.Errorf("model/max_tokens = %v/%v", got.body["model"], got.body["max_tokens"])
	}
	want := llm.Usage{InputTokens: 20, OutputTokens: 20, CacheReadTokens: 100}
	if resp.Usage != want || resp.Model != "served-model" || resp.Text == "" {
		t.Errorf("resp = %+v, want usage %+v, model served-model and the content text", resp, want)
	}
}

func TestCompleteLocalServerWithoutKeyOrUsage(t *testing.T) {
	s := newServer(t, func(call) (int, string) {
		return http.StatusOK, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`
	})
	c := newClient(t, s.URL, func(o *Options) { o.StructuredOutput = StructuredJSON })
	resp, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: schema, Mode: llm.ModeNative})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	got := s.recorded()[0]
	if got.auth != "" {
		t.Errorf("Authorization = %q, want none when no key is configured (local servers)", got.auth)
	}
	rf := got.body["response_format"].(map[string]any)
	if rf["type"] != "json_object" || rf["json_schema"] != nil {
		t.Errorf("response_format = %v, want json_object and no schema in json mode", rf)
	}
	if resp.Usage != (llm.Usage{}) || resp.Model != "llama3" || resp.Text != "{}" {
		t.Errorf("resp = %+v, want zero usage, configured model and the text", resp)
	}
	if c.Caps().NativeSchema {
		t.Error("Caps().NativeSchema = true in json mode, want false")
	}
}

func TestAutoFallsBackToJSONModeOnce(t *testing.T) {
	s := newServer(t, func(c call) (int, string) {
		if formatType(c) == "json_schema" {
			return http.StatusBadRequest, `{"error":{"message":"response_format json_schema not supported"}}`
		}
		return http.StatusOK, okBody
	})
	c := newClient(t, s.URL, nil)
	if !c.Caps().NativeSchema {
		t.Fatal("Caps().NativeSchema = false before any call, want true in auto mode")
	}
	req := llm.Request{System: "s", Data: "d", Schema: schema, Mode: llm.ModeNative}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("first Complete() error = %v, want the JSON-mode resend to succeed", err)
	}
	if _, err := c.Complete(t.Context(), req); err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}
	var types []string
	for _, cl := range s.recorded() {
		types = append(types, formatType(cl))
	}
	want := []string{"json_schema", "json_object", "json_object"}
	if len(types) != len(want) || types[0] != want[0] || types[1] != want[1] || types[2] != want[2] {
		t.Fatalf("response formats = %v, want %v (one native attempt, then JSON mode for the process)", types, want)
	}
	if c.Caps().NativeSchema {
		t.Error("Caps().NativeSchema = true after fallback, want false")
	}
}

func TestFallbackOnlyOnSchemaRejection(t *testing.T) {
	tests := []struct {
		name   string
		mode   string
		status int
		want   error
		calls  int
	}{
		{"auto 401", StructuredAuto, 401, llm.ErrAuth, 1},
		{"auto 404", StructuredAuto, 404, llm.ErrInvalidRequest, 1},
		{"auto 429", StructuredAuto, 429, llm.ErrRateLimited, 1},
		{"auto 500", StructuredAuto, 500, llm.ErrUnavailable, 1},
		{"auto 422 resends", StructuredAuto, 422, llm.ErrInvalidRequest, 2},
		{"native 400 never falls back", StructuredNative, 400, llm.ErrInvalidRequest, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(t, func(call) (int, string) { return tt.status, `{"error":{"message":"x"}}` })
			c := newClient(t, s.URL, func(o *Options) { o.StructuredOutput = tt.mode })
			_, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: schema, Mode: llm.ModeNative})
			if !errors.Is(err, tt.want) || len(s.recorded()) != tt.calls {
				t.Fatalf("err=%v calls=%d, want %v after %d call(s)", err, len(s.recorded()), tt.want, tt.calls)
			}
		})
	}
}

func TestAutoFallbackIsSafeUnderConcurrency(t *testing.T) {
	var native atomic.Int32
	s := newServer(t, func(c call) (int, string) {
		if formatType(c) == "json_schema" {
			native.Add(1)
			return http.StatusBadRequest, `{}`
		}
		return http.StatusOK, okBody
	})
	c := newClient(t, s.URL, nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: schema, Mode: llm.ModeNative}); err != nil {
				t.Errorf("Complete() error = %v", err)
			}
		})
	}
	wg.Wait()
	if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Schema: schema, Mode: llm.ModeNative}); err != nil {
		t.Fatal(err)
	}
	if n := native.Load(); n < 1 || n > 20 {
		t.Fatalf("native attempts = %d, want between 1 and 20 (each in-flight call at most once)", n)
	}
	if c.Caps().NativeSchema {
		t.Error("Caps().NativeSchema = true after fallback, want false")
	}
}

func TestCompleteResponseOutcomes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{"refusal field", `{"choices":[{"message":{"content":null,"refusal":"no"},"finish_reason":"stop"}]}`, llm.ErrRefused},
		{"content filter", `{"choices":[{"message":{"content":""},"finish_reason":"content_filter"}]}`, llm.ErrRefused},
		{"truncated", `{"choices":[{"message":{"content":"{\"la"},"finish_reason":"length"}]}`, llm.ErrTruncated},
		{"no choices", `{"choices":[]}`, llm.ErrUnavailable},
		{"not json", `<html>proxy error</html>`, llm.ErrUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(t, func(call) (int, string) { return http.StatusOK, tt.body })
			c := newClient(t, s.URL, nil)
			if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Mode: llm.ModeJSON}); !errors.Is(err, tt.want) {
				t.Fatalf("Complete() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCompleteTimeoutAndCancel(t *testing.T) {
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
	if _, err := c.Complete(t.Context(), llm.Request{System: "s", Data: "d", Mode: llm.ModeJSON}); !errors.Is(err, llm.ErrTimeout) {
		t.Fatalf("Complete() error = %v, want ErrTimeout (slow local model → still uncertain)", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.Complete(ctx, llm.Request{System: "s", Data: "d", Mode: llm.ModeJSON}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete() error = %v, want context.Canceled", err)
	}
}

func TestNewValidates(t *testing.T) {
	tests := []struct {
		name string
		o    Options
	}{
		{"no url", Options{Model: "m"}},
		{"url without scheme", Options{BaseURL: "ollama:11434/v1", Model: "m"}},
		{"ftp url", Options{BaseURL: "ftp://x/v1", Model: "m"}},
		{"no model", Options{BaseURL: "http://x/v1"}},
		{"bad mode", Options{BaseURL: "http://x/v1", Model: "m", StructuredOutput: "xml"}},
		{"negative max tokens", Options{BaseURL: "http://x/v1", Model: "m", MaxTokens: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.o); err == nil {
				t.Fatal("New() error = nil, want a configuration error")
			}
		})
	}
}
