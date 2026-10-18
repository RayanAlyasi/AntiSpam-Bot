package pipeline

import (
	"context"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/aicontext"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/gateway"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/redact"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
)

// maxAIText caps each text surface sent to the AI (DESIGN.md §7.4 cost controls).
const maxAIText = 1000

// AIVerifier adapts the AI gateway (DESIGN.md §7.4) to the engine's Verifier seam: it builds the
// redacted request for an UNCERTAIN message and returns the resolved verdict.
type AIVerifier struct {
	Gateway *gateway.Verifier
}

// Verify implements Verifier.
func (a AIVerifier) Verify(ctx context.Context, e *envelope.Envelope, s policy.Settings, v scoring.Verdict, th scoring.Thresholds) scoring.Verdict {
	if a.Gateway == nil {
		return scoring.Resolve(v, nil, th)
	}
	msg := ai.Message{Kind: string(e.Kind), IsEdit: e.IsEdit}
	var norm []string
	for _, t := range e.Texts {
		text := t.Norm
		if text == "" {
			text = t.Raw
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		msg.Texts = append(msg.Texts, ai.Text{Source: t.Source, Text: redact.Text(text, redact.Options{MaxRunes: maxAIText})})
		norm = append(norm, text)
	}
	for _, u := range e.URLs {
		msg.URLs = append(msg.URLs, u.URL)
	}
	for _, sc := range v.Score.Signals {
		if sc.Counted && !sc.Hard {
			msg.Signals = append(msg.Signals, sc.Code)
		}
	}
	out := a.Gateway.Verify(ctx, gateway.Item{
		Group:   string(e.Group),
		Scored:  v,
		Request: ai.ClassifyRequest{Message: msg},
		// The context bundle (F-108) needs a per-group "ai.context" setting that v0.1's settings
		// don't have yet, so it stays off: the AI sees only the message (the private choice).
		ContextHash: aicontext.NoContextHash,
		NormText:    strings.Join(norm, "\n"),
		AIEnabled:   s.Detection.AI.Enabled,
		GroupBudget: s.Detection.AI.DailyBudgetCalls,
		Thresholds:  th,
	})
	return out.Verdict
}
