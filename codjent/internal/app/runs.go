package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ijaidev/gojent/core/agent"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

// RunStatus is the lifecycle state of an agent run.
type RunStatus string

// Run lifecycle states.
const (
	RunStarting        RunStatus = "starting"
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunCompleted       RunStatus = "completed"
	RunFailed          RunStatus = "failed"
	RunCanceled        RunStatus = "canceled"
)

// PendingApproval is a tool call waiting for an allow/deny decision.
type PendingApproval struct {
	ID         string    `json:"id"`
	ToolCallID string    `json:"tool_call_id"`
	ToolName   string    `json:"tool_name"`
	Arguments  string    `json:"arguments"`
	CreatedAt  time.Time `json:"created_at"`
}

// ApprovalDecision is the client's response to a pending approval.
type ApprovalDecision struct {
	Allow bool   `json:"allow"`
	Note  string `json:"note,omitempty"`
}

// RunInfo is the API-facing snapshot of a run.
type RunInfo struct {
	ID              string           `json:"id"`
	ThreadID        string           `json:"thread_id"`
	Status          RunStatus        `json:"status"`
	Error           string           `json:"error,omitempty"`
	PendingApproval *PendingApproval `json:"pending_approval,omitempty"`
	Usage           llm.Usage        `json:"usage"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type run struct {
	id       string
	threadID string
	thread   *Thread
	history  []*Message
	user     *Message

	ctx    context.Context
	cancel context.CancelFunc
	bus    *eventBus

	mu        sync.Mutex
	status    RunStatus
	err       error
	pending   *pendingApproval
	usage     llm.Usage
	createdAt time.Time
	updatedAt time.Time
}

type pendingApproval struct {
	request  PendingApproval
	decision chan ApprovalDecision
}

func newRun(ctx context.Context, thread *Thread, history []*Message, user *Message) *run {
	cancelCtx, cancel := context.WithCancel(ctx)
	now := nowUTC()
	id := newID("run")
	return &run{
		id:        id,
		threadID:  thread.ID,
		thread:    thread,
		history:   history,
		user:      user,
		ctx:       cancelCtx,
		cancel:    cancel,
		bus:       newEventBus(id),
		status:    RunStarting,
		createdAt: now,
		updatedAt: now,
	}
}

func (r *run) info() *RunInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	info := &RunInfo{
		ID:        r.id,
		ThreadID:  r.threadID,
		Status:    r.status,
		Usage:     r.usage,
		CreatedAt: r.createdAt,
		UpdatedAt: r.updatedAt,
	}
	if r.err != nil {
		info.Error = r.err.Error()
	}
	if r.pending != nil {
		pending := r.pending.request
		info.PendingApproval = &pending
	}
	return info
}

func (r *run) emit(eventType string, data any) {
	r.bus.emit(eventType, data)
}

func (r *run) setStatus(status RunStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
	r.updatedAt = nowUTC()
}

func (r *run) finish(status RunStatus, err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalLocked() {
		return false
	}
	r.status = status
	r.err = err
	r.pending = nil
	r.updatedAt = nowUTC()
	return true
}

func (r *run) terminalLocked() bool {
	return r.status == RunCompleted || r.status == RunFailed || r.status == RunCanceled
}

func (r *run) complete(resp *agent.Response, messageIDs []string) {
	r.mu.Lock()
	if r.terminalLocked() {
		r.mu.Unlock()
		return
	}
	r.status = RunCompleted
	r.usage = resp.Usage
	r.err = nil
	r.pending = nil
	r.updatedAt = nowUTC()
	r.mu.Unlock()

	r.emit(EventRunCompleted, RunCompletedEvent{
		Usage:        resp.Usage,
		FinishReason: resp.FinishReason,
		MessageIDs:   messageIDs,
	})
}

func (r *run) fail(err error) {
	if !r.finish(RunFailed, err) {
		return
	}
	r.emit(EventRunFailed, RunFailedEvent{Error: err.Error()})
}

func (r *run) canceled() {
	if !r.finish(RunCanceled, nil) {
		return
	}
	r.emit(EventRunCanceled, RunCanceledEvent{Reason: "run canceled"})
}

func (r *run) terminal() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.terminalLocked()
}

func (r *run) resolveApproval(approvalID string, decision ApprovalDecision) error {
	r.mu.Lock()
	if r.status != RunWaitingApproval {
		r.mu.Unlock()
		return ErrRunFinished
	}
	pending := r.pending
	if pending == nil || pending.request.ID != approvalID {
		r.mu.Unlock()
		return ErrApprovalNotFound
	}
	r.pending = nil
	r.mu.Unlock()

	pending.decision <- decision
	return nil
}

func (r *run) awaitApproval(ctx context.Context, req PendingApproval) (ApprovalDecision, error) {
	pending := &pendingApproval{
		request:  req,
		decision: make(chan ApprovalDecision, 1),
	}

	r.mu.Lock()
	r.pending = pending
	r.status = RunWaitingApproval
	r.updatedAt = nowUTC()
	r.mu.Unlock()

	r.emit(EventApprovalRequired, ApprovalRequiredEvent{Approval: req})

	select {
	case decision := <-pending.decision:
		r.mu.Lock()
		if r.pending == pending {
			r.pending = nil
		}
		r.status = RunRunning
		r.updatedAt = nowUTC()
		r.mu.Unlock()
		r.emit(EventApprovalResolved, ApprovalResolvedEvent{
			ApprovalID: req.ID,
			Allow:      decision.Allow,
			Note:       decision.Note,
		})
		return decision, nil
	case <-ctx.Done():
		r.mu.Lock()
		if r.pending == pending {
			r.pending = nil
		}
		r.updatedAt = nowUTC()
		r.mu.Unlock()
		r.emit(EventApprovalResolved, ApprovalResolvedEvent{
			ApprovalID: req.ID,
			Allow:      false,
			Note:       "run canceled",
		})
		return ApprovalDecision{}, ctx.Err()
	}
}

func (r *run) subscribe(after int64) (backlog []Event, live <-chan Event, cancel func()) {
	return r.bus.subscribe(after)
}

// StartMessage persists a text-only user message and starts an agent run.
func (s *Service) StartMessage(ctx context.Context, threadID, content string) (*RunInfo, error) {
	return s.StartMessageWith(ctx, threadID, StartMessageInput{Content: content})
}

// StartMessageWith persists a user message and starts an agent run for the
// thread. The returned run streams through Subscribe; the caller does not need
// to keep the request context alive.
func (s *Service) StartMessageWith(ctx context.Context, threadID string, input StartMessageInput) (*RunInfo, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" && len(input.Attachments) == 0 {
		return nil, fmt.Errorf("%w: message content or an image attachment is required", ErrInvalidInput)
	}
	if len(content) > maxMessageContentBytes {
		return nil, fmt.Errorf(
			"%w: message content exceeds the %d KiB limit",
			ErrInvalidInput,
			maxMessageContentBytes>>10,
		)
	}

	images, imageBytes, err := buildImageParts(input.Attachments)
	if err != nil {
		return nil, err
	}

	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	s.clearFinishedActiveLocked(threadID)
	if s.activeByThread[threadID] != "" {
		s.mu.Unlock()
		return nil, ErrThreadBusy
	}
	history, err := s.store.ListMessages(ctx, threadID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if used := imageBytesInMessages(history); used+imageBytes > maxThreadImageBytes {
		s.mu.Unlock()
		return nil, fmt.Errorf(
			"%w: this thread has reached the %d MiB image limit; start a new thread for more images",
			ErrInvalidInput,
			maxThreadImageBytes>>20,
		)
	}

	parts := make([]llm.Part, 0, len(images)+1)
	parts = append(parts, images...)
	if content != "" {
		parts = append(parts, llm.NewTextPart(content))
	}
	user := userMessageWithParts(thread.ID, parts, nowUTC())
	r := newRun(s.ctx, thread, history, user)
	s.runs[r.id] = r
	s.activeByThread[threadID] = r.id
	s.wg.Add(1)
	s.mu.Unlock()

	if err := s.store.AppendMessages(ctx, user); err != nil {
		s.discardRun(r)
		return nil, err
	}

	if thread.Title == defaultThreadTitle {
		thread.Title = truncateTitle(messageTitle(content, images))
		thread.UpdatedAt = nowUTC()
		if err := s.store.UpdateThread(ctx, thread); err != nil {
			// A title is cosmetic; the run should still proceed.
			thread.Title = defaultThreadTitle
		}
	}

	go s.execute(r)
	return r.info(), nil
}

// runTranscript captures the canonical messages a run produced before it
// reaches a terminal state. A completed run is persisted from the final agent
// response; canceled and failed runs use this snapshot so the transcript keeps
// the work that was already visible in the UI. It is guarded by transcriptSink.
type runTranscript struct {
	messages  []llm.Message
	assistant *llm.Message
	results   []llm.ToolResult
	partial   map[int]partialPart
	step      int
}

type partialPart struct {
	kind llm.PartType
	text string
}

// transcriptSink observes agent events synchronously inside the runner. The
// public run event stream is delivered through a queue that stops on
// cancellation, so a canceled run can lose its trailing events; the sink is
// the reliable source for persisting work that already happened.
type transcriptSink struct {
	mu         sync.Mutex
	transcript runTranscript
	runErr     error
}

func (s *transcriptSink) Emit(event agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch typed := event.(type) {
	case agent.StepStart:
		s.transcript.startStep(typed.Step)
	case agent.TextDelta:
		s.transcript.addText(typed.Step, typed.Index, typed.Text, typed.Replace)
	case agent.ThinkingDelta:
		s.transcript.addThinking(typed.Step, typed.Index, typed.Text, typed.Replace)
	case agent.MessageEnd:
		s.transcript.setAssistant(typed.Message)
	case agent.ToolEnd:
		s.transcript.addResults(typed.Message)
	case agent.StepEnd:
		s.transcript.endStep(typed.Err)
	case agent.RunEnd:
		s.runErr = typed.Err
		s.transcript.flush(typed.Err)
	}
}

func (s *transcriptSink) snapshot(fallback error) []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.runErr
	if err == nil {
		err = fallback
	}
	s.transcript.flush(err)
	return append([]llm.Message(nil), s.transcript.messages...)
}

func (t *runTranscript) startStep(step int) {
	t.step = step
	t.assistant = nil
	t.results = nil
	t.partial = make(map[int]partialPart)
}

func (t *runTranscript) addText(step, index int, text string, replace bool) {
	t.addPartial(step, index, llm.PartTypeText, text, replace)
}

func (t *runTranscript) addThinking(step, index int, text string, replace bool) {
	t.addPartial(step, index, llm.PartTypeReasoning, text, replace)
}

func (t *runTranscript) addPartial(step, index int, kind llm.PartType, text string, replace bool) {
	if step != t.step || t.partial == nil {
		return
	}
	part := t.partial[index]
	if part.kind != "" && part.kind != kind {
		return
	}
	part.kind = kind
	if replace {
		part.text = text
	} else {
		part.text += text
	}
	t.partial[index] = part
}

func (t *runTranscript) setAssistant(message *llm.Message) {
	if message == nil {
		return
	}
	clone := message.Clone()
	// MessageEnd supersedes any deltas accumulated for this step.
	t.partial = nil
	t.assistant = &clone
}

func (t *runTranscript) addResults(message *llm.Message) {
	if message == nil {
		return
	}
	t.results = append(t.results, message.ToolResults()...)
}

func (t *runTranscript) endStep(err error) {
	t.flush(err)
}

// flush appends the current step to the snapshot. If the step was interrupted
// before MessageEnd, the visible deltas become an incomplete assistant message.
// Any unresolved tool call receives a synthetic result so the persisted
// transcript stays valid for the next model request.
func (t *runTranscript) flush(err error) {
	assistant := t.assistant
	if assistant == nil {
		assistant = t.partialMessage(err)
	}
	results := t.results
	if assistant != nil {
		results = withMissingToolResults(assistant, results, err)
	}
	if assistant != nil {
		t.messages = append(t.messages, *assistant)
	}
	if len(results) > 0 {
		t.messages = append(t.messages, llm.ToolResultsMessage(results...))
	}
	t.assistant = nil
	t.results = nil
	t.partial = nil
}

func (t *runTranscript) partialMessage(err error) *llm.Message {
	if len(t.partial) == 0 {
		return nil
	}
	indices := make([]int, 0, len(t.partial))
	for index := range t.partial {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	parts := make([]llm.Part, 0, len(indices))
	for _, index := range indices {
		part := t.partial[index]
		if part.text == "" {
			continue
		}
		switch part.kind {
		case llm.PartTypeText:
			parts = append(parts, llm.NewTextPart(part.text))
		case llm.PartTypeReasoning:
			parts = append(parts, llm.NewReasoningPart(part.text))
		}
	}
	if len(parts) == 0 {
		return nil
	}

	status := llm.MessageStatusIncomplete
	if err != nil && !interrupted(err) {
		status = llm.MessageStatusFailed
	}
	return &llm.Message{Role: llm.RoleAssistant, Content: parts, Status: status}
}

// interrupted reports whether the error stopped the run before a normal
// completion (cancellation, deadline, or budget boundary).
func interrupted(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, agent.ErrBudgetExceeded) ||
		errors.Is(err, agent.ErrMaxSteps)
}

// withMissingToolResults closes dangling tool calls so the next run can replay
// the transcript without an unanswered call.
func withMissingToolResults(assistant *llm.Message, results []llm.ToolResult, err error) []llm.ToolResult {
	if assistant == nil || len(assistant.ToolCalls()) == 0 {
		return results
	}
	seen := make(map[string]struct{}, len(results))
	for i := range results {
		seen[results[i].CallID] = struct{}{}
	}

	note := "The run ended before this tool finished."
	status := llm.ToolStatusIncomplete
	if errors.Is(err, context.Canceled) {
		note = "The run was canceled before this tool finished."
	} else if err != nil && !interrupted(err) {
		note = "The run failed before this tool finished."
		status = llm.ToolStatusError
	}

	for _, call := range assistant.ToolCalls() {
		if call.EffectiveKind() == llm.ToolKindServer {
			continue
		}
		if _, ok := seen[call.CallID]; ok {
			continue
		}
		results = append(results, llm.ToolResult{
			CallID:  call.CallID,
			Name:    call.Name,
			Kind:    call.EffectiveKind(),
			Status:  status,
			Content: []llm.Part{llm.NewTextPart(note)},
		})
	}
	return results
}

// streamedText reduces replace snapshots and incremental fragments to the text
// the frontend has not seen yet. Indices restart at zero for each step.
type streamedText struct {
	parts map[streamKey]string
}

type streamKey struct {
	step  int
	index int
}

func (s *streamedText) delta(step, index int, text string, replace bool) string {
	if s.parts == nil {
		s.parts = make(map[streamKey]string)
	}
	key := streamKey{step: step, index: index}
	previous := s.parts[key]
	if text == previous {
		return ""
	}
	if replace {
		s.parts[key] = text
		if strings.HasPrefix(text, previous) {
			return text[len(previous):]
		}
		// A shorter, unrelated snapshot cannot be expressed as an append. Send
		// it whole; providers only replace with longer cumulative snapshots.
		return text
	}
	s.parts[key] = previous + text
	return text
}

func (s *Service) execute(r *run) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.failRun(r, fmt.Errorf("run panicked: %v", recovered))
		}
		s.removeRun(r)
		r.bus.close()
	}()

	r.setStatus(RunRunning)
	r.emit(EventRunStarted, RunStartedEvent{
		ThreadID:   r.thread.ID,
		Provider:   r.thread.Provider,
		Model:      r.thread.Model,
		AccessMode: r.thread.AccessMode,
	})

	model, err := s.models.NewModel(r.ctx, r.thread.Provider, r.thread.Model)
	if err != nil {
		s.failRun(r, fmt.Errorf("create model: %w", err))
		return
	}

	a, err := agent.New(agent.Config{
		Name:         "codjent",
		Description:  "A coding agent that inspects and edits the workspace.",
		Instructions: s.instructions,
		Model:        model,
		Tools:        s.tools,
		Policy: agent.Hooks{
			OnBeforeTool: func(ctx context.Context, req *agent.ToolRequest) (agent.Decision, error) {
				return s.beforeTool(r, ctx, req)
			},
		},
	})
	if err != nil {
		s.failRun(r, fmt.Errorf("create agent: %w", err))
		return
	}

	input := make([]llm.Message, 0, len(r.history)+1)
	for _, message := range r.history {
		input = append(input, message.ToLLM())
	}
	input = append(input, r.user.ToLLM())

	transcript := &transcriptSink{}
	execution, err := a.StreamMessagesWith(r.ctx, input, agent.RunOptions{
		Session: &session.Session{ID: r.thread.ID},
		Budget:  agent.Budget{MaxSteps: s.maxSteps},
		Sink:    transcript,
	})
	if err != nil {
		s.failRun(r, fmt.Errorf("start run: %w", err))
		return
	}
	defer func() { _ = execution.Close() }()

	var final agent.RunEnd
	var text, thinking streamedText
	for event := range execution.Events() {
		switch typed := event.(type) {
		case agent.TextDelta:
			if delta := text.delta(typed.Step, typed.Index, typed.Text, typed.Replace); delta != "" {
				r.emit(EventTextDelta, TextDeltaEvent{Index: typed.Index, Text: delta})
			}
		case agent.ThinkingDelta:
			if delta := thinking.delta(typed.Step, typed.Index, typed.Text, typed.Replace); delta != "" {
				r.emit(EventThinkingDelta, ThinkingDeltaEvent{Index: typed.Index, Text: delta})
			}
		case agent.ToolEnd:
			// Denials are reported by beforeTool before the runner records
			// them; do not emit a duplicate result.
			if errors.Is(typed.Err, agent.ErrToolDenied) {
				continue
			}
			emitToolResult(r, typed)
		case agent.RunEnd:
			final = typed
		}
	}
	// The agent event queue stops delivering when the run context is canceled,
	// so a canceled run can end without a RunEnd event. Treat that as the
	// cancellation it is instead of a missing response.
	if final.Err == nil && final.Response == nil && r.ctx.Err() != nil {
		final.Err = r.ctx.Err()
	}
	if final.Err != nil || final.Response == nil {
		// Wait for the runner goroutine so the synchronous transcript sink has
		// observed RunEnd. A canceled tool may ignore its context briefly, so
		// bound the wait and persist what we have.
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = execution.Wait(waitCtx)
		waitCancel()
	}
	if final.Err != nil {
		if err := s.persistInterrupted(r, transcript, final.Err); err != nil {
			s.failRun(r, fmt.Errorf("persist interrupted run: %w", err))
			return
		}
		s.failRun(r, fmt.Errorf("run: %w", final.Err))
		return
	}
	if final.Response == nil {
		runErr := errors.New("run returned no response")
		if err := s.persistInterrupted(r, transcript, runErr); err != nil {
			s.failRun(r, fmt.Errorf("persist interrupted run: %w", err))
			return
		}
		s.failRun(r, runErr)
		return
	}
	messageIDs, err := s.persistResponse(r, final.Response)
	if err != nil {
		// The canonical response could not be written; keep the messages the
		// event sink already observed instead of dropping the whole run.
		_ = s.persistInterrupted(r, transcript, err)
		s.failRun(r, fmt.Errorf("persist response: %w", err))
		return
	}
	r.complete(final.Response, messageIDs)
}

func (s *Service) beforeTool(r *run, ctx context.Context, req *agent.ToolRequest) (agent.Decision, error) {
	call := req.Call
	arguments := string(call.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	r.emit(EventToolCall, ToolCallEvent{
		Index:     call.Index,
		ID:        call.ID,
		Name:      call.Name,
		Arguments: arguments,
	})

	if r.thread.AccessMode == AccessFull || !requiresApproval(call.Name) {
		return agent.Allow, nil
	}

	decision, err := r.awaitApproval(ctx, PendingApproval{
		ID:         newID("apr"),
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Arguments:  arguments,
		CreatedAt:  nowUTC(),
	})
	if err != nil {
		return agent.Deny, fmt.Errorf("approval interrupted: %w", err)
	}
	if !decision.Allow {
		message := fmt.Sprintf("tool %q denied by user", call.Name)
		if decision.Note != "" {
			message += ": " + decision.Note
		}
		r.emit(EventToolResult, ToolResultEvent{
			Index:   call.Index,
			ID:      call.ID,
			Name:    call.Name,
			Error:   message,
			Skipped: true,
		})
		return agent.Deny, nil
	}
	return agent.Allow, nil
}

func emitToolResult(r *run, event agent.ToolEnd) {
	data := ToolResultEvent{
		Index: event.Call.Index,
		ID:    event.Call.ID,
		Name:  event.Call.Name,
	}
	if event.Result != nil {
		data.Output = event.Result.Text()
	}
	if event.Err != nil {
		data.Error = event.Err.Error()
	}
	if data.Output == "" && data.Error == "" && event.Message != nil {
		if results := event.Message.ToolResults(); len(results) > 0 {
			data.Output = results[0].Text()
		}
	}
	r.emit(EventToolResult, data)
}

// persistInterrupted writes the messages a run produced before it was canceled
// or failed. It must run before the terminal event is emitted so clients that
// refresh on that event see the transcript. Persistence uses a detached
// context because the run's own context is already canceled.
func (s *Service) persistInterrupted(r *run, transcript *transcriptSink, runErr error) error {
	messages := transcript.snapshot(runErr)
	if len(messages) == 0 {
		return nil
	}

	now := nowUTC()
	converted := make([]*Message, 0, len(messages))
	for i := range messages {
		if message := messageFromLLM(r.threadID, &messages[i], now); message != nil {
			converted = append(converted, message)
		}
	}
	if len(converted) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer cancel()
	return s.store.AppendMessages(ctx, converted...)
}

func (s *Service) persistResponse(r *run, resp *agent.Response) ([]string, error) {
	offset := len(r.history) + 1
	if s.instructions != "" {
		offset++
	}
	if len(resp.Messages) < offset {
		return nil, fmt.Errorf("unexpected response transcript length %d (want at least %d)", len(resp.Messages), offset)
	}

	now := nowUTC()
	messages := make([]*Message, 0, len(resp.Messages)-offset)
	for i := offset; i < len(resp.Messages); i++ {
		message := &resp.Messages[i]
		if message.Role == llm.RoleSystem {
			continue
		}
		if converted := messageFromLLM(r.threadID, message, now); converted != nil {
			messages = append(messages, converted)
		}
	}
	if len(messages) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), 5*time.Second)
	defer cancel()
	if err := s.store.AppendMessages(ctx, messages...); err != nil {
		return nil, err
	}

	ids := make([]string, len(messages))
	for i, message := range messages {
		ids[i] = message.ID
	}
	return ids, nil
}

func requiresApproval(toolName string) bool {
	return toolName == "execute_command"
}

// GetRun returns a snapshot of a run.
func (s *Service) GetRun(runID string) (*RunInfo, error) {
	s.mu.RLock()
	r := s.runs[runID]
	s.mu.RUnlock()
	if r == nil {
		return nil, ErrRunNotFound
	}
	return r.info(), nil
}

// ResolveApproval resolves a pending tool approval.
func (s *Service) ResolveApproval(runID, approvalID string, decision ApprovalDecision) error {
	s.mu.RLock()
	r := s.runs[runID]
	s.mu.RUnlock()
	if r == nil {
		return ErrRunNotFound
	}
	return r.resolveApproval(approvalID, decision)
}

// CancelRun cancels an active run.
func (s *Service) CancelRun(runID string) error {
	s.mu.RLock()
	r := s.runs[runID]
	s.mu.RUnlock()
	if r == nil {
		return ErrRunNotFound
	}
	r.mu.Lock()
	finished := r.status == RunCompleted || r.status == RunFailed || r.status == RunCanceled
	r.mu.Unlock()
	if finished {
		return ErrRunFinished
	}
	r.cancel()
	return nil
}

// Subscribe returns replayable events and a live event channel for a run.
func (s *Service) Subscribe(runID string, after int64) (backlog []Event, live <-chan Event, cancel func(), err error) {
	s.mu.RLock()
	r := s.runs[runID]
	s.mu.RUnlock()
	if r == nil {
		return nil, nil, nil, ErrRunNotFound
	}
	backlog, live, cancel = r.subscribe(after)
	return backlog, live, cancel, nil
}

func (s *Service) removeRun(r *run) {
	s.mu.Lock()
	if s.activeByThread[r.threadID] == r.id {
		delete(s.activeByThread, r.threadID)
	}
	s.mu.Unlock()
	r.cancel()
	s.wg.Done()
}

func (s *Service) discardRun(r *run) {
	s.mu.Lock()
	delete(s.runs, r.id)
	if s.activeByThread[r.threadID] == r.id {
		delete(s.activeByThread, r.threadID)
	}
	s.mu.Unlock()
	r.cancel()
	s.wg.Done()
	r.bus.close()
}

func (s *Service) failRun(r *run, err error) {
	if r.ctx.Err() != nil {
		r.canceled()
		return
	}
	r.fail(err)
}
