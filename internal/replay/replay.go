// Package replay feeds recorded or fixture events through the full pipeline and reports the
// verdicts, so two rule or weight versions can be compared on the same traffic (DESIGN.md §18,
// §4 "replayable inbox"). A replay never talks to WhatsApp: targets run the bot against the fake
// client, and never persist anything in the source database.
package replay

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Record kinds in a fixture file.
const (
	KindGroup       = "group"        // onboard a protected group
	KindJoin        = "join"         // a member joins now, so they're new
	KindConfirmSpam = "confirm_spam" // an admin confirmed a text as spam
	KindBlacklist   = "blacklist"    // a number on the group's manual blacklist
	KindAdvance     = "advance"      // let time pass
	KindMessage     = "message"      // an incoming group message
)

// Record is one line of a JSONL fixture file. Kind picks which fields apply; a record without
// a kind is a message.
type Record struct {
	Kind string `json:"kind,omitempty"`

	Group wa.JID `json:"group,omitempty"`

	// group
	Admins      []Member      `json:"admins,omitempty"`
	Members     []Member      `json:"members,omitempty"` // pre-existing, so established
	Mode        pipeline.Mode `json:"mode,omitempty"`
	Preset      string        `json:"preset,omitempty"`
	Enforcement string        `json:"enforcement,omitempty"`
	ReviewGroup wa.JID        `json:"review_group,omitempty"`

	// join
	Member *Member `json:"member,omitempty"`

	// confirm_spam (Text) and blacklist (Number)
	Number string `json:"number,omitempty"`

	// advance
	Duration string `json:"duration,omitempty"`

	// message
	Sender   wa.JID       `json:"sender,omitempty"`
	SenderPN wa.JID       `json:"sender_pn,omitempty"`
	ID       wa.MessageID `json:"id,omitempty"`
	Time     time.Time    `json:"time,omitzero"`
	Text     string       `json:"text,omitempty"`
	VCards   []string     `json:"vcards,omitempty"`
	// Expect, when set on a message, is the label a person gave it: "spam" or "ham". The
	// report counts how each label was handled.
	Expect string `json:"expect,omitempty"`
}

// Member is a person in a group: the LID always, the phone-number JID when known.
type Member struct {
	LID wa.JID `json:"lid"`
	PN  wa.JID `json:"pn,omitempty"`
}

// Load reads a JSONL fixture: one record per line; blank lines and lines starting with # are
// skipped. Errors name the line.
func Load(r io.Reader) ([]Record, error) {
	var out []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		var rec Record
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if rec.Kind == "" {
			rec.Kind = KindMessage
		}
		if err := rec.validate(); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	return out, nil
}

func (r Record) validate() error {
	need := func(ok bool, what string) error {
		if !ok {
			return fmt.Errorf("%s record needs %s", r.Kind, what)
		}
		return nil
	}
	switch r.Kind {
	case KindGroup:
		return need(r.Group.IsGroup(), "a group JID")
	case KindJoin:
		return need(r.Group != "" && r.Member != nil && r.Member.LID != "", "group and member.lid")
	case KindConfirmSpam:
		return need(r.Group != "" && strings.TrimSpace(r.Text) != "", "group and text")
	case KindBlacklist:
		return need(r.Group != "" && r.Number != "", "group and number")
	case KindAdvance:
		d, err := time.ParseDuration(r.Duration)
		return need(err == nil && d > 0, "a positive duration")
	case KindMessage:
		if r.Expect != "" && r.Expect != "spam" && r.Expect != "ham" {
			return fmt.Errorf("expect %q: want spam or ham", r.Expect)
		}
		return need(r.Group != "" && r.Sender != "" && r.ID != "", "group, sender and id")
	default:
		return fmt.Errorf("unknown kind %q", r.Kind)
	}
}

// Target is the bot a replay runs against, such as pipelinetest's harness: Postgres plus the
// fake WhatsApp client. Setup calls never reach WhatsApp.
type Target interface {
	AddGroup(r Record) error
	Join(group wa.JID, m Member) error
	ConfirmSpam(group wa.JID, text string) error
	Blacklist(group wa.JID, number string) error
	Advance(d time.Duration) error
	Deliver(r Record) (pipeline.Verdict, error)
}

// Result is the verdict for one replayed message.
type Result struct {
	Record  Record
	Verdict pipeline.Verdict
}

// Report is the outcome of one replay.
type Report struct {
	Results []Result
}

// Run replays records against t in order. It stops at the first error, returning the report so far.
func Run(t Target, records []Record) (Report, error) {
	var rep Report
	for i, r := range records {
		var err error
		switch r.Kind {
		case KindGroup:
			err = t.AddGroup(r)
		case KindJoin:
			err = t.Join(r.Group, *r.Member)
		case KindConfirmSpam:
			err = t.ConfirmSpam(r.Group, r.Text)
		case KindBlacklist:
			err = t.Blacklist(r.Group, r.Number)
		case KindAdvance:
			d, _ := time.ParseDuration(r.Duration)
			err = t.Advance(d)
		case KindMessage:
			var v pipeline.Verdict
			if v, err = t.Deliver(r); err == nil {
				rep.Results = append(rep.Results, Result{Record: r, Verdict: v})
			}
		}
		if err != nil {
			return rep, fmt.Errorf("record %d (%s %s): %w", i+1, r.Kind, cmp.Or(string(r.ID), string(r.Group)), err)
		}
	}
	return rep, nil
}

