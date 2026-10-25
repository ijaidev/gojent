package session_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
)

func TestMemory_SessionIsolation(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemory()

	first := session.Session{ID: "shared", ResourceID: "tenant-a"}
	second := session.Session{ID: "shared", ResourceID: "tenant-b"}

	if err := store.Append(ctx, first, llm.UserMessage("a")); err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if err := store.Append(ctx, second, llm.UserMessage("b")); err != nil {
		t.Fatalf("Append second: %v", err)
	}

	firstMessages, err := store.Load(ctx, first)
	if err != nil {
		t.Fatalf("Load first: %v", err)
	}
	secondMessages, err := store.Load(ctx, second)
	if err != nil {
		t.Fatalf("Load second: %v", err)
	}
	if len(firstMessages) != 1 || firstMessages[0].Text() != "a" {
		t.Errorf("tenant a messages = %+v", firstMessages)
	}
	if len(secondMessages) != 1 || secondMessages[0].Text() != "b" {
		t.Errorf("tenant b messages = %+v", secondMessages)
	}
}

func TestMemory_DeepCopiesMessages(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemory()
	sess := session.Session{ID: "thread"}

	text := llm.NewTextPart("hello")
	file := llm.FileFromBytes([]byte{1, 2, 3}, "application/octet-stream")
	original := llm.UserMessageWithParts(text, file)
	if err := store.Append(ctx, sess, original); err != nil {
		t.Fatalf("Append: %v", err)
	}
	text.Text = "mutated"
	file.Data[0] = 9

	loaded, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded[0].Text() != "hello" {
		t.Errorf("stored text was mutated: %q", loaded[0].Text())
	}
	loadedFile, ok := loaded[0].Content[1].(*llm.FilePart)
	if !ok {
		t.Fatalf("loaded content[1] = %T, want file", loaded[0].Content[1])
	}
	if loadedFile.Data[0] != 1 {
		t.Errorf("stored data was mutated: %v", loadedFile.Data)
	}

	loaded[0].Content[0].(*llm.TextPart).Text = "changed by caller"
	again, err := store.Load(ctx, sess)
	if err != nil {
		t.Fatalf("Load again: %v", err)
	}
	if again[0].Text() != "hello" {
		t.Errorf("caller mutation leaked into the store: %q", again[0].Text())
	}
}

func TestMemory_ValidationAndCancellation(t *testing.T) {
	store := session.NewMemory()
	ctx := context.Background()

	if _, err := store.Load(ctx, session.Session{}); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Load without ID = %v, want ErrIDRequired", err)
	}
	if err := store.Append(ctx, session.Session{}, llm.UserMessage("x")); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Append without ID = %v, want ErrIDRequired", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrMessagesRequired) {
		t.Errorf("Append without messages = %v, want ErrMessagesRequired", err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Load(canceled, session.Session{ID: "s"}); !errors.Is(err, context.Canceled) {
		t.Errorf("Load with canceled context = %v", err)
	}
	if err := store.Append(canceled, session.Session{ID: "s"}, llm.UserMessage("x")); !errors.Is(err, context.Canceled) {
		t.Errorf("Append with canceled context = %v", err)
	}
}

func TestMemory_Close(t *testing.T) {
	store := session.NewMemory()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := store.Load(context.Background(), session.Session{ID: "s"}); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Load after Close = %v, want ErrClosed", err)
	}
	if err := store.Append(context.Background(), session.Session{ID: "s"}, llm.UserMessage("x")); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
}

func TestMemory_ConcurrentAppends(t *testing.T) {
	store := session.NewMemory()
	sess := session.Session{ID: "concurrent"}

	const writers = 20
	const perWriter = 25
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := store.Append(context.Background(), sess, llm.UserMessage("x")); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	messages, err := store.Load(context.Background(), sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != writers*perWriter {
		t.Errorf("messages = %d, want %d", len(messages), writers*perWriter)
	}
}
