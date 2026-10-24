package tool

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// SchemaOption configures schema generation options.
type SchemaOption func(*schemaConfig)

// schemaConfig controls schema generation parameters.
type schemaConfig struct {
	// Strict disallows additional object properties and marks every field required.
	Strict bool
}

// WithStrictSchema configures the generator for strict JSON schema providers.
// It sets additionalProperties to false on objects and requires every field.
func WithStrictSchema(strict bool) SchemaOption {
	return func(cfg *schemaConfig) {
		cfg.Strict = strict
	}
}

// GenerateSchema generates a JSON Schema map for T and reports unsupported
// types instead of silently emitting an incorrect schema.
func GenerateSchema[T any](opts ...SchemaOption) (map[string]any, error) {
	var zero T
	return GenerateSchemaFromType(reflect.TypeOf(zero), opts...)
}

// GenerateSchemaFromType generates a JSON Schema map for a given reflect.Type
// and reports unsupported types.
func GenerateSchemaFromType(t reflect.Type, opts ...SchemaOption) (map[string]any, error) {
	cfg := schemaConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	generator := schemaGenerator{
		cfg:     cfg,
		visited: make(map[reflect.Type]bool),
	}
	return generator.schema(t)
}

type schemaGenerator struct {
	cfg     schemaConfig
	visited map[reflect.Type]bool
}

var (
	timeType          = reflect.TypeFor[time.Time]()
	rawMessageType    = reflect.TypeFor[json.RawMessage]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

func (g *schemaGenerator) schema(t reflect.Type) (map[string]any, error) {
	if t == nil {
		return map[string]any{}, nil
	}

	// Defined pointer types can cycle (for example `type P *P`). Reject the
	// cycle instead of looping forever while unwrapping pointers.
	if t.Kind() == reflect.Pointer {
		seen := make(map[reflect.Type]bool)
		for t.Kind() == reflect.Pointer {
			if seen[t] {
				return nil, fmt.Errorf("unsupported recursive pointer type %s", t)
			}
			seen[t] = true
			t = t.Elem()
		}
	}

	if isTimeType(t) {
		return dateTimeSchema(), nil
	}
	if t == rawMessageType {
		return map[string]any{}, nil
	}
	if implementsJSONMarshaler(t) {
		// A custom marshaler can emit any JSON shape. An empty schema is
		// permissive rather than confidently wrong.
		return map[string]any{}, nil
	}
	if implementsTextMarshaler(t) {
		return map[string]any{"type": "string"}, nil
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return map[string]any{
			"type":            "string",
			"contentEncoding": "base64",
		}, nil
	}

	switch t.Kind() {
	case reflect.Struct:
		if g.visited[t] {
			return g.objectPlaceholder(), nil
		}
		g.visited[t] = true
		defer delete(g.visited, t)
		return g.structSchema(t)
	case reflect.Slice, reflect.Array:
		if g.visited[t] {
			return g.containerPlaceholder(t), nil
		}
		g.visited[t] = true
		defer delete(g.visited, t)
		items, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"type":  "array",
			"items": items,
		}, nil
	case reflect.Map:
		if !supportsMapKey(t.Key()) {
			return nil, fmt.Errorf("unsupported map key type %s", t.Key())
		}
		if g.visited[t] {
			return g.containerPlaceholder(t), nil
		}
		g.visited[t] = true
		defer delete(g.visited, t)
		values, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"type":                 "object",
			"additionalProperties": values,
		}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Interface:
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", t)
	}
}

func (g *schemaGenerator) structSchema(t reflect.Type) (map[string]any, error) {
	properties := make(map[string]any)
	var required []string
	requiredSet := make(map[string]bool)
	if err := g.addFields(t, properties, &required, requiredSet, false); err != nil {
		return nil, err
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	if g.cfg.Strict {
		schema["additionalProperties"] = false
	}
	return schema, nil
}

func (g *schemaGenerator) addFields(t reflect.Type, properties map[string]any, required *[]string, requiredSet map[string]bool, forceOptional bool) error {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		tag := parseJSONFieldTag(field.Tag.Get("json"))
		if tag.skip {
			continue
		}

		fieldType := field.Type
		optional := forceOptional || tag.optional
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
			optional = true
		}

		// encoding/json flattens anonymous struct fields unless the field has
		// an explicit JSON name.
		if field.Anonymous && tag.name == "" && isFlattenableStruct(fieldType) {
			if g.visited[fieldType] {
				continue
			}
			g.visited[fieldType] = true
			err := g.addFields(fieldType, properties, required, requiredSet, optional)
			delete(g.visited, fieldType)
			if err != nil {
				return err
			}
			continue
		}

		name := tag.name
		if name == "" {
			name = field.Name
		}

		property, err := g.schema(fieldType)
		if err != nil {
			return fmt.Errorf("field %s: %w", field.Name, err)
		}

		if tag.asString && isScalarKind(fieldType.Kind()) {
			property["type"] = "string"
			delete(property, "contentEncoding")
			delete(property, "format")
		}

		if description := fieldDescription(field); description != "" {
			property["description"] = description
		}

		if enumTag := field.Tag.Get("enum"); enumTag != "" {
			values, err := enumValues(fieldType, enumTag, tag.asString)
			if err != nil {
				return fmt.Errorf("field %s: %w", field.Name, err)
			}
			property["enum"] = values
		}

		properties[name] = property
		if (!optional || g.cfg.Strict) && !requiredSet[name] {
			*required = append(*required, name)
			requiredSet[name] = true
		}
	}
	return nil
}

