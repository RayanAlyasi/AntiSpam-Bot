//go:build integration

package pipeline_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pipelinetest"
)

func TestKnownSpamFromNewMemberDeletedAndBanned(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	c := h.WA()
	msg := textFrom(newcomer, knownSpam)

	v := h.Deliver(msg)

	wantCertainKnown(t, v, "confirmed spam is CERTAIN known evidence, DESIGN §7.0, §20 v0.1")
	if v.Route != pipeline.RouteAutoBan {
		t.Errorf("got route %q, want auto_ban (Standard: known text spam from a new member auto-bans, DESIGN §9.2)", v.Route)
	}
	if n := revokes(c, group, msg.ID); n != 1 {
		t.Errorf("got %d Revoke calls for the spam, want 1 (certain spam is deleted at once, DESIGN §8.3)", n)
	}
	if n := removals(c, group, newcomer); n != 1 {
		t.Errorf("got %d removals of the sender, want 1 (Standard auto-bans a new member for known spam, DESIGN §9.2)", n)
	}
	if !h.Banned(group, newcomer.LID) {
		t.Errorf("got no active ban for the sender, want one (a ban is kick plus rejoin block, DESIGN §9.1)")
	}
	assertSilent(t, c)
}

func TestShadowModeRecordsButNeverActs(t *testing.T) {
	h := setup(t, pipelinetest.Settings{Mode: pipeline.ModeShadow})
	c := h.WA()
	before := len(c.Calls())
	msg := textFrom(newcomer, knownSpam)

	v := h.Deliver(msg)

	if w := writesSince(c, before); len(w) != 0 {
		t.Errorf("got WhatsApp writes in shadow mode:%s\nwant none (shadow mode acts on nothing, DESIGN §8.4)", describe(w))
	}
	if v.Mode != pipeline.ModeShadow {
		t.Errorf("got verdict mode %q, want shadow (would-be actions are recorded as shadow, DESIGN §8.4)", v.Mode)
	}
	wantCertainKnown(t, v, "shadow mode runs the same detection, DESIGN §8.4")
	if !hasAction(v, pipeline.ActionDelete) || !hasAction(v, pipeline.ActionBan) {
		t.Errorf("got planned actions %v, want delete and ban (shadow records what enforce would have done, DESIGN §8.4)", v.Actions)
	}
	d, ok := h.Detection(group, msg.ID)
	if !ok {
		t.Fatalf("got no stored detection, want one with mode shadow (shadow actions must be reviewable, DESIGN §7.0, §8.4)")
	}
	if d.Mode != pipeline.ModeShadow || !hasAction(d, pipeline.ActionDelete) || !hasAction(d, pipeline.ActionBan) {
		t.Errorf("got stored detection mode %q actions %v, want shadow with delete and ban (DESIGN §8.4, §14)", d.Mode, d.Actions)
	}
	if h.Banned(group, newcomer.LID) {
		t.Errorf("got an active ban in shadow mode, want none (shadow acts on nothing, DESIGN §8.4)")
	}
}

func TestKnownSpamFromEstablishedMemberDeletedAndReviewed(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	c := h.WA()
	msg := textFrom(established, knownSpam)

	v := h.Deliver(msg)

	wantCertainKnown(t, v, "no trust bonus: an established member's spam scores the same, DESIGN §7.3, §8.1")
	if v.Route != pipeline.RouteAsk {
		t.Errorf("got route %q, want ask (established members are never auto-banned, DESIGN §8.1, §8.3)", v.Route)
	}
	if n := revokes(c, group, msg.ID); n != 1 {
		t.Errorf("got %d Revoke calls, want 1 (known spam is deleted whoever sent it, DESIGN §8.1, §8.7)", n)
	}
	if n := removals(c, group, established); n != 0 {
		t.Errorf("got %d removals of an established member, want 0 (trust routes the ban to admins, DESIGN §8.1)", n)
	}
	if h.Banned(group, established.LID) {
		t.Errorf("got an active ban for an established member, want none (DESIGN §8.1)")
	}
	if n := sendsTo(c, reviewGroup); n < 1 {
		t.Errorf("got %d review messages, want at least 1 (admins decide the ban, DESIGN §8.3, §9.5)", n)
	}
	assertSilent(t, c)
}

