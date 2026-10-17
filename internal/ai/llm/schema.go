package llm

// valueConstraints are JSON Schema keywords that limit values rather than shape. Providers'
// native structured output often rejects them (Anthropic: numeric, string-length and complex
// array constraints; OpenAI strict mode: several of them), so adapters send the schema without
// them. The caller still validates every answer strictly against the full schema.
var valueConstraints = []string{
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern",
	"minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties",
}

// PortableSchema returns a deep copy of schema without value constraints. The input is not
// modified.
func PortableSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out, _ := portable(schema).(map[string]any)
	return out
}

func portable(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = portable(val)
		}
		for _, k := range valueConstraints {
			// Only drop the keyword itself: a property that happens to be named "pattern" lives
			// inside "properties", where its value is a schema (a map), so it is kept.
			if _, isSchema := out[k].(map[string]any); !isSchema {
				delete(out, k)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = portable(val)
		}
		return out
	default:
		return v
	}
}
