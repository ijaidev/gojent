package catalog_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/provider/catalog"
	"github.com/ijaidev/gojent/codjent/internal/store/sqlite"
)

func newTestCatalog(t *testing.T) (*catalog.Catalog, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "codjent.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	cat, err := catalog.New(context.Background(), store)
	if err != nil {
		t.Fatalf("catalog.New: %v", err)
	}
	return cat, store
}

func testProviderInput(name string) provider.ConfigInput {
	return provider.ConfigInput{
		Name:   name,
		Format: provider.FormatOpenAICompletion,
		Models: []provider.ModelInfo{
			{ID: "llama3", Name: "Llama 3"},
			{ID: "qwen3", Name: "Qwen 3"},
		},
		APIKey: "secret-key",
	}
}

func TestCatalogLifecycle(t *testing.T) {
	cat, _ := newTestCatalog(t)
	ctx := context.Background()

	if got := cat.Configs(); len(got) != 0 {
		t.Fatalf("Configs = %+v, want empty", got)
	}
	if got := cat.List(); len(got) != 0 {
		t.Fatalf("List = %+v, want empty", got)
	}

	created, err := cat.Create(ctx, testProviderInput("Local"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty ID")
	}
	if !created.APIKeySet {
		t.Fatal("APIKeySet = false")
	}
	if created.APIKey != "" {
		t.Fatalf("Create leaked the API key: %q", created.APIKey)
	}
	if created.DefaultModel != "llama3" {
		t.Fatalf("DefaultModel = %q, want llama3", created.DefaultModel)
	}

	configs := cat.Configs()
	if len(configs) != 1 || configs[0].ID != created.ID || configs[0].Name != "Local" {
		t.Fatalf("Configs = %+v", configs)
	}
	if configs[0].APIKey != "" || !configs[0].APIKeySet {
		t.Fatalf("Configs leaked key state: %+v", configs[0])
	}

	infos := cat.List()
	if len(infos) != 1 || infos[0].ID != created.ID || infos[0].DefaultModel != "llama3" {
		t.Fatalf("List = %+v", infos)
	}
	if len(infos[0].Models) != 2 {
		t.Fatalf("List models = %+v", infos[0].Models)
	}

	// An empty API key on update keeps the stored secret.
	updated, err := cat.Update(ctx, created.ID, provider.ConfigInput{
		Name:         "Local Renamed",
		Format:       provider.FormatOpenAICompletion,
		DefaultModel: "qwen3",
		Models:       []provider.ModelInfo{{ID: "qwen3", Name: "Qwen 3"}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Local Renamed" || updated.DefaultModel != "qwen3" {
		t.Fatalf("Update = %+v", updated)
	}
	if !updated.APIKeySet {
		t.Fatal("Update cleared the API key")
	}
	if len(updated.Models) != 1 || updated.Models[0].ID != "qwen3" {
		t.Fatalf("Update models = %+v", updated.Models)
	}

	// ClearAPIKey removes the secret.
	cleared, err := cat.Update(ctx, created.ID, provider.ConfigInput{
		Name:         "Local Renamed",
		Format:       provider.FormatOpenAICompletion,
		DefaultModel: "qwen3",
		Models:       []provider.ModelInfo{{ID: "qwen3"}},
		ClearAPIKey:  true,
	})
	if err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	if cleared.APIKeySet {
		t.Fatal("ClearAPIKey did not clear the secret")
	}

	// A provider with no models is stored but cannot run a thread.
	empty, err := cat.Create(ctx, provider.ConfigInput{
		Name:   "Empty",
		Format: provider.FormatAnthropic,
	})
	if err != nil {
		t.Fatalf("Create empty: %v", err)
	}
	if len(empty.Models) != 0 || empty.DefaultModel != "" {
		t.Fatalf("empty provider = %+v", empty)
	}
	if len(cat.List()) != 1 {
		t.Fatalf("List = %+v, want only runnable providers", cat.List())
	}
	if len(cat.Configs()) != 2 {
		t.Fatalf("Configs = %+v, want both providers", cat.Configs())
	}

	if err := cat.Delete(ctx, empty.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(cat.Configs()) != 1 {
		t.Fatalf("Configs after delete = %+v", cat.Configs())
	}
	if err := cat.Delete(ctx, "missing"); !errors.Is(err, provider.ErrConfigNotFound) {
		t.Fatalf("Delete missing error = %v", err)
	}
}

func TestCatalogRejectsInvalidInput(t *testing.T) {
	cat, _ := newTestCatalog(t)

	_, err := cat.Create(context.Background(), provider.ConfigInput{Name: "", Format: provider.FormatGoogle})
	if !errors.Is(err, provider.ErrInvalidConfig) {
		t.Fatalf("Create error = %v, want ErrInvalidConfig", err)
	}

	_, err = cat.Update(context.Background(), "", testProviderInput("Local"))
	if !errors.Is(err, provider.ErrConfigNotFound) {
		t.Fatalf("Update empty ID error = %v, want ErrConfigNotFound", err)
	}
}

func TestCatalogDeleteBlockedByThreads(t *testing.T) {
	cat, store := newTestCatalog(t)
	ctx := context.Background()

	created, err := cat.Create(ctx, testProviderInput("Local"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	now := time.Now().UTC()
	thread := &app.Thread{
		ID:         "th_1",
		Title:      "Thread",
		Provider:   created.ID,
		Model:      "llama3",
		AccessMode: app.AccessAsk,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := store.CreateThread(ctx, thread); err != nil {
		t.Fatalf("CreateThread: %v", err)
	}

	if err := cat.Delete(ctx, created.ID); !errors.Is(err, provider.ErrConfigInUse) {
		t.Fatalf("Delete error = %v, want ErrConfigInUse", err)
	}
	if len(cat.Configs()) != 1 {
		t.Fatal("provider was deleted while threads reference it")
	}

	if err := store.DeleteThread(ctx, thread.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}
	if err := cat.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete after thread removal: %v", err)
	}
}

func TestCatalogResolveDefaults(t *testing.T) {
	cat, _ := newTestCatalog(t)
	ctx := context.Background()

	if _, err := cat.NewModel(ctx, "", ""); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Fatalf("NewModel with no providers = %v, want ErrUnknownProvider", err)
	}

	created, err := cat.Create(ctx, testProviderInput("Local"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	model, err := cat.NewModel(ctx, "", "")
	if err != nil {
		t.Fatalf("NewModel defaults: %v", err)
	}
	if model.ModelID() != "llama3" {
		t.Fatalf("default model ID = %q", model.ModelID())
	}

	model, err = cat.NewModel(ctx, created.ID, "qwen3")
	if err != nil {
		t.Fatalf("NewModel explicit: %v", err)
	}
	if model.ModelID() != "qwen3" {
		t.Fatalf("explicit model ID = %q", model.ModelID())
	}

	if _, err := cat.NewModel(ctx, "missing", "llama3"); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Fatalf("NewModel unknown provider = %v", err)
	}
	if _, err := cat.NewModel(ctx, created.ID, "missing"); !errors.Is(err, provider.ErrUnknownModel) {
		t.Fatalf("NewModel unknown model = %v", err)
	}
}

func TestCatalogBuildsAdaptersForEveryFormat(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-key")
	cat, _ := newTestCatalog(t)
	ctx := context.Background()

	for _, format := range provider.Formats() {
		t.Run(string(format), func(t *testing.T) {
			created, err := cat.Create(ctx, provider.ConfigInput{
				Name:   string(format),
				Format: format,
				Models: []provider.ModelInfo{{ID: "test-model"}},
				APIKey: "test-key",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			model, err := cat.NewModel(ctx, created.ID, "test-model")
			if err != nil {
				t.Fatalf("NewModel: %v", err)
			}
			if model.ModelID() != "test-model" {
				t.Fatalf("ModelID = %q", model.ModelID())
			}
		})
	}
}

func TestCatalogSeedPreservesExisting(t *testing.T) {
	cat, _ := newTestCatalog(t)
	ctx := context.Background()

	seeded, err := cat.Seed(ctx, "google", provider.ConfigInput{
		Name:   "Google Gemini",
		Format: provider.FormatGoogle,
		Models: []provider.ModelInfo{{ID: "gemini-3.8-flash"}},
	})
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if seeded.ID != "google" || seeded.DefaultModel != "gemini-3.8-flash" {
		t.Fatalf("Seed = %+v", seeded)
	}

	again, err := cat.Seed(ctx, "google", provider.ConfigInput{
		Name:   "Different",
		Format: provider.FormatGoogle,
		Models: []provider.ModelInfo{{ID: "other"}},
	})
	if err != nil {
		t.Fatalf("Seed existing: %v", err)
	}
	if again.Name != "Google Gemini" || len(again.Models) != 1 || again.Models[0].ID != "gemini-3.8-flash" {
		t.Fatalf("Seed existing changed config: %+v", again)
	}
	if len(cat.Configs()) != 1 {
		t.Fatalf("Configs = %+v, want one seeded provider", cat.Configs())
	}
}
