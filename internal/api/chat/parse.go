package chat

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Command is a parsed command.
type Command struct {
	Name   string   // canonical English name without the prefix, e.g. "status"
	Args   []string // whitespace-separated arguments
	Rest   string   // everything after the name, trimmed (for phrases and reasons)
	Arabic bool     // the command was written with an Arabic alias, so the reply is in Arabic
}

// commands is every command name the bot knows (§13.7).
var commands = map[string]bool{
	"help": true, "groups": true, "use": true, "login": true,
	"set": true, "get": true, "settings": true,
	"status": true, "mode": true, "preset": true, "silent": true, "newsignal": true, "ladder": true,
	"policy": true, "block": true, "allow": true, "blocknew": true, "autoban": true, "askadmins": true,
	"ban": true, "kick": true, "unban": true, "unallow": true, "undo": true,
	"spam": true, "notspam": true, "ok": true,
	"domain": true, "word": true,
	"review": true, "pending": true, "reviewgroup": true,
	"blacklist": true,
}

// Aliases maps Arabic (and other) command names to their canonical names, e.g. "حالة" → "status".
var Aliases = map[string]string{
	"مساعدة": "help", "مجموعات": "groups", "المجموعات": "groups", "استخدم": "use", "دخول": "login",
	"ضبط": "set", "عرض": "get", "اعدادات": "settings", "الاعدادات": "settings",
	"حالة": "status", "الحالة": "status", "وضع": "mode", "نمط": "preset", "صامت": "silent",
	"سياسة": "policy", "منع": "block", "سماح": "allow", "منع_الجدد": "blocknew",
	"حظر_تلقائي": "autoban", "اسأل_المشرفين": "askadmins",
	"حظر": "ban", "طرد": "kick", "فك_الحظر": "unban", "الغاء_السماح": "unallow", "تراجع": "undo",
	"سبام": "spam", "ليس_سبام": "notspam", "سليم": "ok",
	"نطاق": "domain", "كلمة": "word",
	"مراجعة": "review", "معلق": "pending", "المعلقة": "pending", "مجموعة_المراجعة": "reviewgroup",
	"القائمة_السوداء": "blacklist", "سوداء": "blacklist",
}

// foldArabic removes tatweel and folds alef variants and taa marbuta so aliases match however
// they're typed.
var foldArabic = strings.NewReplacer("ـ", "", "أ", "ا", "إ", "ا", "آ", "ا", "ة", "ه", "ى", "ي")

func init() {
	folded := make(map[string]string, len(Aliases))
	for k, v := range Aliases {
		folded[foldArabic.Replace(k)] = v
	}
	for k, v := range folded {
		Aliases[k] = v
	}
}

// Parse reads a command from text. ok is false when text doesn't start with the prefix
// followed by a known command name or alias. Names are case-insensitive.
func Parse(text, prefix string) (c Command, ok bool) {
	if prefix == "" {
		prefix = DefaultPrefix
	}
	t := strings.TrimSpace(norm.NFKC.String(text))
	// Bidi marks often precede text typed in Arabic.
	t = strings.TrimLeftFunc(t, func(r rune) bool { return unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) })
	if !strings.HasPrefix(t, prefix) {
		return Command{}, false
	}
	t = strings.TrimSpace(t[len(prefix):])
	name, rest, _ := strings.Cut(t, " ")
	if i := strings.IndexAny(name, "\n\t"); i >= 0 {
		name, rest = name[:i], name[i+1:]+" "+rest
	}
	name = strings.ToLower(name)
	if !commands[name] {
		canon, ok := Aliases[name]
		if !ok {
			canon, ok = Aliases[foldArabic.Replace(name)]
		}
		if !ok {
			return Command{}, false
		}
		name, c.Arabic = canon, true
	}
	c.Name = name
	c.Rest = strings.TrimSpace(rest)
	c.Args = strings.Fields(c.Rest)
	return c, true
}
