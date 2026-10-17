//go:build integration

package pipeline_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pipelinetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	group       wa.JID = "120363000000000001@g.us"
	reviewGroup wa.JID = "120363000000000099@g.us" // separate, unprotected (DESIGN §9.7)
)

var (
	admin       = pipelinetest.Member{LID: "200000000000001@lid", PN: "966500000001@s.whatsapp.net"}
	established = pipelinetest.Member{LID: "200000000000002@lid", PN: "966500000002@s.whatsapp.net"}
	newcomer    = pipelinetest.Member{LID: "200000000000003@lid", PN: "966500000003@s.whatsapp.net"}
)

// knownSpam is confirmed as spam in the group before each scenario.
const knownSpam = "URGENT: earn 500 USD every day from home with our crypto trading signals. " +
	"No experience needed and profit is guaranteed. Message our manager today to join the VIP group " +
	"and receive your welcome bonus before the offer closes tonight."

const normalText = "Hi everyone, the football match moved to Saturday at 5pm, please bring water and your kit."

// writeMethods are the WAClient calls that change something on WhatsApp.
var writeMethods = []string{
	fake.MethodSend, fake.MethodReact, fake.MethodRevoke, fake.MethodEdit,
	fake.MethodUpdateParticipants, fake.MethodUpdateJoinRequests, fake.MethodSetAnnounce,
	fake.MethodSetLocked, fake.MethodSetJoinApproval, fake.MethodResetInviteLink,
}

// setup onboards the protected group with an admin and an established member, lets newcomer
// join now, and confirms knownSpam. The harness's own setup calls are not counted.
func setup(t *testing.T, s pipelinetest.Settings) *pipelinetest.Harness {
	t.Helper()
	if s.ReviewGroup == "" {
		s.ReviewGroup = reviewGroup
	}
	h := pipelinetest.New(t, pipelinetest.Options{})
	h.AddGroup(pipelinetest.Group{
		JID:      group,
		Admins:   []pipelinetest.Member{admin},
		Members:  []pipelinetest.Member{established},
		Settings: s,
	})
	h.Join(group, newcomer)
	h.ConfirmSpam(group, knownSpam)
	return h
}

var msgSeq int

func msgID() wa.MessageID {
	msgSeq++
	return wa.MessageID(fmt.Sprintf("3EB0ACCEPT%06d", msgSeq))
}

func textFrom(m pipelinetest.Member, text string) pipelinetest.Message {
	return pipelinetest.Message{Group: group, Sender: m.LID, SenderPN: m.PN, ID: msgID(), Text: text}
}

// writesSince returns the write calls recorded after the first `from` calls.
func writesSince(c *fake.Client, from int) []fake.Call {
	var out []fake.Call
	for _, call := range c.Calls()[from:] {
		if slices.Contains(writeMethods, call.Method) {
			out = append(out, call)
		}
	}
	return out
}

func describe(calls []fake.Call) string {
	s := ""
	for _, c := range calls {
		s += fmt.Sprintf("\n    %s %v", c.Method, c.Args)
	}
	if s == "" {
		return " (none)"
	}
	return s
}

// revokes counts Revoke calls in chat for message id.
func revokes(c *fake.Client, chat wa.JID, id wa.MessageID) int {
	n := 0
	for _, call := range c.CallsTo(fake.MethodRevoke) {
		if len(call.Args) < 2 {
			continue
		}
		ref, _ := call.Args[1].(wa.MessageRef)
		if call.Args[0] == chat && ref.ID == id {
			n++
		}
	}
	return n
}

// removals counts UpdateParticipants(Remove) calls in g that include m (by LID or PN).
func removals(c *fake.Client, g wa.JID, m pipelinetest.Member) int {
	n := 0
	for _, call := range c.CallsTo(fake.MethodUpdateParticipants) {
		if len(call.Args) < 3 || call.Args[0] != g || call.Args[2] != wa.ParticipantRemove {
			continue
		}
		jids, _ := call.Args[1].([]wa.JID)
		if slices.Contains(jids, m.LID) || (m.PN != "" && slices.Contains(jids, m.PN)) {
			n++
		}
	}
	return n
}

// anyRemovals counts every UpdateParticipants(Remove) call.
func anyRemovals(c *fake.Client) int {
	n := 0
	for _, call := range c.CallsTo(fake.MethodUpdateParticipants) {
		if len(call.Args) >= 3 && call.Args[2] == wa.ParticipantRemove {
			n++
		}
	}
	return n
}

// sendsTo counts Send calls to chat.
func sendsTo(c *fake.Client, chat wa.JID) int {
	n := 0
	for _, call := range c.CallsTo(fake.MethodSend) {
		if len(call.Args) > 0 && call.Args[0] == chat {
			n++
		}
	}
	return n
}

// assertSilent fails if anything was posted in the protected group (DESIGN §9.7).
func assertSilent(t *testing.T, c *fake.Client) {
	t.Helper()
	if n := sendsTo(c, group); n != 0 {
		t.Errorf("got %d Send calls to the protected group, want 0 (the bot is silent in protected groups, DESIGN §9.7)", n)
	}
}

func hasAction(v pipeline.Verdict, k pipeline.ActionKind) bool { return slices.Contains(v.Actions, k) }

// wantCertainKnown checks the verdict's band and source for known spam.
func wantCertainKnown(t *testing.T, v pipeline.Verdict, why string) {
	t.Helper()
	if v.Band != pipeline.BandCertain || v.Source != pipeline.SourceKnown {
		t.Errorf("got band %q source %q, want certain/known (%s)", v.Band, v.Source, why)
	}
}
