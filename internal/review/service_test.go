package review_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review/reviewtest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const (
	group  wa.JID = "120363000000000001@g.us"
	rgroup wa.JID = "120363000000000099@g.us"
	sara   wa.JID = "300000000000001@lid"
	omar   wa.JID = "300000000000002@lid"
	ahmed  wa.JID = "200000000000001@lid"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

type rig struct {
	svc      *review.Service
	store    *review.MemoryStore
	acts     *reviewtest.Actions
	admins   *reviewtest.Admins
	settings *reviewtest.Settings
	corpus   *reviewtest.Corpus
	standing *reviewtest.Standing
	clock    *clock.Fake
}

func newRig(cfg review.Config) *rig {
	r := &rig{
		store: review.NewMemoryStore(), acts: &reviewtest.Actions{}, admins: &reviewtest.Admins{},
		settings: &reviewtest.Settings{Default: cfg}, corpus: &reviewtest.Corpus{}, standing: &reviewtest.Standing{},
		clock: clock.NewFake(start),
	}
	learning := review.NewLearning(review.LearningDeps{Evidence: review.NewMemoryEvidence(), Corpus: r.corpus, Standing: r.standing, Clock: r.clock})
	r.svc = review.NewService(review.Deps{
		Store: r.store, Settings: r.settings, Admins: r.admins, Actions: r.acts, Learner: learning,
		Standing: r.standing, Clock: r.clock,
	})
	r.admins.Set(group, sara, true)
	r.admins.Set(group, omar, true)
	return r
}

func spamItem() review.NewItem {
	return review.NewItem{
		Group: group, GroupName: "Riyadh Cars Market", Kind: review.KindBanDecision,
		Subject:  review.Subject{Person: ahmed, Phone: "966512345612@s.whatsapp.net", Name: "Ahmed", MemberFor: 150 * 24 * time.Hour, EarlierMessages: 312},
		Message:  &wa.MessageRef{Sender: ahmed, ID: "SPAM1"},
		Evidence: review.Evidence{Text: "💰 اربح ٣٠٠٠ ريال يومياً contact +966 55 987 6543 or x@y.com", Score: 0.97, Source: "rules", MessageDeleted: true},
	}
}

func TestOpenPostsToReviewGroupOnly(t *testing.T) {
	tests := []struct {
		name  string
		cfg   review.Config
		posts int
	}{
		{"dashboard default", review.Config{}, 0},
		{"review group", review.Config{Destination: review.DestinationGroup, ReviewGroup: rgroup}, 1},
		{"group without a review group", review.Config{Destination: review.DestinationGroup}, 0},
		{"protected group as its own review group", review.Config{Destination: review.DestinationGroup, ReviewGroup: group}, 0},
		{"dm (v0.3) falls back to dashboard", review.Config{Destination: review.DestinationDM}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(tt.cfg)
			it, err := r.svc.Open(context.Background(), spamItem())
			if err != nil {
				t.Fatal(err)
			}
			sends := reviewtest.Of[review.Send](r.acts)
			if len(sends) != tt.posts {
				t.Fatalf("got %d posts, want %d (DESIGN §9.5 destinations; never the protected group, §9.7)", len(sends), tt.posts)
			}
			for _, s := range sends {
				if s.Chat != rgroup || s.ReviewID != it.ID {
					t.Errorf("posted to %s for review %d, want %s for %d", s.Chat, s.ReviewID, rgroup, it.ID)
				}
			}
		})
	}
}

func TestOpenAssignsUniqueCodes(t *testing.T) {
	r := newRig(review.Config{})
	seen := map[string]bool{}
	for range 300 {
		it, err := r.svc.Open(context.Background(), spamItem())
		if err != nil {
			t.Fatal(err)
		}
		if len(it.Code) < 4 || strings.ContainsAny(it.Code, "01OIL") || seen[it.Code] {
			t.Fatalf("code %q: want a unique code of 4+ characters without look-alikes (DESIGN §9.5)", it.Code)
		}
		seen[it.Code] = true
		if got, err := r.svc.ByCode(context.Background(), strings.ToLower("#"+it.Code)); err != nil || got.ID != it.ID {
			t.Fatalf("ByCode(#%s lower-case) = %v, %v; want item %d", it.Code, got.ID, err, it.ID)
		}
	}
}

