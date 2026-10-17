package gateway

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aimetrics"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/budget"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/cache"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/llm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// Reasons an UNCERTAIN message stays uncertain (the metric label and the explanation).
const (
	ReasonVerified      = ""               // the AI settled it
	ReasonAIOff         = "ai_off"         // no AI configured, or AI disabled for the group
	ReasonBudget        = "budget"         // a budget is exhausted
	ReasonTimeout       = "timeout"        // the provider was too slow
	ReasonError         = "error"          // any other failure
	ReasonInvalid       = "invalid_output" // the model's output was invalid twice
	ReasonUnsure        = "unsure"         // the model said unsure
	ReasonLowConfidence = "low_confidence" // a label below its confidence threshold
)

// maxStoredReason caps a reason kept in the cache or reused for other copies (§17).
const maxStoredReason = 200

// errBudget marks a call skipped because a budget is exhausted.
var errBudget = errors.New("ai: budget exhausted")

// Item is one message to verify.
type Item struct {
	Group       string             // the group JID, for the budget
	Scored      scoring.Verdict    // scoring's verdict before the AI (scoring.Classify)
	Request     ai.ClassifyRequest // what the AI sees, already redacted
	NormText    string             // normalized text, for the cache key
	ContextHash string             // from the context bundle; aicontext.NoContextHash when off
	Cluster     string             // SimHash cluster or campaign ID; "" when none
	AIEnabled   bool               // the group's ai.enabled
	GroupBudget int                // the group's ai.daily_budget_calls; 0 uses the instance default
	Thresholds  scoring.Thresholds
}

// Outcome is the verified verdict and how it was reached.
type Outcome struct {
	Verdict scoring.Verdict // resolved band and source (scoring.Resolve)
	Called  bool            // a provider call was made for this item
	Source  cache.Source    // fresh, cache or cluster, when the AI answered
	Reason  string          // ReasonVerified, or why it is still uncertain
	Err     error           // the failure behind ReasonTimeout/ReasonError, for logs
}

// Verifier runs T3 for the UNCERTAIN band (DESIGN.md §7.0, §7.4).
type Verifier struct {
	classifier ai.AIClassifier // nil: AI off
	model      string
	cache      *cache.Cache[ai.Verdict]
	budget     *budget.Budget
	prices     budget.Prices
	metrics    *aimetrics.Metrics
}

// VerifierOptions wire a Verifier. Classifier may be nil (AI off); Budget and Metrics may be nil.
// With a *Classifier every provider request (the invalid-output retry included) takes a budget
// call; with any other ai.AIClassifier each Classify takes one.
type VerifierOptions struct {
	Classifier ai.AIClassifier
	Model      string // for the cache key; defaults to the *Classifier's model
	Cache      *cache.Cache[ai.Verdict]
	Budget     *budget.Budget
	Prices     budget.Prices
	Metrics    *aimetrics.Metrics
}

// NewVerifier builds a Verifier. A Cache is required when a classifier is set.
func NewVerifier(o VerifierOptions) (*Verifier, error) {
	if o.Classifier != nil && isNil(o.Classifier) {
		o.Classifier = nil
	}
	if _, ok := o.Classifier.(*Classifier); o.Classifier != nil && !ok && o.Model == "" {
		return nil, errors.New("gateway: Model is required with a classifier other than *Classifier (it is part of the cache key)")
	}
	if o.Classifier != nil && o.Cache == nil {
		return nil, errors.New("gateway: a result cache is required with a classifier")
	}
	if err := o.Prices.Validate(); err != nil {
		return nil, err
	}
	model := o.Model
	if c, ok := o.Classifier.(*Classifier); ok && model == "" {
		model = c.Model()
	}
	return &Verifier{classifier: o.Classifier, model: model, cache: o.Cache, budget: o.Budget, prices: o.Prices, metrics: o.Metrics}, nil
}

// NewResultCache builds the AI result cache for verdicts: only spam and not_spam are kept, and a
// stored or reused verdict carries no more than a short reason.
func NewResultCache(store cache.Store, clk clock.Clock) (*cache.Cache[ai.Verdict], error) {
	return cache.New(store, clk, cache.Options[ai.Verdict]{Definitive: Definitive, Sanitize: Sanitize})
}

// Definitive reports whether a verdict may be cached and reused.
func Definitive(v ai.Verdict) bool {
	return v.Label == ai.LabelSpam || v.Label == ai.LabelNotSpam
}

// Sanitize prepares a verdict for storage and for copies in other groups: the reason is capped
// for spam and dropped for not_spam, so no normal-message text is kept (DESIGN.md §17).
func Sanitize(v ai.Verdict) ai.Verdict {
	v.Categories = slices.Clone(v.Categories)
	switch {
	case v.Label != ai.LabelSpam:
		v.Reason = ""
	case utf8.RuneCountInString(v.Reason) > maxStoredReason:
		v.Reason = string([]rune(v.Reason)[:maxStoredReason-1]) + "…"
	}
	return v
}

