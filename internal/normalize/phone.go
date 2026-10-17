package normalize

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nyaruka/phonenumbers"
)

// ErrInvalidPhone is returned by ParsePhone for input that isn't exactly one
// valid phone number.
var ErrInvalidPhone = errors.New("invalid phone number")

// Phone is a phone number found in text.
type Phone struct {
	// E164 is the number in E.164 form, e.g. +966551234567.
	E164 string
	// Start and End are byte offsets into the searched text covering the whole
	// written number, separators, invisibles and number words included.
	Start, End int
}

const (
	// maxPhoneGap is the most visible separator runes allowed between two
	// digit groups of one number, as in ") " or " - ".
	maxPhoneGap = 3
	// maxPhoneDigits bounds one candidate: 00 + a 15-digit E.164 number.
	maxPhoneDigits = 17
	// phoneParseBudget caps phonenumbers parses per call and per kind of
	// start. Real text needs a handful; a flood of digits is cut off instead of
	// taking seconds. Each kind of start has its own budget: plain digits;
	// number words and run starts; and numbers written internationally (+,
	// 00, a wa.me link). Noise of one kind can't use up another's budget, so
	// it can't hide an internationally written number behind it.
	phoneParseBudget = 4096
)

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// FindPhones returns the valid phone numbers in text, in order and without
// overlaps (DESIGN.md §7.2 step 7). It reads digits in any of ASCII,
// fullwidth, Arabic-Indic and Persian forms, spaced or separated digits,
// invisibles inside the number, wa.me links, and digits spelled out in
// English or Arabic. region is the group's default country (ISO 3166-1
// alpha-2); when it is empty or unknown, only numbers written
// internationally (+ or 00) are found. Only numbers phonenumbers rates valid
// are returned, so prices, dates and order numbers are not.
func FindPhones(text, region string) []Phone {
	region = strings.ToUpper(region)
	if !phonenumbers.GetSupportedRegions()[region] {
		region = ""
	}
	f := phoneFinder{text: text, region: region, budget: [3]int{phoneParseBudget, phoneParseBudget, phoneParseBudget}, memo: map[string]string{}}
	f.scan()
	return f.found
}

// SpelledDigitRuns returns the byte spans of runs of minDigits or more
// digits that are at least partly spelled out ("zero five five …"), valid
// numbers or not. Redaction masks them as a fallback, so no parse budget
// can leave a spelled-out number readable.
func SpelledDigitRuns(text string, minDigits int) [][2]int {
	f := phoneFinder{text: text, minWordRun: minDigits}
	f.scan()
	return f.wordRuns
}

// ParsePhone parses one phone number typed as a whole command argument, in
// any form FindPhones reads, and returns its E.164 form. Anything other than
// exactly one valid number is an error wrapping ErrInvalidPhone.
func ParsePhone(s, region string) (string, error) {
	phones := FindPhones(s, region)
	if len(phones) != 1 {
		return "", fmt.Errorf("%w: found %d numbers", ErrInvalidPhone, len(phones))
	}
	p := phones[0]
	before, after := s[:p.Start], s[p.End:]
	if link := phoneLinkPrefix.FindString(before); link != "" {
		before = before[len(link):]
		after = phoneLinkSuffix.ReplaceAllString(after, "")
	}
	if !onlyPhoneFiller(before) || !onlyPhoneFiller(after) {
		return "", fmt.Errorf("%w: text around the number", ErrInvalidPhone)
	}
	return p.E164, nil
}

// phoneLinkPrefix and phoneLinkSuffix let ParsePhone take a wa.me or
// api.whatsapp.com/send link as the number.
var (
	phoneLinkPrefix = regexp.MustCompile(`^\s*(?i:(?:https?://)?(?:www\.)?(?:wa\.me/|api\.whatsapp\.com/send/?\?(?:[^#\s]*&)?phone=))`)
	phoneLinkSuffix = regexp.MustCompile(`^[/?&#]\S*`)
)

