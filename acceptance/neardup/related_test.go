package neardup_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/neardup"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/normalize"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/pipeline"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/wa"
)

// Synthetic "list of services + contact" ads. adLong has about 75 distinct match terms, so a
// short ad wholly inside it has a low Jaccard but full containment.
const (
	adLong = "Hello everyone, we provide complete student services: assignments, homework, research papers, " +
		"graduation projects, presentations, posters, summaries, translation, proofreading, editing, essays, theses, " +
		"dissertations, statistics, accounting, programming, engineering drawings, mathematics, physics, chemistry, " +
		"biology, nursing, pharmacy, medicine, law, management, marketing, economics, quizzes, exams, online lectures, " +
		"blackboard tasks, portfolio design, resume writing, cover letters, sick leave notes, medical excuses, hospital " +
		"reports, attendance certificates and internship letters. Experienced tutors, fast delivery, guaranteed grades, " +
		"affordable prices, confidential handling, discounts for groups, payment after completion. " +
		"Message us on WhatsApp wa.me/966551234567"
	// 12 terms, all in adLong (Jaccard ≈ 0.16, containment 1).
	adShort = "Translation, proofreading, essays, presentations, statistics and accounting assignments. " +
		"Fast delivery, affordable prices, WhatsApp wa.me/966559876543"
	// 5 terms, all in adLong: too few for containment, Jaccard ≈ 0.07.
	adFive = "Essays, statistics, accounting, posters, quizzes."

	adLongAr = "السلام عليكم ورحمة الله وبركاته، نقدم خدمات طلابية متكاملة: حل الواجبات والبحوث والتقارير والمشاريع " +
		"والعروض التقديمية والتلخيص والترجمة والتدقيق اللغوي وكتابة السيرة الذاتية والرسائل الجامعية وحل الاختبارات " +
		"والكويزات ومتابعة البلاك بورد وتصميم البوسترات والانفوجرافيك والمحاسبة والبرمجة والرياضيات والفيزياء والكيمياء " +
		"والتمريض والصيدلة والقانون والتسويق والاقتصاد، وأيضا الأعذار الطبية والإجازات المرضية وتقارير المستشفى وشهادات " +
		"الحضور وخطابات التدريب. أسعار مناسبة وتسليم سريع وضمان الدرجة وسرية تامة وخصومات للمجموعات والدفع بعد الإنجاز. " +
		"تواصل واتساب wa.me/966551234567"
	// The same services without the article, or with a different clitic: related only once
	// clitics are stripped.
	adShortAr = "حل واجبات وبحوث وتقارير ومشاريع وترجمة وتلخيص وبرمجة ومحاسبة، تسليم سريع، واتساب 0559876543"

	// Ordinary chat pairs that share only a group link, the phone placeholder, greetings and
	// function words (raw word overlap is high; match-term overlap is near zero).
	noiseA = "Hello everyone, please join the group https://chat.whatsapp.com/AbCdEf1234567890XyZab and call [phone] " +
		"for the football match on Saturday evening near the stadium gate, bring water, boots, shin guards, towels, " +
		"snacks, jerseys, and your friends, thanks"
	noiseB = "Hi, hello, the link is https://chat.whatsapp.com/AbCdEf1234567890XyZab and [phone] for the physics " +
		"revision session tomorrow morning in library room, bring calculators, notebooks, pencils, erasers, rulers, " +
		"past papers, and coffee, thanks"
	noiseArA = "السلام عليكم ورحمة الله وبركاته يا شباب، هذا رابط القروب https://chat.whatsapp.com/AbCdEf1234567890XyZab " +
		"و [phone] في مباراة كرة القدم يوم السبت مساء عند بوابة الملعب، أحضروا الماء والأحذية والمناشف"
	noiseArB = "السلام عليكم ورحمة الله وبركاته، هذا الرابط https://chat.whatsapp.com/AbCdEf1234567890XyZab " +
		"و [phone] من أجل مراجعة مادة الفيزياء غدا صباحا في قاعة المكتبة، أحضروا الآلات الحاسبة والدفاتر والأقلام"

	// Five terms each, two shared: Jaccard 0.25 on match terms, containment path off (< 6 terms).
	jacA = "crypto mining rigs rented weekly"
	jacB = "crypto mining payouts guaranteed instantly"
)

func terms(raw string) []string { return neardup.Terms(normalize.Text(raw).Norm) }

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

