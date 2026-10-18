//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/actions"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pgstore"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/db"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
)

func TestReplayShadowReport(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	s := pgstore.New(pool, func(context.Context, db.DBTX, actions.Action) error { return nil }, nil)
	for _, d := range []policy.DetectionRecord{
		{Group: "1@g.us", MsgID: "S1", Score: 0.99, TierReached: "t1", Verdict: policy.VerdictSpam, Source: policy.SourceKnown,
			Route: policy.RouteAutoBan, RuleVersion: "v", Mode: policy.DetectionShadow,
			Signals:    []policy.Signal{{Code: "corpus.near_duplicate", Weight: 4, Known: true}},
			ActionPlan: []policy.Action{{Kind: policy.ActionDelete}, {Kind: policy.ActionBan}}},
		{Group: "1@g.us", MsgID: "E1", Score: 0.99, TierReached: "t1", Verdict: policy.VerdictSpam, Source: policy.SourceKnown,
			Route: policy.RouteAutoBan, RuleVersion: "v", Mode: policy.DetectionEnforce,
			ActionPlan: []policy.Action{{Kind: policy.ActionDelete}}},
	} {
		if err := s.InTx(ctx, func(ctx context.Context, tx pipeline.Tx) error {
			_, err := tx.InsertDetection(ctx, d)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var out, log bytes.Buffer
	env := map[string]string{"DATABASE_URL": url}
	if code := replayCmd(ctx, []string{"-shadow", "-since", "1h"}, func(k string) string { return env[k] }, &out, &log); code != 0 {
		t.Fatalf("exit %d; log: %s", code, log.String())
	}
	got := out.String()
	if !strings.Contains(got, "1@g.us S1 route=auto_ban source=known would=delete+ban") || !strings.Contains(got, "why=corpus.near_duplicate") {
		t.Fatalf("report lacks the shadow detection:\n%s", got)
	}
	if strings.Contains(got, " E1 ") {
		t.Fatalf("report lists an enforce detection; only shadow ones belong there:\n%s", got)
	}
	if !strings.Contains(got, "would have deleted 1 messages, banned 1 people") {
		t.Fatalf("summary wrong:\n%s", got)
	}
}

// TestReplayInbox re-runs stored inbox rows through today's corpus, read only: a resend of a
// template an admin confirmed after it arrived shows as certain known spam, a normal message as
// clean, and nothing is recorded.
func TestReplayInbox(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const (
		bot   wa.JID = "966500000009@s.whatsapp.net"
		group wa.JID = "120363000000000001@g.us"
	)
	key := "0123456789abcdef0123456789abcdef"
	if err := store.NewBots(pool).UpsertBot(ctx, bot, "100@lid", "bot", "active"); err != nil {
		t.Fatal(err)
	}
	template := "Congratulations you have been selected for our exclusive crypto investment program earn five hundred dollars every single day"
	writer := inbox.NewWriter(pool, clock.Real{})
	for id, text := range map[string]string{"I1": template, "I2": "see you all at the school gate at five"} {
		evt, err := synth.Event(synth.Spec{Chat: group, Sender: "300@lid", ID: wa.MessageID(id), Time: time.Now(), Text: text})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := proto.Marshal(evt.RawMessage)
		info, _ := json.Marshal(evt.Info)
		if err := writer.HandleEvent(ctx, &wa.MessageEvent{Bot: bot, Info: wa.MessageInfo{Chat: group, Sender: "300@lid", ID: wa.MessageID(id)}, Raw: raw, InfoJSON: info}); err != nil {
			t.Fatal(err)
		}
	}
	hasher, _ := blacklist.NewHasher([]byte(key))
	learn := &fastlane.CorpusLearning{Pool: pool, Clock: clock.Real{}, Hasher: hasher, Key: []byte(key), Log: zerolog.Nop()}
	if _, err := learn.Confirm(ctx, review.Confirmation{Group: group, Message: wa.MessageRef{ID: "OTHER"}, Text: template, Source: "review"}); err != nil {
		t.Fatal(err)
	}

	var out, log bytes.Buffer
	env := map[string]string{"DATABASE_URL": url, "HASH_KEY": key}
	if code := replayCmd(ctx, []string{"-inbox", "-since", "1h"}, func(k string) string { return env[k] }, &out, &log); code != 0 {
		t.Fatalf("exit %d; log: %s", code, log.String())
	}
	got := out.String()
	if !strings.Contains(got, string(group)+" I1 mode=enforce band=certain source=known route=auto_ban") || !strings.Contains(got, string(group)+" I2 mode=enforce band=clean") {
		t.Fatalf("inbox replay output:\n%s\nwant I1 certain known and I2 clean", got)
	}
	if rows, _ := store.NewPolicyStore(pool).ListDetections(ctx, group, 0, 10); len(rows) != 0 {
		t.Fatalf("the replay recorded %d detections; it must only read", len(rows))
	}
}
