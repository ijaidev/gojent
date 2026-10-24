package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/core/llm"
)

func TestThreadCRUD(t *testing.T) {
	svc, _, _, _ := newTestService(t, AccessAsk, scriptedStep{text: "hello"})
	ctx := context.Background()

	thread, err := svc.CreateThread(ctx, CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	if thread.ID == "" || thread.Title != defaultThreadTitle {
		t.Fatalf("thread defaults = %+v", thread)
	}
	if thread.Provider != "google" || thread.Model != "test-model" || thread.AccessMode != AccessAsk {
		t.Fatalf("thread settings = %+v", thread)
	}

	full, err := svc.CreateThread(ctx, CreateThreadInput{
		Title:      "Full access",
		AccessMode: AccessFull,
	})
	if err != nil {
		t.Fatalf("CreateThread full: %v", err)
	}
	if full.AccessMode != AccessFull {
		t.Fatalf("access mode = %q", full.AccessMode)
	}

	threads, err := svc.ListThreads(ctx)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("ListThreads len = %d", len(threads))
	}

	title := "Renamed"
	detail, err := svc.UpdateThread(ctx, thread.ID, UpdateThreadInput{Title: &title})
	if err != nil {
		t.Fatalf("UpdateThread: %v", err)
	}
	if detail.Title != title {
		t.Fatalf("title = %q", detail.Title)
	}

	got, err := svc.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if got.Thread.Title != title || got.Messages == nil {
		t.Fatalf("thread detail = %+v", got)
	}

	if err := svc.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if _, err := svc.GetThread(ctx, thread.ID); !errors.Is(err, ErrThreadNotFound) {
		t.Fatalf("GetThread after delete = %v", err)
	}

	bad := AccessMode("root")
	if _, err := svc.CreateThread(ctx, CreateThreadInput{AccessMode: bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid access mode error = %v", err)
	}
	if _, err := svc.CreateThread(ctx, CreateThreadInput{Provider: "nope"}); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
	if _, err := svc.CreateThread(ctx, CreateThreadInput{Model: "nope"}); !errors.Is(err, provider.ErrUnknownModel) {
		t.Fatalf("unknown model error = %v", err)
	}
}

func TestFullAccessRunPersistsTranscript(t *testing.T) {
	svc, _, model, workspace := newTestService(t, AccessFull,
		scriptedStep{
			text: "Running the command.",
			toolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "execute_command",
				Arguments: `{"command":"printf codjent-ok"}`,
			}},
		},
		scriptedStep{text: "Finished."},
		scriptedStep{text: "Second turn."},
	)

	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{AccessMode: AccessFull})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "run it")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}

	reader := subscribe(t, svc, run.ID)
	events := reader.drain(t)
	reader.cancel()

	for _, event := range events {
		if event.Type == EventApprovalRequired {
			t.Fatalf("full access emitted approval event: %+v", event)
		}
	}
	assertEvent(t, events, EventToolCall)
	result := assertEvent(t, events, EventToolResult)
	resultData, ok := result.Data.(ToolResultEvent)
	if !ok {
		t.Fatalf("tool result data type = %T", result.Data)
	}
	if resultData.Error != "" || !strings.Contains(resultData.Output, "codjent-ok") {
		t.Fatalf("tool result = %+v", resultData)
	}
	completed := assertEvent(t, events, EventRunCompleted)
	completedData := completed.Data.(RunCompletedEvent)
	if completedData.Usage.TotalTokens != 4 {
		t.Fatalf("usage = %+v", completedData.Usage)
	}

	detail, err := svc.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if detail.Thread.Title != "run it" {
		t.Fatalf("auto title = %q", detail.Thread.Title)
	}
	if len(detail.Messages) != 4 {
		t.Fatalf("message count = %d: %+v", len(detail.Messages), detail.Messages)
	}
	wantRoles := []llm.Role{llm.RoleUser, llm.RoleAssistant, llm.RoleTool, llm.RoleAssistant}
	for i, role := range wantRoles {
		if detail.Messages[i].Role != role {
			t.Fatalf("message %d role = %q, want %q", i, detail.Messages[i].Role, role)
		}
	}
	if !strings.Contains(detail.Messages[2].Text(), "codjent-ok") {
		t.Fatalf("persisted tool output = %q", detail.Messages[2].Text())
	}

	secondRun, err := svc.StartMessage(context.Background(), thread.ID, "again")
	if err != nil {
		t.Fatalf("second StartMessage: %v", err)
	}
	reader = subscribe(t, svc, secondRun.ID)
	secondEvents := reader.drain(t)
	reader.cancel()
	assertEvent(t, secondEvents, EventRunCompleted)
	if len(secondEvents) == 0 {
		t.Fatal("no second-run events")
	}

	requests := model.snapshotRequests()
	if len(requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(requests))
	}
	last := requests[len(requests)-1]
	if len(last.Messages) < 4 {
		t.Fatalf("second-run history = %+v", last.Messages)
	}
	var sawTool bool
	for _, message := range last.Messages {
		if message.Role == llm.RoleTool {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatalf("second-run request did not include tool history: %+v", last.Messages)
	}

	marker := filepath.Join(workspace, "unused")
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("unexpected marker")
	}
}

