package tool_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/tool"
)

type NestedAuthor struct {
	Name  string `json:"name" description:"Author full name"`
	Email string `json:"email,omitempty" description:"Author email address"`
}

type TagInfo struct {
	Label string `json:"label"`
}

type ComplexPost struct {
	Title     string         `json:"title" description:"Post title"`
	Status    string         `json:"status" enum:"draft,published,archived" description:"Publication status"`
	Author    NestedAuthor   `json:"author" description:"Primary author"`
	CoAuthors []NestedAuthor `json:"co_authors,omitempty" description:"Optional co-authors"`
	Tags      []TagInfo      `json:"tags" description:"List of tags"`
	Scores    []int          `json:"scores,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type RecursiveNode struct {
	Val  int            `json:"val"`
	Next *RecursiveNode `json:"next,omitempty"`
}

type EmbeddedID struct {
	ID string `json:"id"`
}

type CommonTypesInput struct {
	EmbeddedID
	Data     []byte          `json:"data"`
	Any      any             `json:"any"`
	Map      map[string]int  `json:"map"`
	Matrix   [][]string      `json:"matrix"`
	Count    int             `json:"count,string"`
	Optional string          `json:"optional,omitempty"`
	Raw      json.RawMessage `json:"raw"`
	When     time.Time       `json:"when"`
}

type NumericEnumInput struct {
	Code     int `json:"code" enum:"1,2,3"`
	TextCode int `json:"text_code,string" enum:"1,2"`
}

type EnumKindsInput struct {
	Bool        bool    `json:"bool" enum:"true,false"`
	BoolString  bool    `json:"bool_string,string" enum:"true,false"`
	Uint        uint    `json:"uint" enum:"1,2"`
	Float       float64 `json:"float" enum:"1.5,2.5"`
	FloatString float64 `json:"float_string,string" enum:"1.5,2.5"`
}

type InvalidEnumInput struct {
	Value struct{} `json:"value" enum:"a,b"`
}

type UnsupportedInput struct {
	Ch chan int `json:"ch"`
}

type customMarshaler struct{}

func (customMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`"custom"`), nil
}

type CustomMarshalerInput struct {
	Value customMarshaler `json:"value"`
}

type PointerEmbeddedInput struct {
	*EmbeddedID
	Name string `json:"name"`
}

type CustomTime time.Time

type NamedTimeInput struct {
	At CustomTime `json:"at"`
}

type StrictMapInput struct {
	Values map[string]int `json:"values"`
}

func TestGenerateSchemaWithOptions_NestedAndStrict(t *testing.T) {
	schema, err := tool.GenerateSchemaE[ComplexPost](tool.WithStrictSchema(true))
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}

	if schema["type"] != "object" {
		t.Fatalf("expected type object, got %v", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Errorf("expected additionalProperties to be false in strict mode, got %v", schema["additionalProperties"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}

	statusProp, ok := props["status"].(map[string]any)
	if !ok {
		t.Fatalf("missing status property")
	}
	enums, ok := statusProp["enum"].([]any)
	if !ok || len(enums) != 3 {
		t.Fatalf("expected 3 enum values for status, got %v", statusProp["enum"])
	}
	if enums[0] != "draft" || enums[1] != "published" || enums[2] != "archived" {
		t.Errorf("unexpected enum values: %v", enums)
	}

	authorProp, ok := props["author"].(map[string]any)
	if !ok {
		t.Fatalf("missing author property")
	}
	if authorProp["type"] != "object" {
		t.Errorf("expected author type object, got %v", authorProp["type"])
	}
	if authorProp["additionalProperties"] != false {
		t.Errorf("expected nested object additionalProperties false, got %v", authorProp["additionalProperties"])
	}
	authorProps, ok := authorProp["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing author child properties")
	}
	if _, ok := authorProps["name"]; !ok {
		t.Errorf("missing author name property")
	}

	coAuthorsProp, ok := props["co_authors"].(map[string]any)
	if !ok {
		t.Fatalf("missing co_authors property")
	}
	if coAuthorsProp["type"] != "array" {
		t.Errorf("expected co_authors type array, got %v", coAuthorsProp["type"])
	}
	items, ok := coAuthorsProp["items"].(map[string]any)
	if !ok {
		t.Fatalf("missing items in co_authors")
	}
	if items["type"] != "object" {
		t.Errorf("expected co_authors items type object, got %v", items["type"])
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok || itemProps["name"] == nil {
		t.Errorf("expected author properties inside items, got %v", items)
	}

	createdProp, ok := props["created_at"].(map[string]any)
	if !ok {
		t.Fatalf("missing created_at property")
	}
	if createdProp["type"] != "string" || createdProp["format"] != "date-time" {
		t.Errorf("expected string/date-time for time.Time, got %+v", createdProp)
	}

	required := schema["required"].([]string)
	if len(required) != 7 {
		t.Errorf("expected all 7 fields required in strict mode, got %v", required)
	}
}

func TestGenerateSchema_RecursiveSafety(t *testing.T) {
	schema := tool.GenerateSchema[RecursiveNode]()
	if schema["type"] != "object" {
		t.Fatalf("expected object, got %v", schema["type"])
	}
	props := schema["properties"].(map[string]any)
	nextProp := props["next"].(map[string]any)
	if nextProp["type"] != "object" {
		t.Errorf("expected next to be object, got %v", nextProp["type"])
	}
}

func TestGenerateSchema_RecursiveStrictPlaceholder(t *testing.T) {
	schema, err := tool.GenerateSchemaE[RecursiveNode](tool.WithStrictSchema(true))
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)
	next := props["next"].(map[string]any)
	if next["additionalProperties"] != false {
		t.Errorf("expected strict recursive placeholder, got %+v", next)
	}
}

func TestGenerateSchema_WrapperFunctions(t *testing.T) {
	if schema := tool.GenerateSchemaWithOptions[WeatherInput](tool.WithStrictSchema(true)); len(schema) == 0 {
		t.Error("expected GenerateSchemaWithOptions to return a schema")
	}
	inputType := reflect.TypeOf(WeatherInput{})
	if schema := tool.GenerateSchemaFromType(inputType); len(schema) == 0 {
		t.Error("expected GenerateSchemaFromType to return a schema")
	}
	if schema := tool.GenerateSchemaFromTypeWithOptions(inputType, tool.WithStrictSchema(true)); len(schema) == 0 {
		t.Error("expected GenerateSchemaFromTypeWithOptions to return a schema")
	}
}

func TestGenerateSchema_EnumScalarKinds(t *testing.T) {
	schema, err := tool.GenerateSchemaE[EnumKindsInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)

	boolValues := props["bool"].(map[string]any)["enum"].([]any)
	if len(boolValues) != 2 || boolValues[0] != true || boolValues[1] != false {
		t.Errorf("unexpected bool enum: %v", boolValues)
	}

	boolStringValues := props["bool_string"].(map[string]any)["enum"].([]any)
	if len(boolStringValues) != 2 || boolStringValues[0] != "true" || boolStringValues[1] != "false" {
		t.Errorf("unexpected bool string enum: %v", boolStringValues)
	}

	uintValues := props["uint"].(map[string]any)["enum"].([]any)
	if len(uintValues) != 2 || uintValues[0] != uint64(1) {
		t.Errorf("unexpected uint enum: %v", uintValues)
	}

	floatValues := props["float"].(map[string]any)["enum"].([]any)
	if len(floatValues) != 2 || floatValues[0] != 1.5 {
		t.Errorf("unexpected float enum: %v", floatValues)
	}

	floatStringValues := props["float_string"].(map[string]any)["enum"].([]any)
	if len(floatStringValues) != 2 || floatStringValues[0] != "1.5" || floatStringValues[1] != "2.5" {
		t.Errorf("unexpected float string enum: %v", floatStringValues)
	}
}

func TestGenerateSchema_CommonJSONTypes(t *testing.T) {
	schema, err := tool.GenerateSchemaE[CommonTypesInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}

	props := schema["properties"].(map[string]any)
	if _, ok := props["EmbeddedID"]; ok {
		t.Error("expected embedded struct fields to be flattened")
	}
	if _, ok := props["id"]; !ok {
		t.Error("expected flattened embedded id property")
	}

	dataProp := props["data"].(map[string]any)
	if dataProp["type"] != "string" || dataProp["contentEncoding"] != "base64" {
		t.Errorf("expected []byte to be a base64 string, got %+v", dataProp)
	}

	anyProp := props["any"].(map[string]any)
	if len(anyProp) != 0 {
		t.Errorf("expected any to be an empty schema, got %+v", anyProp)
	}

	mapProp := props["map"].(map[string]any)
	mapValues := mapProp["additionalProperties"].(map[string]any)
	if mapValues["type"] != "integer" {
		t.Errorf("expected map values to be integers, got %+v", mapValues)
	}

	matrixProp := props["matrix"].(map[string]any)
	matrixItems := matrixProp["items"].(map[string]any)
	if matrixItems["type"] != "array" {
		t.Errorf("expected nested array items, got %+v", matrixItems)
	}
	if matrixItems["items"].(map[string]any)["type"] != "string" {
		t.Errorf("expected nested string items, got %+v", matrixItems)
	}

	countProp := props["count"].(map[string]any)
	if countProp["type"] != "string" {
		t.Errorf("expected json:,string field to be a string, got %+v", countProp)
	}

	rawProp := props["raw"].(map[string]any)
	if len(rawProp) != 0 {
		t.Errorf("expected json.RawMessage to be permissive, got %+v", rawProp)
	}

	whenProp := props["when"].(map[string]any)
	if whenProp["type"] != "string" || whenProp["format"] != "date-time" {
		t.Errorf("expected time.Time date-time, got %+v", whenProp)
	}

	required := schema["required"].([]string)
	if len(required) != 8 {
		t.Fatalf("expected 8 required fields, got %v", required)
	}
	for _, name := range required {
		if name == "optional" {
			t.Errorf("optional field must not be required: %v", required)
		}
	}
}

func TestGenerateSchema_PointerEmbeddedFieldsAreOptional(t *testing.T) {
	schema, err := tool.GenerateSchemaE[PointerEmbeddedInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["id"]; !ok {
		t.Fatal("expected embedded pointer fields to be flattened")
	}
	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "name" {
		t.Errorf("expected only name to be required, got %v", required)
	}
}

func TestGenerateSchema_NamedTimeTypes(t *testing.T) {
	schema, err := tool.GenerateSchemaE[NamedTimeInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)
	at := props["at"].(map[string]any)
	if at["type"] != "string" || at["format"] != "date-time" {
		t.Errorf("expected named time type to use date-time, got %+v", at)
	}
}

func TestGenerateSchema_EnumValuesMatchFieldType(t *testing.T) {
	schema, err := tool.GenerateSchemaE[NumericEnumInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)

	code := props["code"].(map[string]any)
	codeEnums := code["enum"].([]any)
	if len(codeEnums) != 3 || codeEnums[0] != int64(1) {
		t.Errorf("expected numeric enum values, got %v", codeEnums)
	}

	textCode := props["text_code"].(map[string]any)
	if textCode["type"] != "string" {
		t.Errorf("expected string field for json:,string, got %+v", textCode)
	}
	textEnums := textCode["enum"].([]any)
	if len(textEnums) != 2 || textEnums[0] != "1" {
		t.Errorf("expected string enum values, got %v", textEnums)
	}
}

func TestGenerateSchema_CustomMarshalerIsPermissive(t *testing.T) {
	schema, err := tool.GenerateSchemaE[CustomMarshalerInput]()
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)
	value := props["value"].(map[string]any)
	if len(value) != 0 {
		t.Errorf("expected a permissive schema for a custom marshaler, got %+v", value)
	}
}

func TestGenerateSchema_ErrorsForUnsupportedTypes(t *testing.T) {
	if _, err := tool.GenerateSchemaE[UnsupportedInput](); err == nil {
		t.Error("expected an error for chan fields")
	}
	if _, err := tool.GenerateSchemaE[InvalidEnumInput](); err == nil {
		t.Error("expected an error for enum on an unsupported field type")
	}
	if schema := tool.GenerateSchema[UnsupportedInput](); len(schema) != 0 {
		t.Errorf("expected best-effort wrapper to return an empty schema, got %+v", schema)
	}
}

func TestGenerateSchema_StrictMapUsesValueSchema(t *testing.T) {
	schema, err := tool.GenerateSchemaE[StrictMapInput](tool.WithStrictSchema(true))
	if err != nil {
		t.Fatalf("GenerateSchemaE failed: %v", err)
	}
	props := schema["properties"].(map[string]any)
	values := props["values"].(map[string]any)
	additional := values["additionalProperties"].(map[string]any)
	if additional["type"] != "integer" {
		t.Errorf("expected strict map value schema, got %+v", values)
	}
}

func TestGenerateSchema_TopLevelAndNilTypes(t *testing.T) {
	bytesSchema, err := tool.GenerateSchemaE[[]byte]()
	if err != nil {
		t.Fatalf("GenerateSchemaE bytes failed: %v", err)
	}
	if bytesSchema["type"] != "string" || bytesSchema["contentEncoding"] != "base64" {
		t.Errorf("unexpected []byte schema: %+v", bytesSchema)
	}

	intsSchema, err := tool.GenerateSchemaE[[]int]()
	if err != nil {
		t.Fatalf("GenerateSchemaE ints failed: %v", err)
	}
	if intsSchema["type"] != "array" || intsSchema["items"].(map[string]any)["type"] != "integer" {
		t.Errorf("unexpected []int schema: %+v", intsSchema)
	}

	anySchema, err := tool.GenerateSchemaE[any]()
	if err != nil {
		t.Fatalf("GenerateSchemaE any failed: %v", err)
	}
	if len(anySchema) != 0 {
		t.Errorf("expected empty schema for any, got %+v", anySchema)
	}
}
