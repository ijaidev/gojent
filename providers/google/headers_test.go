package google_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/llm/llmtest"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/providers/google"
	"google.golang.org/genai"
)

func headerRequest() *llm.Request {
	return &llm.Request{Messages: []llm.Message{llm.UserMessage("hi")}}
}

func writeHeaderJSONResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	writeJSON(t, w, map[string]any{
		"candidates": []map[string]any{{
			"content":      map[string]any{"role": "model", "parts": []map[string]any{{"text": "hello"}}},
			"finishReason": "STOP",
		}},
	})
}

func TestNew_RejectsInvalidHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers http.Header
		want    string
	}{
		{name: "invalid name", headers: http.Header{"Bad Name": {"value"}}, want: "invalid name"},
		{name: "invalid value", headers: http.Header{"X-Test": {"a\nb"}}, want: "invalid value"},
		{name: "duplicate case-insensitive name", headers: http.Header{"X-Test": {"a"}, "x-test": {"b"}}, want: "duplicate name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := google.New(context.Background(), google.Config{
				Model:   "gemini-test",
				Client:  &genai.Client{},
				Headers: tc.headers,
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "google:") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want prefix google: and %q", err, tc.want)
			}
		})
	}
}

func TestGenerate_SendsConfiguredHeaders(t *testing.T) {
	var got http.Header
	model := newModelWithConfig(t, google.Config{
		Headers: http.Header{
			"X-Static":       {"one", "two"},
			"x-tenant":       {"tenant-a"},
			"X-Goog-Api-Key": {"gateway-key"},
		},
	}, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeHeaderJSONResponse(t, w)
	})

	if _, err := model.Generate(context.Background(), headerRequest()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if values := got.Values("X-Static"); len(values) != 2 || values[0] != "one" || values[1] != "two" {
		t.Errorf("X-Static = %v, want [one two]", values)
	}
	if value := got.Get("X-Tenant"); value != "tenant-a" {
		t.Errorf("X-Tenant = %q, want tenant-a", value)
	}
	// The SDK sets x-goog-api-key after applying request headers, so the
	// configured API key always wins over a Header value.
	if value := got.Get("X-Goog-Api-Key"); value != "test-key" {
		t.Errorf("X-Goog-Api-Key = %q, want the SDK API key", value)
	}
}

func TestGenerate_HeaderFuncSeesSessionAndOverridesStaticHeaders(t *testing.T) {
	var (
		got       http.Header
		funcCalls atomic.Int32
	)
	model := newModelWithConfig(t, google.Config{
		Headers: http.Header{"X-Tenant": {"static"}, "X-Static": {"kept"}},
		HeaderFunc: func(ctx context.Context) (http.Header, error) {
			funcCalls.Add(1)
			sess, ok := session.FromContext(ctx)
			if !ok {
				return nil, errors.New("missing session")
			}
			return http.Header{
				"X-Tenant":  {sess.ResourceID},
				"X-Session": {sess.ID},
			}, nil
		},
	}, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeHeaderJSONResponse(t, w)
	})

	ctx := session.NewContext(context.Background(), session.Session{ID: "conversation", ResourceID: "tenant-a"})
	if _, err := model.Generate(ctx, headerRequest()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if value := got.Get("X-Tenant"); value != "tenant-a" {
		t.Errorf("X-Tenant = %q, want the HeaderFunc value", value)
	}
	if value := got.Get("X-Session"); value != "conversation" {
		t.Errorf("X-Session = %q, want conversation", value)
	}
	if value := got.Get("X-Static"); value != "kept" {
		t.Errorf("X-Static = %q, want the static value", value)
	}
	if calls := funcCalls.Load(); calls != 1 {
		t.Errorf("HeaderFunc calls = %d, want 1", calls)
	}
}

func TestGenerate_HeaderFuncErrorSkipsRequest(t *testing.T) {
	headerErr := errors.New("header boom")
	var requested atomic.Bool
	model := newModelWithConfig(t, google.Config{
		HeaderFunc: func(context.Context) (http.Header, error) {
			return nil, headerErr
		},
	}, func(w http.ResponseWriter, _ *http.Request) {
		requested.Store(true)
		writeHeaderJSONResponse(t, w)
	})

	_, err := model.Generate(context.Background(), headerRequest())
	if !errors.Is(err, headerErr) {
		t.Fatalf("err = %v, want the HeaderFunc error", err)
	}
	if !strings.Contains(err.Error(), "header function") {
		t.Errorf("err = %v, want a header function context", err)
	}
	if requested.Load() {
		t.Error("unexpected HTTP request after HeaderFunc failed")
	}
}