func TestOpenRetriesTakenCodes(t *testing.T) {
	// A reader that repeats the same bytes makes every 4-character code collide.
	r := newRig(review.Config{})
	svc := review.NewService(review.Deps{
		Store: r.store, Settings: r.settings, Admins: r.admins, Actions: r.acts, Clock: r.clock,
		Rand: bytes.NewReader(bytes.Repeat([]byte{7}, 1000)),
	})
	first, err := svc.Open(context.Background(), spamItem())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Open(context.Background(), spamItem())
	if err != nil {
		t.Fatalf("second Open with colliding codes: %v, want a longer code instead", err)
	}
	if len(second.Code) <= len(first.Code) {
		t.Errorf("codes %q then %q: want the retry to lengthen the code", first.Code, second.Code)
	}
}

func TestNewCode(t *testing.T) {
	code, err := review.NewCode(nil, 6)
	if err != nil || len(code) != 6 {
		t.Fatalf("NewCode = %q, %v; want 6 characters", code, err)
	}
	if _, err := review.NewCode(bytes.NewReader(nil), 4); err == nil {
		t.Error("NewCode with an empty reader: want an error")
	}
}

func TestRender(t *testing.T) {
	r := newRig(review.Config{})
	it, err := r.svc.Open(context.Background(), spamItem())
	if err != nil {
		t.Fatal(err)
	}
	msg := review.Render(it, review.Config{}, nil)
	for _, want := range []string{"#" + it.Code, "Riyadh Cars Market", "~Ahmed", "+966 5•• ••• •12", "312 earlier", "0.97", "rules", "deleted ✔", "Auto-decision in 6h: keep deleted, no ban"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("review text lacks %q (DESIGN §9.5 who/what/why/score/source):\n%s", want, msg.Text)
		}
	}
	for _, leak := range []string{"987 6543", "x@y.com", "966512345612"} {
		if strings.Contains(msg.Text, leak) {
			t.Errorf("review text leaks %q (third-party numbers and emails are masked, DESIGN §9.5)", leak)
		}
	}
	if msg.Image != nil {
		t.Error("no thumbnail: want a text message")
	}

	it.Evidence.Thumbnail = &wa.Image{Data: []byte{1, 2, 3}, Mimetype: "image/jpeg"}
	it.Evidence.Text = strings.Repeat("spam ", 200)
	msg = review.Render(it, review.Config{Language: "ar"}, nil)
	if msg.Image == nil {
		t.Error("with a thumbnail: want an image with the text as caption")
	}
	if !strings.Contains(msg.Text, "مراجعة #"+it.Code) {
		t.Errorf("Arabic review: got %q, want the Arabic header", strings.SplitN(msg.Text, "\n", 2)[0])
	}
	if strings.Count(msg.Text, "spam") > 70 {
		t.Error("evidence text not truncated to about 300 characters")
	}
	if review.Render(it, review.Config{BlurMedia: true}, nil).Image != nil {
		t.Error("review.blur_media: want no thumbnail")
	}
}

