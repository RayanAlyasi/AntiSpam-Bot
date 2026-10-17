package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

// readyTimeout bounds each readiness probe, so a hung database can't hang the health check.
const readyTimeout = 2 * time.Second

// Readiness is what /readyz checks (DESIGN.md §16): the database answers and at least one bot
// number is connected to WhatsApp.
type Readiness struct {
	// PingDB checks the database; *pgxpool.Pool's Ping fits.
	PingDB func(ctx context.Context) error
	// ConnectedBots reports how many bot numbers are connected right now.
	ConnectedBots func() int
}

// NewRegistry returns the Prometheus registry for the process, with the Go runtime and process
// collectors registered. Packages register their own metrics on it.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}

// OpsHandler serves the operational endpoints:
//   - /metrics: Prometheus metrics from reg;
//   - /healthz: 200 while the process is serving (liveness);
//   - /readyz: 200 when the database answers and at least one bot is connected, else 503, with a
//     JSON body naming the failing check.
//
// Coolify and Docker health checks use /healthz; /readyz is for "is the bot actually protecting
// groups" alerts and load balancers.
func OpsHandler(reg *prometheus.Registry, ready Readiness, log zerolog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		body := readyBody{Status: "ok", Database: "ok"}
		code := http.StatusOK
		if ready.PingDB == nil {
			body.Database = "not configured"
		} else if err := ready.PingDB(ctx); err != nil {
			log.Warn().Err(err).Msg("readiness: database unreachable")
			body.Database = "unreachable"
		}
		if ready.ConnectedBots != nil {
			body.BotsConnected = ready.ConnectedBots()
		}
		if body.Database != "ok" || body.BotsConnected < 1 {
			body.Status, code = "unavailable", http.StatusServiceUnavailable
		}
		writeJSON(w, code, body)
	})
	return mux
}

type readyBody struct {
	Status        string `json:"status"`
	Database      string `json:"database"`
	BotsConnected int    `json:"bots_connected"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v) // the client may have gone; nothing useful to do
}
