// Package wpk_test holds the hidden acceptance tests for WP-K: reviews, in-chat commands,
// learning overturns and the phone-number blacklist (DESIGN.md §7.5, §9.5, §9.7, §9.8, §13.7).
package wpk_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/api/chat/chattest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/blacklist/blacklisttest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/fake"
)

const (
	grp    wa.JID = "120363000000000001@g.us" // protected group
	grp2   wa.JID = "120363000000000002@g.us" // second protected group
	revGrp wa.JID = "120363000000000099@g.us" // review group (not protected)

	adminA   wa.JID = "100000000000001@lid" // admin "Sara"
	adminB   wa.JID = "100000000000002@lid" // second admin
	operator wa.JID = "100000000000009@lid" // instance operator, also an admin of grp

	member      wa.JID = "200000000000001@lid"         // ordinary member "Ahmed"
	memberPhone wa.JID = "966551234567@s.whatsapp.net" // member's phone identity
	memberE164         = "+966551234567"
	member2     wa.JID = "200000000000002@lid"
)

// --- shared assertions ---------------------------------------------------------------------

// assertSilent fails if any Send went into a protected group (DESIGN §9.7: nothing is ever
// posted in a protected group).
func assertSilent(t *testing.T, acts *reviewtest.Actions, protected ...wa.JID) {
	t.Helper()
	for _, s := range reviewtest.Of[review.Send](acts) {
		for _, p := range protected {
			if s.Chat == p {
				t.Errorf("got a Send into protected group %s (%q), want none (silent by default, DESIGN §9.7)", p, s.Message.Text)
			}
		}
	}
}

func sendsTo(acts *reviewtest.Actions, chatJID wa.JID) []review.Send {
	var out []review.Send
	for _, s := range reviewtest.Of[review.Send](acts) {
		if s.Chat == chatJID {
			out = append(out, s)
		}
	}
	return out
}

func bansOf(acts *reviewtest.Actions, target wa.JID) []review.Ban {
	var out []review.Ban
	for _, b := range reviewtest.Of[review.Ban](acts) {
		if b.Target == target {
			out = append(out, b)
		}
	}
	return out
}

func deletesOf(acts *reviewtest.Actions, id wa.MessageID) []review.Delete {
	var out []review.Delete
	for _, d := range reviewtest.Of[review.Delete](acts) {
		if d.Message.ID == id {
			out = append(out, d)
		}
	}
	return out
}

func standingKinds(s *reviewtest.Standing, person wa.JID) map[string]int {
	out := map[string]int{}
	for _, e := range s.Events() {
		if e.Person == person {
			out[e.Kind]++
		}
	}
	return out
}

func activeFor(c *reviewtest.Corpus, id wa.MessageID) []reviewtest.CorpusEntry {
	var out []reviewtest.CorpusEntry
	for _, e := range c.Active() {
		if e.Message.ID == id {
			out = append(out, e)
		}
	}
	return out
}

// assertNoTextLeft fails if any stored corpus entry, allow-list entry or evidence snapshot
// still holds text of the overturned message (DESIGN §7.5 "Overturns remove what was learned").
func assertNoTextLeft(t *testing.T, ctx context.Context, ev *review.MemoryEvidence, c *reviewtest.Corpus, group wa.JID, id wa.MessageID, text string) {
	t.Helper()
	probe := text
	if len(probe) > 12 {
		probe = probe[:12]
	}
	for _, e := range c.Entries() {
		if e.Message.ID == id && e.Text != "" {
			t.Errorf("corpus entry %d still holds text %q after the overturn, want none (no text of an overturned message is kept, DESIGN §7.5)", e.ID, e.Text)
		}
	}
	for _, a := range c.Allowed() {
		if a.Text != "" {
			t.Errorf("allow-list entry holds text %q, want fingerprint only (DESIGN §7.5)", a.Text)
		}
	}
	snap, err := ev.Get(ctx, group, id)
	if err == nil && (snap.Text != "" || snap.Thumbnail != nil) {
		t.Errorf("evidence snapshot still has text %q / thumbnail after the overturn, want it deleted (DESIGN §7.5)", snap.Text)
	}
	if err == nil && strings.Contains(snap.Text, probe) {
		t.Errorf("evidence snapshot still contains %q, want no text of the overturned message (DESIGN §7.5)", probe)
	}
}

// --- learning ------------------------------------------------------------------------------

type learnEnv struct {
	clk      *clock.Fake
	evidence *review.MemoryEvidence
	corpus   *reviewtest.Corpus
	standing *reviewtest.Standing
	learning *review.Learning
}

