package chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/review"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

type command struct {
	run        func(h *Handler, ctx context.Context, c *call) error
	needsGroup bool
}

var table map[string]command

func init() {
	table = map[string]command{
		"help":   {run: (*Handler).cmdHelp},
		"groups": {run: (*Handler).cmdGroups},
		"use":    {run: (*Handler).cmdUse},
		"login":  {run: (*Handler).cmdLogin},

		"set":      {run: (*Handler).cmdSet, needsGroup: true},
		"get":      {run: (*Handler).cmdGet, needsGroup: true},
		"settings": {run: (*Handler).cmdSettings, needsGroup: true},

		"status":    {run: (*Handler).cmdStatus, needsGroup: true},
		"mode":      {run: (*Handler).cmdMode, needsGroup: true},
		"preset":    {run: (*Handler).cmdPreset, needsGroup: true},
		"silent":    {run: toggle("silent"), needsGroup: true},
		"newsignal": {run: toggle("new_members.just_joined.enabled"), needsGroup: true},
		"ladder":    {run: toggle("ladder.enabled"), needsGroup: true},
		"policy":    {run: (*Handler).cmdPolicy, needsGroup: true},
		"block":     {run: content("block"), needsGroup: true},
		"blocknew":  {run: content("block_for_new"), needsGroup: true},
		"allow":     {run: (*Handler).cmdAllow, needsGroup: true},
		"autoban":   {run: (*Handler).cmdAutoban, needsGroup: true},
		"askadmins": {run: (*Handler).cmdAskAdmins, needsGroup: true},

		"ban":     {run: (*Handler).cmdBan, needsGroup: true},
		"kick":    {run: (*Handler).cmdKick, needsGroup: true},
		"unban":   {run: (*Handler).cmdUnban, needsGroup: true},
		"unallow": {run: (*Handler).cmdUnallow, needsGroup: true},
		"undo":    {run: (*Handler).cmdUndo, needsGroup: true},
		"spam":    {run: (*Handler).cmdSpam, needsGroup: true},
		"notspam": {run: (*Handler).cmdNotSpam, needsGroup: true},
		"ok":      {run: (*Handler).cmdNotSpam, needsGroup: true},

		"domain": {run: (*Handler).cmdDomain, needsGroup: true},
		"word":   {run: (*Handler).cmdWord, needsGroup: true},

		"review":      {run: (*Handler).cmdReview},
		"pending":     {run: (*Handler).cmdPending, needsGroup: true},
		"reviewgroup": {run: (*Handler).cmdReviewGroup},

		"blacklist": {run: (*Handler).cmdBlacklist, needsGroup: true},
	}
}

// dispatch runs a parsed command. In a DM the sender must administer the context group; in a
// protected or review group that was checked before.
func (h *Handler) dispatch(ctx context.Context, c *call) error {
	cmd, ok := table[c.cmd.Name]
	if !ok {
		return nil
	}
	if c.where == inDM {
		groups, err := h.d.Groups.AdminGroups(ctx, c.person)
		if err != nil {
			return fmt.Errorf("groups of sender: %w", err)
		}
		if len(groups) == 0 && !h.operator(c.person) {
			// Not an admin anywhere: commands are ignored (§13.7).
			return nil
		}
		if c.group != "" {
			admin, err := h.senderIsAdmin(ctx, c.group, c.m, c.person)
			if err != nil {
				return err
			}
			if !admin {
				c.group = ""
			}
		}
	}
	if cmd.needsGroup && c.group == "" {
		return h.pickGroup(ctx, c)
	}
	return cmd.run(h, ctx, c)
}

func (h *Handler) operator(p wa.JID) bool { return h.d.Operator != nil && h.d.Operator(p) }

// withSite returns the _web variant of a text key when a website URL is configured. Without one
// (v0.1), the plain key's text never mentions a website or a link.
func (h *Handler) withSite(key string) string {
	if h.d.WebsiteURL != "" {
		return key + "_web"
	}
	return key
}

func (h *Handler) pickGroup(ctx context.Context, c *call) error {
	groups, err := h.adminGroups(ctx, c.person)
	if err != nil {
		return err
	}
	return h.reply(ctx, c, "cmd.pick_group", map[string]string{"groups": groupList(groups), "prefix": h.d.Prefix})
}

