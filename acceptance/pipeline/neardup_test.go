//go:build integration

package pipeline_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline/pipelinetest"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// template is a long confirmed scam. trivialEdit changes one word of it (clearly within the
// CERTAIN thresholds under any reasonable shingling); rewrite keeps the first two thirds and
// replaces the rest (clearly related, clearly below CERTAIN).
const template = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
	"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
	"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
	"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
	"attention to each new investor. Hundreds of people have already joined and changed their lives. Do not miss this chance " +
	"to secure your financial future and support your family. Send a message to our manager on WhatsApp today and write the " +
	"word INVEST to receive the full details, the registration form and your welcome bonus."

var trivialEdit = strings.Replace(template, "Monday", "Friday", 1)

const rewrite = "Dear members, we are excited to announce an exclusive investment opportunity for this group only. " +
	"Our licensed crypto trading team has helped hundreds of families earn between five hundred and two thousand dollars every week " +
	"without any experience. You only need a phone and thirty minutes a day. Our expert mentor will guide you step by step, " +
	"and all profits are paid directly to your bank account every Monday. Spaces are limited because we want to give personal " +
	"attention to each new investor. Hundreds of people have already joined and changed their lives. Message our support desk now, " +
	"mention this group, and you will get a free first lesson, a beginner guide and a seat in tonight's live webinar with our senior analyst."

const arabicSpam = "اربح خمسمائة دولار يوميا من البيت بدون خبرة، فقط تواصل مع المدير على الواتساب واكتب كلمة استثمار " +
	"لتحصل على التفاصيل الكاملة ومكافأة الترحيب قبل انتهاء العرض الليلة"

// Obfuscations that normalization undoes (DESIGN §7.2).
func zeroWidth(s string) string {
	var b strings.Builder
	for i, r := range []rune(s) {
		b.WriteRune(r)
		if r != ' ' && i%3 == 0 {
			b.WriteString([]string{"\u200b", "\u2060", "\ufeff", "\u200d"}[i%4])
		}
	}
	return b.String()
}

func homoglyphs(s string) string {
	return strings.NewReplacer("a", "\u0430", "e", "\u0435", "o", "\u043e", "c", "\u0441", "p", "\u0440").Replace(s)
}

func fullwidth(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > ' ' && r < 0x7f {
			r += 0xFEE0
		}
		b.WriteRune(r)
	}
	return b.String()
}

func arabicDecorated(s string) string {
	return strings.NewReplacer(
		"اربح", "أربَحْ", "دولار", "دولارٍ", "يوميا", "يومياً", "البيت", "البيـــت", "الكاملة", "الكامِلة",
	).Replace(s)
}

// fresh returns a new member who has just joined, so each case has its own sender.
func fresh(h *pipelinetest.Harness, i int) pipelinetest.Member {
	m := pipelinetest.Member{
		LID: wa.JID(fmt.Sprintf("2100000000%05d@lid", i)),
		PN:  wa.JID(fmt.Sprintf("9665800%05d@s.whatsapp.net", i)),
	}
	h.Join(group, m)
	return m
}

func TestObfuscatedConfirmedSpamIsCertainKnown(t *testing.T) {
	tests := []struct {
		name, confirmed, sent string
	}{
		{"zero-width", template, zeroWidth(template)},
		{"homoglyphs", template, homoglyphs(template)},
		{"fullwidth", template, fullwidth(template)},
		{"all combined", template, zeroWidth(fullwidth(homoglyphs(template)))},
		{"arabic tashkeel tatweel alef", arabicSpam, arabicDecorated(arabicSpam)},
	}
	h := setup(t, pipelinetest.Settings{})
	h.ConfirmSpam(group, template)
	h.ConfirmSpam(group, arabicSpam)
	c := h.WA()
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := fresh(h, i)
			msg := textFrom(m, tt.sent)

			v := h.Deliver(msg)

			wantCertainKnown(t, v, "matching runs on normalized text, so an obfuscated copy of confirmed spam is CERTAIN, DESIGN §7.0, §7.2")
			if n := revokes(c, group, msg.ID); n != 1 {
				t.Errorf("got %d Revoke calls, want 1 (certain known spam is deleted, DESIGN §7.0)", n)
			}
			if n := removals(c, group, m); n != 1 {
				t.Errorf("got %d removals of the new sender, want 1 (Standard auto-bans on known evidence, DESIGN §9.2)", n)
			}
		})
	}
}

