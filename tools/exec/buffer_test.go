package exec

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedBuffer_ReadAndSnapshot(t *testing.T) {
	b := newBoundedBuffer(2000)
	data := bytes.Repeat([]byte("a"), 5000)
	if _, err := b.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := b.Total(); got != 5000 {
		t.Fatalf("total = %d, want 5000", got)
	}

	first := b.Read(0, 100)
	if len(first.data) != 100 || first.next != 100 || first.reset {
		t.Fatalf("first read = %+v", first)
	}
	if !bytes.Equal(first.data, bytes.Repeat([]byte("a"), 100)) {
		t.Fatal("first read returned unexpected bytes")
	}

	skipped := b.Read(1000, 2000)
	if !skipped.reset {
		t.Fatal("expected cursor reset for a dropped region")
	}
	if skipped.omitted != 2500 {
		t.Fatalf("omitted = %d, want 2500", skipped.omitted)
	}
	if skipped.next != 5000 {
		t.Fatalf("next = %d, want 5000", skipped.next)
	}
	if len(skipped.data) != 1500 {
		t.Fatalf("data length = %d, want 1500", len(skipped.data))
	}

	snap := b.Snapshot(400)
	if !snap.truncated {
		t.Fatal("expected truncated snapshot")
	}
	if snap.omitted != 4600 {
		t.Fatalf("snapshot omitted = %d, want 4600", snap.omitted)
	}
	if !strings.Contains(snap.String(), "omitted") {
		t.Fatalf("snapshot %q does not contain an omission marker", snap.String())
	}
}

func TestBoundedBuffer_TailLines(t *testing.T) {
	b := newBoundedBuffer(128)
	if _, err := b.Write([]byte("a\nb\nc\nd\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap := b.TailLines(2, 64)
	got := snap.String()
	if !strings.Contains(got, "c") || !strings.Contains(got, "d") {
		t.Fatalf("tail = %q, want the last two lines", got)
	}
	if strings.Contains(got, "a") || strings.Contains(got, "b") {
		t.Fatalf("tail = %q, contains earlier lines", got)
	}
}

func TestBoundedBuffer_WaitAfterClose(t *testing.T) {
	b := newBoundedBuffer(64)
	b.Close()
	done := make(chan struct{})
	go func() {
		if !b.WaitForData(done, 0, 0) {
			t.Error("Wait returned false for a closed buffer")
		}
		close(done)
	}()
	<-done
}

func TestBoundedBuffer_UTF8Boundary(t *testing.T) {
	b := newBoundedBuffer(32)
	if _, err := b.Write([]byte("hello \u2603")); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap := b.TailLines(1, 7)
	if !strings.HasSuffix(snap.String(), "\u2603") {
		t.Fatalf("tail = %q, expected complete rune", snap.String())
	}
}
