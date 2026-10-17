package policy

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDefaultsAreValidStandard(t *testing.T) {
	s := Defaults()
	if err := s.Validate(); err != nil {
		t.Fatalf("Defaults().Validate() = %v, want nil (the default document must always be storable)", err)
	}
	if s.Preset != PresetStandard || s.Mode != ModeEnforce || !s.Silent || s.Enforcement.Mode != EnforceAuto {
		t.Errorf("defaults = preset %s, mode %s, silent %v, enforcement %s; want standard, enforce, silent, auto (DESIGN §12.1)",
			s.Preset, s.Mode, s.Silent, s.Enforcement.Mode)
	}
	if s.Policy.Action != ViolationDeleteOnly || s.Policy.Repeat != nil {
		t.Errorf("policy = %+v, want delete_only with no repeat (violations are a silent delete by default, §9.3)", s.Policy)
	}
	if s.Uncertain.DeleteWhilePending {
		t.Error("uncertain.delete_while_pending = true, want false (still-uncertain messages stay visible, §7.0)")
	}
	jj := s.NewMembers.JustJoined
	if !jj.Enabled || jj.Weight != 0.8 || time.Duration(jj.Window) != 24*time.Hour {
		t.Errorf("just_joined = %+v, want on, 0.8, 24h (§8.2)", jj)
	}
	if s.Limits.MaxBansPerHour != 10 {
		t.Errorf("limits.max_bans_per_hour = %d, want 10 (§8.6)", s.Limits.MaxBansPerHour)
	}
	if e := s.Enforcement.Established; e.MinDays != 7 || e.MinCleanMessages != 20 {
		t.Errorf("established = %+v, want 7 days and 20 clean messages (§8.1)", e)
	}
}

func TestDocumentRoundTrip(t *testing.T) {
	for _, p := range []Preset{PresetRelaxed, PresetStandard, PresetStrict, PresetAskAdmins, PresetCustom} {
		s, err := PresetSettings(p)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: Parse(Marshal(s)) = %v, want nil", p, err)
		}
		if d, _ := Diff(s, got); len(d) != 0 {
			t.Errorf("%s: round trip changed %v, want no change", p, d)
		}
	}
}

