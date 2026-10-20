package agent

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/core/tool"
)

// run holds the resolved state of a single Generate or Stream execution.
// Both entry points build it once, so option handling, the storage bootstrap,
// thinking resolution, and tool collection live in exactly one place.
type run struct {
	agent *Agent
	cfg   *GenerateConfig

	// Resolved execution state, snapshotted before the run starts.
	model    llm.LLM
	hooks    Hooks
	maxSteps int
	thinking *llm.ThinkingConfig

	// Conversation state, grown across ReAct steps.
	messages []llm.Message
	tools    []tool.Tool
	toolDefs []llm.ToolDefinition
}

// newRun snapshots agent state, applies per-call options, resolves the
// thinking configuration and tool set, and hydrates conversation history from
// storage when a thread is configured.
func (a *Agent) newRun(ctx context.Context, messages []llm.Message, opts []GenerateOption) (*run, error) {
	a.mu.RLock()
	activeModel := a.model
	activeStorage := a.storage
	instructions := a.instructions
	defaultSteps := a.maxSteps
	agentHooks := a.hooks
	agentThinking := a.thinking
	a.mu.RUnlock()

	cfg := &GenerateConfig{
		Model:   activeModel,
		Storage: activeStorage,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	hooks := mergeHooks(agentHooks, cfg.Hooks)

	if cfg.Model == nil {
		return nil, ErrModelRequired
	}

	maxSteps := defaultSteps
	if cfg.MaxSteps != nil && *cfg.MaxSteps > 0 {
		maxSteps = *cfg.MaxSteps
	}

	r := &run{
		agent:    a,
		cfg:      cfg,
		model:    cfg.Model,
		hooks:    hooks,
		maxSteps: maxSteps,
		thinking: cfg.Thinking,
	}
	if r.thinking == nil {
		r.thinking = agentThinking
	}

	messages = mergeMedia(messages, cfg.Media)

	if r.persists() {
		r.ensureThread(ctx)
		if history := r.loadHistory(ctx); len(history) > 0 {
			cfg.Messages = append(history, cfg.Messages...)
		}
		if len(messages) > 0 {
			r.saveMessages(ctx, messages...)
		}
	}

	r.messages = prepareMessages(instructions, cfg.Messages, messages)
	r.tools = a.collectTools(cfg.Tools)
	r.toolDefs = tool.ToDefinitions(r.tools)
	return r, nil
}

// persists reports whether conversation messages should be persisted for this run.
func (r *run) persists() bool {
	return r.cfg.Storage != nil && r.cfg.ThreadID != ""
}

// request builds the generation request for the given messages.
func (r *run) request(messages []llm.Message) llm.GenerateRequest {
	return llm.GenerateRequest{
		Messages:       messages,
		Tools:          r.toolDefs,
		Thinking:       r.thinking,
		ResponseFormat: r.cfg.ResponseFormat,
		Temperature:    r.cfg.Temperature,
		MaxTokens:      r.cfg.MaxTokens,
		TopP:           r.cfg.TopP,
	}
}

// saveMessages persists messages to the configured thread. Storage failures
// are reported through the storage-error hook and never abort the run.
func (r *run) saveMessages(ctx context.Context, msgs ...llm.Message) {
	if !r.persists() || len(msgs) == 0 {
		return
	}

	toSave := make([]*storage.Message, len(msgs))
	for i := range msgs {
		toSave[i] = storage.NewMessageFromLLM(r.cfg.ThreadID, msgs[i])
		if r.cfg.ResourceID != "" {
			toSave[i].ResourceID = r.cfg.ResourceID
		}
	}
	if _, err := r.cfg.Storage.SaveMessages(ctx, toSave); err != nil {
		r.hooks.storageError(ctx, opSaveMessages, err)
	}
}

// ensureThread creates the conversation thread when it does not exist yet.
func (r *run) ensureThread(ctx context.Context) {
	if _, err := r.cfg.Storage.GetThreadByID(ctx, r.cfg.ThreadID); errors.Is(err, storage.ErrThreadNotFound) {
		if _, err := r.cfg.Storage.SaveThread(ctx, &storage.Thread{
			ID:         r.cfg.ThreadID,
			ResourceID: r.cfg.ResourceID,
		}); err != nil {
			r.hooks.storageError(ctx, opSaveThread, err)
		}
	} else if err != nil {
		r.hooks.storageError(ctx, opGetThread, err)
	}
}

// loadHistory returns the thread's prior messages in chronological order.
func (r *run) loadHistory(ctx context.Context) []llm.Message {
	history, err := r.cfg.Storage.ListMessages(ctx, storage.MessageFilter{
		ThreadID:  r.cfg.ThreadID,
		SortOrder: storage.SortAsc,
	})
	if err != nil {
		r.hooks.storageError(ctx, opListMessages, err)
		return nil
	}
	return storage.ToLLMMessages(history)
}

// mergeMedia attaches media parts to the trailing user message, or appends a
// new user message when the conversation does not end with one. The caller's
// messages are never mutated.
func mergeMedia(messages []llm.Message, media []llm.Part) []llm.Message {
	if len(media) == 0 {
		return messages
	}
	if len(messages) == 0 {
		return []llm.Message{llm.UserMessageWithParts(media...)}
	}
	last := len(messages) - 1
	if messages[last].Role != llm.RoleUser {
		merged := make([]llm.Message, 0, len(messages)+1)
		merged = append(merged, messages...)
		return append(merged, llm.UserMessageWithParts(media...))
	}

	merged := make([]llm.Message, len(messages))
	copy(merged, messages)
	merged[last].Content = append(slices.Clone(merged[last].Content), media...)
	return merged
}

// prepareMessages constructs the initial messages slice consisting of system
// instructions, prior context messages, and the current turn's input messages.
func prepareMessages(instructions string, priorMsgs, inputMsgs []llm.Message) []llm.Message {
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
		if !tool.IsNilTool(t) {
			merged[t.ID()] = t
		}
	}

	result := make([]tool.Tool, 0, len(merged))
	for _, t := range merged {
		result = append(result, t)
	}
	// Sort by ID so tool definitions are stable across runs. Map iteration
	// order would otherwise leak into the request and vary between runs,
	// breaking prompt caching and reproducible transcripts.
	slices.SortFunc(result, func(a, b tool.Tool) int {
		return cmp.Compare(a.ID(), b.ID())
	})
	return result
}
