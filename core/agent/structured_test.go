package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/providers/mockllm"
)

type WeatherReport struct {
	City        string   `json:"city" description:"City name"`
	Temperature float64  `json:"temperature" description:"Temperature in Celsius"`
	Condition   string   `json:"condition" enum:"sunny,rainy,cloudy" description:"Weather condition"`
	Forecast    []string `json:"forecast,omitempty" description:"Optional next days forecast"`
}

func TestResponse_Unmarshal_DirectAndFenced(t *testing.T) {
	// 1. Clean JSON
	cleanResp := &agent.Response{
		Text: `{"city":"San Francisco","temperature":18.5,"condition":"sunny"}`,
	}
	var report1 WeatherReport
	if err := cleanResp.Unmarshal(&report1); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if report1.City != "San Francisco" || report1.Temperature != 18.5 || report1.Condition != "sunny" {
		t.Errorf("unexpected report: %+v", report1)
	}

	// 2. Wrapped in markdown code fence with json tag
	fencedResp := &agent.Response{
		Text: "```json\n{\"city\":\"Tokyo\",\"temperature\":22.0,\"condition\":\"cloudy\",\"forecast\":[\"rainy\",\"sunny\"]}\n```",
	}
	var report2 WeatherReport
	if err := fencedResp.Unmarshal(&report2); err != nil {
		t.Fatalf("Unmarshal fenced failed: %v", err)
	}
	if report2.City != "Tokyo" || report2.Temperature != 22.0 || len(report2.Forecast) != 2 {
		t.Errorf("unexpected report2: %+v", report2)
	}

	// 3. Error cases
	var nilResp *agent.Response
	if err := nilResp.Unmarshal(&report1); err == nil {
		t.Error("expected error for nil response, got nil")
	}

	emptyResp := &agent.Response{Text: "  "}
	if err := emptyResp.Unmarshal(&report1); err == nil {
		t.Error("expected error for empty response, got nil")
	}

	invalidResp := &agent.Response{Text: "{invalid-json"}
	if err := invalidResp.Unmarshal(&report1); err == nil {
		t.Error("expected error for invalid json, got nil")
	}
}