// Line renders one result: group, message, mode, band, source, route and actions.
func (r Result) Line() string {
	v := r.Verdict
	acts := "-"
	if len(v.Actions) > 0 {
		parts := make([]string, len(v.Actions))
		for i, a := range v.Actions {
			parts[i] = string(a)
		}
		acts = strings.Join(parts, "+")
	}
	line := fmt.Sprintf("%s %s mode=%s band=%s source=%s route=%s actions=%s",
		v.Group, v.Message, v.Mode, v.Band, cmp.Or(string(v.Source), "none"), v.Route, acts)
	if r.Record.Expect != "" {
		line += " expect=" + r.Record.Expect
	}
	return line
}

// Summary counts results per band, source, route and action, and for labelled messages how
// spam was caught (deleted, sent to admins, missed) and whether any ham was acted on.
type Summary struct {
	Messages int
	Bands    map[pipeline.Band]int
	Sources  map[pipeline.Source]int
	Routes   map[pipeline.Route]int
	Actions  map[pipeline.ActionKind]int

	Spam, SpamDeleted, SpamToAdmins, SpamMissed int
	Ham, HamDeleted, HamBanned, HamToAdmins     int
}

// Summarize builds the summary of a report.
func (rep Report) Summarize() Summary {
	s := Summary{Bands: map[pipeline.Band]int{}, Sources: map[pipeline.Source]int{}, Routes: map[pipeline.Route]int{}, Actions: map[pipeline.ActionKind]int{}}
	for _, r := range rep.Results {
		v := r.Verdict
		s.Messages++
		s.Bands[v.Band]++
		s.Sources[v.Source]++
		s.Routes[v.Route]++
		for _, a := range v.Actions {
			s.Actions[a]++
		}
		del, ban, review := slices.Contains(v.Actions, pipeline.ActionDelete), slices.Contains(v.Actions, pipeline.ActionBan), slices.Contains(v.Actions, pipeline.ActionReview)
		switch r.Record.Expect {
		case "spam":
			s.Spam++
			switch {
			case del:
				s.SpamDeleted++
			case review:
				s.SpamToAdmins++
			default:
				s.SpamMissed++
			}
		case "ham":
			s.Ham++
			if del {
				s.HamDeleted++
			}
			if ban {
				s.HamBanned++
			}
			if review && !del {
				s.HamToAdmins++
			}
		}
	}
	return s
}

// String renders the summary as a few lines of text.
func (s Summary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "messages=%d\n", s.Messages)
	fmt.Fprintf(&b, "bands: %s\n", counts(s.Bands))
	fmt.Fprintf(&b, "sources: %s\n", counts(s.Sources))
	fmt.Fprintf(&b, "routes: %s\n", counts(s.Routes))
	fmt.Fprintf(&b, "actions: %s\n", counts(s.Actions))
	if s.Spam > 0 {
		fmt.Fprintf(&b, "spam: %d deleted, %d to admins, %d missed (of %d)\n", s.SpamDeleted, s.SpamToAdmins, s.SpamMissed, s.Spam)
	}
	if s.Ham > 0 {
		fmt.Fprintf(&b, "ham: %d deleted, %d banned, %d to admins (of %d)\n", s.HamDeleted, s.HamBanned, s.HamToAdmins, s.Ham)
	}
	return b.String()
}

func counts[K ~string](m map[K]int) string {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", cmp.Or(string(k), "none"), m[k])
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

// Change is one message whose verdict differs between two replays.
type Change struct {
	Group   wa.JID
	Message wa.MessageID
	Before  string // the result line in the first replay, or "" when it wasn't there
	After   string
}

// Diff lists the messages whose band, source, route or actions differ between two replays of
// the same fixture, in the order of the first one.
func Diff(before, after Report) []Change {
	type key struct {
		g wa.JID
		m wa.MessageID
	}
	index := func(rep Report) (map[key]string, []key) {
		lines, order := map[key]string{}, []key{}
		for _, r := range rep.Results {
			k := key{r.Verdict.Group, r.Verdict.Message}
			if _, seen := lines[k]; !seen {
				order = append(order, k)
			}
			lines[k] = r.Line()
		}
		return lines, order
	}
	a, orderA := index(before)
	b, orderB := index(after)
	var out []Change
	for _, k := range orderA {
		if a[k] != b[k] {
			out = append(out, Change{Group: k.g, Message: k.m, Before: a[k], After: b[k]})
		}
	}
	for _, k := range orderB {
		if _, ok := a[k]; !ok {
			out = append(out, Change{Group: k.g, Message: k.m, After: b[k]})
		}
	}
	return out
}

// Write prints a report: one line per message, then the summary.
func Write(w io.Writer, rep Report) error {
	var errs []error
	for _, r := range rep.Results {
		_, err := fmt.Fprintln(w, r.Line())
		errs = append(errs, err)
	}
	_, err := fmt.Fprint(w, "--\n", rep.Summarize().String())
	return errors.Join(append(errs, err)...)
}
