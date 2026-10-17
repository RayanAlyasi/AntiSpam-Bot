package gateway

import (
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai"
)

func TestSanitizeRedactsTheStoredReason(t *testing.T) {
	v := Sanitize(ai.Verdict{Label: ai.LabelSpam, Reason: "asks to contact +971 50 123 4567 or win@prize.example"})
	for _, leak := range []string{"4567", "win@prize.example"} {
		if strings.Contains(v.Reason, leak) {
			t.Errorf("stored reason %q keeps %q; cached and reused verdicts carry no personal data (§17)", v.Reason, leak)
		}
	}
}
