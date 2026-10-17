package chat

import (
	"context"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/ai/safety"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// SafetyTrack adapts the AI safety gate (F-112) to TrackRecord: `.autoban … ai` is accepted
// only once the AI has a track record on this instance (§9.2). safety.Unmet{} is v0.1's record.
type SafetyTrack struct {
	Record safety.TrackRecord
}

var _ TrackRecord = SafetyTrack{}

// AIReady reports whether AI evidence may auto-ban. The track record is instance-wide.
func (s SafetyTrack) AIReady(context.Context, wa.JID) (bool, error) {
	if s.Record == nil {
		return false, nil
	}
	return safety.AllowAISource(s.Record), nil
}
