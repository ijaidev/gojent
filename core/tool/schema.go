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
type SchemaOption func(*SchemaConfig)

// SchemaConfig controls schema generation parameters.
type SchemaConfig struct {
	// Strict disallows additional object properties and marks every field required.
	Strict bool
}

// WithStrictSchema configures the generator for strict JSON schema providers.
// It sets additionalProperties to false on objects and requires every field.
func WithStrictSchema(strict bool) SchemaOption {
	return func(cfg *SchemaConfig) {
		cfg.Strict = strict
	}
}

// GenerateSchema generates a JSON Schema map for a given Go type T.
// It is a best-effort wrapper around GenerateSchemaE.
func GenerateSchema[T any]() map[string]any {
	schema, _ := GenerateSchemaE[T]()
	if schema == nil {
		return map[string]any{}
	}
	return schema
}

// GenerateSchemaE generates a JSON Schema map for T and reports unsupported
// types instead of silently emitting an incorrect schema.
func GenerateSchemaE[T any](opts ...SchemaOption) (map[string]any, error) {
	var zero T
	return GenerateSchemaFromTypeE(reflect.TypeOf(zero), opts...)
}

// GenerateSchemaWithOptions generates a JSON Schema map for T with custom options.
// It is a best-effort wrapper around GenerateSchemaE.
func GenerateSchemaWithOptions[T any](opts ...SchemaOption) map[string]any {
	schema, _ := GenerateSchemaE[T](opts...)
	if schema == nil {
		return map[string]any{}
	}
	return schema
}

// GenerateSchemaFromType generates a JSON Schema map for a given reflect.Type.
// It is a best-effort wrapper around GenerateSchemaFromTypeE.
func GenerateSchemaFromType(t reflect.Type) map[string]any {
	return GenerateSchemaFromTypeWithOptions(t)
}

// GenerateSchemaFromTypeWithOptions generates a JSON Schema map for a given
// reflect.Type with custom options. It is a best-effort wrapper around
// GenerateSchemaFromTypeE.
func GenerateSchemaFromTypeWithOptions(t reflect.Type, opts ...SchemaOption) map[string]any {
	schema, _ := GenerateSchemaFromTypeE(t, opts...)
	if schema == nil {
		return map[string]any{}
	}
	return schema
}

// GenerateSchemaFromTypeE generates a JSON Schema map for a given reflect.Type
// and reports unsupported types.
func GenerateSchemaFromTypeE(t reflect.Type, opts ...SchemaOption) (map[string]any, error) {
	cfg := SchemaConfig{}
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
	cfg     SchemaConfig
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

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
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
		return g.structSchema(t)
	case reflect.Slice, reflect.Array:
		items, err := g.schema(t.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"type":  "array",
			"items": items,
		}, nil
	case reflect.Map:
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
	if g.visited[t] {
		return g.objectPlaceholder(), nil
	}
	g.visited[t] = true
	defer delete(g.visited, t)

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
		optional := forceOptional || tag.omitempty
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

type jsonFieldTag struct {
	name      string
	omitempty bool
	asString  bool
	skip      bool
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
		case "omitempty":
			tag.omitempty = true
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

func dateTimeSchema() map[string]any {
	return map[string]any{
		"type":   "string",
		"format": "date-time",
	}
}
