package policy

import (
	"fmt"
	"net"
	"path"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// ContentFacts are the content types present in one message, as the content policy sees
// them (DESIGN.md §9.3, §6.18, §6.5).
type ContentFacts struct {
	LinkHosts          []string // host of every link, lower case (www. is ignored)
	GroupInvites       bool
	ChannelShares      bool
	Contacts           bool
	Images             bool
	Videos             bool
	Stickers           bool
	Documents          []string // file names; an empty name still counts as a document
	DocumentMimetypes  []string // declared mimetypes of the documents
	HasDocument        bool
	Audio              bool
	Polls              bool
	Locations          bool
	Events             bool
	ViewOnce           bool
	ForwardedManyTimes bool
	StatusShares       bool // a share or reshare of a status
	StatusMentions     bool // a status that mentions the group
	GroupStatusPosts   bool // a Group Status post
}

// applies reports whether rule blocks this sender.
func (r ContentRule) applies(st Standing) bool {
	switch r {
	case RuleBlock:
		return true
	case RuleBlockForNew:
		return st.New
	case RuleBlockForUntrusted:
		return !st.Established && !st.Allowlisted
	default:
		return false
	}
}

// EvaluateContent checks a message against the group's content policy and returns its hard
// violations, sorted by rule. Violations are separate from the spam score: they're policy,
// not evidence of spam (DESIGN.md §7.3).
func EvaluateContent(c Content, f ContentFacts, st Standing) []Violation {
	var out []Violation
	add := func(rule, detail string) { out = append(out, Violation{Rule: rule, Detail: detail}) }
	check := func(present bool, rule ContentRule, key, what string) {
		if present && rule.applies(st) {
			add("content."+key, fmt.Sprintf("%s (%s)", what, rule))
		}
	}

	for _, h := range f.LinkHosts {
		h = normHost(h)
		if h == "" {
			continue
		}
		switch {
		case domainListed(h, c.LinkDomains.Block):
			add("content.link_domains.block", "link to "+h)
		case c.Links.applies(st) && !domainListed(h, c.LinkDomains.Allow):
			add("content.links", fmt.Sprintf("link to %s (%s)", h, c.Links))
		}
	}
	check(f.GroupInvites, c.GroupInvites, "group_invites", "group invite")
	check(f.ChannelShares, c.ChannelShares, "channel_shares", "channel share")
	check(f.Contacts, c.Contacts, "contacts", "contact card")
	check(f.Images, c.Images, "images", "image")
	check(f.Videos, c.Videos, "videos", "video")
	check(f.Stickers, c.Stickers, "stickers", "sticker")
	hasDoc := f.HasDocument || len(f.Documents) > 0
	check(hasDoc, c.Documents, "documents", "document")
	if hasDoc && !c.Documents.applies(st) {
		for _, name := range f.Documents {
			if why := blockedDocument(name, c.DocumentExtensionsBlock); why != "" {
				add("content.document_extensions_block", why)
			}
		}
		for _, mt := range f.DocumentMimetypes {
			if why := blockedMimetype(mt, c.DocumentExtensionsBlock); why != "" {
				add("content.document_extensions_block", why)
			}
		}
	}
	check(f.Audio, c.Audio, "audio", "audio")
	check(f.Polls, c.Polls, "polls", "poll")
	check(f.Locations, c.Locations, "locations", "location")
	check(f.Events, c.Events, "events", "event")
	check(f.ViewOnce, c.ViewOnce, "view_once", "view-once message")
	check(f.ForwardedManyTimes, c.ForwardedManyTimes, "forwarded_many_times", "forwarded many times")
	check(f.StatusShares, c.StatusShares, "status_shares", "status share")
	check(f.StatusMentions, c.StatusMentions, "status_mentions", "status mention")
	check(f.GroupStatusPosts, c.GroupStatusPosts, "group_status_posts", "Group Status post")

	slices.SortStableFunc(out, func(a, b Violation) int { return strings.Compare(a.Rule, b.Rule) })
	return out
}

// normHost canonicalises a link host for list matching: lower case, no port, no trailing dot,
// no "www.", the ideographic and fullwidth dots that browsers treat as "." mapped to ".", and
// internationalised names in their ASCII (punycode) form, so "BIT．ly:443" matches "bit.ly".
func normHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.NewReplacer("\u3002", ".", "\uff0e", ".", "\uff61", ".").Replace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if a, err := idna.Lookup.ToASCII(h); err == nil {
		h = a
	}
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	return strings.TrimPrefix(h, "www.")
}

