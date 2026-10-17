package alert

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Breaker is the action executor's circuit breaker for one bot (DESIGN.md §10.5). Trip stops that
// bot's writes (except what the executor still allows, such as P0 lockdowns) until the given time;
// a zero time means until an operator resets it.
type Breaker interface {
	Trip(ctx context.Context, bot wa.JID, reason wa.AccountProblem, until time.Time) error
}

// DisconnectAlertAfter is how long a bot may stay disconnected before the operator is alerted
// (DESIGN.md §16: "bot disconnected > 2 min").
const DisconnectAlertAfter = 2 * time.Minute

// Health tracks each bot's connection from connector events: it keeps wa_connected and the
// reconnect, keepalive and ban metrics, alerts the operator on bans, logouts, replaced streams
// and long disconnects, and trips the breaker for the first three.
type Health struct {
	notifier *Notifier
	breaker  Breaker
	clk      clock.Clock

	connected  *prometheus.GaugeVec
	reconnects *prometheus.CounterVec
	keepalive  *prometheus.CounterVec
	tempBans   *prometheus.CounterVec
	problems   *prometheus.CounterVec

	mu   sync.Mutex
	bots map[wa.JID]*botState
}

type botState struct {
	connected bool
	everUp    bool
	downSince time.Time // zero while connected
}

// NewHealth registers the connection metrics on reg and returns the tracker. breaker may be nil
// until the action executor exists; then trips are only alerted.
func NewHealth(n *Notifier, breaker Breaker, reg prometheus.Registerer, clk clock.Clock) (*Health, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	h := &Health{
		notifier: n,
		breaker:  breaker,
		clk:      clk,
		bots:     make(map[wa.JID]*botState),
		connected: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "wa_connected", Help: "1 while the bot number is connected to WhatsApp.",
		}, []string{"bot"}),
		reconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wa_reconnects_total", Help: "Connections re-established after a disconnect.",
		}, []string{"bot"}),
		keepalive: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wa_keepalive_timeouts_total", Help: "Keepalive pings that timed out.",
		}, []string{"bot"}),
		tempBans: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wa_temp_ban_total", Help: "Temporary bans received, by reason code.",
		}, []string{"code"}),
		problems: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wa_account_problems_total", Help: "Temporary bans, logouts and replaced streams.",
		}, []string{"bot", "problem"}),
	}
	if reg != nil {
		for _, c := range []prometheus.Collector{h.connected, h.reconnects, h.keepalive, h.tempBans, h.problems} {
			if err := reg.Register(c); err != nil {
				return nil, fmt.Errorf("register health metrics: %w", err)
			}
		}
	}
	return h, nil
}

// HandleEvent applies connection and account events; other events are ignored. It never blocks
// on the network, so the connector may call it on the receive loop.
func (h *Health) HandleEvent(ctx context.Context, evt wa.Event) error {
	switch e := evt.(type) {
	case *wa.ConnectionEvent:
		h.connection(e)
	case *wa.AccountEvent:
		return h.account(ctx, e)
	}
	return nil
}

// ConnectedBots is how many bots are connected right now (for /readyz).
func (h *Health) ConnectedBots() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, b := range h.bots {
		if b.connected {
			n++
		}
	}
	return n
}

// IsConnected reports whether one bot is connected.
func (h *Health) IsConnected(bot wa.JID) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.bots[bot]
	return b != nil && b.connected
}

// Run alerts on bots that stay disconnected longer than DisconnectAlertAfter, until ctx ends.
func (h *Health) Run(ctx context.Context) error {
	for {
		h.CheckDisconnected()
		select {
		case <-ctx.Done():
			return nil
		case <-h.clk.After(15 * time.Second):
		}
	}
}

// CheckDisconnected alerts once per dedupe window for every bot disconnected too long.
func (h *Health) CheckDisconnected() {
	now := h.clk.Now()
	h.mu.Lock()
	var down []wa.JID
	for jid, b := range h.bots {
		if !b.connected && !b.downSince.IsZero() && now.Sub(b.downSince) >= DisconnectAlertAfter {
			down = append(down, jid)
		}
	}
	h.mu.Unlock()
	for _, bot := range down {
		h.notifier.Notify(Alert{
			Key: "disconnected:" + string(bot), Severity: Critical, Bot: bot,
			Title: "Bot disconnected for more than 2 minutes",
			Text:  "Bot " + MaskJID(bot) + " has not reconnected to WhatsApp.",
		})
	}
}

func (h *Health) state(bot wa.JID) *botState {
	b := h.bots[bot]
	if b == nil {
		b = &botState{}
		h.bots[bot] = b
	}
	return b
}

func (h *Health) connection(e *wa.ConnectionEvent) {
	label := MaskJID(e.Bot)
	h.mu.Lock()
	b := h.state(e.Bot)
	switch e.State {
	case wa.Connected:
		if b.everUp && !b.connected {
			h.reconnects.WithLabelValues(label).Inc()
		}
		b.connected, b.everUp, b.downSince = true, true, time.Time{}
		h.connected.WithLabelValues(label).Set(1)
	case wa.Disconnected:
		if b.connected || b.downSince.IsZero() {
			b.downSince = h.clk.Now()
		}
		b.connected = false
		h.connected.WithLabelValues(label).Set(0)
	case wa.Released:
		delete(h.bots, e.Bot)
		h.connected.WithLabelValues(label).Set(0)
	case wa.KeepAliveTimeout:
		h.keepalive.WithLabelValues(label).Inc()
	case wa.KeepAliveRestored:
	}
	h.mu.Unlock()
}

func (h *Health) account(ctx context.Context, e *wa.AccountEvent) error {
	h.problems.WithLabelValues(MaskJID(e.Bot), string(e.Problem)).Inc()
	h.mu.Lock()
	b := h.state(e.Bot)
	// The problem's own alert covers this outage, so no "disconnected" alert follows it.
	b.connected, b.downSince = false, time.Time{}
	h.connected.WithLabelValues(MaskJID(e.Bot)).Set(0)
	h.mu.Unlock()

	var until time.Time
	a := Alert{Key: string(e.Problem) + ":" + string(e.Bot), Severity: Critical, Bot: e.Bot}
	switch e.Problem {
	case wa.ProblemTemporaryBan:
		h.tempBans.WithLabelValues(strconv.Itoa(e.Code)).Inc()
		if e.Expire > 0 {
			until = h.clk.Now().Add(e.Expire)
		}
		a.Title = "Bot temporarily banned by WhatsApp"
		a.Text = fmt.Sprintf("Bot %s: temporary ban, code %d, expires in %s. All writes are paused; the bot keeps reading.",
			MaskJID(e.Bot), e.Code, e.Expire.Round(time.Second))
	case wa.ProblemLoggedOut:
		a.Title = "Bot logged out"
		a.Text = fmt.Sprintf("Bot %s was unpaired (reason %d). Pair it again with `antispam pair`.", MaskJID(e.Bot), e.Code)
	case wa.ProblemStreamReplaced:
		a.Title = "Bot session replaced by another connection"
		a.Text = fmt.Sprintf("Bot %s: another process connected with the same session. Only one process may hold a bot's lease.", MaskJID(e.Bot))
	default:
		a.Title = "Bot account problem: " + string(e.Problem)
	}
	h.notifier.Notify(a)
	if h.breaker == nil {
		return nil
	}
	if err := h.breaker.Trip(ctx, e.Bot, e.Problem, until); err != nil {
		return fmt.Errorf("trip breaker for %s: %w", e.Problem, err)
	}
	return nil
}
