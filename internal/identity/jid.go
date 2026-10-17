package identity

import (
	"strconv"
	"strings"

	"github.com/nyaruka/phonenumbers"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// normalize reduces a person JID to its non-AD form (DESIGN.md §7.7: ToNonAD), so a device
// ("123:5@lid") or agent ("123.1:5@s.whatsapp.net") suffix never creates a second identity. It
// reports the identity kind, or false for anything that can't identify a person: groups,
// channels, broadcast lists, malformed or empty JIDs.
func normalize(j wa.JID) (wa.JID, Kind, bool) {
	user, server, found := strings.Cut(strings.TrimSpace(string(j)), "@")
	if !found {
		return "", "", false
	}
	if i := strings.IndexAny(user, ".:"); i >= 0 {
		user = user[:i]
	}
	if user == "" || !digitsOnly(user) {
		return "", "", false
	}
	var kind Kind
	switch server {
	case wa.ServerLID:
		kind = KindLID
	case wa.ServerUser:
		kind = KindPN
	default:
		return "", "", false
	}
	return wa.JID(user + "@" + server), kind, true
}

func digitsOnly(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// CountryCode returns the calling code of a phone-number JID ("966" for 9665…@s.whatsapp.net),
// or "" when it isn't a parsable phone number.
func CountryCode(pn wa.JID) string {
	num, ok := parsePN(pn)
	if !ok {
		return ""
	}
	return strconv.Itoa(int(num.GetCountryCode()))
}

// NumberPrefix returns the country code plus the first two digits of the national number
// ("96650" for +966 50…), the granularity the number-prefix statistics use (DESIGN.md §7.6,
// §7.7). It's "" when the JID isn't a parsable phone number.
func NumberPrefix(pn wa.JID) string {
	num, ok := parsePN(pn)
	if !ok {
		return ""
	}
	nsn := phonenumbers.GetNationalSignificantNumber(num)
	if len(nsn) > 2 {
		nsn = nsn[:2]
	}
	return strconv.Itoa(int(num.GetCountryCode())) + nsn
}

func parsePN(pn wa.JID) (*phonenumbers.PhoneNumber, bool) {
	norm, kind, ok := normalize(pn)
	if !ok || kind != KindPN {
		return nil, false
	}
	num, err := phonenumbers.Parse("+"+norm.User(), "")
	if err != nil || num.GetCountryCode() == 0 {
		return nil, false
	}
	return num, true
}