func TestNormalMessageUntouched(t *testing.T) {
	for _, m := range []struct {
		name   string
		sender pipelinetest.Member
	}{{"established", established}, {"new member", newcomer}} {
		t.Run(m.name, func(t *testing.T) {
			h := setup(t, pipelinetest.Settings{})
			c := h.WA()
			before := len(c.Calls())

			v := h.Deliver(textFrom(m.sender, normalText))

			if w := writesSince(c, before); len(w) != 0 {
				t.Errorf("got WhatsApp writes for a normal message:%s\nwant none (not suspicious means untouched, DESIGN §8.3)", describe(w))
			}
			if hasAction(v, pipeline.ActionDelete) || hasAction(v, pipeline.ActionBan) || hasAction(v, pipeline.ActionReview) {
				t.Errorf("got planned actions %v, want none (a normal message is untouched, DESIGN §8.3)", v.Actions)
			}
			if v.Source == pipeline.SourceKnown || v.Band == pipeline.BandCertain {
				t.Errorf("got band %q source %q, want not certain/known (unrelated text must not match the corpus, DESIGN §7.0)", v.Band, v.Source)
			}
		})
	}
}

func TestAskDeleteFirstDeletesAndAsksBan(t *testing.T) {
	h := setup(t, pipelinetest.Settings{Preset: pipelinetest.PresetAskAdmins})
	c := h.WA()
	msg := textFrom(newcomer, knownSpam)

	v := h.Deliver(msg)

	if n := revokes(c, group, msg.ID); n != 1 {
		t.Errorf("got %d Revoke calls, want 1 (the Ask admins preset is ask_delete_first: spam is deleted at once, DESIGN §8.3, §12.2)", n)
	}
	if n := anyRemovals(c); n != 0 {
		t.Errorf("got %d removals, want 0 (ask-admins mode never auto-bans, DESIGN §8.3, §18)", n)
	}
	if h.Banned(group, newcomer.LID) {
		t.Errorf("got an active ban, want none (ask-admins mode never auto-bans, DESIGN §8.3)")
	}
	if n := sendsTo(c, reviewGroup); n < 1 {
		t.Errorf("got %d review messages, want at least 1 (the ban goes to admins, DESIGN §8.3)", n)
	}
	if v.Route != pipeline.RouteAsk || hasAction(v, pipeline.ActionBan) {
		t.Errorf("got route %q actions %v, want ask without ban (ask-admins mode never auto-bans, DESIGN §8.3)", v.Route, v.Actions)
	}
	assertSilent(t, c)
}

func TestAskBeforeActionOnlyReviews(t *testing.T) {
	h := setup(t, pipelinetest.Settings{
		Preset:      pipelinetest.PresetAskAdmins,
		Enforcement: pipelinetest.EnforcementAskBeforeAction,
	})
	c := h.WA()
	msg := textFrom(newcomer, knownSpam)

	v := h.Deliver(msg)

	if n := revokes(c, group, msg.ID); n != 0 {
		t.Errorf("got %d Revoke calls, want 0 (ask_before_action deletes nothing until an admin decides, DESIGN §8.3)", n)
	}
	if n := anyRemovals(c); n != 0 {
		t.Errorf("got %d removals, want 0 (ask-admins mode never auto-bans, DESIGN §8.3, §18)", n)
	}
	if n := sendsTo(c, reviewGroup); n < 1 {
		t.Errorf("got %d review messages, want at least 1 (admins decide delete and ban, DESIGN §8.3)", n)
	}
	if hasAction(v, pipeline.ActionDelete) || hasAction(v, pipeline.ActionBan) || !hasAction(v, pipeline.ActionReview) {
		t.Errorf("got planned actions %v, want review only (ask_before_action, DESIGN §8.3)", v.Actions)
	}
	assertSilent(t, c)
}

func TestPresetRoutesKnownTextSpamFromNewMember(t *testing.T) {
	tests := []struct {
		preset              pipelinetest.Preset
		wantDelete, wantBan bool
		wantReview          bool
		why                 string
	}{
		{pipelinetest.PresetRelaxed, true, false, false, "Relaxed text cell is delete_only, DESIGN §9.2"},
		{pipelinetest.PresetStandard, true, true, false, "Standard text cell is auto_ban [known], DESIGN §9.2"},
		{pipelinetest.PresetStrict, true, true, false, "Strict text cell is auto_ban [known], DESIGN §9.2"},
		{pipelinetest.PresetAskAdmins, true, false, true, "Ask admins is ask_delete_first, DESIGN §8.3, §12.2"},
	}
	for _, tt := range tests {
		t.Run(string(tt.preset), func(t *testing.T) {
			h := setup(t, pipelinetest.Settings{Preset: tt.preset})
			c := h.WA()
			msg := textFrom(newcomer, knownSpam)

			h.Deliver(msg)

			if got := revokes(c, group, msg.ID) == 1; got != tt.wantDelete {
				t.Errorf("got deleted=%v, want %v (%s)", got, tt.wantDelete, tt.why)
			}
			if got := removals(c, group, newcomer) == 1; got != tt.wantBan {
				t.Errorf("got banned=%v, want %v (%s)", got, tt.wantBan, tt.why)
			}
			if tt.wantReview && sendsTo(c, reviewGroup) < 1 {
				t.Errorf("got no review message, want one (%s)", tt.why)
			}
			assertSilent(t, c)
		})
	}
}