// domainListed reports whether host is one of the domains or a subdomain of one.
func domainListed(host string, list []string) bool {
	for _, d := range list {
		d = normHost(d)
		if d != "" && (host == d || strings.HasSuffix(host, "."+d)) {
			return true
		}
	}
	return false
}

// innocentExt are extensions a double-extension trick hides behind ("invoice.pdf.exe").
var innocentExt = []string{
	"pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "txt", "rtf", "csv",
	"jpg", "jpeg", "png", "gif", "webp", "heic", "mp3", "mp4", "mov", "avi", "zip", "rar",
}

// executableExt are extensions that run code when opened.
var executableExt = []string{
	"apk", "exe", "scr", "bat", "cmd", "com", "js", "jse", "vbs", "vbe", "wsf", "msi",
	"jar", "ps1", "pif", "hta", "lnk", "reg", "dll", "app", "dmg", "xapk", "apks",
}

// blockedDocument returns why a file name is blocked, or "" if it isn't: its extension is
// on the group's block list, or it hides an executable behind an innocent extension. The
// name is cleaned first, so invisible and bidi characters ("exe.pdf" written backwards with
// U+202E), trailing dots and spaces, and case don't hide the real extension.
func blockedDocument(name string, block []string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, name)
	clean = strings.TrimRight(clean, ". ")
	exts := extensions(clean)
	if len(exts) == 0 {
		return ""
	}
	last := exts[len(exts)-1]
	for _, b := range block {
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(b)), ".") == last {
			return fmt.Sprintf("document %q has blocked extension .%s", name, last)
		}
	}
	if len(exts) >= 2 && slices.Contains(executableExt, last) && slices.Contains(innocentExt, exts[len(exts)-2]) {
		return fmt.Sprintf("document %q hides .%s behind .%s", name, last, exts[len(exts)-2])
	}
	return ""
}

// executableMime maps executable mimetypes to the extension they stand for, so a renamed
// executable ("invoice.pdf" sent as an APK) is still caught.
var executableMime = map[string]string{
	"application/vnd.android.package-archive":       "apk",
	"application/x-msdownload":                      "exe",
	"application/x-msdos-program":                   "exe",
	"application/x-dosexec":                         "exe",
	"application/vnd.microsoft.portable-executable": "exe",
	"application/x-ms-installer":                    "msi",
	"application/x-msi":                             "msi",
	"application/java-archive":                      "jar",
	"application/x-bat":                             "bat",
	"application/javascript":                        "js",
	"text/javascript":                               "js",
}

// blockedMimetype returns why a document's declared mimetype is blocked, or "" if it isn't:
// it names an executable type whose extension is on the group's block list.
func blockedMimetype(mt string, block []string) string {
	mt, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(mt)), ";")
	ext, ok := executableMime[strings.TrimSpace(mt)]
	if !ok {
		return ""
	}
	for _, b := range block {
		if strings.TrimPrefix(strings.ToLower(strings.TrimSpace(b)), ".") == ext {
			return fmt.Sprintf("document of type %s (.%s) is blocked", mt, ext)
		}
	}
	return ""
}

// extensions returns the dot-separated extensions of a file name, outermost last
// ("a.pdf.exe" → [pdf exe]). Spaces around the dots are ignored.
func extensions(name string) []string {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return nil
	}
	var out []string
	for _, p := range parts[1:] {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