func TestApprovalAllow(t *testing.T) {
	svc, _, _, workspace := newTestService(t, AccessAsk,
		scriptedStep{
			toolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "execute_command",
				Arguments: `{"command":"touch approved-marker"}`,
			}},
		},
		scriptedStep{text: "Command finished."},
	)

	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "create the marker")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}

	reader := subscribe(t, svc, run.ID)
	defer reader.cancel()
	approvalEvent := reader.until(t, func(event Event) bool {
		return event.Type == EventApprovalRequired
	})
	approval := approvalEvent.Data.(ApprovalRequiredEvent).Approval
	if approval.ToolName != "execute_command" || !strings.Contains(approval.Arguments, "approved-marker") {
		t.Fatalf("approval = %+v", approval)
	}

	info, err := svc.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if info.Status != RunWaitingApproval || info.PendingApproval == nil {
		t.Fatalf("run info = %+v", info)
	}
	if _, err := svc.StartMessage(context.Background(), thread.ID, "another"); !errors.Is(err, ErrThreadBusy) {
		t.Fatalf("busy StartMessage error = %v", err)
	}
	if _, err := svc.UpdateThread(context.Background(), thread.ID, UpdateThreadInput{}); !errors.Is(err, ErrThreadBusy) {
		t.Fatalf("busy UpdateThread error = %v", err)
	}

	if err := svc.ResolveApproval(run.ID, approval.ID, ApprovalDecision{Allow: true}); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	events := reader.drain(t)
	assertEvent(t, events, EventApprovalResolved)
	assertEvent(t, events, EventToolResult)
	assertEvent(t, events, EventRunCompleted)

	if _, err := os.Stat(filepath.Join(workspace, "approved-marker")); err != nil {
		t.Fatalf("approved command did not run: %v", err)
	}

	info, err = svc.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun completed: %v", err)
	}
	if info.Status != RunCompleted || info.PendingApproval != nil {
		t.Fatalf("completed run info = %+v", info)
	}
	if err := svc.ResolveApproval(run.ID, approval.ID, ApprovalDecision{Allow: true}); !errors.Is(err, ErrRunFinished) {
		t.Fatalf("resolve after completion = %v", err)
	}
}

