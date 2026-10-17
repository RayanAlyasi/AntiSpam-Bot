package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const bot = wa.JID("966512345678@s.whatsapp.net")

type recSink struct {
	mu   sync.Mutex
	got  []Alert
	fail error
}

func (s *recSink) Name() string { return "rec" }

func (s *recSink) Send(_ context.Context, a Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, a)
	return s.fail
}

func (s *recSink) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k []string
	for _, a := range s.got {
		k = append(k, a.Key)
	}
	return k
}

// drain delivers everything queued so far.
func drain(t *testing.T, n *Notifier) {
	t.Helper()
	for {
		select {
		case a := <-n.queue:
			_ = n.deliver(t.Context(), a)
		default:
			return
		}
	}
}

func TestNotifyDedupesPerKeyWithinWindow(t *testing.T) {
	clk := clock.NewFake(t0)
	sink := &recSink{}
	n := NewNotifier([]Sink{sink}, 10*time.Minute, clk, zerolog.Nop())

	steps := []struct {
		key     string
		advance time.Duration
		want    bool
	}{
		{"a", 0, true},
		{"a", time.Minute, false},     // repeat inside the window
		{"b", 0, true},                // other keys are independent
		{"a", 9 * time.Minute, true},  // window over (10 m since the first "a")
		{"a", 9 * time.Minute, false}, // new window started at the last send
	}
	for i, s := range steps {
		clk.Advance(s.advance)
		if got := n.Notify(Alert{Key: s.key, Title: "t"}); got != s.want {
			t.Errorf("step %d Notify(%s): queued=%v, want %v (one alert per key per window)", i, s.key, got, s.want)
		}
	}
	drain(t, n)
	if got := strings.Join(sink.keys(), ","); got != "a,b,a" {
		t.Errorf("delivered keys %q, want a,b,a", got)
	}
}

func TestNotifyWithoutSinksLogs(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier(nil, 0, clock.NewFake(t0), zerolog.New(&buf))
	if !n.Notify(Alert{Key: "k", Title: "Bot logged out", Severity: Critical, Bot: bot}) {
		t.Fatal("Notify with no sinks: not accepted")
	}
	out := buf.String()
	if !strings.Contains(out, "no alert sink configured") || !strings.Contains(out, "Bot logged out") {
		t.Errorf("log %q: want the alert logged with a hint that no sink is set", out)
	}
	if strings.Contains(out, "966512345678") {
		t.Errorf("log %q contains the full phone number, want it masked", out)
	}
}

