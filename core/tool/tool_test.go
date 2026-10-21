package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
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

type namedString string

type namedBytes []byte

type validatingInput struct {
	Name string `json:"name"`
}

func (v validatingInput) Validate() error {
	if v.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

type customTool struct {
	id string
}

func (t customTool) ID() string          { return t.id }
func (t customTool) Description() string { return "custom tool" }
func (t customTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t customTool) Execute(_ context.Context, _ []byte) (tool.Result, error) {
	return tool.Result{Content: []llm.Part{llm.TextPart("custom result")}}, nil
}

func TestTool_Validation(t *testing.T) {
	execute := func(_ context.Context, _ WeatherInput) (WeatherOutput, error) {
		return WeatherOutput{}, nil
	}

	tests := []struct {
		name string
		cfg  tool.Config[WeatherInput, WeatherOutput]
		want error
	}{
		{
			name: "missing id",
			cfg:  tool.Config[WeatherInput, WeatherOutput]{Execute: execute},
			want: tool.ErrIDRequired,
		},
		{
			name: "whitespace id",
			cfg: tool.Config[WeatherInput, WeatherOutput]{
				ID:      "   ",
				Execute: execute,
			},
			want: tool.ErrIDRequired,
		},
		{
			name: "missing execute",
			cfg:  tool.Config[WeatherInput, WeatherOutput]{ID: "weather"},
			want: tool.ErrExecuteRequired,
		},
		{
			name: "non-object custom schema",
			cfg: tool.Config[WeatherInput, WeatherOutput]{
				ID:         "weather",
				Parameters: "not an object",
				Execute:    execute,
			},
			want: tool.ErrInvalidSchema,
		},
		{
			name: "unencodable custom schema",
			cfg: tool.Config[WeatherInput, WeatherOutput]{
				ID:         "weather",
				Parameters: make(chan int),
				Execute:    execute,
			},
			want: tool.ErrInvalidSchema,
		},
		{
			name: "typed nil custom schema",
			cfg: tool.Config[WeatherInput, WeatherOutput]{
				ID:         "weather",
				Parameters: map[string]any(nil),
				Execute:    execute,
			},
			want: tool.ErrInvalidSchema,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.New(tc.cfg)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}

	t.Run("unsupported input schema", func(t *testing.T) {
		_, err := tool.New(tool.Config[struct {
			Ch chan int `json:"ch"`
		}, string]{
			ID: "unsupported",
			Execute: func(_ context.Context, _ struct {
				Ch chan int `json:"ch"`
			}) (string, error) {
				return "", nil
			},
		})
		if !errors.Is(err, tool.ErrInvalidSchema) {
			t.Fatalf("expected ErrInvalidSchema, got %v", err)
		}
	})

	t.Run("must new panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("expected MustNew to panic on validation error")
			}
		}()
		tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{})
	})
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

	params := weatherTool.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type object, got %v", params["type"])
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties in schema, got %v", params["properties"])
	}

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

	unitsProp, ok := props["units"].(map[string]any)
	if !ok {
		t.Fatalf("expected units property in schema")
	}
	if unitsProp["type"] != "string" {
		t.Errorf("expected units type string, got %v", unitsProp["type"])
	}

	reqs, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("expected required list in schema, got %v", params["required"])
	}
	if len(reqs) != 1 || reqs[0] != "location" {
		t.Errorf("expected required to contain only 'location', got %v", reqs)
	}
}

func TestTool_SchemaOptions(t *testing.T) {
	weatherTool := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID:            "strict_weather",
		SchemaOptions: []tool.SchemaOption{tool.WithStrictSchema(true)},
		Execute: func(_ context.Context, _ WeatherInput) (WeatherOutput, error) {
			return WeatherOutput{}, nil
		},
	})

	params := weatherTool.Parameters()
	if params["additionalProperties"] != false {
		t.Errorf("expected additionalProperties false, got %v", params["additionalProperties"])
	}
	reqs := params["required"].([]string)
	if len(reqs) != 2 || reqs[0] != "location" || reqs[1] != "units" {
		t.Errorf("expected all fields required, got %v", reqs)
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

	customSchema["properties"].(map[string]any)["custom"] = map[string]any{"type": "integer"}

	params := myTool.Parameters()
	customProp := params["properties"].(map[string]any)["custom"].(map[string]any)
	if customProp["type"] != "string" {
		t.Errorf("expected schema to be copied at construction, got %v", customProp)
	}

	params["properties"].(map[string]any)["custom"] = map[string]any{"type": "boolean"}
	paramsAgain := myTool.Parameters()
	customProp = paramsAgain["properties"].(map[string]any)["custom"].(map[string]any)
	if customProp["type"] != "string" {
		t.Errorf("expected Parameters to return a copy, got %v", customProp)
	}
}

