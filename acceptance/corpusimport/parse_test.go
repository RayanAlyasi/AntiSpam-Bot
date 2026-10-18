package corpusimport_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

var (
	hexA = strings.Repeat("ab", 32)
	hexB = strings.Repeat("0f", 32)
)

func parse(t *testing.T, in string) ([]corpusimport.Record, []corpusimport.Rejection) {
	t.Helper()
	recs, rej, err := corpusimport.Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Parse: got error %v, want nil (bad lines are reported as rejections, not a failed import, DESIGN §7.8)", err)
	}
	return recs, rej
}

func recLines(recs []corpusimport.Record) []int {
	var out []int
	for _, r := range recs {
		out = append(out, r.Line)
	}
	return out
}

func rejLines(t *testing.T, rej []corpusimport.Rejection) []int {
	t.Helper()
	var out []int
	for _, r := range rej {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("rejection on line %d has an empty reason, want one that says what is wrong (rejected lines are reported clearly, DESIGN §7.8, §18)", r.Line)
		}
		out = append(out, r.Line)
	}
	return out
}

func sameRecord(t *testing.T, got, want corpusimport.Record) {
	t.Helper()
	if got.Line != want.Line || got.Text != want.Text || got.Label != want.Label || got.Kind != want.Kind ||
		!slices.Equal(got.URLs, want.URLs) || !slices.Equal(got.MediaSHA256, want.MediaSHA256) ||
		got.PHash != want.PHash || got.Language != want.Language || got.Category != want.Category ||
		got.Source != want.Source || got.ClusterID != want.ClusterID {
		t.Errorf("record on line %d:\n got %+v\nwant %+v\n(every documented field is read as given, DESIGN §7.8)", want.Line, got, want)
	}
}

func TestParseJSONL(t *testing.T) {
	in := `{"text":"Join our crypto channel today","label":"spam","kind":"link","urls":["https://x.example/a"],"media_sha256":"` + hexA + `","phash":"f0e1d2c3b4a59687","language":"en","category":"crypto","source":"synthetic","cluster_id":"c1"}
{"text":"no label here"}

{"text":"good morning all","label":"ham"}
{"text":"","media_sha256":["` + hexA + `","` + hexB + `"]}
`
	recs, rej := parse(t, in)
	if len(rej) != 0 {
		t.Errorf("got rejections %+v, want none (all four records are valid; a blank line is skipped, not rejected)", rej)
	}
	want := []corpusimport.Record{
		{
			Line: 1, Text: "Join our crypto channel today", Label: "spam", Kind: "link", URLs: []string{"https://x.example/a"},
			MediaSHA256: []string{hexA}, PHash: "f0e1d2c3b4a59687", Language: "en", Category: "crypto", Source: "synthetic", ClusterID: "c1",
		},
		{Line: 2, Text: "no label here", Label: "spam"},
		{Line: 4, Text: "good morning all", Label: "ham"},
		{Line: 5, Text: "", Label: "spam", MediaSHA256: []string{hexA, hexB}},
	}
	if len(recs) != len(want) {
		t.Fatalf("got records on lines %v, want lines [1 2 4 5] (one record per non-blank line, line numbers count blank lines)", recLines(recs))
	}
	for i := range want {
		sameRecord(t, recs[i], want[i])
	}
	if recs[1].Label != "spam" {
		t.Errorf("got label %q for a record without one, want spam (spam-only files import without warnings, DESIGN §7.8, §18)", recs[1].Label)
	}
}

func TestParseJSONLRejectsWithLineNumbers(t *testing.T) {
	in := `{"text":"valid one"}
not json at all
{"text":""}
{"text":"   	 "}
{"label":"spam"}
{"text":"x","label":"maybe"}
{"text":"y","media_sha256":"zz"}
{"text":5}
{"text":"valid two","label":"ham"}
`
	recs, rej := parse(t, in)
	if got := recLines(recs); !slices.Equal(got, []int{1, 9}) {
		t.Errorf("got records on lines %v, want [1 9] (only valid records are imported, DESIGN §7.8)", got)
	}
	if got := rejLines(t, rej); !slices.Equal(got, []int{2, 3, 4, 5, 6, 7, 8}) {
		t.Errorf("got rejections on lines %v, want [2 3 4 5 6 7 8] (malformed JSON, empty or blank text without media, missing text, unknown label, bad sha256 and non-string text are each reported with their line, DESIGN §7.8)", got)
	}
}

func TestParseLineNumbersCountBlankLines(t *testing.T) {
	recs, rej := parse(t, "\n\n{\"text\":\"spam offer here\"}\n\n{\"text\":\"\"}\n")
	if got := recLines(recs); !slices.Equal(got, []int{3}) {
		t.Errorf("got records on lines %v, want [3] (line numbers are the file's own 1-based lines, so the operator can find them)", got)
	}
	if got := rejLines(t, rej); !slices.Equal(got, []int{5}) {
		t.Errorf("got rejections on lines %v, want [5] (line numbers are the file's own 1-based lines)", got)
	}
}

