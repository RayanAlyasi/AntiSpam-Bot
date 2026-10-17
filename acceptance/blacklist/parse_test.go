package blacklist_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/blacklist"
)

var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

func TestParseArgsFormats(t *testing.T) {
	tests := []struct {
		name, args, region, want string
	}{
		{"international with spaces", "+966 55 123 4567", region, listed},
		{"international compact", "+966551234567", region, listed},
		{"00 prefix", "00966551234567", region, listed},
		{"national with group country", "0551234567", region, listed},
		{"national with dashes", "055-123-4567", region, listed},
		{"Arabic-Indic digits", "٠٥٥١٢٣٤٥٦٧", region, listed},
		{"Arabic-Indic international", "+٩٦٦٥٥١٢٣٤٥٦٧", region, listed},
		{"wa.me link", "wa.me/966551234567", region, listed},
		{"https wa.me link", "https://wa.me/966551234567", region, listed},
		{"foreign number in a Saudi group", "+44 7911 123456", region, listedOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blacklist.ParseArgs(tt.args, tt.region)
			if !slices.Equal(got.Numbers, []string{tt.want}) || len(got.Invalid) != 0 {
				t.Errorf("ParseArgs(%q, %q) = Numbers %q, Invalid %q; want Numbers [%s], no Invalid "+
					"(every common written form normalizes to E.164, DESIGN §9.8)",
					tt.args, tt.region, got.Numbers, got.Invalid, tt.want)
			}
			if got.Reason != "" {
				t.Errorf("ParseArgs(%q).Reason = %q, want empty (no words after the number)", tt.args, got.Reason)
			}
		})
	}
}

