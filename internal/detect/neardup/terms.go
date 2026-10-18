package neardup

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// Match terms are what the Related band compares (DESIGN.md §7.5): the words of a normalized
// text that say what it is about. Links, redaction placeholders, short numbers, function words
// and greetings are dropped, because ordinary chat shares them with spam all the time (a group
// link alone is four tokens), and an Arabic word loses its leading article and conjunction, so
// و / ال / بال variants of the same word are one term.

var (
	// phoneIDRe matches a kept advertised number, [phone:<id>]: its id stays a term (the same
	// number is real evidence), the placeholder word doesn't.
	phoneIDRe = regexp.MustCompile(`\[phone:([0-9a-f]+)\]`)
	// linkRe matches links with a scheme or www, and bare domains with a path or a common TLD.
	linkRe = regexp.MustCompile(`(?i)(?:https?://|\bw{2,}\.)\S*|\b[a-z0-9-]+(?:\.[a-z0-9-]+)*\.(?:com|net|org|info|biz|io|me|ly|gl|co|app|link|site|online|store|shop|xyz|sa|ae|eg)\b(?:/\S*)?`)
)

// noiseWords is a closed list of Arabic and English function words, greetings and courtesy
// formulas, the words links and redaction leave behind, and the words that only describe a link
// ("join the group: <link>"), which the contact analyzer already counts. It is folded through
// the same normalizer and key as the texts at start-up.
const noiseWords = `
في من على علي الى إلى عن مع هذا هذه هذي ذا ذي ذلك تلك هناك هنا التي الذي الذين اللي الي
و او أو ثم بل لا ما لم لن ليس ان أن إن انه كان كانت يكون تكون كل جميع بعض غير بس عند عندي
عنده عندك لي لك له لها لنا لكم لهم هم هو هي انا أنا انت انتي انتم احنا نحن يا قد كما مثل حتى
اذا إذا لو بعد قبل فيه فيها منه منها عليه عليها به بها وش ايش شي شيء اي أي هل كيف متى وين
ليش لماذا مين يعني طيب
السلام سلام عليكم ورحمة ورحمه الله وبركاته وعليكم صباح مساء الخير النور اهلا وسهلا مرحبا
هلا شكرا جزاك جزاكم خيرا الحمدلله الحمد لله شاء انشاء ماشاء يعطيك يعطيكم العافية
عام وانتم بخير كريم مبارك مباركة عيد عيدكم رمضان جمعة
a an the and or but if then so of to in on at by for from with about as into is are was were
be been am do does did have has had can could will would should may might must i me my we us
our you your he him his she her it its they them their this that these those there here what
which who when where why how all any each every some no not yes yeah ok okay just also too
very please pls thanks thank thx hi hello hey dear guys everyone
http https www com net org phone email mention name whatsapp
link links group groups join channel click رابط الرابط قروب جروب مجموعة انضم انضمام قناة
telegram واتساب واتس وتساب تلغرام تليجرام
`

// articles are the definite-article prefixes stripped from an Arabic term (one of them, longest
// first), then a lone conjunction و, each only when at least minStemRunes letters remain. In that
// order والواجبات, الواجبات and واجبات all give the same term.
var articles = []string{"وال", "بال", "فال", "كال", "لل", "ال"}

const conjunction = "و"

const minStemRunes = 3

var (
	noiseKeys      map[string]bool
	articleKeys    []string
	conjunctionKey string
)

func init() {
	for _, p := range articles {
		articleKeys = append(articleKeys, Key(normalize.Text(p).Norm))
	}
	conjunctionKey = Key(normalize.Text(conjunction).Norm)
	noiseKeys = map[string]bool{}
	for _, w := range strings.Fields(noiseWords) {
		for _, t := range tokenize(Key(normalize.Text(w).Norm)) {
			noiseKeys[t] = true
			noiseKeys[stem(t)] = true
		}
	}
}

// Terms returns the distinct match terms of normalized text, in order of first appearance.
func Terms(norm string) []string {
	s := phoneIDRe.ReplaceAllString(norm, " $1 ")
	s = strings.ToLower(linkRe.ReplaceAllString(s, " "))
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, w := range tokenize(s) {
		// Numbers are taken as written: the confusables key would turn digits into letters.
		if number(w) {
			if utf8.RuneCountInString(w) >= minNumberRunes {
				add(w)
			}
			continue
		}
		for _, t := range tokenize(Key(w)) {
			// A lone letter ("e.g.", "U.S.") says nothing about the text.
			if noiseKeys[t] || utf8.RuneCountInString(t) < 2 {
				continue
			}
			if t = stem(t); !noiseKeys[t] {
				add(t)
			}
		}
	}
	return out
}

func stem(t string) string {
	if !arabic(t) {
		return t
	}
	for _, p := range articleKeys {
		if rest, ok := strings.CutPrefix(t, p); ok && utf8.RuneCountInString(rest) >= minStemRunes {
			t = rest
			break
		}
	}
	if rest, ok := strings.CutPrefix(t, conjunctionKey); ok && utf8.RuneCountInString(rest) >= minStemRunes {
		t = rest
	}
	return t
}

func arabic(t string) bool {
	for _, r := range t {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

// minNumberRunes is the fewest digits a number needs to be a term: shorter ones are prices,
// counts, times and dates.
const minNumberRunes = 3

func number(t string) bool {
	for _, r := range t {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return t != ""
}

// termHashes returns the sorted hashes of the match terms.
func termHashes(norm string) []uint64 {
	ts := Terms(norm)
	hs := make([]uint64, 0, len(ts))
	for _, t := range ts {
		hs = append(hs, hashString(t))
	}
	slices.Sort(hs)
	return slices.Compact(hs)
}

// shared counts the values two sorted, duplicate-free slices have in common.
func shared(a, b []uint64) int {
	n := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			n++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return n
}
