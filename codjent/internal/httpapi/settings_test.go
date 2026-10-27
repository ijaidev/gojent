package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/provider/catalog"
	"github.com/ijaidev/gojent/codjent/internal/store/sqlite"
	"github.com/ijaidev/gojent/providers/mockllm"
)

func newSettingsTestServer(t *testing.T) (*httptest.Server, *catalog.Catalog) {
	t.Helper()

	store, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "codjent.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	cat, err := catalog.New(context.Background(), store)
	if err != nil {
		_ = store.Close()
		t.Fatalf("catalog.New: %v", err)
	}
	svc, err := app.New(app.Config{
		Store:             store,
		Models:            cat,
		Workspace:         t.TempDir(),
		DefaultAccessMode: app.AccessAsk,
		MaxSteps:          5,
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("app.New: %v", err)
	}

	server := httptest.NewServer(NewWithCatalog(svc, cat, nil, ""))
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := svc.Close(ctx); err != nil {
			t.Errorf("service.Close: %v", err)
		}
	})
	return server, cat
}

func TestProviderSettingsFlow(t *testing.T) {
	server, _ := newSettingsTestServer(t)
	client := server.Client()
	base := server.URL + "/api/settings/providers"

	var configs []provider.Config
	resp := doJSON(t, client, http.MethodGet, base, nil, &configs)
	if resp.StatusCode != http.StatusOK || len(configs) != 0 {
		t.Fatalf("initial configs = %+v (status %d)", configs, resp.StatusCode)
	}

	var created provider.Config
	resp = doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":          "Local",
		"format":        "openai-completion",
		"base_url":      "http://localhost:11434/v1",
		"default_model": "llama3",
		"models": []any{
			map[string]any{"id": "llama3", "name": "Llama 3"},
			map[string]any{"id": "qwen3"},
		},
		"headers": []any{
			map[string]any{"name": "x-opencode-session", "value": "{{session.id}}", "dynamic": true},
			map[string]any{"name": "X-Static", "value": "one"},
		},
		"api_key": "secret-key",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", resp.StatusCode)
	}
	if created.ID == "" || created.Name != "Local" || created.DefaultModel != "llama3" {
		t.Fatalf("created = %+v", created)
	}
	if created.APIKey != "" || !created.APIKeySet {
		t.Fatalf("created leaked key state: %+v", created)
	}
	if len(created.Models) != 2 || created.Models[1].Name != "qwen3" {
		t.Fatalf("created models = %+v", created.Models)
	}
	if len(created.Headers) != 2 ||
		created.Headers[0].Name != "X-Opencode-Session" ||
		created.Headers[0].Value != "{{session.id}}" ||
		!created.Headers[0].Dynamic ||
		created.Headers[1].Name != "X-Static" {
		t.Fatalf("created headers = %+v", created.Headers)
	}

	// The stored secret must never appear in a list response.
	rawResp, err := client.Get(base)
	if err != nil {
		t.Fatalf("get configs: %v", err)
	}
	body, err := io.ReadAll(rawResp.Body)
	_ = rawResp.Body.Close()
	if err != nil {
		t.Fatalf("read configs: %v", err)
	}
	if strings.Contains(string(body), "secret-key") {
		t.Fatalf("list response leaks API key: %s", body)
	}

	var infos []provider.Info
	resp = doJSON(t, client, http.MethodGet, server.URL+"/api/providers", nil, &infos)
	if resp.StatusCode != http.StatusOK || len(infos) != 1 || infos[0].ID != created.ID {
		t.Fatalf("providers = %+v (status %d)", infos, resp.StatusCode)
	}

	// An empty API key on update keeps the stored secret.
	var updated provider.Config
	resp = doJSON(t, client, http.MethodPut, base+"/"+created.ID, map[string]any{
		"name":          "Renamed",
		"format":        "openai-completion",
		"default_model": "qwen3",
		"models":        []any{map[string]any{"id": "qwen3", "name": "Qwen 3"}},
		"headers":       []any{map[string]any{"name": "X-Static", "value": "two"}},
	}, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", resp.StatusCode)
	}
	if updated.Name != "Renamed" || updated.DefaultModel != "qwen3" || len(updated.Models) != 1 {
		t.Fatalf("updated = %+v", updated)
	}
	if len(updated.Headers) != 1 || updated.Headers[0].Value != "two" || updated.Headers[0].Dynamic {
		t.Fatalf("updated headers = %+v", updated.Headers)
	}
	if !updated.APIKeySet {
		t.Fatal("update cleared the stored API key")
	}

	// A thread pins the provider, so deletion is refused until it is removed.
	// Empty provider/model fields resolve against the live catalog.
	var thread app.Thread
	resp = doJSON(t, client, http.MethodPost, server.URL+"/api/threads", map[string]any{}, &thread)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create thread status = %d", resp.StatusCode)
	}
	if thread.Provider != created.ID || thread.Model != "qwen3" {
		t.Fatalf("default thread selection = %s/%s", thread.Provider, thread.Model)
	}

	resp = doJSON(t, client, http.MethodDelete, base+"/"+created.ID, nil, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete in-use status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}

	resp = doJSON(t, client, http.MethodDelete, server.URL+"/api/threads/"+thread.ID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete thread status = %d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodDelete, base+"/"+created.ID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete provider status = %d", resp.StatusCode)
	}

	resp = doJSON(t, client, http.MethodGet, base, nil, &configs)
	if resp.StatusCode != http.StatusOK || len(configs) != 0 {
		t.Fatalf("configs after delete = %+v (status %d)", configs, resp.StatusCode)
	}
}

func TestProviderSettingsErrors(t *testing.T) {
	server, _ := newSettingsTestServer(t)
	client := server.Client()
	base := server.URL + "/api/settings/providers"

	resp := doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":   "",
		"format": "google",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid create status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":   "Local",
		"format": "vendor",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown format status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":   "Local",
		"format": "openai-completion",
		"headers": []any{
			map[string]any{"name": "X-Test", "value": "a"},
			map[string]any{"name": "x-test", "value": "b"},
		},
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate header status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":   "Google",
		"format": "google",
		"headers": []any{
			map[string]any{"name": "x-goog-api-key", "value": "other"},
		},
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("google key header status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = doJSON(t, client, http.MethodPost, base, map[string]any{
		"name":   "Gateway",
		"format": "openai-completion",
		"headers": []any{
			map[string]any{"name": "X-Test", "value": "{{session.missing}}", "dynamic": true},
		},
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown placeholder status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = doJSON(t, client, http.MethodPut, base+"/missing", map[string]any{
		"name":   "Local",
		"format": "google",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing update status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	resp = doJSON(t, client, http.MethodDelete, base+"/missing", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing delete status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	// A malformed body is a bad request.
	req, err := http.NewRequest(http.MethodPost, base, strings.NewReader("{"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post malformed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed create status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestSettingsRoutesRequireCatalog(t *testing.T) {
	server, _ := newTestServer(t, mockllm.New("hello"), "test-model", app.AccessAsk)
	client := server.Client()

	resp, err := client.Get(server.URL + "/api/settings/providers")
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("settings status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
