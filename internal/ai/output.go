package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"
)

// Categories are the spam categories a verdict may name. The list is part of the schema and
// the system prompt, so changing it means a new PromptVersion.
var Categories = []string{
	"crypto", "investment", "phishing", "job_scam", "loan", "giveaway",
	"adult", "promotion", "malware", "impersonation", "other",
}

// Limits on a verdict's free-form parts.
const (
	MaxCategories   = 5
	MaxReasonLength = 500 // characters
)

// ErrInvalidOutput wraps every reason model output fails validation.
var ErrInvalidOutput = errors.New("invalid AI output")

// VerdictSchema returns the JSON Schema of a verdict, for providers with native schema output.
func VerdictSchema() json.RawMessage { return slices.Clone(verdictSchema) }

var verdictSchema = func() json.RawMessage {
	b, err := json.Marshal(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"label", "confidence", "categories", "reason"},
		"properties": map[string]any{
			"label":      map[string]any{"type": "string", "enum": []Label{LabelSpam, LabelNotSpam, LabelUnsure}},
			"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"categories": map[string]any{
				"type": "array", "maxItems": MaxCategories, "uniqueItems": true,
				"items": map[string]any{"type": "string", "enum": Categories},
			},
			"reason": map[string]any{"type": "string", "minLength": 1, "maxLength": MaxReasonLength},
		},
	})
	if err != nil {
		panic(err)
	}
	return b
}()

// rawVerdict uses pointers so a missing field is told apart from a zero value.
type rawVerdict struct {
	Label      *Label    `json:"label"`
	Confidence *float64  `json:"confidence"`
	Categories *[]string `json:"categories"`
	Reason     *string   `json:"reason"`
}

// ParseVerdict parses model output strictly against the verdict schema: exactly one JSON object
// with all four fields and nothing else, a known label, a confidence in [0, 1], known and unique
// categories, and a non-empty reason within MaxReasonLength. Surrounding whitespace and a single
// Markdown code fence (common with local models) are tolerated; anything else is ErrInvalidOutput.
func ParseVerdict(raw []byte) (Verdict, error) {
	body := unfence(bytes.TrimSpace(raw))
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var rv rawVerdict
	if err := dec.Decode(&rv); err != nil {
		return Verdict{}, fmt.Errorf("%w: %w", ErrInvalidOutput, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Verdict{}, fmt.Errorf("%w: trailing data after the object", ErrInvalidOutput)
	}
	if rv.Label == nil || rv.Confidence == nil || rv.Categories == nil || rv.Reason == nil {
		return Verdict{}, fmt.Errorf("%w: label, confidence, categories and reason are all required", ErrInvalidOutput)
	}
	v := Verdict{Label: *rv.Label, Confidence: *rv.Confidence, Categories: *rv.Categories, Reason: *rv.Reason}
	switch v.Label {
	case LabelSpam, LabelNotSpam, LabelUnsure:
	default:
		return Verdict{}, fmt.Errorf("%w: unknown label %q", ErrInvalidOutput, v.Label)
	}
	if math.IsNaN(v.Confidence) || v.Confidence < 0 || v.Confidence > 1 {
		return Verdict{}, fmt.Errorf("%w: confidence %v outside [0, 1]", ErrInvalidOutput, v.Confidence)
	}
	if len(v.Categories) > MaxCategories {
		return Verdict{}, fmt.Errorf("%w: more than %d categories", ErrInvalidOutput, MaxCategories)
	}
	for i, c := range v.Categories {
		if !slices.Contains(Categories, c) {
			return Verdict{}, fmt.Errorf("%w: unknown category %q", ErrInvalidOutput, c)
		}
		if slices.Contains(v.Categories[:i], c) {
			return Verdict{}, fmt.Errorf("%w: duplicate category %q", ErrInvalidOutput, c)
		}
	}
	if v.Reason == "" || utf8.RuneCountInString(v.Reason) > MaxReasonLength {
		return Verdict{}, fmt.Errorf("%w: reason must have 1 to %d characters", ErrInvalidOutput, MaxReasonLength)
	}
	return v, nil
}

// unfence strips one surrounding ``` or ```json fence.
func unfence(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("```")) || !bytes.HasSuffix(b, []byte("```")) || len(b) < 6 {
		return b
	}
	inner := b[3 : len(b)-3]
	nl := bytes.IndexByte(inner, '\n')
	if nl < 0 {
		return b
	}
	if tag := bytes.TrimSpace(inner[:nl]); len(tag) > 0 && !bytes.EqualFold(tag, []byte("json")) {
		return b
	}
	return bytes.TrimSpace(inner[nl+1:])
}

// SelectMode picks how an adapter asks for structured output: native schema output when the
// setting allows it and the provider supports it, else JSON mode with strict parsing. Asking for
// native output from a provider without it is a configuration error.
func SelectMode(setting OutputSetting, caps Caps) (OutputSetting, error) {
	switch setting {
	case OutputAuto, "":
		if caps.NativeSchema {
			return OutputNative, nil
		}
		return OutputJSON, nil
	case OutputNative:
		if !caps.NativeSchema {
			return "", errors.New("ai.structured_output native: this provider or model has no native schema output; use auto or json")
		}
		return OutputNative, nil
	case OutputJSON:
		return OutputJSON, nil
	}
	return "", fmt.Errorf("ai.structured_output %q: want auto, native or json", setting)
}

// Generate makes one model call and returns its raw output. retry is true on the second call,
// after invalid output, so an adapter can remind the model of the schema.
type Generate func(ctx context.Context, retry bool) ([]byte, error)

// Completion is the outcome of Complete.
type Completion struct {
	Verdict  Verdict
	Attempts int  // model calls made: 1 or 2
	Invalid  bool // both outputs were invalid, and Verdict is the stand-in unsure
}

// Complete runs gen and validates its output (DESIGN.md §7.4): invalid output is retried once,
// and if it's still invalid the verdict is unsure, so the message goes to admins. An error from
// gen (transport, timeout, cancellation) is returned as is; the caller treats it as still
// uncertain too.
func Complete(ctx context.Context, gen Generate) (Completion, error) {
	var c Completion
	for _, retry := range []bool{false, true} {
		if err := ctx.Err(); err != nil {
			return c, err
		}
		c.Attempts++
		raw, err := gen(ctx, retry)
		if err != nil {
			return c, fmt.Errorf("ai call %d: %w", c.Attempts, err)
		}
		if v, err := ParseVerdict(raw); err == nil {
			c.Verdict = v
			return c, nil
		}
	}
	c.Verdict, c.Invalid = Unsure("the model's output was invalid twice"), true
	return c, nil
}
