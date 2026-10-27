package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/store/sqlite"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func TestThreadAndRunFlow(t *testing.T) {
	model := mockllm.New("Hello from the test provider.")
	server, _ := newTestServer(t, model, "mock-llm", app.AccessAsk)
	client := server.Client()

	resp := doJSON(t, client, http.MethodGet, server.URL+"/api/health", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	var providers []provider.Info
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/providers", nil, &providers)
	if resp.StatusCode != http.StatusOK || len(providers) != 1 || providers[0].ID != "test" {
		t.Fatalf("providers = %+v (status %d)", providers, resp.StatusCode)
	}

	var thread app.Thread
	resp = doJSON(t, client, http.MethodPost, server.URL+"/api/threads", map[string]any{
		"title": "HTTP thread",
	}, &thread)
	if resp.StatusCode != http.StatusCreated || thread.ID == "" {
		t.Fatalf("create thread = %+v (status %d)", thread, resp.StatusCode)
	}

	var threads []*app.Thread
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/threads", nil, &threads)
	if resp.StatusCode != http.StatusOK || len(threads) != 1 {
		t.Fatalf("threads = %+v (status %d)", threads, resp.StatusCode)
	}

	var run app.RunInfo
	resp = doJSON(t, client, http.MethodPost, server.URL+"/api/threads/"+thread.ID+"/messages", map[string]any{
		"content": "Hello",
	}, &run)
	if resp.StatusCode != http.StatusAccepted || run.ID == "" {
		t.Fatalf("start message = %+v (status %d)", run, resp.StatusCode)
	}

	events := readEvents(t, client, server.URL+"/api/runs/"+run.ID+"/events")
	assertEventType(t, events, app.EventRunStarted)
	assertEventType(t, events, app.EventTextDelta)
	assertEventType(t, events, app.EventRunCompleted)

	var info app.RunInfo
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/runs/"+run.ID, nil, &info)
	if resp.StatusCode != http.StatusOK || info.Status != app.RunCompleted {
		t.Fatalf("run info = %+v (status %d)", info, resp.StatusCode)
	}

	var detail threadDetail
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/threads/"+thread.ID, nil, &detail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get thread status = %d", resp.StatusCode)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if detail.Messages[0].Role != string(llm.RoleUser) || detail.Messages[1].Role != string(llm.RoleAssistant) {
		t.Fatalf("roles = %q, %q", detail.Messages[0].Role, detail.Messages[1].Role)
	}
	if text := viewText(detail.Messages[1]); !strings.Contains(text, "Hello from the test provider.") {
		t.Fatalf("assistant text = %q", text)
	}

	var updated app.Thread
	resp = doJSON(t, client, http.MethodPatch, server.URL+"/api/threads/"+thread.ID, map[string]any{
		"title": "Renamed",
	}, &updated)
	if resp.StatusCode != http.StatusOK || updated.Title != "Renamed" {
		t.Fatalf("updated thread = %+v (status %d)", updated, resp.StatusCode)
	}

	resp = doJSON(t, client, http.MethodDelete, server.URL+"/api/threads/"+thread.ID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/threads/"+thread.ID, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get deleted thread status = %d", resp.StatusCode)
	}
}