func TestTermsDropNoise(t *testing.T) {
	tests := []struct {
		name, text string
		want       int
		why        string
	}{
		{"english function words and greetings", "Hi hello hey, thanks please. This is it, and you are with that: we and i, a or an, to the, of, in, on, for.", 0, "function words and greetings carry no campaign wording"},
		{"arabic function words and greetings", "السلام عليكم ورحمة الله وبركاته، يا ما لا في من على الى عن مع هذا هذه، كل عام وانتم بخير", 0, "Arabic greetings and function words carry no campaign wording"},
		{"urls and domain links", "https://chat.whatsapp.com/AbCdEf1234567890XyZab www.example.com/path uni.edu.sa wa.me/966551234567 t.me/somechannel", 0, "URL pieces (https, chat, whatsapp, com) are not words of the message"},
		{"short digit runs", "7 12 99", 0, "digit runs under 3 digits are prices, counts and dates, not wording"},
		{"long digit runs kept", "12 7 345 2026", 2, "digit runs of 3 or more digits stay terms"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := terms(tt.text); len(got) != tt.want {
				t.Errorf("got terms %q, want %d terms (%s; contract holdout-recall, DESIGN §7.5)", got, tt.want, tt.why)
			}
		})
	}
}

func TestTermsDropPlaceholders(t *testing.T) {
	got := neardup.Terms("[phone] [phone:0a1b2c3d4e5f] [email] [mention] [name]")
	for _, w := range got {
		if strings.Contains(w, "phone") || strings.Contains(w, "email") || strings.Contains(w, "mention") || strings.Contains(w, "name") {
			t.Errorf("got term %q from redaction placeholders, want none (placeholders are the redactor's, not the message's wording)", w)
		}
	}
	if len(got) > 1 {
		t.Errorf("got terms %q, want at most the [phone:<id>] id (contract holdout-recall)", got)
	}
}

func TestTermsDistinctAndCaseFolded(t *testing.T) {
	if got := terms("Essays ESSAYS essays Essays"); len(got) != 1 {
		t.Errorf("got terms %q, want 1 (terms are distinct and lower-cased)", got)
	}
}

func TestTermsArabicClitics(t *testing.T) {
	base := terms("واجبات")
	if len(base) != 1 {
		t.Fatalf("got terms %q for one word, want 1", base)
	}
	for _, v := range []string{"والواجبات", "الواجبات", "بالواجبات", "فالواجبات", "كالواجبات", "للواجبات"} {
		if got := terms(v); !sameSet(got, base) {
			t.Errorf("got terms %q for %s, want %q (a leading clitic doesn't make a new word, contract holdout-recall)", got, v, base)
		}
	}
	short, stem := terms("ولد"), terms("لد")
	if len(short) != 1 || sameSet(short, stem) {
		t.Errorf("got terms %q for ولد and %q for لد, want ولد kept whole (a clitic is only dropped when 3 letters remain)", short, stem)
	}
}

func TestTermsNoiseOnlyOverlapIsEmpty(t *testing.T) {
	for _, p := range [][2]string{{noiseA, noiseB}, {noiseArA, noiseArB}} {
		b := terms(p[1])
		var shared []string
		for _, w := range terms(p[0]) {
			if slices.Contains(b, w) {
				shared = append(shared, w)
			}
		}
		if len(shared) > 3 {
			t.Errorf("got shared terms %q, want at most 3 (a shared link, phone placeholder, greetings and function words are not shared wording)", shared)
		}
	}
}

// field reads or writes an exported field by name, so the tests compile before the builder adds
// it (contract holdout-recall names the fields).
func field(t *testing.T, ptr any, name string) reflect.Value {
	t.Helper()
	f := reflect.ValueOf(ptr).Elem().FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("got no field %s on %T, want it (contract holdout-recall public surface)", name, ptr)
	}
	return f
}

func asFloat(v reflect.Value) float64 {
	if v.CanInt() {
		return float64(v.Int())
	}
	return v.Float()
}

func TestRelatedDefaultThresholds(t *testing.T) {
	d := neardup.DefaultThresholds()
	for name, want := range map[string]float64{"RelatedMinJaccard": 0.2, "RelatedMinContainment": 0.4, "RelatedMinTerms": 6} {
		if got := asFloat(field(t, &d, name)); got != want {
			t.Errorf("got default %s %v, want %v (contract holdout-recall)", name, got, want)
		}
	}
	if d.SimHashMaxDistance != 3 || d.MinHashMinJaccard != 0.9 || d.LooseSimHashMaxDistance != 12 || d.LooseMinHashMinJaccard != 0.4 {
		t.Errorf("got certain/loose defaults %+v, want 3/0.9/12/0.4 unchanged (this feature doesn't move CERTAIN or Loose)", d)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("got Validate error %v for the defaults, want nil", err)
	}
}

