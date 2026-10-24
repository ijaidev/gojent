package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
)

// Runner owns execution concerns: storage, policy, observation, approval,
// budgets, retries, and tool limits. It is safe for concurrent use.
type Runner struct {
	// Store is the optional conversation store.
	Store session.Store
	// Policy controls model and tool calls. An Agent policy is used when this
	// is nil.
	Policy Policy
	// Sink observes run events.
	Sink Sink
	// Approver resolves Ask decisions. Without one, Ask is treated as Deny.
	Approver Approver
	// Budget bounds the run.
	Budget Budget
	// Retry configures transient model retries.
	Retry RetryPolicy
	// ToolTimeout bounds each tool execution. Zero uses DefaultToolTimeout.
	// A negative value disables the timeout.
	ToolTimeout time.Duration
}

// RunOptions overrides model request and per-call settings for one run.
type RunOptions struct {
	// Model overrides the agent model.
	Model llm.Model
	// Tools adds or replaces agent tools by name.
	Tools []tool.Tool
	// Thinking overrides reasoning configuration.
	Thinking *llm.ThinkingConfig
	// ResponseFormat constrains structured output.
	ResponseFormat *llm.ResponseFormat
	// Temperature overrides sampling temperature.
	Temperature *float64
	// MaxTokens overrides the output token limit.
	MaxTokens *int
	// TopP overrides nucleus sampling.
	TopP *float64
	// MaxSteps overrides Runner.Budget.MaxSteps.
	MaxSteps int
	// Media attaches parts to the trailing user message.
	Media []llm.Part
	// ToolChoice constrains tool selection.
	ToolChoice llm.ToolChoice
	// Session supplies a session when the caller passes nil to RunWith.
	Session *session.Session
}

// Run executes an agent synchronously and returns its final response.
func (r *Runner) Run(ctx context.Context, a *Agent, s *session.Session, input ...llm.Message) (*Response, error) {
	return r.RunWith(ctx, a, s, RunOptions{}, input...)
}

// RunWith executes an agent with per-run options.
func (r *Runner) RunWith(ctx context.Context, a *Agent, s *session.Session, opts RunOptions, input ...llm.Message) (resp *Response, err error) {
	if r == nil {
		r = &Runner{}
	}
	if a == nil {
		return nil, ErrAgentRequired
	}
	model := opts.Model
	if model == nil {
		model = a.Model()
	}
	if model == nil {
		return nil, ErrModelRequired
	}
	if s == nil {
		s = opts.Session
	}
	if r.Store != nil {
		if s == nil {
			return nil, ErrSessionRequired
		}
		if s.ID == "" {
			return nil, ErrSessionIDRequired
		}
	}

	registry := newToolRegistry(a.Tools(), opts.Tools)
	policy := r.Policy
	if policy == nil {
		policy = a.Policy()
	}
	e := &execution{
		runner:     r,
		agent:      a,
		model:      model,
		registry:   registry,
		policy:     policy,
		sink:       r.Sink,
		approver:   r.Approver,
		session:    s,
		opts:       opts,
		usedCallID: make(map[string]bool),
	}
	return e.run(ctx, input)
}

type execution struct {
	runner     *Runner
	agent      *Agent
	model      llm.Model
	registry   *toolRegistry
	policy     Policy
	sink       Sink
	approver   Approver
	session    *session.Session
	opts       RunOptions
	usage      llm.Usage
	calls      []llm.ToolCall
	toolCalls  int
	usedCallID map[string]bool
}

