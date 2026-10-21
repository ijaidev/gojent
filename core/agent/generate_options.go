package agent

import (
	"reflect"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

// GenerateConfig holds runtime options for a single Generate or Stream execution.
type GenerateConfig struct {
	Model          llm.LLM
	Storage        storage.Storage
	ThreadID       string
	ResourceID     string
	Thinking       *llm.ThinkingConfig
	ResponseFormat *llm.ResponseFormat
	Temperature    *float64
	MaxTokens      *int
	TopP           *float64
	Messages       []llm.Message
	Media          []llm.Part
	Tools          []tool.Tool
	MaxSteps       *int
	Hooks          *Hooks
}

// GenerateOption allows customizing an individual Generate call.
type GenerateOption func(*GenerateConfig)

// WithModel allows overriding the model for a single Generate invocation.
func WithModel(m llm.LLM) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Model = m
	}
}

// WithStorage overrides the storage backend for a single Generate invocation.
func WithStorage(s storage.Storage) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Storage = s
	}
}

// WithThreadID specifies the conversation thread ID to load history from and persist messages to.
func WithThreadID(id string) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.ThreadID = id
	}
}

// WithResourceID specifies an owner/tenant resource ID scoping the thread.
func WithResourceID(id string) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.ResourceID = id
	}
}

// WithThinking configures thinking/reasoning parameters for a generation.
func WithThinking(cfg llm.ThinkingConfig) GenerateOption {
	return func(c *GenerateConfig) {
		c.Thinking = cfg.Clone()
	}
}

// WithResponseFormat configures structured output constraints directly using an llm.ResponseFormat.
func WithResponseFormat(rf *llm.ResponseFormat) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.ResponseFormat = rf.Clone()
	}
}

// WithOutputSchema configures the agent to return structured output strictly conforming to the Go type T.
func WithOutputSchema[T any](opts ...tool.SchemaOption) GenerateOption {
	return func(cfg *GenerateConfig) {
		schemaOpts := append([]tool.SchemaOption(nil), opts...)
		schemaOpts = append(schemaOpts, tool.WithStrictSchema(true))
		schema, _ := tool.GenerateSchema[T](schemaOpts...)
		if schema == nil {
			schema = map[string]any{}
		}
		var zero T
		t := reflect.TypeOf(zero)
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		name := "StructuredOutput"
		if t != nil && t.Name() != "" {
			name = t.Name()
		}
		strict := true
		cfg.ResponseFormat = &llm.ResponseFormat{
			Type:   llm.ResponseFormatJSONSchema,
			Name:   name,
			Schema: schema,
			Strict: &strict,
		}
	}
}

// WithTemperature overrides the temperature parameter for a generation.
func WithTemperature(t float64) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Temperature = &t
	}
}

// WithMaxTokens overrides the maximum tokens to generate.
func WithMaxTokens(n int) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.MaxTokens = &n
	}
}

// WithTopP overrides the nucleus sampling probability for a generation.
func WithTopP(p float64) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.TopP = &p
	}
}

// WithMedia attaches multimodal media parts (images, documents, audio, video) to the generation prompt.
func WithMedia(parts ...llm.Part) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Media = append(cfg.Media, parts...)
	}
}

// WithTools supplies additional per-call tools for this generation invocation.
func WithTools(tools ...tool.Tool) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Tools = append(cfg.Tools, tools...)
	}
}

// WithMaxSteps overrides the maximum number of ReAct loop iterations for this generation.
func WithMaxSteps(n int) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.MaxSteps = &n
	}
}

// WithHooks replaces the agent-level lifecycle hooks for this generation.
// A later WithHooks option replaces an earlier one wholesale.
func WithHooks(h Hooks) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Hooks = &h
	}
}
