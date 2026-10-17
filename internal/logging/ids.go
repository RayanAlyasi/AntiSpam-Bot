package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/rs/zerolog"
)

type ctxKey int

const (
	requestIDKey ctxKey = iota
	detectionIDKey
)

// NewID returns a random 16-character hex ID for a request or a detection.
func NewID() string {
	var b [8]byte
	// crypto/rand.Read never returns an error; it crashes the program if the OS can't supply
	// randomness.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WithRequestID returns logger with a "request_id" field, and ctx carrying both that logger
// (for zerolog.Ctx) and the ID (for RequestID).
func WithRequestID(ctx context.Context, logger zerolog.Logger, id string) (context.Context, zerolog.Logger) {
	return withID(ctx, logger, requestIDKey, "request_id", id)
}

// WithDetectionID returns logger with a "detection_id" field, and ctx carrying both that
// logger (for zerolog.Ctx) and the ID (for DetectionID).
func WithDetectionID(ctx context.Context, logger zerolog.Logger, id string) (context.Context, zerolog.Logger) {
	return withID(ctx, logger, detectionIDKey, "detection_id", id)
}

// RequestID returns the request ID in ctx, or "" if there is none.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// DetectionID returns the detection ID in ctx, or "" if there is none.
func DetectionID(ctx context.Context) string {
	id, _ := ctx.Value(detectionIDKey).(string)
	return id
}

func withID(ctx context.Context, logger zerolog.Logger, key ctxKey, field, id string) (context.Context, zerolog.Logger) {
	logger = logger.With().Str(field, id).Logger()
	ctx = context.WithValue(ctx, key, id)
	return logger.WithContext(ctx), logger
}
