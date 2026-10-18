package corpusimport

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestParseJSONL(t *testing.T) {
	in := strings.Join([]string{
		`{"text":"Cheap followers today, write now","category":"Promo","cluster_id":7}`,
		``,
		`{"text":"","media_sha256":"` + strings.ToUpper(sha) + `","label":"SPAM"}`,
		`{"text":"see you at dinner","label":"ham","urls":["example.org/menu","example.org/menu"]}`,
		`{"text":"bad label","label":"maybe"}`,
		`not json`,
		`{"text":"bad kind","kind":"hologram"}`,
		`{"text":""}`,
		`{"label":"spam"}`,
		`{"text":["a","list"]}`,
		`{"text":"x","media_sha256":["123"]}`,
		`{"text":"x","urls":["ftp://files.example"]}`,
		`{"text":"x","phash":"zz"}`,
		`{"text":"x","language":"Arabic!"}`,
		"{\"text\":\"bad \xff utf8\"}",
		`{"text":"pHash and extra fields","phash":"0xFF","id":12345,"meta":{"a":[1,true]},"seen":false,"language":"AR"}`,
	}, "\n")
	recs, rej, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []int{1, 3, 4, 16}
	var lines []int
	for _, r := range recs {
		lines = append(lines, r.Line)
	}
	if !slices.Equal(lines, wantLines) {
		t.Fatalf("valid records on lines %v, want %v (line numbers count blank lines)", lines, wantLines)
	}
	if recs[0].Label != LabelSpam || recs[0].ClusterID != "7" || recs[0].Category != "promo" {
		t.Errorf("record 1 = %+v, want label spam by default, a numeric cluster_id read as text and a lower-cased category", recs[0])
	}
	if !slices.Equal(recs[1].MediaSHA256, []string{sha}) || recs[1].Label != LabelSpam {
		t.Errorf("record 3 = %+v, want one lower-cased media hash from a plain string", recs[1])
	}
	if recs[2].Label != LabelHam || len(recs[2].URLs) != 1 {
		t.Errorf("record 4 = %+v, want ham with the repeated URL listed once", recs[2])
	}
	if recs[3].PHash != "00000000000000ff" || recs[3].Language != "ar" {
		t.Errorf("record 16 = %+v, want the phash padded to 16 hex digits and the language lower-cased", recs[3])
	}
	wantRej := []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	var got []int
	for _, r := range rej {
		got = append(got, r.Line)
		if r.Reason == "" {
			t.Errorf("line %d rejected without a reason", r.Line)
		}
	}
	if !slices.Equal(got, wantRej) {
		t.Errorf("rejected lines %v, want %v; reasons: %v", got, wantRej, rej)
	}
}

func TestParseCSV(t *testing.T) {
	in := "\xEF\xBB\xBFText,Label,urls,media_sha256,cluster_id,notes\n" +
		"\"multi\nline offer\",spam,https://a.example/x | b.example,,c1,whatever\n" +
		"hello there,ham,,,,\n" +
		"too,many,fields,,,,,\n" +
		",spam,," + sha + ",c2,\n" +
		"\n" +
		",spam,,,,\n"
	recs, rej, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records %+v, want 3", len(recs), recs)
	}
	if recs[0].Line != 2 || !slices.Equal(recs[0].URLs, []string{"https://a.example/x", "b.example"}) || recs[0].ClusterID != "c1" {
		t.Errorf("row 1 = %+v, want line 2, URLs split on | and spaces, cluster c1", recs[0])
	}
	if recs[1].Line != 4 || recs[1].Label != LabelHam {
		t.Errorf("row 2 = %+v, want ham on line 4 (the quoted cell spans two lines)", recs[1])
	}
	if recs[2].Line != 6 || len(recs[2].MediaSHA256) != 1 {
		t.Errorf("row 4 = %+v, want the media-only record on line 6", recs[2])
	}
	want := []Rejection{{5, ""}, {8, ""}}
	if len(rej) != len(want) || rej[0].Line != 5 || rej[1].Line != 8 {
		t.Errorf("rejected %v, want lines 5 (field count) and 8 (no text and no media)", rej)
	}
}

func TestParseCSVHeader(t *testing.T) {
	for name, in := range map[string]string{
		"no text column":  "label,category\nspam,promo\n",
		"duplicate field": "text,text\na,b\n",
		"empty":           "",
	} {
		if _, _, err := Parse(strings.NewReader(in)); !errors.Is(err, ErrInput) {
			t.Errorf("%s: err = %v, want ErrInput (a CSV needs a header with a text column)", name, err)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	for in, want := range map[string]string{
		"\xEF\xBB\xBF  {\"text\":1}": FormatJSONL,
		"\n\n{":                      FormatJSONL,
		"text,label\n":               FormatCSV,
		"":                           FormatCSV,
	} {
		if got := DetectFormat([]byte(in)); got != want {
			t.Errorf("DetectFormat(%q) = %s, want %s", in, got, want)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`{"text":"hi","urls":["a.example"],"cluster_id":3}` + "\n" + `{"text":""}`)
	f.Add("text,label\n\"a\nb\",ham\n,\n")
	f.Fuzz(func(t *testing.T, in string) {
		recs, rej, err := Parse(strings.NewReader(in))
		if err != nil {
			return
		}
		for _, r := range recs {
			if r.Label != LabelSpam && r.Label != LabelHam {
				t.Fatalf("record with label %q, want spam or ham", r.Label)
			}
			if r.Line < 1 || (strings.TrimSpace(r.Text) == "" && len(r.MediaSHA256) == 0 && r.PHash == "") {
				t.Fatalf("invalid record accepted: %+v", r)
			}
		}
		for _, r := range rej {
			if r.Line < 1 || r.Reason == "" {
				t.Fatalf("rejection without a line or reason: %+v", r)
			}
		}
	})
}
