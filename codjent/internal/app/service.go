package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/core/tool"
	"github.com/ijaidev/gojent/tools/exec"
	"github.com/ijaidev/gojent/tools/fs"
)

const defaultMaxSteps = 20

const defaultInstructions = `You are codjent, a pragmatic coding agent working in the repository at %s.

Use the available file tools to inspect and edit code, and the execute_command tool to run commands. Keep changes focused and consistent with the existing code. Verify your work with tests or builds when practical. Never claim that a command or change succeeded unless a tool result confirms it. If a tool call is denied or fails, report that honestly instead of working around it.`

// Config configures a codjent application service.
type Config struct {
	Store             Store
	Models            provider.ModelSource
	Workspace         string
	Instructions      string
	DefaultProvider   string
	DefaultModel      string
	DefaultAccessMode AccessMode
	MaxSteps          int
}

// Service owns threads, runs, approvals, and the agent loop.
type Service struct {
	store        Store
	models       provider.ModelSource
	workspace    string
	instructions string
	tools        []tool.Tool

	defaultProvider   string
	defaultModel      string
	defaultAccessMode AccessMode
	maxSteps          int

	ctx    context.Context
	cancel context.CancelFunc

	mu             sync.RWMutex
	closed         bool
	runs           map[string]*run
	activeByThread map[string]string
	wg             sync.WaitGroup
	closeOnce      sync.Once
	closeErr       error
}

// New validates dependencies and constructs a service.
func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, errors.New("app: store is required")
	}
	if cfg.Models == nil {
		return nil, errors.New("app: model source is required")
	}

	infos := cfg.Models.List()
	if len(infos) == 0 {
		return nil, errors.New("app: at least one provider is required")
	}

	workspace := strings.TrimSpace(cfg.Workspace)
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("app: resolve workspace: %w", err)
		}
		workspace = cwd
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: resolve workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("app: workspace %q is not a directory", workspace)
	}

	defaultProvider := strings.TrimSpace(cfg.DefaultProvider)
	if defaultProvider == "" {
		defaultProvider = infos[0].ID
	}
	providerInfo, ok := findProvider(infos, defaultProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s", provider.ErrUnknownProvider, defaultProvider)
	}
	defaultModel := strings.TrimSpace(cfg.DefaultModel)
	if defaultModel == "" {
		defaultModel = providerInfo.DefaultModel
	}
	if !hasModel(providerInfo, defaultModel) {
		return nil, fmt.Errorf("%w: %s/%s", provider.ErrUnknownModel, defaultProvider, defaultModel)
	}

	accessMode := cfg.DefaultAccessMode
	if accessMode == "" {
		accessMode = DefaultAccessMode
	}
	if !accessMode.Valid() {
		return nil, fmt.Errorf("%w: access mode %q", ErrInvalidInput, accessMode)
	}

	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	tools, err := newTools(workspace)
	if err != nil {
		return nil, fmt.Errorf("app: create tools: %w", err)
	}

	instructions := strings.TrimSpace(cfg.Instructions)
	if instructions == "" {
		instructions = fmt.Sprintf(defaultInstructions, workspace)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		store:             cfg.Store,
		models:            cfg.Models,
		workspace:         workspace,
		instructions:      instructions,
		tools:             tools,
		defaultProvider:   defaultProvider,
		defaultModel:      defaultModel,
		defaultAccessMode: accessMode,
		maxSteps:          maxSteps,
		ctx:               ctx,
		cancel:            cancel,
		runs:              make(map[string]*run),
		activeByThread:    make(map[string]string),
	}, nil
}

// Providers returns the configured provider catalog.
func (s *Service) Providers() []provider.Info {
	return s.models.List()
}

// Workspace returns the absolute workspace root used by tools.
func (s *Service) Workspace() string {
	return s.workspace
}