// adminGroups lists the protected groups person administers, sorted by name, so the numbers
// groupList shows stay the same from one reply to the next and .use 2 means what was listed.
func (h *Handler) adminGroups(ctx context.Context, person wa.JID) ([]GroupRef, error) {
	groups, err := h.d.Groups.AdminGroups(ctx, person)
	if err != nil {
		return nil, fmt.Errorf("groups of sender: %w", err)
	}
	groups = slices.Clone(groups)
	slices.SortStableFunc(groups, func(a, b GroupRef) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.JID, b.JID))
	})
	return groups, nil
}

// groupList numbers groups for .use <number>: "1. Cars", "2. Shop".
func groupList(groups []GroupRef) string {
	names := make([]string, 0, len(groups))
	for i, g := range groups {
		names = append(names, strconv.Itoa(i+1)+". "+g.Name)
	}
	return strings.Join(names, "\n")
}

// westernDigits turns Arabic-Indic digits into ASCII ones, so ".use ٢" works like ".use 2".
var westernDigits = strings.NewReplacer(
	"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
)

// matchGroup picks one of groups (as sorted by adminGroups) for what an admin typed: an exact
// name first, so a group named "2" stays reachable by name; then a number from groupList; then
// the only name containing it. ok is false when nothing or several groups match.
func matchGroup(groups []GroupRef, typed string) (GroupRef, bool) {
	want := strings.ToLower(strings.TrimSpace(typed))
	if want == "" {
		return GroupRef{}, false
	}
	for _, g := range groups {
		if strings.ToLower(g.Name) == want {
			return g, true
		}
	}
	if n, err := strconv.Atoi(westernDigits.Replace(want)); err == nil && n >= 1 && n <= len(groups) {
		return groups[n-1], true
	}
	var match []GroupRef
	for _, g := range groups {
		if strings.Contains(strings.ToLower(g.Name), want) {
			match = append(match, g)
		}
	}
	if len(match) != 1 {
		return GroupRef{}, false
	}
	return match[0], true
}

func (h *Handler) groupName(ctx context.Context, g wa.JID) string {
	name, err := h.d.Groups.Name(ctx, g)
	if err != nil || name == "" {
		return string(g)
	}
	return name
}

// unavailable answers a command whose collaborator isn't wired.
func (h *Handler) unavailable(ctx context.Context, c *call) error {
	return h.reply(ctx, c, "cmd.unavailable", map[string]string{"cmd": h.d.Prefix + c.cmd.Name})
}

func (h *Handler) cmdGroups(ctx context.Context, c *call) error {
	groups, err := h.adminGroups(ctx, c.person)
	if err != nil {
		return err
	}
	return h.reply(ctx, c, "cmd.groups", map[string]string{"groups": groupList(groups), "prefix": h.d.Prefix})
}

func (h *Handler) cmdUse(ctx context.Context, c *call) error {
	groups, err := h.adminGroups(ctx, c.person)
	if err != nil {
		return err
	}
	g, ok := matchGroup(groups, c.cmd.Rest)
	if !ok {
		return h.reply(ctx, c, "cmd.pick_group", map[string]string{"groups": groupList(groups), "prefix": h.d.Prefix})
	}
	h.setContext(c, g.JID)
	return h.reply(ctx, c, "cmd.using", map[string]string{"group": g.Name})
}

func (h *Handler) cmdLogin(ctx context.Context, c *call) error {
	if h.d.WebsiteURL == "" {
		// v0.1 has no website: say so plainly instead of sending an empty link.
		return h.reply(ctx, c, "cmd.login_no_site", map[string]string{"prefix": h.d.Prefix})
	}
	if c.where == inReviewGroup {
		// A login link is personal: never in a group.
		c.where = inProtected
	}
	return h.reply(ctx, c, "cmd.login", map[string]string{"url": h.d.WebsiteURL})
}

// set writes one setting and reports the change or the validation error.
func (h *Handler) set(ctx context.Context, c *call, key, value string) (bool, error) {
	if h.d.Settings == nil {
		return false, h.unavailable(ctx, c)
	}
	ch, err := h.d.Settings.Set(ctx, c.group, key, value, c.person)
	switch {
	case errors.Is(err, ErrUnknownKey):
		return false, h.reply(ctx, c, "cmd.unknown_key", map[string]string{"key": key})
	case errors.Is(err, ErrInvalidValue):
		return false, h.reply(ctx, c, "cmd.invalid_value", map[string]string{"key": key, "value": value, "reason": err.Error()})
	case err != nil:
		return false, fmt.Errorf("set %s: %w", key, err)
	}
	return true, h.audit(ctx, c, "settings.set", map[string]any{"key": ch.Key, "old": ch.Old, "new": ch.New})
}

