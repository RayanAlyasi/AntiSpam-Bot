package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ErrUnknownKey is returned for a dotted key that isn't part of the settings document.
var ErrUnknownKey = errors.New("unknown settings key")

// Keys returns every leaf's dotted key in the default document, sorted. Lists count as leaves.
func Keys() []string {
	l, err := leaves(Defaults())
	if err != nil {
		panic(fmt.Sprintf("policy: default settings don't encode: %v", err)) // a programming error
	}
	return slices.Sorted(maps.Keys(l))
}

// Get returns the JSON value at a dotted key: a leaf, or a whole sub-document for a prefix
// such as "enforcement.autoban_matrix.videos".
func (s Settings) Get(key string) (json.RawMessage, error) {
	doc, err := toTree(s)
	if err != nil {
		return nil, err
	}
	var cur any = doc
	for _, part := range splitKey(key) {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
		if cur, ok = obj[part]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownKey, key)
		}
	}
	return json.Marshal(cur)
}

// Set returns a copy of s with the value at key replaced by value (JSON), validated as a
// whole document. s itself is never changed, so a failed Set leaves nothing half-applied.
// Setting "preset" applies that preset (it resets the matrix and the extras).
func (s Settings) Set(key string, value json.RawMessage) (Settings, error) {
	parts := splitKey(key)
	if len(parts) == 0 {
		return Settings{}, fmt.Errorf("%w: empty key", ErrUnknownKey)
	}
	var v any
	if err := json.Unmarshal(value, &v); err != nil {
		return Settings{}, fmt.Errorf("%w: %s: value is not JSON: %w", ErrInvalidSettings, key, err)
	}
	if key == "preset" {
		p, ok := v.(string)
		if !ok {
			return Settings{}, invalid(key, "must be a string")
		}
		out := s.Clone()
		if err := out.ApplyPreset(Preset(p)); err != nil {
			return Settings{}, err
		}
		if err := out.Validate(); err != nil {
			return Settings{}, err
		}
		return out, nil
	}
	doc, err := toTree(s)
	if err != nil {
		return Settings{}, err
	}
	obj := doc
	for _, part := range parts[:len(parts)-1] {
		next, ok := obj[part].(map[string]any)
		if !ok {
			// A missing object (such as policy.repeat while it's null) is created; decoding then
			// rejects anything that isn't part of the document.
			if obj[part] != nil {
				return Settings{}, fmt.Errorf("%w: %s", ErrUnknownKey, key)
			}
			next = map[string]any{}
			obj[part] = next
		}
		obj = next
	}
	obj[parts[len(parts)-1]] = v
	raw, err := json.Marshal(doc)
	if err != nil {
		return Settings{}, fmt.Errorf("encode settings: %w", err)
	}
	out, err := Parse(raw)
	if err != nil {
		return Settings{}, fmt.Errorf("set %s: %w", key, err)
	}
	return out, nil
}

// SetText is Set for a value typed by a person (the `.set` command): valid JSON is used as is
// (true, 10, ["known"]), anything else is taken as a string (2h, ask).
func (s Settings) SetText(key, text string) (Settings, error) {
	text = strings.TrimSpace(text)
	raw := json.RawMessage(text)
	if !json.Valid(raw) {
		raw, _ = json.Marshal(text)
	}
	return s.Set(key, raw)
}

// Parse decodes a stored or edited settings document strictly: unknown keys and wrong types
// are errors, and the result must pass Validate.
func Parse(raw []byte) (Settings, error) {
	if err := ValidateDocument(raw); err != nil {
		return Settings{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Settings
	if err := dec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}
	// require_source and else only mean something for auto_ban; switching a cell to another
	// action by its .action key leaves them behind, so they're dropped rather than rejected.
	for t, c := range s.Enforcement.AutobanMatrix {
		if c.Action != CellAutoBan {
			s.Enforcement.AutobanMatrix[t] = Cell{Action: c.Action}
		}
	}
	if err := s.Validate(); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// ParseStored decodes a document read back from the database. Keys added after it was written
// take their default values; everything else is as strict as Parse (unknown keys and wrong
// types are errors, and the result must pass Validate).
//
// Missing keys come from the stored document's own preset, so a matrix row added later gets
// that preset's cell rather than Standard's.
func ParseStored(raw []byte) (Settings, error) {
	var head struct {
		Preset Preset `json:"preset"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Settings{}, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}
	s, err := PresetSettings(head.Preset)
	if err != nil {
		s = Defaults()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("%w: %w", ErrInvalidSettings, err)
	}
	full, err := json.Marshal(s)
	if err != nil {
		return Settings{}, fmt.Errorf("encode settings: %w", err)
	}
	return Parse(full)
}

func splitKey(key string) []string {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return strings.Split(key, ".")
}

func toTree(s Settings) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	return doc, nil
}

// leaves flattens s into dotted key → compact JSON value. Objects are walked; everything else,
// lists and null included, is a leaf.
func leaves(s Settings) (map[string][]byte, error) {
	doc, err := toTree(s)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	var walk func(prefix string, v any) error
	walk = func(prefix string, v any) error {
		if obj, ok := v.(map[string]any); ok && len(obj) > 0 {
			for k, child := range obj {
				if err := walk(joinKey(prefix, k), child); err != nil {
					return err
				}
			}
			return nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("encode %s: %w", prefix, err)
		}
		out[prefix] = b
		return nil
	}
	return out, walk("", doc)
}

func joinKey(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

func unionKeys(a, b map[string][]byte) []string {
	set := map[string]struct{}{}
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	return slices.Sorted(maps.Keys(set))
}
