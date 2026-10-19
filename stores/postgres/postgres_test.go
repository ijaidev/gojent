package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/storage"
	"github.com/ijaidev/gojent/stores/postgres"
)

func newMockStore(t *testing.T) (*postgres.Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	store, err := postgres.NewWithDB(db, postgres.WithAutoMigrate(false))
	if err != nil {
		t.Fatalf("failed to initialize store with mock db: %v", err)
	}
	return store, mock
}

func TestStore_ThreadLifecycle(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()

	// 1. SaveThread (new thread with generated ID)
	mock.ExpectExec("INSERT INTO threads").
		WithArgs(sqlmock.AnyArg(), "Test Thread", "res-1", sqlmock.AnyArg(), sqlmock.AnyArg(), `{"env":"test"}`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	th, err := store.SaveThread(ctx, &storage.Thread{
		Title:      "Test Thread",
		ResourceID: "res-1",
		Metadata:   map[string]any{"env": "test"},
	})
	if err != nil {
		t.Fatalf("SaveThread failed: %v", err)
	}
	if th.ID == "" {
		t.Fatalf("expected non-empty thread ID")
	}
	if th.CreatedAt.IsZero() || th.UpdatedAt.IsZero() {
		t.Fatalf("expected non-zero timestamps")
	}

	// 2. SaveThread (existing thread preserving created_at)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT created_at FROM threads WHERE id = $1")).
		WithArgs("th-existing").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectExec("INSERT INTO threads").
		WithArgs("th-existing", "Existing Thread", "res-1", now, sqlmock.AnyArg(), `{}`).
		WillReturnResult(sqlmock.NewResult(0, 1))

	thExisting, err := store.SaveThread(ctx, &storage.Thread{
		ID:         "th-existing",
		Title:      "Existing Thread",
		ResourceID: "res-1",
	})
	if err != nil {
		t.Fatalf("SaveThread existing failed: %v", err)
	}
	if !thExisting.CreatedAt.Equal(now) {
		t.Errorf("expected preserved created_at %v, got %v", now, thExisting.CreatedAt)
	}

	// 3. GetThreadByID (success)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1")).
		WithArgs("th-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "resource_id", "created_at", "updated_at", "metadata"}).
			AddRow("th-1", "Fetched Thread", "res-1", now, now, `{"env":"test"}`))

	fetched, err := store.GetThreadByID(ctx, "th-1")
	if err != nil {
		t.Fatalf("GetThreadByID failed: %v", err)
	}
	if fetched.ID != "th-1" || fetched.Title != "Fetched Thread" || fetched.Metadata["env"] != "test" {
		t.Errorf("unexpected thread payload: %+v", fetched)
	}

	// 4. GetThreadByID (not found)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1")).
		WithArgs("th-missing").
		WillReturnError(sql.ErrNoRows)

	_, err = store.GetThreadByID(ctx, "th-missing")
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound, got: %v", err)
	}

	// 5. GetThreadByID (empty ID)
	_, err = store.GetThreadByID(ctx, "")
	if !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired, got: %v", err)
	}

	// 6. UpdateThread (success)
	newTitle := "Updated Title"
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1")).
		WithArgs("th-1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "resource_id", "created_at", "updated_at", "metadata"}).
			AddRow("Old Title", "res-1", now, now, `{"env":"test"}`))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE threads SET title = $1, resource_id = $2, updated_at = $3, metadata = $4::jsonb WHERE id = $5")).
		WithArgs("Updated Title", "res-1", sqlmock.AnyArg(), `{"env":"test","status":"done"}`, "th-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	updated, err := store.UpdateThread(ctx, "th-1", storage.UpdateThreadInput{
		Title: &newTitle,
		Metadata: map[string]any{
			"status": "done",
		},
	})
	if err != nil {
		t.Fatalf("UpdateThread failed: %v", err)
	}
	if updated.Title != newTitle || updated.Metadata["status"] != "done" || updated.Metadata["env"] != "test" {
		t.Errorf("unexpected updated thread: %+v", updated)
	}

	// 7. UpdateThread (not found)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT title, resource_id, created_at, updated_at, metadata FROM threads WHERE id = $1")).
		WithArgs("th-missing").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	_, err = store.UpdateThread(ctx, "th-missing", storage.UpdateThreadInput{Title: &newTitle})
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound, got: %v", err)
	}

	// 8. DeleteThread (success with cascading deletes)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM threads WHERE id = $1")).
		WithArgs("th-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM messages WHERE thread_id = $1")).
		WithArgs("th-1").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM thread_state WHERE thread_id = $1")).
		WithArgs("th-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := store.DeleteThread(ctx, "th-1"); err != nil {
		t.Fatalf("DeleteThread failed: %v", err)
	}

	// 9. DeleteThread (not found)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM threads WHERE id = $1")).
		WithArgs("th-missing").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = store.DeleteThread(ctx, "th-missing")
	if !errors.Is(err, storage.ErrThreadNotFound) {
		t.Errorf("expected ErrThreadNotFound, got: %v", err)
	}

	// 10. DeleteThread (empty ID)
	err = store.DeleteThread(ctx, "")
	if !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_ListThreads(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()

	// 1. ListThreads without filter
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads ORDER BY created_at ASC, id ASC")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "resource_id", "created_at", "updated_at", "metadata"}).
			AddRow("th-1", "Thread 1", "res-1", now, now, `{}`).
			AddRow("th-2", "Thread 2", "res-2", now, now, `{"k":"v"}`))

	threads, err := store.ListThreads(ctx, storage.ThreadFilter{})
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}

	// 2. ListThreads with resource_id, metadata, limit, offset, and sort order
	expectedQuery := `SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads WHERE resource_id = $1 AND (metadata != '{}'::jsonb AND (metadata->$2 IS NULL OR metadata->$2 = 'null'::jsonb)) AND metadata->$3 = $4::jsonb ORDER BY updated_at DESC, id DESC LIMIT $5 OFFSET $6`
	mock.ExpectQuery(regexp.QuoteMeta(expectedQuery)).
		WithArgs("res-1", "deleted_at", "env", `"prod"`, 10, 5).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "resource_id", "created_at", "updated_at", "metadata"}).
			AddRow("th-1", "Thread 1", "res-1", now, now, `{"env":"prod"}`))

	filtered, err := store.ListThreads(ctx, storage.ThreadFilter{
		ResourceID: "res-1",
		Metadata: map[string]any{
			"env":        "prod",
			"deleted_at": nil,
		},
		SortBy:    storage.ThreadSortUpdatedAt,
		SortOrder: storage.SortDesc,
		Limit:     10,
		Offset:    5,
	})
	if err != nil {
		t.Fatalf("ListThreads with filter failed: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != "th-1" {
		t.Errorf("unexpected filtered threads: %+v", filtered)
	}

	// 3. ListThreads with only offset
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, title, resource_id, created_at, updated_at, metadata FROM threads ORDER BY created_at ASC, id ASC OFFSET $1")).
		WithArgs(20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "resource_id", "created_at", "updated_at", "metadata"}))

	_, err = store.ListThreads(ctx, storage.ThreadFilter{Offset: 20})
	if err != nil {
		t.Fatalf("ListThreads with only offset failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_MessageOperations(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	now := time.Now().UTC()

	// 1. SaveMessages (empty slice)
	res, err := store.SaveMessages(ctx, []*storage.Message{})
	if err != nil {
		t.Fatalf("SaveMessages empty failed: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected empty slice, got %d", len(res))
	}

	// 2. SaveMessages (validation errors)
	_, err = store.SaveMessages(ctx, []*storage.Message{nil})
	if !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput for nil message, got: %v", err)
	}

	_, err = store.SaveMessages(ctx, []*storage.Message{{Content: []llm.Part{llm.TextPart("hello")}}})
	if !errors.Is(err, storage.ErrThreadRequired) {
		t.Errorf("expected ErrThreadRequired for empty threadID, got: %v", err)
	}

	// 3. SaveMessages (success with parent resource_id lookup and thread updated_at touch)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_id FROM threads WHERE id = $1")).
		WithArgs("th-1").
		WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow("res-parent"))
	mock.ExpectExec("INSERT INTO messages").
		WithArgs(
			sqlmock.AnyArg(),
			"th-1",
			"res-parent",
			string(llm.RoleUser),
			`[{"type":"text","text":"Hello agent"}]`,
			"",
			"[]",
			"",
			sqlmock.AnyArg(),
			`{"source":"web"}`,
			"null",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE threads SET updated_at = $1 WHERE id = $2")).
		WithArgs(sqlmock.AnyArg(), "th-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	savedMsgs, err := store.SaveMessages(ctx, []*storage.Message{
		{
			ThreadID: "th-1",
			Role:     llm.RoleUser,
			Content:  []llm.Part{llm.TextPart("Hello agent")},
			Metadata: map[string]any{"source": "web"},
		},
	})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(savedMsgs) != 1 || savedMsgs[0].ID == "" || savedMsgs[0].ResourceID != "res-parent" {
		t.Errorf("unexpected saved message: %+v", savedMsgs[0])
	}

	// 4. SaveMessages (preserves existing created_at on update)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT created_at FROM messages WHERE id = $1")).
		WithArgs("msg-1").
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(now))
	mock.ExpectExec("INSERT INTO messages").
		WithArgs(
			"msg-1",
			"th-1",
			"res-explicit",
			string(llm.RoleAssistant),
			`[{"type":"text","text":"Response"}]`,
			"",
			"[]",
			"",
			now,
			"{}",
			"null",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE threads SET updated_at = $1 WHERE id = $2")).
		WithArgs(now, "th-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	updatedMsgs, err := store.SaveMessages(ctx, []*storage.Message{
		{
			ID:         "msg-1",
			ThreadID:   "th-1",
			ResourceID: "res-explicit",
			Role:       llm.RoleAssistant,
			Content:    []llm.Part{llm.TextPart("Response")},
		},
	})
	if err != nil {
		t.Fatalf("SaveMessages update failed: %v", err)
	}
	if !updatedMsgs[0].CreatedAt.Equal(now) {
		t.Errorf("expected preserved created_at %v, got %v", now, updatedMsgs[0].CreatedAt)
	}

	// 5. ListMessages with all filters
	since := now.Add(-1 * time.Hour)
	until := now.Add(1 * time.Hour)
	expectedListMsgQuery := `SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE thread_id = $1 AND resource_id = $2 AND role = $3 AND created_at >= $4 AND created_at <= $5 ORDER BY created_at DESC, id DESC LIMIT $6 OFFSET $7`

	mock.ExpectQuery(regexp.QuoteMeta(expectedListMsgQuery)).
		WithArgs("th-1", "res-1", string(llm.RoleTool), since, until, 50, 10).
		WillReturnRows(sqlmock.NewRows([]string{"id", "thread_id", "resource_id", "role", "content", "name", "tool_calls", "tool_call_id", "created_at", "metadata", "raw"}).
			AddRow("msg-tool", "th-1", "res-1", "tool", "result", "calc", `[{"id":"tc-1","type":"function","function":{"name":"calc","arguments":"{}"}}]`, "tc-1", now, `{"meta":"ok"}`, `{"thought":"done"}`))

	listed, err := store.ListMessages(ctx, storage.MessageFilter{
		ThreadID:   "th-1",
		ResourceID: "res-1",
		Role:       llm.RoleTool,
		Since:      &since,
		Until:      &until,
		Limit:      50,
		Offset:     10,
		SortOrder:  storage.SortDesc,
	})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(listed) != 1 || listed[0].Role != llm.RoleTool || len(listed[0].ToolCalls) != 1 || listed[0].Raw == nil {
		t.Errorf("unexpected listed message: %+v", listed[0])
	}

	// 6. GetMessagesByID (empty slice)
	byIDEmpty, err := store.GetMessagesByID(ctx, []string{})
	if err != nil {
		t.Fatalf("GetMessagesByID empty failed: %v", err)
	}
	if len(byIDEmpty) != 0 {
		t.Errorf("expected 0 messages, got %d", len(byIDEmpty))
	}

	// 7. GetMessagesByID (preserves ID order)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN ($1, $2)")).
		WithArgs("msg-2", "msg-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "thread_id", "resource_id", "role", "content", "name", "tool_calls", "tool_call_id", "created_at", "metadata", "raw"}).
			AddRow("msg-1", "th-1", "res-1", "user", "one", "", "[]", "", now, "{}", "null").
			AddRow("msg-2", "th-1", "res-1", "assistant", "two", "", "[]", "", now, "{}", "null"))

	byIDs, err := store.GetMessagesByID(ctx, []string{"msg-2", "msg-1"})
	if err != nil {
		t.Fatalf("GetMessagesByID failed: %v", err)
	}
	if len(byIDs) != 2 || byIDs[0].ID != "msg-2" || byIDs[1].ID != "msg-1" {
		t.Errorf("expected order [msg-2, msg-1], got [%s, %s]", byIDs[0].ID, byIDs[1].ID)
	}

	// 8. DeleteMessages (empty slice)
	if err := store.DeleteMessages(ctx, []string{}); err != nil {
		t.Fatalf("DeleteMessages empty failed: %v", err)
	}

	// 9. DeleteMessages (chunked in transaction)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM messages WHERE id IN ($1, $2)")).
		WithArgs("msg-1", "msg-2").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := store.DeleteMessages(ctx, []string{"msg-1", "msg-2"}); err != nil {
		t.Fatalf("DeleteMessages failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_ThreadState(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	// 1. GetThreadState (validation)
	_, err := store.GetThreadState(ctx, "", "task")
	if !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got: %v", err)
	}

	// 2. GetThreadState (success)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM thread_state WHERE thread_id = $1 AND type = $2")).
		WithArgs("th-1", "task").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`{"status":"in_progress","steps":[1,2]}`))

	val, err := store.GetThreadState(ctx, "th-1", "task")
	if err != nil {
		t.Fatalf("GetThreadState failed: %v", err)
	}
	valMap, ok := val.(map[string]any)
	if !ok || valMap["status"] != "in_progress" {
		t.Errorf("unexpected state value: %v", val)
	}

	// 3. GetThreadState (not found)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM thread_state WHERE thread_id = $1 AND type = $2")).
		WithArgs("th-1", "goal").
		WillReturnError(sql.ErrNoRows)

	_, err = store.GetThreadState(ctx, "th-1", "goal")
	if !errors.Is(err, storage.ErrStateNotFound) {
		t.Errorf("expected ErrStateNotFound, got: %v", err)
	}

	// 4. GetThreadState (null value)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM thread_state WHERE thread_id = $1 AND type = $2")).
		WithArgs("th-1", "empty").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("null"))

	val, err = store.GetThreadState(ctx, "th-1", "empty")
	if err != nil || val != nil {
		t.Errorf("expected nil value, got %v (err: %v)", val, err)
	}

	// 5. SetThreadState (success)
	mock.ExpectExec("INSERT INTO thread_state").
		WithArgs("th-1", "task", `{"status":"completed"}`, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.SetThreadState(ctx, "th-1", "task", map[string]any{"status": "completed"})
	if err != nil {
		t.Fatalf("SetThreadState failed: %v", err)
	}

	// 6. DeleteThreadState (success)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM thread_state WHERE thread_id = $1 AND type = $2")).
		WithArgs("th-1", "task").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.DeleteThreadState(ctx, "th-1", "task")
	if err != nil {
		t.Fatalf("DeleteThreadState failed: %v", err)
	}

	// 7. ListThreadStates (success)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT type, value FROM thread_state WHERE thread_id = $1")).
		WithArgs("th-1").
		WillReturnRows(sqlmock.NewRows([]string{"type", "value"}).
			AddRow("task", `{"id":1}`).
			AddRow("note", `"remember this"`))

	states, err := store.ListThreadStates(ctx, "th-1")
	if err != nil {
		t.Fatalf("ListThreadStates failed: %v", err)
	}
	if len(states) != 2 || states["note"] != "remember this" {
		t.Errorf("unexpected thread states: %+v", states)
	}

	// 8. ListThreadStates (empty threadID)
	_, err = store.ListThreadStates(ctx, "")
	if !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_ClearAndDangerouslyClearAll(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	// 1. Clear
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM thread_state")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM messages")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM threads")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// 2. DangerouslyClearAll
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM thread_state")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM messages")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM threads")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	store.DangerouslyClearAll()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_Migrate(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	mock.ExpectExec(regexp.QuoteMeta(postgres.Schema)).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_ClosedAndInvalidInput(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)

	// Close store
	mock.ExpectClose()
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Calling Close() again should be a no-op
	if err := store.Close(); err != nil {
		t.Fatalf("Second Close() failed: %v", err)
	}

	// All operations should return ErrClosed
	if _, err := store.SaveThread(ctx, &storage.Thread{Title: "t"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, "th-1"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.UpdateThread(ctx, "th-1", storage.UpdateThreadInput{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.DeleteThread(ctx, "th-1"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.ListThreads(ctx, storage.ThreadFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{{ThreadID: "th-1"}}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.GetMessagesByID(ctx, []string{"msg-1"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.DeleteMessages(ctx, []string{"msg-1"}); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.GetThreadState(ctx, "th-1", "task"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.SetThreadState(ctx, "th-1", "task", "val"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.DeleteThreadState(ctx, "th-1", "task"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if _, err := store.ListThreadStates(ctx, "th-1"); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.Clear(ctx); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
	if err := store.Migrate(ctx); !errors.Is(err, storage.ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}

	// Test New validation
	if _, err := postgres.New(""); !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput on empty dsn, got %v", err)
	}
	if _, err := postgres.NewWithDB(nil); !errors.Is(err, storage.ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput on nil db, got %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	store, _ := newMockStore(t)
	defer func() { _ = store.Close() }()

	if _, err := store.SaveThread(ctx, &storage.Thread{Title: "t"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.GetThreadByID(ctx, "th-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.UpdateThread(ctx, "th-1", storage.UpdateThreadInput{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.DeleteThread(ctx, "th-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.ListThreads(ctx, storage.ThreadFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.SaveMessages(ctx, []*storage.Message{{ThreadID: "th-1"}}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.ListMessages(ctx, storage.MessageFilter{}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.GetMessagesByID(ctx, []string{"msg-1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.DeleteMessages(ctx, []string{"msg-1"}); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.GetThreadState(ctx, "th-1", "task"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.SetThreadState(ctx, "th-1", "task", "val"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.DeleteThreadState(ctx, "th-1", "task"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, err := store.ListThreadStates(ctx, "th-1"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.Clear(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if err := store.Migrate(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestStore_OptionsAndDB(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	store, err := postgres.NewWithDB(
		db,
		postgres.WithDriverName("postgres"),
		postgres.WithMaxOpenConns(10),
		postgres.WithMaxIdleConns(5),
		postgres.WithConnMaxLifetime(10*time.Minute),
		postgres.WithConnMaxIdleTime(5*time.Minute),
		postgres.WithAutoMigrate(false),
	)
	if err != nil {
		t.Fatalf("NewWithDB with options failed: %v", err)
	}
	if store.DB() != db {
		t.Errorf("expected store.DB() to match provided db")
	}
}

func TestStore_Concurrency(t *testing.T) {
	store, _ := newMockStore(t)
	defer func() { _ = store.Close() }()

	var wg sync.WaitGroup
	ctx := context.Background()

	// Launch concurrent readers checking closed status and invalid input paths
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.GetThreadByID(ctx, "")
			_, _ = store.GetThreadState(ctx, "", "")
			_ = store.DeleteMessages(ctx, []string{})
			_, _ = store.SaveMessages(ctx, []*storage.Message{})
		}()
	}

	wg.Wait()
}

func TestStore_BatchChunking(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	count := 600
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		ids[i] = fmt.Sprintf("msg-%04d", i)
	}

	// GetMessagesByID chunk 1 (500) and chunk 2 (100)
	mock.ExpectQuery(`SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN \(\$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "thread_id", "resource_id", "role", "content", "name", "tool_calls", "tool_call_id", "created_at", "metadata", "raw"}))
	mock.ExpectQuery(`SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE id IN \(\$1`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "thread_id", "resource_id", "role", "content", "name", "tool_calls", "tool_call_id", "created_at", "metadata", "raw"}))

	res, err := store.GetMessagesByID(ctx, ids)
	if err != nil {
		t.Fatalf("GetMessagesByID chunked failed: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 messages returned from empty mock rows, got %d", len(res))
	}

	// DeleteMessages chunk 1 (500) and chunk 2 (100)
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM messages WHERE id IN \(\$1`).WillReturnResult(sqlmock.NewResult(0, 500))
	mock.ExpectExec(`DELETE FROM messages WHERE id IN \(\$1`).WillReturnResult(sqlmock.NewResult(0, 100))
	mock.ExpectCommit()

	if err := store.DeleteMessages(ctx, ids); err != nil {
		t.Fatalf("DeleteMessages chunked failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}

func TestStore_MultipartMessage_Postgres(t *testing.T) {
	ctx := context.Background()
	store, mock := newMockStore(t)
	defer func() { _ = store.Close() }()

	imgBytes := []byte{10, 20, 30}
	imgPart := llm.ImageFromBytes(imgBytes, "image/png")
	msg := storage.NewMessageWithParts("th-multi", llm.RoleUser, llm.TextPart("Inspect this photo"), imgPart)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource_id FROM threads WHERE id = $1")).
		WithArgs("th-multi").
		WillReturnRows(sqlmock.NewRows([]string{"resource_id"}).AddRow("res-multi"))
	mock.ExpectExec("INSERT INTO messages").
		WithArgs(
			sqlmock.AnyArg(),
			"th-multi",
			"res-multi",
			string(llm.RoleUser),
			sqlmock.AnyArg(),
			"",
			"[]",
			"",
			sqlmock.AnyArg(),
			"{}",
			"null",
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE threads SET updated_at = $1 WHERE id = $2")).
		WithArgs(sqlmock.AnyArg(), "th-multi").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	saved, err := store.SaveMessages(ctx, []*storage.Message{msg})
	if err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}
	if len(saved) != 1 || len(saved[0].Content) != 2 {
		t.Fatalf("unexpected saved message: %+v", saved)
	}

	jsonbContent := `[{"type":"text","text":"Inspect this photo"},{"type":"image","mime_type":"image/png","data":"ChQe"}]`
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, thread_id, resource_id, role, content, name, tool_calls, tool_call_id, created_at, metadata, raw FROM messages WHERE thread_id = $1 ORDER BY created_at ASC, id ASC")).
		WithArgs("th-multi").
		WillReturnRows(sqlmock.NewRows([]string{"id", "thread_id", "resource_id", "role", "content", "name", "tool_calls", "tool_call_id", "created_at", "metadata", "raw"}).
			AddRow("msg-multi", "th-multi", "res-multi", "user", []byte(jsonbContent), "", "[]", "", time.Now(), "{}", "null"))

	msgs, err := store.ListMessages(ctx, storage.MessageFilter{ThreadID: "th-multi"})
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 1 || len(msgs[0].Content) != 2 {
		t.Fatalf("expected 1 message with 2 parts, got %d", len(msgs))
	}
	if msgs[0].Content[0].Text != "Inspect this photo" || msgs[0].Content[1].Type != llm.PartTypeImage {
		t.Errorf("unexpected content parts in retrieved message: %+v", msgs[0].Content)
	}
	if string(msgs[0].Content[1].Data) != string(imgBytes) {
		t.Errorf("expected decoded image bytes %v, got %v", imgBytes, msgs[0].Content[1].Data)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled mock expectations: %v", err)
	}
}
