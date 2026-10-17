package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"testing"

	"github.com/rs/zerolog"
)

func TestNewIDIsRandomHex(t *testing.T) {
	hex16 := regexp.MustCompile(`^[0-9a-f]{16}$`)
	seen := make(map[string]bool)
	for range 1000 {
		id := NewID()
		if !hex16.MatchString(id) {
			t.Fatalf("NewID() = %q, want 16 lowercase hex characters", id)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q within 1000 calls; IDs must tell requests apart", id)
		}
		seen[id] = true
	}
}

func TestIDsInContext(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, zerolog.InfoLevel, nil).For("detect")

	ctx, reqLogger := WithRequestID(context.Background(), base, "req-1")
	ctx, detLogger := WithDetectionID(ctx, *zerolog.Ctx(ctx), "det-1")

	if got := RequestID(ctx); got != "req-1" {
		t.Errorf("RequestID(ctx) = %q, want %q", got, "req-1")
	}
	if got := DetectionID(ctx); got != "det-1" {
		t.Errorf("DetectionID(ctx) = %q, want %q", got, "det-1")
	}

	tests := []struct {
		name   string
		logger zerolog.Logger
		want   map[string]string
		absent string
	}{
		{"zerolog.Ctx carries both IDs", *zerolog.Ctx(ctx), map[string]string{"module": "detect", "request_id": "req-1", "detection_id": "det-1"}, ""},
		{"returned detection logger carries both IDs", detLogger, map[string]string{"request_id": "req-1", "detection_id": "det-1"}, ""},
		{"returned request logger has only the request ID", reqLogger, map[string]string{"request_id": "req-1"}, "detection_id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			tt.logger.Info().Msg("scored")
			var line map[string]any
			if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
				t.Fatalf("log line %q isn't JSON: %v", buf.String(), err)
			}
			for k, v := range tt.want {
				if line[k] != v {
					t.Errorf("field %q = %v, want %q (downstream logs must be traceable to the request)", k, line[k], v)
				}
			}
			if _, ok := line[tt.absent]; tt.absent != "" && ok {
				t.Errorf("field %q present in %q, want absent", tt.absent, buf.String())
			}
		})
	}
}

func TestIDsMissingFromContext(t *testing.T) {
	ctx := context.Background()
	if got := RequestID(ctx); got != "" {
		t.Errorf("RequestID(empty ctx) = %q, want \"\"", got)
	}
	if got := DetectionID(ctx); got != "" {
		t.Errorf("DetectionID(empty ctx) = %q, want \"\"", got)
	}
}
