package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// script is a fake llm.Completer answering from a queue, then with fallback.
type script struct {
	mu       sync.Mutex
	caps     llm.Caps
	queue    []answer
	fallback answer
	reqs     []llm.Request
}

type answer struct {
	text string
	err  error
}

func (s *script) Caps() llm.Caps { return s.caps }

func (s *script) Complete(_ context.Context, r llm.Request) (llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
	a := s.fallback
	if len(s.queue) > 0 {
		a, s.queue = s.queue[0], s.queue[1:]
	}
	return llm.Response{Text: a.text, Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}}, a.err
}

func (s *script) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func verdictJSON(label string, conf float64) string {
	cats := `[]`
	if label == "spam" {
		cats = `["crypto"]`
	}
	return fmt.Sprintf(`{"label":%q,"confidence":%v,"categories":%s,"reason":"because"}`, label, conf, cats)
}

func request(text string) ai.ClassifyRequest {
	return ai.ClassifyRequest{Message: ai.Message{Kind: "text", Texts: []ai.Text{{Source: "body", Text: text}}}}
}

func TestClassifierModesAndRetry(t *testing.T) {
	tests := []struct {
		name      string
		caps      llm.Caps
		setting   ai.OutputSetting
		answers   []answer
		wantLabel ai.Label
		wantCalls int
		wantMode  llm.Mode
		wantErr   error
	}{
		{"native when supported", llm.Caps{NativeSchema: true}, ai.OutputAuto, []answer{{text: verdictJSON("spam", 0.95)}}, ai.LabelSpam, 1, llm.ModeNative, nil},
		{"json mode without native", llm.Caps{}, ai.OutputAuto, []answer{{text: verdictJSON("not_spam", 0.9)}}, ai.LabelNotSpam, 1, llm.ModeJSON, nil},
		{"json forced", llm.Caps{NativeSchema: true}, ai.OutputJSON, []answer{{text: verdictJSON("spam", 0.95)}}, ai.LabelSpam, 1, llm.ModeJSON, nil},
		{"invalid then valid", llm.Caps{}, ai.OutputAuto, []answer{{text: "sure, it's spam"}, {text: verdictJSON("spam", 0.92)}}, ai.LabelSpam, 2, llm.ModeJSON, nil},
		{"invalid twice is unsure", llm.Caps{}, ai.OutputAuto, []answer{{text: "{}"}, {text: `{"label":"maybe"}`}}, ai.LabelUnsure, 2, llm.ModeJSON, nil},
		{"provider error is unsure", llm.Caps{}, ai.OutputAuto, []answer{{err: llm.ErrTimeout}}, ai.LabelUnsure, 1, llm.ModeJSON, llm.ErrTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &script{caps: tt.caps, queue: tt.answers}
			c := NewClassifier(s, Options{Provider: "p", Model: "m", StructuredOutput: tt.setting})
			v, err := c.Classify(t.Context(), request("free crypto"))
			if v.Label != tt.wantLabel || !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Classify() = %+v, %v; want label %s, err %v", v, err, tt.wantLabel, tt.wantErr)
			}
			if s.calls() != tt.wantCalls || s.reqs[0].Mode != tt.wantMode {
				t.Fatalf("calls=%d mode=%v, want %d calls in %v", s.calls(), s.reqs[0].Mode, tt.wantCalls, tt.wantMode)
			}
			r := s.reqs[0]
			if r.System != ai.SystemPrompt() || !strings.Contains(r.Data, "free crypto") || r.Schema == nil {
				t.Errorf("request = %+v, want the system prompt, the rendered data block and the schema", r)
			}
			if tt.wantCalls == 2 && !strings.HasSuffix(s.reqs[1].Data, retryNote) {
				t.Errorf("retry data = %q, want the schema reminder appended", s.reqs[1].Data)
			}
		})
	}
}

func TestClassifierNativeSettingWithoutSupportIsUnsure(t *testing.T) {
	s := &script{}
	c := NewClassifier(s, Options{StructuredOutput: ai.OutputNative})
	v, err := c.Classify(t.Context(), request("x"))
	if err == nil || v.Label != ai.LabelUnsure || s.calls() != 0 {
		t.Fatalf("Classify() = %+v, %v after %d calls; want unsure, an error and no call", v, err, s.calls())
	}
}

