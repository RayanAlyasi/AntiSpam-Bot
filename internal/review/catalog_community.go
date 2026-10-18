package review

// Replies about WhatsApp communities (DESIGN.md §12.4): the community lines of the numbered
// groups list, .use <community>, and the one answer a command sent to every group of a
// community gets. Merged into the built-in catalog like commandTexts.
var communityTexts = Catalog{
	"en": {
		"cmd.community_line":     "*{community}* (community, {n} groups)",
		"cmd.community_line_one": "*{community}* (community, 1 group)",
		"cmd.community_unnamed":  "Unnamed community",
		"cmd.using_community": "Now working on *{community}* for the next hour: the {n} groups you manage there.\n{groups}\n\n" +
			"Settings you change now apply to all of them, and new groups of this community start with them.{unprotected}",
		"cmd.community_none":      "You don't manage any group of *{community}* that I'm in. Send {prefix}groups to see your groups.",
		"cmd.community_one_group": "{cmd} works on one group at a time, not on all of *{community}*. Pick a group with {prefix}use and its number:\n{groups}",
		"cmd.community_done":      "✔ *{community}*: changed in {changed} of your {total} groups\n{command}{failed}{unprotected}",
		"cmd.community_failed":    "*Not changed:*\n{groups}",
		"cmd.community_unprotected": "*I can't protect these groups of {community}:*\n{groups}\n" +
			"Add me to each one and make me an admin there.",
		"cmd.community_not_member": "- {group}: I'm not in it",
		"cmd.community_not_admin":  "- {group}: I'm not an admin there",
		"cmd.community_today":      "today {deleted} deleted, {banned} banned",
		"cmd.community_status":     "*{community}* · {n} groups you manage\n{groups}{unprotected}",
	},
	"ar": {
		"cmd.community_line":     "*{community}* (مجتمع، {n} مجموعات)",
		"cmd.community_line_one": "*{community}* (مجتمع، مجموعة واحدة)",
		"cmd.community_unnamed":  "مجتمع بلا اسم",
		"cmd.using_community": "أعمل الآن على *{community}* لمدة ساعة: المجموعات التي تديرها فيه ({n}).\n{groups}\n\n" +
			"أي إعداد تغيّره الآن ينطبق عليها كلها، وتبدأ به المجموعات الجديدة في هذا المجتمع.{unprotected}",
		"cmd.community_none":      "لا تدير أي مجموعة في *{community}* أنا موجود فيها. أرسل {prefix}المجموعات لترى مجموعاتك.",
		"cmd.community_one_group": "يعمل {cmd} على مجموعة واحدة في كل مرة، لا على كل *{community}*. اختر مجموعة بـ {prefix}استخدم ورقمها:\n{groups}",
		"cmd.community_done":      "✔ *{community}*: تغيّر في {changed} من مجموعاتك الـ {total}\n{command}{failed}{unprotected}",
		"cmd.community_failed":    "*لم يتغيّر في:*\n{groups}",
		"cmd.community_unprotected": "*لا أستطيع حماية هذه المجموعات من {community}:*\n{groups}\n" +
			"أضفني إلى كل واحدة منها واجعلني مشرفاً فيها.",
		"cmd.community_not_member": "- {group}: لست فيها",
		"cmd.community_not_admin":  "- {group}: لست مشرفاً فيها",
		"cmd.community_today":      "اليوم {deleted} حذف، {banned} حظر",
		"cmd.community_status":     "*{community}* · {n} مجموعات تديرها\n{groups}{unprotected}",
	},
}

func init() {
	for lang, texts := range communityTexts {
		for k, v := range texts {
			builtin[lang][k] = v
		}
	}
}
