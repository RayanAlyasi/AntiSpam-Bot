package review

import "strings"

// lookup implements Catalog.T.
func (c Catalog) lookup(lang, key string, vars map[string]string) string {
	text, ok := c[lang][key]
	if !ok {
		text, ok = c["en"][key]
	}
	if !ok {
		return key
	}
	if len(vars) == 0 {
		return text
	}
	pairs := make([]string, 0, 2*len(vars))
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// builtin is the English and Arabic text for review messages, command replies and notices.
// Keys are shared with the chat and blacklist packages.
var builtin = Catalog{
	"en": {
		"review.header":        "🛡️ *Review #{code}* · {group}",
		"review.header_hacked": "⚠️ *Review #{code}* · {group} · account may be hacked",
		"review.header_alert":  "🔔 *{group}*",
		"review.who":           "*Who:* {who}",
		"review.member_for":    "member for {age}",
		"review.earlier":       "{n} earlier messages",
		"review.what":          "*What:* {what}",
		"review.link":          "link",
		"review.why":           "*Why:* {why}",
		"review.score":         "Score: {score}",
		"review.source":        "Source: {source}",
		"review.msg_deleted":   "Message: deleted ✔",
		"review.msg_visible":   "Message: still visible",
		"review.why_asking":    "Why asking: {why}",
		"review.how":           "*Decide:* react ✅ to ban or ❌ if it's not spam,\nor reply ban, ok or delete only.",
		"review.how_hacked":    "*Decide:* react ✅ to ban or ❌ if it's not spam,\nor reply ban, ok, delete only, or clear (keep the hacked account).",
		"review.auto":          "_Auto-decision in {in}: {fallback}_",

		"review.fallback.keep_deleted_no_ban": "keep deleted, no ban",
		"review.fallback.delete_no_ban":       "delete, no ban",
		"review.fallback.auto_ban":            "ban",
		"review.fallback.dismiss":             "dismiss, restore trust",

		"review.decision.ban":         "banned",
		"review.decision.not_spam":    "not spam",
		"review.decision.delete_only": "deleted, no ban",
		"review.decision.clear":       "cleared",
		"review.decision.mute":        "muted",
		"review.decision.ignore":      "dismissed",

		"review.kind.ban_decision":     "ban?",
		"review.kind.uncertain":        "have a look",
		"review.kind.hacked":           "hacked account?",
		"review.kind.ask_before":       "waiting for you",
		"review.kind.network_incoming": "listed elsewhere",
		"review.kind.appeal":           "appeal",
		"review.kind.alert":            "notice",

		"review.handled":         "✔ Handled by {name} · {decision} · {time}",
		"review.already_handled": "Already handled by {name}.",
		"review.an_admin":        "an admin",
		"review.by_timeout":      "timeout",
		"review.timeout_note":    "⏱ #{code}: no decision in {timeout} → {fallback}",
	},
	"ar": {
		"review.header":        "🛡️ *مراجعة #{code}* · {group}",
		"review.header_alert":  "🔔 *{group}*",
		"review.header_hacked": "⚠️ *مراجعة #{code}* · {group} · قد يكون الحساب مخترقاً",
		"review.who":           "*من:* {who}",
		"review.member_for":    "عضو منذ {age}",
		"review.earlier":       "{n} رسالة سابقة",
		"review.what":          "*ماذا:* {what}",
		"review.link":          "رابط",
		"review.why":           "*لماذا:* {why}",
		"review.score":         "الدرجة: {score}",
		"review.source":        "المصدر: {source}",
		"review.msg_deleted":   "الرسالة: محذوفة ✔",
		"review.msg_visible":   "الرسالة: ما زالت ظاهرة",
		"review.why_asking":    "سبب السؤال: {why}",
		"review.how":           "*للبتّ:* تفاعل بـ ✅ للحظر أو ❌ إن لم تكن سبام،\nأو رُدّ: حظر أو ليس سبام أو حذف فقط.",
		"review.how_hacked":    "*للبتّ:* تفاعل بـ ✅ للحظر أو ❌ إن لم تكن سبام،\nأو رُدّ: حظر أو ليس سبام أو حذف فقط أو مسح (إبقاء الحساب المخترق).",
		"review.auto":          "_قرار تلقائي بعد {in}: {fallback}_",

		"review.fallback.keep_deleted_no_ban": "إبقاء الحذف بلا حظر",
		"review.fallback.delete_no_ban":       "حذف بلا حظر",
		"review.fallback.auto_ban":            "حظر",
		"review.fallback.dismiss":             "تجاهل واستعادة الثقة",

		"review.decision.ban":         "تم الحظر",
		"review.decision.not_spam":    "ليس سبام",
		"review.decision.delete_only": "حُذفت بلا حظر",
		"review.decision.clear":       "تمت التبرئة",
		"review.decision.mute":        "تم الكتم",
		"review.decision.ignore":      "تم التجاهل",

		"review.kind.ban_decision":     "حظر؟",
		"review.kind.uncertain":        "للاطلاع",
		"review.kind.hacked":           "حساب مخترق؟",
		"review.kind.ask_before":       "بانتظارك",
		"review.kind.network_incoming": "مدرج في مكان آخر",
		"review.kind.appeal":           "اعتراض",
		"review.kind.alert":            "تنبيه",

		"review.handled":         "✔ تمت المعالجة بواسطة {name} · {decision} · {time}",
		"review.already_handled": "تمت معالجتها مسبقاً بواسطة {name}.",
		"review.an_admin":        "أحد المشرفين",
		"review.by_timeout":      "انتهاء المهلة",
		"review.timeout_note":    "⏱ #{code}: لا قرار خلال {timeout} ← {fallback}",
	},
}
