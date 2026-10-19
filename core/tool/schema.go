// Package tool provides interfaces and constructors for creating modular,
// LLM-callable tools with automatic schema generation and typed execution.
package tool

import (
	"reflect"
	"strings"
)

// SchemaOption configures schema generation options.
type SchemaOption func(*SchemaConfig)

// SchemaConfig controls schema generation parameters.
type SchemaConfig struct {
	Strict bool
}

// WithStrictSchema configures the generator to disallow additional properties on object schemas.
func WithStrictSchema(strict bool) SchemaOption {
	return func(cfg *SchemaConfig) {
		cfg.Strict = strict
	}
}

// GenerateSchema generates a JSON Schema map for a given Go type T.
func GenerateSchema[T any]() map[string]any {
	var zero T
	return GenerateSchemaFromType(reflect.TypeOf(zero))
}

// GenerateSchemaWithOptions generates a JSON Schema map for a given Go type T with custom options.
func GenerateSchemaWithOptions[T any](opts ...SchemaOption) map[string]any {
	var zero T
	return GenerateSchemaFromTypeWithOptions(reflect.TypeOf(zero), opts...)
}

// GenerateSchemaFromType generates a JSON Schema map for a given reflect.Type.
func GenerateSchemaFromType(t reflect.Type) map[string]any {
	return GenerateSchemaFromTypeWithOptions(t)
}

// GenerateSchemaFromTypeWithOptions generates a JSON Schema map for a given reflect.Type with custom options.
func GenerateSchemaFromTypeWithOptions(t reflect.Type, opts ...SchemaOption) map[string]any {
	cfg := &SchemaConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	visited := make(map[reflect.Type]bool)
	return generateSchemaFromTypeWithVisited(t, visited, cfg.Strict)
}

func generateSchemaFromTypeWithVisited(t reflect.Type, visited map[reflect.Type]bool, strict bool) map[string]any {
	if t == nil {
		s := map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		if strict {
			s["additionalProperties"] = false
		}
		return s
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Handle time.Time
	if t.PkgPath() == "time" && t.Name() == "Time" {
		return map[string]any{
			"type":   "string",
			"format": "date-time",
		}
	}

	if t.Kind() != reflect.Struct {
		return map[string]any{
			"type": mapKindToJSONType(t.Kind()),
		}
	}

	if visited[t] {
		return map[string]any{
			"type": "object",
		}
	}
	visited[t] = true
	defer delete(visited, t)

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

		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
			isOptional = true
		}

		var fieldProp map[string]any

		switch {
		case fieldType.PkgPath() == "time" && fieldType.Name() == "Time":
			fieldProp = map[string]any{
				"type":   "string",
				"format": "date-time",
			}
		case fieldType.Kind() == reflect.Struct:
			fieldProp = generateSchemaFromTypeWithVisited(fieldType, visited, strict)
		case fieldType.Kind() == reflect.Slice || fieldType.Kind() == reflect.Array:
			elemType := fieldType.Elem()
			for elemType.Kind() == reflect.Pointer {
				elemType = elemType.Elem()
			}
			var itemsSchema map[string]any
			switch {
			case elemType.PkgPath() == "time" && elemType.Name() == "Time":
				itemsSchema = map[string]any{
					"type":   "string",
					"format": "date-time",
				}
			case elemType.Kind() == reflect.Struct:
				itemsSchema = generateSchemaFromTypeWithVisited(elemType, visited, strict)
			default:
				itemsSchema = map[string]any{
					"type": mapKindToJSONType(elemType.Kind()),
				}
			}
			fieldProp = map[string]any{
				"type":  "array",
				"items": itemsSchema,
			}
		default:
			fieldProp = map[string]any{
				"type": mapKindToJSONType(fieldType.Kind()),
			}
		}

		desc := field.Tag.Get("description")
		if desc == "" {
			desc = field.Tag.Get("doc")
		}
		if desc != "" {
			fieldProp["description"] = desc
		}

		enumTag := field.Tag.Get("enum")
		if enumTag != "" {
			parts := strings.Split(enumTag, ",")
			var enumVals []any
			for _, p := range parts {
				enumVals = append(enumVals, strings.TrimSpace(p))
			}
			fieldProp["enum"] = enumVals
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
	if strict {
		schema["additionalProperties"] = false
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
