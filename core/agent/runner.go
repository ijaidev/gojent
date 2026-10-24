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
	"slices"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/core/tool"
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

// RunOptions configures one agent run. It is the single public configuration
// type; execution internals stay unexported.
type RunOptions struct {
	// Session identifies the conversation. It is required when Store is set.
	Session *session.Session
	// Store persists the conversation. It is optional.
	Store session.Store
	// Policy controls model and tool calls. The agent policy is used when nil.
	Policy Policy
	// Approver resolves Ask decisions. Without one, Ask is treated as Deny.
	Approver Approver
	// Sink observes run events.
	Sink Sink
	// Budget bounds the run.
	Budget Budget
	// Retry configures transient model retries.
	Retry RetryPolicy
	// ToolTimeout bounds each tool execution. Zero uses DefaultToolTimeout.
	// A negative value disables the timeout.
	ToolTimeout time.Duration

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
	// Media attaches parts to the trailing user message.
	Media []llm.Part
	// ToolChoice constrains tool selection.
	ToolChoice llm.ToolChoice

	// optionErr records a failure from an Option so newRunner can return it.
	optionErr error
}

// errDurationBudget identifies the run's own duration deadline so it can be
// distinguished from a caller's context deadline.
var errDurationBudget = errors.New("agent: duration budget exceeded")

// newRunner resolves and validates the configuration for one execution.
func newRunner(a *Agent, opts RunOptions) (*runner, error) {
	if a == nil {
		return nil, ErrAgentRequired
	}
	opts = opts.snapshot()
	if opts.optionErr != nil {
		return nil, opts.optionErr
	}
	if err := validateTools(opts.Tools); err != nil {
		return nil, err
	}
	if opts.Model == nil {
		opts.Model = a.Model()
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
	if opts.Policy == nil {
		opts.Policy = a.Policy()
	}
	return &runner{
		agent:      a,
		opts:       opts,
		tools:      newToolRegistry(a.Tools(), opts.Tools),
		usedCallID: make(map[string]bool),
	}, nil
}

// snapshot copies the mutable parts of the configuration so a run is isolated
// from changes made by the caller after it starts.
func (o RunOptions) snapshot() RunOptions {
	o.Session = cloneSession(o.Session)
	o.Tools = slices.Clone(o.Tools)
	o.Thinking = o.Thinking.Clone()
	o.ResponseFormat = o.ResponseFormat.Clone()
	o.Temperature = cloneFloat(o.Temperature)
	o.MaxTokens = cloneInt(o.MaxTokens)
	o.TopP = cloneFloat(o.TopP)
	o.Media = cloneParts(o.Media)
	return o
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
		conversation = append(conversation, cloneMessages(input)...)
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
		if r.opts.Policy != nil {
			if policyErr := callPolicy(func() error {
				return r.opts.Policy.BeforeModel(budgetCtx, &ModelRequest{Step: step, Request: request})
			}); policyErr != nil {
				emit(StepEnd{Step: step, Err: policyErr})
				return nil, policyErr
			}
		}

		response, callErr := r.callModel(budgetCtx, request, step)
		if r.opts.Policy != nil {
			if policyErr := callPolicy(func() error {
				return r.opts.Policy.AfterModel(budgetCtx, &ModelResult{
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
				callErr = r.contextError(budgetCtx)
			}
			emit(StepEnd{Step: step, Err: callErr})
			return nil, callErr
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
		if step+1 >= maxSteps {
			return r.response(conversation, llm.FinishReasonLength), ErrMaxSteps
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
		if isEmptyAssistant(conversation[i]) {
			continue
		}
		messages = append(messages, conversation[i].Clone())
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
	display = append(display, cloneMessages(conversation)...)
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

func (r *runner) callModel(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	attempts := r.opts.Retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		response, err := r.callModelOnce(ctx, request, step)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if attempt == attempts || !retryable(err) {
			return nil, err
		}
		delay := r.opts.Retry.delay(attempt)
		if providerErr, ok := llm.AsError(err); ok && providerErr.RetryAfter > 0 {
			delay = providerErr.RetryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, r.contextError(ctx)
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (r *runner) callModelOnce(ctx context.Context, request *llm.Request, step int) (*llm.Response, error) {
	streamer, ok := r.opts.Model.(llm.Streamer)
	if !ok {
		response, err := callGenerate(ctx, r.opts.Model, request)
		if err != nil {
			return nil, err
		}
		message := r.finalizeAssistant(response.Message, step)
		emitMessageDeltasFromMessage(r.opts.Sink, step, message)
		safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
		return &llm.Response{Message: message, FinishReason: response.FinishReason, Usage: response.Usage}, nil
	}

	stream, err := callStream(ctx, streamer, request)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stream.Close()
	}()

	safeEmit(r.opts.Sink, MessageStart{Step: step})
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
			safeEmit(r.opts.Sink, TextDelta{Step: step, Text: chunk.Delta})
		}
		if chunk.ThinkingDelta != "" {
			thinking += chunk.ThinkingDelta
			safeEmit(r.opts.Sink, ThinkingDelta{Step: step, Text: chunk.ThinkingDelta})
		}
		if len(chunk.ToolCalls) > 0 {
			calls = accumulateToolCalls(calls, chunk.ToolCalls)
			for _, call := range chunk.ToolCalls {
				safeEmit(r.opts.Sink, ToolCallDelta{
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
	message = r.finalizeAssistant(message, step)
	if finish == "" {
		if len(message.ToolCalls) > 0 {
			finish = llm.FinishReasonTool
		} else {
			finish = llm.FinishReasonStop
		}
	}
	safeEmit(r.opts.Sink, MessageEnd{Step: step, Message: &message})
	return &llm.Response{Message: message, FinishReason: finish, Usage: usage}, nil
}

func (r *runner) finalizeAssistant(message llm.Message, step int) llm.Message {
	message.Role = llm.RoleAssistant
	message.Content = llm.NonEmptyParts(message.Content)
	for i := range message.ToolCalls {
		call := &message.ToolCalls[i]
		call.Index = i
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		if call.ID == "" || r.usedCallID[call.ID] {
			call.ID = fmt.Sprintf("call_%d_%d", step, i)
		}
		r.usedCallID[call.ID] = true
	}
	return message
}

func (r *runner) runTools(ctx context.Context, calls []llm.ToolCall, step int) ([]llm.Message, bool, error) {
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
		impl, found := r.tools.byName[call.Name]
		if found {
			spec = impl.Spec()
		}
		safeEmit(r.opts.Sink, ToolStart{Step: step, Call: call, Spec: spec})

		if r.opts.Budget.MaxToolCalls > 0 && r.toolCalls >= r.opts.Budget.MaxToolCalls {
			budgetExceeded = true
			message := toolErrorMessage(call, fmt.Errorf("%w: tool call budget exceeded", ErrBudgetExceeded))
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrBudgetExceeded})
			results = append(results, message)
			continue
		}

		decision, policyErr := r.beforeTool(ctx, step, call, spec)
		if policyErr != nil {
			return nil, false, policyErr
		}
		switch decision {
		case Terminate:
			return nil, false, ErrTerminated
		case Deny, Ask:
			message := toolErrorMessage(call, ErrToolDenied)
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrToolDenied})
			results = append(results, message)
			continue
		case Allow:
		default:
			message := toolErrorMessage(call, ErrToolDenied)
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: ErrToolDenied})
			results = append(results, message)
			continue
		}

		if !found {
			err := fmt.Errorf("agent: tool %q not found", call.Name)
			message := toolErrorMessage(call, err)
			if afterErr := r.afterTool(ctx, step, call, nil, err); afterErr != nil {
				return nil, false, afterErr
			}
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Message: &message, Err: err})
			results = append(results, message)
			continue
		}

		r.toolCalls++
		result, execErr := r.executeTool(ctx, impl, call)
		if afterErr := r.afterTool(ctx, step, call, &result, execErr); afterErr != nil {
			return nil, false, afterErr
		}
		if execErr != nil {
			message := toolErrorMessage(call, execErr)
			safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: execErr})
			results = append(results, message)
			continue
		}

		message, encodeErr := resultMessage(call, result)
		if encodeErr != nil {
			message = toolErrorMessage(call, encodeErr)
		}
		safeEmit(r.opts.Sink, ToolEnd{Step: step, Call: call, Result: &result, Message: &message, Err: encodeErr})
		results = append(results, message)
	}
	return results, budgetExceeded, nil
}

