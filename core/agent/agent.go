// Package agent provides autonomous agent abstractions, configuration, execution loops,
// and lifecycle hooks for observing and customizing runs.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

var (
	// ErrModelRequired is returned when an Agent is created without an LLM model.
	ErrModelRequired = errors.New("agent: model is required")
	// ErrIDRequired is returned when an Agent is created without an ID.
	ErrIDRequired = errors.New("agent: id is required")
	// ErrToolNil is returned when attempting to add a nil tool to an Agent.
	ErrToolNil = errors.New("agent: cannot add nil tool")
)

const defaultMaxSteps = 5

// Config defines the initialization options for an Agent.
type Config struct {
	// ID is the unique identifier for the agent (e.g. "chef-agent").
	ID string
	// Name is a human-readable display name for the agent.
	Name string
	// Description explains the agent's purpose and specialization.
	Description string
	// Instructions are the system instructions/prompt guiding agent behavior.
	Instructions string
	// Model is the swappable LLM instance that powers the agent.
	Model llm.LLM
	// Storage is the optional swappable storage backend for conversation threads, messages, and state.
	Storage storage.Storage
	// Tools contains the list of tools available to the agent.
	Tools []tool.Tool
	// MaxSteps sets the default maximum number of ReAct loop iterations (defaults to 5).
	MaxSteps int
	// Thinking configures default thinking/reasoning parameters for the agent.
	Thinking *llm.ThinkingConfig
	// Hooks defines optional lifecycle callbacks for observing and customizing runs.
	Hooks Hooks
}

// Agent represents an autonomous AI agent that leverages a Large Language Model.
// It encapsulates instructions, swappable model dependencies, and execution methods.
type Agent struct {
	mu           sync.RWMutex
	id           string
	name         string
	description  string
	instructions string
	model        llm.LLM
	storage      storage.Storage
	tools        map[string]tool.Tool
	hooks        Hooks
	maxSteps     int
	thinking     *llm.ThinkingConfig
}

// New creates and initializes a new Agent with the given configuration.
func New(cfg Config) (*Agent, error) {
	if cfg.ID == "" {
		return nil, ErrIDRequired
	}
	if cfg.Model == nil {
		return nil, ErrModelRequired
	}
	name := cfg.Name
	if name == "" {
		name = cfg.ID
	}

	toolsMap := make(map[string]tool.Tool, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if t != nil {
			toolsMap[t.ID()] = t
		}
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	return &Agent{
		id:           cfg.ID,
		name:         name,
		description:  cfg.Description,
		instructions: cfg.Instructions,
		model:        cfg.Model,
		storage:      cfg.Storage,
		tools:        toolsMap,
		hooks:        cfg.Hooks,
		maxSteps:     maxSteps,
		thinking:     cfg.Thinking.Clone(),
	}, nil
}

// ID returns the unique ID of the agent.
func (a *Agent) ID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.id
}

// Name returns the name of the agent.
func (a *Agent) Name() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.name
}

// Description returns the description of the agent.
func (a *Agent) Description() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.description
}

// Instructions returns the agent's system instructions.
func (a *Agent) Instructions() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.instructions
}

// Model returns the current swappable LLM used by this agent.
func (a *Agent) Model() llm.LLM {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// Tools returns a snapshot slice of all registered tools on this agent.
func (a *Agent) Tools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	res := make([]tool.Tool, 0, len(a.tools))
	for _, t := range a.tools {
		res = append(res, t)
	}
	return res
}

// GetTool retrieves a registered tool by its unique identifier.
func (a *Agent) GetTool(id string) (tool.Tool, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	t, ok := a.tools[id]
	return t, ok
}

// AddTool registers a new tool on the agent.
func (a *Agent) AddTool(t tool.Tool) error {
	if t == nil {
		return ErrToolNil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tools[t.ID()] = t
	return nil
}

// RemoveTool unregisters a tool from the agent by ID.
func (a *Agent) RemoveTool(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tools, id)
}

// MaxSteps returns the default maximum number of ReAct loop iterations for this agent.
func (a *Agent) MaxSteps() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.maxSteps
}

// SetMaxSteps updates the default maximum number of ReAct loop iterations for this agent.
func (a *Agent) SetMaxSteps(n int) {
	if n <= 0 {
		n = defaultMaxSteps
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxSteps = n
}

// Thinking returns the default thinking configuration for this agent, or nil.
func (a *Agent) Thinking() *llm.ThinkingConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinking.Clone()
}

