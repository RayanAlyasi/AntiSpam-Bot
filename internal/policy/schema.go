package policy

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// enumValues lists the allowed values of the string types used in the document.
var enumValues = map[reflect.Type][]string{
	reflect.TypeFor[Preset]():          {"relaxed", "standard", "strict", "ask_admins", "custom"},
	reflect.TypeFor[Mode]():            {"shadow", "enforce", "paused"},
	reflect.TypeFor[EnforcementMode](): {"auto", "ask_delete_first", "ask_before_action"},
	reflect.TypeFor[ViolationAction](): {"delete_only", "ask", "ban"},
	reflect.TypeFor[TimeoutAction]():   {"keep_deleted_no_ban", "delete_no_ban", "auto_ban", "dismiss", "leave"},
	reflect.TypeFor[ContentRule]():     {"allow", "block", "block_for_new", "block_for_untrusted"},
	reflect.TypeFor[CellAction]():      {"auto_ban", "ask", "delete_only", "allow"},
	reflect.TypeFor[Source]():          {"known", "ai"},
}

var (
	schemaOnce sync.Once
	schemaDoc  map[string]any
	schemaJSON []byte
)

func buildSchema() {
	root := schemaFor(reflect.TypeFor[Settings]())
	root["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	root["title"] = "Group settings (DESIGN.md §12.1)"
	schemaDoc = root
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("policy: settings schema doesn't encode: %v", err)) // a programming error
	}
	schemaJSON = b
}

// Schema returns the JSON Schema of the settings document, generated from the Go struct, for
// the website's editor and for validating stored documents.
func Schema() []byte {
	schemaOnce.Do(buildSchema)
	return slices.Clone(schemaJSON)
}

func schemaFor(t reflect.Type) map[string]any {
	if vals, ok := enumValues[t]; ok {
		return map[string]any{"type": "string", "enum": vals}
	}
	if t == reflect.TypeFor[Duration]() {
		return map[string]any{"type": "string", "description": "a Go duration such as 15m or 168h"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		s := schemaFor(t.Elem())
		s["type"] = []any{s["type"], "null"}
		return s
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			p := schemaFor(f.Type)
			if e := f.Tag.Get("enum"); e != "" {
				p["enum"] = strings.Split(e, ",")
			}
			props[name] = p
			if !strings.Contains(opts, "omitempty") {
				required = append(required, name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	case reflect.Map:
		props := map[string]any{}
		var required []string
		if t.Key() == reflect.TypeFor[ContentType]() {
			for _, ct := range contentTypes {
				props[string(ct)] = schemaFor(t.Elem())
				required = append(required, string(ct))
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	case reflect.Slice:
		return map[string]any{"type": []any{"array", "null"}, "items": schemaFor(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		panic(fmt.Sprintf("policy: no schema for %s", t)) // a programming error
	}
}

// ValidateDocument checks a raw settings document against Schema: types, enums, required
// keys and no unknown keys. Errors name the dotted key.
func ValidateDocument(raw []byte) error {
	schemaOnce.Do(buildSchema)
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: not JSON: %w", ErrInvalidSettings, err)
	}
	return checkSchema("", schemaDoc, doc)
}

func checkSchema(key string, s map[string]any, v any) error {
	at := key
	if at == "" {
		at = "(document)"
	}
	if !typeMatches(s["type"], v) {
		return invalid(at, "want %v, got %s", s["type"], jsonType(v))
	}
	if enum, ok := s["enum"].([]string); ok {
		str, _ := v.(string)
		if !slices.Contains(enum, str) {
			return invalid(at, "%q is not one of %s", str, strings.Join(enum, ", "))
		}
	}
	switch val := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		for _, r := range s["required"].([]string) {
			if _, ok := val[r]; !ok {
				return invalid(joinKey(key, r), "missing")
			}
		}
		for _, k := range slices.Sorted(maps.Keys(val)) {
			ps, ok := props[k].(map[string]any)
			if !ok {
				return fmt.Errorf("%w: %s", ErrUnknownKey, joinKey(key, k))
			}
			if err := checkSchema(joinKey(key, k), ps, val[k]); err != nil {
				return err
			}
		}
	case []any:
		items, _ := s["items"].(map[string]any)
		for i, item := range val {
			if err := checkSchema(fmt.Sprintf("%s[%d]", key, i), items, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func typeMatches(want any, v any) bool {
	switch w := want.(type) {
	case string:
		return jsonTypeIs(w, v)
	case []any:
		for _, t := range w {
			if s, ok := t.(string); ok && jsonTypeIs(s, v) {
				return true
			}
		}
	}
	return false
}

func jsonTypeIs(want string, v any) bool {
	switch want {
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	default:
		return jsonType(v) == want
	}
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
