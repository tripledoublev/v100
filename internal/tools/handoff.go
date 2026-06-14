package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
)

const (
	// HandoffSchemaStandard is the built-in structured sub-agent result schema.
	HandoffSchemaStandard = "standard"
)

var standardHandoffSchema = json.RawMessage(`{
	"type": "object",
	"required": ["status", "summary", "next_steps"],
	"properties": {
		"status": {"type": "string", "enum": ["ok", "blocked", "failed"]},
		"summary": {"type": "string"},
		"findings": {"type": "array", "items": {
			"type": "object",
			"required": ["summary"],
			"properties": {
				"severity": {"type": "string"},
				"summary": {"type": "string"},
				"details": {"type": "string"},
				"refs": {"type": "array", "items": {"type": "string"}}
			}
		}},
		"artifacts": {"type": "array", "items": {
			"type": "object",
			"required": ["path"],
			"properties": {
				"path": {"type": "string"},
				"kind": {"type": "string"},
				"description": {"type": "string"}
			}
		}},
		"changed_files": {"type": "array", "items": {"type": "string"}},
		"citations": {"type": "array", "items": {
			"type": "object",
			"properties": {
				"source": {"type": "string"},
				"ref": {"type": "string"},
				"description": {"type": "string"}
			}
		}},
		"errors": {"type": "array", "items": {"type": "string"}},
		"blockers": {"type": "array", "items": {"type": "string"}},
		"warnings": {"type": "array", "items": {"type": "string"}},
		"private_notes": {"type": "array", "items": {"type": "string"}},
		"next_steps": {"type": "array", "items": {"type": "string"}},
		"blackboard_updates": {"type": "array", "items": {
			"type": "object",
			"properties": {
				"key": {"type": "string"},
				"value": {"type": "string"}
			}
		}},
		"patch": {"type": "object", "properties": {
			"summary": {"type": "string"},
			"diff": {"type": "string"}
		}}
	}
}`)

// ResolveHandoffSchema returns the schema selected by a caller. Custom schemas
// take precedence over named schemas. An empty name and empty schema means the
// legacy markdown handoff contract should be used.
func ResolveHandoffSchema(name string, schema json.RawMessage) (json.RawMessage, string, error) {
	name = strings.TrimSpace(name)
	schema = normalizeRawJSON(schema)
	if len(schema) > 0 {
		if !json.Valid(schema) {
			return nil, "", fmt.Errorf("handoff_schema is not valid JSON")
		}
		var decoded map[string]any
		if err := json.Unmarshal(schema, &decoded); err != nil || len(decoded) == 0 {
			return nil, "", fmt.Errorf("handoff_schema must be a JSON object")
		}
		return append(json.RawMessage(nil), schema...), "custom", nil
	}
	if name == "" {
		return nil, "", nil
	}
	switch strings.ToLower(name) {
	case HandoffSchemaStandard, "default", "handoff", "handoff/v1":
		return append(json.RawMessage(nil), standardHandoffSchema...), HandoffSchemaStandard, nil
	default:
		return nil, "", fmt.Errorf("unknown handoff_schema_name %q (available: %s)", name, HandoffSchemaStandard)
	}
}

// HandoffSchemaPrompt returns compact instructions for schema-constrained child runs.
func HandoffSchemaPrompt(name string, schema json.RawMessage) string {
	var b strings.Builder
	b.WriteString("Return the final handoff as a single JSON object and no markdown.\n")
	if name != "" {
		b.WriteString("Schema name: ")
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("Schema:\n")
	b.WriteString(string(compactJSON(schema)))
	b.WriteString("\n\n")
	b.WriteString("Use these channel conventions when relevant: findings, artifacts, changed_files, citations, patch, warnings, private_notes, blackboard_updates, errors, blockers, next_steps.")
	return b.String()
}

// ValidateStructuredHandoff extracts a JSON object from model text and validates
// it against a resolved handoff schema.
func ValidateStructuredHandoff(text string, schema json.RawMessage) (json.RawMessage, []string) {
	raw, err := ExtractJSONObject(text)
	if err != nil {
		return nil, []string{err.Error()}
	}
	diagnostics := ValidateJSONSchema(raw, schema)
	if len(diagnostics) > 0 {
		return raw, diagnostics
	}
	return compactJSON(raw), nil
}

// ExtractJSONObject extracts a JSON object from raw model text. It accepts pure
// JSON, fenced ```json blocks, or the first balanced object in the text.
func ExtractJSONObject(text string) (json.RawMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("structured handoff is empty")
	}
	if raw, ok := parseJSONObject(text); ok {
		return raw, nil
	}
	if raw, ok := extractFencedJSONObject(text); ok {
		return raw, nil
	}
	if raw, ok := extractBalancedJSONObject(text); ok {
		return raw, nil
	}
	return nil, fmt.Errorf("structured handoff must contain a JSON object")
}

