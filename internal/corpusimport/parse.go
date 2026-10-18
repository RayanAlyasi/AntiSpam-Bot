// Package corpusimport imports a labeled spam dataset into the confirmed-spam corpus (DESIGN.md
// §7.8): it parses and validates JSONL or CSV records, normalizes and fingerprints them with the
// bot's own code, dedupes them, splits a hold-out slice by cluster and reports what it did.
// Storage and the recall replay live in pgimport; this package does no I/O.
//
// Privacy (§7.5, §17): stored text is redacted like any corpus entry and numbers are kept only as
// keyed hashes. Ham records are only ever used in memory.
package corpusimport

import (
	"bytes"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize/urls"
)

// Labels.
const (
	LabelSpam = "spam"
	LabelHam  = "ham"
)

// Formats an input file can have.
const (
	FormatJSONL = "jsonl"
	FormatCSV   = "csv"
)

// Limits on one record and on the whole input.
const (
	MaxInput     = 256 << 20 // bytes
	MaxTextBytes = 65536     // WhatsApp's own limit on a message is about this long
	MaxURLs      = 50
	MaxURLBytes  = 2048
	MaxMedia     = 20
	maxShort     = 64  // category
	maxClusterID = 200 // cluster_id
	maxSource    = 500 // source
)

// Kinds a record may name (§7.8). Media kinds are matched by their hashes; the others by text
// and links.
var Kinds = []string{
	"text", "link", "image", "video", "sticker", "document", "audio", "voice", "contact",
	"channel_share", "invite", "poll", "location", "event",
}

// bom is the UTF-8 byte-order mark some editors put at the start of a file.
var bom = []byte{0xEF, 0xBB, 0xBF}

// ErrInput means the input as a whole can't be read (too large, a CSV without a usable header).
// Problems with single records are Rejections instead.
var ErrInput = errors.New("unreadable input")

// Record is one validated record. Lists are deduplicated, hashes are lower-case hex and Label is
// always LabelSpam or LabelHam.
type Record struct {
	Line        int // 1-based line of the input the record starts on
	Text        string
	Label       string
	Kind        string // "" when the record didn't name one; the importer infers it
	URLs        []string
	MediaSHA256 []string
	PHash       string // 16 hex digits, or ""
	Language    string
	Category    string
	Source      string
	ClusterID   string
}

// Rejection is a record that failed validation.
type Rejection struct {
	Line   int
	Reason string
}

func (r Rejection) String() string { return fmt.Sprintf("line %d: %s", r.Line, r.Reason) }

// DetectFormat tells JSONL from CSV by the first character that isn't a byte-order mark or
// white space: a JSON object starts with '{'.
func DetectFormat(data []byte) string {
	data = bytes.TrimLeft(bytes.TrimPrefix(data, bom), " \t\r\n")
	if len(data) > 0 && data[0] == '{' {
		return FormatJSONL
	}
	return FormatCSV
}

// Parse reads every record of r, JSONL or CSV with a header (DetectFormat). Valid records come
// back in input order; every other non-blank line or row is a Rejection with its line number.
// The error is for an input that can't be read at all.
func Parse(r io.Reader) ([]Record, []Rejection, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxInput+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read input: %w", err)
	}
	if len(data) > MaxInput {
		return nil, nil, fmt.Errorf("%w: larger than %d MiB", ErrInput, MaxInput>>20)
	}
	data = bytes.TrimPrefix(data, bom)
	if DetectFormat(data) == FormatJSONL {
		recs, rej := parseJSONL(data)
		return recs, rej, nil
	}
	return parseCSV(data)
}

func parseJSONL(data []byte) ([]Record, []Rejection) {
	var recs []Record
	var rej []Rejection
	for i, line := range bytes.Split(data, []byte("\n")) {
		n := i + 1
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !utf8.Valid(line) {
			rej = append(rej, Rejection{n, "not valid UTF-8"})
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(line, &obj); err != nil {
			rej = append(rej, Rejection{n, "not a JSON object: " + jsonReason(err)})
			continue
		}
		fields := map[string]value{}
		bad := ""
		for k, raw := range obj {
			if !knownFields[strings.ToLower(strings.TrimSpace(k))] {
				continue // a dataset's own field (an ID, a date), whatever its type
			}
			v, err := jsonValue(raw)
			if err != nil {
				bad = fmt.Sprintf("%s: %v", k, err)
				break
			}
			fields[strings.ToLower(strings.TrimSpace(k))] = v
		}
		if bad != "" {
			rej = append(rej, Rejection{n, bad})
			continue
		}
		rec, reason := build(fields)
		if reason != "" {
			rej = append(rej, Rejection{n, reason})
			continue
		}
		rec.Line = n
		recs = append(recs, rec)
	}
	return recs, rej
}

func jsonReason(err error) string {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return se.Error()
	}
	return "want one object per line"
}

