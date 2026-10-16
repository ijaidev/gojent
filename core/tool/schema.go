// Package tool provides interfaces and constructors for creating modular,
// LLM-callable tools with automatic schema generation and typed execution.
package tool

import (
	"reflect"
	"strings"
)

// GenerateSchema generates a JSON Schema map for a given Go type T.
func GenerateSchema[T any]() map[string]any {
	var zero T
	return GenerateSchemaFromType(reflect.TypeOf(zero))
}

// GenerateSchemaFromType generates a JSON Schema map for a given reflect.Type.
func GenerateSchemaFromType(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return map[string]any{
			"type": mapKindToJSONType(t.Kind()),
		}
	}

	properties := make(map[string]any)
	var required []string

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		fieldName := field.Name
		isOptional := false

		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			if parts[0] != "" {
				fieldName = parts[0]
			}
			for _, part := range parts[1:] {
				if part == "omitempty" {
					isOptional = true
					break
				}
			}
		}

		fieldProp := make(map[string]any)
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
			isOptional = true
		}

		fieldProp["type"] = mapKindToJSONType(fieldType.Kind())

		if fieldType.Kind() == reflect.Slice || fieldType.Kind() == reflect.Array {
			elemType := fieldType.Elem()
			for elemType.Kind() == reflect.Pointer {
				elemType = elemType.Elem()
			}
			fieldProp["items"] = map[string]any{
				"type": mapKindToJSONType(elemType.Kind()),
			}
		}

		desc := field.Tag.Get("description")
		if desc == "" {
			desc = field.Tag.Get("doc")
		}
		if desc != "" {
			fieldProp["description"] = desc
		}

		properties[fieldName] = fieldProp
		if !isOptional {
			required = append(required, fieldName)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	return schema
}

func mapKindToJSONType(kind reflect.Kind) string {
	switch kind {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Struct, reflect.Map:
		return "object"
	default:
		return "string"
	}
}