// ValidateJSONSchema implements the small JSON Schema subset needed by handoff
// contracts: type, required, properties, items, and enum.
func ValidateJSONSchema(raw json.RawMessage, schema json.RawMessage) []string {
	raw = normalizeRawJSON(raw)
	schema = normalizeRawJSON(schema)
	if len(schema) == 0 {
		return nil
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return []string{"invalid JSON result: " + err.Error()}
	}
	var spec map[string]any
	if err := json.Unmarshal(schema, &spec); err != nil {
		return []string{"invalid JSON schema: " + err.Error()}
	}
	var diagnostics []string
	validateJSONValue(data, spec, "$", &diagnostics)
	sort.Strings(diagnostics)
	return diagnostics
}

func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	return raw
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	raw = normalizeRawJSON(raw)
	if len(raw) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return json.RawMessage(buf.Bytes())
}

func parseJSONObject(text string) (json.RawMessage, bool) {
	raw := json.RawMessage(text)
	if !json.Valid(raw) {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	return compactJSON(raw), true
}

func extractFencedJSONObject(text string) (json.RawMessage, bool) {
	const fence = "```"
	remaining := text
	for {
		start := strings.Index(remaining, fence)
		if start < 0 {
			return nil, false
		}
		afterFence := remaining[start+len(fence):]
		lineEnd := strings.IndexByte(afterFence, '\n')
		if lineEnd < 0 {
			return nil, false
		}
		info := strings.TrimSpace(afterFence[:lineEnd])
		bodyStart := lineEnd + 1
		end := strings.Index(afterFence[bodyStart:], fence)
		if end < 0 {
			return nil, false
		}
		body := strings.TrimSpace(afterFence[bodyStart : bodyStart+end])
		if info == "" || strings.EqualFold(info, "json") || strings.Contains(strings.ToLower(info), "json") {
			if raw, ok := parseJSONObject(body); ok {
				return raw, true
			}
		}
		remaining = afterFence[bodyStart+end+len(fence):]
	}
}

func extractBalancedJSONObject(text string) (json.RawMessage, bool) {
	start := strings.IndexByte(text, '{')
	for start >= 0 {
		inString := false
		escaped := false
		depth := 0
		for i := start; i < len(text); i++ {
			ch := text[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					if raw, ok := parseJSONObject(text[start : i+1]); ok {
						return raw, true
					}
					next := strings.IndexByte(text[start+1:], '{')
					if next < 0 {
						return nil, false
					}
					start += next + 1
					i = start - 1
				}
			}
		}
		next := strings.IndexByte(text[start+1:], '{')
		if next < 0 {
			return nil, false
		}
		start += next + 1
	}
	return nil, false
}

func validateJSONValue(value any, schema map[string]any, path string, diagnostics *[]string) {
	if len(schema) == 0 {
		return
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		var matched bool
		for _, option := range enum {
			if reflect.DeepEqual(value, option) {
				matched = true
				break
			}
		}
		if !matched {
			*diagnostics = append(*diagnostics, fmt.Sprintf("%s must be one of %s", path, formatEnum(enum)))
		}
	}
	if typeName, ok := schemaType(schema); ok && !jsonTypeMatches(value, typeName) {
		*diagnostics = append(*diagnostics, fmt.Sprintf("%s must be %s", path, typeName))
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		validateJSONObject(typed, schema, path, diagnostics)
	case []any:
		validateJSONArray(typed, schema, path, diagnostics)
	}
}

func validateJSONObject(value map[string]any, schema map[string]any, path string, diagnostics *[]string) {
	for _, required := range schemaStringArray(schema["required"]) {
		if _, ok := value[required]; !ok {
			*diagnostics = append(*diagnostics, fmt.Sprintf("%s.%s is required", path, required))
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, rawProperty := range properties {
		propertySchema, _ := rawProperty.(map[string]any)
		if propertySchema == nil {
			continue
		}
		propertyValue, ok := value[name]
		if !ok {
			continue
		}
		validateJSONValue(propertyValue, propertySchema, path+"."+name, diagnostics)
	}
}

func validateJSONArray(value []any, schema map[string]any, path string, diagnostics *[]string) {
	itemSchema, _ := schema["items"].(map[string]any)
	if itemSchema == nil {
		return
	}
	for i, item := range value {
		validateJSONValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, i), diagnostics)
	}
}

func schemaType(schema map[string]any) (string, bool) {
	switch t := schema["type"].(type) {
	case string:
		return strings.TrimSpace(t), strings.TrimSpace(t) != ""
	case []any:
		if len(t) == 0 {
			return "", false
		}
		if s, ok := t[0].(string); ok {
			return strings.TrimSpace(s), strings.TrimSpace(s) != ""
		}
	}
	return "", false
}

func jsonTypeMatches(value any, typeName string) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && math.Trunc(n) == n
	case "null":
		return value == nil
	default:
		return true
	}
}

func schemaStringArray(raw any) []string {
	values, _ := raw.([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func formatEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		raw, _ := json.Marshal(value)
		parts = append(parts, string(raw))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
