package review

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// render builds the review message (§9.5 "The review message").
func render(it Item, cfg Config, texts Localizer) wa.OutgoingMessage {
	lang := cfg.Language
	t := func(key string, vars map[string]string) string { return texts.T(lang, key, vars) }

	var lines []string
	header := "review.header"
	if it.Kind == KindHacked || it.Subject.Hacked {
		header = "review.header_hacked"
	}
	if it.Kind == KindAlert {
		header = "review.header_alert"
	}
	lines = append(lines, t(header, map[string]string{"code": it.Code, "group": it.GroupName}))

	who := []string{}
	if it.Subject.Name != "" {
		who = append(who, "~"+redact(it.Subject.Name))
	}
	if it.Subject.Phone != "" {
		who = append(who, maskNumber("+"+it.Subject.Phone.User()))
	}
	if it.Subject.MemberFor > 0 {
		who = append(who, t("review.member_for", map[string]string{"age": humanAge(it.Subject.MemberFor)}))
	}
	if it.Subject.EarlierMessages > 0 {
		who = append(who, t("review.earlier", map[string]string{"n": strconv.Itoa(it.Subject.EarlierMessages)}))
	}
	if len(who) > 0 {
		lines = append(lines, t("review.who", map[string]string{"who": strings.Join(who, " · ")}))
	}

	if it.Evidence.Text != "" || len(it.Evidence.Links) > 0 {
		what := ""
		if it.Evidence.Text != "" {
			what = "\"" + truncate(redact(it.Evidence.Text), MaxEvidenceText) + "\""
		}
		if len(it.Evidence.Links) > 0 {
			links := make([]string, 0, len(it.Evidence.Links))
			for _, l := range it.Evidence.Links {
				links = append(links, truncate(redact(l), 60))
			}
			what = strings.TrimSpace(what + " + " + t("review.link", nil) + " " + strings.Join(links, " → "))
			what = strings.TrimPrefix(what, "+ ")
		}
		lines = append(lines, t("review.what", map[string]string{"what": what}))
	}

	if len(it.Evidence.Why) > 0 {
		why := make([]string, 0, len(it.Evidence.Why))
		for _, r := range it.Evidence.Why {
			why = append(why, fmt.Sprintf("%s (%+.1f)", redact(r.Text), r.Weight))
		}
		lines = append(lines, t("review.why", map[string]string{"why": strings.Join(why, " · ")}))
	}

	facts := []string{}
	if it.Evidence.Score > 0 {
		facts = append(facts, t("review.score", map[string]string{"score": strconv.FormatFloat(it.Evidence.Score, 'f', 2, 64)}))
	}
	if it.Evidence.Source != "" {
		facts = append(facts, t("review.source", map[string]string{"source": it.Evidence.Source}))
	}
	if it.Message != nil {
		key := "review.msg_visible"
		if it.Evidence.MessageDeleted {
			key = "review.msg_deleted"
		}
		facts = append(facts, t(key, nil))
	}
	if it.Evidence.WhyAsking != "" {
		facts = append(facts, t("review.why_asking", map[string]string{"why": it.Evidence.WhyAsking}))
	}
	if len(facts) > 0 {
		lines = append(lines, strings.Join(facts, " · "))
	}

	if it.Kind != KindAlert {
		how := "review.how"
		if it.Kind == KindHacked || it.Subject.Hacked {
			how = "review.how_hacked"
		}
		lines = append(lines, t(how, nil))
		lines = append(lines, t("review.auto", map[string]string{
			"in":       humanDuration(it.Deadline.Sub(it.CreatedAt)),
			"fallback": t("review.fallback."+string(it.OnTimeout), nil),
		}))
	}

	msg := wa.OutgoingMessage{Text: strings.Join(lines, "\n")}
	if it.Evidence.Thumbnail != nil && len(it.Evidence.Thumbnail.Data) > 0 && !cfg.BlurMedia {
		img := *it.Evidence.Thumbnail
		msg.Image = &img
	}
	return msg
}

// handledLine is the outcome line appended to a decided review: "✔ Handled by ~Sara · banned · 12:41".
func handledLine(it Item, cfg Config, texts Localizer) string {
	return texts.T(cfg.Language, "review.handled", map[string]string{
		"name":     DeciderName(it, texts, cfg.Language),
		"decision": texts.T(cfg.Language, "review.decision."+string(it.Decision), nil),
		"time":     it.DecidedAt.UTC().Format("15:04"),
	})
}

// humanDuration formats a timeout: "6h", "15m", "1h30m", "2d".
func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d <= 0:
		return "0m"
	case d%(24*time.Hour) == 0:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d%time.Hour == 0:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	default:
		return strconv.Itoa(int(d/time.Hour)) + "h" + strconv.Itoa(int((d%time.Hour)/time.Minute)) + "m"
	}
}

// humanAge formats a membership age coarsely: days, then months, then years.
func humanAge(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	switch {
	case days < 1:
		return "<1d"
	case days < 60:
		return strconv.Itoa(days) + "d"
	case days < 730:
		return strconv.Itoa(days/30) + "mo"
	default:
		return strconv.Itoa(days/365) + "y"
	}
}
