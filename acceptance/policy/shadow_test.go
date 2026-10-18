package policy_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/RayanAlyasi/AntiSpam-Bot/internal/policy"
)

// TestShadowReactionsSetting: shadow.reactions is an opt-in bool, settable with .set, and a
// stored document written before the key existed reads as off (DESIGN §8.4, §12.1).
func TestShadowReactionsSetting(t *testing.T) {
	const key = "shadow.reactions"
	if policy.Defaults().Shadow.Reactions {
		t.Errorf("Defaults().Shadow.Reactions = true, want false (shadow reactions are opt-in: by default shadow mode posts nothing, DESIGN §8.4, §9.7)")
	}
	if !slices.Contains(policy.Keys(), key) {
		t.Errorf("Keys() lacks %q, want it listed (every setting has a dotted key for .set/.get, DESIGN §12.1)", key)
	}

	on, err := policy.Defaults().SetText(key, "true")
	if err != nil {
		t.Fatalf(".set %s true: %v, want nil (it's a bool setting, DESIGN §12.1)", key, err)
	}
	if !on.Shadow.Reactions {
		t.Errorf(".set %s true left Shadow.Reactions false, want true", key)
	}
	off, err := on.SetText(key, "false")
	if err != nil || off.Shadow.Reactions {
		t.Errorf(".set %s false = %v, %v; want Reactions false and no error", key, off.Shadow.Reactions, err)
	}
	if _, err := policy.Defaults().SetText(key, "sometimes"); err == nil {
		t.Errorf(".set %s sometimes succeeded, want an error (the value must be a bool, DESIGN §12.1)", key)
	}

	// A stored "on" document reads back on.
	raw, err := json.Marshal(on)
	if err != nil {
		t.Fatal(err)
	}
	back, err := policy.ParseStored(raw)
	if err != nil || !back.Shadow.Reactions {
		t.Errorf("ParseStored(stored doc with shadow.reactions true) = %v, %v; want true and no error", back.Shadow.Reactions, err)
	}

	// A document stored before the key existed has no "shadow" object at all.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["shadow"]; !ok {
		t.Fatalf("marshalled settings have no \"shadow\" object, want the json name shadow (key %s)", key)
	}
	delete(doc, "shadow")
	old, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := policy.ParseStored(old)
	if err != nil {
		t.Fatalf("ParseStored(doc without shadow): %v, want nil (keys added later take their defaults)", err)
	}
	if s.Shadow.Reactions {
		t.Errorf("ParseStored(doc without shadow).Shadow.Reactions = true, want false (an old document counts as off)")
	}
}
