package storage_test

import (
	"testing"

	"github.com/ijaidev/gojent/core/storage"
)

func TestThreadClone_IsDeep(t *testing.T) {
	original := &storage.Thread{
		ID:       "thread-1",
		Title:    "Title",
		Metadata: map[string]any{"key": "value"},
	}

	clone := original.Clone()
	clone.Title = "Changed"
	clone.Metadata["key"] = "changed"

	if original.Title != "Title" {
		t.Error("title mutation leaked into the original")
	}
	if original.Metadata["key"] != "value" {
		t.Error("metadata mutation leaked into the original")
	}
}
