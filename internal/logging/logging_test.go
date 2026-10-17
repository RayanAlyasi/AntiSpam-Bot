package logging

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestModuleLevels(t *testing.T) {
	modules := map[string]zerolog.Level{"store": zerolog.DebugLevel, "wa": zerolog.WarnLevel}
	tests := []struct {
		name   string
		module string // "" means Base
		level  zerolog.Level
		logged bool
	}{
		{"base drops debug at global info", "", zerolog.DebugLevel, false},
		{"base logs info at global info", "", zerolog.InfoLevel, true},
		{"store at debug logs debug", "store", zerolog.DebugLevel, true},
		{"store at debug logs info", "store", zerolog.InfoLevel, true},
		{"wa at warn drops info", "wa", zerolog.InfoLevel, false},
		{"wa at warn logs warn", "wa", zerolog.WarnLevel, true},
		{"unknown module falls back to global info", "detect", zerolog.InfoLevel, true},
		{"unknown module drops debug", "detect", zerolog.DebugLevel, false},
		{"module names are exact", "Store", zerolog.DebugLevel, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(&buf, zerolog.InfoLevel, modules)
			logger := l.Base()
			if tt.module != "" {
				logger = l.For(tt.module)
			}
			logger.WithLevel(tt.level).Msg("hello")
			if got := buf.Len() > 0; got != tt.logged {
				t.Fatalf("logged = %v, want %v (module %q at %v with LOG_LEVEL=info,store=debug,wa=warn); output %q",
					got, tt.logged, tt.module, tt.level, buf.String())
			}
		})
	}
}

func TestModuleAtTraceLogsTrace(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, zerolog.InfoLevel, map[string]zerolog.Level{"ai": zerolog.TraceLevel})
	ai := l.For("ai")
	ai.Trace().Msg("prompt built")
	if buf.Len() == 0 {
		t.Fatal("ai module set to trace logged nothing at trace; zerolog's process-wide floor must be lowered")
	}
}

func TestOutputIsJSONWithTimestampAndModule(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, zerolog.InfoLevel, nil)
	store := l.For("store")
	store.Info().Msg("migrated")

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line %q isn't JSON: %v (logs must be machine-readable)", buf.String(), err)
	}
	want := map[string]string{"level": "info", "module": "store", "message": "migrated"}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("field %q = %v, want %q", k, line[k], v)
		}
	}
	ts, _ := line[zerolog.TimestampFieldName].(string)
	if _, err := time.Parse(zerolog.TimeFieldFormat, ts); err != nil {
		t.Errorf("timestamp %q doesn't parse as %q: %v (every line needs a time)", ts, zerolog.TimeFieldFormat, err)
	}

	buf.Reset()
	base := l.Base()
	base.Info().Msg("started")
	if bytes.Contains(buf.Bytes(), []byte(`"module"`)) {
		t.Errorf("Base() output %q has a module field, want none", buf.String())
	}
}
