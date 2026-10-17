package alert

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

func TestAlertLogsMaskTheBotNumber(t *testing.T) {
	var buf bytes.Buffer
	n := NewNotifier([]Sink{&recSink{}}, 0, clock.NewFake(t0), zerolog.New(&buf))
	n.Notify(Alert{Key: "temporary_ban:" + string(bot), Title: "Banned", Bot: bot})
	if strings.Contains(buf.String(), "966512345678") {
		t.Errorf("log %q contains the full bot number, want it masked everywhere (GO-003)", buf.String())
	}
	if !strings.Contains(buf.String(), "temporary_ban:966") {
		t.Errorf("log %q: want the masked key", buf.String())
	}
}

func TestDroppedAlertCanBeRetried(t *testing.T) {
	n := NewNotifier([]Sink{&recSink{}}, 0, clock.NewFake(t0), zerolog.Nop())
	for i := range cap(n.queue) {
		n.Notify(Alert{Key: fmt.Sprint("fill", i)})
	}
	if n.Notify(Alert{Key: "k"}) {
		t.Fatal("Notify with a full queue: queued=true")
	}
	drain(t, n)
	if !n.Notify(Alert{Key: "k"}) {
		t.Error("an alert dropped for a full queue stayed deduplicated, want it retried on the next Notify")
	}
}

func TestHealthReleasedIsNotAnOutage(t *testing.T) {
	h, n, sink, _, clk, _ := newHealth(t)
	ctx := t.Context()
	_ = h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Connected})
	_ = h.HandleEvent(ctx, &wa.ConnectionEvent{Bot: bot, State: wa.Released})
	clk.Advance(time.Hour)
	h.CheckDisconnected()
	drain(t, n)
	if len(sink.got) != 0 || h.ConnectedBots() != 0 {
		t.Errorf("alerts %v, connected %d: a released bot (another process owns it now) counts as not connected here and doesn't alert", sink.keys(), h.ConnectedBots())
	}
	if v := testutil.ToFloat64(h.connected.WithLabelValues(MaskJID(bot))); v != 0 {
		t.Errorf("wa_connected = %v after release, want 0", v)
	}
}

func TestAccountProblemDoesNotAlsoAlertDisconnected(t *testing.T) {
	h, n, sink, _, clk, _ := newHealth(t)
	_ = h.HandleEvent(t.Context(), &wa.AccountEvent{Bot: bot, Problem: wa.ProblemTemporaryBan, Code: 101})
	clk.Advance(time.Hour)
	h.CheckDisconnected()
	drain(t, n)
	if len(sink.got) != 1 {
		t.Errorf("alerts %v, want only the ban alert (it already covers the outage)", sink.keys())
	}
}
