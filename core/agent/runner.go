package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ijaidev/gojent/core/llm"
)

// runner is one agent execution. opts is the normalized run configuration and
// tools is the merged agent and per-run tool set; everything else is mutable
// state for this run. It stays private so Agent remains the public execution
// surface.
type runner struct {
	agent *Agent
	opts  RunOptions
	tools *toolRegistry

	usage      llm.Usage
	calls      []llm.ToolCall
	toolCalls  int
	usedCallID map[string]bool
}

// newRunner resolves and validates the configuration for one execution.
func newRunner(a *Agent, opts RunOptions) (*runner, error) {
	if a == nil {
		return nil, ErrAgentRequired
	}
	opts = opts.snapshot()
	if opts.optionErr != nil {
		return nil, opts.optionErr
	}
	opts = a.ApplyAgentOptions(opts)
	if err := validateTools(opts.Tools); err != nil {
		return nil, err
	}
	if opts.Model == nil {
		return nil, ErrModelRequired
	}
	if opts.Store != nil {
		if opts.Session == nil {
			return nil, ErrSessionRequired
		}
		if opts.Session.ID == "" {
			return nil, ErrSessionIDRequired
		}
	}
	return &runner{
		agent:      a,
		opts:       opts,
		tools:      newToolRegistry(a.Tools(), opts.Tools),
		usedCallID: make(map[string]bool),
	}, nil
}