func (e *execution) run(ctx context.Context, input []llm.Message) (resp *Response, err error) {
	input = mergeMedia(input, e.opts.Media)
	started := time.Now()
	runID := newRunID()
	emit := func(event Event) { safeEmit(e.sink, event) }
	emit(RunStart{RunID: runID, Agent: e.agent.Name(), Session: cloneSession(e.session), At: started})
	defer func() {
		emit(RunEnd{Response: resp, Err: err})
	}()

	var budgetCtx context.Context
	var cancel context.CancelFunc
	if e.runner.Budget.MaxDuration > 0 {
		budgetCtx, cancel = context.WithTimeout(ctx, e.runner.Budget.MaxDuration)
		defer cancel()
	} else {
		budgetCtx = ctx
	}

	conversation, err := e.load(budgetCtx)
	if err != nil {
		return nil, err
	}
	if len(input) > 0 {
		conversation = append(conversation, cloneMessages(input)...)
		if err := e.append(budgetCtx, input...); err != nil {
			return nil, err
		}
	}

	maxSteps := e.opts.MaxSteps
	if maxSteps <= 0 {
		maxSteps = e.runner.Budget.steps()
	}
	finish := llm.FinishReasonStop

	for step := 0; ; step++ {
		if err := budgetCtx.Err(); err != nil {
			return e.response(conversation, finish), e.contextError(budgetCtx)
		}
		if step >= maxSteps {
			return e.response(conversation, llm.FinishReasonLength), ErrMaxSteps
		}

		emit(StepStart{Step: step})
		request := e.request(conversation)
		if e.policy != nil {
			if policyErr := callPolicy(func() error {
				return e.policy.BeforeModel(budgetCtx, &ModelRequest{Step: step, Request: request})
			}); policyErr != nil {
				emit(StepEnd{Step: step, Err: policyErr})
				return nil, policyErr
			}
		}

		response, callErr := e.callModel(budgetCtx, request, step)
		if e.policy != nil {
			if policyErr := callPolicy(func() error {
				return e.policy.AfterModel(budgetCtx, &ModelResult{
					Step:     step,
					Request:  request,
					Response: response,
					Err:      callErr,
				})
			}); policyErr != nil {
				emit(StepEnd{Step: step, Err: policyErr})
				return nil, policyErr
			}
		}
		if callErr != nil {
			if budgetCtx.Err() != nil {
				callErr = e.contextError(budgetCtx)
			}
			emit(StepEnd{Step: step, Err: callErr})
			return nil, callErr
		}

		assistant := response.Message
		conversation = append(conversation, assistant)
		e.usage = e.usage.Add(response.Usage)
		e.calls = append(e.calls, assistant.ToolCalls...)
		finish = response.FinishReason

		if len(assistant.ToolCalls) == 0 {
			if err := e.append(budgetCtx, assistant); err != nil {
				return nil, err
			}
			emit(StepEnd{Step: step, Usage: response.Usage})
			if e.tokenBudgetExceeded() {
				return e.response(conversation, finish), fmt.Errorf("%w: %d tokens", ErrBudgetExceeded, e.usage.TotalTokens)
			}
			return e.response(conversation, finish), nil
		}

		results, budgetExceeded, toolsErr := e.runTools(budgetCtx, assistant.ToolCalls, step)
		if toolsErr != nil {
			emit(StepEnd{Step: step, Usage: response.Usage, Err: toolsErr})
			return nil, toolsErr
		}

		batch := make([]llm.Message, 0, len(results)+1)
		batch = append(batch, assistant)
		batch = append(batch, results...)
		if err := e.append(budgetCtx, batch...); err != nil {
			return nil, err
		}
		conversation = append(conversation, results...)
		emit(StepEnd{Step: step, Usage: response.Usage})

		if budgetExceeded {
			return e.response(conversation, llm.FinishReasonLength), fmt.Errorf("%w: %d tool calls", ErrBudgetExceeded, e.toolCalls)
		}
		if e.tokenBudgetExceeded() {
			return e.response(conversation, finish), fmt.Errorf("%w: %d tokens", ErrBudgetExceeded, e.usage.TotalTokens)
		}
		// Replay defense: a hung tool must not outlive its run.
		if err := budgetCtx.Err(); err != nil {
			return e.response(conversation, finish), e.contextError(budgetCtx)
		}
		if step+1 >= maxSteps {
			return e.response(conversation, llm.FinishReasonLength), ErrMaxSteps
		}
	}
}

func (e *execution) load(ctx context.Context) ([]llm.Message, error) {
	if e.runner.Store == nil || e.session == nil {
		return nil, nil
	}
	messages, err := e.runner.Store.Load(ctx, *e.session)
	if err != nil {
		return nil, fmt.Errorf("agent: load session: %w", err)
	}
	return messages, nil
}

func (e *execution) append(ctx context.Context, messages ...llm.Message) error {
	if e.runner.Store == nil || e.session == nil || len(messages) == 0 {
		return nil
	}
	if err := e.runner.Store.Append(ctx, *e.session, messages...); err != nil {
		return fmt.Errorf("agent: append session: %w", err)
	}
	return nil
}

