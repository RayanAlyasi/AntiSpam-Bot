// Package alert sends operator alerts (DESIGN.md §16) to the configured webhook, Telegram and
// email sinks, deduplicated per alert key, and turns connection events into health metrics,
// alerts and breaker signals (DESIGN.md §5.1, §10.5).
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Severity orders alerts.
type Severity string

// Severities.
const (
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// Alert is one operator alert.
type Alert struct {
	// Key identifies the condition (e.g. "temp_ban:<bot>"). Alerts with the same key within the
	// dedupe window are sent once.
	Key      string
	Severity Severity
	Title    string
	Text     string
	Bot      wa.JID
	At       time.Time
}

// Sink delivers alerts to one destination.
type Sink interface {
	Name() string
	Send(ctx context.Context, a Alert) error
}

// DefaultDedupeWindow is how long a repeated alert key stays silent.
const DefaultDedupeWindow = 30 * time.Minute

// Notifier fans alerts out to its sinks. Notify never blocks the caller: alerts are queued and
// delivered by Run, so it is safe to call from the connector's receive loop.
type Notifier struct {
	sinks  []Sink
	clk    clock.Clock
	log    zerolog.Logger
	window time.Duration
	queue  chan Alert

	mu   sync.Mutex
	sent map[string]time.Time
}

// NewNotifier builds a notifier. With no sinks, alerts are only logged. A non-positive window
// means DefaultDedupeWindow.
func NewNotifier(sinks []Sink, window time.Duration, clk clock.Clock, log zerolog.Logger) *Notifier {
	if window <= 0 {
		window = DefaultDedupeWindow
	}
	if clk == nil {
		clk = clock.Real{}
	}
	return &Notifier{
		sinks:  sinks,
		clk:    clk,
		log:    log.With().Str("module", "alert").Logger(),
		window: window,
		queue:  make(chan Alert, 256),
		sent:   make(map[string]time.Time),
	}
}

// Notify queues an alert unless the same key was alerted within the dedupe window. It reports
// whether the alert was queued.
func (n *Notifier) Notify(a Alert) bool {
	now := n.clk.Now()
	if a.At.IsZero() {
		a.At = now
	}
	n.mu.Lock()
	if last, ok := n.sent[a.Key]; ok && now.Sub(last) < n.window {
		n.mu.Unlock()
		return false
	}
	n.sent[a.Key] = now
	for k, at := range n.sent { // keep the map small: forget keys whose window is over
		if now.Sub(at) >= n.window {
			delete(n.sent, k)
		}
	}
	n.mu.Unlock()

	n.logAlert(a)
	if len(n.sinks) == 0 {
		return true
	}
	select {
	case n.queue <- a:
		return true
	default:
		n.log.Error().Str("key", a.logKey()).Msg("alert queue full; alert dropped")
		n.mu.Lock()
		delete(n.sent, a.Key) // not sent, so a repeat may try again
		n.mu.Unlock()
		return false
	}
}

// Run delivers queued alerts until ctx ends.
func (n *Notifier) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case a := <-n.queue:
			if err := n.deliver(ctx, a); err != nil {
				n.log.Error().Err(err).Str("key", a.logKey()).Msg("deliver alert")
			}
		}
	}
}

// deliver sends one alert to every sink, each with its own timeout.
func (n *Notifier) deliver(ctx context.Context, a Alert) error {
	var errs []error
	for _, s := range n.sinks {
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := s.Send(sctx, a); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
		cancel()
	}
	return errors.Join(errs...)
}

func (n *Notifier) logAlert(a Alert) {
	ev := n.log.Warn()
	if a.Severity == Critical {
		ev = n.log.Error()
	}
	ev = ev.Str("key", a.logKey()).Str("title", a.Title)
	if a.Bot != "" {
		ev = ev.Str("bot", MaskJID(a.Bot))
	}
	if len(n.sinks) == 0 {
		ev.Msg("operator alert (no alert sink configured)")
		return
	}
	ev.Msg("operator alert")
}

// MaskJID hides the middle of a phone-number JID's digits for logs (personal data, GO-003).
func MaskJID(j wa.JID) string {
	u := j.User()
	if j.IsLID() || j.IsGroup() || len(u) < 6 {
		return string(j)
	}
	return u[:3] + strings.Repeat("•", len(u)-5) + u[len(u)-2:] + "@" + j.Server()
}

// logKey is the alert key with the bot's number masked, for logs (keys embed the bot JID).
func (a Alert) logKey() string {
	if a.Bot == "" {
		return a.Key
	}
	return strings.ReplaceAll(a.Key, string(a.Bot), MaskJID(a.Bot))
}

func (a Alert) message() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s", strings.ToUpper(string(a.Severity)), oneLine(a.Title))
	if a.Text != "" {
		b.WriteString("\n")
		b.WriteString(a.Text)
	}
	return b.String()
}

// oneLine flattens line breaks, so a title can't inject mail headers or fake lines.
func oneLine(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

// Webhook posts each alert as JSON to a URL.
type Webhook struct {
	URL    string
	Client *http.Client
}

// Name implements Sink.
func (w *Webhook) Name() string { return "webhook" }

// Send implements Sink.
func (w *Webhook) Send(ctx context.Context, a Alert) error {
	body, err := json.Marshal(map[string]any{
		"key": a.Key, "severity": a.Severity, "title": a.Title, "text": a.Text,
		"bot": string(a.Bot), "at": a.At.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("encode alert: %w", err)
	}
	return post(ctx, w.Client, w.URL, body)
}

// Telegram sends each alert through the Telegram Bot API.
type Telegram struct {
	Token  string
	ChatID string
	// BaseURL defaults to https://api.telegram.org; tests point it at a local server.
	BaseURL string
	Client  *http.Client
}

// Name implements Sink.
func (t *Telegram) Name() string { return "telegram" }

// Send implements Sink.
func (t *Telegram) Send(ctx context.Context, a Alert) error {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	body, err := json.Marshal(map[string]string{"chat_id": t.ChatID, "text": a.message()})
	if err != nil {
		return fmt.Errorf("encode alert: %w", err)
	}
	return post(ctx, t.Client, base+"/bot"+t.Token+"/sendMessage", body)
}

func post(ctx context.Context, client *http.Client, url string, body []byte) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// The URL may carry a token (Telegram), so don't echo the client error verbatim.
		return fmt.Errorf("post alert: %w", redactURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("post alert: status %d", resp.StatusCode)
	}
	return nil
}

// redactURL drops the URL from a *url.Error, keeping the operation and cause.
func redactURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// Email sends each alert by SMTP.
type Email struct {
	Addr string // host:port
	From string
	To   []string
	Auth smtp.Auth // nil for an unauthenticated relay
	// send defaults to smtp.SendMail; tests replace it.
	send func(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// Name implements Sink.
func (e *Email) Name() string { return "email" }

// Send implements Sink. net/smtp has no context support, so a hung server is bounded only by the
// TCP timeouts; alerts are delivered off the hot path, so that's acceptable.
func (e *Email) Send(_ context.Context, a Alert) error {
	send := e.send
	if send == nil {
		send = smtp.SendMail
	}
	subject := oneLine(a.Title)
	msg := "From: " + e.From + "\r\nTo: " + strings.Join(e.To, ", ") +
		"\r\nSubject: [antispam] " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(a.message(), "\n", "\r\n") + "\r\n"
	if err := send(e.Addr, e.Auth, e.From, e.To, []byte(msg)); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}
