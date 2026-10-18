package chat

import (
	"context"
	"strings"
)

// helpTopics maps the words .help <topic> accepts, in English and Arabic (folded like command
// aliases), to the topic's page.
var helpTopics = map[string]string{
	"moderation": "moderation", "moderate": "moderation", "mod": "moderation",
	"reviews": "reviews", "review": "reviews",
	"settings": "settings", "setting": "settings",
	"blacklist": "blacklist",
	"الاشراف":   "moderation", "اشراف": "moderation",
	"المراجعات": "reviews", "مراجعات": "reviews", "المراجعه": "reviews", "مراجعه": "reviews",
	"الاعدادات": "settings", "اعدادات": "settings",
	"القائمه_السوداء": "blacklist", "القائمه السوداء": "blacklist", "سوداء": "blacklist",
}

// cmdHelp sends the command menu, or with a topic (.help reviews) that topic's page.
func (h *Handler) cmdHelp(ctx context.Context, c *call) error {
	vars := map[string]string{"prefix": h.d.Prefix}
	if c.cmd.Rest == "" {
		return h.reply(ctx, c, "cmd.help", vars)
	}
	topic, ok := helpTopics[foldArabic.Replace(strings.ToLower(c.cmd.Rest))]
	if !ok {
		vars["topic"] = c.cmd.Rest
		return h.reply(ctx, c, "cmd.help_unknown", vars)
	}
	return h.reply(ctx, c, "cmd.help."+topic, vars)
}
