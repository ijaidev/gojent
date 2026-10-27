package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ijaidev/gojent/codjent/internal/app"
	"github.com/ijaidev/gojent/codjent/internal/config"
	"github.com/ijaidev/gojent/codjent/internal/provider"
	"github.com/ijaidev/gojent/codjent/internal/provider/catalog"
	googleprovider "github.com/ijaidev/gojent/codjent/internal/provider/google"
	"github.com/ijaidev/gojent/codjent/internal/store/sqlite"
)

func newSeedStore(t *testing.T) (*sqlite.Store, *catalog.Catalog) {
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
	return store, cat
}

func TestSeedGoogleProvider(t *testing.T) {
	t.Run("fresh install without credentials", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")
		store, cat := newSeedStore(t)

		if err := seedGoogleProvider(context.Background(), cat, store, config.Config{}); err != nil {
			t.Fatalf("seedGoogleProvider: %v", err)
		}
		if len(cat.Configs()) != 0 {
			t.Fatalf("Configs = %+v, want none", cat.Configs())
		}
	})

	t.Run("existing google threads", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")
		store, cat := newSeedStore(t)

		now := time.Now().UTC()
		thread := &app.Thread{
			ID:         "th_legacy",
			Title:      "Legacy",
			Provider:   googleprovider.ID,
			Model:      googleprovider.DefaultModel,
			AccessMode: app.AccessAsk,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := store.CreateThread(context.Background(), thread); err != nil {
			t.Fatalf("CreateThread: %v", err)
		}

		if err := seedGoogleProvider(context.Background(), cat, store, config.Config{}); err != nil {
			t.Fatalf("seedGoogleProvider: %v", err)
		}
		configs := cat.Configs()
		if len(configs) != 1 || configs[0].ID != googleprovider.ID {
			t.Fatalf("Configs = %+v, want google", configs)
		}
		if configs[0].DefaultModel != googleprovider.DefaultModel {
			t.Fatalf("DefaultModel = %q", configs[0].DefaultModel)
		}
	})

	t.Run("google credentials", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "test-key")
		store, cat := newSeedStore(t)

		if err := seedGoogleProvider(context.Background(), cat, store, config.Config{}); err != nil {
			t.Fatalf("seedGoogleProvider: %v", err)
		}
		if len(cat.Configs()) != 1 || cat.Configs()[0].ID != googleprovider.ID {
			t.Fatalf("Configs = %+v, want google", cat.Configs())
		}
	})

	t.Run("existing providers win", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "test-key")
		store, cat := newSeedStore(t)
		created, err := cat.Create(context.Background(), provider.ConfigInput{
			Name:   "Local",
			Format: provider.FormatOpenAICompletion,
			Models: []provider.ModelInfo{{ID: "llama3"}},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if err := seedGoogleProvider(context.Background(), cat, store, config.Config{}); err != nil {
			t.Fatalf("seedGoogleProvider: %v", err)
		}
		configs := cat.Configs()
		if len(configs) != 1 || configs[0].ID != created.ID {
			t.Fatalf("Configs = %+v, want only the existing provider", configs)
		}
	})
}