// SetThinking updates the default thinking configuration for this agent at runtime.
func (a *Agent) SetThinking(cfg *llm.ThinkingConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinking = cfg.Clone()
}

// Hooks returns the lifecycle hooks configured on this agent.
func (a *Agent) Hooks() Hooks {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hooks
}

// SetHooks replaces the lifecycle hooks configured on this agent at runtime.
// Per-call hooks supplied via WithHooks override matching callbacks per field.
func (a *Agent) SetHooks(h Hooks) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hooks = h
}

// SetModel swaps the LLM implementation for this agent at runtime.
func (a *Agent) SetModel(m llm.LLM) error {
	if m == nil {
		return ErrModelRequired
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = m
	return nil
}

// SetInstructions updates the system instructions for this agent at runtime.
func (a *Agent) SetInstructions(instructions string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.instructions = instructions
}

// Storage returns the configured storage backend for this agent, or nil.
func (a *Agent) Storage() storage.Storage {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.storage
}

// SetStorage sets or swaps the storage backend for this agent at runtime.
func (a *Agent) SetStorage(s storage.Storage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.storage = s
}

// Response represents the result of an agent generation run.
type Response struct {
	// Text contains the primary text output generated by the agent.
	Text string `json:"text"`
	// Thinking contains the reasoning or thought process generated by the model.
	Thinking string `json:"thinking,omitempty"`
	// Content contains the rich multipart components generated by the agent.
	Content []llm.Part `json:"content,omitempty"`
	// ToolCalls contains any tool calls requested by the model during generation.
	ToolCalls []llm.ToolCall `json:"tool_calls,omitempty"`
	// Messages contains the full conversation history including instructions, user prompt, and assistant response.
	Messages []llm.Message `json:"messages"`
	// FinishReason explains why generation ended (e.g. "stop", "length", "tool_calls").
	FinishReason llm.FinishReason `json:"finish_reason"`
	// Usage details token consumption for the request.
	Usage llm.Usage `json:"usage"`
	// Raw contains the underlying raw model output if available.
	Raw any `json:"raw,omitempty"`
}

// Unmarshal parses the JSON-encoded response text and stores the result in the value pointed to by target.
// Any markdown code fence blocks (```json ... ```) are cleanly stripped prior to deserialization.
func (r *Response) Unmarshal(target any) error {
	if r == nil {
		return errors.New("agent: response is nil")
	}
	cleaned := cleanJSONText(r.Text)
	if cleaned == "" {
		return errors.New("agent: response text is empty")
	}
	return json.Unmarshal([]byte(cleaned), target)
}

// cleanJSONText removes potential markdown code block wrappers (e.g. ```json ... ```) from model output.
func cleanJSONText(s string) string {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "```") {
		if idx := strings.Index(trimmed, "\n"); idx != -1 {
			trimmed = strings.TrimSpace(trimmed[idx+1:])
		}
		if strings.HasSuffix(trimmed, "```") {
			trimmed = strings.TrimSpace(trimmed[:len(trimmed)-3])
		}
	}
	return trimmed
}

// GenerateConfig holds runtime options for a Generate execution.
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

// StructuredResponse encapsulates an Agent response accompanied by the typed, unmarshaled data payload.
type StructuredResponse[T any] struct {
	Data     T
	Response *Response
}

// GenerateStructured executes an agent generation with structured output conforming to Go type T,
// unmarshaling the result directly into T.
func GenerateStructured[T any](ctx context.Context, a *Agent, prompt string, opts ...GenerateOption) (*StructuredResponse[T], error) {
	allOpts := append([]GenerateOption{WithOutputSchema[T]()}, opts...)
	res, err := a.Generate(ctx, prompt, allOpts...)
	if err != nil {
		return nil, err
	}
	var data T
	if err := res.Unmarshal(&data); err != nil {
		return &StructuredResponse[T]{Response: res}, fmt.Errorf("agent: failed to unmarshal response into %T: %w (raw response: %s)", data, err, res.Text)
	}
	return &StructuredResponse[T]{
		Data:     data,
		Response: res,
	}, nil
}