func TestGenerateStructured_EndToEnd(t *testing.T) {
	mockJSON := `{"city":"London","temperature":15.0,"condition":"rainy"}`
	mock := mockllm.New(mockJSON)

	a, err := agent.New(agent.Config{
		ID:    "weather-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	ctx := context.Background()
	report, _, err := agent.GenerateStructured[WeatherReport](ctx, a, "Get London weather")
	if err != nil {
		t.Fatalf("GenerateStructured failed: %v", err)
	}

	if report.City != "London" || report.Temperature != 15.0 || report.Condition != "rainy" {
		t.Errorf("unexpected structured data: %+v", report)
	}

	// Check that the mock received the expected response format and schema
	lastCall := mock.LastCall()
	if lastCall == nil || lastCall.ResponseFormat == nil {
		t.Fatalf("expected last call to have ResponseFormat, got %+v", lastCall)
	}
	if lastCall.ResponseFormat.Type != llm.ResponseFormatJSONSchema {
		t.Errorf("expected format json_schema, got %s", lastCall.ResponseFormat.Type)
	}
	if lastCall.ResponseFormat.Name != "WeatherReport" {
		t.Errorf("expected schema name WeatherReport, got %s", lastCall.ResponseFormat.Name)
	}
	schema, ok := lastCall.ResponseFormat.Schema.(map[string]any)
	if !ok {
		t.Fatalf("expected schema map[string]any, got %T", lastCall.ResponseFormat.Schema)
	}
	props := schema["properties"].(map[string]any)
	if props["city"] == nil || props["temperature"] == nil || props["condition"] == nil {
		t.Errorf("expected properties in schema, got %+v", props)
	}
}

func TestGenerateStructured_WithToolsReAct(t *testing.T) {
	// Step 1: Agent requests a tool call to query DB
	// Step 2: Agent synthesizes the tool result into structured output
	step1Calls := []llm.ToolCall{
		{
			ID:        "call_1",
			Name:      "lookup_temp",
			Arguments: `{"city":"Berlin"}`,
		},
	}
	step2JSON := `{"city":"Berlin","temperature":20.5,"condition":"sunny"}`

	lookupTool := tool.MustNew(tool.Config[struct {
		City string `json:"city"`
	}, string]{
		ID: "lookup_temp",
		Execute: func(_ context.Context, _ struct {
			City string `json:"city"`
		}) (string, error) {
			return "20.5 C, Sunny", nil
		},
	})

	mock := mockllm.New("calling tool...").ApplyOptions(
		mockllm.WithCustomHandler(func(_ context.Context, req llm.GenerateRequest) (*llm.GenerateResponse, error) {
			// First step has no tool results
			hasToolResult := false
			for _, m := range req.Messages {
				if m.Role == llm.RoleTool {
					hasToolResult = true
					break
				}
			}
			if !hasToolResult {
				return &llm.GenerateResponse{
					ToolCalls:    step1Calls,
					FinishReason: llm.FinishReasonTool,
				}, nil
			}
			// Second step returns structured JSON
			return &llm.GenerateResponse{
				Text:         step2JSON,
				FinishReason: llm.FinishReasonStop,
			}, nil
		}),
	)

	a, err := agent.New(agent.Config{
		ID:    "agent-tools-react",
		Model: mock,
		Tools: []tool.Tool{lookupTool},
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	report, _, err := agent.GenerateStructured[WeatherReport](context.Background(), a, "Check Berlin weather")
	if err != nil {
		t.Fatalf("GenerateStructured with tools failed: %v", err)
	}

	if report.City != "Berlin" || report.Temperature != 20.5 || report.Condition != "sunny" {
		t.Errorf("unexpected data after ReAct: %+v", report)
	}
}

func TestGenerateStructured_UnmarshalError(t *testing.T) {
	mock := mockllm.New("I cannot provide weather data right now.")
	a, _ := agent.New(agent.Config{
		ID:    "error-agent",
		Model: mock,
	})

	_, _, err := agent.GenerateStructured[WeatherReport](context.Background(), a, "weather")
	if err == nil {
		t.Fatal("expected unmarshal error on malformed response, got nil")
	}
	if !strings.Contains(err.Error(), "failed to unmarshal structured response") {
		t.Errorf("expected structured unmarshal error, got %v", err)
	}
}

func TestGenerateMessagesStructured_EndToEnd(t *testing.T) {
	mock := mockllm.New(`{"city":"Paris","temperature":21.0,"condition":"sunny"}`)
	a, err := agent.New(agent.Config{
		ID:    "messages-agent",
		Model: mock,
	})
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}

	report, _, err := agent.GenerateMessagesStructured[WeatherReport](
		context.Background(),
		a,
		[]llm.Message{llm.UserMessage("Get Paris weather")},
	)
	if err != nil {
		t.Fatalf("GenerateMessagesStructured failed: %v", err)
	}
	if report.City != "Paris" || report.Temperature != 21.0 || report.Condition != "sunny" {
		t.Errorf("unexpected structured data: %+v", report)
	}

	lastCall := mock.LastCall()
	if lastCall == nil || lastCall.ResponseFormat == nil || lastCall.ResponseFormat.Name != "WeatherReport" {
		t.Errorf("expected WeatherReport response format, got %+v", lastCall)
	}
}

func TestWithJSONOutput_Option(t *testing.T) {
	mock := mockllm.New(`{"custom":"json"}`)
	a, _ := agent.New(agent.Config{
		ID:    "json-agent",
		Model: mock,
	})

	res, err := a.Generate(context.Background(), "give json", agent.WithJSONOutput())
	if err != nil {
		t.Fatalf("Generate with JSON output failed: %v", err)
	}

	if mock.LastCall().ResponseFormat == nil || mock.LastCall().ResponseFormat.Type != llm.ResponseFormatJSON {
		t.Errorf("expected ResponseFormatJSON, got %+v", mock.LastCall().ResponseFormat)
	}

	var parsed map[string]string
	if err := res.Unmarshal(&parsed); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if parsed["custom"] != "json" {
		t.Errorf("expected custom: json, got %+v", parsed)
	}
}