func TestApprovalDeny(t *testing.T) {
	svc, _, _, workspace := newTestService(t, AccessAsk,
		scriptedStep{
			toolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "execute_command",
				Arguments: `{"command":"touch denied-marker"}`,
			}},
		},
		scriptedStep{text: "The command was denied."},
	)

	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "do not run")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}

	reader := subscribe(t, svc, run.ID)
	defer reader.cancel()
	approvalEvent := reader.until(t, func(event Event) bool {
		return event.Type == EventApprovalRequired
	})
	approval := approvalEvent.Data.(ApprovalRequiredEvent).Approval

	if err := svc.ResolveApproval(run.ID, approval.ID, ApprovalDecision{Allow: false, Note: "not this one"}); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	events := reader.drain(t)
	result := assertEvent(t, events, EventToolResult)
	resultData := result.Data.(ToolResultEvent)
	if !resultData.Skipped || !strings.Contains(resultData.Error, "denied by user") {
		t.Fatalf("denied tool result = %+v", resultData)
	}
	assertEvent(t, events, EventRunCompleted)

	if _, err := os.Stat(filepath.Join(workspace, "denied-marker")); !os.IsNotExist(err) {
		t.Fatalf("denied command ran: %v", err)
	}
}

func TestCancelRun(t *testing.T) {
	svc, _, _, _ := newTestService(t, AccessAsk,
		scriptedStep{
			toolCalls: []llm.ToolCall{{
				ID:        "call_1",
				Name:      "execute_command",
				Arguments: `{"command":"printf never"}`,
			}},
		},
		scriptedStep{text: "unreachable"},
	)

	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "wait for approval")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}

	reader := subscribe(t, svc, run.ID)
	defer reader.cancel()
	reader.until(t, func(event Event) bool {
		return event.Type == EventApprovalRequired
	})

	detail, err := svc.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if detail.ActiveRun == nil || detail.ActiveRun.ID != run.ID ||
		detail.ActiveRun.Status != RunWaitingApproval ||
		detail.ActiveRun.PendingApproval == nil {
		t.Fatalf("active run = %+v", detail.ActiveRun)
	}

	if err := svc.CancelRun(run.ID); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	events := reader.drain(t)
	assertEvent(t, events, EventRunCanceled)

	info, err := svc.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if info.Status != RunCanceled {
		t.Fatalf("status = %q", info.Status)
	}
	if err := svc.CancelRun(run.ID); !errors.Is(err, ErrRunFinished) {
		t.Fatalf("second CancelRun = %v", err)
	}
}

func TestRunFailure(t *testing.T) {
	svc, _, _, _ := newTestService(t, AccessAsk,
		scriptedStep{err: errors.New("model unavailable")},
	)

	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "hello")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}

	reader := subscribe(t, svc, run.ID)
	defer reader.cancel()
	events := reader.drain(t)
	failed := assertEvent(t, events, EventRunFailed)
	if !strings.Contains(failed.Data.(RunFailedEvent).Error, "model unavailable") {
		t.Fatalf("failure = %+v", failed.Data)
	}
	info, err := svc.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if info.Status != RunFailed || !strings.Contains(info.Error, "model unavailable") {
		t.Fatalf("run info = %+v", info)
	}
	detail, err := svc.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}
	if len(detail.Messages) != 1 || detail.Messages[0].Role != llm.RoleUser {
		t.Fatalf("messages after failure = %+v", detail.Messages)
	}
}

