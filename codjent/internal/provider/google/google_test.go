package googleprovider

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ijaidev/gojent/codjent/internal/provider"
)

func TestInfoDefaults(t *testing.T) {
	p := New(Config{})
	info := p.Info()
	if info.ID != ID || info.Name == "" {
		t.Fatalf("info = %+v", info)
	}
	if info.DefaultModel != DefaultModel {
		t.Fatalf("default model = %q", info.DefaultModel)
	}
	if len(info.Models) != 1 || info.Models[0].ID != DefaultModel {
		t.Fatalf("models = %+v", info.Models)
	}
}

func TestInfoCustomModels(t *testing.T) {
	p := New(Config{
		Models:       []string{"custom-a", " custom-b ", "custom-a"},
		DefaultModel: "custom-b",
	})
	info := p.Info()
	if info.DefaultModel != "custom-b" {
		t.Fatalf("default = %q", info.DefaultModel)
	}
	ids := make([]string, 0, len(info.Models))
	for _, model := range info.Models {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"custom-a", "custom-b"}) {
		t.Fatalf("models = %v", ids)
	}
}

func TestDefaultModelAddedToModels(t *testing.T) {
	p := New(Config{Models: []string{"only"}, DefaultModel: "default"})
	info := p.Info()
	ids := make([]string, 0, len(info.Models))
	for _, model := range info.Models {
		ids = append(ids, model.ID)
	}
	if !slices.Equal(ids, []string{"default", "only"}) {
		t.Fatalf("models = %v", ids)
	}
}

func TestNewModelRejectsUnknown(t *testing.T) {
	p := New(Config{Models: []string{"known"}})
	if _, err := p.NewModel(context.Background(), "missing"); !errors.Is(err, provider.ErrUnknownModel) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewModelRequiresAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	p := New(Config{})
	if _, err := p.NewModel(context.Background(), DefaultModel); err == nil {
		t.Fatal("expected missing API key error")
	}
}

func TestInfoClonesModels(t *testing.T) {
	p := New(Config{})
	info := p.Info()
	info.Models[0].ID = "mutated"
	if got := p.Info().Models[0].ID; got == "mutated" {
		t.Fatal("Info leaked internal model slice")
	}
}
