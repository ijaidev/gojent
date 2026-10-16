// Package agent provides autonomous agent abstractions, configuration, and execution loops.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	maxSteps     int
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
		maxSteps:     maxSteps,
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

// GenerateConfig holds runtime options for a Generate execution.
type GenerateConfig struct {
	Model       llm.LLM
	Storage     storage.Storage
	ThreadID    string
	ResourceID  string
	Temperature *float64
	MaxTokens   *int
	TopP        *float64
	Messages    []llm.Message
	Tools       []tool.Tool
	MaxSteps    *int
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

// Generate runs the agent with a string prompt, returning the model's response.
func (a *Agent) Generate(ctx context.Context, prompt string, opts ...GenerateOption) (*Response, error) {
	var userMsg []llm.Message
	if prompt != "" {
		userMsg = []llm.Message{{
			Role:    llm.RoleUser,
			Content: prompt,
		}}
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
	a.mu.RUnlock()

	genCfg := &GenerateConfig{
		Model:   activeModel,
		Storage: activeStorage,
	}
	for _, opt := range opts {
		opt(genCfg)
	}

	if genCfg.Model == nil {
		return nil, ErrModelRequired
	}

	maxSteps := defaultSteps
	if genCfg.MaxSteps != nil && *genCfg.MaxSteps > 0 {
		maxSteps = *genCfg.MaxSteps
	}

	// If storage and thread ID are configured, load past messages and persist current turn
	if genCfg.Storage != nil && genCfg.ThreadID != "" {
		if _, err := genCfg.Storage.GetThreadByID(ctx, genCfg.ThreadID); errors.Is(err, storage.ErrThreadNotFound) {
			_, _ = genCfg.Storage.SaveThread(ctx, &storage.Thread{
				ID:         genCfg.ThreadID,
				ResourceID: genCfg.ResourceID,
			})
		}
		history, err := genCfg.Storage.ListMessages(ctx, storage.MessageFilter{
			ThreadID:  genCfg.ThreadID,
			SortOrder: storage.SortAsc,
		})
		if err == nil && len(history) > 0 {
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
			_, _ = genCfg.Storage.SaveMessages(ctx, toSave)
		}
	}

	callMessages := a.prepareMessages(instructions, genCfg.Messages, messages)
	activeTools := a.collectTools(genCfg.Tools)
	toolDefs := tool.ToDefinitions(activeTools)

	var cumulativeUsage llm.Usage
	var lastRaw any

	for step := 0; step < maxSteps; step++ {
		req := llm.GenerateRequest{
			Messages:    callMessages,
			Tools:       toolDefs,
			Temperature: genCfg.Temperature,
			MaxTokens:   genCfg.MaxTokens,
			TopP:        genCfg.TopP,
		}

		resp, err := genCfg.Model.Generate(ctx, req)
		if err != nil {
			return nil, err
		}

		cumulativeUsage.PromptTokens += resp.Usage.PromptTokens
		cumulativeUsage.CompletionTokens += resp.Usage.CompletionTokens
		cumulativeUsage.TotalTokens += resp.Usage.TotalTokens
		lastRaw = resp.Raw

		// Append the assistant's response to conversation messages
		assistantLLMMsg := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Text,
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
				_, _ = genCfg.Storage.SaveMessages(ctx, []*storage.Message{assistantStored})
			}
			return &Response{
				Text:         resp.Text,
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
			_, _ = genCfg.Storage.SaveMessages(ctx, []*storage.Message{assistantStored})
		}

		// Execute tool calls
		for _, call := range resp.ToolCalls {
			toolOutput := a.executeToolCall(ctx, call, activeTools)
			toolLLMMsg := llm.Message{
				Role:       llm.RoleTool,
				Name:       call.Name,
				ToolCallID: call.ID,
				Content:    toolOutput,
			}
			callMessages = append(callMessages, toolLLMMsg)
			if genCfg.Storage != nil && genCfg.ThreadID != "" {
				toolStored := storage.NewMessageFromLLM(genCfg.ThreadID, toolLLMMsg)
				if genCfg.ResourceID != "" {
					toolStored.ResourceID = genCfg.ResourceID
				}
				_, _ = genCfg.Storage.SaveMessages(ctx, []*storage.Message{toolStored})
			}
		}
	}

	// MaxSteps reached
	lastMsg := callMessages[len(callMessages)-1]
	return &Response{
		Text:         lastMsg.Content,
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
		userMsg = []llm.Message{{
			Role:    llm.RoleUser,
			Content: prompt,
		}}
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
	a.mu.RUnlock()

	genCfg := &GenerateConfig{
		Model:   activeModel,
		Storage: activeStorage,
	}
	for _, opt := range opts {
		opt(genCfg)
	}

	if genCfg.Model == nil {
		return nil, ErrModelRequired
	}

	maxSteps := defaultSteps
	if genCfg.MaxSteps != nil && *genCfg.MaxSteps > 0 {
		maxSteps = *genCfg.MaxSteps
	}

	if genCfg.Storage != nil && genCfg.ThreadID != "" {
		if _, err := genCfg.Storage.GetThreadByID(ctx, genCfg.ThreadID); errors.Is(err, storage.ErrThreadNotFound) {
			_, _ = genCfg.Storage.SaveThread(ctx, &storage.Thread{
				ID:         genCfg.ThreadID,
				ResourceID: genCfg.ResourceID,
			})
		}
		history, err := genCfg.Storage.ListMessages(ctx, storage.MessageFilter{
			ThreadID:  genCfg.ThreadID,
			SortOrder: storage.SortAsc,
		})
		if err == nil && len(history) > 0 {
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
			_, _ = genCfg.Storage.SaveMessages(ctx, toSave)
		}
	}

	callMessages := a.prepareMessages(instructions, genCfg.Messages, messages)
	activeTools := a.collectTools(genCfg.Tools)
	toolDefs := tool.ToDefinitions(activeTools)

	req := llm.GenerateRequest{
		Messages:    callMessages,
		Tools:       toolDefs,
		Temperature: genCfg.Temperature,
		MaxTokens:   genCfg.MaxTokens,
		TopP:        genCfg.TopP,
	}

	rawStream, err := genCfg.Model.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	return newStreamResponse(ctx, a, genCfg, rawStream, callMessages, activeTools, toolDefs, maxSteps), nil
}

// executeToolCall executes a single tool call against registered tools and returns its string output.
func (a *Agent) executeToolCall(ctx context.Context, call llm.ToolCall, activeTools []tool.Tool) string {
	var target tool.Tool
	for _, t := range activeTools {
		if t != nil && t.ID() == call.Name {
			target = t
			break
		}
	}
	if target == nil {
		return fmt.Sprintf(`{"error":"tool '%s' not found"}`, call.Name)
	}

	out, err := target.Execute(ctx, []byte(call.Arguments))
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}

	switch v := out.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		b, marshalErr := json.Marshal(v)
		if marshalErr != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
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
			Content: instructions,
		})
	}
	if len(priorMsgs) > 0 {
		callMessages = append(callMessages, priorMsgs...)
	}
	callMessages = append(callMessages, inputMsgs...)
	return callMessages
}