func TestParseArgsSeveralNumbersAndReason(t *testing.T) {
	tests := []struct {
		name, args  string
		wantNumbers []string
		wantReason  string
	}{
		{
			"two numbers then reason", "+966551234567 0501234567 spam ads",
			[]string{listed, unlisted},
			"spam ads",
		},
		{
			"written order kept", "0501234567 0551234567",
			[]string{unlisted, listed},
			"",
		},
		{
			"duplicates in different forms removed", "+966551234567 00966551234567 0551234567 wa.me/966551234567",
			[]string{listed},
			"",
		},
		{
			"spaced number then reason", "+966 55 123 4567 sells fake followers",
			[]string{listed},
			"sells fake followers",
		},
		{
			"mixed forms and countries", "wa.me/447911123456 ٠٥٥١٢٣٤٥٦٧ crypto",
			[]string{listedOther, listed},
			"crypto",
		},
		{
			"digits inside the reason stay there", "0551234567 scam since 2024",
			[]string{listed},
			"scam since 2024",
		},
		{
			"Arabic reason", "0551234567 إعلانات مزعجة",
			[]string{listed},
			"إعلانات مزعجة",
		},
		{
			"reason trimmed", "   0551234567   spam   ",
			[]string{listed},
			"spam",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blacklist.ParseArgs(tt.args, region)
			if !slices.Equal(got.Numbers, tt.wantNumbers) {
				t.Errorf("ParseArgs(%q).Numbers = %q, want %q (several numbers per command, in written order, "+
					"deduplicated by E.164, DESIGN §9.8)", tt.args, got.Numbers, tt.wantNumbers)
			}
			if len(got.Invalid) != 0 {
				t.Errorf("ParseArgs(%q).Invalid = %q, want none (every number here is valid)", tt.args, got.Invalid)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("ParseArgs(%q).Reason = %q, want %q (the reason starts at the first word after the "+
					"numbers and is trimmed)", tt.args, got.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseArgsInvalidReported(t *testing.T) {
	tests := []struct {
		name, args  string
		wantNumbers []string
		wantInvalid []string
		wantReason  string
	}{
		{"too short", "+96612", nil, []string{"+96612"}, ""},
		{"too short then reason", "+96612 spam", nil, []string{"+96612"}, "spam"},
		{"valid and invalid", "0551234567 +96612 spam", []string{listed}, []string{"+96612"}, "spam"},
		{"invalid pieces joined by one space", "+966 12 34 crypto scam", nil, []string{"+966 12 34"}, "crypto scam"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blacklist.ParseArgs(tt.args, region)
			if !slices.Equal(got.Numbers, tt.wantNumbers) {
				t.Errorf("ParseArgs(%q).Numbers = %q, want %q", tt.args, got.Numbers, tt.wantNumbers)
			}
			if !slices.Equal(got.Invalid, tt.wantInvalid) {
				t.Errorf("ParseArgs(%q).Invalid = %q, want %q (invalid numbers are reported back in the form "+
					"written, never silently dropped, DESIGN §9.8)", tt.args, got.Invalid, tt.wantInvalid)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("ParseArgs(%q).Reason = %q, want %q", tt.args, got.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseArgsEmptyRegion(t *testing.T) {
	got := blacklist.ParseArgs("0551234567", "")
	if len(got.Numbers) != 0 || !slices.Equal(got.Invalid, []string{"0551234567"}) {
		t.Errorf("ParseArgs(national, no region) = Numbers %q, Invalid %q; want no numbers and Invalid [0551234567] "+
			"(a national number can't be resolved without the group's country, and must be reported)",
			got.Numbers, got.Invalid)
	}
	for _, args := range []string{"+966551234567", "+966 55 123 4567", "wa.me/966551234567", "https://wa.me/966551234567"} {
		got := blacklist.ParseArgs(args, "")
		if !slices.Equal(got.Numbers, []string{listed}) || len(got.Invalid) != 0 {
			t.Errorf("ParseArgs(%q, no region) = Numbers %q, Invalid %q; want [%s] "+
				"(international forms don't need a region)", args, got.Numbers, got.Invalid, listed)
		}
	}
}

func TestHasherKeyedHash(t *testing.T) {
	if blacklist.MinKeyLen != 16 {
		t.Errorf("MinKeyLen = %d, want 16 (a keyed hash needs at least 128 bits of key)", blacklist.MinKeyLen)
	}
	for _, n := range []int{0, 8, 15} {
		if _, err := blacklist.NewHasher(make([]byte, n)); !errors.Is(err, blacklist.ErrShortKey) {
			t.Errorf("NewHasher(%d-byte key) error = %v, want ErrShortKey (short keys make number hashes guessable)", n, err)
		}
	}
	if _, err := blacklist.NewHasher([]byte("0123456789abcdef")); err != nil {
		t.Errorf("NewHasher(16-byte key) error = %v, want nil (16 bytes is the minimum)", err)
	}

	h := newHasher(t)
	mac := hmac.New(sha256.New, hashKey)
	mac.Write([]byte(listed))
	want := hex.EncodeToString(mac.Sum(nil))
	got := h.Hash(listed)
	if got != want {
		t.Errorf("Hash(%s) = %q, want %q (lowercase hex HMAC-SHA256 of the E.164 string, the same value as "+
			"redact.PhoneHash, so blacklist and corpus number hashes match)", listed, got, want)
	}
	if h.Hash(listed) != got {
		t.Errorf("Hash is not deterministic, want the same hash for the same number (content matching depends on it)")
	}
	if h.Hash(unlisted) == got {
		t.Errorf("Hash(%s) == Hash(%s), want different hashes for different numbers", unlisted, listed)
	}
	other, err := blacklist.NewHasher([]byte("fedcba9876543210fedcba9876543210"))
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	if other.Hash(listed) == got {
		t.Errorf("hash is the same under two keys, want it keyed (a plain hash of a number is reversible by brute force, DESIGN §17)")
	}
	if strings.Contains(asciiDigits(got), "551234567") {
		t.Errorf("Hash(%s) = %q contains the number, want an opaque hash", listed, got)
	}
}

func TestMask(t *testing.T) {
	tests := []struct{ in, want string }{
		{listed, "+966•••••••67"},
		{listedOther, "+447•••••••56"},
		{"+14155550123", "+141••••••23"},
	}
	for _, tt := range tests {
		if got := blacklist.Mask(tt.in); got != tt.want {
			t.Errorf("Mask(%s) = %q, want %q (group admins see numbers masked except the country code "+
				"start and the last digits, DESIGN §9.8)", tt.in, got, tt.want)
		}
	}
}

func FuzzParseArgs(f *testing.F) {
	for _, s := range []string{
		"", " ", "+966 55 123 4567 spam", "00966551234567", "0551234567 0501234567 x",
		"٠٥٥١٢٣٤٥٦٧", "wa.me/966551234567", "https://wa.me/", "+", "+966 12 34 crypto",
		"spam 0551234567", "​0551234567", "۰۵۵۱۲۳۴۵۶۷ فارسی",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, args string) {
		got := blacklist.ParseArgs(args, region)
		seen := map[string]bool{}
		for _, n := range got.Numbers {
			if !e164Re.MatchString(n) {
				t.Errorf("ParseArgs(%q) returned %q, want only E.164 numbers (+ and 7-15 digits)", args, n)
			}
			if seen[n] {
				t.Errorf("ParseArgs(%q) returned %q twice, want numbers deduplicated", args, n)
			}
			seen[n] = true
		}
		if got.Reason != strings.TrimSpace(got.Reason) {
			t.Errorf("ParseArgs(%q).Reason = %q, want it trimmed", args, got.Reason)
		}
	})
}