func TestParseCSV(t *testing.T) {
	in := "text,label,kind,urls,media_sha256,phash,language,category,source,cluster_id\n" +
		"\"Win big, today\",,link,https://a.example/x https://b.example/y,,,en,promo,synthetic export,c1\n" +
		"good morning all,ham,,,,,en,,,\n" +
		",,image,," + hexA + ",f0e1d2c3b4a59687,,,,\n" +
		"\"He said \"\"join now\"\"\",spam,,,,,,,,c2\n"
	recs, rej := parse(t, in)
	if len(rej) != 0 {
		t.Errorf("got rejections %+v, want none (all four rows are valid)", rej)
	}
	want := []corpusimport.Record{
		{
			Line: 2, Text: "Win big, today", Label: "spam", Kind: "link", URLs: []string{"https://a.example/x", "https://b.example/y"},
			Language: "en", Category: "promo", Source: "synthetic export", ClusterID: "c1",
		},
		{Line: 3, Text: "good morning all", Label: "ham", Language: "en"},
		{Line: 4, Text: "", Label: "spam", Kind: "image", MediaSHA256: []string{hexA}, PHash: "f0e1d2c3b4a59687"},
		{Line: 5, Text: `He said "join now"`, Label: "spam", ClusterID: "c2"},
	}
	if len(recs) != len(want) {
		t.Fatalf("got records on lines %v, want [2 3 4 5] (the header is line 1; one record per row)", recLines(recs))
	}
	for i := range want {
		sameRecord(t, recs[i], want[i])
	}
}

func TestParseCSVHeaderSubsetAndBOM(t *testing.T) {
	recs, rej := parse(t, "\ufeffcategory,text\r\njobs,Work from home and earn daily\r\n")
	if len(rej) != 0 || len(recs) != 1 {
		t.Fatalf("got %d records and rejections %+v, want 1 record (a spreadsheet export with a BOM, CRLF and only some columns, in any order, is valid; only text is required, DESIGN §7.8)", len(recs), rej)
	}
	sameRecord(t, recs[0], corpusimport.Record{Line: 2, Text: "Work from home and earn daily", Label: "spam", Category: "jobs"})
}

func TestParseCSVRejectsWithLineNumbers(t *testing.T) {
	in := "text,label,media_sha256\n" +
		"ok text here,,\n" +
		",,\n" +
		"x,junk,\n" +
		"a,b,c,d\n" +
		"y,,nothex\n" +
		"fine text,ham,\n"
	recs, rej := parse(t, in)
	if got := recLines(recs); !slices.Equal(got, []int{2, 7}) {
		t.Errorf("got records on lines %v, want [2 7] (only valid rows are imported, DESIGN §7.8)", got)
	}
	if got := rejLines(t, rej); !slices.Equal(got, []int{3, 4, 5, 6}) {
		t.Errorf("got rejections on lines %v, want [3 4 5 6] (empty text without media, unknown label, wrong field count and bad sha256 are each reported with their line, DESIGN §7.8)", got)
	}
}

func TestParseKeepsUnicodeText(t *testing.T) {
	text := "عرض خاص 🔥 انضم\u200b الآن ｗｗｗ"
	recs, rej := parse(t, `{"text":"`+text+`","language":"ar"}`+"\n")
	if len(rej) != 0 || len(recs) != 1 {
		t.Fatalf("got %d records and rejections %+v, want 1 record", len(recs), rej)
	}
	if recs[0].Text != text {
		t.Errorf("got text %q, want %q unchanged (Parse keeps the text as given; the import normalizes it with the bot's own code, DESIGN §7.2, §7.8)", recs[0].Text, text)
	}
}

func TestParseScope(t *testing.T) {
	ok := map[string]wa.JID{
		"instance":                      "",
		"group:120363000000000001@g.us": "120363000000000001@g.us",
	}
	for in, want := range ok {
		s, err := corpusimport.ParseScope(in)
		if err != nil || s.Group != want {
			t.Errorf("ParseScope(%q): got %+v, %v, want group %q, nil (scope is instance or group:<jid>, DESIGN §7.8)", in, s, err, want)
		}
	}
	for _, in := range []string{"", "global", "group:", "group:123456@lid", "group:966501234567@s.whatsapp.net", "group"} {
		if s, err := corpusimport.ParseScope(in); err == nil {
			t.Errorf("ParseScope(%q): got %+v, nil, want an error (only instance or a group JID is a valid scope, DESIGN §7.8)", in, s)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`{"text":"a b c"}` + "\n" + `{"text":"","media_sha256":["` + hexA + `"]}`)
	f.Add("text,label\nhello,ham\n,\n\"unterminated,spam\n")
	f.Add("\ufeffcategory,text\r\njobs,x\r\n")
	f.Add("\n\n{\"text\":5}\n[1,2]\n")
	f.Fuzz(func(t *testing.T, in string) {
		recs, rej, err := corpusimport.Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		maxLine := strings.Count(in, "\n") + 1
		seen := map[int]bool{}
		prev := 0
		for _, r := range recs {
			if r.Line < 1 || r.Line > maxLine || r.Line <= prev {
				t.Fatalf("got record line %d after %d (input has %d lines), want increasing lines within the input", r.Line, prev, maxLine)
			}
			prev = r.Line
			seen[r.Line] = true
			if r.Label != "spam" && r.Label != "ham" {
				t.Fatalf("got label %q on line %d, want spam or ham (other labels are rejected, DESIGN §7.8)", r.Label, r.Line)
			}
			if strings.TrimSpace(r.Text) == "" && len(r.MediaSHA256) == 0 && r.PHash == "" {
				t.Fatalf("got a record on line %d with no text and no media, want it rejected (text may be empty only if media fields are given, DESIGN §7.8)", r.Line)
			}
		}
		for _, r := range rej {
			if r.Line < 1 || r.Line > maxLine {
				t.Fatalf("got rejection line %d, want 1..%d", r.Line, maxLine)
			}
			if seen[r.Line] {
				t.Fatalf("line %d is both a record and a rejection, want one or the other", r.Line)
			}
		}
	})
}
