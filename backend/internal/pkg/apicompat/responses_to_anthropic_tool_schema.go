package apicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
)

// Only lower root unions whose validation semantics can be retained. Unioning
// each property independently loses correlations; allOf must remain conjunctive.
func flattenAnthropicRootUnions(schema map[string]json.RawMessage) error {
	budget := 128
	return flattenAnthropicRootUnionsBounded(schema, 0, &budget)
}

func flattenAnthropicRootUnionsBounded(schema map[string]json.RawMessage, depth int, budget *int) error {
	if depth > 32 || *budget <= 0 {
		return fmt.Errorf("root union exceeds conversion complexity limit")
	}
	*budget--
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		raw, present := schema[keyword]
		if !present {
			continue
		}
		var branches []map[string]json.RawMessage
		if json.Unmarshal(raw, &branches) != nil || len(branches) == 0 || len(branches) > *budget {
			return fmt.Errorf("invalid or oversized %s branches", keyword)
		}
		for _, branch := range branches {
			if branch == nil {
				return fmt.Errorf("non-object %s branch", keyword)
			}
			if err := flattenAnthropicRootUnionsBounded(branch, depth+1, budget); err != nil {
				return err
			}
			if err := validateAnthropicUnionObject(branch); err != nil {
				return err
			}
		}
		delete(schema, keyword)
		// Other root unions are processed separately as conjunctions.
		base := maps.Clone(schema)
		delete(base, "allOf")
		delete(base, "anyOf")
		delete(base, "oneOf")
		if err := validateAnthropicUnionObject(base); err != nil {
			return err
		}
		if keyword != "allOf" {
			union, err := combineAnthropicAlternatives(branches, keyword)
			if err != nil {
				return err
			}
			branches = []map[string]json.RawMessage{union}
		}
		combined, err := combineAnthropicConjunction(append([]map[string]json.RawMessage{base}, branches...))
		if err != nil {
			return err
		}
		for _, field := range []string{"properties", "required", "additionalProperties"} {
			if value, ok := combined[field]; ok {
				schema[field] = value
			}
		}
	}
	return nil
}

// Unhandled object-wide constraints must not disappear with their branch.
// References may point into removed branches, so do not relocate them silently.
func validateAnthropicUnionObject(schema map[string]json.RawMessage) error {
	for key, raw := range schema {
		switch key {
		case "type":
			if string(raw) != `"object"` {
				return fmt.Errorf("non-object root union branch")
			}
		case "properties":
			var properties map[string]json.RawMessage
			if json.Unmarshal(raw, &properties) != nil || properties == nil {
				return fmt.Errorf("invalid union properties")
			}
			if containsAnthropicSchemaReference(raw) {
				return fmt.Errorf("references in root union properties cannot be relocated safely")
			}
		case "required":
			var required []string
			if json.Unmarshal(raw, &required) != nil {
				return fmt.Errorf("invalid union required fields")
			}
		case "additionalProperties":
			if string(raw) != "true" && string(raw) != "false" {
				return fmt.Errorf("schema-valued additionalProperties in root union is unsupported")
			}
		case "description", "title", "$comment", "default", "examples", "deprecated", "readOnly", "writeOnly":
		default:
			return fmt.Errorf("root union constraint %q cannot be lowered safely", key)
		}
	}
	return nil
}

