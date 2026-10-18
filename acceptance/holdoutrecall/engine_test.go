// Package holdoutrecall_test runs related wording and contact channels end to end through the
// fast-lane engine with the default weights (contract holdout-recall).
package holdoutrecall_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/app/fastlane"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/clock"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/rules"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/scoring"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa/adapter/synth"
)

const (
	group wa.JID = "120363000000000001@g.us"
	bot   wa.JID = "966500000000@s.whatsapp.net"
)

var (
	t0      = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	hashKey = []byte("acceptance-holdout-recall-key-001")
)

// Synthetic confirmed spam.
const (
	adLong = "Hello everyone, we provide complete student services: assignments, homework, research papers, " +
		"graduation projects, presentations, posters, summaries, translation, proofreading, editing, essays, theses, " +
		"dissertations, statistics, accounting, programming, engineering drawings, mathematics, physics, chemistry, " +
		"biology, nursing, pharmacy, medicine, law, management, marketing, economics, quizzes, exams, online lectures, " +
		"blackboard tasks, portfolio design, resume writing, cover letters, sick leave notes, medical excuses, hospital " +
		"reports, attendance certificates and internship letters. Experienced tutors, fast delivery, guaranteed grades, " +
		"affordable prices, confidential handling, discounts for groups, payment after completion. " +
		"Message us on WhatsApp wa.me/966551234567"
	adLongAr = "السلام عليكم ورحمة الله وبركاته، نقدم خدمات طلابية متكاملة: حل الواجبات والبحوث والتقارير والمشاريع " +
		"والعروض التقديمية والتلخيص والترجمة والتدقيق اللغوي وكتابة السيرة الذاتية والرسائل الجامعية وحل الاختبارات " +
		"والكويزات ومتابعة البلاك بورد وتصميم البوسترات والانفوجرافيك والمحاسبة والبرمجة والرياضيات والفيزياء والكيمياء " +
		"والتمريض والصيدلة والقانون والتسويق والاقتصاد، وأيضا الأعذار الطبية والإجازات المرضية وتقارير المستشفى وشهادات " +
		"الحضور وخطابات التدريب. أسعار مناسبة وتسليم سريع وضمان الدرجة وسرية تامة وخصومات للمجموعات والدفع بعد الإنجاز. " +
		"تواصل واتساب wa.me/966551234567"
	template = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
		"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
		"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
		"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
		"attention to each new investor. Hundreds of people have already joined and changed their lives. Do not miss this chance " +
		"to secure your financial future and support your family. Send a message to our manager on WhatsApp today and write the " +
		"word INVEST to receive the full details, the registration form and your welcome bonus."
)

// Messages.
const (
	relatedOnly   = "Translation, proofreading, essays, presentations, statistics and accounting assignments. Fast delivery and affordable prices."
	relatedOnlyAr = "حل واجبات وبحوث وتقارير ومشاريع وترجمة وتلخيص وبرمجة ومحاسبة، تسليم سريع وأسعار مناسبة"
	relatedWaMe   = "Translation, proofreading, essays, presentations, statistics and accounting assignments. Fast delivery, affordable prices, WhatsApp wa.me/966559876543"
	relatedArNum  = "حل واجبات وبحوث وتقارير ومشاريع وترجمة وتلخيص وبرمجة ومحاسبة، تسليم سريع، واتساب 0559876543"
	contactOnly   = "Anyone selling a used microscope? Call 0551234567"
	// Cyrillic е in "Frее" (mixed script) and three zero-width spaces (light invisible chars).
	obfuscatedContact = "Frее football boots, bar​ely used, si​ze 42, ask in our gro​up https://chat.whatsapp.com/AbCdEf1234567890XyZab"
	// Ordinary chat that shares a lot of wording with the ads but offers no contact.
	sickNote   = "My sick leave notes, medical excuses and hospital reports were accepted, so the nursing exams and quizzes are postponed"
	sickNoteAr = "الحمد لله سلمت الأعذار الطبية والإجازات المرضية وتقارير المستشفى وشهادات الحضور، والدكتور أجل الاختبارات والكويزات"
	rewrite    = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
		"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
		"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
		"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
		"attention to each new investor. Hundreds of people have already joined and changed their lives. Message our support desk now, " +
		"mention this group, and you will get a free first lesson, a beginner guide and a seat in tonight's live webinar with our senior analyst."
)

