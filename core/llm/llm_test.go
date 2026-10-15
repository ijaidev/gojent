package llm_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestMockLLM_SingleResponse(t *testing.T) {
	mock := llm.NewMockLLM("test response")
	if mock.ModelID() != "mock-llm" {
		t.Errorf("expected modelID mock-llm, got %s", mock.ModelID())
	}

	req := llm.GenerateRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
		},
	}

	res, err := mock.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "test response" {
		t.Errorf("expected 'test response', got '%s'", res.Text)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected 1 call, got %d", mock.CallCount())
	}
	if mock.LastCall() == nil || len(mock.LastCall().Messages) != 1 {
		t.Errorf("expected last call to have 1 message")
	}
}

func TestMockLLM_SequentialResponses(t *testing.T) {
	mock := llm.NewMockLLM("first", "second")

	res1, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res1.Text != "first" {
		t.Errorf("expected 'first', got '%s'", res1.Text)
	}

	res2, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res2.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res2.Text)
	}

	// Should repeat the last response
	res3, _ := mock.Generate(context.Background(), llm.GenerateRequest{})
	if res3.Text != "second" {
		t.Errorf("expected 'second', got '%s'", res3.Text)
	}
}

func TestMockLLM_NewMockllmAlias(t *testing.T) {
	mock := llm.NewMockllm("alias check")
	res, err := mock.Generate(context.Background(), llm.GenerateRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Text != "alias check" {
		t.Errorf("expected 'alias check', got '%s'", res.Text)
	}
}

func TestMockLLM_RespectsContextCancellation(t *testing.T) {
	mock := llm.NewMockLLM("cancelled")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := mock.Generate(ctx, llm.GenerateRequest{})
	if err == nil {
		t.Error("expected error due to cancelled context, got nil")
	}
}