func TestParseReplyAndReaction(t *testing.T) {
	replies := []struct {
		text   string
		ladder bool
		want   review.Decision
		ok     bool
	}{
		{"ban", false, review.DecisionBan, true},
		{"  Kick! ", false, review.DecisionBan, true},
		{"حظر", false, review.DecisionBan, true},
		{"طرد", false, review.DecisionBan, true},
		{"OK", false, review.DecisionNotSpam, true},
		{"not   spam", false, review.DecisionNotSpam, true},
		{"ليس سبام", false, review.DecisionNotSpam, true},
		{"delete only", false, review.DecisionDeleteOnly, true},
		{"clear", false, review.DecisionClear, true},
		{"mute 2h", false, "", false},
		{"mute 2h", true, review.DecisionMute, true},
		{"كتم", true, review.DecisionMute, true},
		{"banana", false, "", false},
		{"", false, "", false},
	}
	for _, tt := range replies {
		if got, ok := review.ParseReply(tt.text, tt.ladder); got != tt.want || ok != tt.ok {
			t.Errorf("ParseReply(%q, ladder=%v) = %q, %v; want %q, %v (DESIGN §9.5 reply keywords)", tt.text, tt.ladder, got, ok, tt.want, tt.ok)
		}
	}
	reactions := []struct {
		emoji string
		cfg   review.Config
		want  review.Decision
		ok    bool
	}{
		{"✅", review.Config{}, review.DecisionBan, true},
		{"👍🏽", review.Config{}, review.DecisionBan, true},
		{"❌", review.Config{}, review.DecisionNotSpam, true},
		{"👎", review.Config{}, review.DecisionNotSpam, true},
		{"❤️", review.Config{}, "", false},
		{"🔇", review.Config{Reactions: map[review.Decision][]string{review.DecisionMute: {"🔇"}}}, "", false},
		{"🔇", review.Config{Ladder: true, Reactions: map[review.Decision][]string{review.DecisionMute: {"🔇"}}}, review.DecisionMute, true},
		{"🚫", review.Config{Reactions: map[review.Decision][]string{review.DecisionBan: {"🚫"}}}, review.DecisionBan, true},
	}
	for _, tt := range reactions {
		if got, ok := review.ParseReaction(tt.emoji, tt.cfg); got != tt.want || ok != tt.ok {
			t.Errorf("ParseReaction(%q) = %q, %v; want %q, %v", tt.emoji, got, ok, tt.want, tt.ok)
		}
	}
}

func FuzzParseReply(f *testing.F) {
	for _, s := range []string{"ban", "ليس سبام", "mute 2h", ".ok!"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, ok := review.ParseReply(s, false)
		if ok && d == review.DecisionMute {
			t.Fatalf("ParseReply(%q) = mute without the ladder", s)
		}
		if ok != (d != "") {
			t.Fatalf("ParseReply(%q) = %q, %v: ok must match a non-empty decision", s, d, ok)
		}
	})
}

// TestOutcomeEditOrFollowUp covers F-121: within WhatsApp's 20-minute edit window the review
// message is edited; after it, a follow-up quotes it.
func TestOutcomeEditOrFollowUp(t *testing.T) {
	for _, tt := range []struct {
		name  string
		after time.Duration
		edit  bool
	}{
		{"within the edit window", 19 * time.Minute, true},
		{"after the edit window", 21 * time.Minute, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r := newRig(review.Config{Destination: review.DestinationGroup, ReviewGroup: rgroup})
			it, err := r.svc.Open(ctx, spamItem())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.svc.RecordPosted(ctx, review.Posted{ReviewID: it.ID, Chat: rgroup, ID: "BOT1", SentAt: r.clock.Now()}); err != nil {
				t.Fatal(err)
			}
			r.clock.Advance(tt.after)
			r.acts.Reset()
			if _, err := r.svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: sara, VoterName: "Sara", Decision: review.DecisionBan, Method: review.MethodReaction}); err != nil {
				t.Fatal(err)
			}
			edits, sends := reviewtest.Of[review.Edit](r.acts), reviewtest.Of[review.Send](r.acts)
			if tt.edit {
				if len(edits) != 1 || edits[0].ID != "BOT1" || !strings.Contains(edits[0].Text, "✔ Handled by ~Sara · banned") {
					t.Fatalf("edits = %+v, want one edit of BOT1 showing who decided (DESIGN §9.5)", edits)
				}
				return
			}
			if len(edits) != 0 || len(sends) != 1 || sends[0].Message.ReplyTo == nil || sends[0].Message.ReplyTo.ID != "BOT1" {
				t.Fatalf("edits=%d sends=%+v, want one follow-up quoting BOT1 after the 20-minute edit window", len(edits), sends)
			}
		})
	}
}