// CreateThread persists a new thread.
func (s *Service) CreateThread(ctx context.Context, input CreateThreadInput) (*Thread, error) {
	if s.isClosed() {
		return nil, ErrClosed
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = defaultThreadTitle
	}
	providerID := strings.TrimSpace(input.Provider)
	if providerID == "" {
		providerID = s.defaultProvider
	}
	modelID := strings.TrimSpace(input.Model)
	if modelID == "" {
		modelID = s.defaultModel
	}
	if err := s.validateModel(providerID, modelID); err != nil {
		return nil, err
	}
	accessMode := input.AccessMode
	if accessMode == "" {
		accessMode = s.defaultAccessMode
	}
	if !accessMode.Valid() {
		return nil, fmt.Errorf("%w: access mode %q", ErrInvalidInput, accessMode)
	}

	now := nowUTC()
	thread := &Thread{
		ID:         newID("th"),
		Title:      title,
		Provider:   providerID,
		Model:      modelID,
		AccessMode: accessMode,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.store.CreateThread(ctx, thread); err != nil {
		return nil, err
	}
	return thread, nil
}

// ListThreads returns threads most recently updated first.
func (s *Service) ListThreads(ctx context.Context) ([]*Thread, error) {
	if s.isClosed() {
		return nil, ErrClosed
	}
	return s.store.ListThreads(ctx)
}

// GetThread returns a thread and its transcript.
func (s *Service) GetThread(ctx context.Context, id string) (*ThreadDetail, error) {
	if s.isClosed() {
		return nil, ErrClosed
	}
	thread, err := s.store.GetThread(ctx, id)
	if err != nil {
		return nil, err
	}
	messages, err := s.store.ListMessages(ctx, id)
	if err != nil {
		return nil, err
	}
	return &ThreadDetail{Thread: thread, Messages: messages}, nil
}

// UpdateThread applies a partial update to a thread.
func (s *Service) UpdateThread(ctx context.Context, id string, input UpdateThreadInput) (*Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	s.clearFinishedActiveLocked(id)
	if s.activeByThread[id] != "" {
		return nil, ErrThreadBusy
	}

	thread, err := s.store.GetThread(ctx, id)
	if err != nil {
		return nil, err
	}

	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" {
			return nil, fmt.Errorf("%w: title cannot be empty", ErrInvalidInput)
		}
		thread.Title = title
	}
	if input.Provider != nil {
		thread.Provider = strings.TrimSpace(*input.Provider)
	}
	if input.Model != nil {
		thread.Model = strings.TrimSpace(*input.Model)
	}
	if input.AccessMode != nil {
		thread.AccessMode = *input.AccessMode
	}
	if thread.Provider == "" {
		thread.Provider = s.defaultProvider
	}
	if thread.Model == "" {
		thread.Model = s.defaultModel
	}
	if !thread.AccessMode.Valid() {
		return nil, fmt.Errorf("%w: access mode %q", ErrInvalidInput, thread.AccessMode)
	}
	if err := s.validateModel(thread.Provider, thread.Model); err != nil {
		return nil, err
	}

	thread.UpdatedAt = nowUTC()
	if err := s.store.UpdateThread(ctx, thread); err != nil {
		return nil, err
	}
	return thread, nil
}

// DeleteThread removes a thread and its messages.
func (s *Service) DeleteThread(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.clearFinishedActiveLocked(id)
	if s.activeByThread[id] != "" {
		return ErrThreadBusy
	}
	return s.store.DeleteThread(ctx, id)
}

// Close cancels active runs, waits for them to stop, and closes the store.
func (s *Service) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.closeErr = s.close(ctx)
	})
	return s.closeErr
}

func (s *Service) close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
	}
	runs := make([]*run, 0, len(s.runs))
	for _, r := range s.runs {
		runs = append(runs, r)
	}
	s.mu.Unlock()

	for _, r := range runs {
		r.cancel()
	}

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return s.store.Close()
}

func (s *Service) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// clearFinishedActiveLocked releases a thread whose active run has emitted its
// terminal event but whose goroutine has not cleaned up yet. The caller must
// hold s.mu.
func (s *Service) clearFinishedActiveLocked(threadID string) {
	id := s.activeByThread[threadID]
	if id == "" {
		return
	}
	if r := s.runs[id]; r != nil && r.terminal() {
		delete(s.activeByThread, threadID)
	}
}

func (s *Service) validateModel(providerID, modelID string) error {
	info, ok := findProvider(s.models.List(), providerID)
	if !ok {
		return fmt.Errorf("%w: %s", provider.ErrUnknownProvider, providerID)
	}
	if !hasModel(info, modelID) {
		return fmt.Errorf("%w: %s/%s", provider.ErrUnknownModel, providerID, modelID)
	}
	return nil
}

func findProvider(infos []provider.Info, id string) (provider.Info, bool) {
	for _, info := range infos {
		if info.ID == id {
			return info, true
		}
	}
	return provider.Info{}, false
}

func hasModel(info provider.Info, modelID string) bool {
	for _, model := range info.Models {
		if model.ID == modelID {
			return true
		}
	}
	return false
}

func newTools(workspace string) ([]tool.Tool, error) {
	fsConfig := fs.Config{BaseDir: workspace}
	readFile, err := fs.NewReadFileTool(fsConfig)
	if err != nil {
		return nil, err
	}
	writeFile, err := fs.NewWriteFileTool(fsConfig)
	if err != nil {
		return nil, err
	}
	editFile, err := fs.NewEditFileTool(fsConfig)
	if err != nil {
		return nil, err
	}
	listFiles, err := fs.NewListFilesTool(fsConfig)
	if err != nil {
		return nil, err
	}
	runCommand, err := exec.NewExecuteCommandTool(exec.Config{BaseDir: workspace})
	if err != nil {
		return nil, err
	}
	return []tool.Tool{readFile, writeFile, editFile, listFiles, runCommand}, nil
}
