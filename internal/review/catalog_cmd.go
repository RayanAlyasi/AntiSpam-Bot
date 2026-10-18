package review

// Command replies, help pages, onboarding and blacklist notices, merged into the built-in
// catalog. Multi-line replies use WhatsApp formatting only: *bold* headings and labels, _italic_
// notes, real line breaks and numbered steps. Keys ending in _web are the variants that link the
// website; the chat package picks them only when a public URL is configured.
var commandTexts = Catalog{
	"en": {
		"cmd.yes": "yes",
		"cmd.no":  "no",

		"cmd.help": "*What I can do*\n\n" +
			"*Moderation* (reply to a message)\n" +
			"{prefix}spam – delete it and ban the sender\n" +
			"{prefix}ok – not spam, trust the sender again\n" +
			"{prefix}ban – ban someone (reply or @mention)\n" +
			"{prefix}kick – remove someone; they can rejoin\n" +
			"{prefix}unban +966551234567 – lift a ban\n\n" +
			"*Reviews*\n" +
			"{prefix}pending – cases waiting for your decision\n" +
			"{prefix}review K7Q2 ban – ban the sender of case #K7Q2\n" +
			"{prefix}review K7Q2 ok – case #K7Q2 isn't spam\n" +
			"{prefix}reviewgroup here – get cases in an admins-only group\n\n" +
			"*Settings*\n" +
			"{prefix}status – how this group is set up\n" +
			"{prefix}preset strict – choose how strict I am\n" +
			"{prefix}mode shadow – watch only, change nothing\n" +
			"{prefix}askadmins delete – delete spam right away, ask before banning\n" +
			"{prefix}settings – see the main settings\n\n" +
			"*Blacklist*\n" +
			"{prefix}blacklist add +966551234567 – block a phone number\n" +
			"{prefix}blacklist list – see the list\n\n" +
			"In a DM, pick a group with {prefix}use 2 or {prefix}use part of the name. If you manage one group, you don't need {prefix}use.\n" +
			"More help: {prefix}help moderation, {prefix}help reviews, {prefix}help settings or {prefix}help blacklist",
		"cmd.help.moderation": "*Moderation*\n" +
			"Reply to the message (or @mention the person) with:\n\n" +
			"{prefix}spam – delete it, ban the sender and remember it as spam\n" +
			"{prefix}ok – not spam: trust the sender again and forget the pattern\n" +
			"{prefix}ban spam – ban for spam (or {prefix}ban other)\n" +
			"{prefix}kick – remove them; they can rejoin\n" +
			"{prefix}unban +966551234567 – lift a ban\n\n" +
			"_Example:_ reply {prefix}ban spam to an ad.\n" +
			"I never act on group admins.",
		"cmd.help.reviews": "*Reviews*\n" +
			"When I'm not sure about a message, I open a case with a code like #K7Q2.\n\n" +
			"*Where cases go*\n" +
			"Without a review group, cases wait for you: send {prefix}pending to see them.\n" +
			"{prefix}reviewgroup here – send this in a separate admins-only group to get cases there as they come in\n" +
			"{prefix}reviewgroup off – stop using that group\n\n" +
			"*Deciding*\n" +
			"In the review group: react ✅ to ban or ❌ for not spam, or reply ban, ok or delete only.\n" +
			"Anywhere: {prefix}review K7Q2 ban or {prefix}review K7Q2 ok\n\n" +
			"*If nobody decides*\n" +
			"{prefix}review timeout 6h keep – after 6 hours, keep it deleted and ban no one\n" +
			"Instead of keep: delete, ban or dismiss.",
		"cmd.help.settings": "*Settings*\n" +
			"{prefix}status – how this group is set up\n" +
			"{prefix}settings – the main settings\n" +
			"{prefix}preset standard – how strict I am: relaxed, standard or strict\n" +
			"{prefix}preset ask – delete spam right away, and ask the admins before banning\n" +
			"{prefix}mode enforce – act on spam\n" +
			"{prefix}mode shadow – watch only: I note what I would do\n" +
			"{prefix}mode pause – stop acting until you switch back\n" +
			"{prefix}askadmins delete – delete spam right away, and ask the admins before banning\n" +
			"{prefix}askadmins before – keep the message until an admin decides\n" +
			"{prefix}askadmins off – act automatically\n" +
			"{prefix}block links – delete a type of message (also invites, images, videos…)\n" +
			"{prefix}allow stickers – allow a type again\n\n" +
			"Any setting by name: {prefix}get review.timeout, {prefix}set review.timeout 12h",
		"cmd.help.blacklist": "*Blacklist*\n" +
			"{prefix}blacklist add +966551234567 scam – ban this number here, now and if they come back\n" +
			"{prefix}blacklist remove +966551234567 – take it off the list\n" +
			"{prefix}blacklist list – see this group's list\n" +
			"{prefix}blacklist check +966551234567 – is it listed? is it in the group?\n\n" +
			"Numbers work in any format: 0551234567, 00966…, Arabic digits or wa.me links.",
		"cmd.help_unknown": "I don't have a help page called \"{topic}\". Try {prefix}help moderation, {prefix}help reviews, {prefix}help settings or {prefix}help blacklist.",

		"cmd.groups":        "*Your groups*\n{groups}\n\nPick one with {prefix}use 1, or part of its name.",
		"cmd.pick_group":    "*Which group?* Send {prefix}use 1, or part of its name.\n{groups}",
		"cmd.using":         "Now working on *{group}* for the next hour.",
		"cmd.login":         "Log in here: {url}",
		"cmd.login_no_site": "Logging in isn't part of this version. Send {prefix}help to see the commands.",
		"cmd.unavailable":   "{cmd} isn't available in this version.",

		"cmd.saved":               "✔ Saved for *{group}*\n{changes}",
		"cmd.value":               "{key} = {value}",
		"cmd.destination_pending": "no review group (cases wait for .pending)",
		"cmd.unknown_key":         "There's no setting called {key}. Send .settings for the main ones, or .help settings.",
		"cmd.invalid_value":       "{value} isn't a valid value for {key}: {reason}",
		"cmd.settings":            "*{group}*\n{summary}\n\nChange one with .set <key> <value>. More: .help settings",
		"cmd.settings_web":        "*{group}*\n{summary}\n\nAll settings: {url}",
		"cmd.status":              "*{group}*\n{summary}\n\n*Today:* {deleted} deleted · {banned} banned · {pending} waiting for review",
		"cmd.status_pending":      "*{group}*\n{summary}\n\n*Waiting for review:* {pending}",
		"cmd.status_brief":        "*{group}*\n{summary}",
		"cmd.preset":              "*{group}* now uses the *{preset}* preset. The auto-ban table was reset to match.",
		"cmd.autoban_grid":        "*Auto-ban table*\n{grid}",
		"cmd.autoban_ai_refused":  "AI evidence can't auto-ban here until it has a track record. Use known evidence, or ask.",

		"cmd.never_admin":     "I never act on group admins or on myself.",
		"cmd.ban_ask_reason":  "Banned. Reason? Reply spam or other (no answer counts as other).",
		"cmd.banned":          "Banned (reason: {reason}).",
		"cmd.kicked":          "Removed. They can rejoin.",
		"cmd.unbanned":        "Unbanned. They can rejoin with an invite; I don't re-add anyone.",
		"cmd.allowed":         "Added to the allow-list.",
		"cmd.unallowed":       "Removed from the allow-list.",
		"cmd.nothing_to_undo": "There's nothing I can undo in this group.",
		"cmd.undone":          "Undone ({kind}).",
		"cmd.spam_done":       "Deleted, banned and remembered as spam.",
		"cmd.notspam_done":    "Marked as not spam: trust restored and the pattern forgotten.",
		"cmd.list_saved":      "Saved: {value}",
		"cmd.word_dry_run":    "\"{phrase}\" would have matched {n} recent messages. Send the same command again within 10 minutes to save it.",

		"cmd.set_review_group":           "Set the review group by sending {prefix}reviewgroup here inside that group.",
		"cmd.review_dm_later":            "Reviews by DM aren't part of this version.\nSend {prefix}pending to see open cases, or {prefix}reviewgroup here in a separate admins-only group to get them as they come in.",
		"cmd.review_group_missing":       "No review group is set. Send .reviewgroup here in a separate admins-only group.",
		"cmd.review_dashboard_no_site":   "✔ Saved for *{group}*: cases no longer go to a review group.\nThey wait for you: send .pending to see them.",
		"cmd.review_timeout_ban_refused": "A timeout can't ban in ask-admins mode: choose keep, delete or dismiss.",
		"cmd.review_not_found":           "No review #{code}.",
		"cmd.review_decided":             "✔ #{code}: {decision}.",
		"cmd.pending":                    "*Waiting for a decision* ({n})\n{items}\n\nDecide with .review <code> ban or .review <code> ok.",
		"cmd.pending_none":               "Nothing is waiting for a decision.",
		"cmd.reviewgroup_protected":      "A protected group can't review itself. Send .reviewgroup here in a separate admins-only group.",
		"cmd.reviewgroup_pick":           "Which group should this chat review? Send {prefix}reviewgroup here 1, or part of its name.\n*Your groups*\n{groups}",
		"cmd.reviewgroup_set":            "✔ This chat now gets the review cases of *{group}*. React or reply to each one to decide.",
		"cmd.reviewgroup_off":            "*{group}* no longer sends cases to a review group. They wait for you: send .pending to see them.",
		"cmd.reviewgroup_outsiders":      "⚠️ {n} people here aren't admins of {group}. They'll see the evidence in reviews.",

		"cmd.blacklist_added":          "*Blacklisted:* {added}\n*Already listed:* {already}\n*Not valid numbers:* {invalid}\n*Banned now:* {banned} · *admins skipped:* {skipped} · *sent to review:* {review}",
		"cmd.blacklist_removed":        "*Removed:* {removed}\n*Not listed:* {notfound}\n*Not valid numbers:* {invalid}",
		"cmd.blacklist_list":           "*Blacklist*\n{entries}",
		"cmd.blacklist_empty":          "The blacklist is empty.",
		"cmd.blacklist_invalid":        "{input} isn't a valid phone number.",
		"cmd.blacklist_operator_only":  "Only the operator can change the instance-wide blacklist.",
		"cmd.blacklist_check_group":    "{number} is on this group's blacklist.\n*Member now:* {member}",
		"cmd.blacklist_check_instance": "{number} is on the instance-wide blacklist.\n*Member now:* {member}",
		"cmd.blacklist_check_no":       "{number} isn't blacklisted here.\n*Member now:* {member}",
		"blacklist.admin_skipped":      "{number} is blacklisted but is an admin of {group}, so I didn't act on them.",

		"cmd.usage.set":            "Send a setting and its value:\n.set review.timeout 12h – wait 12 hours for a decision",
		"cmd.usage.get":            "Send a setting's name:\n.get review.timeout – show how long I wait for a decision",
		"cmd.usage.mode":           "Choose a mode:\n.mode enforce – act on spam\n.mode shadow – watch only, change nothing\n.mode pause – stop acting until you switch back",
		"cmd.usage.preset":         "Choose how strict I am:\n.preset relaxed – act only on clear spam\n.preset standard – the default\n.preset strict – act sooner\n.preset ask – delete spam right away, and ask the admins before banning",
		"cmd.usage.toggle":         "Add on or off, for example .silent on",
		"cmd.usage.policy":         "Choose what happens to messages that break the group's rules:\n.policy delete – delete them\n.policy ask – ask the admins\n.policy ban – delete and ban",
		"cmd.usage.content":        "Name a type of message:\n.block links – delete links\n.allow stickers – allow stickers again\n*Types:* links, invites, channels, contacts, images, videos, stickers, documents, audio, polls, locations, events, view_once",
		"cmd.usage.autoban":        ".autoban – show the auto-ban table\n.autoban links auto – ban known link spam without asking\n.autoban links ask – ask the admins first\nOther actions: delete, allow",
		"cmd.usage.askadmins":      "Choose when I ask the admins:\n.askadmins delete – delete spam right away, and ask the admins before banning\n.askadmins before – keep the message until an admin decides\n.askadmins off – act automatically",
		"cmd.usage.ban":            "Reply to the message (or @mention the person) with:\n.ban spam – ban and remember it as spam\n.ban other – ban for another reason",
		"cmd.usage.kick":           "Reply to the message (or @mention the person) with:\n.kick – remove them; they can rejoin",
		"cmd.usage.unban":          "Send the number, or reply to their message:\n.unban +966551234567 – lift the ban",
		"cmd.usage.allow":          "Reply to the message (or @mention the person) with:\n.allow – never flag this member\n.unallow – undo that",
		"cmd.usage.spam":           "Reply .spam to the spam message.",
		"cmd.usage.notspam":        "Reply .ok to the message that isn't spam.",
		"cmd.usage.domain":         "Send a domain:\n.domain block example.com – delete links to it\n.domain allow example.com – always allow it",
		"cmd.usage.word":           "Send a phrase:\n.word block free money – delete messages with it\n.word unblock free money – stop that",
		"cmd.usage.review":         "*Reviews*\n.pending – see open cases\n.review K7Q2 ban – ban the sender of case #K7Q2\n.review K7Q2 ok – not spam\n.review K7Q2 delete only – delete, no ban",
		"cmd.usage.review_timeout": "Say how long to wait and what to do then:\n.review timeout 6h keep – keep it deleted, no ban\n.review timeout 6h delete – delete it, no ban\n.review timeout 6h ban – ban\n.review timeout 6h dismiss – treat it as not spam",
		"cmd.usage.reviewgroup":    "In a separate admins-only group, send:\n.reviewgroup here – get this group's cases here\n.reviewgroup off – stop",
		"cmd.usage.blacklist":      "*Blacklist*\n.blacklist add +966551234567 scam – ban this number\n.blacklist remove +966551234567 – take it off\n.blacklist list – see the list\n.blacklist check +966551234567 – is it listed?",

		"onboard.start": "Hi! 👋 I keep WhatsApp groups free of spam, and I never post in them.\n\n" +
			"*Three steps to start*\n" +
			"1. Add me to your group.\n" +
			"2. Make me an admin there.\n" +
			"3. Choose where uncertain cases go:\n" +
			"- send .pending here to see them with their codes, then decide one: .review K7Q2 ban or .review K7Q2 ok\n" +
			"- or send .reviewgroup here in a separate admins-only group to get them as they come in\n\n" +
			"Send .help for all commands.",
		"onboard.start_web": "Hi! 👋 I keep WhatsApp groups free of spam, and I never post in them.\n\n" +
			"*Three steps to start*\n" +
			"1. Add me to your group.\n" +
			"2. Make me an admin there.\n" +
			"3. Choose where uncertain cases go: the website, or send .reviewgroup here in a separate admins-only group to get them as they come in.\n\n" +
			"Send .help for all commands. Website: {url}",
		"onboard.promoted": "🛡️ Protection is on in *{group}*. I stay silent there.\n\n" +
			"When I'm not sure about a message, I keep it as a case for you:\n" +
			"- send .pending here to see them with their codes, then decide one: .review K7Q2 ban or .review K7Q2 ok\n" +
			"- or send .reviewgroup here in a separate admins-only group to get them as they come in\n\n" +
			"_By default, if nobody decides in time, nobody is banned._",
		"onboard.promoted_web": "🛡️ Protection is on in *{group}*. I stay silent there.\n\n" +
			"When I'm not sure about a message, I keep it as a case for you on the website: {url}\n" +
			"To get cases in WhatsApp, send .pending here, or .reviewgroup here in a separate admins-only group.\n" +
			"_By default, if nobody decides in time, nobody is banned._",
		"onboard.not_admin":        "⚠️ I'm in {group} but I'm not an admin there, so I can't protect it. Make me an admin and I'll start.",
		"onboard.joined":           "✔ Joined {group}. Make me an admin there to turn protection on.",
		"onboard.join_not_admin":   "You need to be an admin of {group} for me to join it.",
		"onboard.join_bad_link":    "That invite link doesn't work.",
		"onboard.join_unavailable": "I can't join groups from invite links in this version.\nAdd me to the group yourself, then make me an admin there.",
		"onboard.join_limit":       "I've joined enough groups for today. Please try again tomorrow.",
	},
	"ar": {
		"cmd.yes": "نعم",
		"cmd.no":  "لا",

		"cmd.help": "*ما الذي أستطيع فعله*\n\n" +
			"*الإشراف* (بالرد على رسالة)\n" +
			"{prefix}سبام – حذفها وحظر المرسل\n" +
			"{prefix}سليم – ليست سبام، واستعادة الثقة بالمرسل\n" +
			"{prefix}حظر – حظر شخص (بالرد أو الإشارة)\n" +
			"{prefix}طرد – إزالة شخص مع السماح بعودته\n" +
			"{prefix}فك_الحظر \u200e+966551234567 – رفع الحظر\n\n" +
			"*المراجعات*\n" +
			"{prefix}معلق – الحالات التي تنتظر قرارك\n" +
			"{prefix}مراجعة K7Q2 ban – حظر مرسل الحالة \u200e#K7Q2\n" +
			"{prefix}مراجعة K7Q2 ok – الحالة \u200e#K7Q2 ليست سبام\n" +
			"{prefix}مجموعة_المراجعة here – استلام الحالات في مجموعة للمشرفين فقط\n\n" +
			"*الإعدادات*\n" +
			"{prefix}حالة – إعداد هذه المجموعة\n" +
			"{prefix}نمط strict – درجة الصرامة\n" +
			"{prefix}وضع shadow – المراقبة فقط دون أي إجراء\n" +
			"{prefix}اسأل_المشرفين delete – حذف السبام فوراً والسؤال قبل الحظر\n" +
			"{prefix}اعدادات – أهم الإعدادات\n\n" +
			"*القائمة السوداء*\n" +
			"{prefix}القائمة_السوداء add \u200e+966551234567 – منع رقم هاتف\n" +
			"{prefix}القائمة_السوداء list – عرض القائمة\n\n" +
			"في الخاص، اختر المجموعة بـ {prefix}استخدم 2 أو {prefix}استخدم جزء من الاسم. إن كنت تدير مجموعة واحدة فلا حاجة إلى {prefix}استخدم.\n" +
			"للمزيد: {prefix}مساعدة الإشراف، {prefix}مساعدة المراجعات، {prefix}مساعدة الإعدادات أو {prefix}مساعدة القائمة_السوداء",
		"cmd.help.moderation": "*الإشراف*\n" +
			"رُدّ على الرسالة (أو أشر إلى الشخص) بـ:\n\n" +
			"{prefix}سبام – حذفها وحظر المرسل وحفظها كسبام\n" +
			"{prefix}سليم – ليست سبام: استعادة الثقة ونسيان النمط\n" +
			"{prefix}حظر سبام – حظر بسبب السبام (أو {prefix}حظر أخرى)\n" +
			"{prefix}طرد – إزالته مع السماح بعودته\n" +
			"{prefix}فك_الحظر \u200e+966551234567 – رفع الحظر\n\n" +
			"_مثال:_ رُدّ بـ {prefix}حظر سبام على إعلان.\n" +
			"لا أتخذ أي إجراء ضد مشرفي المجموعة.",
		"cmd.help.reviews": "*المراجعات*\n" +
			"عندما لا أكون متأكداً من رسالة، أفتح حالة برمز مثل \u200e#K7Q2.\n\n" +
			"*أين تذهب الحالات*\n" +
			"دون مجموعة مراجعة، تنتظرك الحالات: أرسل {prefix}معلق لعرضها.\n" +
			"{prefix}مجموعة_المراجعة here – أرسلها في مجموعة منفصلة للمشرفين فقط لتصلك الحالات فيها أولاً بأول\n" +
			"{prefix}مجموعة_المراجعة off – التوقف عن استخدام تلك المجموعة\n\n" +
			"*البتّ*\n" +
			"في مجموعة المراجعة: تفاعل بـ ✅ للحظر أو ❌ إن لم تكن سبام، أو رُدّ: حظر أو ليس سبام أو حذف فقط.\n" +
			"من أي مكان: {prefix}مراجعة K7Q2 ban أو {prefix}مراجعة K7Q2 ok\n\n" +
			"*إن لم يقرر أحد*\n" +
			"{prefix}مراجعة timeout 6h keep – بعد ٦ ساعات تبقى الرسالة محذوفة ولا يُحظر أحد\n" +
			"بدلاً من keep: delete أو ban أو dismiss.",
		"cmd.help.settings": "*الإعدادات*\n" +
			"{prefix}حالة – إعداد هذه المجموعة\n" +
			"{prefix}اعدادات – أهم الإعدادات\n" +
			"{prefix}نمط standard – درجة الصرامة: relaxed أو standard أو strict\n" +
			"{prefix}نمط ask – حذف السبام فوراً، وسؤال المشرفين قبل الحظر\n" +
			"{prefix}وضع enforce – التصرف مع السبام\n" +
			"{prefix}وضع shadow – المراقبة فقط: أسجّل ما كنت سأفعله\n" +
			"{prefix}وضع pause – التوقف حتى تعيد التشغيل\n" +
			"{prefix}اسأل_المشرفين delete – حذف السبام فوراً، وسؤال المشرفين قبل الحظر\n" +
			"{prefix}اسأل_المشرفين before – إبقاء الرسالة حتى يقرر أحد المشرفين\n" +
			"{prefix}اسأل_المشرفين off – التصرف تلقائياً\n" +
			"{prefix}منع links – حذف نوع من الرسائل (وأيضاً invites وimages وvideos…)\n" +
			"{prefix}سماح stickers – السماح بنوع من جديد\n\n" +
			"أي إعداد باسمه: {prefix}عرض review.timeout، {prefix}ضبط review.timeout 12h",
		"cmd.help.blacklist": "*القائمة السوداء*\n" +
			"{prefix}القائمة_السوداء add \u200e+966551234567 احتيال – حظر الرقم هنا الآن وإن عاد\n" +
			"{prefix}القائمة_السوداء remove \u200e+966551234567 – إزالته من القائمة\n" +
			"{prefix}القائمة_السوداء list – عرض قائمة هذه المجموعة\n" +
			"{prefix}القائمة_السوداء check \u200e+966551234567 – هل هو مدرج؟ هل هو في المجموعة؟\n\n" +
			"تُقبل الأرقام بأي صيغة: 0551234567 أو 00966… أو بالأرقام العربية أو روابط wa.me.",
		"cmd.help_unknown": "لا توجد صفحة مساعدة باسم \"{topic}\". جرّب {prefix}مساعدة الإشراف أو {prefix}مساعدة المراجعات أو {prefix}مساعدة الإعدادات أو {prefix}مساعدة القائمة_السوداء.",

		"cmd.groups":        "*مجموعاتك*\n{groups}\n\nاختر واحدة بـ {prefix}استخدم 1 أو جزء من اسمها.",
		"cmd.pick_group":    "*أي مجموعة؟* أرسل {prefix}استخدم 1 أو جزءاً من اسمها.\n{groups}",
		"cmd.using":         "أعمل الآن على *{group}* لمدة ساعة.",
		"cmd.login":         "سجّل الدخول من هنا: {url}",
		"cmd.login_no_site": "تسجيل الدخول ليس جزءاً من هذا الإصدار. أرسل {prefix}مساعدة لعرض الأوامر.",
		"cmd.unavailable":   "الأمر {cmd} غير متاح في هذا الإصدار.",

		"cmd.saved":               "✔ تم الحفظ لـ *{group}*\n{changes}",
		"cmd.value":               "{key} = {value}",
		"cmd.destination_pending": "بلا مجموعة مراجعة (الحالات تنتظر .معلق)",
		"cmd.unknown_key":         "لا يوجد إعداد باسم {key}. أرسل .اعدادات لأهم الإعدادات، أو .مساعدة الإعدادات.",
		"cmd.invalid_value":       "{value} ليست قيمة صحيحة لـ {key}: {reason}",
		"cmd.settings":            "*{group}*\n{summary}\n\nلتغيير إعداد: \u200e.set <key> <value>. للمزيد: .مساعدة الإعدادات",
		"cmd.settings_web":        "*{group}*\n{summary}\n\nكل الإعدادات: {url}",
		"cmd.status":              "*{group}*\n{summary}\n\n*اليوم:* {deleted} حذف · {banned} حظر · {pending} بانتظار المراجعة",
		"cmd.status_pending":      "*{group}*\n{summary}\n\n*بانتظار المراجعة:* {pending}",
		"cmd.status_brief":        "*{group}*\n{summary}",
		"cmd.preset":              "تستخدم *{group}* الآن النمط *{preset}*، وأُعيد ضبط جدول الحظر التلقائي ليطابقه.",
		"cmd.autoban_grid":        "*جدول الحظر التلقائي*\n{grid}",
		"cmd.autoban_ai_refused":  "لا يمكن لأدلة الذكاء الاصطناعي الحظر تلقائياً هنا قبل أن يكون لها سجل موثوق. استخدم الأدلة المعروفة أو اختر السؤال.",

		"cmd.never_admin":     "لا أتخذ أي إجراء ضد المشرفين أو ضد نفسي.",
		"cmd.ban_ask_reason":  "تم الحظر. ما السبب؟ رُدّ بـ سبام أو أخرى (عدم الرد يُعد أخرى).",
		"cmd.banned":          "تم الحظر (السبب: {reason}).",
		"cmd.kicked":          "تمت الإزالة، ويمكنه العودة.",
		"cmd.unbanned":        "تم فك الحظر. يمكنه العودة برابط دعوة؛ لا أضيف أحداً.",
		"cmd.allowed":         "أُضيف إلى قائمة السماح.",
		"cmd.unallowed":       "أُزيل من قائمة السماح.",
		"cmd.nothing_to_undo": "لا يوجد ما يمكن التراجع عنه في هذه المجموعة.",
		"cmd.undone":          "تم التراجع ({kind}).",
		"cmd.spam_done":       "حُذفت الرسالة وحُظر المرسل وحُفظت كسبام.",
		"cmd.notspam_done":    "اعتُبرت ليست سبام: استُعيدت الثقة ونُسي النمط.",
		"cmd.list_saved":      "تم الحفظ: {value}",
		"cmd.word_dry_run":    "\"{phrase}\" كانت ستطابق {n} رسالة حديثة. أرسل الأمر نفسه مرة أخرى خلال 10 دقائق للحفظ.",

		"cmd.set_review_group":           "اضبط مجموعة المراجعة بإرسال \u200e{prefix}reviewgroup here داخل تلك المجموعة.",
		"cmd.review_dm_later":            "المراجعة عبر الخاص ليست جزءاً من هذا الإصدار.\nأرسل {prefix}معلق لعرض الحالات المفتوحة، أو {prefix}مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط لتصلك أولاً بأول.",
		"cmd.review_group_missing":       "لم تُضبط مجموعة مراجعة. أرسل .مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط.",
		"cmd.review_dashboard_no_site":   "✔ تم الحفظ لـ *{group}*: لم تعد الحالات تذهب إلى مجموعة مراجعة.\nتنتظرك الآن: أرسل .معلق لعرضها.",
		"cmd.review_timeout_ban_refused": "لا يمكن أن تنتهي المهلة بالحظر في وضع سؤال المشرفين: اختر keep أو delete أو dismiss.",
		"cmd.review_not_found":           "لا توجد مراجعة #{code}.",
		"cmd.review_decided":             "✔ #{code}: {decision}.",
		"cmd.pending":                    "*بانتظار القرار* ({n})\n{items}\n\nللبتّ: .مراجعة <الرمز> ban أو .مراجعة <الرمز> ok",
		"cmd.pending_none":               "لا شيء بانتظار القرار.",
		"cmd.reviewgroup_protected":      "لا يمكن للمجموعة المحمية أن تراجع نفسها. أرسل \u200e.reviewgroup here في مجموعة منفصلة للمشرفين فقط.",
		"cmd.reviewgroup_pick":           "أي مجموعة ستراجعها هذه المحادثة؟ أرسل {prefix}مجموعة_المراجعة here 1 أو جزءاً من اسمها.\n*مجموعاتك*\n{groups}",
		"cmd.reviewgroup_set":            "✔ تستقبل هذه المحادثة الآن حالات المراجعة لـ *{group}*. تفاعل أو رُدّ على كل حالة للبتّ فيها.",
		"cmd.reviewgroup_off":            "لم تعد *{group}* ترسل الحالات إلى مجموعة مراجعة. تنتظرك الآن: أرسل .معلق لعرضها.",
		"cmd.reviewgroup_outsiders":      "⚠️ {n} أشخاص هنا ليسوا مشرفين في {group} وسيرون الأدلة في المراجعات.",

		"cmd.blacklist_added":          "*أُضيف للقائمة السوداء:* {added}\n*مدرج مسبقاً:* {already}\n*أرقام غير صالحة:* {invalid}\n*حُظر الآن:* {banned} · *مشرفون لم يُمسّوا:* {skipped} · *أُرسل للمراجعة:* {review}",
		"cmd.blacklist_removed":        "*أُزيل:* {removed}\n*غير مدرج:* {notfound}\n*أرقام غير صالحة:* {invalid}",
		"cmd.blacklist_list":           "*القائمة السوداء*\n{entries}",
		"cmd.blacklist_empty":          "القائمة السوداء فارغة.",
		"cmd.blacklist_invalid":        "{input} ليس رقم هاتف صالحاً.",
		"cmd.blacklist_operator_only":  "المشغّل وحده يستطيع تعديل القائمة السوداء العامة.",
		"cmd.blacklist_check_group":    "{number} في القائمة السوداء لهذه المجموعة.\n*عضو الآن:* {member}",
		"cmd.blacklist_check_instance": "{number} في القائمة السوداء العامة.\n*عضو الآن:* {member}",
		"cmd.blacklist_check_no":       "{number} ليس في القائمة السوداء هنا.\n*عضو الآن:* {member}",
		"blacklist.admin_skipped":      "{number} في القائمة السوداء لكنه مشرف في {group}، لذلك لم أتخذ أي إجراء.",

		"cmd.usage.set":            "أرسل الإعداد وقيمته:\n\u200e.set review.timeout 12h – انتظار القرار ١٢ ساعة",
		"cmd.usage.get":            "أرسل اسم الإعداد:\n\u200e.get review.timeout – عرض مدة انتظار القرار",
		"cmd.usage.mode":           "اختر الوضع:\n.وضع enforce – التصرف مع السبام\n.وضع shadow – المراقبة فقط دون أي تغيير\n.وضع pause – التوقف حتى تعيد التشغيل",
		"cmd.usage.preset":         "اختر درجة الصرامة:\n.نمط relaxed – التصرف مع السبام الواضح فقط\n.نمط standard – الإعداد الافتراضي\n.نمط strict – التصرف أسرع\n.نمط ask – حذف السبام فوراً، وسؤال المشرفين قبل الحظر",
		"cmd.usage.toggle":         "أضف on أو off، مثلاً .صامت on",
		"cmd.usage.policy":         "اختر ما يحدث للرسائل المخالفة لقواعد المجموعة:\n.سياسة delete – حذفها\n.سياسة ask – سؤال المشرفين\n.سياسة ban – الحذف والحظر",
		"cmd.usage.content":        "اذكر نوعاً من الرسائل:\n.منع links – حذف الروابط\n.سماح stickers – السماح بالملصقات من جديد\n*الأنواع:* links, invites, channels, contacts, images, videos, stickers, documents, audio, polls, locations, events, view_once",
		"cmd.usage.autoban":        ".حظر_تلقائي – عرض جدول الحظر التلقائي\n.حظر_تلقائي links auto – حظر سبام الروابط المعروف دون سؤال\n.حظر_تلقائي links ask – سؤال المشرفين أولاً\nإجراءات أخرى: delete و allow",
		"cmd.usage.askadmins":      "اختر متى أسأل المشرفين:\n.اسأل_المشرفين delete – حذف السبام فوراً، وسؤال المشرفين قبل الحظر\n.اسأل_المشرفين before – إبقاء الرسالة حتى يقرر أحد المشرفين\n.اسأل_المشرفين off – التصرف تلقائياً",
		"cmd.usage.ban":            "رُدّ على الرسالة (أو أشر إلى الشخص) بـ:\n.حظر سبام – الحظر وحفظها كسبام\n.حظر أخرى – الحظر لسبب آخر",
		"cmd.usage.kick":           "رُدّ على الرسالة (أو أشر إلى الشخص) بـ:\n.طرد – إزالته مع السماح بعودته",
		"cmd.usage.unban":          "أرسل الرقم، أو رُدّ على رسالته:\n.فك_الحظر \u200e+966551234567 – رفع الحظر",
		"cmd.usage.allow":          "رُدّ على الرسالة (أو أشر إلى الشخص) بـ:\n.سماح – عدم الاشتباه بهذا العضو\n.الغاء_السماح – التراجع عن ذلك",
		"cmd.usage.spam":           "رُدّ بـ .سبام على رسالة السبام.",
		"cmd.usage.notspam":        "رُدّ بـ .سليم على الرسالة السليمة.",
		"cmd.usage.domain":         "أرسل نطاقاً:\n.نطاق block example.com – حذف روابطه\n.نطاق allow example.com – السماح به دائماً",
		"cmd.usage.word":           "أرسل عبارة:\n.كلمة block ربح سريع – حذف الرسائل التي تحويها\n.كلمة unblock ربح سريع – إيقاف ذلك",
		"cmd.usage.review":         "*المراجعات*\n.معلق – عرض الحالات المفتوحة\n.مراجعة K7Q2 ban – حظر مرسل الحالة \u200e#K7Q2\n.مراجعة K7Q2 ok – ليست سبام\n.مراجعة K7Q2 delete only – حذف بلا حظر",
		"cmd.usage.review_timeout": "حدّد مدة الانتظار وما يحدث بعدها:\n.مراجعة timeout 6h keep – تبقى محذوفة بلا حظر\n.مراجعة timeout 6h delete – حذف بلا حظر\n.مراجعة timeout 6h ban – حظر\n.مراجعة timeout 6h dismiss – اعتبارها ليست سبام",
		"cmd.usage.reviewgroup":    "في مجموعة منفصلة للمشرفين فقط، أرسل:\n.مجموعة_المراجعة here – استلام الحالات هنا\n.مجموعة_المراجعة off – التوقف",
		"cmd.usage.blacklist":      "*القائمة السوداء*\n.القائمة_السوداء add \u200e+966551234567 احتيال – حظر الرقم\n.القائمة_السوداء remove \u200e+966551234567 – إزالته\n.القائمة_السوداء list – عرض القائمة\n.القائمة_السوداء check \u200e+966551234567 – هل هو مدرج؟",

		"onboard.start": "أهلاً! 👋 أحمي مجموعات واتساب من السبام، ولا أنشر فيها أي شيء.\n\n" +
			"*ثلاث خطوات للبدء*\n" +
			"1. أضفني إلى مجموعتك.\n" +
			"2. اجعلني مشرفاً فيها.\n" +
			"3. اختر أين تذهب الحالات غير المؤكدة:\n" +
			"- أرسل .معلق هنا لعرضها مع رموزها، ثم قرّر: .مراجعة K7Q2 ban أو .مراجعة K7Q2 ok\n" +
			"- أو أرسل .مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط لتصلك أولاً بأول\n\n" +
			"أرسل .مساعدة لكل الأوامر.",
		"onboard.start_web": "أهلاً! 👋 أحمي مجموعات واتساب من السبام، ولا أنشر فيها أي شيء.\n\n" +
			"*ثلاث خطوات للبدء*\n" +
			"1. أضفني إلى مجموعتك.\n" +
			"2. اجعلني مشرفاً فيها.\n" +
			"3. اختر أين تذهب الحالات غير المؤكدة: الموقع، أو أرسل .مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط لتصلك أولاً بأول.\n\n" +
			"أرسل .مساعدة لكل الأوامر. الموقع: {url}",
		"onboard.promoted": "🛡️ الحماية مفعّلة في *{group}*، وأبقى صامتاً هناك.\n\n" +
			"عندما لا أكون متأكداً من رسالة، أحتفظ بها كحالة لك:\n" +
			"- أرسل .معلق هنا لعرضها مع رموزها، ثم قرّر: .مراجعة K7Q2 ban أو .مراجعة K7Q2 ok\n" +
			"- أو أرسل .مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط لتصلك أولاً بأول\n\n" +
			"_افتراضياً، إن لم يقرر أحد في الوقت المحدد، لا يُحظر أحد._",
		"onboard.promoted_web": "🛡️ الحماية مفعّلة في *{group}*، وأبقى صامتاً هناك.\n\n" +
			"عندما لا أكون متأكداً من رسالة، أحتفظ بها كحالة لك على الموقع: {url}\n" +
			"لاستلام الحالات في واتساب، أرسل .معلق هنا، أو .مجموعة_المراجعة here في مجموعة منفصلة للمشرفين فقط.\n" +
			"_افتراضياً، إن لم يقرر أحد في الوقت المحدد، لا يُحظر أحد._",
		"onboard.not_admin":        "⚠️ أنا في {group} لكنني لست مشرفاً فيها، فلا أستطيع حمايتها. اجعلني مشرفاً وسأبدأ.",
		"onboard.joined":           "✔ انضممت إلى {group}. اجعلني مشرفاً لتفعيل الحماية.",
		"onboard.join_not_admin":   "يجب أن تكون مشرفاً في {group} لكي أنضم إليها.",
		"onboard.join_bad_link":    "رابط الدعوة هذا لا يعمل.",
		"onboard.join_unavailable": "لا أستطيع الانضمام عبر روابط الدعوة في هذا الإصدار.\nأضفني إلى المجموعة بنفسك، ثم اجعلني مشرفاً فيها.",
		"onboard.join_limit":       "انضممت إلى ما يكفي من المجموعات اليوم. حاول غداً.",
	},
}

func init() {
	for lang, texts := range commandTexts {
		for k, v := range texts {
			builtin[lang][k] = v
		}
	}
}