// GeneratePartsStructured executes an agent generation with multimodal parts and structured output conforming to Go type T.
func GeneratePartsStructured[T any](ctx context.Context, a *Agent, parts []llm.Part, opts ...GenerateOption) (*StructuredResponse[T], error) {
	allOpts := append([]GenerateOption{WithOutputSchema[T]()}, opts...)
	res, err := a.GenerateParts(ctx, parts, allOpts...)
	if err != nil {
		return nil, err
	}
	var data T
	if err := res.Unmarshal(&data); err != nil {
		return &StructuredResponse[T]{Response: res}, fmt.Errorf("agent: failed to unmarshal response into %T: %w (raw response: %s)", data, err, res.Text)
	}
	return &StructuredResponse[T]{
		Data:     data,
		Response: res,
	}, nil
}

// GenerateMessagesStructured executes an agent generation with chat messages and structured output conforming to Go type T.
func GenerateMessagesStructured[T any](ctx context.Context, a *Agent, messages []llm.Message, opts ...GenerateOption) (*StructuredResponse[T], error) {
	allOpts := append([]GenerateOption{WithOutputSchema[T]()}, opts...)
	res, err := a.GenerateMessages(ctx, messages, allOpts...)
	if err != nil {
		return nil, err
	}
	var data T
	if err := res.Unmarshal(&data); err != nil {
		return &StructuredResponse[T]{Response: res}, fmt.Errorf("agent: failed to unmarshal response into %T: %w (raw response: %s)", data, err, res.Text)
	}
	return &StructuredResponse[T]{
		Data:     data,
		Response: res,
	}, nil
}

// Generate runs the agent with a string prompt, returning the model's response.
func (a *Agent) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.GenerateMessages(ctx, userMsg, opts...)
}

// GenerateParts runs the agent with multimodal content parts directly.
func (a *Agent) GenerateParts(ctx context.Context, parts []llm.Part, opts ...GenerateOption) (*Response, error) {
	var userMsg []llm.Message
	if len(parts) > 0 {
		userMsg = []llm.Message{llm.UserMessageWithParts(parts...)}
	}
	return a.GenerateMessages(ctx, userMsg, opts...)
}

