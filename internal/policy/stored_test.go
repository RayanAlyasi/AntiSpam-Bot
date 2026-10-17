package policy

import (
	"encoding/json"
	"testing"
)

func TestParseStoredFillsNewKeys(t *testing.T) {
	// A document written before enforcement.ask_before and limits.max_deletions_per_minute existed.
	var m map[string]any
	raw, _ := json.Marshal(Defaults())
	_ = json.Unmarshal(raw, &m)
	delete(m["enforcement"].(map[string]any), "ask_before")
	delete(m["limits"].(map[string]any), "max_deletions_per_minute")
	m["limits"].(map[string]any)["max_bans_per_hour"] = 4
	old, _ := json.Marshal(m)
	if _, err := Parse(old); err == nil {
		t.Fatal("Parse(old document) = nil, want missing keys reported (edits must be complete)")
	}
	s, err := ParseStored(old)
	if err != nil {
		t.Fatalf("ParseStored(old document) = %v, want the defaults filled in", err)
	}
	if s.Limits.MaxDeletionsPerMinute != 30 || s.Enforcement.AskBefore.OnTimeout != TimeoutDeleteNoBan || s.Limits.MaxBansPerHour != 4 {
		t.Errorf("ParseStored = limits %+v, ask_before %+v; want defaults for the new keys and the stored cap kept", s.Limits, s.Enforcement.AskBefore)
	}
	if _, err := ParseStored([]byte(`{"colour":"red"}`)); err == nil {
		t.Error("ParseStored with an unknown key = nil, want an error")
	}
}

func TestParseStoredFillsRowsFromItsPreset(t *testing.T) {
	relaxed, _ := PresetSettings(PresetRelaxed)
	var m map[string]any
	raw, _ := json.Marshal(relaxed)
	_ = json.Unmarshal(raw, &m)
	delete(m["enforcement"].(map[string]any)["autoban_matrix"].(map[string]any), "polls")
	old, _ := json.Marshal(m)
	s, err := ParseStored(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Enforcement.AutobanMatrix[TypePolls].Action; got != CellDeleteOnly {
		t.Errorf("missing polls row in a relaxed document = %s, want relaxed's delete_only, not standard's cell", got)
	}
}
