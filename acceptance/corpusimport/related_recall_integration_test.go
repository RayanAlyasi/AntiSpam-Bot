//go:build integration

package corpusimport_test

import (
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/corpusimport"
)

// Synthetic campaign ads (contract holdout-recall): a long confirmed "list of services + contact"
// ad, a short reworded ad from the same campaign with its own contact channel, and ordinary chat
// that shares the ad's vocabulary but offers no contact.
const (
	relAdLong = "Hello everyone, we provide complete student services: assignments, homework, research papers, " +
		"graduation projects, presentations, posters, summaries, translation, proofreading, editing, essays, theses, " +
		"dissertations, statistics, accounting, programming, engineering drawings, mathematics, physics, chemistry, " +
		"biology, nursing, pharmacy, medicine, law, management, marketing, economics, quizzes, exams, online lectures, " +
		"blackboard tasks, portfolio design, resume writing, cover letters, sick leave notes, medical excuses, hospital " +
		"reports, attendance certificates and internship letters. Experienced tutors, fast delivery, guaranteed grades, " +
		"affordable prices, confidential handling, discounts for groups, payment after completion. " +
		"Message us on WhatsApp wa.me/966551234567"
	relAdLongAr = "السلام عليكم ورحمة الله وبركاته، نقدم خدمات طلابية متكاملة: حل الواجبات والبحوث والتقارير والمشاريع " +
		"والعروض التقديمية والتلخيص والترجمة والتدقيق اللغوي وكتابة السيرة الذاتية والرسائل الجامعية وحل الاختبارات " +
		"والكويزات ومتابعة البلاك بورد وتصميم البوسترات والانفوجرافيك والمحاسبة والبرمجة والرياضيات والفيزياء والكيمياء " +
		"والتمريض والصيدلة والقانون والتسويق والاقتصاد، وأيضا الأعذار الطبية والإجازات المرضية وتقارير المستشفى وشهادات " +
		"الحضور وخطابات التدريب. أسعار مناسبة وتسليم سريع وضمان الدرجة وسرية تامة وخصومات للمجموعات والدفع بعد الإنجاز. " +
		"تواصل واتساب wa.me/966551234567"
	relAdShort    = "Translation, proofreading, essays, presentations, statistics and accounting assignments. Fast delivery, affordable prices, WhatsApp wa.me/966559876543"
	relAdShortAr  = "حل واجبات وبحوث وتقارير ومشاريع وترجمة وتلخيص وبرمجة ومحاسبة، تسليم سريع، واتساب 0559876543"
	relUnrelated  = "Cheap car rental at the airport, daily and weekly offers for families and tourists, call +966 55 765 4321"
	relSickNote   = "My sick leave notes, medical excuses and hospital reports were accepted, so the nursing exams and quizzes are postponed"
	relSickNoteAr = "الحمد لله سلمت الأعذار الطبية والإجازات المرضية وتقارير المستشفى وشهادات الحضور، والدكتور أجل الاختبارات والكويزات"
)

func TestHoldoutRecallRelatedWithContact(t *testing.T) {
	pool := newPool(t)
	rec := func(line int, text, label, lang, cluster string) corpusimport.Record {
		return corpusimport.Record{Line: line, Text: text, Label: label, Kind: "text", Language: lang, Category: "academic", ClusterID: cluster}
	}
	recs := []corpusimport.Record{
		rec(1, relAdLong, "spam", "en", "tr-en"),
		rec(2, relAdLongAr, "spam", "ar", "tr-ar"),
		rec(3, relAdShort, "spam", "en", "hl-en"),
		rec(4, relAdShortAr, "spam", "ar", "hl-ar"),
		rec(5, relUnrelated, "spam", "en", "hm"),
		{Line: 6, Text: relSickNote, Label: "ham", Language: "en"},
		{Line: 7, Text: relSickNoteAr, Label: "ham", Language: "ar"},
	}
	o := opts(inGroup(groupA), 0)
	o.HoldoutClusters = []string{"hl-en", "hl-ar", "hm"}

	rep := run(t, pool, recs, o)

	r := rep.Recall
	if r.Total != 3 {
		t.Fatalf("got recall %+v, want 3 held-out records (the listed clusters are held out, DESIGN §7.8)", r)
	}
	if r.ToAdmins != 2 || r.Missed != 1 {
		t.Errorf("got recall %+v, want 2 sent to admins and 1 missed (a short reworded ad inside a stored ad plus a contact channel reaches T_review; a contact channel alone doesn't, contract holdout-recall)", r)
	}
	if r.Deleted != 0 {
		t.Errorf("got recall %+v, want 0 deleted (related wording and a contact channel are rules signals: never deleted without the AI, DESIGN §7.3)", r)
	}
	if rep.HamCaught != 0 {
		t.Errorf("got %d ham caught, want 0 (ordinary sick-leave chat offers no contact and must stay clean, DET-002)", rep.HamCaught)
	}
}