func (e *execution) request(conversation []llm.Message) *llm.Request {
	messages := make([]llm.Message, 0, len(conversation)+1)
	if instructions := e.agent.Instructions(); instructions != "" {
		messages = append(messages, llm.SystemMessage(instructions))
	}
	for _, message := range conversation {
		if isEmptyAssistant(message) {
			continue
		}
		messages = append(messages, message)
	}
	return &llm.Request{
		Messages:       messages,
		Tools:          e.registry.definitions(),
		ToolChoice:     e.opts.ToolChoice,
		Thinking:       e.opts.Thinking.Clone(),
		ResponseFormat: e.opts.ResponseFormat.Clone(),
		Temperature:    cloneFloat(e.opts.Temperature),
		MaxTokens:      cloneInt(e.opts.MaxTokens),
		TopP:           cloneFloat(e.opts.TopP),
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

func (e *execution) response(conversation []llm.Message, finish llm.FinishReason) *Response {
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
	if instructions := e.agent.Instructions(); instructions != "" {
		display = append(display, llm.SystemMessage(instructions))
	}
	display = append(display, cloneMessages(conversation)...)
	return &Response{
		Text:         text,
		Thinking:     thinking,
		ToolCalls:    append([]llm.ToolCall(nil), e.calls...),
		Messages:     display,
		FinishReason: finish,
		Usage:        e.usage,
	}
}

func (e *execution) tokenBudgetExceeded() bool {
	maxTokens := e.runner.Budget.MaxTotalTokens
	return maxTokens > 0 && e.usage.TotalTokens > maxTokens
}

func (e *execution) contextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrBudgetExceeded, cause)
	}
	if cause == nil {
		cause = ctx.Err()
	}
	if cause == nil {
		return ErrRunCanceled
	}
	return cause
}

func (e *execution) callModel(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	attempts := e.runner.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := e.callModelOnce(ctx, request, step)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt == attempts || !retryable(err) {
			return nil, err
		}
		delay := e.runner.Retry.delay(attempt)
		if providerErr, ok := llm.AsError(err); ok && providerErr.RetryAfter > 0 {
			delay = providerErr.RetryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, e.contextError(ctx)
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (e *execution) callModelOnce(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	streamer, ok := e.model.(llm.Streamer)
	if !ok {
		response, err := callGenerate(ctx, e.model, request)
		if err != nil {
			return nil, err
		}
		message := e.finalizeAssistant(response.Message, step)
		emitMessageDeltasFromMessage(e.sink, step, message)
		safeEmit(e.sink, MessageEnd{Step: step, Message: &message})
		return &llm.Response{Message: message, FinishReason: response.FinishReason, Usage: response.Usage}, nil
	}

	stream, err := callStream(ctx, streamer, request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.Close()
	}()

	safeEmit(e.sink, MessageStart{Step: step})
	var text, thinking string
	var calls []llm.ToolCall
	var usage llm.Usage
	var finish llm.FinishReason
	var providerData *llm.ProviderData

	for {
		chunk, recvErr := recvStream(stream)
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, recvErr
		}
		if chunk.Delta != "" {
			text += chunk.Delta
			safeEmit(e.sink, TextDelta{Step: step, Text: chunk.Delta})
		}
		if chunk.ThinkingDelta != "" {
			thinking += chunk.ThinkingDelta
			safeEmit(e.sink, ThinkingDelta{Step: step, Text: chunk.ThinkingDelta})
		}
		if len(chunk.ToolCalls) > 0 {
			calls = accumulateToolCalls(calls, chunk.ToolCalls)
			for _, call := range chunk.ToolCalls {
				safeEmit(e.sink, ToolCallDelta{
					Step:      step,
					Index:     call.Index,
					ID:        call.ID,
					Name:      call.Name,
					Arguments: call.Arguments,
				})
			}
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = usage.Add(*chunk.Usage)
		}
		if chunk.ProviderData != nil {
			providerData = chunk.ProviderData.Clone()
		}
	}

	message := llm.Message{
		Role:         llm.RoleAssistant,
		Thinking:     thinking,
		ToolCalls:    calls,
		ProviderData: providerData,
	}
	if text != "" {
		message.Content = []llm.Part{llm.TextPart(text)}
	}
	message = e.finalizeAssistant(message, step)
	if finish == "" {
		if len(message.ToolCalls) > 0 {
			finish = llm.FinishReasonTool
		} else {
			finish = llm.FinishReasonStop
		}
	}
	safeEmit(e.sink, MessageEnd{Step: step, Message: &message})
	return &llm.Response{Message: message, FinishReason: finish, Usage: usage}, nil
}

func (e *execution) finalizeAssistant(message llm.Message, step int) llm.Message {
	message.Role = llm.RoleAssistant
	message.Content = llm.NonEmptyParts(message.Content)
	for i := range message.ToolCalls {
		call := &message.ToolCalls[i]
		call.Index = i
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		if call.ID == "" || e.usedCallID[call.ID] {
			call.ID = fmt.Sprintf("call_%d_%d", step, i)
		}
		e.usedCallID[call.ID] = true
	}
	return message
}

func (e *execution) runTools(ctx context.Context, calls []llm.ToolCall, step int) ([]llm.Message, bool, error) {
	results := make([]llm.Message, 0, len(calls))
	budgetExceeded := false

	for index, raw := range calls {
		call := tool.Call{
			ID:             raw.ID,
			Index:          index,
			Name:           raw.Name,
			Arguments:      json.RawMessage(raw.Arguments),
			IdempotencyKey: raw.ID,
		}
		spec := tool.Spec{}
		impl, found := e.registry.byName[call.Name]
		if found {
			spec = impl.Spec()
		}
		safeEmit(e.sink, ToolStart{Step: step, Call: call, Spec: spec})

		if e.runner.Budget.MaxToolCalls > 0 && e.toolCalls >= e.runner.Budget.MaxToolCalls {
			budgetExceeded = true
			message := toolErrorMessage(call, fmt.Errorf("%w: tool call budget exceeded", ErrBudgetExceeded))
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrBudgetExceeded})
			results = append(results, message)
			continue
		}

		decision, policyErr := e.beforeTool(ctx, step, call, spec)
		if policyErr != nil {
			return nil, false, policyErr
		}
		switch decision {
		case Terminate:
			return nil, false, ErrTerminated
		case Deny, Ask:
			message := toolErrorMessage(call, ErrToolDenied)
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrToolDenied})
			results = append(results, message)
			continue
		case Allow:
		default:
			message := toolErrorMessage(call, ErrToolDenied)
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrToolDenied})
			results = append(results, message)
			continue
		}

		if !found {
			err := fmt.Errorf("agent: tool %q not found", call.Name)
			message := toolErrorMessage(call, err)
			e.afterTool(ctx, step, call, nil, err)
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Message: &message, Err: err})
			results = append(results, message)
			continue
		}

		e.toolCalls++
		result, execErr := e.executeTool(ctx, impl, call)
		if afterErr := e.afterTool(ctx, step, call, &result, execErr); afterErr != nil {
			return nil, false, afterErr
		}
		if execErr != nil {
			message := toolErrorMessage(call, execErr)
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: execErr})
			results = append(results, message)
			continue
		}

		message, encodeErr := resultMessage(call, result)
		if encodeErr != nil {
			safeEmit(e.sink, ToolEnd{Step: step, Call: call, Result: &result, Err: encodeErr})
			message = toolErrorMessage(call, encodeErr)
		}
		safeEmit(e.sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: encodeErr})
		results = append(results, message)
	}
	return results, budgetExceeded, nil
}

