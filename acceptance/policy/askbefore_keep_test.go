package policy_test

import (
	"encoding/json"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// Maintainer decision 2026-10-18: in ask_before_action nothing is deleted or banned unless an
// admin says so; a timed-out case leaves the message (DESIGN §8.3 (b), §9.5 Timeouts, §12.1).

// TestAskBeforeKeep_DefaultIsLeave: the default ask_before fallback is leave, in the defaults
// and in every named preset.
func TestAskBeforeKeep_DefaultIsLeave(t *testing.T) {
	if got := policy.Defaults().Enforcement.AskBefore.OnTimeout; got != policy.TimeoutLeave {
		t.Errorf("Defaults().Enforcement.AskBefore.OnTimeout = %q, want %q (the bot never deletes unless an admin says so, maintainer decision 2026-10-18, DESIGN §8.3 (b))", got, policy.TimeoutLeave)
	}
	for _, p := range namedPresets {
		if got := mustPreset(t, p).Enforcement.AskBefore.OnTimeout; got != policy.TimeoutLeave {
			t.Errorf("preset %s: enforcement.ask_before.on_timeout = %q, want %q (default fallback of ask_before_action is leave, DESIGN §9.5)", p, got, policy.TimeoutLeave)
		}
	}
}

// storedDoc marshals Defaults() and lets edit change the decoded JSON tree before it is
// marshalled again, as a document written by an older binary would look.
func storedDoc(t *testing.T, edit func(enforcement map[string]any)) []byte {
	t.Helper()
	raw, err := json.Marshal(policy.Defaults())
	if err != nil {
		t.Fatalf("marshal Defaults(): %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal Defaults(): %v", err)
	}
	enf, ok := doc["enforcement"].(map[string]any)
	if !ok {
		t.Fatalf("Defaults() JSON has no enforcement object: %s", raw)
	}
	edit(enf)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal edited doc: %v", err)
	}
	return out
}

// TestAskBeforeKeep_ParseStoredMissingAskBefore: a stored document without enforcement.ask_before
// takes the new default (leave); an explicit delete_no_ban is read back unchanged.
func TestAskBeforeKeep_ParseStoredMissingAskBefore(t *testing.T) {
	missing := storedDoc(t, func(e map[string]any) { delete(e, "ask_before") })
	s, err := policy.ParseStored(missing)
	if err != nil {
		t.Fatalf("ParseStored(doc without enforcement.ask_before) error = %v, want nil (keys added later take their defaults)", err)
	}
	if got := s.Enforcement.AskBefore.OnTimeout; got != policy.TimeoutLeave {
		t.Errorf("ParseStored(doc without enforcement.ask_before): on_timeout = %q, want %q (missing keys take the default, which is now leave, DESIGN §12.1)", got, policy.TimeoutLeave)
	}

	explicit := storedDoc(t, func(e map[string]any) {
		ab, ok := e["ask_before"].(map[string]any)
		if !ok {
			t.Fatalf("Defaults() JSON has no enforcement.ask_before object")
		}
		ab["on_timeout"] = "delete_no_ban"
	})
	s, err = policy.ParseStored(explicit)
	if err != nil {
		t.Fatalf("ParseStored(doc with delete_no_ban) error = %v, want nil", err)
	}
	if got := s.Enforcement.AskBefore.OnTimeout; got != policy.TimeoutDeleteNoBan {
		t.Errorf("ParseStored(doc with delete_no_ban): on_timeout = %q, want delete_no_ban (a stored choice is read as stored; only migration 00038 rewrites documents)", got)
	}
}

// TestAskBeforeKeep_OnTimeoutValues: leave, delete_no_ban and dismiss are accepted; auto_ban and
// unknown values are refused (the ask_before fallback can never ban, DESIGN §8.3 (b)).
func TestAskBeforeKeep_OnTimeoutValues(t *testing.T) {
	const key = "enforcement.ask_before.on_timeout"
	for _, v := range []policy.TimeoutAction{policy.TimeoutLeave, policy.TimeoutDeleteNoBan, policy.TimeoutDismiss} {
		s, err := policy.Defaults().SetText(key, string(v))
		if err != nil {
			t.Errorf("SetText(%s, %s) error = %v, want nil (%s is a valid ask_before fallback, DESIGN §9.5)", key, v, err, v)
			continue
		}
		if s.Enforcement.AskBefore.OnTimeout != v {
			t.Errorf("after SetText(%s, %s): on_timeout = %q, want %q", key, v, s.Enforcement.AskBefore.OnTimeout, v)
		}
		if err := s.Validate(); err != nil {
			t.Errorf("Validate() with %s=%s error = %v, want nil", key, v, err)
		}
	}
	for _, v := range []string{string(policy.TimeoutAutoBan), "forever"} {
		if _, err := policy.Defaults().SetText(key, v); err == nil {
			t.Errorf("SetText(%s, %s) error = nil, want refused (ask_before_action never bans and only knows leave, delete_no_ban, dismiss, DESIGN §8.3 (b))", key, v)
		}
		s := policy.Defaults()
		s.Enforcement.AskBefore.OnTimeout = policy.TimeoutAction(v)
		if err := s.Validate(); err == nil {
			t.Errorf("Validate() with %s=%s = nil, want an error", key, v)
		}
	}
}

