package fastlane

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestRecoveredLogsPanicWithStack(t *testing.T) {
	var buf bytes.Buffer
	m, err := NewMetrics(nil)
	if err != nil {
		t.Fatal(err)
	}
	log := zerolog.New(&buf).Hook(CallerHook{})
	err = Recovered(log, m, "fastlane", func() error { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("recovered = %v, want the panic as an error", err)
	}
	out := buf.String()
	for _, want := range []string{`"level":"error"`, `"panic":"boom"`, `"stack":"goroutine`, `"component":"fastlane"`, `"caller":"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %s lacks %s", out, want)
		}
	}
	if !strings.Contains(out, "observe_test.go") && !strings.Contains(out, "observe.go") {
		t.Fatalf("caller %s doesn't point into the repo", out)
	}
	if err := Recovered(log, m, "fastlane", func() error { return errors.New("plain") }); err == nil || err.Error() != "plain" {
		t.Fatalf("a plain error must pass through unchanged, got %v", err)
	}
}
