package chat

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Community scope (DESIGN.md §12.4): `.use <community>` picks every group of a WhatsApp
// community the sender administers. Settings commands then run once per group, each with the
// group's own admin check, and are answered once. The same change is stored as the community's
// defaults (the settings document under the community's JID), which a group of the community
// starts with when the bot joins it or becomes its admin there.

// communitySettings are the settings commands that apply to every group of a community and are
// kept as its defaults.
var communitySettings = map[string]bool{
	"mode": true, "preset": true, "askadmins": true, "block": true, "blocknew": true, "set": true,
}

// communityDone are the replies of a command that changed its group.
var communityDone = map[string]bool{
	"cmd.saved": true, "cmd.preset": true, "cmd.blacklist_added": true, "cmd.blacklist_removed": true,
}

// communityScoped reports whether a command can run for each group of a community.
func communityScoped(c *call) bool {
	switch name := c.cmd.Name; {
	case communitySettings[name]:
		return true
	case name == "allow":
		// .allow stickers is a setting; .allow @member names one member of one group.
		if len(c.cmd.Args) != 1 {
			return false
		}
		_, ok := contentTypes[strings.ToLower(c.cmd.Args[0])]
		return ok
	case name == "blacklist":
		// add and remove only; the instance-wide list isn't a group's.
		return len(c.cmd.Args) > 0 && (strings.EqualFold(c.cmd.Args[0], "add") || strings.EqualFold(c.cmd.Args[0], "remove")) &&
			!strings.Contains(c.cmd.Rest, "--instance")
	}
	return false
}

// keepsDefaults reports whether a command's change is stored as the community's defaults.
func keepsDefaults(c *call) bool {
	return communitySettings[c.cmd.Name] || (c.cmd.Name == "allow" && communityScoped(c))
}

// communityGroups returns the groups of a community the sender administers right now, each
// checked like a single group would be, and the community's name.
func (h *Handler) communityGroups(ctx context.Context, c *call, community wa.JID) ([]GroupRef, string, error) {
	groups, err := h.adminGroups(ctx, c.person)
	if err != nil {
		return nil, "", err
	}
	var out []GroupRef
	name := c.communityName
	for _, g := range groups {
		if g.Community != community {
			continue
		}
		if g.CommunityName != "" {
			name = g.CommunityName
		}
		admin, err := h.senderIsAdmin(ctx, g.JID, c.m, c.person)
		if err != nil {
			return nil, "", err
		}
		if admin {
			out = append(out, g)
		}
	}
	return out, h.communityLabel(c, name), nil
}

// unprotected lists the groups of a community the bot can't protect, one per line, or "" when
// there are none or the community's groups aren't known.
func (h *Handler) unprotected(ctx context.Context, c *call, community wa.JID, name string) (string, error) {
	if h.d.Communities == nil {
		return "", nil
	}
	groups, err := h.d.Communities.CommunityGroups(ctx, community)
	if err != nil {
		return "", fmt.Errorf("groups of community %s: %w", community, err)
	}
	var lines []string
	for _, g := range groups {
		if g.Admin {
			continue
		}
		key := "cmd.community_not_member"
		if g.Member {
			key = "cmd.community_not_admin"
		}
		lines = append(lines, h.t(c, key, map[string]string{"group": cmpName(g.Name, g.JID)}))
	}
	if len(lines) == 0 {
		return "", nil
	}
	return "\n\n" + h.t(c, "cmd.community_unprotected", map[string]string{"community": name, "groups": strings.Join(lines, "\n")}), nil
}

func cmpName(name string, j wa.JID) string {
	if name != "" {
		return name
	}
	return string(j)
}

// useCommunity makes a community the chat's context.
func (h *Handler) useCommunity(ctx context.Context, c *call, e entry) error {
	groups, name, err := h.communityGroups(ctx, c, e.JID)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return h.reply(ctx, c, "cmd.community_none", map[string]string{"community": name, "prefix": h.d.Prefix})
	}
	h.setContext(c, dmContext{community: e.JID, communityName: e.Name})
	missing, err := h.unprotected(ctx, c, e.JID, name)
	if err != nil {
		return err
	}
	return h.reply(ctx, c, "cmd.using_community", map[string]string{
		"community": name, "n": strconv.Itoa(len(groups)), "groups": groupNames(groups), "unprotected": missing,
	})
}