func newLearnEnv(clk *clock.Fake) learnEnv {
	e := learnEnv{
		clk:      clk,
		evidence: review.NewMemoryEvidence(),
		corpus:   &reviewtest.Corpus{},
		standing: &reviewtest.Standing{},
	}
	e.learning = review.NewLearning(review.LearningDeps{
		Evidence: e.evidence, Corpus: e.corpus, Standing: e.standing, Clock: clk,
	})
	return e
}

// --- review service ------------------------------------------------------------------------

type reviewEnv struct {
	learnEnv
	ctx      context.Context
	store    *review.MemoryStore
	settings *reviewtest.Settings
	admins   *reviewtest.Admins
	acts     *reviewtest.Actions
	svc      *review.Service
	posted   int
}

func newReviewEnv(t *testing.T, cfg review.Config) *reviewEnv {
	t.Helper()
	clk := clock.NewFake(chattest.Start)
	e := &reviewEnv{
		learnEnv: newLearnEnv(clk),
		ctx:      t.Context(),
		store:    review.NewMemoryStore(),
		settings: &reviewtest.Settings{Default: cfg},
		admins:   &reviewtest.Admins{},
		acts:     &reviewtest.Actions{},
	}
	e.admins.Set(grp, adminA, true)
	e.admins.Set(grp, adminB, true)
	e.svc = review.NewService(review.Deps{
		Store: e.store, Settings: e.settings, Admins: e.admins, Actions: e.acts,
		Learner: e.learning, Clock: clk,
	})
	return e
}

func groupDest(mode review.EnforcementMode) review.Config {
	return review.Config{Destination: review.DestinationGroup, ReviewGroup: revGrp, Mode: mode, Language: "en"}
}

func spamItem(kind review.Kind, deleted bool, subj review.Subject) review.NewItem {
	return review.NewItem{
		Group:     grp,
		GroupName: "Riyadh Cars Market",
		Kind:      kind,
		Subject:   subj,
		Message:   &wa.MessageRef{Sender: member, ID: "spam-1"},
		Evidence: review.Evidence{
			Text:           "💰 اربح ٣٠٠٠ ريال يومياً… join my channel t.me/xyz",
			Score:          0.97,
			Source:         "rules",
			MessageDeleted: deleted,
		},
		Proposed: review.DecisionBan,
	}
}

func plainSubject() review.Subject {
	return review.Subject{Person: member, Phone: memberPhone, Name: "Ahmed"}
}

// open opens an item and, for a group destination, plays the action queue: the review Send is
// "sent" and reported back through RecordPosted. It returns the item and its posted message ID.
func (e *reviewEnv) open(t *testing.T, n review.NewItem) (review.Item, wa.MessageID) {
	t.Helper()
	it, err := e.svc.Open(e.ctx, n)
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	var id wa.MessageID
	for _, s := range reviewtest.Of[review.Send](e.acts) {
		if s.ReviewID == it.ID {
			e.posted++
			id = wa.MessageID("review-msg-" + strconv.Itoa(e.posted))
			if err := e.svc.RecordPosted(e.ctx, review.Posted{ReviewID: it.ID, Chat: s.Chat, ID: id, SentAt: e.clk.Now()}); err != nil {
				t.Fatalf("RecordPosted() error = %v, want nil", err)
			}
		}
	}
	return it, id
}

func (e *reviewEnv) get(t *testing.T, id int64) review.Item {
	t.Helper()
	it, err := e.store.Get(e.ctx, id)
	if err != nil {
		t.Fatalf("store.Get(%d) error = %v", id, err)
	}
	return it
}

// --- chat handler --------------------------------------------------------------------------

type chatEnv struct {
	learnEnv
	ctx      context.Context
	acts     *reviewtest.Actions
	admins   *reviewtest.Admins
	groups   *chattest.Groups
	settings *chattest.Settings
	members  *chattest.Members
	history  *chattest.History
	identity *chattest.Identity
	audit    *reviewtest.Audit
	blList   *blacklist.MemoryList
	blGroups *blacklisttest.Groups
	bl       *blacklist.Service
	h        *chat.Handler
	seq      int
}