// Verify resolves one scored message. Only the UNCERTAIN band reaches the AI; CERTAIN and clean
// verdicts come back unchanged. The result never deletes on failure: AI off, budget, timeout,
// error, invalid output, unsure and low confidence all resolve to STILL_UNCERTAIN.
func (v *Verifier) Verify(ctx context.Context, it Item) Outcome {
	if it.Scored.Band != scoring.BandUncertain {
		return Outcome{Verdict: it.Scored}
	}
	if v.classifier == nil || !it.AIEnabled {
		return v.finish(it, nil, Outcome{Reason: ReasonAIOff})
	}
	// The shared call may outlive this caller (cache.Do keeps it running for other copies), so
	// everything it writes is atomic.
	var called, ran atomic.Bool
	var classifyErr, recordErr atomic.Pointer[error]
	classify := v.classifyFunc(it.Group, it.GroupBudget, &called, &recordErr)
	req := cache.Request{
		Key:           cache.Key(it.NormText, it.ContextHash, v.model, ai.PromptVersion),
		Cluster:       it.Cluster,
		Model:         v.model,
		PromptVersion: ai.PromptVersion,
	}
	verdict, src, err := v.cache.Do(ctx, req, func(ctx context.Context) (ai.Verdict, error) {
		ran.Store(true)
		vd, cerr := classify(ctx, it.Request)
		if cerr != nil {
			classifyErr.Store(&cerr)
		}
		return vd, cerr
	})
	out := Outcome{Called: called.Load(), Source: src, Err: err}
	// A failure is this caller's classify error, or, for a copy that waited on another caller's
	// call (or gave up waiting), any error with no answer. Otherwise err is only a cache-store
	// error: the verdict stands and the caller logs out.Err.
	if err != nil && (ctx.Err() != nil || classifyErr.Load() != nil || (!ran.Load() && src == cache.SourceFresh)) {
		out.Reason = reasonFor(err)
		return v.finish(it, nil, out)
	}
	if src != cache.SourceFresh && v.metrics != nil {
		v.metrics.Hits.WithLabelValues(src.String()).Inc()
	}
	if p := recordErr.Load(); p != nil {
		out.Err = errors.Join(out.Err, *p) // ai_usage missed this call's tokens; the verdict stands
	}
	return v.finish(it, &verdict, out)
}

func (v *Verifier) finish(it Item, verdict *ai.Verdict, out Outcome) Outcome {
	out.Verdict = scoring.Resolve(it.Scored, verdict, it.Thresholds)
	if out.Reason == ReasonVerified && out.Verdict.Band == scoring.BandStillUncertain {
		switch {
		case verdict == nil:
			out.Reason = ReasonError
		case verdict.Label == ai.LabelUnsure && verdict.Reason == invalidReason:
			out.Reason = ReasonInvalid
		case verdict.Label == ai.LabelUnsure:
			out.Reason = ReasonUnsure
		default:
			out.Reason = ReasonLowConfidence
		}
	}
	if v.metrics != nil && out.Reason != ReasonVerified {
		v.metrics.StillUncertains.WithLabelValues(out.Reason).Inc()
	}
	return out
}

// invalidReason is the reason ai.Complete gives its stand-in verdict after two invalid outputs.
var invalidReason = ai.Unsure("the model's output was invalid twice").Reason

func reasonFor(err error) string {
	switch {
	case errors.Is(err, errBudget):
		return ReasonBudget
	case errors.Is(err, llm.ErrTimeout), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ReasonTimeout
	default:
		return ReasonError
	}
}

// classifyFunc returns the classify call for group, with the budget applied.
func (v *Verifier) classifyFunc(group string, limit int, called *atomic.Bool, recordErr *atomic.Pointer[error]) func(context.Context, ai.ClassifyRequest) (ai.Verdict, error) {
	if c, ok := v.classifier.(*Classifier); ok {
		return c.with(&budgeted{inner: c.completer, v: v, group: group, limit: limit, called: called, recordErr: recordErr}).Classify
	}
	return func(ctx context.Context, req ai.ClassifyRequest) (ai.Verdict, error) {
		if v.budget != nil {
			d, err := v.budget.AllowLimit(ctx, group, limit)
			if err != nil {
				return ai.Unsure("budget check failed"), err
			}
			if !d.Allowed {
				return ai.Unsure("budget exhausted"), errBudget
			}
		}
		called.Store(true)
		return v.classifier.Classify(ctx, req)
	}
}

func isNil(c ai.AIClassifier) bool {
	p, ok := c.(*Classifier)
	return ok && p == nil
}

// budgeted reserves a budget call before every provider request (the invalid-output retry
// included) and records its usage after.
type budgeted struct {
	inner     llm.Completer
	v         *Verifier
	group     string
	limit     int
	called    *atomic.Bool
	recordErr *atomic.Pointer[error] // a failed usage write, reported with the verdict
}

func (b *budgeted) Caps() llm.Caps { return b.inner.Caps() }

func (b *budgeted) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if b.v.budget != nil {
		d, err := b.v.budget.AllowLimit(ctx, b.group, b.limit)
		if err != nil {
			return llm.Response{}, err
		}
		if !d.Allowed {
			return llm.Response{}, errBudget
		}
	}
	b.called.Store(true)
	resp, err := b.inner.Complete(ctx, req)
	u := b.v.prices.Usage(resp.Usage)
	if b.v.metrics != nil && u.CostMicros > 0 {
		c, _ := b.v.classifier.(*Classifier)
		b.v.metrics.CostMicros.WithLabelValues(c.provider, c.model).Add(float64(u.CostMicros))
	}
	if b.v.budget != nil {
		if rerr := b.v.budget.Record(ctx, b.group, u); rerr != nil {
			// The verdict is still good; the usage row only misses this call's tokens.
			b.recordErr.Store(&rerr)
		}
	}
	return resp, err
}
