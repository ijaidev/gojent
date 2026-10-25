package provider

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

type fakeProvider struct {
	info providerInfo
	err  error
}

type providerInfo = Info

func (p *fakeProvider) Info() Info { return p.info }
func (p *fakeProvider) NewModel(context.Context, string) (llm.Model, error) {
	if p.err != nil {
		return nil, p.err
	}
	return fakeModel{}, nil
}

type fakeModel struct{}

func (fakeModel) Generate(context.Context, *llm.Request) (*llm.Response, error) {
	return nil, nil
}
func (fakeModel) Stream(context.Context, *llm.Request) (llm.Stream, error) {
	return nil, nil
}
func (fakeModel) ModelID() string { return "fake" }
func (fakeModel) Info() llm.Info  { return llm.Info{Provider: "fake", Model: "fake"} }

func TestRegistry(t *testing.T) {
	first := &fakeProvider{info: Info{
		ID:           "one",
		Name:         "One",
		DefaultModel: "one-default",
		Models:       []ModelInfo{{ID: "one-default"}},
	}}
	second := &fakeProvider{info: Info{
		ID:           "two",
		Name:         "Two",
		DefaultModel: "two-default",
		Models:       []ModelInfo{{ID: "two-default"}},
	}}

	registry, err := NewRegistry(first, second)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	infos := registry.List()
	if len(infos) != 2 || infos[0].ID != "one" || infos[1].ID != "two" {
		t.Fatalf("List = %+v", infos)
	}

	if _, err := registry.NewModel(context.Background(), "missing", "model"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
	if _, err := registry.NewModel(context.Background(), "one", "missing"); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("unknown model error = %v", err)
	}
	if _, err := registry.NewModel(context.Background(), "", ""); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if _, err := NewRegistry(); err == nil {
		t.Fatal("expected empty registry error")
	}
	if _, err := NewRegistry(first, first); err == nil {
		t.Fatal("expected duplicate provider error")
	}
}

func TestRegistryClonesModelLists(t *testing.T) {
	p := &fakeProvider{info: Info{
		ID:     "one",
		Models: []ModelInfo{{ID: "m"}},
	}}
	registry, err := NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	infos := registry.List()
	infos[0].Models[0].ID = "mutated"
	if got := registry.List()[0].Models[0].ID; got != "m" {
		t.Fatalf("model list mutated: %q", got)
	}
}

func TestRegistryPreservesOrderAndClones(t *testing.T) {
	a := &fakeProvider{info: Info{ID: "a", Models: []ModelInfo{{ID: "m"}}}}
	b := &fakeProvider{info: Info{ID: "b", Models: []ModelInfo{{ID: "m"}}}}
	registry, err := NewRegistry(a, b)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 2)
	for _, info := range registry.List() {
		ids = append(ids, info.ID)
	}
	if !slices.Equal(ids, []string{"a", "b"}) {
		t.Fatalf("order = %v", ids)
	}
}