// value is a field as read from either format: absent, one string, or a list of strings.
type value struct {
	list   []string
	isList bool
	number bool // a JSON number, read as its literal
}

func (v value) one() (string, bool) {
	if v.isList {
		return "", false
	}
	if len(v.list) == 0 {
		return "", true
	}
	return v.list[0], true
}

// jsonValue reads a JSON field: null, a string, a number (cluster IDs are often numbers), or an
// array of strings.
func jsonValue(raw json.RawMessage) (value, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case bytes.Equal(raw, []byte("null")):
		return value{}, nil
	case len(raw) > 0 && raw[0] == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return value{}, errors.New("not a string")
		}
		return value{list: []string{s}}, nil
	case len(raw) > 0 && raw[0] == '[':
		var l []string
		if err := json.Unmarshal(raw, &l); err != nil {
			return value{}, errors.New("want a list of strings")
		}
		return value{list: l, isList: true}, nil
	default:
		var n json.Number
		if err := json.Unmarshal(raw, &n); err != nil {
			return value{}, errors.New("want a string, a number or a list of strings")
		}
		return value{list: []string{n.String()}, number: true}, nil
	}
}

// csvListSplit splits a CSV cell that holds a list: values are separated by white space or '|'.
func csvListSplit(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '|' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

var csvListFields = map[string]bool{"urls": true, "media_sha256": true}

// knownFields are the §7.8 fields; any other field or column is ignored.
var knownFields = map[string]bool{
	"text": true, "label": true, "kind": true, "urls": true, "media_sha256": true, "phash": true,
	"language": true, "category": true, "source": true, "cluster_id": true,
}

func parseCSV(data []byte) ([]Record, []Rejection, error) {
	cr := csv.NewReader(bytes.NewReader(data))
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: CSV header: %w", ErrInput, err)
	}
	cols := make([]string, len(header))
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && slices.Contains(cols[:i], h) {
			return nil, nil, fmt.Errorf("%w: CSV header names %q twice", ErrInput, h)
		}
		cols[i] = h
	}
	if !slices.Contains(cols, "text") && !slices.Contains(cols, "media_sha256") {
		return nil, nil, fmt.Errorf("%w: the CSV header has no text column (want a header row such as text,label,category)", ErrInput)
	}
	var recs []Record
	var rej []Rejection
	for {
		row, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				rej = append(rej, Rejection{max(pe.StartLine, 1), "bad CSV: " + pe.Err.Error()})
				continue
			}
			return recs, rej, fmt.Errorf("read CSV: %w", err)
		}
		line, _ := cr.FieldPos(0)
		if len(row) == 1 && strings.TrimSpace(row[0]) == "" {
			continue
		}
		if len(row) != len(cols) {
			rej = append(rej, Rejection{line, fmt.Sprintf("has %d fields, the header has %d", len(row), len(cols))})
			continue
		}
		fields := map[string]value{}
		valid := true
		for i, c := range cols {
			if c == "" {
				continue
			}
			if !utf8.ValidString(row[i]) {
				valid = false
				break
			}
			switch {
			case strings.TrimSpace(row[i]) == "":
			case csvListFields[c]:
				fields[c] = value{list: csvListSplit(row[i]), isList: true}
			default:
				fields[c] = value{list: []string{row[i]}}
			}
		}
		if !valid {
			rej = append(rej, Rejection{line, "not valid UTF-8"})
			continue
		}
		rec, reason := build(fields)
		if reason != "" {
			rej = append(rej, Rejection{line, reason})
			continue
		}
		rec.Line = line
		recs = append(recs, rec)
	}
	return recs, rej, nil
}

var (
	hexSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hexPHash  = regexp.MustCompile(`^[0-9a-f]{1,16}$`)
	langTag   = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)
)

