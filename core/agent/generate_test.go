package agent_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
)

type Person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestGenerateStructured(t *testing.T) {
	model := &scriptModel{steps: []step{finalText(`{"name":"Ada","age":36}`)}}
	a := mustAgent(t, agent.Config{Name: "structured", Model: model})

	person, resp, err := agent.GenerateStructured[Person](context.Background(), a, "make a person")
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if person.Name != "Ada" || person.Age != 36 {
		t.Errorf("person = %+v", person)
	}
	if resp == nil || resp.Text == "" {
		t.Fatal("expected the originating response")
	}

	request := model.Calls()[0]
	if request.ResponseFormat == nil {
		t.Fatal("response format was not set")
	}
	if request.ResponseFormat.Type != llm.ResponseFormatJSONSchema {
		t.Errorf("response format type = %q", request.ResponseFormat.Type)
	}
	if request.ResponseFormat.Name != "Person" {
		t.Errorf("response format name = %q, want Person", request.ResponseFormat.Name)
	}
	if !request.ResponseFormat.Strict {
		t.Error("response format is not strict")
	}
	properties, ok := request.ResponseFormat.Schema["properties"].(map[string]any)
	if !ok || properties["name"] == nil || properties["age"] == nil {
		t.Errorf("unexpected schema: %+v", request.ResponseFormat.Schema)
	}
}

func TestGenerateStructured_UnfencesMarkdown(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("```json\n{\"name\":\"Grace\",\"age\":85}\n```")}}
	a := mustAgent(t, agent.Config{Name: "structured", Model: model})

	person, _, err := agent.GenerateStructured[Person](context.Background(), a, "make a person")
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if person.Name != "Grace" || person.Age != 85 {
		t.Errorf("person = %+v", person)
	}
}

func TestGenerateStructured_DecodeError(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("not json")}}
	a := mustAgent(t, agent.Config{Name: "structured", Model: model})

	_, resp, err := agent.GenerateStructured[Person](context.Background(), a, "make a person")
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if resp == nil {
		t.Error("expected the originating response on decode failure")
	}
}

func TestGenerateStructured_UnsupportedTypeFailsBeforeCallingModel(t *testing.T) {
	model := &scriptModel{steps: []step{finalText("unused")}}
	a := mustAgent(t, agent.Config{Name: "structured", Model: model})

	_, _, err := agent.GenerateStructured[chan int](context.Background(), a, "make a person")
	if !errors.Is(err, agent.ErrOutputSchema) {
		t.Fatalf("err = %v, want ErrOutputSchema", err)
	}
	if model.CallCount() != 0 {
		t.Errorf("model calls = %d, want 0", model.CallCount())
	}
}
