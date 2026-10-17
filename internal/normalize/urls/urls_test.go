package urls

import (
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExtractFinds(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"go to https://example.com/a?b=c#d now", []string{"https://example.com/a?b=c#d"}},
		{"HTTP://Example.com/Path", []string{"http://Example.com/Path"}},
		{"www.example.com", []string{"http://www.example.com"}},
		{"see example.com/path", []string{"http://example.com/path"}},
		{"join t.me/spamchan", []string{"http://t.me/spamchan"}},
		{"wa.me/966551234567", []string{"http://wa.me/966551234567"}},
		{"chat.whatsapp.com/AbCdEf123", []string{"http://chat.whatsapp.com/AbCdEf123"}},
		{"пример.рф", []string{"http://пример.рф"}},
		{"مثال.السعودية", []string{"http://مثال.السعودية"}},
		{"xn--e1afmkfd.xn--p1ai", []string{"http://xn--e1afmkfd.xn--p1ai"}},
		{"a https://x.com b https://x.com c https://y.com", []string{"https://x.com", "https://y.com"}},
		{"example.com:8080/x", []string{"http://example.com:8080/x"}},
		{"item1.URL:https://example.org/me", []string{"https://example.org/me"}},
		{"visit Evil.Com today", []string{"http://Evil.Com"}},
		{"go to Evil.Net", []string{"http://Evil.Net"}},
		{"see work.Me", []string{"http://work.Me"}},
		{"work.Me/x too", []string{"http://work.Me/x"}},
	}
	for _, tt := range tests {
		if got := Extract(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("Extract(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExtractDeobfuscates(t *testing.T) {
	for _, in := range []string{
		"hxxps://evil.com/x",
		"HXXPS://evil.com/x",
		"https[:]//evil.com/x",
		"hxxps[:]//evil[.]com/x",
		"https://evil(.)com/x",
		"https://evil{.}com/x",
		"https://evil[dot]com/x",
		"https://evil(DOT)com/x",
		"https://evil . com/x",
		"https://evil．com/x",
		"https://evil。com/x",
		"https://evil｡com/x",
		"ｈｔｔｐｓ://ｅｖｉｌ.ｃｏｍ/x",
		"https://ev\u200Bil.com/x",
		"https://evil[[.]]com/x",
	} {
		if got := Extract(in); !slices.Equal(got, []string{"https://evil.com/x"}) {
			t.Errorf("Extract(%+q) = %q, want [https://evil.com/x] (defanged links must match the plain spelling)", in, got)
		}
	}
	for _, in := range []string{"evil . com", "evil .com", "evil [.] com", "evil[.]com", "www . evil . xyz123", "EVIL . COM/x", "evil . shop", "evil . me/x"} {
		if got := Extract(in); len(got) != 1 {
			t.Errorf("Extract(%q) = %q, want one URL", in, got)
		}
	}
}

func TestExtractBoundaries(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"visit https://example.com.", "https://example.com"},
		{"(https://example.com/x)", "https://example.com/x"},
		{"https://en.wikipedia.org/wiki/Foo_(bar)", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"https://en.wikipedia.org/wiki/Foo_(bar))", "https://en.wikipedia.org/wiki/Foo_(bar)"},
		{"is it https://example.com?", "https://example.com"},
		{"\"https://example.com/a\",", "https://example.com/a"},
		{"https://example.com/aمرحبا", "https://example.com/a"},
		{"example.comمرحبا", "http://example.com"},
		{"https://example.com، و", "https://example.com"},
		{"<https://example.com/x>", "https://example.com/x"},
		{"chat . whatsapp . com/AbCdEf123", "http://chat.whatsapp.com/AbCdEf123"},
		{"chat . whatsapp.com/AbCdEf123", "http://chat.whatsapp.com/AbCdEf123"},
		{"join now...chat.whatsapp.com/AbC", "http://chat.whatsapp.com/AbC"},
		{"انضم…chat.whatsapp.com/AbC", "http://chat.whatsapp.com/AbC"},
		{"wow...https://t.me/x", "https://t.me/x"},
	}
	for _, tt := range tests {
		if got := Extract(tt.in); !slices.Equal(got, []string{tt.want}) {
			t.Errorf("Extract(%q) = %q, want [%s]", tt.in, got, tt.want)
		}
	}
}

func TestExtractIgnores(t *testing.T) {
	for _, in := range []string{
		"mail ali@example.com",
		"pi is 3.14",
		"version v1.2.3",
		"server 192.168.1.1",
		"e.g. this",
		"at 9 a.m. today",
		"BEGIN:VCARD\nitem1.URL:\nEND:VCARD",
		"open report.pdf and photo.jpg",
		"I'm done . now",
		"This is the best. Top quality",
		"I'm done. Me too",
		"Go to the market. Shop now",
		"Watch now. Live soon",
		"More soon. Info later",
		"work.Me too",
		"ok . me too",
		"we are on . live now",
		"a.b",
		"no links here",
		"https://",
		"http:///path",
		"これは。テスト",
	} {
		if got := Extract(in); len(got) != 0 {
			t.Errorf("Extract(%q) = %q, want none", in, got)
		}
	}
}