func TestBlacklistedVCardFromNewMemberDeletedAndBanned(t *testing.T) {
	const listed = "+966551234567"
	tests := []struct {
		name  string
		vcard string
		spam  bool
	}{
		{"waid", "BEGIN:VCARD\nVERSION:3.0\nFN:Investment Manager\nTEL;type=CELL;waid=966551234567:+966 55 123 4567\nEND:VCARD", true},
		{"tel only", "BEGIN:VCARD\nVERSION:3.0\nFN:Investment Manager\nTEL;type=CELL:+966 55 123 4567\nEND:VCARD", true},
		{"unlisted number", "BEGIN:VCARD\nVERSION:3.0\nFN:Plumber Ahmed\nTEL;type=CELL;waid=966557654321:+966 55 765 4321\nEND:VCARD", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := setup(t, pipelinetest.Settings{})
			h.Blacklist(group, listed)
			c := h.WA()
			msg := pipelinetest.Message{Group: group, Sender: newcomer.LID, SenderPN: newcomer.PN, ID: msgID(), VCards: []string{tt.vcard}}

			v := h.Deliver(msg)

			if !tt.spam {
				if revokes(c, group, msg.ID) != 0 || removals(c, group, newcomer) != 0 {
					t.Errorf("got a delete or ban for a contact card with an unlisted number, want neither (only the listed number is known spam, DESIGN §9.8)")
				}
				return
			}
			wantCertainKnown(t, v, "a blacklisted number in a vCard is known spam, DESIGN §7.3, §9.8")
			if n := revokes(c, group, msg.ID); n != 1 {
				t.Errorf("got %d Revoke calls, want 1 (a blacklisted contact card is deleted, DESIGN §9.8)", n)
			}
			if n := removals(c, group, newcomer); n != 1 {
				t.Errorf("got %d removals of the new sender, want 1 (Standard contacts cell auto-bans on known evidence, DESIGN §9.2, §9.8)", n)
			}
			assertSilent(t, c)
		})
	}
}

func TestAdminsAndBotNeverActedOn(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	c := h.WA()
	for _, sender := range []pipelinetest.Message{
		textFrom(admin, knownSpam),
		{Group: group, Sender: h.Bot(), ID: msgID(), Text: knownSpam},
	} {
		v := h.Deliver(sender)
		if n := revokes(c, group, sender.ID); n != 0 {
			t.Errorf("sender %s: got %d Revoke calls, want 0 (never act on admins or the bot, DESIGN §8.6)", sender.Sender, n)
		}
		if hasAction(v, pipeline.ActionDelete) || hasAction(v, pipeline.ActionBan) {
			t.Errorf("sender %s: got planned actions %v, want no delete or ban (DESIGN §8.3, §8.6)", sender.Sender, v.Actions)
		}
	}
	if n := anyRemovals(c); n != 0 {
		t.Errorf("got %d removals, want 0 (never act on admins or the bot, DESIGN §8.6)", n)
	}
	assertSilent(t, c)
}

func TestPausedModeDoesNothing(t *testing.T) {
	h := setup(t, pipelinetest.Settings{Mode: pipeline.ModePaused})
	c := h.WA()
	before := len(c.Calls())

	h.Deliver(textFrom(newcomer, knownSpam))

	if w := writesSince(c, before); len(w) != 0 {
		t.Errorf("got WhatsApp writes in a paused group:%s\nwant none (paused means the bot does nothing there, DESIGN §12.1)", describe(w))
	}
}

func TestDuplicateDeliveryIsIdempotent(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	c := h.WA()
	msg := textFrom(newcomer, knownSpam)

	h.Deliver(msg)
	h.Deliver(msg)

	if n := revokes(c, group, msg.ID); n != 1 {
		t.Errorf("got %d Revoke calls after a redelivery, want 1 (inbox events are processed once, DESIGN §5.1, §10.4)", n)
	}
	if n := removals(c, group, newcomer); n != 1 {
		t.Errorf("got %d removals after a redelivery, want 1 (actions are deduplicated, DESIGN §10.4)", n)
	}
}
