package replay

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

const fixture = `
# a group, a confirmed template and three messages
{"kind":"group","group":"1@g.us","members":[{"lid":"2@lid"}],"review_group":"9@g.us"}
{"kind":"confirm_spam","group":"1@g.us","text":"win money now"}
{"kind":"join","group":"1@g.us","member":{"lid":"3@lid","pn":"966500000001@s.whatsapp.net"}}
{"kind":"advance","duration":"1m"}
{"group":"1@g.us","sender":"3@lid","id":"A","text":"win money now","expect":"spam"}
{"group":"1@g.us","sender":"2@lid","id":"B","text":"win money now","expect":"spam"}
{"kind":"message","group":"1@g.us","sender":"2@lid","id":"C","text":"lunch at noon?","expect":"ham"}
`

// fakeTarget flags texts as spam when an admin confirmed them, like a tiny bot.
type fakeTarget struct {
	calls     []string
	confirmed map[string]bool
	members   map[wa.JID]bool // established
	fail      string
}

func (f *fakeTarget) AddGroup(r Record) error {
	f.calls = append(f.calls, "group "+string(r.Group))
	for _, m := range r.Members {
		f.members[m.LID] = true
	}
	return nil
}

func (f *fakeTarget) Join(g wa.JID, m Member) error {
	f.calls = append(f.calls, "join "+string(m.LID))
	return nil
}

func (f *fakeTarget) ConfirmSpam(g wa.JID, text string) error {
	f.confirmed[text] = true
	return nil
}
func (f *fakeTarget) Blacklist(wa.JID, string) error { return nil }
func (f *fakeTarget) Advance(d time.Duration) error {
	f.calls = append(f.calls, "advance "+d.String())
	return nil
}

func (f *fakeTarget) Deliver(r Record) (pipeline.Verdict, error) {
	if string(r.ID) == f.fail {
		return pipeline.Verdict{}, errors.New("boom")
	}
	v := pipeline.Verdict{Group: r.Group, Message: r.ID, Sender: r.Sender, Mode: pipeline.ModeEnforce, Band: pipeline.BandClean, Route: pipeline.RouteNone}
	if f.confirmed[r.Text] {
		v.Band, v.Source = pipeline.BandCertain, pipeline.SourceKnown
		if f.members[r.Sender] {
			v.Route, v.Actions = pipeline.RouteAsk, []pipeline.ActionKind{pipeline.ActionDelete, pipeline.ActionReview}
		} else {
			v.Route, v.Actions = pipeline.RouteAutoBan, []pipeline.ActionKind{pipeline.ActionDelete, pipeline.ActionBan}
		}
	}
	return v, nil
}

func newFake() *fakeTarget {
	return &fakeTarget{confirmed: map[string]bool{}, members: map[wa.JID]bool{}}
}

func TestLoadAndRun(t *testing.T) {
	recs, err := Load(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 7 || recs[4].Kind != KindMessage {
		t.Fatalf("loaded %d records (first message kind %q), want 7 and an implicit message kind", len(recs), recs[4].Kind)
	}
	f := newFake()
	rep, err := Run(f, recs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, ", "); got != "group 1@g.us, join 3@lid, advance 1m0s" {
		t.Fatalf("setup calls %q", got)
	}
	var out bytes.Buffer
	if err := Write(&out, rep); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"1@g.us A mode=enforce band=certain source=known route=auto_ban actions=delete+ban expect=spam",
		"1@g.us B mode=enforce band=certain source=known route=ask actions=delete+review expect=spam",
		"1@g.us C mode=enforce band=clean source=none route=none actions=- expect=ham",
		"messages=3",
		"bands: certain=2 clean=1",
		"routes: ask=1 auto_ban=1 none=1",
		"spam: 2 deleted, 0 to admins, 0 missed (of 2)",
		"ham: 0 deleted, 0 banned, 0 to admins (of 1)",
	}
	for _, w := range want {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}
}

