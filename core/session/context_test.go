package session_test

import (
	"context"
	"testing"

	"github.com/ijaidev/gojent/core/session"
)

func TestFromContext_Missing(t *testing.T) {
	if _, ok := session.FromContext(context.Background()); ok {
		t.Fatal("FromContext reported a session on an empty context")
	}
}

func TestFromContext_RoundTrip(t *testing.T) {
	want := session.Session{ID: "conversation", ResourceID: "tenant"}
	ctx := session.NewContext(context.Background(), want)

	got, ok := session.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext did not find the session")
	}
	if got != want {
		t.Fatalf("session = %+v, want %+v", got, want)
	}
}

func TestFromContext_InnerValueWins(t *testing.T) {
	outer := session.Session{ID: "outer"}
	inner := session.Session{ID: "inner", ResourceID: "tenant"}
	ctx := session.NewContext(session.NewContext(context.Background(), outer), inner)

	got, ok := session.FromContext(ctx)
	if !ok || got != inner {
		t.Fatalf("session = %+v, ok = %v, want %+v", got, ok, inner)
	}
}

func TestFromContext_ZeroSessionIsStillPresent(t *testing.T) {
	ctx := session.NewContext(context.Background(), session.Session{})

	got, ok := session.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext did not find the zero-value session")
	}
	if got != (session.Session{}) {
		t.Fatalf("session = %+v, want zero value", got)
	}
}
