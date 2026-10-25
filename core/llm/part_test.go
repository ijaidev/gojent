package llm_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestFilePart_Constructors(t *testing.T) {
	tests := []struct {
		name string
		part *llm.FilePart
	}{
		{"bytes", llm.FileFromBytes([]byte("data"), "image/png")},
		{"url", llm.FileFromURL("https://example.com/a.png", "image/png")},
		{"id", llm.FileFromID("file-1", "application/pdf")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.part.PartType() != llm.PartTypeFile {
				t.Errorf("type = %q", tt.part.PartType())
			}
			msg := llm.UserMessageWithParts(tt.part)
			if err := msg.Validate(); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestFilePart_Validate(t *testing.T) {
	twoSources := llm.FileFromBytes([]byte("x"), "image/png")
	twoSources.URL = "https://example.com/a.png"

	tests := []struct {
		name string
		part *llm.FilePart
	}{
		{"missing mime", llm.FileFromBytes([]byte("x"), "")},
		{"bad mime", llm.FileFromBytes([]byte("x"), "not a mime")},
		{"no source", llm.FileFromBytes(nil, "image/png")},
		{"two sources", twoSources},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := llm.UserMessageWithParts(tt.part).Validate()
			if !errors.Is(err, llm.ErrInvalidPart) {
				t.Fatalf("Validate = %v, want ErrInvalidPart", err)
			}
		})
	}
}

func TestFileFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.png")
	// Minimal PNG header; MIME detection uses the extension first.
	data := []byte("\x89PNG\r\n\x1a\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	part, err := llm.FileFromFile(path)
	if err != nil {
		t.Fatalf("FileFromFile: %v", err)
	}
	if part.MIMEType != "image/png" {
		t.Errorf("MIME = %q", part.MIMEType)
	}
	if part.Filename != "sample.png" {
		t.Errorf("filename = %q", part.Filename)
	}
	if string(part.Data) != string(data) {
		t.Errorf("data = %q", part.Data)
	}
	if err := llm.UserMessageWithParts(part).Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestReasoningPart_Validate(t *testing.T) {
	summary := llm.NewReasoningPart("")
	summary.Summary = []string{"step"}
	redacted := llm.NewReasoningPart("")
	redacted.Redacted = true
	valid := []*llm.ReasoningPart{
		llm.NewReasoningPart("thought"),
		summary,
		redacted,
	}
	for i, part := range valid {
		if err := llm.AssistantMessageWithParts(part).Validate(); err != nil {
			t.Errorf("valid[%d]: %v", i, err)
		}
	}
	if err := llm.AssistantMessageWithParts(llm.NewReasoningPart("")).Validate(); !errors.Is(err, llm.ErrInvalidPart) {
		t.Errorf("empty reasoning = %v, want ErrInvalidPart", err)
	}
}

func TestRefusalPart_Validate(t *testing.T) {
	if err := llm.AssistantMessageWithParts(llm.NewRefusalPart("no")).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := llm.AssistantMessageWithParts(llm.NewRefusalPart("")).Validate(); !errors.Is(err, llm.ErrInvalidPart) {
		t.Fatalf("empty refusal = %v, want ErrInvalidPart", err)
	}
}