func TestParseRejects(t *testing.T) {
	base, _ := json.Marshal(Defaults())
	edit := func(f func(map[string]any)) []byte {
		var m map[string]any
		_ = json.Unmarshal(base, &m)
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	obj := func(m map[string]any, path ...string) map[string]any {
		for _, p := range path {
			m = m[p].(map[string]any)
		}
		return m
	}
	tests := []struct {
		name string
		doc  []byte
		want string
	}{
		{"not json", []byte("{"), "not JSON"},
		{"unknown top key", edit(func(m map[string]any) { m["colour"] = "red" }), "colour"},
		{"unknown nested key", edit(func(m map[string]any) { obj(m, "review")["timeot"] = "1h" }), "review.timeot"},
		{"wrong type", edit(func(m map[string]any) { m["silent"] = "yes" }), "silent"},
		{"bad enum", edit(func(m map[string]any) { m["mode"] = "loud" }), "mode"},
		{"missing key", edit(func(m map[string]any) { delete(m, "limits") }), "limits"},
		{"missing matrix row", edit(func(m map[string]any) { delete(obj(m, "enforcement", "autoban_matrix"), "videos") }), "videos"},
		{"unknown matrix row", edit(func(m map[string]any) {
			obj(m, "enforcement", "autoban_matrix")["reels"] = map[string]any{"action": "ask"}
		}), "reels"},
		{"rules may not ban", edit(func(m map[string]any) {
			obj(m, "enforcement", "autoban_matrix")["text"] = map[string]any{"action": "auto_ban", "require_source": []any{"rules"}}
		}), "require_source"},
		{"else auto_ban", edit(func(m map[string]any) {
			obj(m, "enforcement", "autoban_matrix")["text"] = map[string]any{"action": "auto_ban", "else": "auto_ban"}
		}), "else"},
		{"bad duration", edit(func(m map[string]any) { obj(m, "review")["timeout"] = "soon" }), "soon"},
		{"thresholds out of order", edit(func(m map[string]any) { obj(m, "detection", "thresholds")["review"] = 0.9 }), "review ≤ delete ≤ ban"},
		{"threshold above 1", edit(func(m map[string]any) { obj(m, "detection", "thresholds")["ban"] = 1.5 }), "detection.thresholds.ban"},
		{"timeout ban in ask mode", edit(func(m map[string]any) {
			obj(m, "enforcement")["mode"] = "ask_delete_first"
			obj(m, "review")["on_timeout"] = "auto_ban"
		}), "ask modes never ban"},
		{"ask_before fallback ban", edit(func(m map[string]any) { obj(m, "enforcement", "ask_before")["on_timeout"] = "auto_ban" }), "enforcement.ask_before.on_timeout"},
		{"repeat without window", edit(func(m map[string]any) {
			obj(m, "policy")["repeat"] = map[string]any{"count": 3, "window": "0s", "then": "ask"}
		}), "policy.repeat.window"},
		{"bad deletion limit", edit(func(m map[string]any) { obj(m, "limits")["max_deletions_per_minute"] = 0 }), "max_deletions_per_minute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.doc)
			if err == nil {
				t.Fatalf("Parse = nil, want an error mentioning %q (invalid documents are never stored)", tt.want)
			}
			if !errors.Is(err, ErrInvalidSettings) && !errors.Is(err, ErrUnknownKey) {
				t.Errorf("Parse error %v doesn't wrap ErrInvalidSettings or ErrUnknownKey", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse error %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestKeysCoverEveryLeaf(t *testing.T) {
	keys := Keys()
	for _, want := range []string{
		"preset", "mode", "silent",
		"enforcement.mode", "enforcement.established.min_days", "enforcement.ban.rejoin_block",
		"enforcement.autoban_matrix.videos.action", "enforcement.autoban_matrix.videos.require_source",
		"enforcement.autoban_matrix.audio.action", "review.timeout", "review.on_timeout",
		"new_members.just_joined.enabled", "new_members.just_joined.weight", "new_members.just_joined.window",
		"content.links", "content.document_extensions_block", "limits.max_bans_per_hour", "policy.repeat",
	} {
		if !slices.Contains(keys, want) {
			t.Errorf("Keys() is missing %q (every leaf must be reachable by dotted key, §12.1)", want)
		}
	}
	s := Defaults()
	for _, k := range keys {
		if _, err := s.Get(k); err != nil {
			t.Errorf("Get(%q) = %v, want the value", k, err)
		}
	}
}

func TestGetSet(t *testing.T) {
	s := Defaults()
	tests := []struct {
		key, text string
		want      string // JSON read back with Get
	}{
		{"review.timeout", "2h", `"2h0m0s"`},
		{"silent", "false", `false`},
		{"limits.max_bans_per_hour", "3", `3`},
		{"enforcement.autoban_matrix.videos", `{"action":"ask"}`, `{"action":"ask"}`},
		{"enforcement.autoban_matrix.links.action", "delete_only", `{"action":"delete_only"}`},
		{
			"enforcement.autoban_matrix.audio", `{"action":"auto_ban","require_source":["known"],"else":"delete_only"}`,
			`{"action":"auto_ban","else":"delete_only","require_source":["known"]}`,
		},
		{"policy.repeat", `{"count":3,"window":"168h","then":"ask"}`, `{"count":3,"then":"ask","window":"168h0m0s"}`},
		{"content.document_extensions_block", `["apk","exe"]`, `["apk","exe"]`},
		{"preset", "strict", `"strict"`},
	}
	for _, tt := range tests {
		got, err := s.SetText(tt.key, tt.text)
		if err != nil {
			t.Fatalf("SetText(%s, %s) = %v, want nil", tt.key, tt.text, err)
		}
		readKey := strings.TrimSuffix(tt.key, ".action")
		v, err := got.Get(readKey)
		if err != nil {
			t.Fatal(err)
		}
		if string(v) != tt.want {
			t.Errorf("after SetText(%s, %s), Get(%s) = %s, want %s", tt.key, tt.text, readKey, v, tt.want)
		}
	}
	if v, _ := s.Get("review.timeout"); string(v) != `"6h0m0s"` {
		t.Errorf("Set changed the original document: review.timeout = %s, want 6h (Set returns a copy)", v)
	}
}

func TestSetRejects(t *testing.T) {
	s := Defaults()
	for _, tt := range []struct{ key, text string }{
		{"review.timeot", "2h"},
		{"silent.loud", "true"},
		{"mode", "loud"},
		{"limits.max_bans_per_hour", "many"},
		{"enforcement.autoban_matrix.reels", `{"action":"ask"}`},
		{"preset", "chaos"},
		{"", "x"},
	} {
		if _, err := s.SetText(tt.key, tt.text); err == nil {
			t.Errorf("SetText(%q, %q) = nil, want an error", tt.key, tt.text)
		}
	}
}

func TestSchemaMatchesDocument(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(Schema(), &schema); err != nil {
		t.Fatalf("Schema() isn't JSON: %v", err)
	}
	props := schema["properties"].(map[string]any)
	for _, k := range []string{"preset", "enforcement", "content", "limits"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema has no %q property", k)
		}
	}
	mode := props["mode"].(map[string]any)
	if got := mode["enum"]; len(got.([]any)) != 3 {
		t.Errorf("schema mode enum = %v, want shadow, enforce, paused", got)
	}
	for _, p := range []Preset{PresetRelaxed, PresetStrict, PresetAskAdmins} {
		s, _ := PresetSettings(p)
		raw, _ := json.Marshal(s)
		if err := ValidateDocument(raw); err != nil {
			t.Errorf("ValidateDocument(%s preset) = %v, want nil", p, err)
		}
	}
}

func TestCloneIsDeep(t *testing.T) {
	s := Defaults()
	c := s.Clone()
	c.Enforcement.AutobanMatrix[TypeText] = Cell{Action: CellAllow}
	c.Review.Reviewers = append(c.Review.Reviewers, "1@lid")
	c.Content.DocumentExtensionsBlock[0] = "zip"
	if s.Enforcement.AutobanMatrix[TypeText].Action != CellAutoBan || len(s.Review.Reviewers) != 0 || s.Content.DocumentExtensionsBlock[0] != "apk" {
		t.Error("changing a clone changed the original")
	}
}