func (g *schemaGenerator) objectPlaceholder() map[string]any {
	placeholder := map[string]any{"type": "object"}
	if g.cfg.Strict {
		placeholder["additionalProperties"] = false
	}
	return placeholder
}

// containerPlaceholder terminates self-referential slice and map schemas.
// There is no finite strict schema for an infinitely nested container, so the
// placeholder describes only the immediate shape.
func (g *schemaGenerator) containerPlaceholder(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": map[string]any{}}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": map[string]any{}}
	default:
		return g.objectPlaceholder()
	}
}

type jsonFieldTag struct {
	name     string
	optional bool
	asString bool
	skip     bool
}

func parseJSONFieldTag(raw string) jsonFieldTag {
	if raw == "-" {
		return jsonFieldTag{skip: true}
	}
	if raw == "" {
		return jsonFieldTag{}
	}

	parts := strings.Split(raw, ",")
	tag := jsonFieldTag{name: parts[0]}
	for _, option := range parts[1:] {
		switch option {
		case "omitempty", "omitzero":
			tag.optional = true
		case "string":
			tag.asString = true
		}
	}
	return tag
}

func fieldDescription(field reflect.StructField) string {
	if description := field.Tag.Get("description"); description != "" {
		return description
	}
	return field.Tag.Get("doc")
}

func enumValues(t reflect.Type, raw string, asString bool) ([]any, error) {
	parts := strings.Split(raw, ",")
	values := make([]any, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" {
			return nil, fmt.Errorf("empty enum value")
		}
		value, err := parseEnumValue(t, text)
		if err != nil {
			return nil, err
		}
		if asString {
			value = fmt.Sprint(value)
		}
		values = append(values, value)
	}
	return values, nil
}

func parseEnumValue(t reflect.Type, value string) (any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return value, nil
	case reflect.Bool:
		return strconv.ParseBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.ParseInt(value, 10, t.Bits())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.ParseUint(value, 10, t.Bits())
	case reflect.Float32, reflect.Float64:
		return strconv.ParseFloat(value, t.Bits())
	default:
		return nil, fmt.Errorf("enum is not supported for %s", t)
	}
}

func isScalarKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func isTimeType(t reflect.Type) bool {
	return t == timeType || (t.Kind() == reflect.Struct && t.ConvertibleTo(timeType))
}

func isFlattenableStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct &&
		!isTimeType(t) &&
		!implementsJSONMarshaler(t) &&
		!implementsTextMarshaler(t)
}

func implementsTextMarshaler(t reflect.Type) bool {
	return t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)
}

// supportsMapKey mirrors encoding/json's map key rules: strings, integers, and
// types with a value-receiver MarshalText method.
func supportsMapKey(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return t.Implements(textMarshalerType)
}

func dateTimeSchema() map[string]any {
	return map[string]any{
		"type":   "string",
		"format": "date-time",
	}
}

func normalizeSchema(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, fmt.Errorf("schema must be a JSON object")
	}
	if _, err := json.Marshal(value); err != nil {
		return nil, err
	}
	return cloneSchemaMap(value), nil
}

func cloneSchemaMap(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		switch value := v.(type) {
		case map[string]any:
			out[k] = cloneSchemaMap(value)
		case []any:
			items := make([]any, len(value))
			for i, item := range value {
				if nested, ok := item.(map[string]any); ok {
					items[i] = cloneSchemaMap(nested)
				} else {
					items[i] = item
				}
			}
			out[k] = items
		case []string:
			out[k] = append([]string(nil), value...)
		default:
			out[k] = v
		}
	}
	return out
}
