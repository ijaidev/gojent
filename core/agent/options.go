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
	Hooks          Hooks
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

// WithThinkingLevel configures the qualitative thinking level for a generation.
func WithThinkingLevel(level llm.ThinkingLevel) GenerateOption {
	return func(c *GenerateConfig) {
		if c.Thinking == nil {
			c.Thinking = &llm.ThinkingConfig{}
		}
		c.Thinking.Level = level
	}
}

// WithThinkingBudget configures an explicit token budget for thinking.
func WithThinkingBudget(tokens int) GenerateOption {
	return func(c *GenerateConfig) {
		if c.Thinking == nil {
			c.Thinking = &llm.ThinkingConfig{}
		}
		c.Thinking.BudgetTokens = &tokens
	}
}

// WithResponseFormat configures structured output constraints directly using an llm.ResponseFormat.
func WithResponseFormat(rf *llm.ResponseFormat) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.ResponseFormat = rf.Clone()
	}
}

// WithJSONOutput instructs the agent to return valid JSON output without a constrained schema.
func WithJSONOutput() GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.ResponseFormat = &llm.ResponseFormat{
			Type: llm.ResponseFormatJSON,
		}
	}
}

// WithOutputSchema configures the agent to return structured output strictly conforming to the Go type T.
func WithOutputSchema[T any](opts ...tool.SchemaOption) GenerateOption {
	return func(cfg *GenerateConfig) {
		schema := tool.GenerateSchemaWithOptions[T](opts...)
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

// WithAdditionalMessages appends conversation messages before the user prompt.
func WithAdditionalMessages(msgs ...llm.Message) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Messages = append(cfg.Messages, msgs...)
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

// WithHooks supplies per-call lifecycle hooks for this generation.
// Non-nil callbacks override matching agent-level hooks for the duration of the
// run. Repeated WithHooks options layer on top of each other.
func WithHooks(h Hooks) GenerateOption {
	return func(cfg *GenerateConfig) {
		cfg.Hooks = mergeHooks(cfg.Hooks, h)
	}
}