func onlyPhoneFiller(s string) bool {
	for _, r := range s {
		if !isPhoneSeparator(r) && !isInvisible(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// digitGroup is a run of digits with no separator inside, or one number word.
type digitGroup struct {
	start, end int // byte span in the text
	digits     string
	intl       bool // preceded by +, or by a wa.me/ or phone= link
	word       bool // a spelled-out digit
	spanStart  int  // span start including a + or ( before the digits
}

type phoneFinder struct {
	text   string
	region string
	budget [3]int // parses left for plain, word or run-start, and international starts
	memo   map[string]string
	found  []Phone

	minWordRun int      // when set, collect spelled-out digit runs instead of parsing
	wordRuns   [][2]int // the runs collected

	run     []digitGroup
	gap     int  // visible separator runes since the last group
	plus    int  // byte offset of a pending +, or -1
	paren   int  // byte offset of a pending (, or -1
	inGroup bool // the last rune was a digit
}

func (f *phoneFinder) scan() {
	f.plus, f.paren = -1, -1
	t := f.text
	for i := 0; i < len(t); {
		r, w := utf8.DecodeRuneInString(t[i:])
		if d, ok := digitValue(r); ok {
			f.addDigit(i, i+w, d)
			i += w
			continue
		}
		f.inGroup = false
		switch {
		case unicode.IsLetter(r):
			end := wordEnd(t, i)
			if d, ok := numberWords[foldNumberWord(t[i:end])]; ok {
				f.addWord(i, end, d)
			} else {
				f.flush()
			}
			i = end
			continue
		case r == '+' || r == '＋':
			f.flush()
			f.plus = i
		case isInvisible(r):
		case isPhoneSeparator(r):
			f.gap++
			if f.gap > maxPhoneGap {
				f.flush()
			}
			if r == '(' {
				f.paren = i
			} else if f.plus >= 0 && !unicode.IsSpace(r) {
				f.plus = -1
			}
		default:
			f.flush()
		}
		i += w
	}
	f.flush()
}

func (f *phoneFinder) addDigit(start, end int, d byte) {
	if f.inGroup && len(f.run) > 0 {
		g := &f.run[len(f.run)-1]
		g.end = end
		if len(g.digits) <= maxPhoneDigits {
			g.digits += string('0' + d)
		}
		return
	}
	f.newGroup(start, end, string('0'+d))
	f.inGroup = true
}

func (f *phoneFinder) addWord(start, end int, d byte) {
	f.newGroup(start, end, string('0'+d))
	f.run[len(f.run)-1].word = true
	f.inGroup = false
}

func (f *phoneFinder) newGroup(start, end int, digits string) {
	g := digitGroup{start: start, end: end, digits: digits, spanStart: start}
	switch {
	case f.plus >= 0:
		g.intl, g.spanStart = true, f.plus
	case len(f.run) == 0 && hasLinkPrefix(f.text[:start]):
		g.intl = true
	}
	if f.paren >= 0 && f.paren < g.spanStart {
		g.spanStart = f.paren
	}
	f.plus, f.paren = -1, -1
	f.run = append(f.run, g)
	f.gap = 0
}

func (f *phoneFinder) flush() {
	if len(f.run) > 0 {
		if f.minWordRun > 0 {
			f.collectWordRun(f.run)
		} else {
			f.match(f.run)
		}
		f.run = f.run[:0]
	}
	f.gap = 0
	f.plus, f.paren = -1, -1
	f.inGroup = false
}

func (f *phoneFinder) collectWordRun(run []digitGroup) {
	digits, words := 0, false
	for _, g := range run {
		digits += len(g.digits)
		words = words || g.word
	}
	if words && digits >= f.minWordRun {
		f.wordRuns = append(f.wordRuns, [2]int{run[0].spanStart, run[len(run)-1].end})
	}
}

// match finds numbers in one run of digit groups: from each start it takes
// the longest prefix of whole groups that is a valid number, then continues
// after it, so two numbers written one after the other come out as two.
func (f *phoneFinder) match(run []digitGroup) {
	for i := 0; i < len(run) && f.budget[0]+f.budget[1]+f.budget[2] > 0; {
		kind := 0
		switch g := run[i]; {
		case g.intl || strings.HasPrefix(g.digits, "00"):
			kind = 2
		case i == 0 || g.word:
			kind = 1
		}
		ends := 0
		n := 0
		for j := i; j < len(run); j++ {
			n += len(run[j].digits)
			if n > maxPhoneDigits {
				break
			}
			ends = j - i + 1
		}
		found := false
		for k := ends - 1; k >= 0 && f.budget[kind] > 0; k-- {
			grp := run[i : i+k+1]
			if looksLikeDate(f.text, grp) {
				continue
			}
			if e164, ok := f.parse(grp, kind); ok {
				f.found = append(f.found, Phone{E164: e164, Start: grp[0].spanStart, End: grp[k].end})
				i += k + 1
				found = true
				break
			}
		}
		if !found {
			i++
		}
	}
}

func (f *phoneFinder) parse(grp []digitGroup, kind int) (string, bool) {
	var b strings.Builder
	for _, g := range grp {
		b.WriteString(g.digits)
	}
	digits := b.String()
	intl := grp[0].intl
	if !intl && strings.HasPrefix(digits, "00") {
		digits, intl = digits[2:], true
	}
	if intl {
		if len(digits) < 7 || len(digits) > 15 {
			return "", false
		}
		digits = "+" + digits
	} else if f.region == "" || len(digits) < 6 || len(digits) > 13 {
		return "", false
	}
	key := f.region + digits
	if e, ok := f.memo[key]; ok {
		return e, e != ""
	}
	f.budget[kind]--
	var num phonenumbers.PhoneNumber
	e := ""
	if phonenumbers.ParseToNumber(digits, f.region, &num) == nil && phonenumbers.IsValidNumber(&num) {
		if s := phonenumbers.Format(&num, phonenumbers.E164); e164Pattern.MatchString(s) {
			e = s
		}
	}
	f.memo[key] = e
	return e, e != ""
}

// looksLikeDate rejects three groups shaped like a date (2026-10-17,
// 17/10/2026, 1.2.2026) joined by date separators.
func looksLikeDate(text string, grp []digitGroup) bool {
	if len(grp) != 3 {
		return false
	}
	a, b, c := len(grp[0].digits), len(grp[1].digits), len(grp[2].digits)
	shaped := a == 4 && b <= 2 && c <= 2 || a <= 2 && b <= 2 && (c == 4 || c == 2)
	if !shaped {
		return false
	}
	for i := 1; i < 3; i++ {
		gap := strings.TrimSpace(text[grp[i-1].end:grp[i].start])
		if gap != "-" && gap != "/" && gap != "." {
			return false
		}
	}
	return true
}

func hasLinkPrefix(before string) bool {
	tail := before[max(0, len(before)-16):]
	tail = strings.ToLower(tail)
	return strings.HasSuffix(tail, "wa.me/") || strings.HasSuffix(tail, "phone=")
}

func isPhoneSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '-', '.', '(', ')', '/', '\u00A0', '‐', '‑', '‒', '–', '—', '−':
		return true
	}
	return unicode.Is(unicode.Zs, r)
}

// digitValue reads ASCII, fullwidth, Arabic-Indic, Persian and mathematical
// digits.
func digitValue(r rune) (byte, bool) {
	switch {
	case r >= '0' && r <= '9':
		return byte(r - '0'), true
	case r >= '٠' && r <= '٩':
		return byte(r - '٠'), true
	case r >= '۰' && r <= '۹':
		return byte(r - '۰'), true
	case r >= '０' && r <= '９':
		return byte(r - '０'), true
	case r >= 0x1D7CE && r <= 0x1D7FF:
		return byte((r - 0x1D7CE) % 10), true
	}
	return 0, false
}

// wordEnd returns the end of the word (letters and marks) starting at i.
func wordEnd(s string, i int) int {
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsLetter(r) && !unicode.IsMark(r) {
			break
		}
		i += w
	}
	return i
}

// foldNumberWord lower-cases a word and folds the Arabic spelling variants
// that number words are written with (alef forms, ة/ه, ى/ي, tashkeel).
func foldNumberWord(w string) string {
	if len(w) > 24 {
		return ""
	}
	return LooseArabic(FoldArabic(strings.ToLower(w)))
}

// numberWords are digits spelled out in English and Arabic, keyed by their
// foldNumberWord form.
var numberWords = map[string]byte{
	"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4,
	"five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9,
	"صفر":   0,
	"واحد":  1,
	"اثنين": 2, "اثنان": 2, "اثنتين": 2, "ثنين": 2, "اتنين": 2,
	"ثلاثه": 3, "ثلاث": 3, "تلاته": 3, "تلات": 3,
	"اربعه": 4, "اربع": 4,
	"خمسه": 5, "خمس": 5,
	"سته": 6, "ست": 6,
	"سبعه": 7, "سبع": 7,
	"ثمانيه": 8, "ثماني": 8, "ثمان": 8, "تمانيه": 8, "تمنيه": 8,
	"تسعه": 9, "تسع": 9,
}
