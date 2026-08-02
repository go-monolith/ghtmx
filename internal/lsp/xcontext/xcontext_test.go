package xcontext

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Detach exists so work started under a request context can outlive the
// request without inheriting its cancellation — an LSP handler that
// spawns a background refresh must not have that refresh killed the
// moment the editor cancels the originating request. These tests pin
// both halves of that contract: cancellation does not propagate, values
// do.

func TestDetachDropsCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	detached := Detach(parent)

	cancel()

	// The parent is done; the detached context must not be.
	<-parent.Done()
	if err := detached.Err(); err != nil {
		t.Errorf("Err() = %v after the parent was cancelled, want nil", err)
	}
	if done := detached.Done(); done != nil {
		t.Error("Done() returned a non-nil channel; a detached context can never be done")
	}
}

func TestDetachDropsDeadline(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()

	if _, ok := parent.Deadline(); !ok {
		t.Fatal("the parent should have a deadline; the test is not measuring what it thinks")
	}

	deadline, ok := Detach(parent).Deadline()
	if ok {
		t.Errorf("Deadline() reported ok = true, want false; got %v", deadline)
	}
	if !deadline.IsZero() {
		t.Errorf("Deadline() = %v, want the zero time", deadline)
	}
}

func TestDetachKeepsValues(t *testing.T) {
	type keyType string
	const key keyType = "request-id"

	parent, cancel := context.WithCancel(context.WithValue(context.Background(), key, "abc123"))
	detached := Detach(parent)

	// Values survive both before and after the parent is cancelled:
	// that is the whole point of detaching rather than starting fresh.
	if got := detached.Value(key); got != "abc123" {
		t.Errorf("Value(%q) = %v, want %q", key, got, "abc123")
	}
	cancel()
	if got := detached.Value(key); got != "abc123" {
		t.Errorf("Value(%q) = %v after cancellation, want %q", key, got, "abc123")
	}
	if got := detached.Value(keyType("absent")); got != nil {
		t.Errorf("Value of an absent key = %v, want nil", got)
	}
}

// TestDetachedContextIsUsableWithSelect pins that the nil Done channel
// behaves the way callers rely on: a select on it blocks forever rather
// than firing immediately, so a worker holding a detached context is
// never spuriously torn down.
func TestDetachedContextIsUsableWithSelect(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	detached := Detach(parent)

	select {
	case <-detached.Done():
		t.Error("a receive on the detached Done channel succeeded; it must block forever")
	case <-time.After(10 * time.Millisecond):
	}

	if errors.Is(detached.Err(), context.Canceled) {
		t.Error("Err() reported context.Canceled; cancellation must not propagate")
	}
}
