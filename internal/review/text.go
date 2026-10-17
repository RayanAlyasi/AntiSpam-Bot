package review

// Catalog is a Localizer backed by a map: language → key → text with {name} placeholders.
// A missing language falls back to English, and a missing key returns the key itself.
type Catalog map[string]map[string]string

// T returns the text for key in lang with vars filled in.
func (c Catalog) T(lang, key string, vars map[string]string) string {
	return c.lookup(lang, key, vars)
}

// Texts is the built-in English and Arabic catalog for review, blacklist and command replies.
var Texts = builtin