// GenerateMessages executes the agent with a slice of chat messages running the ReAct execution loop.
func (a *Agent) GenerateMessages(ctx context.Context, messages []llm.Message, opts ...GenerateOption) (*Response, error) {
	a.mu.RLock()
	activeModel := a.model
	activeStorage := a.storage
	instructions := a.instructions
	defaultSteps := a.maxSteps
	agentHooks := a.hooks
	agentThinking := a.thinking
	a.mu.RUnlock()

	genCfg := &GenerateConfig{
		Model:   activeModel,
		Storage: activeStorage,
	}
	for _, opt := range opts {
		opt(genCfg)
	}

	hooks := mergeHooks(agentHooks, genCfg.Hooks)

	if genCfg.Model == nil {
		return nil, ErrModelRequired
	}

	maxSteps := defaultSteps
	if genCfg.MaxSteps != nil && *genCfg.MaxSteps > 0 {
		maxSteps = *genCfg.MaxSteps
	}

	if len(genCfg.Media) > 0 {
		if len(messages) > 0 {
			lastIdx := len(messages) - 1
			if messages[lastIdx].Role == llm.RoleUser {
				messages[lastIdx].Content = append(messages[lastIdx].Content, genCfg.Media...)
			} else {
				messages = append(messages, llm.UserMessageWithParts(genCfg.Media...))
			}
		} else {
			messages = []llm.Message{llm.UserMessageWithParts(genCfg.Media...)}
		}
	}

	// If storage and thread ID are configured, load past messages and persist current turn
	if genCfg.Storage != nil && genCfg.ThreadID != "" {
		if _, err := genCfg.Storage.GetThreadByID(ctx, genCfg.ThreadID); errors.Is(err, storage.ErrThreadNotFound) {
			if _, err := genCfg.Storage.SaveThread(ctx, &storage.Thread{
				ID:         genCfg.ThreadID,
				ResourceID: genCfg.ResourceID,
			}); err != nil {
				hooks.storageError(ctx, opSaveThread, err)
			}
		} else if err != nil {
			hooks.storageError(ctx, opGetThread, err)
		}
		history, err := genCfg.Storage.ListMessages(ctx, storage.MessageFilter{
			ThreadID:  genCfg.ThreadID,
			SortOrder: storage.SortAsc,
		})
		if err != nil {
			hooks.storageError(ctx, opListMessages, err)
		} else if len(history) > 0 {
			genCfg.Messages = append(storage.ToLLMMessages(history), genCfg.Messages...)
		}
		if len(messages) > 0 {
			toSave := make([]*storage.Message, len(messages))
			for i, m := range messages {
				toSave[i] = storage.NewMessageFromLLM(genCfg.ThreadID, m)
				if genCfg.ResourceID != "" {
					toSave[i].ResourceID = genCfg.ResourceID
				}
			}
			if _, err := genCfg.Storage.SaveMessages(ctx, toSave); err != nil {
				hooks.storageError(ctx, opSaveMessages, err)
			}
		}
	}

	// Resolve effective thinking: generate config -> agent default
	effectiveThinking := genCfg.Thinking
	if effectiveThinking == nil {
		effectiveThinking = agentThinking
	}

	callMessages := a.prepareMessages(instructions, genCfg.Messages, messages)
	activeTools := a.collectTools(genCfg.Tools)
	toolDefs := tool.ToDefinitions(activeTools)

	var cumulativeUsage llm.Usage
	var lastRaw any

	for step := 0; step < maxSteps; step++ {
		req := llm.GenerateRequest{
			Messages:       callMessages,
			Tools:          toolDefs,
			Thinking:       effectiveThinking,
			ResponseFormat: genCfg.ResponseFormat,
			Temperature:    genCfg.Temperature,
			MaxTokens:      genCfg.MaxTokens,
			TopP:           genCfg.TopP,
		}

		if err := hooks.beforeModel(ctx, step, &req); err != nil {
			return nil, err
		}

		resp, err := genCfg.Model.Generate(ctx, req)
		hooks.afterModel(ctx, step, &req, resp, err)
		if err != nil {
			return nil, err
		}

		cumulativeUsage.PromptTokens += resp.Usage.PromptTokens
		cumulativeUsage.CompletionTokens += resp.Usage.CompletionTokens
		cumulativeUsage.TotalTokens += resp.Usage.TotalTokens
		cumulativeUsage.ReasoningTokens += resp.Usage.ReasoningTokens
		lastRaw = resp.Raw

		// Append the assistant's response to conversation messages
		assistantLLMMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   []llm.Part{llm.TextPart(resp.Text)},
			Thinking:  resp.Thinking,
			ToolCalls: resp.ToolCalls,
			Raw:       resp.Raw,
		}
		callMessages = append(callMessages, assistantLLMMsg)

		// If no tool calls were requested, the model provided its final response
		if len(resp.ToolCalls) == 0 {
			if genCfg.Storage != nil && genCfg.ThreadID != "" {
				assistantStored := storage.NewMessageFromLLM(genCfg.ThreadID, assistantLLMMsg)
				if genCfg.ResourceID != "" {
					assistantStored.ResourceID = genCfg.ResourceID
				}
				if _, err := genCfg.Storage.SaveMessages(ctx, []*storage.Message{assistantStored}); err != nil {
					hooks.storageError(ctx, opSaveMessages, err)
				}
			}
			return &Response{
				Text:         resp.Text,
				Thinking:     resp.Thinking,
				Content:      []llm.Part{llm.TextPart(resp.Text)},
				Messages:     callMessages,
				FinishReason: resp.FinishReason,
				Usage:        cumulativeUsage,
				Raw:          lastRaw,
			}, nil
		}

		// Persist assistant message with tool calls
		if genCfg.Storage != nil && genCfg.ThreadID != "" {
			assistantStored := storage.NewMessageFromLLM(genCfg.ThreadID, assistantLLMMsg)
			if genCfg.ResourceID != "" {
				assistantStored.ResourceID = genCfg.ResourceID
			}
			if _, err := genCfg.Storage.SaveMessages(ctx, []*storage.Message{assistantStored}); err != nil {
				hooks.storageError(ctx, opSaveMessages, err)
			}
		}

		// Execute tool calls
		for _, call := range resp.ToolCalls {
			_, toolParts, _ := a.executeToolCall(ctx, step, call, activeTools, hooks)
			toolLLMMsg := llm.Message{
				Role:       llm.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolParts,
			}
			callMessages = append(callMessages, toolLLMMsg)
			if genCfg.Storage != nil && genCfg.ThreadID != "" {
				toolStored := storage.NewMessageFromLLM(genCfg.ThreadID, toolLLMMsg)
				if genCfg.ResourceID != "" {
					toolStored.ResourceID = genCfg.ResourceID
				}
				if _, err := genCfg.Storage.SaveMessages(ctx, []*storage.Message{toolStored}); err != nil {
					hooks.storageError(ctx, opSaveMessages, err)
				}
			}
		}
	}

	// MaxSteps reached
	lastMsg := callMessages[len(callMessages)-1]
	return &Response{
		Text:         lastMsg.Text(),
		Thinking:     lastMsg.Thinking,
		Content:      lastMsg.Content,
		ToolCalls:    lastMsg.ToolCalls,
		Messages:     callMessages,
		FinishReason: llm.FinishReasonLength,
		Usage:        cumulativeUsage,
		Raw:          lastRaw,
	}, nil
}

