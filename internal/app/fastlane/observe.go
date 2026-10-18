package fastlane

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog"
)

// Metrics counts what goes wrong at the fast lane's boundaries, on the process's /metrics.
type Metrics struct {
	Panics *prometheus.CounterVec // panics_total{component}
	Errors *prometheus.CounterVec // errors_total{module}
}

// NewMetrics registers the counters on reg. A nil reg gives counters that aren't exposed.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	m := &Metrics{
		Panics: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "panics_total", Help: "Recovered panics, by component."}, []string{"component"}),
		Errors: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "errors_total", Help: "Errors at a processing boundary, by module."}, []string{"module"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{m.Panics, m.Errors} {
			if err := reg.Register(c); err != nil {
				return nil, fmt.Errorf("register metrics: %w", err)
			}
		}
	}
	return m, nil
}

// Failed counts an error at a module's boundary.
func (m *Metrics) Failed(module string) { m.failed(module) }

func (m *Metrics) panicked(component string) {
	if m != nil {
		m.Panics.WithLabelValues(component).Inc()
	}
}

func (m *Metrics) failed(module string) {
	if m != nil {
		m.Errors.WithLabelValues(module).Inc()
	}
}

// ErrPanic marks an error made from a recovered panic: retrying the same input would panic again.
var ErrPanic = errors.New("recovered panic")

// Recovered turns a panic in fn into an error (wrapping ErrPanic), logging it at error level with the panic value
// and the stack, and counting it. One bad message or job must never stop the bot.
func Recovered(log zerolog.Logger, m *Metrics, component string, fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			m.panicked(component)
			log.Error().Str("component", component).Interface("panic", p).Str("stack", string(debug.Stack())).Msg("recovered from a panic")
			err = fmt.Errorf("%w in %s: %v", ErrPanic, component, p)
		}
	}()
	return fn()
}

// CallerHook adds the caller's file:line to warn and error lines, so an unexpected failure can be
// traced to where it was logged.
type CallerHook struct{}

// Run implements zerolog.Hook.
func (CallerHook) Run(e *zerolog.Event, level zerolog.Level, _ string) {
	if level < zerolog.WarnLevel {
		return
	}
	// Skip Run, the hook dispatch, Msg/msg and the logging call itself.
	for skip := 3; skip < 8; skip++ {
		_, file, line, ok := runtime.Caller(skip)
		if !ok {
			return
		}
		if !isZerolog(file) {
			e.Str("caller", file+":"+strconv.Itoa(line))
			return
		}
	}
}

func isZerolog(file string) bool {
	return strings.Contains(file, "/rs/zerolog@") || strings.Contains(file, "/rs/zerolog/")
}

// JobErrors logs River job failures and panics with the job's kind and ID, and counts them.
type JobErrors struct {
	Log     zerolog.Logger
	Metrics *Metrics
}

var _ river.ErrorHandler = JobErrors{}

// HandleError implements river.ErrorHandler.
func (j JobErrors) HandleError(_ context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	j.Metrics.failed("jobs")
	j.Log.Error().Err(err).Str("job", job.Kind).Int64("job_id", job.ID).Int("attempt", job.Attempt).Msg("job failed")
	return nil
}

// HandlePanic implements river.ErrorHandler.
func (j JobErrors) HandlePanic(_ context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	j.Metrics.panicked("jobs")
	j.Log.Error().Str("job", job.Kind).Int64("job_id", job.ID).Int("attempt", job.Attempt).Interface("panic", panicVal).Str("stack", trace).Msg("job panicked")
	return nil
}
