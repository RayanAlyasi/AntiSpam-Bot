package contact

import (
	"strings"
	"unicode"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

// privateWindow is how many words after an order or contact word the private channel may come.
const privateWindow = 4

// What a word does in an order-in-private phrase.
const (
	askNoun = 1 // an order, a question or a booking, which can be a plain noun ("الطلب", "استفسار")
	askCall = 2 // a call to order, get in touch, ask or book ("للطلب", "للتواصل", "اطلب", "contact")
)

var (
	askWords = map[string]int{
		"طلب": askNoun, "الطلب": askNoun, "طلبات": askNoun, "الطلبات": askNoun, "طلبك": askNoun,
		"طلبكم": askNoun, "تواصل": askNoun, "التواصل": askNoun, "تواصلوا": askNoun, "تواصلي": askNoun,
		"استفسار": askNoun, "الاستفسار": askNoun, "استفسارات": askNoun, "حجز": askNoun, "الحجز": askNoun,
		"order": askNoun, "orders": askNoun, "ordering": askNoun, "inquiry": askNoun,
		"inquiries": askNoun, "enquiry": askNoun, "enquiries": askNoun, "booking": askNoun,
		"bookings": askNoun, "contact": askNoun,

		"للطلب": askCall, "لطلب": askCall, "للطلبات": askCall, "اطلب": askCall, "اطلبي": askCall,
		"اطلبوا": askCall, "للتواصل": askCall, "للاستفسار": askCall, "للاستفسارات": askCall,
		"للحجز": askCall, "راسلنا": askCall,
	}
	// privateWords name a private chat or the group's admin.
	privateWords = setOf("الخاص", "بالخاص", "عالخاص", "خاصي", "ادمن", "الادمن", "للادمن", "بالادمن",
		"ادمنز", "الادمنز", "dm", "dms", "inbox", "admin", "admins", "privately")
	// callOnlyWords count only after a call to act: "طلب خاص" is a special request and "استفسار
	// خاص" a question about something, but "للطلب خاص" is "to order, write in private".
	callOnlyWords = setOf("خاص", "private")
	// usWords are "us", right after a word for getting in touch: "التواصل معنا" is "contact us",
	// but "تواصل الدكتور معنا" is "the doctor contacted us".
	usWords   = setOf("معنا", "معانا")
	touchWord = setOf("التواصل", "للتواصل", "تواصلوا", "تواصلي")
	// viaWords lead to the channel after a noun: "الطلب عن طريق الادمن", "orders via dm".
	viaWords = setOf("طريق", "عبر", "على", "ع", "via", "through", "thru")
	// staffWords are the group's admin only when a word for the group follows, with at most one
	// word between: alone, "المشرف" is as often a project supervisor.
	staffWords = setOf("مشرف", "المشرف", "للمشرف", "مشرفه", "مشرفة", "المشرفه", "المشرفة", "مشرفين", "المشرفين")
	groupWords = setOf("القروب", "قروب", "الجروب", "جروب", "المجموعه", "المجموعة", "group")
	// leadWords may also come first, right before a call to act: "بالخاص للطلب", "DM to order".
	leadWords = setOf("بالخاص", "عالخاص", "dm", "dms", "inbox")
	toWords   = setOf("to")
)

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// privateOrder reports whether a text surface asks the reader to order or get in touch in private
// or through the group's admin ("للطلب عن طريق مشرف القروب", "للتواصل خاص", "DM to order"): a
// contact channel without a number or a link.
func privateOrder(e *envelope.Envelope) bool {
	for i := range e.Texts {
		t := &e.Texts[i]
		s := t.Norm
		if s == "" {
			s = t.Raw
		}
		if privateOrderIn(wordsOf(s)) {
			return true
		}
	}
	return false
}

func privateOrderIn(words []string) bool {
	for i, w := range words {
		if leadWords[w] && leadsToCall(words, i) {
			return true
		}
		ask := askOf(words, i)
		if ask == 0 {
			continue
		}
		if touchWord[w] && i+1 < len(words) && usWords[words[i+1]] {
			return true
		}
		for j := i + 1; j < len(words) && j <= i+privateWindow; j++ {
			if channelAt(words, i, j, ask == askCall) {
				return true
			}
		}
	}
	return false
}

// askOf is what words[i] does as an ask word: an English noun right after "for" and right before
// the channel ("for orders dm") is a call to act, like "للطلب".
func askOf(words []string, i int) int {
	ask := askWords[words[i]]
	if ask == askNoun && i > 0 && words[i-1] == "for" && i+1 < len(words) && privateWords[words[i+1]] {
		return askCall
	}
	return ask
}

// leadsToCall reports whether the lead word at i comes right before a call to act: in Arabic
// directly ("بالخاص للطلب"), in English as "to" and an ask word ("DM to order").
func leadsToCall(words []string, i int) bool {
	if i+2 < len(words) && toWords[words[i+1]] && askWords[words[i+2]] != 0 {
		return true
	}
	return i+1 < len(words) && askWords[words[i+1]] == askCall
}

// channelAt reports whether words[j] names a private chat or the group's admin, for the ask word
// at i. A noun needs a via word right after it ("الطلب عن طريق", "orders via"); "اطلب من X" is
// "ask X"; a bare "خاص" counts only right after the call, since further on it is "special".
func channelAt(words []string, i, j int, call bool) bool {
	c := words[j]
	if !call && !viaWords[words[i+1]] && (words[i+1] != "عن" || i+2 >= len(words) || words[i+2] != "طريق") {
		return false
	}
	if call && strings.HasPrefix(words[i], "اطلب") && words[i+1] == "من" {
		return false
	}
	switch {
	case privateWords[c], call && callOnlyWords[c] && j == i+1:
		// "خاص بـ…" is "about …", not "in private".
		return !strings.Contains(c, "خاص") || j+1 == len(words) || !strings.HasPrefix(words[j+1], "ب")
	case staffWords[c]:
		for k := j + 1; k < len(words) && k <= j+2; k++ {
			if groupWords[words[k]] {
				return true
			}
		}
	}
	return false
}

// wordsOf splits s into lower-case words of letters and digits. An Arabic word loses a leading
// "و" (and) when it is a word of the lists without it.
func wordsOf(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, w := range fields {
		if rest, ok := strings.CutPrefix(w, "و"); ok && known(rest) && !known(w) {
			fields[i] = rest
		}
	}
	return fields
}

func known(w string) bool {
	return askWords[w] != 0 || privateWords[w] || callOnlyWords[w] || usWords[w] || staffWords[w] ||
		groupWords[w] || leadWords[w] || touchWord[w]
}