func TestTrivialEditOfConfirmedTemplateIsCertainKnown(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	h.ConfirmSpam(group, template)
	c := h.WA()
	m := fresh(h, 1)
	msg := textFrom(m, trivialEdit)

	v := h.Deliver(msg)

	wantCertainKnown(t, v, "a one-word edit of a long confirmed template is within SimHash ≤ 3 or MinHash ≥ 0.9, DESIGN §7.0, §7.5")
	if n := revokes(c, group, msg.ID); n != 1 {
		t.Errorf("got %d Revoke calls, want 1 (near-duplicate known spam is deleted, DESIGN §7.0)", n)
	}
	if n := removals(c, group, m); n != 1 {
		t.Errorf("got %d removals of the new sender, want 1 (CERTAIN near-duplicate is known evidence that bans under Standard, DESIGN §7.3, §9.2)", n)
	}
}

func TestLooseNearDuplicateGoesToAdminsMessageStays(t *testing.T) {
	h := setup(t, pipelinetest.Settings{})
	h.ConfirmSpam(group, template)
	c := h.WA()
	m := fresh(h, 1)
	msg := textFrom(m, rewrite)

	v := h.Deliver(msg)

	if n := revokes(c, group, msg.ID); n != 0 {
		t.Errorf("got %d Revoke calls, want 0 (a loose near-duplicate never deletes on its own; with AI off the message stays, DESIGN §7.0, §18)", n)
	}
	if n := removals(c, group, m); n != 0 {
		t.Errorf("got %d removals, want 0 (a loose near-duplicate never bans, DESIGN §9.2, §18)", n)
	}
	if v.Source != pipeline.SourceRules {
		t.Errorf("got source %q, want rules (closeness below the tight thresholds is a rules signal, DESIGN §7.3)", v.Source)
	}
	if v.Band != pipeline.BandStillUncertain {
		t.Errorf("got band %q, want still_uncertain (suspicious, not certain, and no AI configured, DESIGN §7.0)", v.Band)
	}
	if n := sendsTo(c, reviewGroup); n < 1 {
		t.Errorf("got %d review messages, want at least 1 (still uncertain goes to admins, DESIGN §7.0, §8.3)", n)
	}
	assertSilent(t, c)
}

func TestOverturnedFingerprintNeverMatches(t *testing.T) {
	tests := []struct {
		name, overturned, sent string
	}{
		{"overturned confirmed message", template, template},
		{"overturned near variant", trivialEdit, trivialEdit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := setup(t, pipelinetest.Settings{})
			h.ConfirmSpam(group, template)
			h.Overturn(group, tt.overturned)
			c := h.WA()
			m := fresh(h, 1)
			msg := textFrom(m, tt.sent)

			v := h.Deliver(msg)

			if n := revokes(c, group, msg.ID); n != 0 {
				t.Errorf("got %d Revoke calls, want 0 (an overturned fingerprint is allow-listed and checked first, DESIGN §7.1, §7.5)", n)
			}
			if n := removals(c, group, m); n != 0 {
				t.Errorf("got %d removals, want 0 (an overturned fingerprint never matches, DESIGN §7.5)", n)
			}
			if v.Source == pipeline.SourceKnown || v.Band == pipeline.BandCertain {
				t.Errorf("got band %q source %q, want not certain/known (the allow-list beats the corpus, DESIGN §7.5)", v.Band, v.Source)
			}
		})
	}
}
