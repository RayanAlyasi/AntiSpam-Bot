// The vCard parser: tolerant, for the vCards in WhatsApp contact messages (DESIGN.md §6.7).
// Spam hides in every field of a card, so it keeps every property, not only the ones a phone
// shows. It never fails: malformed lines are skipped, and input and output are bounded.

package adapter

import (
	"strings"
)

// Limits that keep a crafted card from costing more than a normal one.
const (
	MaxVCardInput      = 64 << 10 // bytes of vCard read; the rest is ignored
	MaxVCardProperties = 256      // properties kept per card
)

// VCardProperty is one vCard line: "item1.TEL;type=CELL;waid=123:+1 234".
type VCardProperty struct {
	Name   string              // upper case, without its group ("TEL")
	Params map[string][]string // parameter names in lower case; a bare "CELL" is a "type" value
	Value  string              // unescaped, decoded value
}

// VCard is a parsed vCard.
type VCard struct {
	Properties []VCardProperty
}

// Get returns the values of every property called name (case-insensitive), in order.
func (c VCard) Get(name string) []string {
	name = strings.ToUpper(name)
	var out []string
	for _, p := range c.Properties {
		if p.Name == name {
			out = append(out, p.Value)
		}
	}
	return out
}

// First returns the first value of the property called name, or "".
func (c VCard) First(name string) string {
	if v := c.Get(name); len(v) > 0 {
		return v[0]
	}
	return ""
}

// Phones returns the TEL values as written, and the waid= numbers (the WhatsApp number a TEL
// line points at, the one spammers advertise) in order and without duplicates.
func (c VCard) Phones() (phones, waids []string) {
	seen := map[string]bool{}
	for _, p := range c.Properties {
		if p.Name != "TEL" {
			continue
		}
		if p.Value != "" {
			phones = append(phones, p.Value)
		}
		for _, w := range p.Params["waid"] {
			w = digits(w)
			if w != "" && !seen[w] {
				seen[w] = true
				waids = append(waids, w)
			}
		}
	}
	return phones, waids
}

// Parse parses one vCard (or the first of several). Text outside BEGIN/END is still read, so a
// card missing its envelope lines is parsed too.
func ParseVCard(s string) VCard {
	if len(s) > MaxVCardInput {
		s = s[:MaxVCardInput]
	}
	var card VCard
	for _, line := range unfoldVCard(s) {
		if len(card.Properties) >= MaxVCardProperties {
			break
		}
		p, ok := parseVCardLine(line)
		if !ok {
			continue
		}
		switch p.Name {
		case "BEGIN", "VERSION":
			continue
		case "END":
			if len(card.Properties) > 0 {
				return card
			}
			continue
		}
		card.Properties = append(card.Properties, p)
	}
	return card
}

// unfoldVCard splits s into logical lines: a line starting with a space or tab continues the
// previous one, and a quoted-printable value ending in "=" continues on the next line. It runs
// in linear time.
func unfoldVCard(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var (
		out  []string
		cur  strings.Builder
		has  bool // cur holds a line
		qp   bool // the current logical line is quoted-printable
		soft bool // and its last part ended in a soft line break
	)
	write := func(part string) {
		soft = qp && strings.HasSuffix(part, "=")
		if soft {
			part = part[:len(part)-1]
		}
		cur.WriteString(part)
	}
	flush := func() {
		if has {
			out = append(out, cur.String())
		}
		cur.Reset()
		has, qp, soft = false, false, false
	}
	for _, raw := range strings.Split(s, "\n") {
		switch {
		case has && raw != "" && (raw[0] == ' ' || raw[0] == '\t'):
			write(raw[1:])
		case has && soft:
			write(raw)
		default:
			flush()
			has = true
			head, _, _ := strings.Cut(raw, ":")
			qp = strings.Contains(strings.ToUpper(head), "QUOTED-PRINTABLE")
			write(raw)
		}
	}
	flush()
	return out
}

// parseVCardLine parses "group.NAME;param=a,b;BARE:value".
func parseVCardLine(line string) (VCardProperty, bool) {
	head, value, ok := cutUnquoted(line)
	if !ok {
		return VCardProperty{}, false
	}
	parts := strings.Split(head, ";")
	name := strings.TrimSpace(parts[0])
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return VCardProperty{}, false
	}
	p := VCardProperty{Name: strings.ToUpper(name)}
	for _, param := range parts[1:] {
		k, v, hasValue := strings.Cut(param, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !hasValue {
			k, v = "type", k
		}
		if k == "" {
			continue
		}
		if p.Params == nil {
			p.Params = map[string][]string{}
		}
		for _, item := range strings.Split(v, ",") {
			p.Params[k] = append(p.Params[k], strings.Trim(strings.TrimSpace(item), `"`))
		}
	}
	if hasParam(p.Params, "encoding", "quoted-printable") || hasParam(p.Params, "type", "quoted-printable") {
		value = decodeQP(value)
	}
	p.Value = strings.TrimSpace(unescapeVCard(value, p.Name))
	return p, true
}

// cutUnquoted splits at the first ':' that isn't inside a double-quoted parameter value.
func cutUnquoted(line string) (string, string, bool) {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ':':
			if !quoted {
				return line[:i], line[i+1:], true
			}
		}
	}
	return "", "", false
}

func hasParam(params map[string][]string, key, value string) bool {
	for _, v := range params[key] {
		if strings.EqualFold(v, value) {
			return true
		}
	}
	return false
}

// unescapeVCard resolves vCard escapes. Structured values (N, ADR, ORG) separate components with
// ';', which become spaces so the text reads naturally.
func unescapeVCard(v, name string) string {
	structured := name == "N" || name == "ADR" || name == "ORG"
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(v[i])
			}
			continue
		}
		if c == ';' && structured {
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	if structured {
		out = strings.Join(strings.Fields(out), " ")
	}
	return out
}

// decodeQP decodes quoted-printable text; invalid escapes are kept as written.
func decodeQP(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '=' && i+2 < len(v) {
			if h, ok := unhex(v[i+1]); ok {
				if l, ok := unhex(v[i+2]); ok {
					b.WriteByte(h<<4 | l)
					i += 2
					continue
				}
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if '0' <= r && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