func (e *execution) beforeTool(ctx context.Context, step int, call tool.Call, spec tool.Spec) (Decision, error) {
	if e.policy == nil {
		return Allow, nil
	}
	var decision Decision
	err := callPolicy(func() error {
		var policyErr error
		decision, policyErr = e.policy.BeforeTool(ctx, &ToolRequest{Step: step, Call: call, Spec: spec})
		return policyErr
	})
	if err != nil {
		return Deny, err
	}
	if decision == Ask {
		if e.approver == nil {
			return Deny, nil
		}
		approved, approveErr := callApprove(func() (bool, error) {
			return e.approver.Approve(ctx, ToolRequest{Step: step, Call: call, Spec: spec})
		})
		if approveErr != nil {
			return Deny, approveErr
		}
		if !approved {
			return Deny, nil
		}
		return Allow, nil
	}
	return decision, nil
}

func (e *execution) afterTool(ctx context.Context, step int, call tool.Call, result *tool.Result, err error) error {
	if e.policy == nil {
		return nil
	}
	return callPolicy(func() error {
		return e.policy.AfterTool(ctx, &ToolResult{Step: step, Call: call, Result: result, Err: err})
	})
}

func (e *execution) executeTool(ctx context.Context, impl tool.Tool, call tool.Call) (tool.Result, error) {
	timeout := e.runner.ToolTimeout
	if timeout == 0 {
		timeout = DefaultToolTimeout
	}
	if timeout <= 0 {
		return callTool(ctx, impl, call)
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		result tool.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- outcome{err: &PanicError{Value: recovered, Stack: debug.Stack()}}
			}
		}()
		result, err := impl.Execute(toolCtx, call)
		done <- outcome{result: result, err: err}
	}()

	select {
	case out := <-done:
		return out.result, out.err
	case <-toolCtx.Done():
		return tool.Result{}, fmt.Errorf("agent: tool %q: %w", call.Name, toolCtx.Err())
	}
}