func TestClassifierInputCap(t *testing.T) {
	long := strings.Repeat("x", 400)
	req := request("short message")
	req.Examples = []string{long}
	req.Context = &ai.Context{Previous: []ai.ContextMessage{{Text: long}}}
	base, _ := ai.RenderData(request("short message"))
	tests := []struct {
		name          string
		cap           int
		wantContext   bool
		wantExamples  bool
		wantErr       error
		wantNoRequest bool
	}{
		{"fits", 10_000, true, true, nil, false},
		{"drops context first", len(base) + 450, false, true, nil, false},
		{"then examples", len(base) + 10, false, false, nil, false},
		{"message too long", 10, false, false, ErrInputTooLong, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &script{fallback: answer{text: verdictJSON("spam", 0.95)}}
			c := NewClassifier(s, Options{MaxInputChars: tt.cap})
			v, err := c.Classify(t.Context(), req)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Classify() = %+v, %v; want err %v", v, err, tt.wantErr)
			}
			if tt.wantNoRequest {
				if s.calls() != 0 || v.Label != ai.LabelUnsure {
					t.Fatalf("calls=%d label=%s, want no call and unsure (the message itself is never cut)", s.calls(), v.Label)
				}
				return
			}
			d := s.reqs[0].Data
			if got := strings.Contains(d, `"previous"`); got != tt.wantContext {
				t.Errorf("context present = %v, want %v", got, tt.wantContext)
			}
			if got := strings.Contains(d, long); got != (tt.wantContext || tt.wantExamples) {
				t.Errorf("long text present = %v, want %v", got, tt.wantContext || tt.wantExamples)
			}
		})
	}
}

func TestNewFromConfig(t *testing.T) {
	env := map[string]string{"AI_API_KEY": "k"}
	getenv := func(k string) string { return env[k] }
	tests := []struct {
		name    string
		cfg     ai.Config
		wantNil bool
		wantErr bool
	}{
		{"off", ai.Config{}, true, false},
		{"anthropic", ai.Config{Provider: ai.ProviderAnthropic, Model: "claude-haiku-5-5", APIKeyEnv: "AI_API_KEY"}, false, false},
		{"anthropic without key", ai.Config{Provider: ai.ProviderAnthropic, Model: "m", APIKeyEnv: "MISSING"}, false, true},
		{"local server", ai.Config{Provider: ai.ProviderOpenAICompatible, Model: "llama3", BaseURL: "http://ollama:11434/v1"}, false, false},
		{"compatible without url", ai.Config{Provider: ai.ProviderOpenAICompatible, Model: "m"}, false, true},
		{"google has no adapter yet", ai.Config{Provider: ai.ProviderGoogle, Model: "g", APIKeyEnv: "AI_API_KEY"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.cfg, getenv, clock.Real{}, nil)
			if (err != nil) != tt.wantErr || (c == nil) != (tt.wantNil || tt.wantErr) {
				t.Fatalf("New() = %v, %v; wantNil=%v wantErr=%v", c, err, tt.wantNil, tt.wantErr)
			}
		})
	}
}

// harness builds a Verifier over a scripted completer.
type harness struct {
	s      *script
	v      *Verifier
	budget *budget.MemStore
}

func newHarness(t *testing.T, lim budget.Limits, answers ...answer) *harness {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC))
	s := &script{queue: answers, fallback: answer{text: verdictJSON("unsure", 0.5)}}
	rc, err := NewResultCache(cache.NewMemStore(), clk)
	if err != nil {
		t.Fatal(err)
	}
	bs := budget.NewMemStore()
	b, err := budget.New(bs, clk, lim, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(VerifierOptions{Classifier: NewClassifier(s, Options{Model: "m"}), Cache: rc, Budget: b})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{s: s, v: v, budget: bs}
}

func uncertain(text string) Item {
	return Item{
		Group: "g1@g.us", Scored: scoring.Verdict{Band: scoring.BandUncertain, Source: scoring.SourceRules},
		Request: request(text), NormText: text, ContextHash: "none", AIEnabled: true,
		Thresholds: scoring.DefaultThresholds(),
	}
}

func TestVerifyOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		answers    []answer
		wantBand   scoring.Band
		wantSource scoring.Source
		wantReason string
	}{
		{"confident spam: deleted, source ai", []answer{{text: verdictJSON("spam", 0.95)}}, scoring.BandUncertain, scoring.SourceAI, ReasonVerified},
		{"spam at the threshold", []answer{{text: verdictJSON("spam", 0.9)}}, scoring.BandUncertain, scoring.SourceAI, ReasonVerified},
		{"spam just below", []answer{{text: verdictJSON("spam", 0.89)}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonLowConfidence},
		{"confident not spam: clean", []answer{{text: verdictJSON("not_spam", 0.95)}}, scoring.BandClean, scoring.SourceAI, ReasonVerified},
		{"not spam, low confidence", []answer{{text: verdictJSON("not_spam", 0.6)}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonLowConfidence},
		{"unsure", []answer{{text: verdictJSON("unsure", 0.99)}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonUnsure},
		{"invalid twice", []answer{{text: "no"}, {text: "still no"}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonInvalid},
		{"timeout", []answer{{err: fmt.Errorf("x: %w", llm.ErrTimeout)}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonTimeout},
		{"provider error", []answer{{err: llm.ErrUnavailable}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonError},
		{"refusal", []answer{{err: llm.ErrRefused}}, scoring.BandStillUncertain, scoring.SourceRules, ReasonError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, budget.Limits{}, tt.answers...)
			out := h.v.Verify(t.Context(), uncertain("claim your prize"))
			if out.Verdict.Band != tt.wantBand || out.Verdict.Source != tt.wantSource || out.Reason != tt.wantReason {
				t.Fatalf("Verify() = %s/%s reason %q, want %s/%s reason %q (DESIGN §7.0)",
					out.Verdict.Band, out.Verdict.Source, out.Reason, tt.wantBand, tt.wantSource, tt.wantReason)
			}
			if !out.Called {
				t.Error("Called = false, want a provider call for an uncertain message")
			}
			if del := out.Verdict.DeletesAsSpam(); del != (tt.wantSource == scoring.SourceAI && tt.wantBand == scoring.BandUncertain) {
				t.Errorf("DeletesAsSpam() = %v; only a confident AI spam verdict reaches the delete line", del)
			}
		})
	}
}

func TestOnlyUncertainReachesTheAI(t *testing.T) {
	h := newHarness(t, budget.Limits{}, answer{text: verdictJSON("spam", 0.99)})
	for _, sv := range []scoring.Verdict{
		{Band: scoring.BandCertain, Source: scoring.SourceKnown},
		{Band: scoring.BandCertain, Violation: true},
		{Band: scoring.BandClean},
		{Band: scoring.BandStillUncertain, Source: scoring.SourceRules},
	} {
		it := uncertain("x")
		it.Scored = sv
		out := h.v.Verify(t.Context(), it)
		if out.Called || out.Verdict.Band != sv.Band || out.Verdict.Source != sv.Source {
			t.Errorf("band %s: Verify() = %+v, want it unchanged and no call", sv.Band, out)
		}
	}
	if h.s.calls() != 0 {
		t.Fatalf("provider calls = %d, want 0 (certain and clean messages never call the AI)", h.s.calls())
	}
}

func TestAIOffGoesToAdmins(t *testing.T) {
	off, err := NewVerifier(VerifierOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, budget.Limits{}, answer{text: verdictJSON("spam", 0.99)})
	disabled := uncertain("x")
	disabled.AIEnabled = false
	for name, run := range map[string]func() Outcome{
		"not configured":     func() Outcome { return off.Verify(t.Context(), uncertain("x")) },
		"disabled for group": func() Outcome { return h.v.Verify(t.Context(), disabled) },
	} {
		out := run()
		if out.Verdict.Band != scoring.BandStillUncertain || out.Reason != ReasonAIOff || out.Called || out.Verdict.DeletesAsSpam() {
			t.Errorf("%s: Verify() = %+v, want still uncertain (ask admins), reason ai_off, no call, nothing deleted", name, out)
		}
	}
	if h.s.calls() != 0 {
		t.Fatalf("provider calls = %d with AI disabled for the group, want 0", h.s.calls())
	}
}

func TestBudgetExhaustedGoesToAdmins(t *testing.T) {
	h := newHarness(t, budget.Limits{GroupPerDay: 1}, answer{text: verdictJSON("spam", 0.95)})
	if out := h.v.Verify(t.Context(), uncertain("first")); out.Verdict.Source != scoring.SourceAI {
		t.Fatalf("first message: %+v, want AI-verified", out)
	}
	out := h.v.Verify(t.Context(), uncertain("second, different"))
	if out.Reason != ReasonBudget || out.Called || out.Verdict.Band != scoring.BandStillUncertain || out.Verdict.DeletesAsSpam() {
		t.Fatalf("over budget: %+v, want still uncertain, reason budget, no call, nothing deleted (DESIGN §7.4)", out)
	}
	if h.s.calls() != 1 {
		t.Fatalf("provider calls = %d, want 1", h.s.calls())
	}
}

func TestEveryProviderRequestCountsAgainstTheBudget(t *testing.T) {
	h := newHarness(t, budget.Limits{}, answer{text: "not json"}, answer{text: verdictJSON("spam", 0.95)})
	h.v.Verify(t.Context(), uncertain("m"))
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if r := h.budget.Get(day, "g1@g.us"); r.Calls != 2 || r.InputTokens != 20 || r.OutputTokens != 10 {
		t.Fatalf("ai_usage row = %+v, want 2 calls (the invalid-output retry counts) and their tokens", r)
	}
}

func TestCacheAndClusterSaveCalls(t *testing.T) {
	h := newHarness(t, budget.Limits{GroupPerDay: 1}, answer{text: verdictJSON("spam", 0.97)})
	first := h.v.Verify(t.Context(), uncertain("join my channel"))
	again := h.v.Verify(t.Context(), uncertain("join my channel"))
	if again.Source != cache.SourceCached || again.Called || again.Verdict.Source != scoring.SourceAI {
		t.Fatalf("repeat: %+v, want a cached AI verdict without a call", again)
	}
	copyElsewhere := uncertain("join my channel!!")
	copyElsewhere.Group, copyElsewhere.Cluster = "g2@g.us", "campaign-1"
	first2 := uncertain("join my channel")
	first2.Cluster = "campaign-1"
	h.v.Verify(t.Context(), first2) // a cache hit fills the cluster
	reused := h.v.Verify(t.Context(), copyElsewhere)
	if reused.Source != cache.SourceCluster || reused.Called || reused.Verdict.Source != scoring.SourceAI {
		t.Fatalf("cluster copy in another group: %+v, want the reused verdict, no call, no budget", reused)
	}
	if h.s.calls() != 1 || !first.Called {
		t.Fatalf("provider calls = %d, want 1 for the whole campaign", h.s.calls())
	}
	if reused.Verdict.AI == nil || reused.Verdict.AI.Reason != "because" {
		t.Errorf("reused verdict = %+v, want the sanitized spam verdict with its short reason", reused.Verdict.AI)
	}
}

func TestSanitize(t *testing.T) {
	long := strings.Repeat("é", 300)
	if got := Sanitize(ai.Verdict{Label: ai.LabelSpam, Reason: long}).Reason; len([]rune(got)) != maxStoredReason {
		t.Errorf("spam reason = %d runes, want capped at %d", len([]rune(got)), maxStoredReason)
	}
	if got := Sanitize(ai.Verdict{Label: ai.LabelNotSpam, Reason: "a normal chat about cars"}).Reason; got != "" {
		t.Errorf("not_spam reason = %q, want dropped (no normal-message text is kept, §17)", got)
	}
	if !Definitive(ai.Verdict{Label: ai.LabelSpam}) || !Definitive(ai.Verdict{Label: ai.LabelNotSpam}) || Definitive(ai.Verdict{Label: ai.LabelUnsure}) {
		t.Error("Definitive must accept spam and not_spam only")
	}
}

// Property (DESIGN §18): whatever the AI answers or however it fails, a message reaches the
// delete line only on a spam verdict at or above ai.spam_confidence, and the source is then ai,
// never known.
func TestPropertyOnlyConfidentAISpamDeletes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		label := rapid.SampledFrom([]string{"spam", "not_spam", "unsure", "bogus"}).Draw(rt, "label")
		conf := rapid.Float64Range(-0.5, 1.5).Draw(rt, "conf")
		fail := rapid.SampledFrom([]error{nil, nil, llm.ErrTimeout, llm.ErrUnavailable, llm.ErrRefused}).Draw(rt, "err")
		thr := rapid.Float64Range(0.5, 1).Draw(rt, "threshold")
		h := newHarness(t, budget.Limits{}, answer{text: verdictJSON(label, conf), err: fail}, answer{text: verdictJSON(label, conf)})
		it := uncertain("m")
		it.Thresholds.AIConfidence = thr
		out := h.v.Verify(context.Background(), it)
		want := fail == nil && label == "spam" && conf >= thr && conf <= 1
		if out.Verdict.DeletesAsSpam() != want {
			rt.Fatalf("label=%s conf=%v err=%v thr=%v: deletes=%v, want %v", label, conf, fail, thr, out.Verdict.DeletesAsSpam(), want)
		}
		if out.Verdict.Source == scoring.SourceKnown {
			rt.Fatalf("AI evidence became source known")
		}
	})
}