// build validates the fields of one record. Unknown fields are ignored, so a dataset can carry
// its own columns (an ID, a date). It returns the reason when the record is rejected.
func build(f map[string]value) (Record, string) {
	var r Record
	str := func(name string) (string, string) {
		v, ok := f[name]
		if !ok {
			return "", ""
		}
		s, ok := v.one()
		if !ok {
			return "", name + ": want a single value, not a list"
		}
		// Only an ID may be a number: text written as a JSON number has already lost digits
		// (a phone number), a pHash is hex, and the other fields are names.
		if v.number && name != "cluster_id" {
			return "", name + ": want a string, not a number"
		}
		return s, ""
	}
	list := func(name string) []string {
		v := f[name]
		var out []string
		for _, s := range v.list {
			if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		return out
	}
	var bad string
	if r.Text, bad = str("text"); bad != "" {
		return r, bad
	}
	if len(r.Text) > MaxTextBytes {
		return r, fmt.Sprintf("text: longer than %d bytes", MaxTextBytes)
	}

	label, bad := str("label")
	if bad != "" {
		return r, bad
	}
	switch label = strings.ToLower(strings.TrimSpace(label)); label {
	case "", LabelSpam:
		r.Label = LabelSpam
	case LabelHam:
		r.Label = LabelHam
	default:
		return r, fmt.Sprintf("label: %q is not spam or ham", label)
	}

	kind, bad := str("kind")
	if bad != "" {
		return r, bad
	}
	if kind = strings.ToLower(strings.TrimSpace(kind)); kind != "" && !slices.Contains(Kinds, kind) {
		return r, fmt.Sprintf("kind: %q is not one of %s", kind, strings.Join(Kinds, ", "))
	}
	r.Kind = kind

	r.URLs = list("urls")
	if len(r.URLs) > MaxURLs {
		return r, fmt.Sprintf("urls: more than %d", MaxURLs)
	}
	for _, u := range r.URLs {
		if len(u) > MaxURLBytes {
			return r, fmt.Sprintf("urls: a URL longer than %d bytes", MaxURLBytes)
		}
		if _, err := urls.Canonicalize(withScheme(u)); err != nil {
			return r, fmt.Sprintf("urls: %q is not an http(s) link", u)
		}
	}

	for _, m := range list("media_sha256") {
		m = strings.ToLower(m)
		if !hexSHA256.MatchString(m) {
			return r, fmt.Sprintf("media_sha256: %q is not 64 hex digits", m)
		}
		if !slices.Contains(r.MediaSHA256, m) {
			r.MediaSHA256 = append(r.MediaSHA256, m)
		}
	}
	if len(r.MediaSHA256) > MaxMedia {
		return r, fmt.Sprintf("media_sha256: more than %d", MaxMedia)
	}

	ph, bad := str("phash")
	if bad != "" {
		return r, bad
	}
	if ph = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ph)), "0x"); ph != "" {
		if !hexPHash.MatchString(ph) {
			return r, fmt.Sprintf("phash: %q is not a 64-bit hex hash", ph)
		}
		n, _ := strconv.ParseUint(ph, 16, 64)
		r.PHash = fmt.Sprintf("%016x", n)
	}

	if strings.TrimSpace(r.Text) == "" && len(r.MediaSHA256) == 0 && r.PHash == "" {
		if _, ok := f["text"]; !ok {
			return r, "text is missing (it may be empty only when media_sha256 or phash is given)"
		}
		return r, "text is empty and there is no media_sha256 or phash"
	}

	lang, bad := str("language")
	if bad != "" {
		return r, bad
	}
	if lang = strings.ToLower(strings.TrimSpace(lang)); lang != "" && !langTag.MatchString(lang) {
		return r, fmt.Sprintf("language: %q is not a language code such as ar or en", lang)
	}
	r.Language = lang

	cat, bad := str("category")
	if bad != "" {
		return r, bad
	}
	if cat = strings.ToLower(strings.TrimSpace(cat)); utf8.RuneCountInString(cat) > maxShort {
		return r, fmt.Sprintf("category: longer than %d characters", maxShort)
	}
	r.Category = cat

	if r.Source, bad = str("source"); bad != "" {
		return r, bad
	}
	if r.Source = strings.TrimSpace(r.Source); utf8.RuneCountInString(r.Source) > maxSource {
		return r, fmt.Sprintf("source: longer than %d characters", maxSource)
	}

	if r.ClusterID, bad = str("cluster_id"); bad != "" {
		return r, bad
	}
	if r.ClusterID = strings.TrimSpace(r.ClusterID); utf8.RuneCountInString(r.ClusterID) > maxClusterID {
		return r, fmt.Sprintf("cluster_id: longer than %d characters", maxClusterID)
	}
	return r, ""
}

// withScheme adds https:// to a link written without a scheme ("example.com/x"), as people
// write them in messages.
func withScheme(u string) string {
	if strings.Contains(u, "://") {
		return u
	}
	return "https://" + u
}

// decodeHex decodes hex that build already validated.
func decodeHex(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}