// Stream runs the agent with a string prompt and returns an active StreamResponse.
func (a *Agent) Stream(ctx context.Context, prompt string, opts ...GenerateOption) (*StreamResponse, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{llm.UserMessage(prompt)}
	}
	return a.StreamMessages(ctx, userMsg, opts...)
}

// StreamParts initiates streaming with multimodal content parts directly.
func (a *Agent) StreamParts(ctx context.Context, parts []llm.Part, opts ...GenerateOption) (*StreamResponse, error) {
	var userMsg []llm.Message
	if len(parts) > 0 {
		userMsg = []llm.Message{llm.UserMessageWithParts(parts...)}
	}
	return a.StreamMessages(ctx, userMsg, opts...)
}

// StreamMessages executes the agent with a slice of chat messages and returns an active StreamResponse.
func (a *Agent) StreamMessages(ctx context.Context, messages []llm.Message, opts ...GenerateOption) (*StreamResponse, error) {
	a.mu.RLock()
	activeModel := a.model
	activeStorage := a.storage
	instructions := a.instructions
	defaultSteps := a.maxSteps
	agentHooks := a.hooks
	agentThinking := a.thinking
	a.mu.RUnlock()

	genCfg := &GenerateConfig{
		Model:   activeModel,
		Storage: activeStorage,
	}
	for _, opt := range opts {
		opt(genCfg)
	}

	hooks := mergeHooks(agentHooks, genCfg.Hooks)

	if genCfg.Model == nil {
		return nil, ErrModelRequired
	}

	maxSteps := defaultSteps
	if genCfg.MaxSteps != nil && *genCfg.MaxSteps > 0 {
		maxSteps = *genCfg.MaxSteps
	}

	if len(genCfg.Media) > 0 {
		if len(messages) > 0 {
			lastIdx := len(messages) - 1
			if messages[lastIdx].Role == llm.RoleUser {
				messages[lastIdx].Content = append(messages[lastIdx].Content, genCfg.Media...)
			} else {
				messages = append(messages, llm.UserMessageWithParts(genCfg.Media...))
			}
		} else {
			messages = []llm.Message{llm.UserMessageWithParts(genCfg.Media...)}
		}
	}

	if genCfg.Storage != nil && genCfg.ThreadID != "" {
		if _, err := genCfg.Storage.GetThreadByID(ctx, genCfg.ThreadID); errors.Is(err, storage.ErrThreadNotFound) {
			if _, err := genCfg.Storage.SaveThread(ctx, &storage.Thread{
				ID:         genCfg.ThreadID,
				ResourceID: genCfg.ResourceID,
			}); err != nil {
				hooks.storageError(ctx, opSaveThread, err)
			}
		} else if err != nil {
			hooks.storageError(ctx, opGetThread, err)
		}
		history, err := genCfg.Storage.ListMessages(ctx, storage.MessageFilter{
			ThreadID:  genCfg.ThreadID,
			SortOrder: storage.SortAsc,
		})
		if err != nil {
			hooks.storageError(ctx, opListMessages, err)
		} else if len(history) > 0 {
			genCfg.Messages = append(storage.ToLLMMessages(history), genCfg.Messages...)
		}
		if len(messages) > 0 {
			toSave := make([]*storage.Message, len(messages))
			for i, m := range messages {
				toSave[i] = storage.NewMessageFromLLM(genCfg.ThreadID, m)
				if genCfg.ResourceID != "" {
					toSave[i].ResourceID = genCfg.ResourceID
				}
			}
			if _, err := genCfg.Storage.SaveMessages(ctx, toSave); err != nil {
				hooks.storageError(ctx, opSaveMessages, err)
			}
		}
	}

	// Resolve effective thinking: generate config -> agent default
	effectiveThinking := genCfg.Thinking
	if effectiveThinking == nil {
		effectiveThinking = agentThinking
	}

	callMessages := a.prepareMessages(instructions, genCfg.Messages, messages)
	activeTools := a.collectTools(genCfg.Tools)
	toolDefs := tool.ToDefinitions(activeTools)

	req := llm.GenerateRequest{
		Messages:       callMessages,
		Tools:          toolDefs,
		Thinking:       effectiveThinking,
		ResponseFormat: genCfg.ResponseFormat,
		Temperature:    genCfg.Temperature,
		MaxTokens:      genCfg.MaxTokens,
		TopP:           genCfg.TopP,
	}

	if err := hooks.beforeModel(ctx, 0, &req); err != nil {
		return nil, err
	}

	rawStream, err := genCfg.Model.Stream(ctx, req)
	if err != nil {
		hooks.afterModel(ctx, 0, &req, nil, err)
		return nil, err
	}

	return newStreamResponse(ctx, a, genCfg, req, rawStream, callMessages, activeTools, toolDefs, maxSteps, hooks), nil
}