// setAndReply sets several keys in order and confirms them in one reply.
func (h *Handler) setAndReply(ctx context.Context, c *call, kv ...string) error {
	var done []string
	for i := 0; i+1 < len(kv); i += 2 {
		ok, err := h.set(ctx, c, kv[i], kv[i+1])
		if err != nil || !ok {
			return err
		}
		done = append(done, kv[i]+" = "+kv[i+1])
	}
	return h.reply(ctx, c, "cmd.saved", map[string]string{"changes": strings.Join(done, "\n"), "group": h.groupName(ctx, c.group)})
}

func (h *Handler) cmdSet(ctx context.Context, c *call) error {
	if len(c.cmd.Args) < 2 {
		return h.reply(ctx, c, "cmd.usage.set", nil)
	}
	key := c.cmd.Args[0]
	value := strings.TrimSpace(strings.TrimPrefix(c.cmd.Rest, key))
	if key == "review.review_group" || strings.HasPrefix("review.review_group", key+".") {
		// Only .reviewgroup here can set it, from inside a separate, non-protected group (§9.5),
		// whether the key is typed directly or through a parent such as "review".
		return h.reply(ctx, c, "cmd.set_review_group", map[string]string{"prefix": h.d.Prefix})
	}
	return h.setAndReply(ctx, c, key, value)
}

func (h *Handler) cmdGet(ctx context.Context, c *call) error {
	if len(c.cmd.Args) != 1 {
		return h.reply(ctx, c, "cmd.usage.get", nil)
	}
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	key := c.cmd.Args[0]
	v, err := h.d.Settings.Get(ctx, c.group, key)
	if errors.Is(err, ErrUnknownKey) {
		return h.reply(ctx, c, "cmd.unknown_key", map[string]string{"key": key})
	}
	if err != nil {
		return fmt.Errorf("get %s: %w", key, err)
	}
	return h.reply(ctx, c, "cmd.value", map[string]string{"key": key, "value": v})
}

// summaryKeys are the settings .settings and .status show.
var summaryKeys = []string{"preset", "mode", "silent", "enforcement.mode", "review.destination", "review.timeout", "review.on_timeout"}

func (h *Handler) summary(ctx context.Context, c *call) string {
	var lines []string
	mode, _ := h.d.Settings.Get(ctx, c.group, "enforcement.mode")
	for _, k := range summaryKeys {
		if mode == "ask_before_action" && (k == "review.timeout" || k == "review.on_timeout") {
			// That mode has its own timeout and fallback, shown in the "before acting" line.
			continue
		}
		if v, err := h.d.Settings.Get(ctx, c.group, k); err == nil {
			if k == "review.destination" && v == string(review.DestinationDashboard) && h.d.WebsiteURL == "" {
				// No website yet (v0.1): say where the cases really wait.
				v = h.t(c, "cmd.destination_pending", nil)
			}
			if k == "enforcement.mode" {
				lines = append(lines, h.t(c, "cmd.asking", map[string]string{"how": h.askingWords(ctx, c, v)}))
				continue
			}
			if k == "review.timeout" {
				v = shortDuration(v)
			}
			lines = append(lines, "*"+k+":* "+v)
		}
	}
	return strings.Join(lines, "\n")
}

// shortDuration shows a stored duration ("15m0s", "6h0m0s") the way admins type it ("15m", "6h").
func shortDuration(s string) string {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return s
	}
	switch {
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d%time.Minute == 0 && d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d%time.Minute == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h" + strconv.Itoa(int(d%time.Hour/time.Minute)) + "m"
	}
	return d.String()
}

// askTimeout is how long ask_before_action waits for the admins in group, as admins type it.
func (h *Handler) askTimeout(ctx context.Context, group wa.JID) string {
	if d, err := h.d.Settings.Get(ctx, group, "enforcement.ask_before.timeout"); err == nil && d != "" {
		return shortDuration(d)
	}
	return "15m" // the default, when the group's value can't be read
}

// askingWords says in plain words when the bot asks the admins, for enforcement.mode value mode.
func (h *Handler) askingWords(ctx context.Context, c *call, mode string) string {
	if mode != "auto" && mode != "ask_delete_first" && mode != "ask_before_action" {
		return mode
	}
	key, vars := "cmd.ask."+mode, map[string]string{}
	if mode == "ask_before_action" {
		vars["timeout"] = h.askTimeout(ctx, c.group)
		if on, err := h.d.Settings.Get(ctx, c.group, "enforcement.ask_before.on_timeout"); err == nil && on == "leave" {
			key += "_leave"
		}
	}
	return h.t(c, key, vars)
}

