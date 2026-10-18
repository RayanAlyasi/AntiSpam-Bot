//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/config"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/identity"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/inbox"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/logging"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/store/storetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/inboxenv"
)

// TestChatSinkSwitchesGroupToShadow is the first step of the README's shadow test: an admin of a
// group the bot was just added to (not admin yet) DMs the bot `.use <group>` and `.mode shadow`,
// and the inbox's DM route stores mode shadow for that group.
func TestChatSinkSwitchesGroupToShadow(t *testing.T) {
	ctx := context.Background()
	url := storetest.URL(t, true)
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const (
		bot   wa.JID = "100@lid"
		group wa.JID = "120363000000000001@g.us"
		admin wa.JID = "200@lid"
	)
	if err := store.NewBots(pool).UpsertBot(ctx, bot, bot, "bot", "active"); err != nil {
		t.Fatal(err)
	}
	var connected atomic.Int64
	logs := logging.New(io.Discard, zerolog.InfoLevel, nil)
	ws, lids := newWAStack(pool, func(string) string { return "" }, logs, &connected)
	if err := ws.groups.UpsertGroup(ctx, identity.Group{JID: group, Bot: bot, Name: "Test group", Status: identity.GroupPendingAdmin, JoinedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	p, err := ws.ids.Ensure(ctx, admin, identity.SourceParticipant)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.ids.SeedMember(ctx, group, p.ID, identity.RoleAdmin, true); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	fl, err := newFastlane(cfg, []byte("0123456789abcdef0123456789abcdef"), pool, func(string) string { return "" }, logs, ws, lids, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	jidOf := func(s wa.JID) types.JID { j, _ := types.ParseJID(string(s)); return j }
	dm := func(id, text string) {
		t.Helper()
		info := types.MessageInfo{MessageSource: types.MessageSource{Chat: jidOf(admin), Sender: jidOf(admin)}, ID: types.MessageID(id), Timestamp: time.Now()}
		raw, _ := proto.Marshal(&waE2E.Message{Conversation: proto.String(text)})
		infoJSON, _ := json.Marshal(info)
		mi, err := inbox.DecodeInfo(infoJSON)
		if err != nil {
			t.Fatal(err)
		}
		it := inbox.Item{Bot: bot, Chat: admin, MsgID: wa.MessageID(id), Kind: inboxenv.KindMessage, Sender: admin, Raw: raw, Info: infoJSON}
		if err := fl.chat.Handle(ctx, it, mi, inbox.Decision{Route: inbox.RouteDM}); err != nil {
			t.Fatalf("Handle %q: %v", text, err)
		}
	}
	dm("D1", ".use Test group")
	dm("D2", ".mode shadow")
	s, _, err := policy.NewSettingsService(store.NewPolicyStore(pool)).Get(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != policy.ModeShadow {
		t.Fatalf("mode after `.mode shadow` = %q, want shadow (the README's first step)", s.Mode)
	}
}
