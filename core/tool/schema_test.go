package tool_test

import (
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

func TestGenerateSchemaWithOptions_NestedAndStrict(t *testing.T) {
	schema := tool.GenerateSchemaWithOptions[ComplexPost](tool.WithStrictSchema(true))

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

	// 1. Enum verification
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

	// 2. Nested struct author
	authorProp, ok := props["author"].(map[string]any)
	if !ok {
		t.Fatalf("missing author property")
	}
	if authorProp["type"] != "object" {
		t.Errorf("expected author type object, got %v", authorProp["type"])
	}
	authorProps, ok := authorProp["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing author child properties")
	}
	if _, ok := authorProps["name"]; !ok {
		t.Errorf("missing author name property")
	}

	// 3. Slice of structs co_authors
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

	// 4. time.Time format
	createdProp, ok := props["created_at"].(map[string]any)
	if !ok {
		t.Fatalf("missing created_at property")
	}
	if createdProp["type"] != "string" || createdProp["format"] != "date-time" {
		t.Errorf("expected string/date-time for time.Time, got %+v", createdProp)
	}
}

func TestGenerateSchema_RecursiveSafety(t *testing.T) {
	// Must not stack overflow or infinite loop on recursive structs
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
