package tool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/tool"
)

type WeatherInput struct {
	Location string `json:"location" description:"The city and state, e.g. San Francisco, CA"`
	Units    string `json:"units,omitempty" description:"Measurement units: celsius or fahrenheit"`
}

type WeatherOutput struct {
	Temperature float64 `json:"temperature"`
	Condition   string  `json:"condition"`
}

func TestTool_Validation(t *testing.T) {
	// Missing ID
	_, err := tool.New(tool.Config[WeatherInput, WeatherOutput]{
		Execute: func(_ context.Context, _ WeatherInput) (WeatherOutput, error) {
			return WeatherOutput{}, nil
		},
	})
	if !errors.Is(err, tool.ErrIDRequired) {
		t.Fatalf("expected ErrIDRequired, got %v", err)
	}

	// Missing Execute
	_, err = tool.New(tool.Config[WeatherInput, WeatherOutput]{
		ID: "weather",
	})
	if !errors.Is(err, tool.ErrExecuteRequired) {
		t.Fatalf("expected ErrExecuteRequired, got %v", err)
	}

	// MustNew panics on error
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected MustNew to panic on validation error")
		}
	}()
	tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{})
}

func TestTool_SchemaGeneration(t *testing.T) {
	weatherTool, err := tool.New(tool.Config[WeatherInput, WeatherOutput]{
		ID:          "get_weather",
		Description: "Get the current weather for a location",
		Execute: func(_ context.Context, _ WeatherInput) (WeatherOutput, error) {
			return WeatherOutput{Temperature: 21.5, Condition: "Clear"}, nil
		},
	})
	if err != nil {
		t.Fatalf("failed to create tool: %v", err)
	}

	params, ok := weatherTool.Parameters().(map[string]any)
	if !ok {
		t.Fatalf("expected parameters to be map[string]any, got %T", weatherTool.Parameters())
	}

	if params["type"] != "object" {
		t.Errorf("expected type object, got %v", params["type"])
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties in schema, got %v", params["properties"])
	}

	// Check Location property
	locProp, ok := props["location"].(map[string]any)
	if !ok {
		t.Fatalf("expected location property in schema")
	}
	if locProp["type"] != "string" {
		t.Errorf("expected location type string, got %v", locProp["type"])
	}
	if locProp["description"] != "The city and state, e.g. San Francisco, CA" {
		t.Errorf("unexpected description: %v", locProp["description"])
	}

	// Check Units property
	unitsProp, ok := props["units"].(map[string]any)
	if !ok {
		t.Fatalf("expected units property in schema")
	}
	if unitsProp["type"] != "string" {
		t.Errorf("expected units type string, got %v", unitsProp["type"])
	}

	// Check required list (location is required, units is omitempty)
	reqs, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("expected required list in schema, got %v", params["required"])
	}
	if len(reqs) != 1 || reqs[0] != "location" {
		t.Errorf("expected required to contain only 'location', got %v", reqs)
	}
}

func TestTool_ExplicitSchemaOverride(t *testing.T) {
	customSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"custom": map[string]any{"type": "string"},
		},
	}

	myTool := tool.MustNew(tool.Config[string, string]{
		ID:          "custom_tool",
		Description: "A custom tool",
		Parameters:  customSchema,
		Execute: func(_ context.Context, in string) (string, error) {
			return "processed: " + in, nil
		},
	})

	if myTool.Parameters() == nil {
		t.Fatal("expected non-nil parameters")
	}
	params := myTool.Parameters().(map[string]any)
	if _, ok := params["properties"].(map[string]any)["custom"]; !ok {
		t.Errorf("expected custom schema to be preserved, got %v", params)
	}
}

func TestTool_Execute_RawAndTyped(t *testing.T) {
	calcTool := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID:          "weather",
		Description: "Returns weather info",
		Execute: func(_ context.Context, in WeatherInput) (WeatherOutput, error) {
			return WeatherOutput{
				Temperature: 19.0,
				Condition:   "Sunny in " + in.Location,
			}, nil
		},
	})

	ctx := context.Background()

	// 1. Raw JSON execution
	rawJSON := []byte(`{"location":"Paris","units":"celsius"}`)
	out, err := calcTool.Execute(ctx, rawJSON)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	weatherOut, ok := out.(WeatherOutput)
	if !ok {
		t.Fatalf("expected WeatherOutput, got %T", out)
	}
	if weatherOut.Condition != "Sunny in Paris" {
		t.Errorf("unexpected condition: %s", weatherOut.Condition)
	}

	// 2. Typed execution
	typedOut, err := calcTool.ExecuteTyped(ctx, WeatherInput{Location: "Berlin"})
	if err != nil {
		t.Fatalf("ExecuteTyped failed: %v", err)
	}
	if typedOut.Condition != "Sunny in Berlin" {
		t.Errorf("unexpected condition: %s", typedOut.Condition)
	}

	// 3. Invalid JSON input error handling
	_, err = calcTool.Execute(ctx, []byte(`{invalid-json`))
	if err == nil {
		t.Fatal("expected error on invalid JSON input, got nil")
	}
	if !strings.Contains(err.Error(), "invalid input") {
		t.Errorf("expected 'invalid input' in error message, got %v", err)
	}
}

func TestTool_ToDefinition(t *testing.T) {
	t1 := tool.MustNew(tool.Config[string, string]{
		ID:          "tool1",
		Description: "First tool",
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})

	t2 := tool.MustNew(tool.Config[string, string]{
		ID:          "tool2",
		Description: "Second tool",
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})

	def1 := tool.ToDefinition(t1)
	if def1.Name != "tool1" || def1.Description != "First tool" {
		t.Errorf("unexpected definition: %+v", def1)
	}

	defs := tool.ToDefinitions([]tool.Tool{t1, t2})
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions, got %d", len(defs))
	}
	if defs[0].Name != "tool1" || defs[1].Name != "tool2" {
		t.Errorf("unexpected definitions list: %+v", defs)
	}

	// Empty slice returns nil
	if tool.ToDefinitions(nil) != nil {
		t.Error("expected nil for empty tools")
	}
}
