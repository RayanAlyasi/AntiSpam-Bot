package llm

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPortableSchema(t *testing.T) {
	in := map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"confidence", "pattern"},
		"properties": map[string]any{
			"confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"reason":     map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"categories": map[string]any{"type": "array", "maxItems": 5, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []any{"a", "b"}}},
			"pattern":    map[string]any{"type": "string", "pattern": "^x"},
		},
	}
	before, _ := json.Marshal(in)
	got := PortableSchema(in)
	want := map[string]any{
		"type": "object", "additionalProperties": false, "required": []any{"confidence", "pattern"},
		"properties": map[string]any{
			"confidence": map[string]any{"type": "number"},
			"reason":     map[string]any{"type": "string"},
			"categories": map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []any{"a", "b"}}},
			"pattern":    map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PortableSchema() = %v, want %v (shape kept, value constraints dropped, a property named pattern kept)", got, want)
	}
	if after, _ := json.Marshal(in); string(after) != string(before) {
		t.Error("PortableSchema modified its input")
	}
	if PortableSchema(nil) != nil {
		t.Error("PortableSchema(nil) != nil")
	}
}
