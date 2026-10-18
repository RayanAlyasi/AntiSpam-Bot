package contact_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/contact"
)

// offerPhone is the first entry Offers returns when a message gives a phone number.
const offerPhone = "a phone number"

func TestPhoneShapesNotAPhone(t *testing.T) {
	tests := []struct{ name, text string }{
		// Times, prices and short dates written with dots or as lists of 1-2 digit groups.
		{"dotted times", "lectures at 08.00 09.30 11.00 tomorrow"},
		{"dotted times arabic", "المحاضرات 08.00 09.30 11.00 في القاعة"},
		{"two-digit-year date and time", "exam on 05-12-26 10.30 in hall B"},
		{"two-digit-year date and time arabic", "الاختبار 05-12-26 10.30 إن شاء الله"},
		{"prices", "copies cost 0.50 0.75 1.00 per page"},
		{"prices arabic", "الأسعار 0.50 0.75 1.00 ريال"},
		{"hyphen time slots", "office hours 08-30 09-30 10-30"},
		// Shapes that already weren't phones stay that way.
		{"times without leading zero", "slots 10.30 11.30 12.30"},
		{"date with four-digit year and time", "due 18.10.2026 09.30"},
		{"year list", "past papers 2023 2024 2025 2026"},
		{"student id without trunk zero", "my student id is 441234567"},
		// Glued to a Latin letter or followed by a letter: a code or reference, not a number to call.
		{"order code", "order A1234567890123 shipped"},
		{"product code with trunk zero", "model A0551234567 is out of stock"},
		{"two-letter code with trunk zero", "ref ZX0551234567 attached"},
		{"letter right after", "serial 0551234567X on the box"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contact.Offers(msg(t, tt.text)); slices.Contains(got, offerPhone) {
				t.Errorf("Offers(%q) = %q, want no %q (times, dates, prices, year lists and codes are not a phone; a false phone match on ordinary chat can send it to admins, DET-002)", tt.text, got, offerPhone)
			}
		})
	}
}

func TestPhoneShapesAreAPhone(t *testing.T) {
	tests := []struct{ name, text string }{
		{"two numbers one space apart", "call 0551234567 0559876543"},
		{"two numbers one space apart arabic", "للحجز 0551234567 0559876543"},
		{"number before slash date", "call 0551234567 12/5/2026"},
		{"number before slash date arabic", "للتواصل 0551234567 12/5/2026"},
		{"number before iso date", "call 0551234567 2026-05-12"},
		{"glued to arabic word", "واتساب0551234567"},
		{"glued to arabic word in a sentence", "للطلب تواصل واتساب0551234567 الآن"},
		{"glued to arabic word arabic-indic digits", "واتساب٠٥٥١٢٣٤٥٦٧"},
		// Shapes that already were phones keep firing.
		{"spaced national", "call 055 123 4567 for details"},
		{"hyphenated national", "call 055-123-4567"},
		{"country code without plus", "whatsapp 966551234567"},
		{"international", "Call +966 55 123 4567"},
		{"punctuated arabic", "تواصل:0551234567,example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := msg(t, tt.text)
			got := contact.Offers(e)
			if len(got) == 0 || got[0] != offerPhone {
				t.Fatalf("Offers(%q) = %q, want first entry %q (a phone-shaped number offered for contact, contract contact-phone-shapes)", tt.text, got, offerPhone)
			}
			sigs := run(t, e)
			if len(sigs) != 1 {
				t.Fatalf("got %d signals %+v, want exactly 1 contact.advertised (one signal per message, contract holdout-recall)", len(sigs), sigs)
			}
			s := sigs[0]
			if s.Code != contact.CodeAdvertised || s.Weight != 1.0 || s.Known || s.Hard {
				t.Errorf("got %s weight %v known=%v hard=%v, want contact.advertised weight 1.0, neither known nor hard (weight unchanged, DET-002)", s.Code, s.Weight, s.Known, s.Hard)
			}
			ev := strings.ReplaceAll(s.Evidence, " ", "")
			for _, n := range []string{"1234567", "9876543"} {
				if strings.Contains(ev, n) {
					t.Errorf("got evidence %q, want no part of the number (phone numbers are personal data, GO-003)", s.Evidence)
				}
			}
		})
	}
}