func (h *Handler) cmdSettings(ctx context.Context, c *call) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	return h.reply(ctx, c, h.withSite("cmd.settings"), map[string]string{
		"group": h.groupName(ctx, c.group), "summary": h.summary(ctx, c), "url": h.d.WebsiteURL,
	})
}

func (h *Handler) cmdStatus(ctx context.Context, c *call) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	vars := map[string]string{"group": h.groupName(ctx, c.group), "summary": h.summary(ctx, c)}
	if h.d.Stats != nil {
		if s, err := h.d.Stats.Today(ctx, c.group); err == nil {
			vars["deleted"], vars["banned"], vars["pending"] = fmt.Sprint(s.Deleted), fmt.Sprint(s.Banned), fmt.Sprint(s.Pending)
			return h.reply(ctx, c, "cmd.status", vars)
		}
	}
	// Without today's counts, show only what is known: the cases waiting for a decision.
	if h.d.Reviews != nil {
		if items, err := h.d.Reviews.Pending(ctx, c.group); err == nil {
			vars["pending"] = strconv.Itoa(len(items))
			return h.reply(ctx, c, "cmd.status_pending", vars)
		}
	}
	return h.reply(ctx, c, "cmd.status_brief", vars)
}

// choice maps a single argument through m, or replies with the usage.
func (h *Handler) choice(ctx context.Context, c *call, m map[string]string, usage string) (string, bool, error) {
	if len(c.cmd.Args) != 1 {
		return "", false, h.reply(ctx, c, usage, nil)
	}
	v, ok := m[strings.ToLower(c.cmd.Args[0])]
	if !ok {
		return "", false, h.reply(ctx, c, usage, nil)
	}
	return v, true, nil
}

func (h *Handler) cmdMode(ctx context.Context, c *call) error {
	v, ok, err := h.choice(ctx, c, map[string]string{"shadow": "shadow", "enforce": "enforce", "pause": "paused", "paused": "paused"}, "cmd.usage.mode")
	if !ok {
		return err
	}
	return h.setAndReply(ctx, c, "mode", v)
}

func (h *Handler) cmdPreset(ctx context.Context, c *call) error {
	v, ok, err := h.choice(ctx, c, map[string]string{
		"relaxed": "relaxed", "standard": "standard", "strict": "strict", "ask": "ask_admins", "ask_admins": "ask_admins",
	}, "cmd.usage.preset")
	if !ok {
		return err
	}
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	changes, err := h.d.Settings.ApplyPreset(ctx, c.group, v, c.person)
	if errors.Is(err, ErrInvalidValue) {
		return h.reply(ctx, c, "cmd.usage.preset", nil)
	}
	if err != nil {
		return fmt.Errorf("apply preset %s: %w", v, err)
	}
	// A preset may keep or change when the bot asks the admins (policy.applyPreset): say how it
	// acts now.
	mode, err := h.d.Settings.Get(ctx, c.group, "enforcement.mode")
	if err != nil {
		mode = ""
	}
	return errors.Join(
		h.audit(ctx, c, "settings.preset", map[string]any{"preset": v, "changes": len(changes)}),
		h.reply(ctx, c, "cmd.preset", map[string]string{"preset": v, "group": h.groupName(ctx, c.group), "how": h.askingWords(ctx, c, mode)}),
	)
}

func toggle(key string) func(*Handler, context.Context, *call) error {
	return func(h *Handler, ctx context.Context, c *call) error {
		v, ok, err := h.choice(ctx, c, map[string]string{"on": "true", "off": "false", "تشغيل": "true", "ايقاف": "false", "إيقاف": "false"}, "cmd.usage.toggle")
		if !ok {
			return err
		}
		return h.setAndReply(ctx, c, key, v)
	}
}

func (h *Handler) cmdPolicy(ctx context.Context, c *call) error {
	v, ok, err := h.choice(ctx, c, map[string]string{"delete": "delete_only", "ask": "ask", "ban": "ban"}, "cmd.usage.policy")
	if !ok {
		return err
	}
	return h.setAndReply(ctx, c, "policy.action", v)
}

// contentTypes maps the names admins type to the content.* keys (§12.1).
var contentTypes = map[string]string{
	"links": "links", "link": "links", "invites": "group_invites", "group_invites": "group_invites",
	"channels": "channel_shares", "channel_shares": "channel_shares", "contacts": "contacts",
	"images": "images", "photos": "images", "videos": "videos", "stickers": "stickers",
	"documents": "documents", "files": "documents", "audio": "audio", "voice": "audio", "polls": "polls",
	"locations": "locations", "events": "events", "view_once": "view_once", "viewonce": "view_once",
	"forwards": "forwarded_many_times", "forwarded_many_times": "forwarded_many_times",
	"status_shares": "status_shares", "status_mentions": "status_mentions",
}

