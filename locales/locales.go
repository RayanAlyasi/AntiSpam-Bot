// Package locales holds every user-facing text (DESIGN.md §22.2): bot messages with several
// variants each, command names with their aliases, and review reply keywords, one YAML file per
// language. The files are embedded and checked when loaded: a key or placeholder that exists in
// one language only is an error, so a translation can't silently fall behind.
package locales

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

// Lang is a language code, also the locale file's name.
type Lang string

// Languages shipped with the bot. English is the fallback.
const (
	English Lang = "en"
	Arabic  Lang = "ar"
)

// MinVariants is how many variants every bot message needs, so the bot doesn't send identical
// texts over and over (DESIGN.md §10.3).
const MinVariants = 2

//go:embed *.yaml
var files embed.FS

// Catalog is a loaded, validated set of locales. It's read-only and safe for concurrent use.
type Catalog struct {
	langs    map[Lang]*locale
	commands map[string]string // any alias, folded → canonical command
	keywords map[string]string // any keyword, folded → canonical decision
}

type locale struct {
	Lang     Lang                `yaml:"lang"`
	Dir      string              `yaml:"dir"` // ltr or rtl
	Messages map[string][]string `yaml:"messages"`
	Commands map[string][]string `yaml:"commands"`
	Keywords map[string][]string `yaml:"keywords"`
}

var (
	loadOnce sync.Once
	loaded   *Catalog
	loadErr  error
)

// Load returns the embedded catalog.
func Load() (*Catalog, error) {
	loadOnce.Do(func() {
		srcs := map[Lang][]byte{}
		entries, err := files.ReadDir(".")
		if err != nil {
			loadErr = fmt.Errorf("locales: %w", err)
			return
		}
		for _, e := range entries {
			data, err := files.ReadFile(e.Name())
			if err != nil {
				loadErr = fmt.Errorf("locales: %w", err)
				return
			}
			srcs[Lang(strings.TrimSuffix(e.Name(), path.Ext(e.Name())))] = data
		}
		loaded, loadErr = Parse(srcs)
	})
	return loaded, loadErr
}

var (
	placeholder = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
	knownDirs   = []string{"ltr", "rtl"}
)