func TestApprovalOverHTTP(t *testing.T) {
	model := &toolModel{}
	server, _ := newTestServer(t, model, "tool-model", app.AccessAsk)
	client := server.Client()

	var thread app.Thread
	resp := doJSON(t, client, http.MethodPost, server.URL+"/api/threads", map[string]any{}, &thread)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create thread status = %d", resp.StatusCode)
	}
	var run app.RunInfo
	resp = doJSON(t, client, http.MethodPost, server.URL+"/api/threads/"+thread.ID+"/messages", map[string]any{
		"content": "run a command",
	}, &run)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d", resp.StatusCode)
	}

	sseResp, err := client.Get(server.URL + "/api/runs/" + run.ID + "/events")
	if err != nil {
		t.Fatalf("open events: %v", err)
	}
	defer func() { _ = sseResp.Body.Close() }()
	if sseResp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", sseResp.StatusCode)
	}
	events := streamEvents(sseResp.Body)

	var approvalData app.ApprovalRequiredEvent
	var toolCallData app.ToolCallEvent
	timeout := time.After(10 * time.Second)
	for approvalData.Approval.ID == "" {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before approval")
			}
			switch event.Type {
			case app.EventToolCall:
				remarshal(t, event.Data, &toolCallData)
			case app.EventApprovalRequired:
				remarshal(t, event.Data, &approvalData)
			}
		case <-timeout:
			t.Fatal("timed out waiting for approval")
		}
	}
	if approvalData.Approval.ID == "" || approvalData.Approval.ToolName != "execute_command" {
		t.Fatalf("approval = %+v", approvalData.Approval)
	}
	if toolCallData.ID != "call_1" || toolCallData.Index != 1 {
		t.Fatalf("tool call event = %+v", toolCallData)
	}

	resp = doJSON(t, client, http.MethodPost,
		server.URL+"/api/runs/"+run.ID+"/approvals/"+approvalData.Approval.ID,
		map[string]any{"allow": true}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approval status = %d", resp.StatusCode)
	}

	sawResult := false
	timeout = time.After(10 * time.Second)
wait:
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("event stream closed before completion")
			}
			switch event.Type {
			case app.EventToolResult:
				var result app.ToolResultEvent
				remarshal(t, event.Data, &result)
				if result.ID != "call_1" || result.Index != 1 {
					t.Fatalf("tool result event = %+v", result)
				}
				sawResult = true
			case app.EventRunCompleted:
				break wait
			}
		case <-timeout:
			t.Fatal("timed out waiting for completion")
		}
	}
	if !sawResult {
		t.Fatal("missing tool result event")
	}

	var detail threadDetail
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/threads/"+thread.ID, nil, &detail)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get thread status = %d", resp.StatusCode)
	}
	if len(detail.Messages) != 4 {
		t.Fatalf("messages = %+v", detail.Messages)
	}
	if got := detail.Messages[1]; got.Role != string(llm.RoleAssistant) ||
		len(got.ToolCalls) != 1 ||
		got.ToolCalls[0].ID != "call_1" ||
		got.ToolCalls[0].Name != "execute_command" ||
		got.ToolCalls[0].Arguments != `{"command":"printf http-ok"}` {
		t.Fatalf("assistant view = %+v", got)
	}
	resultView := detail.Messages[2]
	if resultView.Role != "tool" || resultView.ToolCallID != "call_1" ||
		resultView.Name != "execute_command" {
		t.Fatalf("tool view = %+v", resultView)
	}
	if text := viewText(resultView); !strings.Contains(text, "http-ok") {
		t.Fatalf("tool output = %q", text)
	}
	if got := detail.Messages[3]; got.Role != string(llm.RoleAssistant) ||
		!strings.Contains(viewText(got), "Done.") {
		t.Fatalf("final assistant view = %+v", got)
	}
}

func TestCORSAndErrors(t *testing.T) {
	model := mockllm.New("ok")
	server, _ := newTestServer(t, model, "mock-llm", app.AccessAsk)
	client := server.Client()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/health", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("allowed origin request: %v", err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("allow origin = %q", got)
	}

	req, err = http.NewRequest(http.MethodGet, server.URL+"/api/health", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.test")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("denied origin request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("denied status = %d", resp.StatusCode)
	}

	req, err = http.NewRequest(http.MethodOptions, server.URL+"/api/threads", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("preflight request: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") || !strings.Contains(got, "PUT") {
		t.Fatalf("allow methods = %q", got)
	}

	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/threads/missing", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing thread status = %d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodPost, server.URL+"/api/threads", map[string]any{
		"access_mode": "root",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad access mode status = %d", resp.StatusCode)
	}
}

// threadDetail is the GET /api/threads/{id} response. The server projects
// canonical messages into the stable frontend views.
type threadDetail struct {
	Thread    *app.Thread       `json:"thread"`
	Messages  []app.MessageView `json:"messages"`
	ActiveRun *app.RunInfo      `json:"active_run,omitempty"`
}

