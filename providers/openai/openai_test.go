package openai

import (
	"context"
	"errors"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestNewRequiresModel(t *testing.T) {
	if _, err := New(context.Background(), Config{}); !errors.Is(err, ErrModelNotSet) {
		t.Fatalf("err = %v, want ErrModelNotSet", err)
	}
}

func TestNewAndModelID(t *testing.T) {
	model, err := New(context.Background(), Config{Model: "gpt-test", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if model.ModelID() != "gpt-test" {
		t.Fatalf("model id = %q", model.ModelID())
	}
}

func TestInfoFeatures(t *testing.T) {
	model, err := New(context.Background(), Config{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info := model.Info()
	if info.Provider != providerName || info.Model != "gpt-test" {
		t.Fatalf("info = %+v", info)
	}
	features := info.Features
	if !features.Streaming || !features.Tools || !features.ParallelToolCalls ||
		!features.StrictJSONSchema || !features.ServerState || !features.HostedTools ||
		!features.MCP || !features.Reasoning || !features.ReasoningSummary ||
		!features.ReasoningEncrypted || !features.Vision || !features.Documents {
		t.Fatalf("features = %+v", features)
	}
	if features.AudioIn || features.AudioOut || features.Video {
		t.Fatalf("features = %+v, want no audio or video", features)
	}
	if features.PromptCaching != llm.CacheModeAutomatic {
		t.Fatalf("prompt caching = %q", features.PromptCaching)
	}
}