func TestLoadRejectsBadRecords(t *testing.T) {
	tests := []struct{ name, line, want string }{
		{"unknown field", `{"group":"1@g.us","sender":"2@lid","id":"A","txt":"typo"}`, "line 1"},
		{"unknown kind", `{"kind":"nope"}`, `unknown kind "nope"`},
		{"message without id", `{"group":"1@g.us","sender":"2@lid"}`, "needs group, sender and id"},
		{"group without group JID", `{"kind":"group","group":"2@lid"}`, "needs a group JID"},
		{"bad label", `{"group":"1@g.us","sender":"2@lid","id":"A","expect":"maybe"}`, "want spam or ham"},
		{"bad duration", `{"kind":"advance","duration":"-1m"}`, "positive duration"},
		{"not json", `win money`, "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(tt.line)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestRunStopsAtFirstError(t *testing.T) {
	recs, err := Load(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.fail = "B"
	rep, err := Run(f, recs)
	if err == nil || !strings.Contains(err.Error(), "record 6") || len(rep.Results) != 1 {
		t.Fatalf("Run = %d results, %v; want the error at record 6 and the one result before it", len(rep.Results), err)
	}
}

func TestDiff(t *testing.T) {
	recs, err := Load(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	before, err := Run(newFake(), recs)
	if err != nil {
		t.Fatal(err)
	}
	// The second rule version no longer knows the template.
	after, err := Run(newFake(), append(recs[:1:1], recs[2:]...))
	if err != nil {
		t.Fatal(err)
	}
	changes := Diff(before, after)
	if len(changes) != 2 || changes[0].Message != "A" || changes[1].Message != "B" {
		t.Fatalf("Diff = %+v, want A and B changed", changes)
	}
	if !strings.Contains(changes[0].After, "band=clean") {
		t.Fatalf("change A after = %q", changes[0].After)
	}
	if d := Diff(before, before); len(d) != 0 {
		t.Fatalf("Diff of a report with itself = %+v, want none", d)
	}
}

func FuzzLoad(f *testing.F) {
	for _, l := range strings.Split(fixture, "\n") {
		f.Add(l)
	}
	f.Fuzz(func(t *testing.T, s string) {
		recs, err := Load(strings.NewReader(s))
		if err != nil {
			return
		}
		for _, r := range recs {
			if r.validate() != nil {
				t.Fatalf("Load returned an invalid record %+v", r)
			}
		}
	})
}

func TestSimReplaysV01Fixture(t *testing.T) {
	f, err := os.Open("testdata/v01.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	sim, err := NewSim(SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(sim, recs)
	if err != nil {
		t.Fatal(err)
	}
	want := map[wa.MessageID]string{
		"S1": "route=auto_ban actions=delete+ban",                                       // known spam, new member
		"S2": "route=ask actions=delete+review",                                         // near-copy, established member
		"S3": "route=ask actions=delete+review",                                         // homoglyph copy, established member
		"S4": "mode=shadow band=certain source=known route=auto_ban actions=delete+ban", // would-be, shadow
		"H1": "band=clean source=none route=none actions=-",
		"H2": "band=clean source=none route=none actions=-",
		"H3": "band=clean source=none route=none actions=-",
		"H4": "band=clean source=none route=none actions=-",
		"S5": "band=certain source=known route=auto_ban actions=delete+ban", // blacklisted number in a vCard
		"H5": "band=clean source=none route=none actions=-",                 // an ordinary shared contact
	}
	for _, r := range rep.Results {
		if w := want[r.Verdict.Message]; !strings.Contains(r.Line(), w) {
			t.Errorf("%s: %q, want it to contain %q", r.Verdict.Message, r.Line(), w)
		}
	}
	s := rep.Summarize()
	if s.Spam != 5 || s.SpamDeleted != 5 || s.Ham != 5 || s.HamDeleted+s.HamBanned+s.HamToAdmins != 0 {
		t.Fatalf("summary %+v: want all spam deleted (shadow counts what it would do) and no ham touched", s)
	}
	if sim.Reviews() != 2 {
		t.Fatalf("reviews %d, want 2 (the established members' bans go to admins; shadow opens none)", sim.Reviews())
	}

	// Replaying the same fixture with the corpus disabled shows what the rules change does.
	noCorpus := append([]Record(nil), recs...)
	noCorpus = slices.DeleteFunc(noCorpus, func(r Record) bool { return r.Kind == KindConfirmSpam })
	sim2, err := NewSim(SimOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep2, err := Run(sim2, noCorpus)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(rep, rep2); len(d) != 4 {
		t.Fatalf("diff without the corpus = %+v, want the 4 template messages (the blacklisted vCard is still caught)", d)
	}
}
