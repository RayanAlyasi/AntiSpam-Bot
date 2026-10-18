package contact

import (
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/detect/textnorm"
	"github.com/RayanAlyasi/AntiSpam-Bot/internal/envelope"
)

func TestOffersPrivateOrder(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"order through the group admin", "*الطلب عن طريق مشرف القروب فقط* 👇", []string{offerPrivate}},
		{"order through the admin", "تقارير طبية معتمدة، للطلب عن طريق الأدمن", []string{offerPrivate}},
		{"contact in private", "للتواصـل 👇🏻\nخاص", []string{offerPrivate}},
		{"order in private", "اجازة مرضية\nتقرير طبي\nللطلب خاص", []string{offerPrivate}},
		{"contact us", "حل واجبات وبحوث، التواصل معنا", []string{offerPrivate}},
		{"and-prefixed inquiry", "وللاستفسار على الخاص", []string{offerPrivate}},
		{"english dm to order", "Assignments and reports. DM to order!", []string{offerPrivate}},
		{"english for orders", "Sick notes and medical reports, for orders dm", []string{offerPrivate}},
		{"in private to order", "بالخاص للطلب", []string{offerPrivate}},
		{"english order via admin", "order through the group admin only", []string{offerPrivate}},
		{"a word before the group", "للطلب راسل مشرف هذا القروب", []string{offerPrivate}},
		{"with an invite link", "للطلب خاص الادمن https://chat.whatsapp.com/AbCdEf123456", []string{offerInvite, offerPrivate}},

		{"special request", "عندي طلب خاص للدكتور بخصوص الاختبار", nil},
		{"project supervisor", "التواصل مع المشرف بخصوص المشروع", nil},
		{"send it in private", "ارسلي لي الملف خاص", nil},
		{"admin far from the order", "the admin posted the order of the presentations for next week", nil},
		{"contact the doctor", "تواصلت مع الدكتور وقال الاختبار الأحد", nil},
		{"question about a sick note", "عندي استفسار خاص بالاجازة المرضية: التقرير الطبي من صحتي هل تقبله الجامعات؟", nil},
		{"booking for first years", "الحجز خاص بطلاب السنة الاولى", nil},
		{"the form for an excuse", "تعبئة النموذج الخاص لطلب الاعتذار", nil},
		{"a question for the admin", "عندي استفسار للادمن بخصوص التقرير الطبي", nil},
		{"the doctor contacted us", "تواصل الدكتور معنا وقال اللي عنده تقرير طبي او اجازة مرضية من صحتي يرسله", nil},
		{"sent it in private", "ارسلت بالخاص طلب الاعتذار والتقرير الطبي المعتمد من صحتي", nil},
		{"order of presentations by dm", "I'll dm you the order of presentations", nil},
		{"booking in the inbox", "Got the booking confirmation in my inbox", nil},
		{"rejected by the admin", "تم رفض الطلب من الادمن", nil},
		{"contact the admin about it", "I will contact the admin about my sick note", nil},
		{"contacted me in private", "تواصل معي الدكتور على الخاص وقال ارسل التقرير الطبي المعتمد من صحتي", nil},
		{"reported speech after in private", "كلمته بالخاص وقال اطلب تقرير طبي معتمد من صحتي", nil},
		{"a special report", "اطلب تقرير طبي خاص من صحتي معتمد للجامعة", nil},
		{"ask the admin", "اطلب من الادمن يحذف اعلانات الاجازات المرضية والتقارير الطبية", nil},
		{"contact the admin to be added", "تواصلي مع الادمن عشان يضيفك", nil},
		{"booking details in the inbox", "check your inbox for booking details", nil},
		{"waiting for orders", "we are waiting for orders from the admin", nil},
		{"a clock time", "the meeting is at 2 pm, order of speakers attached", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &envelope.Envelope{Kind: envelope.KindText, Texts: []envelope.TextSurface{{Source: "body", Raw: tt.text}}}
			textnorm.Prepare(e, "")
			if got := Offers(e); !slices.Equal(got, tt.want) {
				t.Fatalf("offers %q, want %q (a call to order in private is a contact channel; a question or a noun phrase that only mentions one is ordinary chat)", got, tt.want)
			}
		})
	}
}