// TestAskBeforeKeep_DecideFallback: in ask_before_action the request's fallback is leave unless
// the group chose delete_no_ban or dismiss; uncertain messages are never deleted by a fallback;
// review.on_timeout plays no part (DESIGN §8.3 (b), §9.5).
func TestAskBeforeKeep_DecideFallback(t *testing.T) {
	spamF := spam(policy.BandCertain, policy.SourceKnown, policy.TypeText)
	aiSpam := spam(policy.BandUncertain, policy.SourceAI, policy.TypeLinks)
	violation := policy.Finding{
		Band: policy.BandClean, Type: policy.TypeLinks, At: t0,
		Violations: []policy.Violation{{Rule: "content.links", Detail: "blocked type"}},
	}
	uncertain := policy.Finding{
		Band: policy.BandStillUncertain, Type: policy.TypeText, Score: 0.7,
		Signals: []policy.Signal{contentSignal}, At: t0,
	}

	type finding struct {
		name string
		f    policy.Finding
		kind policy.ReviewKind
	}
	findings := []finding{
		{"certain known spam", spamF, policy.ReviewAskBefore},
		{"AI-verified spam", aiSpam, policy.ReviewAskBefore},
		{"content-rule violation", violation, policy.ReviewAskBefore},
		{"still uncertain", uncertain, policy.ReviewUncertain},
	}
	cases := []struct {
		configured policy.TimeoutAction
		want       func(kind policy.ReviewKind) policy.TimeoutAction
	}{
		{policy.TimeoutLeave, func(policy.ReviewKind) policy.TimeoutAction { return policy.TimeoutLeave }},
		{policy.TimeoutDismiss, func(policy.ReviewKind) policy.TimeoutAction { return policy.TimeoutDismiss }},
		{policy.TimeoutDeleteNoBan, func(k policy.ReviewKind) policy.TimeoutAction {
			if k == policy.ReviewUncertain {
				return policy.TimeoutLeave
			}
			return policy.TimeoutDeleteNoBan
		}},
	}
	for _, preset := range []policy.Preset{policy.PresetStandard, policy.PresetAskAdmins} {
		for _, tc := range cases {
			for _, reviewOn := range []policy.TimeoutAction{policy.TimeoutKeepDeletedNoBan, policy.TimeoutAutoBan} {
				s := mustPreset(t, preset)
				s.Enforcement.Mode = policy.EnforceAskBeforeAction
				s.Enforcement.AskBefore.OnTimeout = tc.configured
				s.Review.OnTimeout = reviewOn
				for _, fd := range findings {
					d := policy.Decide(s, fd.f, newMember())
					ctx := string(preset) + ", ask_before.on_timeout=" + string(tc.configured) + ", review.on_timeout=" + string(reviewOn) + ", " + fd.name
					if d.Delete || d.Ban {
						t.Errorf("%s: got %s, want no delete and no ban (nothing happens until an admin decides, DESIGN §8.3 (b))", ctx, describeDecision(d))
					}
					if d.Review == nil {
						t.Errorf("%s: got no review, want a %s review (admins are asked, DESIGN §8.3 (b))", ctx, fd.kind)
						continue
					}
					if d.Review.Kind != fd.kind {
						t.Errorf("%s: got review kind %s, want %s", ctx, d.Review.Kind, fd.kind)
					}
					if want := tc.want(fd.kind); d.Review.OnTimeout != want {
						t.Errorf("%s: got OnTimeout %q, want %q (ask_before_action leaves the message unless the group chose delete_no_ban or dismiss; uncertain messages are never deleted by a fallback; review.on_timeout is ignored, DESIGN §9.5)", ctx, d.Review.OnTimeout, want)
					}
				}
			}
		}
	}

	// Defaults() switched to ask_before_action, nothing else changed: leave.
	s := policy.Defaults()
	s.Enforcement.Mode = policy.EnforceAskBeforeAction
	d := policy.Decide(s, spamF, newMember())
	if d.Review == nil || d.Review.OnTimeout != policy.TimeoutLeave {
		t.Errorf("Defaults() in ask_before_action, certain spam: got %s, want an ask_before review with OnTimeout leave (maintainer decision 2026-10-18)", describeDecision(d))
	}
}
