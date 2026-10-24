package tool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Validate checks input against the dependency-free JSON Schema subset used by
// generated tool schemas.
//
// Supported keywords: type, properties, required, additionalProperties,
// items, enum, const, minimum, maximum, exclusiveMinimum, exclusiveMaximum,
// minLength, maxLength, pattern, minItems, maxItems, anyOf, oneOf, and allOf.
// Unknown keywords are ignored so provider-specific annotations (description,
// title, default, format) do not reject valid input.
func Validate(schema map[string]any, input []byte) error {
	if len(schema) == 0 {
		return nil
	}
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		trimmed = "{}"
	}

	var value any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: malformed JSON: %w", ErrInvalidInput, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: unexpected data after the JSON value", ErrInvalidInput)
		}
		return fmt.Errorf("%w: malformed JSON: %w", ErrInvalidInput, err)
	}
	return validateValue(schema, value, "$")
}

func validateValue(schema map[string]any, value any, path string) error {
	if schema == nil {
		return nil
	}

	if rawType, ok := schema["type"]; ok {
		if err := validateType(rawType, value, path); err != nil {
			return err
		}
	}
	if enum, ok := schema["enum"]; ok {
		if !enumContains(enum, value) {
			return invalid(path, "value is not one of the allowed enum values")
		}
	}
	if constant, ok := schema["const"]; ok {
		if !jsonEqual(constant, value) {
			return invalid(path, "value does not match const")
		}
	}

	if object, ok := value.(map[string]any); ok {
		if err := validateObject(schema, object, path); err != nil {
			return err
		}
	}
	if array, ok := value.([]any); ok {
		if err := validateArray(schema, array, path); err != nil {
			return err
		}
	}
	if text, ok := value.(string); ok {
		if err := validateString(schema, text, path); err != nil {
			return err
		}
	}
	if number, ok := toFloat(value); ok {
		if err := validateNumber(schema, number, path); err != nil {
			return err
		}
	}

	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		raw, ok := schema[keyword]
		if !ok {
			continue
		}
		branches, ok := raw.([]any)
		if !ok {
			continue
		}
		matches := 0
		var lastErr error
		for _, branch := range branches {
			branchSchema, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			if err := validateValue(branchSchema, value, path); err != nil {
				lastErr = err
			} else {
				matches++
			}
		}
		switch keyword {
		case "allOf":
			if matches != len(branches) {
				return lastErr
			}
		case "anyOf":
			if matches == 0 {
				return invalid(path, "value does not match any allowed schema")
			}
		case "oneOf":
			if matches != 1 {
				return invalid(path, "value must match exactly one allowed schema")
			}
		}
	}

	return nil
}

func validateType(rawType, value any, path string) error {
	switch typed := rawType.(type) {
	case string:
		if !matchesType(typed, value) {
			return invalid(path, "expected type %s", typed)
		}
	case []any:
		for _, candidate := range typed {
			if name, ok := candidate.(string); ok && matchesType(name, value) {
				return nil
			}
		}
		return invalid(path, "value does not match any allowed type")
	case []string:
		for _, name := range typed {
			if matchesType(name, value) {
				return nil
			}
		}
		return invalid(path, "value does not match any allowed type")
	default:
		return nil
	}
	return nil
}

func matchesType(name string, value any) bool {
	switch name {
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
		_, ok := toFloat(value)
		return ok
	case "integer":
		number, ok := toFloat(value)
		return ok && math.Trunc(number) == number
	case "null":
		return value == nil
	default:
		return true
	}
}