func TestRelatedThresholdsValidate(t *testing.T) {
	if err := explicit.Validate(); err != nil {
		t.Errorf("got Validate error %v for a 4-field Thresholds literal, want nil (zero Related fields mean Related off)", err)
	}
	set := func(name string, v float64) neardup.Thresholds {
		th := neardup.DefaultThresholds()
		f := field(t, &th, name)
		if f.CanInt() {
			f.SetInt(int64(v))
		} else {
			f.SetFloat(v)
		}
		return th
	}
	bad := map[string]neardup.Thresholds{
		"negative jaccard":       set("RelatedMinJaccard", -0.1),
		"jaccard above 1":        set("RelatedMinJaccard", 1.5),
		"negative containment":   set("RelatedMinContainment", -0.1),
		"containment above 1":    set("RelatedMinContainment", 1.5),
		"negative minimum terms": set("RelatedMinTerms", -1),
	}
	for name, th := range bad {
		if err := th.Validate(); err == nil {
			t.Errorf("%s: got nil from Validate, want an error (an out-of-range Related limit would match everything)", name)
		}
	}
}

func TestRelatedMatch(t *testing.T) {
	tests := []struct {
		name, entry, query string
		want               neardup.Closeness
		containment        bool
		why                string
	}{
		{"short ad inside long ad", adLong, adShort, neardup.Related, true, "containment ≥ 0.4 with ≥ 6 terms on the smaller side, even though Jaccard is low"},
		{"five-term ad inside long ad", adLong, adFive, neardup.None, false, "containment needs at least 6 terms on the smaller side, and Jaccard is under 0.2"},
		{"arabic short ad with other clitics", adLongAr, adShortAr, neardup.Related, true, "clitic variants count as the same term, so the short ad is contained"},
		{"jaccard path on short texts", jacA, jacB, neardup.Related, false, "match-term Jaccard 0.25 ≥ 0.2"},
		{"english noise only", noiseA, noiseB, neardup.None, false, "a shared link, placeholder, greetings and function words are not shared wording"},
		{"arabic noise only", noiseArA, noiseArB, neardup.None, false, "a shared link, placeholder, greetings and function words are not shared wording"},
		{"other language", adLongAr, adShort, neardup.None, false, "no shared terms"},
		{"unrelated chat", adLong, "The football match moved to Saturday at five, bring water and your kit", neardup.None, false, "normal chat must not look like the corpus, DESIGN §8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := neardup.NewIndex(neardup.DefaultThresholds())
			ix.Add("e1", neardup.FingerprintText(tt.entry))
			m := ix.Match(neardup.FingerprintText(tt.query))
			if m.Closeness != tt.want {
				t.Fatalf("got %s (distance %d, jaccard %.2f), want %s (%s; contract holdout-recall)", m.Closeness, m.Distance, m.Jaccard, tt.want, tt.why)
			}
			if tt.want == neardup.Related && m.EntryID != "e1" {
				t.Errorf("got entry %q, want e1 (the match names the corpus entry it hit, DESIGN §7.3 evidence)", m.EntryID)
			}
			if tt.containment {
				if c := asFloat(field(t, &m, "Containment")); c < 0.4 {
					t.Errorf("got Match.Containment %.2f, want ≥ 0.4 (the short ad's terms are all in the entry)", c)
				}
			}
		})
	}
}

func TestRelatedPrecedence(t *testing.T) {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("ad", neardup.FingerprintText(adLong))
	ix.Add("tmpl", neardup.FingerprintText(template))
	tests := []struct {
		name, query string
		want        neardup.Closeness
		entry       string
	}{
		{"near-copy stays certain", template, neardup.Certain, "tmpl"},
		{"rewrite stays loose", rewrite, neardup.Loose, "tmpl"},
		{"short ad is related", adShort, neardup.Related, "ad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := ix.Match(neardup.FingerprintText(tt.query))
			if m.Closeness != tt.want || m.EntryID != tt.entry {
				t.Errorf("got %s via %q, want %s via %q (Certain > Loose > Related > None, DESIGN §7.0, §7.3)", m.Closeness, m.EntryID, tt.want, tt.entry)
			}
		})
	}
	if neardup.Related <= neardup.None || neardup.Related >= neardup.Loose || neardup.Loose >= neardup.Certain {
		t.Errorf("got order None=%d Related=%d Loose=%d Certain=%d, want None < Related < Loose < Certain", neardup.None, neardup.Related, neardup.Loose, neardup.Certain)
	}
	if neardup.Related.String() != "related" {
		t.Errorf("got Related.String() %q, want \"related\"", neardup.Related.String())
	}
}

func TestRelatedOffWithOldThresholds(t *testing.T) {
	ix := neardup.NewIndex(explicit)
	ix.Add("ad", neardup.FingerprintText(adLong))
	if m := ix.Match(neardup.FingerprintText(adShort)); m.Closeness != neardup.None {
		t.Errorf("got %s, want none (zero Related thresholds turn Related off, so old configurations behave as before)", m.Closeness)
	}
}