func TestExtractBounded(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		b.WriteString("https://e")
		b.WriteString(strings.Repeat("x", i))
		b.WriteString(".com ")
	}
	if got := Extract(b.String()); len(got) != MaxURLs {
		t.Errorf("Extract returned %d URLs, want %d", len(got), MaxURLs)
	}
	long := "https://example.com/" + strings.Repeat("a", 10000)
	got := Extract(long)
	if len(got) != 1 || len(got[0]) > MaxURLBytes {
		t.Errorf("Extract(long) = %d URLs, first %d bytes; want one of at most %d", len(got), len(got[0]), MaxURLBytes)
	}
	for name, in := range map[string]string{
		"dots":      strings.Repeat("a.", 128<<10),
		"spaced":    strings.Repeat("www . a ", 32<<10),
		"brackets":  "https://x.com/" + strings.Repeat(")", 256<<10),
		"defang":    strings.Repeat("[[.]", 64<<10),
		"labels":    strings.Repeat("ab ", 85<<10),
		"many urls": strings.Repeat("x.com/a ", 32<<10),
	} {
		start := time.Now()
		Extract(in)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("Extract(%s) took %v, want under 3s", name, d)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	tests := []struct {
		in, canonical, host, unicodeHost, domain string
	}{
		{"HTTP://EXAMPLE.COM:80", "http://example.com/", "example.com", "example.com", "example.com"},
		{"http://example.com.", "http://example.com/", "example.com", "example.com", "example.com"},
		{"http://example.com/#x", "http://example.com/", "example.com", "example.com", "example.com"},
		{"https://example.com:443/A/B?Q=1", "https://example.com/A/B?Q=1", "example.com", "example.com", "example.com"},
		{"https://example.com:8443/", "https://example.com:8443/", "example.com", "example.com", "example.com"},
		{"http://пример.рф/", "http://xn--e1afmkfd.xn--p1ai/", "xn--e1afmkfd.xn--p1ai", "пример.рф", "xn--e1afmkfd.xn--p1ai"},
		{"http://xn--e1afmkfd.xn--p1ai/", "http://xn--e1afmkfd.xn--p1ai/", "xn--e1afmkfd.xn--p1ai", "пример.рф", "xn--e1afmkfd.xn--p1ai"},
		{"http://a.b.example.co.uk/x", "http://a.b.example.co.uk/x", "a.b.example.co.uk", "a.b.example.co.uk", "example.co.uk"},
		{"https://evil.github.io/x", "https://evil.github.io/x", "evil.github.io", "evil.github.io", "evil.github.io"},
		{"https://x.blogspot.com/", "https://x.blogspot.com/", "x.blogspot.com", "x.blogspot.com", "x.blogspot.com"},
		{"http://192.168.0.1:8080/x", "http://192.168.0.1:8080/x", "192.168.0.1", "192.168.0.1", ""},
		{"http://[::1]/", "http://[::1]/", "::1", "::1", ""},
		{"http://co.uk/", "http://co.uk/", "co.uk", "co.uk", ""},
	}
	for _, tt := range tests {
		u, err := Canonicalize(tt.in)
		if err != nil {
			t.Errorf("Canonicalize(%q): %v", tt.in, err)
			continue
		}
		if u.Canonical != tt.canonical || u.Host != tt.host || u.UnicodeHost != tt.unicodeHost || u.Domain != tt.domain {
			t.Errorf("Canonicalize(%q) = %+v, want canonical %q host %q unicode %q domain %q",
				tt.in, u, tt.canonical, tt.host, tt.unicodeHost, tt.domain)
		}
	}
	for _, bad := range []string{"", "ftp://example.com/", "mailto:a@b.com", "http://", "https:///x", "http://exa mple.com/", "http://xn--a.com/"} {
		if u, err := Canonicalize(bad); err == nil {
			t.Errorf("Canonicalize(%q) = %+v, want error", bad, u)
		}
	}
}

func TestCanonicalizeTracking(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://x.com/p?utm_source=a&id=7&UTM_Medium=b", "https://x.com/p?id=7"},
		{"https://x.com/p?fbclid=1&gclid=2&dclid=3&gbraid=4&wbraid=5&msclkid=6&igshid=7&mc_cid=8&mc_eid=9&yclid=10&_ga=11", "https://x.com/p"},
		{"https://x.com/?b=2&a=1&utm_x=3", "https://x.com/?b=2&a=1"},
		{"https://wa.me/966551234567?text=Hello%20There&utm_source=x", "https://wa.me/966551234567?text=Hello%20There"},
		{"https://x.com/p?", "https://x.com/p"},
	}
	for _, tt := range tests {
		u, err := Canonicalize(tt.in)
		if err != nil || u.Canonical != tt.want {
			t.Errorf("Canonicalize(%q) = %q, %v; want %q", tt.in, u.Canonical, err, tt.want)
		}
	}
}

