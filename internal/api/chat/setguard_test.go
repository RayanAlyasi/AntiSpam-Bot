package chat_test

import "testing"

// TestSetCantReachReviewGroup: no .set key, parent keys included, can change the review group;
// only .reviewgroup here, sent inside that group, can (DESIGN §9.5).
func TestSetCantReachReviewGroup(t *testing.T) {
	r := newRig(t)
	for _, cmd := range []string{
		`.set review.review_group 120363000000000002@g.us`,
		`.set review {"destination":"group","review_group":"120363000000000002@g.us"}`,
	} {
		r.dm(t, sara, cmd)
		if r.value(cars, "review.review_group") != "" {
			t.Fatalf("%s changed the review group, want refused", cmd)
		}
	}
}

// TestOneReviewGroupServesSeveral: an admin of another group can attach it to a review group
// that already serves one (DESIGN §9.5).
func TestOneReviewGroupServesSeveral(t *testing.T) {
	r := newRig(t)
	r.groups.Serve(admins, cars)
	r.in(t, admins, omar, ".reviewgroup here")
	if r.value(shop, "review.review_group") != string(admins) {
		t.Errorf("Shop's review group = %q, want %s", r.value(shop, "review.review_group"), admins)
	}
}
