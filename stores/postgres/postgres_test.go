package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/ijaidev/gojent/core/llm"
	"github.com/ijaidev/gojent/core/session"
	"github.com/ijaidev/gojent/stores/postgres"
)

func newTestStore(t *testing.T, opts ...postgres.Option) (*postgres.Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	opts = append([]postgres.Option{postgres.WithAutoMigrate(false)}, opts...)
	store, err := postgres.NewWithDB(db, opts...)
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, mock
}

func encoded(t *testing.T, messages ...llm.Message) [][]byte {
	t.Helper()
	out := make([][]byte, len(messages))
	for i := range messages {
		data, err := json.Marshal(messages[i])
		if err != nil {
			t.Fatalf("marshal message %d: %v", i, err)
		}
		out[i] = data
	}
	return out
}

func TestStore_LoadPreservesOrder(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}
	rows := encoded(t, llm.SystemMessage("be brief"), llm.UserMessage("hello"), llm.AssistantMessage("hi"))

	mock.ExpectQuery("SELECT message").
		WithArgs("tenant", "thread").
		WillReturnRows(sqlmock.NewRows([]string{"message"}).
			AddRow(rows[0]).
			AddRow(rows[1]).
			AddRow(rows[2]))

	messages, err := store.Load(context.Background(), sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	if messages[0].Role != llm.RoleSystem || messages[0].Text() != "be brief" {
		t.Errorf("first message = %+v", messages[0])
	}
	if messages[1].Text() != "hello" || messages[2].Text() != "hi" {
		t.Errorf("order was not preserved: %+v", messages)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_AppendIsAtomicTransaction(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}
	messages := []llm.Message{llm.UserMessage("hello"), llm.AssistantMessage("hi")}
	payloads := encoded(t, messages...)

	mock.ExpectBegin()
	prepared := mock.ExpectPrepare("INSERT INTO session_messages")
	for _, payload := range payloads {
		prepared.ExpectExec().
			WithArgs("tenant", "thread", payload).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}
	mock.ExpectCommit()

	if err := store.Append(context.Background(), sess, messages...); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_AppendRollsBackOnFailure(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}
	messages := []llm.Message{llm.UserMessage("hello"), llm.AssistantMessage("hi")}
	payloads := encoded(t, messages...)

	mock.ExpectBegin()
	prepared := mock.ExpectPrepare("INSERT INTO session_messages")
	prepared.ExpectExec().
		WithArgs("tenant", "thread", payloads[0]).
		WillReturnResult(sqlmock.NewResult(1, 1))
	prepared.ExpectExec().
		WithArgs("tenant", "thread", payloads[1]).
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	err := store.Append(context.Background(), sess, messages...)
	if err == nil {
		t.Fatal("expected an append error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_AppendEncodingFailureDoesNotTouchDatabase(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}

	invalid := llm.Message{Role: llm.RoleTool, Data: json.RawMessage(`{`)}
	err := store.Append(context.Background(), sess, llm.UserMessage("valid"), invalid)
	if err == nil {
		t.Fatal("expected an encoding error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected database interaction: %v", err)
	}
}

func TestStore_LoadDecodesReplayState(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "replay"}

	original := llm.Message{
		Role:         llm.RoleAssistant,
		Content:      []llm.Part{llm.TextPart("")},
		Thinking:     "reasoning",
		ToolCalls:    []llm.ToolCall{{Index: 0, ID: "call-1", Name: "lookup", Arguments: `{"q":"go"}`}},
		ProviderData: &llm.ProviderData{Provider: "test", Version: 2, Data: []byte(`{"signature":"abc"}`)},
	}
	row := encoded(t, original)[0]
	mock.ExpectQuery("SELECT message").
		WithArgs("", "replay").
		WillReturnRows(sqlmock.NewRows([]string{"message"}).AddRow(row))

	messages, err := store.Load(context.Background(), sess)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	assistant := messages[0]
	if len(assistant.Content) != 0 {
		t.Errorf("empty text part survived persistence: %+v", assistant.Content)
	}
	if assistant.Thinking != "reasoning" || len(assistant.ToolCalls) != 1 {
		t.Errorf("message = %+v", assistant)
	}
	if assistant.ProviderData == nil || assistant.ProviderData.Provider != "test" {
		t.Fatalf("ProviderData = %+v", assistant.ProviderData)
	}
	if string(assistant.ProviderData.Data) != `{"signature":"abc"}` {
		t.Errorf("ProviderData.Data = %s", assistant.ProviderData.Data)
	}
}

func TestStore_LoadDecodeError(t *testing.T) {
	store, mock := newTestStore(t)
	mock.ExpectQuery("SELECT message").
		WillReturnRows(sqlmock.NewRows([]string{"message"}).AddRow([]byte("not-json")))

	if _, err := store.Load(context.Background(), session.Session{ID: "s"}); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestStore_Delete(t *testing.T) {
	store, mock := newTestStore(t)
	sess := session.Session{ID: "thread", ResourceID: "tenant"}
	mock.ExpectExec("DELETE FROM session_messages").
		WithArgs("tenant", "thread").
		WillReturnResult(sqlmock.NewResult(0, 3))

	if err := store.Delete(context.Background(), sess); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_Migrate(t *testing.T) {
	store, mock := newTestStore(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS session_messages").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestStore_Validation(t *testing.T) {
	store, mock := newTestStore(t)
	ctx := context.Background()

	if _, err := store.Load(ctx, session.Session{}); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Load without ID = %v", err)
	}
	if err := store.Append(ctx, session.Session{}, llm.UserMessage("x")); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Append without ID = %v", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrMessagesRequired) {
		t.Errorf("Append without messages = %v", err)
	}
	if err := store.Delete(ctx, session.Session{}); !errors.Is(err, session.ErrIDRequired) {
		t.Errorf("Delete without ID = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected database interaction: %v", err)
	}
}

func TestStore_Close(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	store, err := postgres.NewWithDB(db, postgres.WithAutoMigrate(false))
	if err != nil {
		t.Fatalf("NewWithDB: %v", err)
	}
	mock.ExpectClose()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Closing twice is safe and does not touch the database again.
	if err := store.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	ctx := context.Background()
	if _, err := store.Load(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Load after Close = %v, want ErrClosed", err)
	}
	if err := store.Append(ctx, session.Session{ID: "s"}, llm.UserMessage("x")); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Append after Close = %v, want ErrClosed", err)
	}
	if err := store.Delete(ctx, session.Session{ID: "s"}); !errors.Is(err, session.ErrClosed) {
		t.Errorf("Delete after Close = %v, want ErrClosed", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestNewWithDB_RejectsNil(t *testing.T) {
	if _, err := postgres.NewWithDB(nil); err == nil {
		t.Fatal("expected an error for a nil database")
	}
}