func validateObject(schema, object map[string]any, path string) error {
	if required, ok := schema["required"]; ok {
		for _, name := range stringList(required) {
			if _, exists := object[name]; !exists {
				return invalid(path, "missing required property %q", name)
			}
		}
	}

	properties, _ := schema["properties"].(map[string]any)
	for name, rawProperty := range properties {
		property, ok := rawProperty.(map[string]any)
		if !ok {
			continue
		}
		value, exists := object[name]
		if !exists {
			continue
		}
		if err := validateValue(property, value, path+"."+name); err != nil {
			return err
		}
	}

	if additional, ok := schema["additionalProperties"]; ok {
		switch typed := additional.(type) {
		case bool:
			if !typed {
				for name := range object {
					if properties == nil {
						return invalid(path, "additional property %q is not allowed", name)
					}
					if _, declared := properties[name]; !declared {
						return invalid(path, "additional property %q is not allowed", name)
					}
				}
			}
		case map[string]any:
			for name, value := range object {
				if properties != nil {
					if _, declared := properties[name]; declared {
						continue
					}
				}
				if err := validateValue(typed, value, path+"."+name); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func validateArray(schema map[string]any, array []any, path string) error {
	if minimum, ok := integerKeyword(schema, "minItems"); ok && len(array) < minimum {
		return invalid(path, "must contain at least %d items", minimum)
	}
	if maximum, ok := integerKeyword(schema, "maxItems"); ok && len(array) > maximum {
		return invalid(path, "must contain at most %d items", maximum)
	}
	items, ok := schema["items"].(map[string]any)
	if !ok {
		return nil
	}
	for i, item := range array {
		if err := validateValue(items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func validateString(schema map[string]any, value, path string) error {
	length := utf8.RuneCountInString(value)
	if minimum, ok := integerKeyword(schema, "minLength"); ok && length < minimum {
		return invalid(path, "must be at least %d characters", minimum)
	}
	if maximum, ok := integerKeyword(schema, "maxLength"); ok && length > maximum {
		return invalid(path, "must be at most %d characters", maximum)
	}
	if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
		// An invalid pattern means the schema itself is malformed. Ignoring it
		// is safer than rejecting otherwise valid input at runtime.
		if compiled, err := regexp.Compile(pattern); err == nil && !compiled.MatchString(value) {
			return invalid(path, "does not match pattern %q", pattern)
		}
	}
	return nil
}

func validateNumber(schema map[string]any, value float64, path string) error {
	if minimum, ok := numberKeyword(schema, "minimum"); ok && value < minimum {
		return invalid(path, "must be >= %v", minimum)
	}
	if maximum, ok := numberKeyword(schema, "maximum"); ok && value > maximum {
		return invalid(path, "must be <= %v", maximum)
	}
	if minimum, ok := numberKeyword(schema, "exclusiveMinimum"); ok && value <= minimum {
		return invalid(path, "must be > %v", minimum)
	}
	if maximum, ok := numberKeyword(schema, "exclusiveMaximum"); ok && value >= maximum {
		return invalid(path, "must be < %v", maximum)
	}
	return nil
}

func integerKeyword(schema map[string]any, key string) (int, bool) {
	value, ok := schema[key]
	if !ok {
		return 0, false
	}
	number, ok := toFloat(value)
	if !ok {
		return 0, false
	}
	return int(number), true
}

func numberKeyword(schema map[string]any, key string) (float64, bool) {
	value, ok := schema[key]
	if !ok {
		return 0, false
	}
	return toFloat(value)
}

func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if name, ok := item.(string); ok {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func enumContains(raw, value any) bool {
	switch values := raw.(type) {
	case []any:
		for _, candidate := range values {
			if jsonEqual(candidate, value) {
				return true
			}
		}
		return false
	case []string:
		text, ok := value.(string)
		if !ok {
			return false
		}
		for _, candidate := range values {
			if candidate == text {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func jsonEqual(a, b any) bool {
	// Compare numeric values numerically so 1 and 1.0 are equal, as JSON
	// Schema requires.
	if left, ok := toFloat(a); ok {
		right, ok := toFloat(b)
		return ok && left == right
	}
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

func invalid(path, format string, args ...any) error {
	if path == "" {
		path = "$"
	}
	return fmt.Errorf("%w: %s: %s", ErrInvalidInput, path, fmt.Sprintf(format, args...))
}

// IsValidationError reports whether err came from schema validation.
func IsValidationError(err error) bool {
	return errors.Is(err, ErrInvalidInput)
}