func TestCanonicalizeTargets(t *testing.T) {
	tests := []struct {
		in   string
		want *Target
	}{
		{"https://chat.whatsapp.com/AbCdEf123", &Target{TargetGroupInvite, "AbCdEf123"}},
		{"https://chat.whatsapp.com/invite/AbCdEf123", &Target{TargetGroupInvite, "AbCdEf123"}},
		{"https://CHAT.WHATSAPP.COM/AbC", &Target{TargetGroupInvite, "AbC"}},
		{"https://whatsapp.com/channel/0029VaXyZ", &Target{TargetChannel, "0029VaXyZ"}},
		{"https://www.whatsapp.com/channel/0029VaXyZ", &Target{TargetChannel, "0029VaXyZ"}},
		{"https://wa.me/966551234567", &Target{TargetPhone, "+966551234567"}},
		{"https://api.whatsapp.com/send?phone=966551234567&text=hi", &Target{TargetPhone, "+966551234567"}},
		{"https://wa.me/message/ABCDEF", &Target{TargetMessage, "ABCDEF"}},
		{"https://wa.me/qr/XYZ123", &Target{TargetQR, "XYZ123"}},
		{"https://wa.me/123", nil},
		{"https://wa.me/", nil},
		{"https://chat.whatsapp.com/", nil},
		{"https://chat.whatsapp.com.evil.xyz/AbC", nil},
		{"https://chat-whatsapp.com/AbC", nil},
		{"https://wa.me.example.com/966551234567", nil},
		{"https://example.com/channel/x", nil},
	}
	for _, tt := range tests {
		u, err := Canonicalize(tt.in)
		if err != nil {
			t.Errorf("Canonicalize(%q): %v", tt.in, err)
			continue
		}
		if (u.Target == nil) != (tt.want == nil) || u.Target != nil && *u.Target != *tt.want {
			t.Errorf("Canonicalize(%q).Target = %+v, want %+v (only WhatsApp's exact hosts are native targets)", tt.in, u.Target, tt.want)
		}
	}
}

func TestExtractThenCanonicalize(t *testing.T) {
	a := Extract("hxxps://Chat.WhatsApp[.]com/AbC?utm_source=x")
	if len(a) != 1 {
		t.Fatalf("Extract = %q, want one", a)
	}
	u, err := Canonicalize(a[0])
	if err != nil || u.Canonical != "https://chat.whatsapp.com/AbC" || u.Target == nil || u.Target.ID != "AbC" {
		t.Errorf("Canonicalize(%q) = %+v, %v; want an invite target AbC", a[0], u, err)
	}
}

func FuzzExtract(f *testing.F) {
	for _, s := range []string{
		"https://example.com/a?b#c", "hxxps://evil[.]com", "www . evil . com", "пример.рф", "ali@example.com",
		"(https://x.com/Foo_(bar))", "wa.me/966551234567", "evil[[.]]com", "x。com", "https://[::1]:80/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Extract(in)
		if len(got) > MaxURLs {
			t.Fatalf("Extract returned %d URLs", len(got))
		}
		for _, u := range got {
			p, err := url.Parse(u)
			if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Hostname() == "" {
				t.Fatalf("Extract(%+q) gave %+q: not an http(s) URL with a host (%v)", in, u, err)
			}
			if len(u) > MaxURLBytes {
				t.Fatalf("Extract(%+q) gave %d bytes", in, len(u))
			}
			if again := Extract(u); !slices.Equal(again, []string{u}) {
				t.Fatalf("Extract(%+q) = %q, want [%q] (results are fixed points)", u, again, u)
			}
		}
	})
}

func FuzzCanonicalize(f *testing.F) {
	for _, s := range []string{
		"HTTP://EXAMPLE.COM:80", "http://пример.рф/", "https://wa.me/966551234567?utm_x=1", "http://[::1]:443/",
		"https://chat.whatsapp.com/invite/x", "http://a.b.example.co.uk/x?y#z",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		u, err := Canonicalize(in)
		if err != nil {
			return
		}
		if u.Host != strings.ToLower(u.Host) {
			t.Fatalf("Canonicalize(%q).Host = %q is not lower-case", in, u.Host)
		}
		for _, r := range u.Host {
			if r >= 0x80 {
				t.Fatalf("Canonicalize(%q).Host = %q is not ASCII", in, u.Host)
			}
		}
		again, err := Canonicalize(u.Canonical)
		if err != nil || again.Canonical != u.Canonical || again.Host != u.Host || again.Domain != u.Domain ||
			(again.Target == nil) != (u.Target == nil) || again.Target != nil && *again.Target != *u.Target {
			t.Fatalf("Canonicalize is not idempotent: %q gave %+v, then %+v, %v", in, u, again, err)
		}
	})
}