func viewText(view app.MessageView) string {
	var builder strings.Builder
	for _, part := range view.Content {
		if part.Type == llm.PartTypeText || part.Type == llm.PartTypeRefusal {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

type staticModelSource struct {
	info  provider.Info
	model llm.Model
}

func (s *staticModelSource) List() []provider.Info {
	info := s.info
	info.Models = append([]provider.ModelInfo(nil), s.info.Models...)
	return []provider.Info{info}
}

func (s *staticModelSource) NewModel(_ context.Context, providerID, modelID string) (llm.Model, error) {
	if providerID != s.info.ID {
		return nil, provider.ErrUnknownProvider
	}
	if modelID != s.info.Models[0].ID {
		return nil, provider.ErrUnknownModel
	}
	return s.model, nil
}

func newTestServer(t *testing.T, model llm.Model, modelID string, mode app.AccessMode) (*httptest.Server, *app.Service) {
	t.Helper()

	store, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "codjent.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	models := &staticModelSource{
		info: provider.Info{
			ID:           "test",
			Name:         "Test",
			DefaultModel: modelID,
			Models:       []provider.ModelInfo{{ID: modelID, Name: modelID}},
		},
		model: model,
	}
	svc, err := app.New(app.Config{
		Store:             store,
		Models:            models,
		Workspace:         t.TempDir(),
		DefaultProvider:   "test",
		DefaultModel:      modelID,
		DefaultAccessMode: mode,
		MaxSteps:          5,
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("app.New: %v", err)
	}

	server := httptest.NewServer(New(svc, []string{"http://localhost:3000"}))
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.Close(ctx); err != nil {
			t.Errorf("service.Close: %v", err)
		}
	})
	return server, svc
}

func doJSON(t *testing.T, client *http.Client, method, url string, body, target any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if target != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return resp
}

func readEvents(t *testing.T, client *http.Client, url string) []app.Event {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d", resp.StatusCode)
	}
	var events []app.Event
	for event := range streamEvents(resp.Body) {
		events = append(events, event)
	}
	return events
}

func streamEvents(body io.Reader) <-chan app.Event {
	events := make(chan app.Event)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event app.Event
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
				return
			}
			events <- event
		}
	}()
	return events
}

func assertEventType(t *testing.T, events []app.Event, eventType string) {
	t.Helper()
	for _, event := range events {
		if event.Type == eventType {
			return
		}
	}
	t.Fatalf("event %q not found in %+v", eventType, events)
}

func remarshal(t *testing.T, from, into any) {
	t.Helper()
	raw, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
}

type toolModel struct {
	mu    sync.Mutex
	calls int
}

func (m *toolModel) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, errors.New("not implemented")
}

func (m *toolModel) Stream(ctx context.Context, _ *llm.Request) (llm.Stream, error) {
	m.mu.Lock()
	call := m.calls
	m.calls++
	m.mu.Unlock()

	usage := llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}
	if call == 0 {
		return llm.NewSliceStream(ctx, []llm.StreamEvent{
			llm.PartStart{Index: 0, Part: llm.NewTextPart("Running.")},
			llm.PartStart{Index: 1, Part: llm.NewToolCallPart(llm.ToolCall{
				CallID: "call_1",
				Name:   "execute_command",
				Input:  json.RawMessage(`{"command":"printf http-ok"}`),
			})},
			llm.MessageEnd{FinishReason: llm.FinishReasonTool, Usage: usage},
		}), nil
	}
	return llm.NewSliceStream(ctx, []llm.StreamEvent{
		llm.PartStart{Index: 0, Part: llm.NewTextPart("Done.")},
		llm.MessageEnd{FinishReason: llm.FinishReasonStop, Usage: usage},
	}), nil
}

func (m *toolModel) ModelID() string { return "tool-model" }

func (m *toolModel) Info() llm.Info {
	return llm.Info{
		Provider: "test",
		Model:    "tool-model",
		Features: llm.Features{
			Streaming:         true,
			Tools:             true,
			ParallelToolCalls: true,
		},
	}
}
