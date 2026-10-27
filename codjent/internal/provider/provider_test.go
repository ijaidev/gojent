package provider

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
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

func TestConfigInputNormalize(t *testing.T) {
	tests := []struct {
		name    string
		input   ConfigInput
		want    ConfigInput
		wantErr bool
	}{
		{
			name: "trims and defaults",
			input: ConfigInput{
				Name:    "  Local  ",
				Format:  FormatOpenAICompletion,
				BaseURL: " http://localhost:11434/v1/ ",
				Models:  []ModelInfo{{ID: " llama3 ", Name: "  "}},
			},
			want: ConfigInput{
				Name:         "Local",
				Format:       FormatOpenAICompletion,
				BaseURL:      "http://localhost:11434/v1",
				DefaultModel: "llama3",
				Models:       []ModelInfo{{ID: "llama3", Name: "llama3"}},
			},
		},
		{
			name: "keeps explicit default",
			input: ConfigInput{
				Name:         "Remote",
				Format:       FormatAnthropic,
				DefaultModel: "b",
				Models:       []ModelInfo{{ID: "a"}, {ID: "b", Name: "B"}},
			},
			want: ConfigInput{
				Name:         "Remote",
				Format:       FormatAnthropic,
				DefaultModel: "b",
				Models:       []ModelInfo{{ID: "a", Name: "a"}, {ID: "b", Name: "B"}},
			},
		},
		{
			name:  "allows no models",
			input: ConfigInput{Name: "Empty", Format: FormatGoogle},
			want:  ConfigInput{Name: "Empty", Format: FormatGoogle, Models: []ModelInfo{}},
		},
		{
			name:    "requires name",
			input:   ConfigInput{Format: FormatGoogle},
			wantErr: true,
		},
		{
			name:    "rejects long name",
			input:   ConfigInput{Name: strings.Repeat("x", 81), Format: FormatGoogle},
			wantErr: true,
		},
		{
			name:    "rejects unknown format",
			input:   ConfigInput{Name: "x", Format: "vendor"},
			wantErr: true,
		},
		{
			name:    "rejects bad base URL",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, BaseURL: "ftp://example.com"},
			wantErr: true,
		},
		{
			name:    "rejects credentials in base URL",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, BaseURL: "https://user:pass@example.com"},
			wantErr: true,
		},
		{
			name:    "rejects base URL query",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, BaseURL: "https://example.com/v1?key=secret"},
			wantErr: true,
		},
		{
			name:    "rejects base URL fragment",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, BaseURL: "https://example.com/v1#fragment"},
			wantErr: true,
		},
		{
			name:    "rejects duplicate models",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, Models: []ModelInfo{{ID: "a"}, {ID: "a"}}},
			wantErr: true,
		},
		{
			name:    "rejects missing model ID",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, Models: []ModelInfo{{Name: "a"}}},
			wantErr: true,
		},
		{
			name:    "rejects default outside models",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, DefaultModel: "z", Models: []ModelInfo{{ID: "a"}}},
			wantErr: true,
		},
		{
			name:    "rejects set and clear API key",
			input:   ConfigInput{Name: "x", Format: FormatGoogle, APIKey: "secret", ClearAPIKey: true},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := test.input
			err := input.Normalize()
			if test.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("Normalize error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}
			if !reflect.DeepEqual(input, test.want) {
				t.Fatalf("Normalize = %+v, want %+v", input, test.want)
			}
		})
	}
}

func TestConfigPublicHidesAPIKey(t *testing.T) {
	config := Config{
		ID:     "one",
		APIKey: "secret",
		Models: []ModelInfo{{ID: "m"}},
	}
	public := config.Public()
	if public.APIKey != "" {
		t.Fatalf("public API key = %q", public.APIKey)
	}
	if !public.APIKeySet {
		t.Fatal("APIKeySet = false")
	}
	public.Models[0].ID = "mutated"
	if config.Models[0].ID != "m" {
		t.Fatal("Public did not clone models")
	}

	data, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("serialized config leaks secret: %s", data)
	}
}

func TestFormats(t *testing.T) {
	for _, format := range Formats() {
		if !format.Valid() {
			t.Fatalf("format %q is not valid", format)
		}
	}
	if Format("other").Valid() {
		t.Fatal("unknown format reported valid")
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
