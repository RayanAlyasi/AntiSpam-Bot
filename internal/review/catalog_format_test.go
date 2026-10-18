package review_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
)

var catalogPlaceholder = regexp.MustCompile(`\{[a-z_]+\}`)

// TestCatalogFormatting: every built-in text exists in English and Arabic with the same
// placeholders, uses only formatting WhatsApp renders (no Markdown **, balanced *bold* on each
// line), and only the _web variants and the login link carry a {url}, so a bot without a
// website never sends an empty link.
func TestCatalogFormatting(t *testing.T) {
	en, ar := review.Texts["en"], review.Texts["ar"]
	for _, lang := range []map[string]string{en, ar} {
		for key := range lang {
			if _, ok := en[key]; !ok {
				t.Errorf("%s is only in Arabic, want it in English too", key)
			}
			if _, ok := ar[key]; !ok {
				t.Errorf("%s is only in English, want it in Arabic too", key)
			}
		}
	}
	for key, text := range en {
		if got, want := placeholders(ar[key]), placeholders(text); !slices.Equal(got, want) {
			t.Errorf("%s: Arabic uses %v, English %v; want the same placeholders", key, got, want)
		}
		for lang, s := range map[string]string{"en": text, "ar": ar[key]} {
			if strings.Contains(s, "**") {
				t.Errorf("%s %s = %q uses Markdown **, which WhatsApp doesn't render", lang, key, s)
			}
			for _, line := range strings.Split(s, "\n") {
				if strings.Count(line, "*")%2 != 0 {
					t.Errorf("%s %s line %q has an unbalanced *, want *bold* pairs", lang, key, line)
				}
			}
			if strings.Contains(s, "{url}") && !strings.HasSuffix(key, "_web") && key != "cmd.login" {
				t.Errorf("%s %s links {url}, want links only in _web variants (no website in v0.1)", lang, key)
			}
		}
	}
}

func placeholders(s string) []string {
	ps := catalogPlaceholder.FindAllString(s, -1)
	slices.Sort(ps)
	return slices.Compact(ps)
}
