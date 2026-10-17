// Package pipelinetest builds the whole bot around a fresh Postgres (testcontainers) and the
// fake WAClient with a fake clock, so tests can deliver messages and observe what reaches
// WhatsApp (DESIGN.md §18).
package pipelinetest

import (
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

// Stub for F-088: declares only the exported names the acceptance tests use
// (.agent/contracts/F-088.md). The builder replaces this file.

// Preset is a group's settings preset (DESIGN.md §12.2).
type Preset string

// Presets.
const (
	PresetRelaxed   Preset = "relaxed"
	PresetStandard  Preset = "standard"
	PresetStrict    Preset = "strict"
	PresetAskAdmins Preset = "ask_admins"
)

// Enforcement is enforcement.mode (DESIGN.md §8.3). Empty means the preset's own mode.
type Enforcement string

// Enforcement modes.
const (
	EnforcementPreset          Enforcement = ""
	EnforcementAuto            Enforcement = "auto"
	EnforcementAskDeleteFirst  Enforcement = "ask_delete_first"
	EnforcementAskBeforeAction Enforcement = "ask_before_action"
)

// Settings are the group settings a test controls. Zero values mean enforce, standard and the
// preset's enforcement mode.
type Settings struct {
	Mode        pipeline.Mode
	Preset      Preset
	Enforcement Enforcement
	ReviewGroup wa.JID
}

// Member is a person in a group: LID always, phone number when known.
type Member struct {
	LID wa.JID
	PN  wa.JID
}

// Group is a protected group as the bot finds it at onboarding.
type Group struct {
	JID      wa.JID
	Admins   []Member
	Members  []Member // pre-existing at onboarding, so established (DESIGN.md §8.1)
	Settings Settings
}

// Message is a whatsmeow-free description of an incoming group message.
type Message struct {
	Group    wa.JID
	Sender   wa.JID // LID
	SenderPN wa.JID // optional
	ID       wa.MessageID
	Time     time.Time // zero means the fake clock's now
	Text     string
	VCards   []string // raw vCard documents shared as contacts
}

// Options configure New. Zero values get defaults.
type Options struct {
	Start time.Time
	Bot   wa.JID
}

// Harness is the bot under test.
type Harness struct{}

// New builds the bot on a fresh, isolated database. It fails t if Postgres can't start.
func New(t testing.TB, opts Options) *Harness { panic("not implemented") }

// WA returns the fake WhatsApp client.
func (h *Harness) WA() *fake.Client { panic("not implemented") }

// Clock returns the fake clock.
func (h *Harness) Clock() *clock.Fake { panic("not implemented") }

// Bot returns the bot's own JID.
func (h *Harness) Bot() wa.JID { panic("not implemented") }

// AddGroup onboards a protected group, with the bot as an admin.
func (h *Harness) AddGroup(g Group) { panic("not implemented") }

// Join records that m joined group now, which makes m a new member.
func (h *Harness) Join(group wa.JID, m Member) { panic("not implemented") }

// ConfirmSpam stores text as admin-confirmed spam in group's corpus (DESIGN.md §20 v0.1).
func (h *Harness) ConfirmSpam(group wa.JID, text string) { panic("not implemented") }

// Overturn records an admin "not spam" decision on text in group (DESIGN.md §7.5).
func (h *Harness) Overturn(group wa.JID, text string) { panic("not implemented") }

// Blacklist adds a phone number to group's manual blacklist (DESIGN.md §9.8).
func (h *Harness) Blacklist(group wa.JID, number string) { panic("not implemented") }

// Deliver runs msg through the bot, drains the due actions and returns the verdict.
func (h *Harness) Deliver(msg Message) pipeline.Verdict { panic("not implemented") }

// Detection returns the stored detection for a message, if one was recorded.
func (h *Harness) Detection(group wa.JID, id wa.MessageID) (pipeline.Verdict, bool) {
	panic("not implemented")
}

// Banned reports whether member has an active ban (rejoin block) in group.
func (h *Harness) Banned(group, member wa.JID) bool { panic("not implemented") }
