package script

import (
	"fmt"
	"testing"
)

func TestMixedWords(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string // fmt of the words found
	}{
		{"cyrillic e in latin word", "get frее crypto", "[frее]"},
		{"greek omicron in latin word", "bοnus offer", "[bοnus]"},
		{"cyrillic a in arabic-english message", "مرحبا join the pаypal team", "[pаypal]"},
		{"ordinary bilingual arabic english", "مرحبا everyone, الاجتماع tomorrow at 5pm في المكتب", "[]"},
		{"arabic glued to latin is not spoofing", "WhatsAppواتساب", "[]"},
		{"pure russian", "Привет всем, как дела?", "[]"},
		{"pure greek", "Καλημέρα σε όλους", "[]"},
		{"units and short tokens", "10Ωm 5μg", "[]"},
		{"latin with digits only", "covid19 h4ck", "[]"},
		{"accented latin", "café naïve résumé", "[]"},
		{"empty", "", "[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var words []string
			for _, w := range MixedWords(tt.text) {
				words = append(words, w.Word)
				if len(w.Scripts) < 2 {
					t.Errorf("word %q reported with scripts %v, want at least two", w.Word, w.Scripts)
				}
			}
			if got := fmt.Sprint(words); got != tt.want {
				t.Errorf("MixedWords(%q) = %s, want %s (ordinary multilingual text must not fire)", tt.text, got, tt.want)
			}
		})
	}
}

func FuzzMixedWords(f *testing.F) {
	for _, s := range []string{"frее", "", "\xff", "مرحبا hi"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, w := range MixedWords(s) {
			if len(w.Scripts) < 2 || w.Word == "" {
				t.Fatalf("bad result %+v", w)
			}
		}
	})
}