func TestTool_CustomSchemaFormats(t *testing.T) {
	rawSchema := json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`)
	rawTool := tool.MustNew(tool.Config[string, string]{
		ID:         "raw_schema",
		Parameters: rawSchema,
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})
	rawParams := rawTool.Parameters()
	rawProps, ok := rawParams["properties"].(map[string]any)
	if !ok || rawProps["value"] == nil {
		t.Fatalf("expected raw JSON schema to be normalized, got %+v", rawParams)
	}

	mapSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{"value"},
	}
	mapTool := tool.MustNew(tool.Config[string, string]{
		ID:         "map_schema",
		Parameters: mapSchema,
		Execute: func(_ context.Context, in string) (string, error) {
			return in, nil
		},
	})
	required, ok := mapTool.Parameters()["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "value" {
		t.Errorf("expected concrete []string required values to be preserved, got %T %v", mapTool.Parameters()["required"], mapTool.Parameters()["required"])
	}
}

func TestTool_Execute(t *testing.T) {
	weatherTool := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
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

	result, err := weatherTool.Execute(ctx, []byte(`{"location":"Paris","units":"celsius"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	weatherOut, ok := result.Data.(WeatherOutput)
	if !ok {
		t.Fatalf("expected WeatherOutput data, got %T", result.Data)
	}
	if weatherOut.Condition != "Sunny in Paris" {
		t.Errorf("unexpected condition: %s", weatherOut.Condition)
	}
	if !strings.Contains(result.Text(), `"condition":"Sunny in Paris"`) {
		t.Errorf("expected JSON text content, got %q", result.Text())
	}

	typedOut, err := weatherTool.Execute(ctx, []byte(`{"location":"Berlin","units":"celsius"}`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if typedOut.Text() == "" {
		t.Error("expected non-empty output for Berlin")
	}
}

func TestTool_Execute_StringAndByteInputsUseJSON(t *testing.T) {
	ctx := context.Background()
	var stringInput string
	stringTool := tool.MustNew(tool.Config[string, string]{
		ID: "string",
		Execute: func(_ context.Context, in string) (string, error) {
			stringInput = in
			return "ok", nil
		},
	})
	if _, err := stringTool.Execute(ctx, []byte(`"hello"`)); err != nil {
		t.Fatalf("string Execute failed: %v", err)
	}
	if stringInput != "hello" {
		t.Errorf("expected JSON string to decode to hello, got %q", stringInput)
	}

	stringInput = "set"
	if _, err := stringTool.Execute(ctx, nil); err != nil {
		t.Fatalf("empty input Execute failed: %v", err)
	}
	if stringInput != "" {
		t.Errorf("expected empty input to produce zero value, got %q", stringInput)
	}

	stringInput = "set"
	if _, err := stringTool.Execute(ctx, []byte("null")); err != nil {
		t.Fatalf("null input Execute failed: %v", err)
	}
	if stringInput != "" {
		t.Errorf("expected null input to produce zero value, got %q", stringInput)
	}

	var namedInput namedString
	namedTool := tool.MustNew(tool.Config[namedString, string]{
		ID: "named_string",
		Execute: func(_ context.Context, in namedString) (string, error) {
			namedInput = in
			return "ok", nil
		},
	})
	if _, err := namedTool.Execute(context.Background(), []byte(`"world"`)); err != nil {
		t.Fatalf("named string Execute failed: %v", err)
	}
	if namedInput != "world" {
		t.Errorf("expected named string to decode to world, got %q", namedInput)
	}

	var bytesInput []byte
	bytesTool := tool.MustNew(tool.Config[[]byte, string]{
		ID: "bytes",
		Execute: func(_ context.Context, in []byte) (string, error) {
			bytesInput = in
			return "ok", nil
		},
	})
	encoded, err := json.Marshal([]byte("bytes"))
	if err != nil {
		t.Fatalf("failed to encode bytes: %v", err)
	}
	if _, err := bytesTool.Execute(context.Background(), encoded); err != nil {
		t.Fatalf("bytes Execute failed: %v", err)
	}
	if string(bytesInput) != "bytes" {
		t.Errorf("expected base64-decoded bytes, got %q", bytesInput)
	}
}

func TestTool_Execute_InvalidInput(t *testing.T) {
	called := false
	weatherTool := tool.MustNew(tool.Config[WeatherInput, WeatherOutput]{
		ID: "weather",
		Execute: func(_ context.Context, _ WeatherInput) (WeatherOutput, error) {
			called = true
			return WeatherOutput{}, nil
		},
	})

	_, err := weatherTool.Execute(context.Background(), []byte(`{invalid-json`))
	if !errors.Is(err, tool.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput, got %v", err)
	}
	if called {
		t.Error("handler should not run after invalid input")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := weatherTool.Execute(ctx, []byte(`{}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context cancellation, got %v", err)
	}
}

func TestTool_Execute_Validator(t *testing.T) {
	validatingTool := tool.MustNew(tool.Config[validatingInput, string]{
		ID: "validated",
		Execute: func(_ context.Context, in validatingInput) (string, error) {
			return in.Name, nil
		},
	})

	if _, err := validatingTool.Execute(context.Background(), []byte(`{}`)); !errors.Is(err, tool.ErrInvalidInput) {
		t.Fatalf("expected validation error, got %v", err)
	}

	result, err := validatingTool.Execute(context.Background(), []byte(`{"name":"ok"}`))
	if err != nil {
		t.Fatalf("expected valid input, got %v", err)
	}
	if result.Text() != "ok" {
		t.Errorf("expected ok, got %q", result.Text())
	}
}

func TestTool_Execute_OutputNormalization(t *testing.T) {
	t.Run("data only result gets JSON content", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, tool.Result]{
			ID: "data",
			Execute: func(_ context.Context, _ string) (tool.Result, error) {
				return tool.Result{Data: map[string]any{"ok": true}}, nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != `{"ok":true}` {
			t.Errorf("unexpected data content: %q", result.Text())
		}
	})

	t.Run("named string output is text", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, namedString]{
			ID: "named_output",
			Execute: func(_ context.Context, _ string) (namedString, error) {
				return "ready", nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != "ready" {
			t.Errorf("unexpected named string output: %q", result.Text())
		}
	})

	t.Run("unencodable output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, chan int]{
			ID: "bad_output",
			Execute: func(_ context.Context, _ string) (chan int, error) {
				return make(chan int), nil
			},
		})
		if _, err := resultTool.Execute(context.Background(), []byte(`"x"`)); !errors.Is(err, tool.ErrOutputEncode) {
			t.Errorf("expected ErrOutputEncode, got %v", err)
		}
	})

	t.Run("part output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, llm.Part]{
			ID: "part_output",
			Execute: func(_ context.Context, _ string) (llm.Part, error) {
				return llm.TextPart("part text"), nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if len(result.Content) != 1 || result.Text() != "part text" {
			t.Errorf("unexpected part output: %+v", result)
		}
	})

	t.Run("parts output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, []llm.Part]{
			ID: "parts_output",
			Execute: func(_ context.Context, _ string) ([]llm.Part, error) {
				return []llm.Part{llm.TextPart("one"), llm.TextPart("two")}, nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != "one\ntwo" {
			t.Errorf("unexpected parts output: %q", result.Text())
		}
	})

	t.Run("byte output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, []byte]{
			ID: "byte_output",
			Execute: func(_ context.Context, _ string) ([]byte, error) {
				return []byte("bytes"), nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != "bytes" {
			t.Errorf("unexpected byte output: %q", result.Text())
		}
	})

	t.Run("named byte output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, namedBytes]{
			ID: "named_byte_output",
			Execute: func(_ context.Context, _ string) (namedBytes, error) {
				return namedBytes("named bytes"), nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != "named bytes" {
			t.Errorf("unexpected named byte output: %q", result.Text())
		}
	})

	t.Run("raw message output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, json.RawMessage]{
			ID: "raw_output",
			Execute: func(_ context.Context, _ string) (json.RawMessage, error) {
				return json.RawMessage(`{"raw":true}`), nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != `{"raw":true}` {
			t.Errorf("unexpected raw output: %q", result.Text())
		}
		if _, ok := result.Data.(json.RawMessage); !ok {
			t.Errorf("expected raw data, got %T", result.Data)
		}
	})

	t.Run("nil result output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, *tool.Result]{
			ID: "nil_result_output",
			Execute: func(_ context.Context, _ string) (*tool.Result, error) {
				return nil, nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if len(result.Content) != 0 || result.Data != nil {
			t.Errorf("expected empty result, got %+v", result)
		}
	})

	t.Run("nil interface output", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, any]{
			ID: "nil_output",
			Execute: func(_ context.Context, _ string) (any, error) {
				return nil, nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if len(result.Content) != 0 || result.Data != nil {
			t.Errorf("expected empty result, got %+v", result)
		}
	})

	t.Run("result with content and data", func(t *testing.T) {
		resultTool := tool.MustNew(tool.Config[string, tool.Result]{
			ID: "mixed_result_output",
			Execute: func(_ context.Context, _ string) (tool.Result, error) {
				return tool.Result{
					Content: []llm.Part{llm.TextPart("visible")},
					Data:    map[string]any{"internal": true},
				}, nil
			},
		})
		result, err := resultTool.Execute(context.Background(), []byte(`"x"`))
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
		if result.Text() != "visible" {
			t.Errorf("unexpected mixed content: %q", result.Text())
		}
		if result.Data == nil {
			t.Error("expected structured data to be preserved")
		}
	})
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

	custom := customTool{id: "custom"}
	customDef := tool.ToDefinition(custom)
	if customDef.Name != "custom" || customDef.Description != "custom tool" {
		t.Errorf("unexpected custom definition: %+v", customDef)
	}

	var typedNil *tool.BaseTool[string, string]
	defs := tool.ToDefinitions([]tool.Tool{t1, nil, typedNil, t2})
	if len(defs) != 2 {
		t.Fatalf("expected 2 definitions after skipping nil tools, got %d", len(defs))
	}
	if defs[0].Name != "tool1" || defs[1].Name != "tool2" {
		t.Errorf("unexpected definitions list: %+v", defs)
	}

	if def := tool.ToDefinition(nil); def.Name != "" || def.Description != "" || def.Parameters != nil {
		t.Error("expected zero definition for nil tool")
	}
	if tool.ToDefinitions(nil) != nil {
		t.Error("expected nil for empty tools")
	}
	if tool.ToDefinitions([]tool.Tool{nil, typedNil}) != nil {
		t.Error("expected nil when all tools are nil")
	}
}

func TestTool_MultipartResult(t *testing.T) {
	imgPart := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	screenshotTool := tool.MustNew(tool.Config[string, tool.Result]{
		ID:          "screenshot",
		Description: "Takes screenshot",
		Execute: func(_ context.Context, _ string) (tool.Result, error) {
			return tool.NewImageResult("screen captured", imgPart), nil
		},
	})

	result, err := screenshotTool.Execute(context.Background(), []byte(`"full"`))
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Text() != "screen captured" {
		t.Errorf("expected 'screen captured', got %q", result.Text())
	}
	if len(result.Content) != 2 || result.Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected content parts: %+v", result.Content)
	}
}

func TestTool_ResultConstructors(t *testing.T) {
	textResult := tool.Result{Content: []llm.Part{llm.TextPart("hello")}}
	if textResult.Text() != "hello" {
		t.Errorf("unexpected text result: %+v", textResult)
	}

	imgPart := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	imageResult := tool.NewImageResult("caption", imgPart)
	if len(imageResult.Content) != 2 || imageResult.Text() != "caption" {
		t.Errorf("unexpected image result: %+v", imageResult)
	}

	dataResult := tool.Result{Data: map[string]any{"ok": true}}
	resultParts, err := dataResult.Parts()
	if err != nil {
		t.Fatalf("Parts failed: %v", err)
	}
	if len(resultParts) != 1 || resultParts[0].Text != `{"ok":true}` {
		t.Errorf("unexpected data parts: %+v", resultParts)
	}

	badResult := tool.Result{Data: make(chan int)}
	if _, err := badResult.Parts(); !errors.Is(err, tool.ErrOutputEncode) {
		t.Errorf("expected ErrOutputEncode for unencodable data, got %v", err)
	}
}

func TestTool_NilAndZeroValue(t *testing.T) {
	var nilTool *tool.BaseTool[string, string]
	if nilTool.ID() != "" || nilTool.Description() != "" || nilTool.Parameters() != nil {
		t.Error("expected nil tool accessors to be safe")
	}
	if _, err := nilTool.Execute(context.Background(), nil); !errors.Is(err, tool.ErrExecuteRequired) {
		t.Errorf("expected ErrExecuteRequired for nil tool, got %v", err)
	}
	var zero tool.BaseTool[string, string]
	if _, err := zero.Execute(context.Background(), nil); !errors.Is(err, tool.ErrExecuteRequired) {
		t.Errorf("expected ErrExecuteRequired for zero tool, got %v", err)
	}
}
