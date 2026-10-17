package locales

import (
	"os"
	"strings"
	"testing"
)

func TestEmbeddedLocalesLoad(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("embedded locales are invalid: %v", err)
	}
	if got := c.Langs(); len(got) != 2 || got[0] != Arabic || got[1] != English {
		t.Fatalf("got languages %v, want [ar en] (Arabic and English come first, DESIGN §22.2)", got)
	}
	if c.Dir(Arabic) != "rtl" || c.Dir(English) != "ltr" {
		t.Fatal("got wrong text direction, want Arabic rtl")
	}
	for _, lang := range c.Langs() {
		for key := range c.langs[English].Messages {
			if n := len(c.Variants(lang, key)); n < MinVariants {
				t.Errorf("%s %s: got %d variants, want ≥ %d", lang, key, n, MinVariants)
			}
		}
	}
}

// The locale files must be complete in every language: a key or a placeholder set that exists
// in one language only fails the load.
func TestParseRejectsIncompleteTranslations(t *testing.T) {
	en := "lang: en\ndir: ltr\nmessages:\n  hi: [\"Hi {name}\", \"Hello {name}\"]\ncommands:\n  status: [status]\nkeywords:\n  ban: [ban]\n"
	ar := func(body string) string { return "lang: ar\ndir: rtl\n" + body }
	good := "messages:\n  hi: [\"مرحبا {name}\", \"أهلا {name}\"]\ncommands:\n  status: [حالة]\nkeywords:\n  ban: [حظر]\n"
	for _, tc := range []struct {
		name, ar string
		wantErr  string
	}{
		{"complete", ar(good), ""},
		{"message missing", ar("messages: {}\ncommands:\n  status: [حالة]\nkeywords:\n  ban: [حظر]\n"), "messages.hi is missing"},
		{"extra message", ar(strings.Replace(good, "messages:\n", "messages:\n  bye: [\"a\", \"b\"]\n", 1)), "messages.bye is not in en.yaml"},
		{"command missing", ar("messages:\n  hi: [\"مرحبا {name}\", \"أهلا {name}\"]\ncommands: {}\nkeywords:\n  ban: [حظر]\n"), "commands.status is missing"},
		{"keyword missing", ar("messages:\n  hi: [\"مرحبا {name}\", \"أهلا {name}\"]\ncommands:\n  status: [حالة]\nkeywords: {}\n"), "keywords.ban is missing"},
		{"placeholder differs", ar(strings.Replace(good, "أهلا {name}", "أهلا {user}", 1)), "variant 2 uses [user]"},
		{"placeholder dropped", ar(strings.Replace(good, "مرحبا {name}", "مرحبا", 1)), "variant 1 uses []"},
		{"one variant", ar(strings.Replace(good, ", \"أهلا {name}\"", "", 1)), "1 variants"},
		{"malformed placeholder", ar(strings.Replace(good, "مرحبا {name}", "مرحبا {name} {Bad}", 1)), "malformed placeholder"},
		{"command alias equal to a keyword", ar(strings.Replace(good, "status: [حالة]", "status: [ban, حالة]", 1)), ""},
		{"wrong lang field", strings.Replace(ar(good), "lang: ar", "lang: en", 1), "lang is"},
		{"bad dir", strings.Replace(ar(good), "rtl", "up", 1), "dir"},
		{"unknown section", ar(good + "extra: 1\n"), "extra"},
	} {
		_, err := Parse(map[Lang][]byte{English: []byte(en), Arabic: []byte(tc.ar)})
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: got %v, want no error", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: got %v, want an error mentioning %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestParseRejectsAmbiguousAliases(t *testing.T) {
	en := "lang: en\ndir: ltr\nmessages: {}\ncommands:\n  status: [status]\n  stats: [status]\nkeywords: {}\n"
	if _, err := Parse(map[Lang][]byte{English: []byte(en)}); err == nil || !strings.Contains(err.Error(), "means both") {
		t.Fatalf("got %v, want an error (one word must invoke exactly one command)", err)
	}
	if _, err := Parse(map[Lang][]byte{Arabic: []byte("lang: ar\ndir: rtl\n")}); err == nil {
		t.Fatal("got nil error without en.yaml, want an error (English is the fallback)")
	}
}

func TestRender(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"group": "Cars {link}", "preset": "Standard"}
	a, err := c.Render(English, "onboarding.protection_on", 0, vars)
	if err != nil || !strings.Contains(a, `"Cars {link}"`) || !strings.Contains(a, "Standard") {
		t.Fatalf("got %q, %v, want both values inserted verbatim (values are never expanded again)", a, err)
	}
	b, _ := c.Render(English, "onboarding.protection_on", 1, vars)
	if a == b {
		t.Fatal("got the same text for variants 0 and 1, want different texts (§10.3)")
	}
	if wrap, _ := c.Render(English, "onboarding.protection_on", -1, vars); wrap != b {
		t.Fatal("got a different text for variant -1, want it to wrap around like 1")
	}
	if ar, err := c.Render(Arabic, "onboarding.protection_on", 0, vars); err != nil || !strings.Contains(ar, "الحماية") {
		t.Fatalf("got %q, %v, want the Arabic text", ar, err)
	}
	if fr, _ := c.Render("fr", "command.done", 0, nil); fr != "Done." {
		t.Fatalf("got %q for an unknown language, want the English fallback", fr)
	}
	if _, err := c.Render(English, "onboarding.protection_on", 0, map[string]string{"group": "x"}); err == nil {
		t.Fatal("got nil error with a missing value, want an error (never send a half-filled template)")
	}
	if _, err := c.Render(English, "no.such.key", 0, nil); err == nil {
		t.Fatal("got nil error for an unknown key, want an error")
	}
}

func TestCommandsAndKeywords(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for word, want := range map[string]string{
		"status": "status", " STATUS ": "status", "حالة": "status", "ok": "notspam", "تمام": "notspam",
		"مساعدة": "help", "حظر": "ban", "طرد": "kick", "notspam": "notspam",
	} {
		if got, ok := c.Command(word); !ok || got != want {
			t.Errorf("Command(%q) = %q, %v; want %q (DESIGN §13.7 aliases)", word, got, ok, want)
		}
	}
	for reply, want := range map[string]string{
		"ban": "ban", "Kick": "ban", "طرد": "ban", "not  spam": "not_spam", "ليس سبام": "not_spam",
		"delete only": "delete_only", "حذف فقط": "delete_only", "كتم": "mute", "all spam": "all_spam",
	} {
		if got, ok := c.Keyword(reply); !ok || got != want {
			t.Errorf("Keyword(%q) = %q, %v; want %q (DESIGN §9.5 reply keywords)", reply, got, ok, want)
		}
	}
	if _, ok := c.Command("rm"); ok {
		t.Error("got a match for an unknown command, want none")
	}
}

// Every locale file on disk is embedded, so a new language can't be added without being checked.
func TestEveryLocaleFileIsEmbedded(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
			if _, loaded := c.langs[Lang(name)]; !loaded {
				t.Errorf("%s is not loaded", e.Name())
			}
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("lang: en\ndir: ltr\nmessages:\n  hi: [\"Hi {name}\", \"Yo {name}\"]\ncommands: {}\nkeywords: {}\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := Parse(map[Lang][]byte{English: data})
		if err != nil {
			return
		}
		for key := range c.langs[English].Messages {
			if len(c.Variants(English, key)) < MinVariants {
				t.Fatalf("accepted %s with too few variants", key)
			}
		}
	})
}