func (r *runner) run(ctx context.Context, input ...llm.Message) (resp *Response, err error) {
	input = mergeMedia(input, r.opts.Media)
	started := time.Now()
	runID := newRunID()
	emit := func(event Event) { safeEmit(r.opts.Sink, event) }
	emit(RunStart{RunID: runID, Agent: r.agent.Name(), Session: cloneSession(r.opts.Session), At: started})
	defer func() {
		emit(RunEnd{Response: resp, Err: err})
	}()
	// A panic in a dependency must not escape an asynchronous run. Convert it
	// to an error before RunEnd so observers see the failure.
	defer func() {
		if recovered := recover(); recovered != nil {
			resp = nil
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()

	var budgetCtx context.Context
	var cancel context.CancelFunc
	if r.opts.Budget.MaxDuration > 0 {
		budgetCtx, cancel = context.WithTimeoutCause(ctx, r.opts.Budget.MaxDuration, errDurationBudget)
		defer cancel()
	} else {
		budgetCtx = ctx
	}

	conversation, err := r.load(budgetCtx)
	if err != nil {
		return nil, err
	}
	if len(input) > 0 {
		conversation = append(conversation, llm.CloneMessages(input)...)
		if err := r.append(budgetCtx, input...); err != nil {
			return nil, err
		}
	}

	maxSteps := r.opts.Budget.steps()
	finish := llm.FinishReasonStop

	for step := 0; ; step++ {
		if err := budgetCtx.Err(); err != nil {
			return r.response(conversation, finish), r.contextError(budgetCtx)
		}
		if step >= maxSteps {
			return r.response(conversation, llm.FinishReasonLength), ErrMaxSteps
		}

		emit(StepStart{Step: step})
		request := r.request(conversation)
		response, modelErr := r.modelStep(budgetCtx, request, step)
		if modelErr != nil {
			emit(StepEnd{Step: step, Err: modelErr})
			return nil, modelErr
		}

		assistant := response.Message
		conversation = append(conversation, assistant)
		r.usage = r.usage.Add(response.Usage)
		r.calls = append(r.calls, assistant.ToolCalls...)
		finish = response.FinishReason

		if len(assistant.ToolCalls) == 0 {
			if err := r.append(budgetCtx, assistant); err != nil {
				return nil, err
			}
			emit(StepEnd{Step: step, Usage: response.Usage})
			if r.tokenBudgetExceeded() {
				return r.response(conversation, finish), fmt.Errorf("%w: %d tokens", ErrBudgetExceeded, r.usage.TotalTokens)
			}
			return r.response(conversation, finish), nil
		}

		results, budgetExceeded, toolsErr := r.runTools(budgetCtx, assistant.ToolCalls, step)
		if toolsErr != nil {
			emit(StepEnd{Step: step, Usage: response.Usage, Err: toolsErr})
			return nil, toolsErr
		}

		batch := make([]llm.Message, 0, len(results)+1)
		batch = append(batch, assistant)
		batch = append(batch, results...)
		if err := r.append(budgetCtx, batch...); err != nil {
			return nil, err
		}
		conversation = append(conversation, results...)
		emit(StepEnd{Step: step, Usage: response.Usage})

		if budgetExceeded {
			return r.response(conversation, llm.FinishReasonLength), fmt.Errorf("%w: %d tool calls", ErrBudgetExceeded, r.toolCalls)
		}
		if r.tokenBudgetExceeded() {
			return r.response(conversation, finish), fmt.Errorf("%w: %d tokens", ErrBudgetExceeded, r.usage.TotalTokens)
		}
		// Replay defense: a hung tool must not outlive its run.
		if err := budgetCtx.Err(); err != nil {
			return r.response(conversation, finish), r.contextError(budgetCtx)
		}
	}
}

func (r *runner) load(ctx context.Context) ([]llm.Message, error) {
	if r.opts.Store == nil || r.opts.Session == nil {
		return nil, nil
	}
	messages, err := r.opts.Store.Load(ctx, *r.opts.Session)
	if err != nil {
		return nil, fmt.Errorf("agent: load session: %w", err)
	}
	return messages, nil
}

func (r *runner) append(ctx context.Context, messages ...llm.Message) error {
	if r.opts.Store == nil || r.opts.Session == nil || len(messages) == 0 {
		return nil
	}
	if err := r.opts.Store.Append(ctx, *r.opts.Session, messages...); err != nil {
		return fmt.Errorf("agent: append session: %w", err)
	}
	return nil
}

func (r *runner) request(conversation []llm.Message) *llm.Request {
	messages := make([]llm.Message, 0, len(conversation)+1)
	if instructions := r.agent.Instructions(); instructions != "" {
		messages = append(messages, llm.SystemMessage(instructions))
	}
	for i := range conversation {
		message := conversation[i].Clone()
		message.Content = llm.NonEmptyParts(message.Content)
		if isEmptyAssistant(message) {
			continue
		}
		messages = append(messages, message)
	}
	return &llm.Request{
		Messages:       messages,
		Tools:          r.tools.definitions(),
		ToolChoice:     r.opts.ToolChoice,
		Thinking:       r.opts.Thinking.Clone(),
		ResponseFormat: r.opts.ResponseFormat.Clone(),
		Temperature:    cloneFloat(r.opts.Temperature),
		MaxTokens:      cloneInt(r.opts.MaxTokens),
		TopP:           cloneFloat(r.opts.TopP),
	}
}

// isEmptyAssistant reports whether an assistant message carries nothing a
// provider can replay. Persisted empty responses are harmless, but sending
// them back to a provider is not portable.
func isEmptyAssistant(message llm.Message) bool {
	return message.Role == llm.RoleAssistant &&
		len(message.Content) == 0 &&
		len(message.ToolCalls) == 0 &&
		message.Thinking == "" &&
		message.ProviderData == nil
}

func (r *runner) response(conversation []llm.Message, finish llm.FinishReason) *Response {
	text := ""
	thinking := ""
	if len(conversation) > 0 {
		last := conversation[len(conversation)-1]
		if last.Role == llm.RoleAssistant {
			text = last.Text()
			thinking = last.Thinking
		}
	}
	display := make([]llm.Message, 0, len(conversation)+1)
	if instructions := r.agent.Instructions(); instructions != "" {
		display = append(display, llm.SystemMessage(instructions))
	}
	display = append(display, llm.CloneMessages(conversation)...)
	return &Response{
		Text:         text,
		Thinking:     thinking,
		ToolCalls:    append([]llm.ToolCall(nil), r.calls...),
		Messages:     display,
		FinishReason: finish,
		Usage:        r.usage,
	}
}

func (r *runner) tokenBudgetExceeded() bool {
	maxTokens := r.opts.Budget.MaxTotalTokens
	return maxTokens > 0 && r.usage.TotalTokens > maxTokens
}

func (r *runner) contextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, errDurationBudget) {
		return fmt.Errorf("%w: duration %s", ErrBudgetExceeded, r.opts.Budget.MaxDuration)
	}
	if cause != nil {
		return cause
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrRunCanceled
}

func newRunID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("run_%d", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(buf[:])
}
