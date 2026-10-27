package app

import (
	"context"
	"errors"
	"fmt"
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

	execution, err := a.StreamMessagesWith(r.ctx, input, agent.RunOptions{
		Session: &session.Session{ID: r.thread.ID},
		Budget:  agent.Budget{MaxSteps: s.maxSteps},
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
	if final.Err != nil {
		s.failRun(r, fmt.Errorf("run: %w", final.Err))
		return
	}
	if final.Response == nil {
		s.failRun(r, errors.New("run returned no response"))
		return
	}
	messageIDs, err := s.persistResponse(r, final.Response)
	if err != nil {
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
	if err := s.store.AppendMessages(r.ctx, messages...); err != nil {
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
