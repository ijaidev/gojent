package llm_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
)

func TestPart_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.png")
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	part, err := llm.ImageFromFile(filePath)
	if err != nil {
		t.Fatalf("ImageFromFile failed: %v", err)
	}
	if part.Type != llm.PartTypeImage || len(part.Data) != len(data) {
		t.Errorf("unexpected part: %+v", part)
	}
	if part.MIMEType != "image/png" {
		t.Errorf("expected mime image/png, got %s", part.MIMEType)
	}
}

func TestPart_Validate(t *testing.T) {
	// Valid text
	if err := llm.TextPart("hello").Validate(); err != nil {
		t.Errorf("unexpected error for valid text part: %v", err)
	}

	// Empty text
	if err := (llm.Part{Type: llm.PartTypeText}).Validate(); err == nil {
		t.Error("expected error for empty text part")
	}

	// Valid image bytes
	if err := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png").Validate(); err != nil {
		t.Errorf("unexpected error for valid image part: %v", err)
	}

	// Invalid empty media part
	if err := (llm.Part{Type: llm.PartTypeImage}).Validate(); err == nil {
		t.Error("expected error for empty image part")
	}

	// Invalid part type
	if err := (llm.Part{Type: "unknown"}).Validate(); err == nil {
		t.Error("expected error for unknown part type")
	}
}