// executeToolCall executes a single tool call against the given tools, firing
// BeforeTool and AfterTool hooks, and returns the string output and multipart content delivered to the model.
func (a *Agent) executeToolCall(ctx context.Context, step int, call llm.ToolCall, activeTools []tool.Tool, hooks Hooks) (string, []llm.Part, error) {
	ev := &ToolEvent{Step: step, Call: &call}

	if hooks.BeforeTool != nil {
		if err := hooks.BeforeTool(ctx, ev); err != nil {
			ev.Output = fmt.Sprintf(`{"error":%q}`, err.Error())
			ev.Err = err
			ev.Skipped = true
			hooks.afterTool(ctx, ev)
			return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, err
		}
	}

	var target tool.Tool
	for _, t := range activeTools {
		if t != nil && t.ID() == call.Name {
			target = t
			break
		}
	}
	if target == nil {
		ev.Output = fmt.Sprintf(`{"error":"tool '%s' not found"}`, call.Name)
		ev.Err = fmt.Errorf("agent: tool %q not found", call.Name)
		hooks.afterTool(ctx, ev)
		return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, ev.Err
	}

	out, err := target.Execute(ctx, []byte(call.Arguments))
	if err != nil {
		ev.Output = fmt.Sprintf(`{"error":%q}`, err.Error())
		ev.Err = err
		hooks.afterTool(ctx, ev)
		return ev.Output, []llm.Part{llm.TextPart(ev.Output)}, err
	}

	var toolParts []llm.Part
	switch v := out.(type) {
	case tool.Result:
		toolParts = v.Content
		ev.Output = v.Text()
	case *tool.Result:
		if v != nil {
			toolParts = v.Content
			ev.Output = v.Text()
		}
	case llm.Part:
		toolParts = []llm.Part{v}
		ev.Output = v.Text
	case []llm.Part:
		toolParts = v
		ev.Output = extractTextFromParts(v)
	case string:
		toolParts = []llm.Part{llm.TextPart(v)}
		ev.Output = v
	case []byte:
		toolParts = []llm.Part{llm.TextPart(string(v))}
		ev.Output = string(v)
	default:
		b, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			s := fmt.Sprintf("%v", v)
			toolParts = []llm.Part{llm.TextPart(s)}
			ev.Output = s
		} else {
			toolParts = []llm.Part{llm.TextPart(string(b))}
			ev.Output = string(b)
		}
	}

	hooks.afterTool(ctx, ev)
	return ev.Output, toolParts, nil
}

func extractTextFromParts(parts []llm.Part) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == llm.PartTypeText && p.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// collectTools merges agent-level tools with any per-call extra tools.
func (a *Agent) collectTools(extraTools []tool.Tool) []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.tools) == 0 && len(extraTools) == 0 {
		return nil
	}

	merged := make(map[string]tool.Tool, len(a.tools)+len(extraTools))
	for k, v := range a.tools {
		merged[k] = v
	}
	for _, t := range extraTools {
		if t != nil {
			merged[t.ID()] = t
		}
	}

	result := make([]tool.Tool, 0, len(merged))
	for _, t := range merged {
		result = append(result, t)
	}
	return result
}

// prepareMessages constructs the initial messages slice consisting of system instructions,
// prior context messages, and the current turn's input messages.
func (a *Agent) prepareMessages(instructions string, priorMsgs, inputMsgs []llm.Message) []llm.Message {
	var callMessages []llm.Message
	if instructions != "" {
		callMessages = append(callMessages, llm.Message{
			Role:    llm.RoleSystem,
			Content: []llm.Part{llm.TextPart(instructions)},
		})
	}
	if len(priorMsgs) > 0 {
		callMessages = append(callMessages, priorMsgs...)
	}
	callMessages = append(callMessages, inputMsgs...)
	return callMessages
}