// Parse builds a catalog from locale files keyed by language. English is required. Every file
// must have the same message, command and keyword keys; every message needs MinVariants
// variants, and all variants in all languages must use the same {placeholders}. A command alias
// or keyword may mean only one thing across all languages.
func Parse(srcs map[Lang][]byte) (*Catalog, error) {
	c := &Catalog{langs: map[Lang]*locale{}, commands: map[string]string{}, keywords: map[string]string{}}
	for lang, data := range srcs {
		var l locale
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&l); err != nil {
			return nil, fmt.Errorf("locales/%s.yaml: %w", lang, err)
		}
		if l.Lang != lang {
			return nil, fmt.Errorf("locales/%s.yaml: lang is %q, want %q", lang, l.Lang, lang)
		}
		if !slices.Contains(knownDirs, l.Dir) {
			return nil, fmt.Errorf("locales/%s.yaml: dir %q, want ltr or rtl", lang, l.Dir)
		}
		c.langs[lang] = &l
	}
	en, ok := c.langs[English]
	if !ok {
		return nil, errors.New("locales: en.yaml is required")
	}

	var errs []error
	for _, lang := range c.Langs() {
		l := c.langs[lang]
		errs = append(errs, sameKeys(lang, "messages", l.Messages, en.Messages)...)
		errs = append(errs, sameKeys(lang, "commands", l.Commands, en.Commands)...)
		errs = append(errs, sameKeys(lang, "keywords", l.Keywords, en.Keywords)...)
		for key, variants := range l.Messages {
			if len(variants) < MinVariants {
				errs = append(errs, fmt.Errorf("locales/%s.yaml: %s has %d variants, want at least %d", lang, key, len(variants), MinVariants))
			}
			want := placeholders(en.Messages[key])
			for i, v := range variants {
				if got := placeholders([]string{v}); !slices.Equal(got, want) {
					errs = append(errs, fmt.Errorf("locales/%s.yaml: %s variant %d uses %v, want %v (as in en.yaml)", lang, key, i+1, got, want))
				}
				if strings.Count(v, "{") != strings.Count(v, "}") || strings.Count(v, "{") != len(placeholder.FindAllString(v, -1)) {
					errs = append(errs, fmt.Errorf("locales/%s.yaml: %s variant %d has a malformed placeholder", lang, key, i+1))
				}
			}
		}
		errs = append(errs, index(c.commands, lang, "command", l.Commands, true)...)
		errs = append(errs, index(c.keywords, lang, "keyword", l.Keywords, false)...)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

func sameKeys(lang Lang, section string, got, want map[string][]string) []error {
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(want)) {
		if _, ok := got[k]; !ok {
			errs = append(errs, fmt.Errorf("locales/%s.yaml: %s.%s is missing (it's in en.yaml)", lang, section, k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			errs = append(errs, fmt.Errorf("locales/%s.yaml: %s.%s is not in en.yaml", lang, section, k))
		}
		if len(got[k]) == 0 {
			errs = append(errs, fmt.Errorf("locales/%s.yaml: %s.%s is empty", lang, section, k))
		}
	}
	return errs
}

// placeholders returns the sorted, unique placeholder names in texts.
func placeholders(texts []string) []string {
	var names []string
	for _, t := range texts {
		for _, m := range placeholder.FindAllStringSubmatch(t, -1) {
			names = append(names, m[1])
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// index adds every word of words to idx, folded, mapping to its canonical key; a word that
// already means something else is an error. Command names themselves are always valid too.
func index(idx map[string]string, lang Lang, kind string, words map[string][]string, withKey bool) []error {
	var errs []error
	for _, canon := range slices.Sorted(maps.Keys(words)) {
		all := words[canon]
		if withKey {
			all = append([]string{canon}, all...)
		}
		for _, w := range all {
			f := fold(w)
			if f == "" {
				errs = append(errs, fmt.Errorf("locales/%s.yaml: empty %s for %s", lang, kind, canon))
				continue
			}
			if prev, ok := idx[f]; ok && prev != canon {
				errs = append(errs, fmt.Errorf("locales/%s.yaml: %s %q means both %s and %s", lang, kind, w, prev, canon))
				continue
			}
			idx[f] = canon
		}
	}
	return errs
}

// fold makes words comparable: lower case, trimmed, inner whitespace collapsed to one space.
func fold(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// Langs returns the loaded languages, sorted.
func (c *Catalog) Langs() []Lang { return slices.Sorted(maps.Keys(c.langs)) }

// Dir returns "rtl" or "ltr" for lang (English's for an unknown language).
func (c *Catalog) Dir(lang Lang) string { return c.locale(lang).Dir }

func (c *Catalog) locale(lang Lang) *locale {
	if l, ok := c.langs[lang]; ok {
		return l
	}
	return c.langs[English]
}

// Variants returns the variants of a message in lang, falling back to English for an unknown
// language. It returns nil for an unknown key.
func (c *Catalog) Variants(lang Lang, key string) []string {
	return slices.Clone(c.locale(lang).Messages[key])
}

// Render fills in variant number variant (taken modulo the number of variants, so a caller can
// pass any random or rotating number) of a message. Values are inserted as is, never expanded
// again. A missing value or an unknown key is an error.
func (c *Catalog) Render(lang Lang, key string, variant int, vars map[string]string) (string, error) {
	vs := c.locale(lang).Messages[key]
	if len(vs) == 0 {
		return "", fmt.Errorf("locales: unknown message %q", key)
	}
	tmpl := vs[((variant%len(vs))+len(vs))%len(vs)]
	var missing []string
	out := placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name]
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("locales: %s needs %v", key, missing)
	}
	return out, nil
}

// Command resolves a command word (without the prefix) in any language to its canonical name,
// e.g. "حالة" → "status". Matching ignores case and surrounding whitespace.
func (c *Catalog) Command(word string) (string, bool) {
	canon, ok := c.commands[fold(word)]
	return canon, ok
}

// Keyword resolves a review reply ("ban", "ليس سبام", "delete only") to its decision.
func (c *Catalog) Keyword(reply string) (string, bool) {
	canon, ok := c.keywords[fold(reply)]
	return canon, ok
}