func TestDeliverJoinsSinkErrorsAndKeepsGoing(t *testing.T) {
	bad := &recSink{fail: errors.New("down")}
	good := &recSink{}
	n := NewNotifier([]Sink{bad, good}, 0, clock.NewFake(t0), zerolog.Nop())
	err := n.deliver(t.Context(), Alert{Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "down") {
		t.Errorf("deliver error = %v, want the failing sink's error", err)
	}
	if len(good.keys()) != 1 {
		t.Error("a failing sink stopped delivery to the next one")
	}
}

func TestMaskJID(t *testing.T) {
	tests := []struct {
		in   wa.JID
		want string
	}{
		{"966512345678@s.whatsapp.net", "966•••••••78@s.whatsapp.net"},
		{"123456789@lid", "123456789@lid"},
		{"1203630@g.us", "1203630@g.us"},
		{"123@s.whatsapp.net", "123@s.whatsapp.net"},
	}
	for _, tt := range tests {
		if got := MaskJID(tt.in); got != tt.want {
			t.Errorf("MaskJID(%s) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestWebhookAndTelegramSinks(t *testing.T) {
	var mu sync.Mutex
	got := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		got[r.URL.Path] = m
		mu.Unlock()
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()

	a := Alert{Key: "k", Severity: Critical, Title: "Banned", Text: "details", Bot: bot, At: t0}
	if err := (&Webhook{URL: srv.URL + "/hook"}).Send(t.Context(), a); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if err := (&Telegram{Token: "T0K", ChatID: "42", BaseURL: srv.URL}).Send(t.Context(), a); err != nil {
		t.Fatalf("telegram: %v", err)
	}
	if err := (&Webhook{URL: srv.URL + "/fail"}).Send(t.Context(), a); err == nil {
		t.Error("webhook got 502, want an error")
	}
	if err := (&Telegram{Token: "SECRET", ChatID: "1", BaseURL: "http://127.0.0.1:1"}).Send(t.Context(), a); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("unreachable telegram: err = %v, want an error without the bot token", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if h := got["/hook"]; h["key"] != "k" || h["title"] != "Banned" || h["severity"] != "critical" {
		t.Errorf("webhook body %v: want key, title and severity", h)
	}
	if tg := got["/botT0K/sendMessage"]; tg["chat_id"] != "42" || !strings.Contains(tg["text"].(string), "Banned") {
		t.Errorf("telegram body %v: want chat_id 42 and the title in text", tg)
	}
}

func TestEmailSink(t *testing.T) {
	var gotTo []string
	var gotMsg string
	e := &Email{
		Addr: "smtp:25", From: "bot@x", To: []string{"op@x"},
		send: func(_ string, _ smtp.Auth, _ string, to []string, msg []byte) error {
			gotTo, gotMsg = to, string(msg)
			return nil
		},
	}
	if err := e.Send(t.Context(), Alert{Title: "Banned\r\nBcc: evil@x", Text: "line1\nline2"}); err != nil {
		t.Fatal(err)
	}
	if len(gotTo) != 1 || gotTo[0] != "op@x" {
		t.Errorf("to = %v", gotTo)
	}
	if strings.Contains(gotMsg, "\r\nBcc:") {
		t.Errorf("message %q: a newline in the title injected a header", gotMsg)
	}
	if !strings.Contains(gotMsg, "line1\r\nline2") {
		t.Errorf("message %q: body lines must use CRLF", gotMsg)
	}
}

type recBreaker struct {
	trips []string
	until []time.Time
}

func (b *recBreaker) Trip(_ context.Context, bot wa.JID, reason wa.AccountProblem, until time.Time) error {
	b.trips = append(b.trips, string(bot)+"/"+string(reason))
	b.until = append(b.until, until)
	return nil
}

func newHealth(t *testing.T) (*Health, *Notifier, *recSink, *recBreaker, *clock.Fake, *prometheus.Registry) {
	t.Helper()
	clk := clock.NewFake(t0)
	sink := &recSink{}
	n := NewNotifier([]Sink{sink}, 0, clk, zerolog.Nop())
	br := &recBreaker{}
	reg := prometheus.NewRegistry()
	h, err := NewHealth(n, br, reg, clk)
	if err != nil {
		t.Fatal(err)
	}
	return h, n, sink, br, clk, reg
}

func TestHealthConnectionMetrics(t *testing.T) {
	h, _, _, _, _, _ := newHealth(t)
	ctx := t.Context()
	for _, s := range []wa.ConnectionState{wa.Connected, wa.Disconnected, wa.Connected, wa.KeepAliveTimeout, wa.Disconnected, wa.Connected} {
		if err := h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: s}); err != nil {
			t.Fatal(err)
		}
	}
	label := MaskJID(bot)
	if v := testutil.ToFloat64(h.connected.WithLabelValues(label)); v != 1 {
		t.Errorf("wa_connected = %v, want 1", v)
	}
	if v := testutil.ToFloat64(h.reconnects.WithLabelValues(label)); v != 2 {
		t.Errorf("wa_reconnects_total = %v, want 2 (the first connect is not a reconnect)", v)
	}
	if v := testutil.ToFloat64(h.keepalive.WithLabelValues(label)); v != 1 {
		t.Errorf("wa_keepalive_timeouts_total = %v, want 1", v)
	}
	if h.ConnectedBots() != 1 || !h.IsConnected(bot) {
		t.Errorf("ConnectedBots = %d, want 1", h.ConnectedBots())
	}
}

func TestHealthAccountProblemsAlertAndTripBreaker(t *testing.T) {
	tests := []struct {
		evt       wa.AccountEvent
		wantTitle string
		wantUntil time.Time
	}{
		{wa.AccountEvent{Bot: bot, Problem: wa.ProblemTemporaryBan, Code: 101, Expire: time.Hour}, "temporarily banned", t0.Add(time.Hour)},
		{wa.AccountEvent{Bot: bot, Problem: wa.ProblemLoggedOut, Code: 401}, "logged out", time.Time{}},
		{wa.AccountEvent{Bot: bot, Problem: wa.ProblemStreamReplaced}, "replaced", time.Time{}},
	}
	for _, tt := range tests {
		t.Run(string(tt.evt.Problem), func(t *testing.T) {
			h, n, sink, br, _, _ := newHealth(t)
			_ = h.HandleEvent(t.Context(), &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
			if err := h.HandleEvent(t.Context(), &tt.evt); err != nil {
				t.Fatal(err)
			}
			drain(t, n)
			if len(sink.got) != 1 || !strings.Contains(sink.got[0].Title, tt.wantTitle) || sink.got[0].Severity != Critical {
				t.Fatalf("alerts %+v: want one critical alert about %q", sink.got, tt.wantTitle)
			}
			if len(br.trips) != 1 || br.trips[0] != string(bot)+"/"+string(tt.evt.Problem) || !br.until[0].Equal(tt.wantUntil) {
				t.Errorf("breaker trips %v until %v, want one for %s until %v (writes must stop)", br.trips, br.until, tt.evt.Problem, tt.wantUntil)
			}
			if h.IsConnected(bot) {
				t.Error("bot still counted connected after an account problem")
			}
		})
	}
	h, _, _, _, _, _ := newHealth(t)
	_ = h.HandleEvent(t.Context(), &tests[0].evt)
	if v := testutil.ToFloat64(h.tempBans.WithLabelValues("101")); v != 1 {
		t.Errorf("wa_temp_ban_total{code=101} = %v, want 1", v)
	}
}

func TestHealthAlertsLongDisconnectOnce(t *testing.T) {
	h, n, sink, _, clk, _ := newHealth(t)
	ctx := t.Context()
	_ = h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
	_ = h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Disconnected})

	clk.Advance(DisconnectAlertAfter - time.Second)
	h.CheckDisconnected()
	clk.Advance(time.Second)
	h.CheckDisconnected()
	clk.Advance(time.Minute)
	h.CheckDisconnected()
	drain(t, n)
	if len(sink.got) != 1 || sink.got[0].Key != "disconnected:"+string(bot) {
		t.Fatalf("alerts %v: want exactly one disconnect alert after 2 minutes", sink.keys())
	}

	_ = h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
	clk.Advance(time.Hour)
	h.CheckDisconnected()
	drain(t, n)
	if len(sink.got) != 1 {
		t.Errorf("alerts %v: a reconnected bot must not alert", sink.keys())
	}
}

func TestNewHealthRejectsDuplicateRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()
	n := NewNotifier(nil, 0, nil, zerolog.Nop())
	if _, err := NewHealth(n, nil, reg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := NewHealth(n, nil, reg, nil); err == nil {
		t.Error("second registration on one registry: got nil error")
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	n := NewNotifier([]Sink{&recSink{}}, 0, nil, zerolog.Nop())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { done <- n.Run(ctx) }()
	n.Notify(Alert{Key: "x"})
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
}
