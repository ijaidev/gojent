package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/provider"
)

func testProviderConfig() *provider.Config {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	return &provider.Config{
		ID:           "prv_test",
		Name:         "Local",
		Format:       provider.FormatOpenAICompletion,
		BaseURL:      "http://localhost:11434/v1",
		DefaultModel: "llama3",
		Models: []provider.ModelInfo{
			{ID: "llama3", Name: "Llama 3"},
			{ID: "qwen3", Name: "Qwen 3"},
		},
		APIKey:    "secret",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestProviderConfigLifecycle(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	config := testProviderConfig()

	if err := store.CreateProviderConfig(ctx, config); err != nil {
		t.Fatalf("CreateProviderConfig: %v", err)
	}

	got, err := store.GetProviderConfig(ctx, config.ID)
	if err != nil {
		t.Fatalf("GetProviderConfig: %v", err)
	}
	if got.Name != config.Name || got.Format != config.Format || got.BaseURL != config.BaseURL ||
		got.DefaultModel != config.DefaultModel || got.APIKey != config.APIKey {
		t.Fatalf("GetProviderConfig = %+v", got)
	}
	if !got.CreatedAt.Equal(config.CreatedAt) {
		t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, config.CreatedAt)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "llama3" || got.Models[1].ID != "qwen3" {
		t.Fatalf("Models = %+v", got.Models)
	}

	configs, err := store.ListProviderConfigs(ctx)
	if err != nil {
		t.Fatalf("ListProviderConfigs: %v", err)
	}
	if len(configs) != 1 || configs[0].ID != config.ID {
		t.Fatalf("ListProviderConfigs = %+v", configs)
	}

	config.Name = "Renamed"
	config.Models = []provider.ModelInfo{{ID: "qwen3", Name: "Qwen 3"}}
	config.DefaultModel = "qwen3"
	config.UpdatedAt = config.UpdatedAt.Add(time.Minute)
	if err := store.UpdateProviderConfig(ctx, config); err != nil {
		t.Fatalf("UpdateProviderConfig: %v", err)
	}
	got, err = store.GetProviderConfig(ctx, config.ID)
	if err != nil {
		t.Fatalf("GetProviderConfig after update: %v", err)
	}
	if got.Name != "Renamed" || got.DefaultModel != "qwen3" {
		t.Fatalf("updated config = %+v", got)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "qwen3" {
		t.Fatalf("updated models = %+v", got.Models)
	}

	if err := store.DeleteProviderConfig(ctx, config.ID); err != nil {
		t.Fatalf("DeleteProviderConfig: %v", err)
	}
	if _, err := store.GetProviderConfig(ctx, config.ID); !errors.Is(err, provider.ErrConfigNotFound) {
		t.Fatalf("GetProviderConfig after delete = %v, want ErrConfigNotFound", err)
	}
	if err := store.DeleteProviderConfig(ctx, config.ID); !errors.Is(err, provider.ErrConfigNotFound) {
		t.Fatalf("DeleteProviderConfig missing = %v, want ErrConfigNotFound", err)
	}
}

func TestProviderConfigPreservesOrder(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	first := testProviderConfig()
	first.ID = "prv_first"
	first.CreatedAt = first.CreatedAt.Add(-time.Minute)
	second := testProviderConfig()
	second.ID = "prv_second"
	second.Name = "Second"

	if err := store.CreateProviderConfig(ctx, first); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if err := store.CreateProviderConfig(ctx, second); err != nil {
		t.Fatalf("Create second: %v", err)
	}

	configs, err := store.ListProviderConfigs(ctx)
	if err != nil {
		t.Fatalf("ListProviderConfigs: %v", err)
	}
	if len(configs) != 2 || configs[0].ID != first.ID || configs[1].ID != second.ID {
		t.Fatalf("ListProviderConfigs = %+v", configs)
	}
	if len(configs[0].Models) != 2 || configs[0].Models[0].ID != "llama3" {
		t.Fatalf("model order = %+v", configs[0].Models)
	}
}

func TestDeleteProviderConfigRejectsInUse(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	config := testProviderConfig()
	if err := store.CreateProviderConfig(ctx, config); err != nil {
		t.Fatalf("CreateProviderConfig: %v", err)
	}

	thread := testThread()
	thread.Provider = config.ID
	thread.Model = config.DefaultModel
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	if err := store.DeleteProviderConfig(ctx, config.ID); !errors.Is(err, provider.ErrConfigInUse) {
		t.Fatalf("DeleteProviderConfig in use = %v, want ErrConfigInUse", err)
	}
	if _, err := store.GetProviderConfig(ctx, config.ID); err != nil {
		t.Fatalf("provider was deleted while in use: %v", err)
	}

	if err := store.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if err := store.DeleteProviderConfig(ctx, config.ID); err != nil {
		t.Fatalf("DeleteProviderConfig after thread removal: %v", err)
	}
}

func TestCountThreadsByProvider(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	count, err := store.CountThreadsByProvider(ctx, "google")
	if err != nil {
		t.Fatalf("CountThreadsByProvider: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}

	thread := testThread()
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}
	count, err = store.CountThreadsByProvider(ctx, thread.Provider)
	if err != nil {
		t.Fatalf("CountThreadsByProvider: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestCreateProviderConfigRejectsNil(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.CreateProviderConfig(context.Background(), nil); err == nil {
		t.Fatal("CreateProviderConfig(nil) returned nil error")
	}
}