func newChatEnv(t *testing.T) *chatEnv {
	t.Helper()
	clk := clock.NewFake(chattest.Start)
	e := &chatEnv{
		learnEnv: newLearnEnv(clk),
		ctx:      t.Context(),
		acts:     &reviewtest.Actions{},
		admins:   &reviewtest.Admins{},
		members:  &chattest.Members{},
		history:  &chattest.History{},
		identity: &chattest.Identity{},
		audit:    &reviewtest.Audit{},
		blList:   blacklist.NewMemoryList(),
		blGroups: &blacklisttest.Groups{},
	}
	e.groups = chattest.NewGroups(e.admins)
	e.groups.Protect(grp, "Riyadh Cars Market")
	e.groups.SetName(revGrp, "Admins")
	e.groups.Serve(revGrp, grp)
	e.groups.SetParticipants(grp, adminA, adminB, operator, member, member2)
	e.admins.Set(grp, adminA, true)
	e.admins.Set(grp, adminB, true)
	e.admins.Set(grp, operator, true)
	e.identity.Link(memberPhone, member)
	e.blGroups.SetMembers(grp,
		blacklist.Member{JID: member, LID: member, Phone: memberPhone},
		blacklist.Member{JID: adminA, LID: adminA, Admin: true},
	)
	e.settings = chattest.NewSettings(map[string]string{
		"enforcement.mode":   string(review.ModeAuto),
		"review.destination": string(review.DestinationDashboard),
		"review.timeout":     "6h",
		"review.on_timeout":  string(review.TimeoutKeepDeletedNoBan),
	})

	waClient := fake.New()
	waClient.SetUser(memberPhone, wa.UserInfo{LID: member})
	rs := &reviewtest.Settings{Default: review.Config{Destination: review.DestinationDashboard, Mode: review.ModeAuto}}
	store := review.NewMemoryStore()
	svc := review.NewService(review.Deps{Store: store, Settings: rs, Admins: e.admins, Actions: e.acts, Learner: e.learning, Clock: clk})
	e.bl = blacklist.NewService(blacklist.Deps{
		List:      e.blList,
		Normalize: blacklist.SimpleNormalizer{},
		Resolver: blacklist.NewResolver(blacklist.ResolverDeps{
			Users: waClient, Budget: &blacklisttest.Budget{}, Linker: &blacklisttest.Linker{}, List: e.blList,
		}),
		Groups:   e.blGroups,
		Settings: &blacklisttest.Settings{Default: blacklist.GroupSettings{UseInstance: true, Mode: review.ModeAuto, DefaultCountry: "SA"}},
		Actions:  e.acts,
		Reviews:  svc,
		Notifier: &blacklisttest.Notifier{},
		Audit:    e.audit,
		Clock:    clk,
	})
	e.h = chat.New(chat.Deps{
		Groups: e.groups, Admins: e.admins, Actions: e.acts, Reviews: svc,
		Learning: e.learning, Evidence: e.learning, Blacklist: e.bl, Normalize: blacklist.SimpleNormalizer{},
		Settings: e.settings, Lists: &chattest.Lists{}, Recent: &chattest.Recent{}, Members: e.members,
		History: e.history, Identity: e.identity, Track: chattest.Track{}, Stats: chattest.Stats{},
		Threads: chat.NewMemoryThreads(), Audit: e.audit, Clock: clk, Language: "en",
		Operator: func(j wa.JID) bool { return j == operator },
	})
	return e
}

func (e *chatEnv) nextID() wa.MessageID {
	e.seq++
	return wa.MessageID("in-" + strconv.Itoa(e.seq))
}

// inGroup builds a message sent by from in the protected group.
func (e *chatEnv) inGroup(from wa.JID, text string) chat.Message {
	return chat.Message{Chat: grp, Sender: from, SenderName: "Sara", ID: e.nextID(), Text: text, At: e.clk.Now()}
}

// replyTo builds a reply in the protected group quoting a member's message.
func (e *chatEnv) replyTo(from wa.JID, text string, quotedID wa.MessageID, quotedSender wa.JID, quotedText string) chat.Message {
	m := e.inGroup(from, text)
	m.Quoted = &chat.Quoted{ID: quotedID, Sender: quotedSender, Text: quotedText}
	return m
}

// dm builds a DM from person to the bot.
func (e *chatEnv) dm(from wa.JID, text string) chat.Message {
	return chat.Message{Chat: from, Sender: from, SenderName: "Sara", ID: e.nextID(), Text: text, At: e.clk.Now()}
}

func (e *chatEnv) handle(t *testing.T, m chat.Message) {
	t.Helper()
	if err := e.h.Handle(e.ctx, m); err != nil {
		t.Fatalf("Handle(%q) error = %v, want nil", m.Text, err)
	}
}

// openThread has person DM the bot first, so command replies aren't limited by the
// inbound-first exception, then forgets whatever the bot replied.
func (e *chatEnv) openThread(t *testing.T, person wa.JID) {
	t.Helper()
	e.handle(t, e.dm(person, "hi"))
	e.acts.Reset()
}

func (e *chatEnv) changesBy(actor wa.JID, key string) []chattest.SettingsChange {
	var out []chattest.SettingsChange
	for _, c := range e.settings.Changes() {
		if c.Actor == actor && c.Key == key {
			out = append(out, c)
		}
	}
	return out
}
