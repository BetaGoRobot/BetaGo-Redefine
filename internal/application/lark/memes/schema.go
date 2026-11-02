package memes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// validateStageSchema enforces the JSON Schema constructs used by our stage contracts.
// In particular, Go's JSON decoder alone accepts null for strings, structs and containers.
func validateStageSchema(raw, schemaJSON []byte) error {
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	return validateSchemaValue(value, schema, "$")
}

func validateSchemaValue(value any, schema map[string]any, path string) error {
	typeName, _ := schema["type"].(string)
	wrongType := func() error { return fmt.Errorf("%s must be %s, got %T", path, typeName, value) }
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return wrongType()
		}
		required, _ := schema["required"].([]any)
		for _, entry := range required {
			key, _ := entry.(string)
			if _, exists := object[key]; !exists {
				return fmt.Errorf("missing required field %s.%s", path, key)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if property, exists := properties[key]; exists {
				if err := validateSchemaValue(object[key], property.(map[string]any), path+"."+key); err != nil {
					return err
				}
				continue
			}
			switch additional := schema["additionalProperties"].(type) {
			case bool:
				if !additional {
					return fmt.Errorf("unknown field %s.%s", path, key)
				}
			case map[string]any:
				if err := validateSchemaValue(object[key], additional, path+"."+key); err != nil {
					return err
				}
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return wrongType()
		}
		items, _ := schema["items"].(map[string]any)
		for i, entry := range array {
			if err := validateSchemaValue(entry, items, path+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return wrongType()
		}
	case "integer", "number":
		number, ok := value.(json.Number)
		if !ok {
			return wrongType()
		}
		if typeName == "integer" {
			if _, err := number.Int64(); err != nil {
				return wrongType()
			}
		}
		actual, err := number.Float64()
		if err != nil {
			return wrongType()
		}
		if minimum, ok := schema["minimum"].(float64); ok && actual < minimum {
			return fmt.Errorf("%s is below minimum %v", path, minimum)
		}
		if maximum, ok := schema["maximum"].(float64); ok && actual > maximum {
			return fmt.Errorf("%s exceeds maximum %v", path, maximum)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return wrongType()
		}
	default:
		return fmt.Errorf("unsupported stage schema type %q at %s", typeName, path)
	}
	if allowed, ok := schema["enum"].([]any); ok {
		actual, err := Fingerprint(value)
		if err != nil {
			return err
		}
		for _, option := range allowed {
			expected, err := Fingerprint(option)
			if err != nil {
				return err
			}
			if actual == expected {
				return nil
			}
		}
		return fmt.Errorf("%s is outside allowed values", path)
	}
	return nil
}
