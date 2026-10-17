package actions

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
)

func TestConfigFromPacingDefaultsMatch(t *testing.T) {
	got := ConfigFromPacing(config.Defaults().Pacing)
	want := DefaultConfig()
	if err := got.Validate(); err != nil {
		t.Fatalf("config from default pacing: %v", err)
	}
	if got.CeilingPerMin != want.CeilingPerMin || got.Burst != want.Burst || got.Spacing != want.Spacing ||
		got.Jitter != want.Jitter || got.GroupPerMin != want.GroupPerMin || got.SlowdownFactor != want.SlowdownFactor ||
		got.MinRatePerMin != want.MinRatePerMin || got.RecoveryStep != want.RecoveryStep ||
		got.RecoverEvery != want.RecoverEvery || got.MaxKickBatch != want.MaxKickBatch || got.DailyAlert != want.DailyAlert {
		t.Errorf("config.yaml pacing defaults map to %+v, want the queue defaults %+v (one source of truth, DESIGN.md §10.3)", got, want)
	}
}

func TestSlowdownMustSlowDown(t *testing.T) {
	for _, f := range []float64{0, 1, 1.5, -0.25} {
		c := DefaultConfig()
		c.SlowdownFactor = f
		if c.Validate() == nil {
			t.Errorf("SlowdownFactor %g validated, want an error (a warning sign must cut the rate)", f)
		}
	}
}