func TestGenerate_RejectsInvalidDynamicHeaders(t *testing.T) {
	var requested atomic.Bool
	model := newModelWithConfig(t, google.Config{
		HeaderFunc: func(context.Context) (http.Header, error) {
			return http.Header{"Bad Name": {"value"}}, nil
		},
	}, func(w http.ResponseWriter, _ *http.Request) {
		requested.Store(true)
		writeHeaderJSONResponse(t, w)
	})

	_, err := model.Generate(context.Background(), headerRequest())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid name") {
		t.Fatalf("err = %v, want invalid name", err)
	}
	if requested.Load() {
		t.Error("unexpected HTTP request after header validation failed")
	}
}

func TestStream_SendsConfiguredHeaders(t *testing.T) {
	var (
		got       http.Header
		funcCalls atomic.Int32
	)
	model := newModelWithConfig(t, google.Config{
		Headers: http.Header{"X-Static": {"static"}},
		HeaderFunc: func(ctx context.Context) (http.Header, error) {
			funcCalls.Add(1)
			sess, ok := session.FromContext(ctx)
			if !ok {
				return nil, errors.New("missing session")
			}
			return http.Header{"X-Session": {sess.ID}}, nil
		},
	}, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeSSE(t, w, streamCandidate([]map[string]any{{"text": "hello"}}, "STOP", nil))
	})

	ctx := session.NewContext(context.Background(), session.Session{ID: "stream-conversation"})
	stream, err := model.Stream(ctx, headerRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if response := llmtest.Collect(t, stream); response.Message.Text() != "hello" {
		t.Fatalf("text = %q", response.Message.Text())
	}

	if value := got.Get("X-Static"); value != "static" {
		t.Errorf("X-Static = %q, want static", value)
	}
	if value := got.Get("X-Session"); value != "stream-conversation" {
		t.Errorf("X-Session = %q, want stream-conversation", value)
	}
	if calls := funcCalls.Load(); calls != 1 {
		t.Errorf("HeaderFunc calls = %d, want 1", calls)
	}
}

func TestStream_HeaderFuncErrorSkipsRequest(t *testing.T) {
	headerErr := errors.New("header boom")
	var requested atomic.Bool
	model := newModelWithConfig(t, google.Config{
		HeaderFunc: func(context.Context) (http.Header, error) {
			return nil, headerErr
		},
	}, func(w http.ResponseWriter, _ *http.Request) {
		requested.Store(true)
		writeHeaderJSONResponse(t, w)
	})

	_, err := model.Stream(context.Background(), headerRequest())
	if !errors.Is(err, headerErr) {
		t.Fatalf("err = %v, want the HeaderFunc error", err)
	}
	if requested.Load() {
		t.Error("unexpected HTTP request after HeaderFunc failed")
	}
}

func TestGenerate_RequestHeadersOverrideClientConfigHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeHeaderJSONResponse(t, w)
	}))
	t.Cleanup(server.Close)

	model, err := google.New(context.Background(), google.Config{
		Model:  "gemini-test",
		APIKey: "test-key",
		ClientConfig: &genai.ClientConfig{
			HTTPOptions: genai.HTTPOptions{
				BaseURL: server.URL,
				Headers: http.Header{
					"X-Client":   {"from-client"},
					"X-Override": {"from-client"},
				},
			},
		},
		Headers: http.Header{
			"X-Override": {"from-request"},
			"X-Extra":    {"request"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := model.Generate(context.Background(), headerRequest()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if value := got.Get("X-Client"); value != "from-client" {
		t.Errorf("X-Client = %q, want the client header", value)
	}
	if value := got.Get("X-Override"); value != "from-request" {
		t.Errorf("X-Override = %q, want the request header to win", value)
	}
	if value := got.Get("X-Extra"); value != "request" {
		t.Errorf("X-Extra = %q, want request", value)
	}
}

func TestGenerate_PreconfiguredClientKeepsRequestHeaders(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		writeHeaderJSONResponse(t, w)
	}))
	t.Cleanup(server.Close)

	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey: "client-key",
		HTTPOptions: genai.HTTPOptions{
			BaseURL: server.URL,
			Headers: http.Header{"X-Client": {"from-client"}},
		},
	})
	if err != nil {
		t.Fatalf("genai.NewClient: %v", err)
	}
	model, err := google.New(context.Background(), google.Config{
		Model:  "gemini-test",
		Client: client,
		Headers: http.Header{
			"X-Client": {"from-request"},
			"X-Extra":  {"request"},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := model.Generate(context.Background(), headerRequest()); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if value := got.Get("X-Client"); value != "from-request" {
		t.Errorf("X-Client = %q, want the request header to win", value)
	}
	if value := got.Get("X-Extra"); value != "request" {
		t.Errorf("X-Extra = %q, want request", value)
	}
	if value := got.Get("X-Goog-Api-Key"); value != "client-key" {
		t.Errorf("X-Goog-Api-Key = %q, want the client key", value)
	}
}