func TestEventsReplay(t *testing.T) {
	svc, _, _, _ := newTestService(t, AccessAsk, scriptedStep{text: "hello"})
	thread, err := svc.CreateThread(context.Background(), CreateThreadInput{})
	if err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	run, err := svc.StartMessage(context.Background(), thread.ID, "hi")
	if err != nil {
		t.Fatalf("StartMessage: %v", err)
	}
	reader := subscribe(t, svc, run.ID)
	events := reader.drain(t)
	reader.cancel()
	if len(events) < 2 {
		t.Fatalf("events = %+v", events)
	}

	backlog, live, cancel, err := svc.Subscribe(run.ID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer cancel()
	if len(backlog) != len(events) {
		t.Fatalf("backlog len = %d, want %d", len(backlog), len(events))
	}
	if _, open := <-live; open {
		t.Fatal("live channel should be closed after run completion")
	}

	lastID := events[len(events)-1].ID
	backlog, _, cancel, err = svc.Subscribe(run.ID, lastID)
	if err != nil {
		t.Fatalf("Subscribe after: %v", err)
	}
	cancel()
	if len(backlog) != 0 {
		t.Fatalf("backlog after %d = %+v", lastID, backlog)
	}
}

func TestCloseCancelsAndRejectsWork(t *testing.T) {
	svc, _, _, _ := newTestService(t, AccessAsk, scriptedStep{text: "hello"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := svc.CreateThread(context.Background(), CreateThreadInput{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("CreateThread after close = %v", err)
	}
	if err := svc.Close(ctx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

type scriptedStep struct {
	text      string
	thinking  string
	toolCalls []llm.ToolCall
	err       error
}

type scriptedModel struct {
	mu       sync.Mutex
	steps    []scriptedStep
	calls    int
	requests []*llm.Request
}

func (m *scriptedModel) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *scriptedModel) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	index := m.calls
	m.calls++
	m.requests = append(m.requests, req.Clone())
	if index >= len(m.steps) {
		m.mu.Unlock()
		return nil, fmt.Errorf("scripted model exhausted after %d calls", index)
	}
	step := m.steps[index]
	m.mu.Unlock()

	if step.err != nil {
		return nil, step.err
	}

	var chunks []*llm.StreamChunk
	if step.thinking != "" {
		chunks = append(chunks, &llm.StreamChunk{ThinkingDelta: step.thinking})
	}
	if step.text != "" {
		half := len(step.text) / 2
		if half == 0 {
			half = len(step.text)
		}
		chunks = append(chunks,
			&llm.StreamChunk{Delta: step.text[:half]},
			&llm.StreamChunk{Delta: step.text[half:]},
		)
	}
	finish := llm.FinishReasonStop
	if len(step.toolCalls) > 0 {
		finish = llm.FinishReasonTool
		chunks = append(chunks, &llm.StreamChunk{ToolCalls: step.toolCalls})
	}
	chunks = append(chunks, &llm.StreamChunk{
		FinishReason: finish,
		Usage:        &llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
	})
	return llm.NewSliceStream(ctx, chunks), nil
}

func (m *scriptedModel) ModelID() string {
	return "test-model"
}

func (m *scriptedModel) snapshotRequests() []*llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.requests)
}

type fakeModels struct {
	info  provider.Info
	model *scriptedModel
}

func (f *fakeModels) List() []provider.Info {
	info := f.info
	info.Models = slices.Clone(f.info.Models)
	return []provider.Info{info}
}

func (f *fakeModels) NewModel(_ context.Context, providerID, modelID string) (llm.Model, error) {
	if providerID != f.info.ID {
		return nil, provider.ErrUnknownProvider
	}
	if modelID != f.info.Models[0].ID {
		return nil, provider.ErrUnknownModel
	}
	return f.model, nil
}

type fakeStore struct {
	mu       sync.Mutex
	threads  map[string]*Thread
	messages map[string][]*Message
	closed   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		threads:  make(map[string]*Thread),
		messages: make(map[string][]*Message),
	}
}

func (s *fakeStore) CreateThread(_ context.Context, thread *Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, exists := s.threads[thread.ID]; exists {
		return fmt.Errorf("duplicate thread")
	}
	s.threads[thread.ID] = cloneThread(thread)
	return nil
}

func (s *fakeStore) GetThread(_ context.Context, id string) (*Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, ok := s.threads[id]
	if !ok {
		return nil, ErrThreadNotFound
	}
	return cloneThread(thread), nil
}

func (s *fakeStore) ListThreads(_ context.Context) ([]*Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	threads := make([]*Thread, 0, len(s.threads))
	for _, thread := range s.threads {
		threads = append(threads, cloneThread(thread))
	}
	sort.Slice(threads, func(i, j int) bool {
		return threads[i].UpdatedAt.After(threads[j].UpdatedAt)
	})
	return threads, nil
}

func (s *fakeStore) UpdateThread(_ context.Context, thread *Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[thread.ID]; !ok {
		return ErrThreadNotFound
	}
	s.threads[thread.ID] = cloneThread(thread)
	return nil
}

func (s *fakeStore) DeleteThread(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[id]; !ok {
		return ErrThreadNotFound
	}
	delete(s.threads, id)
	delete(s.messages, id)
	return nil
}

func (s *fakeStore) AppendMessages(_ context.Context, messages ...*Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(messages) == 0 {
		return nil
	}
	threadID := messages[0].ThreadID
	thread, ok := s.threads[threadID]
	if !ok {
		return ErrThreadNotFound
	}
	for _, message := range messages {
		s.messages[threadID] = append(s.messages[threadID], cloneMessage(message))
		thread.UpdatedAt = time.Now().UTC()
	}
	return nil
}

func (s *fakeStore) ListMessages(_ context.Context, threadID string) ([]*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[threadID]; !ok {
		return nil, ErrThreadNotFound
	}
	messages := make([]*Message, 0, len(s.messages[threadID]))
	for _, message := range s.messages[threadID] {
		messages = append(messages, cloneMessage(message))
	}
	return messages, nil
}

func (s *fakeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func cloneThread(thread *Thread) *Thread {
	clone := *thread
	return &clone
}

func cloneMessage(message *Message) *Message {
	clone := *message
	clone.Content = slices.Clone(message.Content)
	clone.ToolCalls = slices.Clone(message.ToolCalls)
	return &clone
}

func newTestService(t *testing.T, mode AccessMode, steps ...scriptedStep) (*Service, *fakeStore, *scriptedModel, string) {
	t.Helper()
	store := newFakeStore()
	model := &scriptedModel{steps: steps}
	models := &fakeModels{
		info: provider.Info{
			ID:           "google",
			Name:         "Test Google",
			DefaultModel: "test-model",
			Models:       []provider.ModelInfo{{ID: "test-model", Name: "Test Model"}},
		},
		model: model,
	}
	workspace := t.TempDir()
	svc, err := New(Config{
		Store:             store,
		Models:            models,
		Workspace:         workspace,
		DefaultProvider:   "google",
		DefaultModel:      "test-model",
		DefaultAccessMode: mode,
		MaxSteps:          5,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return svc, store, model, workspace
}

type eventReader struct {
	backlog []Event
	live    <-chan Event
	cancel  func()
}

func subscribe(t *testing.T, svc *Service, runID string) *eventReader {
	t.Helper()
	backlog, live, cancel, err := svc.Subscribe(runID, 0)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return &eventReader{backlog: backlog, live: live, cancel: cancel}
}

func (r *eventReader) next(t *testing.T) Event {
	t.Helper()
	if len(r.backlog) > 0 {
		event := r.backlog[0]
		r.backlog = r.backlog[1:]
		return event
	}
	select {
	case event, ok := <-r.live:
		if !ok {
			t.Fatal("event stream closed before terminal event")
		}
		return event
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for run event")
		return Event{}
	}
}

func (r *eventReader) until(t *testing.T, predicate func(Event) bool) Event {
	t.Helper()
	for {
		event := r.next(t)
		if predicate(event) {
			return event
		}
	}
}

func (r *eventReader) drain(t *testing.T) []Event {
	t.Helper()
	var events []Event
	for {
		event := r.next(t)
		events = append(events, event)
		switch event.Type {
		case EventRunCompleted, EventRunFailed, EventRunCanceled:
			return events
		}
	}
}

func assertEvent(t *testing.T, events []Event, eventType string) Event {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return event
		}
	}
	t.Fatalf("event %q not found in %+v", eventType, events)
	return Event{}
}