func content(value string) func(*Handler, context.Context, *call) error {
	return func(h *Handler, ctx context.Context, c *call) error {
		if len(c.cmd.Args) != 1 {
			return h.reply(ctx, c, "cmd.usage.content", nil)
		}
		t, ok := contentTypes[strings.ToLower(c.cmd.Args[0])]
		if !ok {
			return h.reply(ctx, c, "cmd.usage.content", nil)
		}
		return h.setAndReply(ctx, c, "content."+t, value)
	}
}

// cmdAllow is the member allow-list when it names a member (.allow @member), and the content
// policy shortcut otherwise (.allow stickers).
func (h *Handler) cmdAllow(ctx context.Context, c *call) error {
	if len(c.cmd.Args) == 1 {
		if _, isType := contentTypes[strings.ToLower(c.cmd.Args[0])]; isType {
			return content("allow")(h, ctx, c)
		}
	}
	if h.d.Members == nil {
		// Without the member allow-list, .allow only takes a message type: show how.
		return h.reply(ctx, c, "cmd.usage.content", nil)
	}
	return h.memberAllow(ctx, c, true)
}

// matrixTypes are the auto-ban matrix rows (§9.2).
var matrixTypes = []string{
	"text", "links", "images", "videos", "audio", "stickers", "documents", "contacts",
	"channel_shares", "group_invites", "polls", "locations_events", "status", "view_once",
}

var matrixAliases = map[string]string{
	"invites": "group_invites", "channels": "channel_shares", "photos": "images", "files": "documents",
	"locations": "locations_events", "events": "locations_events", "viewonce": "view_once", "link": "links",
}

func (h *Handler) cmdAutoban(ctx context.Context, c *call) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	if len(c.cmd.Args) == 0 {
		var rows []string
		for _, t := range matrixTypes {
			v, err := h.d.Settings.Get(ctx, c.group, "enforcement.autoban_matrix."+t+".action")
			if err != nil {
				continue
			}
			if src, err := h.d.Settings.Get(ctx, c.group, "enforcement.autoban_matrix."+t+".require_source"); err == nil && src != "" && v == "auto_ban" {
				v += " (" + src + ")"
			}
			rows = append(rows, t+": "+v)
		}
		return h.reply(ctx, c, "cmd.autoban_grid", map[string]string{"grid": strings.Join(rows, "\n")})
	}
	if len(c.cmd.Args) < 2 || len(c.cmd.Args) > 3 {
		return h.reply(ctx, c, "cmd.usage.autoban", nil)
	}
	typ := strings.ToLower(c.cmd.Args[0])
	if a, ok := matrixAliases[typ]; ok {
		typ = a
	}
	known := false
	for _, t := range matrixTypes {
		known = known || t == typ
	}
	action, ok := map[string]string{"auto": "auto_ban", "ask": "ask", "delete": "delete_only", "allow": "allow"}[strings.ToLower(c.cmd.Args[1])]
	if !known || !ok {
		return h.reply(ctx, c, "cmd.usage.autoban", nil)
	}
	kv := []string{"enforcement.autoban_matrix." + typ + ".action", action}
	if action == "auto_ban" {
		sources := "known"
		if len(c.cmd.Args) == 3 {
			sources = strings.ToLower(c.cmd.Args[2])
		}
		for _, s := range strings.Split(sources, ",") {
			switch s {
			case "known":
			case "ai":
				// AI evidence never auto-bans before it has a track record here (§9.2, F-112).
				ready := false
				if h.d.Track != nil {
					var err error
					if ready, err = h.d.Track.AIReady(ctx, c.group); err != nil {
						return fmt.Errorf("ai track record: %w", err)
					}
				}
				if !ready {
					return h.reply(ctx, c, "cmd.autoban_ai_refused", nil)
				}
			default:
				return h.reply(ctx, c, "cmd.usage.autoban", nil)
			}
		}
		kv = append(kv, "enforcement.autoban_matrix."+typ+".require_source", sources)
	}
	return h.setAndReply(ctx, c, kv...)
}

func (h *Handler) cmdAskAdmins(ctx context.Context, c *call) error {
	v, ok, err := h.choice(ctx, c, map[string]string{"off": "auto", "delete": "ask_delete_first", "before": "ask_before_action"}, "cmd.usage.askadmins")
	if !ok {
		return err
	}
	return h.setAndReply(ctx, c, "enforcement.mode", v)
}