// TestTimeoutNeverBansWhenNotAllowed is the §9.5 invariant: whatever the configured fallback, a
// timeout never bans in an ask-admins mode, nor an established member, a hacked account or an admin.
func TestTimeoutNeverBansWhenNotAllowed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		mode := rapid.SampledFrom([]review.EnforcementMode{review.ModeAuto, review.ModeAskDeleteFirst, review.ModeAskBeforeAction}).Draw(t, "mode")
		fb := rapid.SampledFrom([]review.OnTimeout{review.TimeoutAutoBan, review.TimeoutKeepDeletedNoBan, review.TimeoutDeleteNoBan, review.TimeoutDismiss, "bogus"}).Draw(t, "fallback")
		subj := review.Subject{
			Person:      ahmed,
			Established: rapid.Bool().Draw(t, "established"),
			Hacked:      rapid.Bool().Draw(t, "hacked"),
			Admin:       rapid.Bool().Draw(t, "admin"),
		}
		r := newRig(review.Config{Mode: mode, OnTimeout: fb, Timeout: time.Duration(rapid.IntRange(0, 48).Draw(t, "hours")) * time.Hour})
		n := spamItem()
		n.Subject = subj
		ctx := context.Background()
		if _, err := r.svc.Open(ctx, n); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(49 * time.Hour)
		if _, err := r.svc.Expire(ctx); err != nil {
			t.Fatal(err)
		}
		bans := reviewtest.Of[review.Ban](r.acts)
		allowed := !mode.AskMode() && !subj.Established && !subj.Hacked && !subj.Admin && fb == review.TimeoutAutoBan
		if len(bans) > 0 && !allowed {
			t.Fatalf("mode=%s fallback=%s subject=%+v: timeout banned, want no ban (DESIGN §9.5 Timeouts)", mode, fb, subj)
		}
		if allowed && len(bans) != 1 {
			t.Fatalf("auto_ban allowed: got %d bans, want 1", len(bans))
		}
	})
}

func TestExpireDismissRestoresTrust(t *testing.T) {
	ctx := context.Background()
	r := newRig(review.Config{OnTimeout: review.TimeoutDismiss})
	if _, err := r.svc.Open(ctx, spamItem()); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(7 * time.Hour)
	if n, err := r.svc.Expire(ctx); err != nil || n != 1 {
		t.Fatalf("Expire = %d, %v; want 1 item handled", n, err)
	}
	ev := r.standing.Events()
	if len(ev) != 1 || ev[0].Kind != "restore_trust" || ev[0].Person != ahmed {
		t.Errorf("standing events = %+v, want one trust restore for the subject (dismiss, DESIGN §9.5)", ev)
	}
}

func TestVoteValidation(t *testing.T) {
	ctx := context.Background()
	r := newRig(review.Config{})
	it, err := r.svc.Open(ctx, spamItem())
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: sara, Decision: review.DecisionClear, Method: review.MethodReply})
	if err != nil || res.Outcome != review.OutcomeInvalid {
		t.Fatalf("clear on a non-hacked item = %v, %v; want OutcomeInvalid", res.Outcome, err)
	}
	res, err = r.svc.Vote(ctx, review.Ballot{ReviewID: it.ID, Voter: sara, Method: review.MethodReply, Raw: "hello"})
	if err != nil || res.Outcome != review.OutcomeUnknown {
		t.Fatalf("unknown keyword = %v, %v; want OutcomeUnknown", res.Outcome, err)
	}
	if _, err := r.svc.Vote(ctx, review.Ballot{Code: "ZZZZ", Voter: sara, Decision: review.DecisionBan}); err == nil {
		t.Error("vote on an unknown code: want ErrNotFound")
	}
	votes, _ := r.store.Votes(ctx, it.ID)
	if len(votes) != 2 {
		t.Errorf("stored %d votes, want 2: every vote is stored (DESIGN §9.5)", len(votes))
	}
	pending, _ := r.svc.Pending(ctx, group)
	if len(pending) != 1 {
		t.Errorf("pending = %d, want 1: invalid votes don't decide", len(pending))
	}
}

func TestAlertsCloseQuietly(t *testing.T) {
	ctx := context.Background()
	r := newRig(review.Config{Destination: review.DestinationGroup, ReviewGroup: rgroup})
	if _, err := r.svc.Open(ctx, review.NewItem{Group: group, GroupName: "G", Kind: review.KindAlert, Evidence: review.Evidence{WhyAsking: "bot lost admin"}}); err != nil {
		t.Fatal(err)
	}
	r.acts.Reset()
	if n, err := r.svc.Expire(ctx); err != nil || n != 1 {
		t.Fatalf("Expire = %d, %v; want the alert closed", n, err)
	}
	if got := r.acts.All(); len(got) != 0 {
		t.Errorf("alert timeout enqueued %+v, want nothing (alerts ask for no decision)", got)
	}
}
