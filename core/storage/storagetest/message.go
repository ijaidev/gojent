package storagetest

import (
	"context"
	"testing"
	"time"

	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
)

func testMessageLifecycle(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Chat", ResourceID: "user-1"})

	first := storage.NewMessage(th.ID, llm.RoleUser, "Hello")
	second := storage.NewMessage(th.ID, llm.RoleAssistant, "Hi")
	second.Thinking = "greeting"
	second.ToolCalls = []llm.ToolCall{{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`}}

	saved, err := store.SaveMessages(ctx, []*storage.Message{first, second})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 2 {
		t.Fatalf("expected 2 saved messages, got %d", len(saved))
	}
	if saved[0].ID == "" || saved[1].ID == "" {
		t.Fatal("expected generated IDs for saved messages")
	}
	if saved[0].ResourceID != "user-1" {
		t.Errorf("expected inherited resource ID user-1, got %q", saved[0].ResourceID)
	}

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID:  th.ID,
		SortOrder: storage.SortAsc,
	})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Text() != "Hello" || msgs[1].Text() != "Hi" {
		t.Errorf("unexpected content or order: %q, %q", msgs[0].Text(), msgs[1].Text())
	}
	if msgs[1].Thinking != "greeting" {
		t.Errorf("thinking not persisted: %q", msgs[1].Thinking)
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "tc-1" {
		t.Errorf("tool calls not persisted: %+v", msgs[1].ToolCalls)
	}

	byIDs, err := store.GetMessagesByID(ctx, []string{saved[1].ID, saved[0].ID})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byIDs) != 2 || byIDs[0].ID != saved[1].ID || byIDs[1].ID != saved[0].ID {
		t.Errorf("GetMessagesByID must preserve requested order, got %+v", byIDs)
	}

	if err := store.DeleteMessages(ctx, []string{saved[0].ID}); err != nil {
		t.Fatalf("DeleteMessages failed: %v", err)
	}
	remaining, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages after delete failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != saved[1].ID {
		t.Errorf("expected only %s to remain, got %+v", saved[1].ID, remaining)
	}
	if byIDs, err := store.GetMessagesByID(ctx, []string{saved[0].ID}); err != nil || len(byIDs) != 0 {
		t.Errorf("expected deleted message to be gone, got %+v (err: %v)", byIDs, err)
	}
}

func testMessageListFilters(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Filters"})

	first := saveMessage(t, store, storage.NewMessage(th.ID, llm.RoleUser, "one"))
	time.Sleep(2 * time.Millisecond) // guarantee distinct CreatedAt values
	second := saveMessage(t, store, storage.NewMessage(th.ID, llm.RoleAssistant, "two"))
	time.Sleep(2 * time.Millisecond)
	third := saveMessage(t, store, storage.NewMessage(th.ID, llm.RoleUser, "three"))

	assistants, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID: th.ID,
		Role:     llm.RoleAssistant,
	})
	if err != nil {
		t.Fatalf("ListMessages by role failed: %v", err)
	}
	if len(assistants) != 1 || assistants[0].ID != second.ID {
		t.Errorf("expected only assistant message %s, got %+v", second.ID, assistants)
	}

	usersDesc, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID:  th.ID,
		Role:      llm.RoleUser,
		SortOrder: storage.SortDesc,
	})
	if err != nil {
		t.Fatalf("ListMessages descending failed: %v", err)
	}
	if len(usersDesc) != 2 || usersDesc[0].ID != third.ID || usersDesc[1].ID != first.ID {
		t.Errorf("unexpected descending user messages: %+v", usersDesc)
	}

	page, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID:  th.ID,
		SortOrder: storage.SortAsc,
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("ListMessages pagination failed: %v", err)
	}
	if len(page) != 1 || page[0].ID != second.ID {
		t.Errorf("expected page to contain %s, got %+v", second.ID, page)
	}

	future := time.Now().Add(time.Hour)
	if msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID, Since: &future}); err != nil || len(msgs) != 0 {
		t.Errorf("expected no messages after %v, got %+v (err: %v)", future, msgs, err)
	}
	past := time.Now().Add(-time.Hour)
	if msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID, Until: &past}); err != nil || len(msgs) != 0 {
		t.Errorf("expected no messages before %v, got %+v (err: %v)", past, msgs, err)
	}
}

func testMessageUpsert(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Upsert"})

	created := saveMessage(t, store, &storage.Message{
		ID:       "msg-upsert",
		ThreadID: th.ID,
		Role:     llm.RoleUser,
		Content:  []llm.Part{llm.TextPart("v1")},
	})

	updated := saveMessage(t, store, &storage.Message{
		ID:       "msg-upsert",
		ThreadID: th.ID,
		Role:     llm.RoleUser,
		Content:  []llm.Part{llm.TextPart("v2")},
	})
	if !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("expected CreatedAt %v to be preserved, got %v", created.CreatedAt, updated.CreatedAt)
	}

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text() != "v2" {
		t.Errorf("expected a single updated message, got %+v", msgs)
	}
}

func testMultipartMessage(t *testing.T, store storage.Storage) {
	ctx := context.Background()
	th := saveThread(t, store, &storage.Thread{Title: "Multimodal"})

	img := llm.ImageFromBytes([]byte{1, 2, 3}, "image/png")
	msg := storage.NewMessageWithParts(th.ID, llm.RoleUser, llm.TextPart("Look at this"), img)
	saveMessage(t, store, msg)

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: th.ID})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Content) != 2 {
		t.Fatalf("expected 1 message with 2 parts, got %+v", msgs)
	}
	if msgs[0].Content[0].Text != "Look at this" || msgs[0].Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected content: %+v", msgs[0].Content)
	}
}
