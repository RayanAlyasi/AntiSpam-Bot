package urls

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
)

// ErrNotHTTP is returned by Canonicalize for input that isn't an http(s)
// URL with a valid host.
var ErrNotHTTP = errors.New("not an http(s) URL with a valid host")

// URL is a canonicalized link.
type URL struct {
	// Canonical is the form used for matching and dedup: lower-case scheme and
	// host, host in ASCII, no default port, no fragment, no tracking
	// parameters, and / for an empty path. Path and query case is kept.
	Canonical string
	// Host is the canonical host in ASCII (punycode).
	Host string
	// UnicodeHost is Host for display, with IDN labels in Unicode.
	UnicodeHost string
	// Domain is the registrable domain (eTLD+1, private suffixes included,
	// so x.github.io stays x.github.io). Empty for IPs and bare suffixes.
	Domain string
	// Target is set for WhatsApp-native links; nil otherwise.
	Target *Target
}

// TargetKind says what a WhatsApp-native link points to.
type TargetKind string

// WhatsApp-native target kinds.
const (
	TargetGroupInvite TargetKind = "group_invite"
	TargetChannel     TargetKind = "channel"
	TargetPhone       TargetKind = "phone"
	TargetMessage     TargetKind = "message"
	TargetQR          TargetKind = "qr"
)

// Target is a WhatsApp group invite, channel, chat or short link.
type Target struct {
	Kind TargetKind
	// ID is the invite or link code as written (case-sensitive), the channel
	// ID, or the E.164 number for TargetPhone.
	ID string
}

var defaultPorts = map[string]string{"http": "80", "https": "443"}

// trackingParams are query parameters that only identify the click.
var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "igshid": true, "mc_cid": true, "mc_eid": true, "yclid": true,
	"_ga": true, "_gl": true, "twclid": true, "ttclid": true,
}

// Canonicalize returns the canonical form of an http(s) URL, its registrable
// domain and, for WhatsApp links, the target it points to.
func Canonicalize(raw string) (URL, error) {
	p, err := url.Parse(raw)
	if err != nil {
		return URL{}, fmt.Errorf("%w: %w", ErrNotHTTP, err)
	}
	scheme := strings.ToLower(p.Scheme)
	if scheme != "http" && scheme != "https" || p.Opaque != "" {
		return URL{}, ErrNotHTTP
	}
	host, unicodeHost, isIP, err := canonicalHost(p.Hostname())
	if err != nil {
		return URL{}, err
	}
	hostPort := host
	if isIP && strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}
	if port := p.Port(); port != "" && port != defaultPorts[scheme] {
		hostPort += ":" + port
	}
	path := p.EscapedPath()
	if path == "" {
		path = "/"
	}
	c := scheme + "://" + hostPort + path
	if q := stripTracking(p.RawQuery); q != "" {
		c += "?" + q
	}
	out := URL{Canonical: c, Host: host, UnicodeHost: unicodeHost}
	if !isIP {
		if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
			out.Domain = d
		}
	}
	out.Target = whatsAppTarget(host, p)
	return out, nil
}

// canonicalHost lower-cases a host, drops its trailing dot and converts it to
// ASCII, returning the Unicode form too.
func canonicalHost(h string) (ascii, uni string, isIP bool, err error) {
	h = strings.TrimSuffix(h, ".")
	if h == "" || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") {
		return "", "", false, ErrNotHTTP
	}
	if ip := net.ParseIP(h); ip != nil {
		s := ip.String()
		return s, s, true, nil
	}
	ascii, err = idna.Lookup.ToASCII(h)
	if err != nil || ascii == "" {
		return "", "", false, fmt.Errorf("%w: invalid host", ErrNotHTTP)
	}
	ascii = strings.ToLower(ascii)
	uni, err = idna.Display.ToUnicode(ascii)
	if err != nil {
		uni = ascii
	}
	return ascii, uni, false, nil
}

// stripTracking removes tracking parameters from a raw query, keeping the
// others in order and as written.
func stripTracking(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(raw, "&")
	kept := parts[:0]
	for _, part := range parts {
		if part == "" {
			continue
		}
		key, _, _ := strings.Cut(part, "=")
		if k, err := url.QueryUnescape(key); err == nil {
			key = k
		}
		key = strings.ToLower(key)
		if trackingParams[key] || strings.HasPrefix(key, "utm_") {
			continue
		}
		kept = append(kept, part)
	}
	return strings.Join(kept, "&")
}

// whatsAppTarget classifies links on WhatsApp's own hosts. The host must
// match exactly, so look-alike domains never get a target.
func whatsAppTarget(host string, p *url.URL) *Target {
	segs := strings.FieldsFunc(p.Path, func(r rune) bool { return r == '/' })
	switch host {
	case "chat.whatsapp.com":
		if len(segs) >= 2 && segs[0] == "invite" {
			return &Target{Kind: TargetGroupInvite, ID: segs[1]}
		}
		if len(segs) >= 1 && segs[0] != "invite" {
			return &Target{Kind: TargetGroupInvite, ID: segs[0]}
		}
	case "whatsapp.com", "www.whatsapp.com":
		if len(segs) >= 2 && segs[0] == "channel" {
			return &Target{Kind: TargetChannel, ID: segs[1]}
		}
	case "wa.me":
		switch {
		case len(segs) >= 2 && segs[0] == "message":
			return &Target{Kind: TargetMessage, ID: segs[1]}
		case len(segs) >= 2 && segs[0] == "qr":
			return &Target{Kind: TargetQR, ID: segs[1]}
		case len(segs) >= 1:
			return phoneTarget(segs[0])
		}
	case "api.whatsapp.com":
		if len(segs) >= 1 && segs[0] == "send" {
			return phoneTarget(p.Query().Get("phone"))
		}
	}
	return nil
}

func phoneTarget(digits string) *Target {
	digits = strings.TrimPrefix(digits, "+")
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return nil
	}
	e164, err := normalize.ParsePhone("+"+digits, "")
	if err != nil {
		return nil
	}
	return &Target{Kind: TargetPhone, ID: e164}
}