func TestRelatedRespectsAllowList(t *testing.T) {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("ad", neardup.FingerprintText(adLong))
	ix.Allow(neardup.FingerprintText(adShort))
	if m := ix.Match(neardup.FingerprintText(adShort)); m.Closeness != neardup.None || !m.Allowed {
		t.Errorf("got %s allowed=%v, want none allowed=true (an overturned message never matches at any level, DESIGN §7.5)", m.Closeness, m.Allowed)
	}
}

func surfaceEnvelope(text string) *envelope.Envelope {
	e := &envelope.Envelope{Group: "120363000000000001@g.us", Kind: envelope.KindText, Texts: []envelope.TextSurface{{Source: envelope.SourceBody, Raw: text}}}
	pipeline.NormalizeTexts(e)
	return e
}

func analyze(t *testing.T, a *neardup.Analyzer[struct{}], text string) []detect.Signal {
	t.Helper()
	e := surfaceEnvelope(text)
	if !a.Applies(e, &struct{}{}) {
		t.Fatalf("got Applies false for a text message, want true")
	}
	sigs, err := a.Analyze(t.Context(), e, &detect.State{})
	if err != nil {
		t.Fatalf("Analyze: got error %v, want nil", err)
	}
	return sigs
}

func TestAnalyzerEmitsRelated(t *testing.T) {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("ad-en", neardup.FingerprintText(adLong))
	ix.Add("ad-ar", neardup.FingerprintText(adLongAr))
	a := neardup.NewAnalyzer[struct{}](func(wa.JID) []*neardup.Index { return []*neardup.Index{ix} })
	for _, tc := range []struct{ name, text, entry string }{{"english", adShort, "ad-en"}, {"arabic", adShortAr, "ad-ar"}} {
		t.Run(tc.name, func(t *testing.T) {
			sigs := analyze(t, a, tc.text)
			if len(sigs) != 1 {
				t.Fatalf("got %d signals %+v, want exactly 1 corpus.related (at most one corpus signal per message)", len(sigs), sigs)
			}
			s := sigs[0]
			if s.Code != "corpus.related" || neardup.CodeRelated != "corpus.related" {
				t.Errorf("got code %q, want corpus.related", s.Code)
			}
			if s.Weight != 2.5 || neardup.WeightRelated != 2.5 {
				t.Errorf("got weight %v (WeightRelated %v), want 2.5 (below T_review alone: related wording needs another signal, contract holdout-recall)", s.Weight, neardup.WeightRelated)
			}
			if s.Known || s.Hard {
				t.Errorf("got known=%v hard=%v, want neither (related wording is a rules signal, never known evidence, DESIGN §7.0, DET-002)", s.Known, s.Hard)
			}
			if !strings.Contains(s.Evidence, tc.entry) {
				t.Errorf("got evidence %q, want it to name entry %s (admins see why, DESIGN §7.3)", s.Evidence, tc.entry)
			}
		})
	}
}

func TestAnalyzerLooseAndCertainUnchanged(t *testing.T) {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("ad", neardup.FingerprintText(adLong))
	ix.Add("tmpl", neardup.FingerprintText(template))
	a := neardup.NewAnalyzer[struct{}](func(wa.JID) []*neardup.Index { return []*neardup.Index{ix} })
	for _, tc := range []struct {
		name, text, code string
		weight           float64
		known            bool
	}{
		{"near-copy", template, neardup.CodeCertain, neardup.WeightCertain, true},
		{"rewrite", rewrite, neardup.CodeLoose, neardup.WeightLoose, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sigs := analyze(t, a, tc.text)
			if len(sigs) != 1 || sigs[0].Code != tc.code || sigs[0].Weight != tc.weight || sigs[0].Known != tc.known {
				t.Errorf("got %+v, want one %s signal weight %v known=%v (existing corpus outcomes are unchanged)", sigs, tc.code, tc.weight, tc.known)
			}
		})
	}
}

func TestAnalyzerRelatedSkipsAllowListed(t *testing.T) {
	ix := neardup.NewIndex(neardup.DefaultThresholds())
	ix.Add("ad", neardup.FingerprintText(adLong))
	ix.Allow(neardup.FingerprintText(adShort))
	a := neardup.NewAnalyzer[struct{}](func(wa.JID) []*neardup.Index { return []*neardup.Index{ix} })
	if sigs := analyze(t, a, adShort); len(sigs) != 0 {
		t.Errorf("got %+v, want no signal (an overturned message never matches again, DESIGN §7.5)", sigs)
	}
}