func containsAnthropicSchemaReference(raw json.RawMessage) bool {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(v any) bool {
		switch v := v.(type) {
		case map[string]any:
			if _, exists := v["$ref"]; exists {
				return true
			}
			for _, child := range v {
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func anthropicSchemaProperties(schema map[string]json.RawMessage) map[string]json.RawMessage {
	properties := make(map[string]json.RawMessage)
	_ = json.Unmarshal(schema["properties"], &properties)
	if properties == nil {
		properties = make(map[string]json.RawMessage)
	}
	return properties
}

func anthropicBranchRequired(schema map[string]json.RawMessage) []string {
	var required []string
	_ = json.Unmarshal(schema["required"], &required)
	return required
}

func combineAnthropicConjunction(branches []map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	properties := make(map[string]json.RawMessage)
	var required []string
	closed := false
	for _, branch := range branches {
		for key, value := range anthropicSchemaProperties(branch) {
			if previous, exists := properties[key]; exists && !anthropicSchemaJSONEqual(previous, value) {
				value, _ = json.Marshal(map[string][]json.RawMessage{"allOf": {previous, value}})
			}
			properties[key] = value
		}
		for _, key := range anthropicBranchRequired(branch) {
			if !containsAnthropicName(required, key) {
				required = append(required, key)
			}
		}
	}
	for _, branch := range branches {
		if string(branch["additionalProperties"]) != "false" {
			continue
		}
		closed = true
		allowed := anthropicSchemaProperties(branch)
		for name := range properties {
			if _, ok := allowed[name]; !ok {
				return nil, fmt.Errorf("root union has incompatible closed object branches")
			}
		}
	}
	out := map[string]json.RawMessage{}
	out["properties"], _ = json.Marshal(properties)
	if len(required) > 0 {
		out["required"], _ = json.Marshal(required)
	}
	if closed {
		out["additionalProperties"] = json.RawMessage("false")
	}
	return out, nil
}

// Alternatives can move to one property only when all other constraints agree.
// oneOf additionally requires that property: absent values would otherwise
// match multiple root branches but bypass a nested oneOf.
func combineAnthropicAlternatives(branches []map[string]json.RawMessage, keyword string) (map[string]json.RawMessage, error) {
	if len(branches) == 1 {
		return branches[0], nil
	}
	first := branches[0]
	properties := anthropicSchemaProperties(first)
	required := anthropicBranchRequired(first)
	varying := ""
	for _, branch := range branches[1:] {
		otherRequired := anthropicBranchRequired(branch)
		if len(required) != len(otherRequired) || (string(first["additionalProperties"]) == "false") != (string(branch["additionalProperties"]) == "false") {
			return nil, fmt.Errorf("%s has correlated object constraints", keyword)
		}
		for _, name := range required {
			if !containsAnthropicName(otherRequired, name) {
				return nil, fmt.Errorf("%s has conditional required fields", keyword)
			}
		}
		other := anthropicSchemaProperties(branch)
		if len(other) != len(properties) {
			return nil, fmt.Errorf("%s has different property sets", keyword)
		}
		for name, value := range properties {
			candidate, exists := other[name]
			if !exists {
				return nil, fmt.Errorf("%s has different property sets", keyword)
			}
			if !anthropicSchemaJSONEqual(value, candidate) {
				if varying != "" && varying != name {
					return nil, fmt.Errorf("%s has correlated property constraints", keyword)
				}
				varying = name
			}
		}
	}
	if varying == "" {
		if keyword == "oneOf" {
			return nil, fmt.Errorf("oneOf contains identical alternatives")
		}
		return first, nil
	}
	if keyword == "oneOf" && !containsAnthropicName(required, varying) {
		return nil, fmt.Errorf("oneOf discriminator must be required")
	}
	var alternatives []json.RawMessage
	for _, branch := range branches {
		alternatives = append(alternatives, anthropicSchemaProperties(branch)[varying])
	}
	properties[varying], _ = json.Marshal(map[string][]json.RawMessage{keyword: alternatives})
	out := maps.Clone(first)
	out["properties"], _ = json.Marshal(properties)
	return out, nil
}

func containsAnthropicName(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func anthropicSchemaJSONEqual(left, right json.RawMessage) bool {
	if bytes.Equal(left, right) {
		return true
	}
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	a, aErr := decode(left)
	b, bErr := decode(right)
	return aErr == nil && bErr == nil && reflect.DeepEqual(a, b)
}