func (r *runner) beforeTool(ctx context.Context, step int, call tool.Call, spec tool.Spec) (Decision, error) {
	if r.opts.Policy == nil {
		return Allow, nil
	}
	var decision Decision
	err := callPolicy(func() error {
		var policyErr error
		decision, policyErr = r.opts.Policy.BeforeTool(ctx, &ToolRequest{Step: step, Call: call, Spec: spec})
		return policyErr
	})
	if err != nil {
		return Deny, err
	}
	if decision == Ask {
		if r.opts.Approver == nil {
			return Deny, nil
		}
		approved, approveErr := callApprove(func() (bool, error) {
			return r.opts.Approver.Approve(ctx, ToolRequest{Step: step, Call: call, Spec: spec})
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

func (r *runner) afterTool(ctx context.Context, step int, call tool.Call, result *tool.Result, err error) error {
	if r.opts.Policy == nil {
		return nil
	}
	return callPolicy(func() error {
		return r.opts.Policy.AfterTool(ctx, &ToolResult{Step: step, Call: call, Result: result, Err: err})
	})
}

func (r *runner) executeTool(ctx context.Context, impl tool.Tool, call tool.Call) (tool.Result, error) {
	timeout := r.opts.ToolTimeout
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
func accumulateToolCalls(existing, incoming []llm.ToolCall) []llm.ToolCall {
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
	for i := range messages {
		out[i] = messages[i].Clone()
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
