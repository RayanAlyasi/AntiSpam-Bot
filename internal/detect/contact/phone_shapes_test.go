package contact

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

func TestNationalNumberShapes(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"dotted times", "lectures 08.00 09.30 11.00", false},
		{"dotted times in arabic", "المحاضرات 08.00 09.30 11.00 12.30", false},
		{"two-digit-year date and time", "الاختبار 05-12-26 10.30", false},
		{"prices", "0.50 0.75 1.00 ريال", false},
		{"hyphen time slots", "slots 08-30 09-30 10-30", false},
		{"three short groups beside longer ones", "رقم 055 12 34 56", false},
		{"lone zero is not a trunk prefix", "prices 0 50 100 150 200", false},
		{"arabic word glued to a number without a trunk 0", "رقم966551234567", false},
		{"latin letter glued before a trunk 0", "code A0551234567", false},
		{"letter glued after", "ref 0551234567X", false},
		{"spaced iban", "IBAN: SA03 8000 0000 6080 1016 7519", false},
		{"spaced iban in arabic", "التحويل على الآيبان SA44 2000 0001 2345 6789 1234", false},
		{"spaced card number", "card 4532 0151 1283 0366", false},
		{"spaced account number", "حساب 0108 0952 3456 7890 12", false},
		{"spaced reference", "ref 1234 0567 8901 2345", false},

		{"two numbers one space apart", "0551234567 0559876543", true},
		{"spaced number before another", "055 123 4567 0559876543", true},
		{"number before an iso date", "0551234567 2026-05-12", true},
		{"number before a slash date", "0551234567 12/5/2026", true},
		{"number after a date", "12 05 2026 0551234567", true},
		{"number after a short count", "1 0551234567", true},
		{"number between a count and a time", "2 0551234567 10.30", true},
		{"arabic word glued to a number", "واتساب0551234567", true},
		{"arabic word glued to a plus number", "واتس+966551234567", true},
		{"plus with short groups", "+966 55 123 4567", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nationalNumber(&envelope.Envelope{Texts: body(tt.text)}); got != tt.want {
				t.Fatalf("nationalNumber(%q) = %v, want %v (a false phone plus related wording sends ordinary chat to admins, DET-002)", tt.text, got, tt.want)
			}
		})
	}
}
