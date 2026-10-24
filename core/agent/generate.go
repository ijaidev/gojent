package agent

import (
	"context"
	"reflect"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// Option customizes one Agent run.
type Option func(*optionConfig)

type optionConfig struct {
	runner  Runner
	run     RunOptions
	session *session.Session
}

// WithModel overrides the agent model for one run.
func WithModel(model llm.Model) Option {
	return func(cfg *optionConfig) {
		cfg.run.Model = model
	}
}

// WithTools adds or replaces tools for one run.
func WithTools(tools ...tool.Tool) Option {
	return func(cfg *optionConfig) {
		cfg.run.Tools = append(cfg.run.Tools, tools...)
	}
}

// WithThinking overrides reasoning configuration for one run.
func WithThinking(thinking llm.ThinkingConfig) Option {
	return func(cfg *optionConfig) {
		cfg.run.Thinking = thinking.Clone()
	}
}

// WithResponseFormat constrains output for one run.
func WithResponseFormat(format *llm.ResponseFormat) Option {
	return func(cfg *optionConfig) {
		cfg.run.ResponseFormat = format.Clone()
	}
}

// WithOutputSchema constrains output to a strict schema generated from T.
func WithOutputSchema[T any](opts ...tool.SchemaOption) Option {
	return func(cfg *optionConfig) {
		schemaOpts := append([]tool.SchemaOption(nil), opts...)
		schemaOpts = append(schemaOpts, tool.WithStrictSchema(true))
		schema, err := tool.GenerateSchema[T](schemaOpts...)
		if err != nil {
			schema = map[string]any{}
		}

		var zero T
		typ := reflect.TypeOf(zero)
		for typ != nil && typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		name := "StructuredOutput"
		if typ != nil && typ.Name() != "" {
			name = typ.Name()
		}
		cfg.run.ResponseFormat = &llm.ResponseFormat{
			Type:   llm.ResponseFormatJSONSchema,
			Name:   name,
			Schema: schema,
			Strict: true,
		}
	}
}

// WithTemperature overrides sampling temperature.
func WithTemperature(temperature float64) Option {
	return func(cfg *optionConfig) {
		cfg.run.Temperature = &temperature
	}
}

// WithMaxTokens overrides the output token limit.
func WithMaxTokens(tokens int) Option {
	return func(cfg *optionConfig) {
		cfg.run.MaxTokens = &tokens
	}
}

// WithTopP overrides nucleus sampling.
func WithTopP(topP float64) Option {
	return func(cfg *optionConfig) {
		cfg.run.TopP = &topP
	}
}

// WithToolChoice constrains tool selection.
func WithToolChoice(choice llm.ToolChoice) Option {
	return func(cfg *optionConfig) {
		cfg.run.ToolChoice = choice
	}
}

// WithMedia attaches multimodal parts to the trailing user message.
func WithMedia(parts ...llm.Part) Option {
	return func(cfg *optionConfig) {
		cfg.run.Media = append(cfg.run.Media, parts...)
	}
}

// WithMaxSteps overrides the step budget for one run.
func WithMaxSteps(steps int) Option {
	return func(cfg *optionConfig) {
		cfg.run.MaxSteps = steps
	}
}

// WithStore configures conversation persistence for one run.
func WithStore(store session.Store) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Store = store
	}
}

// WithSession configures the conversation identity for one run.
func WithSession(id, resourceID string) Option {
	return func(cfg *optionConfig) {
		cfg.session = &session.Session{ID: id, ResourceID: resourceID}
	}
}

// WithPolicy sets the run policy, replacing any agent default.
func WithPolicy(policy Policy) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Policy = policy
	}
}

// WithHooks sets a function-field policy, replacing any agent default.
func WithHooks(hooks Hooks) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Policy = hooks
	}
}

// WithSink observes events for one run.
func WithSink(sink Sink) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Sink = sink
	}
}

// WithApprover resolves Ask decisions for one run.
func WithApprover(approver Approver) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Approver = approver
	}
}

// WithBudget sets the run budget.
func WithBudget(budget Budget) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Budget = budget
	}
}

// WithRetry sets the transient-failure retry policy.
func WithRetry(retry RetryPolicy) Option {
	return func(cfg *optionConfig) {
		cfg.runner.Retry = retry
	}
}

// WithToolTimeout sets the per-tool execution timeout. A negative duration
// disables the timeout.
func WithToolTimeout(timeout time.Duration) Option {
	return func(cfg *optionConfig) {
		cfg.runner.ToolTimeout = timeout
	}
}

func resolveOptions(opts []Option) optionConfig {
	cfg := optionConfig{}
	for _, option := range opts {
		if option != nil {
			option(&cfg)
		}
	}
	return cfg
}

// Generate runs the agent with a text prompt.
func (a *Agent) Generate(ctx context.Context, prompt string, opts ...Option) (*Response, error) {
	var input []llm.Message
	if prompt != "" {
		input = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.GenerateMessages(ctx, input, opts...)
}

// GenerateMessages runs the agent with explicit messages.
func (a *Agent) GenerateMessages(ctx context.Context, messages []llm.Message, opts ...Option) (*Response, error) {
	cfg := resolveOptions(opts)
	runner := cfg.runner
	return runner.RunWith(ctx, a, cfg.session, cfg.run, messages...)
}

// GenerateStructured runs the agent and decodes its output into T.
func GenerateStructured[T any](ctx context.Context, a *Agent, prompt string, opts ...Option) (T, *Response, error) {
	all := append([]Option{WithOutputSchema[T]()}, opts...)
	response, err := a.Generate(ctx, prompt, all...)
	return decodeStructured[T](response, err)
}

func decodeStructured[T any](response *Response, err error) (T, *Response, error) {
	var zero T
	if err != nil {
		return zero, nil, err
	}
	var data T
	if err := response.Unmarshal(&data); err != nil {
		return zero, response, err
	}
	return data, response, nil
}