func resultMessage(call tool.Call, result tool.Result) (llm.Message, error) {
	parts, err := result.Parts()
	if err != nil {
		return llm.Message{}, err
	}
	if len(parts) == 0 {
		parts = []llm.Part{llm.TextPart("{}")}
	}
	var data json.RawMessage
	if result.Data != nil {
		encoded, encodeErr := json.Marshal(result.Data)
		if encodeErr != nil {
			return llm.Message{}, fmt.Errorf("%w: %w", tool.ErrOutputEncode, encodeErr)
		}
		data = encoded
	}
	return llm.Message{
		Role:       llm.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    parts,
		Data:       data,
	}, nil
}

func toolErrorMessage(call tool.Call, err error) llm.Message {
	return llm.Message{
		Role:       llm.RoleTool,
		Name:       call.Name,
		ToolCallID: call.ID,
		Content:    []llm.Part{llm.TextPart(toolErrorPayload(err))},
		IsError:    true,
	}
}

// accumulateToolCalls merges streamed fragments by ID or index.
func accumulateToolCalls(existing []llm.ToolCall, incoming []llm.ToolCall) []llm.ToolCall {
	for _, fragment := range incoming {
		index := -1
		if fragment.ID != "" {
			for i := range existing {
				if existing[i].ID == fragment.ID {
					index = i
					break
				}
			}
		}
		if index < 0 && fragment.ID == "" {
			for i := range existing {
				if existing[i].Index == fragment.Index {
					index = i
					break
				}
			}
		}
		if index < 0 && fragment.ID != "" {
			for i := range existing {
				if existing[i].Index == fragment.Index && existing[i].ID == "" {
					index = i
					break
				}
			}
		}
		if index < 0 {
			existing = append(existing, fragment)
			continue
		}
		target := &existing[index]
		if target.ID == "" {
			target.ID = fragment.ID
		}
		if target.Name == "" {
			target.Name = fragment.Name
		}
		if fragment.Index != 0 || target.Index == 0 {
			target.Index = fragment.Index
		}
		target.Arguments += fragment.Arguments
	}
	return existing
}

func emitMessageDeltasFromMessage(sink Sink, step int, message llm.Message) {
	safeEmit(sink, MessageStart{Step: step})
	if text := message.Text(); text != "" {
		safeEmit(sink, TextDelta{Step: step, Text: text})
	}
	if message.Thinking != "" {
		safeEmit(sink, ThinkingDelta{Step: step, Text: message.Thinking})
	}
	for _, call := range message.ToolCalls {
		safeEmit(sink, ToolCallDelta{
			Step:      step,
			Index:     call.Index,
			ID:        call.ID,
			Name:      call.Name,
			Arguments: call.Arguments,
		})
	}
}

func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if providerErr, ok := llm.AsError(err); ok {
		return providerErr.Retryable()
	}
	return false
}

func safeEmit(sink Sink, event Event) {
	if sink == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	sink.Emit(event)
}

func callPolicy(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return fn()
}

func callApprove(fn func() (bool, error)) (approved bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return fn()
}

func callGenerate(ctx context.Context, model llm.Model, request *llm.Request) (response *llm.Response, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return model.Generate(ctx, request)
}

func callStream(ctx context.Context, streamer llm.Streamer, request *llm.Request) (stream llm.Stream, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return streamer.Stream(ctx, request)
}

func recvStream(stream llm.Stream) (chunk *llm.StreamChunk, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return stream.Recv()
}

func callTool(ctx context.Context, impl tool.Tool, call tool.Call) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return impl.Execute(ctx, call)
}

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
	merged := cloneMessages(messages)
	merged[last].Content = llm.NonEmptyParts(append(cloneParts(merged[last].Content), media...))
	return merged
}

func cloneMessages(messages []llm.Message) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]llm.Message, len(messages))
	for i, message := range messages {
		out[i] = message.Clone()
	}
	return out
}

func cloneParts(parts []llm.Part) []llm.Part {
	if len(parts) == 0 {
		return nil
	}
	out := make([]llm.Part, len(parts))
	for i, part := range parts {
		out[i] = part
		out[i].Data = append([]byte(nil), part.Data...)
	}
	return out
}

func cloneSession(s *session.Session) *session.Session {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func marshalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func newRunID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("run_%d", time.Now().UnixNano())
	}
	return "run_" + hex.EncodeToString(buf[:])
}
