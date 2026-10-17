package policy

import (
	"slices"
	"testing"
)

func rules(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Rule)
	}
	return out
}

func TestEvaluateContent(t *testing.T) {
	def := Defaults().Content
	with := func(f func(*Content)) Content {
		c := Defaults().Clone().Content
		f(&c)
		return c
	}
	newbie := Standing{Role: RoleMember, New: true}
	member := Standing{Role: RoleMember}
	est := Standing{Role: RoleMember, Established: true}
	tests := []struct {
		name string
		c    Content
		f    ContentFacts
		st   Standing
		want []string
	}{
		{"defaults allow everything", def, ContentFacts{LinkHosts: []string{"x.com"}, Videos: true, ViewOnce: true, StatusShares: true}, newbie, nil},
		{"blocked videos", with(func(c *Content) { c.Videos = RuleBlock }), ContentFacts{Videos: true}, est, []string{"content.videos"}},
		{"links for new: new member", with(func(c *Content) { c.Links = RuleBlockForNew }), ContentFacts{LinkHosts: []string{"x.com"}}, newbie, []string{"content.links"}},
		{"links for new: older member", with(func(c *Content) { c.Links = RuleBlockForNew }), ContentFacts{LinkHosts: []string{"x.com"}}, member, nil},
		{"links for untrusted: member", with(func(c *Content) { c.Links = RuleBlockForUntrusted }), ContentFacts{LinkHosts: []string{"x.com"}}, member, []string{"content.links"}},
		{"links for untrusted: established", with(func(c *Content) { c.Links = RuleBlockForUntrusted }), ContentFacts{LinkHosts: []string{"x.com"}}, est, nil},
		{
			"link allow-list", with(func(c *Content) { c.Links = RuleBlock; c.LinkDomains.Allow = []string{"youtube.com"} }),
			ContentFacts{LinkHosts: []string{"www.youtube.com", "m.youtube.com"}},
			newbie, nil,
		},
		{
			"link allow-list is not a suffix trick", with(func(c *Content) { c.Links = RuleBlock; c.LinkDomains.Allow = []string{"youtube.com"} }),
			ContentFacts{LinkHosts: []string{"notyoutube.com"}},
			newbie,
			[]string{"content.links"},
		},
		{
			"blocked domain even when links are allowed", with(func(c *Content) { c.LinkDomains.Block = []string{"bit.ly"} }),
			ContentFacts{LinkHosts: []string{"BIT.LY."}},
			est,
			[]string{"content.link_domains.block"},
		},
		{
			"status family", with(func(c *Content) {
				c.StatusShares, c.StatusMentions, c.GroupStatusPosts = RuleBlock, RuleBlock, RuleBlock
			}),
			ContentFacts{StatusShares: true, StatusMentions: true, GroupStatusPosts: true},
			est,
			[]string{"content.group_status_posts", "content.status_mentions", "content.status_shares"},
		},
		{
			"view-once and forwarded many times", with(func(c *Content) { c.ViewOnce, c.ForwardedManyTimes = RuleBlock, RuleBlockForNew }),
			ContentFacts{ViewOnce: true, ForwardedManyTimes: true},
			newbie,
			[]string{"content.forwarded_many_times", "content.view_once"},
		},
		{"blocked extension", def, ContentFacts{Documents: []string{"WhatsApp.APK"}}, est, []string{"content.document_extensions_block"}},
		{"double extension", def, ContentFacts{Documents: []string{"invoice.pdf.vbs"}}, est, []string{"content.document_extensions_block"}},
		{"right-to-left override", def, ContentFacts{Documents: []string{"invoice\u202efdp.exe"}}, est, []string{"content.document_extensions_block"}},
		{"trailing dots and spaces", def, ContentFacts{Documents: []string{"setup.exe. . "}}, est, []string{"content.document_extensions_block"}},
		{"ordinary documents", def, ContentFacts{Documents: []string{"report.pdf", "notes.v2.docx", "README"}}, newbie, nil},
		{"documents blocked: one violation", with(func(c *Content) { c.Documents = RuleBlock }), ContentFacts{Documents: []string{"a.exe"}}, est, []string{"content.documents"}},
		{
			"other types", with(func(c *Content) {
				c.GroupInvites, c.ChannelShares, c.Contacts, c.Images, c.Stickers, c.Audio, c.Polls, c.Locations, c.Events =
					RuleBlock, RuleBlock, RuleBlock, RuleBlock, RuleBlock, RuleBlock, RuleBlock, RuleBlock, RuleBlock
			}),
			ContentFacts{GroupInvites: true, ChannelShares: true, Contacts: true, Images: true, Stickers: true, Audio: true, Polls: true, Locations: true, Events: true},
			est,
			[]string{"content.audio", "content.channel_shares", "content.contacts", "content.events", "content.group_invites", "content.images", "content.locations", "content.polls", "content.stickers"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rules(EvaluateContent(tt.c, tt.f, tt.st)); !slices.Equal(got, tt.want) {
				t.Errorf("violations %v, want %v (§9.3)", got, tt.want)
			}
		})
	}
}

func FuzzBlockedDocument(f *testing.F) {
	for _, s := range []string{"a.pdf", "a.pdf.exe", "\u202e", "...", "a. .exe", "x/y\\z.apk", ""} {
		f.Add(s)
	}
	block := Defaults().Content.DocumentExtensionsBlock
	f.Fuzz(func(t *testing.T, name string) {
		_ = blockedDocument(name, block) // must not panic on hostile names
	})
}