func groupNames(groups []GroupRef) string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i] = "- " + g.Name
	}
	return strings.Join(names, "\n")
}

// runCommunity runs a command that needs a group while a community is the context.
func (h *Handler) runCommunity(ctx context.Context, c *call, cmd command) error {
	groups, name, err := h.communityGroups(ctx, c, c.community)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return h.reply(ctx, c, "cmd.community_none", map[string]string{"community": name, "prefix": h.d.Prefix})
	}
	if c.cmd.Name == "status" {
		return h.communityStatus(ctx, c, groups, name)
	}
	if !communityScoped(c) {
		all, err := h.adminGroups(ctx, c.person)
		if err != nil {
			return err
		}
		return h.reply(ctx, c, "cmd.community_one_group", map[string]string{
			"cmd": h.d.Prefix + c.cmd.Name, "community": name, "groups": h.groupList(c, listing(all)), "prefix": h.d.Prefix,
		})
	}
	var changed int
	var failed []string
	var first *captured
	same := true
	for _, g := range groups {
		got, err := h.runFor(ctx, c, cmd, g.JID)
		if err != nil {
			return err
		}
		if got.key != "" && communityDone[got.key] {
			changed++
			continue
		}
		if first == nil {
			first = &got
		}
		same = same && got.key == first.key
		failed = append(failed, "- "+g.Name+": "+firstLine(got.text))
	}
	if changed == 0 && first != nil && same {
		// The same answer everywhere (a usage hint, an unknown key): say it once.
		return h.replyText(ctx, c, first.text)
	}
	if changed > 0 && keepsDefaults(c) {
		if _, err := h.runFor(ctx, c, cmd, c.community); err != nil {
			return fmt.Errorf("community defaults: %w", err)
		}
	}
	notChanged := ""
	if len(failed) > 0 {
		notChanged = "\n\n" + h.t(c, "cmd.community_failed", map[string]string{"groups": strings.Join(failed, "\n")})
	}
	missing, err := h.unprotected(ctx, c, c.community, name)
	if err != nil {
		return err
	}
	return h.reply(ctx, c, "cmd.community_done", map[string]string{
		"community": name, "command": strings.TrimSpace(c.m.Text),
		"changed": strconv.Itoa(changed), "total": strconv.Itoa(len(groups)),
		"failed": notChanged, "unprotected": missing,
	})
}

// runFor runs cmd for one group (or the community's defaults) and returns the reply it would
// have sent.
func (h *Handler) runFor(ctx context.Context, c *call, cmd command, group wa.JID) (captured, error) {
	var got []captured
	one := *c
	one.group, one.community, one.capture = group, "", &got
	err := cmd.run(h, ctx, &one)
	if len(got) == 0 {
		return captured{}, err
	}
	return got[len(got)-1], err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// communityStatus answers .status for a community: one line per group, then the groups the bot
// can't protect.
func (h *Handler) communityStatus(ctx context.Context, c *call, groups []GroupRef, name string) error {
	if h.d.Settings == nil {
		return h.unavailable(ctx, c)
	}
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		var parts []string
		for _, k := range []string{"mode", "preset", "enforcement.mode"} {
			if v, err := h.d.Settings.Get(ctx, g.JID, k); err == nil {
				parts = append(parts, v)
			}
		}
		if h.d.Stats != nil {
			if s, err := h.d.Stats.Today(ctx, g.JID); err == nil {
				parts = append(parts, h.t(c, "cmd.community_today", map[string]string{
					"deleted": strconv.Itoa(s.Deleted), "banned": strconv.Itoa(s.Banned),
				}))
			}
		}
		lines = append(lines, "*"+g.Name+":* "+strings.Join(parts, " · "))
	}
	missing, err := h.unprotected(ctx, c, c.community, name)
	if err != nil {
		return err
	}
	return h.reply(ctx, c, "cmd.community_status", map[string]string{
		"community": name, "n": strconv.Itoa(len(groups)), "groups": strings.Join(lines, "\n"), "unprotected": missing,
	})
}