func bizCard(desc string) string {
	return "BEGIN:VCARD\nVERSION:3.0\nFN:Study Services\nTEL;type=CELL;waid=966559876543:+966 55 987 6543\n" +
		"X-WA-BIZ-NAME:Study Services\nX-WA-BIZ-DESCRIPTION:" + desc + "\nEND:VCARD"
}

func analyzerSet(t *testing.T, ix *neardup.Index) pipeline.AnalyzerSet {
	t.Helper()
	h, err := blacklist.NewHasher(hashKey)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return pipeline.AnalyzerSet{
		Rules: rules.NewHolder(nil, clock.NewFake(t0), zerolog.Nop()), Hash: h.Hash, Bot: bot,
		Corpus:     func(wa.JID) []*neardup.Index { return []*neardup.Index{ix} },
		CorpusText: fastlane.CorpusText(h, hashKey),
	}
}

// evaluate runs one message from an established member through the engine (no AI) against a
// corpus of the three confirmed ads.
func evaluate(t *testing.T, text string, vcards ...string) pipeline.Decision {
	t.Helper()
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	for id, s := range map[string]string{"ad-en": adLong, "ad-ar": adLongAr, "tmpl": template} {
		ix.Add(id, neardup.FingerprintText(s))
	}
	en, err := pipeline.NewEngine(pipeline.EngineConfig{
		Analyzers: analyzerSet(t, ix).Analyzers(), Weights: scoring.DefaultWeights(),
		Normalize: pipeline.NormalizeTexts, Clock: clock.NewFake(t0),
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	sender := wa.JID("200000000000002@lid")
	e, err := synth.Envelope(synth.Spec{Chat: group, Sender: sender, ID: "3EB0HOLDOUT0001", Time: t0, Text: text, VCards: vcards}, bot)
	if err != nil {
		t.Fatalf("synth.Envelope: %v", err)
	}
	snd := pipeline.Sender{Known: true, Member: policy.MemberRecord{
		LID: string(sender), Role: policy.RoleMember, JoinedAt: t0.Add(-365 * 24 * time.Hour), PreExisting: true,
	}}
	d, err := en.Evaluate(t.Context(), e, t0, policy.Defaults(), snd)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return d
}

func codes(d pipeline.Decision) []string {
	var out []string
	for _, s := range d.Score.Signals {
		out = append(out, s.Code)
	}
	return out
}

func describe(d pipeline.Decision) string {
	var b strings.Builder
	for _, s := range d.Score.Signals {
		fmt.Fprintf(&b, " %s(%+.1f)", s.Code, s.Applied)
	}
	return fmt.Sprintf("p=%.2f band=%s source=%s actions=%v signals:%s", d.Score.P, d.Verdict.Band, d.Verdict.Source, d.Verdict.Actions, b.String())
}

func corpusSignals(d pipeline.Decision) int {
	n := 0
	for _, c := range codes(d) {
		if strings.HasPrefix(c, "corpus.") {
			n++
		}
	}
	return n
}

func wantClean(t *testing.T, d pipeline.Decision, why string) {
	t.Helper()
	if d.Verdict.Band != pipeline.BandClean || len(d.Verdict.Actions) != 0 {
		t.Errorf("got %s, want clean with no actions (%s)", describe(d), why)
	}
}

func TestAnalyzerSetIncludesContact(t *testing.T) {
	var names []string
	for _, a := range analyzerSet(t, neardup.NewIndex(neardup.DefaultThresholds())).Analyzers() {
		names = append(names, a.Name())
	}
	if !slices.Contains(names, "contact") || !slices.Contains(names, "neardup") {
		t.Errorf("got analyzers %v, want contact and neardup among them (the fast lane runs the contact analyzer, contract holdout-recall)", names)
	}
}

func TestDefaultWeightsVersionChanged(t *testing.T) {
	if v := scoring.DefaultWeights().Version; v == "2026-10-17.1" || v == "" {
		t.Errorf("got weights version %q, want a new version (every detection records the version that scored it, DESIGN §7.3)", v)
	}
}

func TestRelatedWordingAloneIsClean(t *testing.T) {
	for _, tc := range []struct{ name, text string }{{"english", relatedOnly}, {"arabic", relatedOnlyAr}} {
		t.Run(tc.name, func(t *testing.T) {
			d := evaluate(t, tc.text)
			if !slices.Contains(codes(d), "corpus.related") || slices.Contains(codes(d), "contact.advertised") {
				t.Fatalf("got %s, want corpus.related and no contact.advertised (the wording is inside a confirmed ad, no contact is offered)", describe(d))
			}
			if d.Score.P < 0.35 || d.Score.P > 0.41 {
				t.Errorf("got %s, want p ≈ 0.38 (sigmoid(-3.0 + 2.5))", describe(d))
			}
			wantClean(t, d, "related wording alone stays below T_review 0.60, DET-002")
		})
	}
}

func TestContactAloneIsClean(t *testing.T) {
	d := evaluate(t, contactOnly)
	if !slices.Contains(codes(d), "contact.advertised") || corpusSignals(d) != 0 {
		t.Fatalf("got %s, want contact.advertised and no corpus signal", describe(d))
	}
	wantClean(t, d, "a phone number alone is ordinary in a group, DET-002")
}

func TestContactWithObfuscationHintsIsClean(t *testing.T) {
	d := evaluate(t, obfuscatedContact)
	for _, c := range []string{"contact.advertised", "text.mixed_script", "text.invisible_chars"} {
		if !slices.Contains(codes(d), c) {
			t.Fatalf("got %s, want %s among the signals (the test message carries it)", describe(d), c)
		}
	}
	if d.Score.P >= 0.60 {
		t.Errorf("got %s, want p < 0.60 (-3.0 + 1.0 + 1.5 + 0.5 = 0 → p 0.50)", describe(d))
	}
	wantClean(t, d, "a contact channel plus light obfuscation hints stays below T_review, contract holdout-recall")
}

func TestRelatedWithContactGoesToAdmins(t *testing.T) {
	tests := []struct {
		name, text string
		vcards     []string
	}{
		{"english with wa.me link", relatedWaMe, nil},
		{"arabic with national number", relatedArNum, nil},
		{"contact card with business description", "Study Services", []string{bizCard(relatedOnly)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := evaluate(t, tt.text, tt.vcards...)
			if !slices.Contains(codes(d), "corpus.related") || !slices.Contains(codes(d), "contact.advertised") {
				t.Fatalf("got %s, want corpus.related and contact.advertised", describe(d))
			}
			if d.Score.P < 0.60 || d.Score.P >= 0.65 {
				t.Errorf("got %s, want p ≈ 0.62 (sigmoid(-3.0 + 2.5 + 1.0) reaches T_review 0.60)", describe(d))
			}
			if d.Verdict.Band != pipeline.BandStillUncertain || d.Verdict.Source != pipeline.SourceRules {
				t.Errorf("got %s, want still_uncertain from rules (UNCERTAIN goes to the AI; with no AI, to admins, DESIGN §7.0, DET-001)", describe(d))
			}
			if slices.Contains(d.Verdict.Actions, pipeline.ActionDelete) || slices.Contains(d.Verdict.Actions, pipeline.ActionBan) {
				t.Errorf("got %s, want no delete and no ban (heuristics never delete or ban without the AI or known evidence, DESIGN §7.3, §9.2)", describe(d))
			}
		})
	}
}

func TestOrdinarySickLeaveMessageIsClean(t *testing.T) {
	for _, tc := range []struct{ name, text string }{{"english", sickNote}, {"arabic", sickNoteAr}} {
		t.Run(tc.name, func(t *testing.T) {
			d := evaluate(t, tc.text)
			if slices.Contains(codes(d), "contact.advertised") {
				t.Errorf("got %s, want no contact.advertised (the message offers no contact)", describe(d))
			}
			if !slices.Contains(codes(d), "corpus.related") {
				t.Errorf("got %s, want corpus.related (most of its terms are in a confirmed ad; this case shows the contact channel is what separates)", describe(d))
			}
			wantClean(t, d, "ordinary chat about one's own sick leave must never reach admins, DESIGN §8, DET-002")
		})
	}
}

func TestExistingCorpusOutcomesUnchanged(t *testing.T) {
	near := evaluate(t, strings.Replace(template, "Monday", "Friday", 1))
	if !slices.Contains(codes(near), "corpus.near_duplicate") || corpusSignals(near) != 1 || near.Verdict.Band != pipeline.BandCertain || near.Verdict.Source != pipeline.SourceKnown {
		t.Errorf("got %s, want one corpus.near_duplicate, certain/known (a near-copy stays CERTAIN, DESIGN §7.0)", describe(near))
	}
	loose := evaluate(t, rewrite)
	if !slices.Contains(codes(loose), "corpus.similar") || corpusSignals(loose) != 1 || loose.Verdict.Band != pipeline.BandStillUncertain {
		t.Errorf("got %s, want one corpus.similar, still_uncertain (a loose match is unchanged, DESIGN §7.5)", describe(loose))
	}
}
