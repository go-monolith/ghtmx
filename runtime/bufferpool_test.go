package runtime

import (
	"bytes"
	"errors"
	"testing"
)

func TestBufferPool(t *testing.T) {
	t.Run("can get a buffer from the pool", func(t *testing.T) {
		w, existing := GetBuffer(new(bytes.Buffer))
		if w == nil {
			t.Error("expected a buffer, got nil")
		}
		if existing {
			t.Error("expected a new buffer, got an existing buffer")
		}
		err := ReleaseBuffer(w)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("can get an existing buffer from the pool", func(t *testing.T) {
		w, existing := GetBuffer(new(bytes.Buffer))
		if w == nil {
			t.Error("expected a buffer, got nil")
		}
		if existing {
			t.Error("expected a new buffer, got an existing buffer")
		}

		w, existing = GetBuffer(w)
		if w == nil {
			t.Error("expected a buffer, got nil")
		}
		if !existing {
			t.Error("expected an existing buffer, got a new buffer")
		}

		err := ReleaseBuffer(w)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("can release any writer without error", func(t *testing.T) {
		err := ReleaseBuffer(new(bytes.Buffer))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("attempting to buffer a nil writer returns nil", func(t *testing.T) {
		w, existing := GetBuffer(nil)
		if w != nil {
			t.Error("expected nil, got a buffer")
		}
		if existing {
			t.Error("expected nil, got an existing buffer")
		}
	})
}

// AcquireBuffer is the buffer prologue every generated render function
// runs, so its contract is the one that has to hold: write through to
// the underlying writer, release exactly once at the outermost
// component, and report a flush failure without ever masking a render
// error.
func TestAcquireBuffer(t *testing.T) {
	t.Run("writes through to the underlying writer on release", func(t *testing.T) {
		var sink bytes.Buffer
		func() (err error) {
			buf, release := AcquireBuffer(&sink)
			defer release(&err)
			if _, err := buf.WriteString("hello"); err != nil {
				t.Fatal(err)
			}
			// Still buffered: the flush belongs to release.
			if sink.Len() != 0 {
				t.Errorf("wrote through before release: %q", sink.String())
			}
			return nil
		}()
		if got := sink.String(); got != "hello" {
			t.Errorf("underlying writer = %q, want hello", got)
		}
	})

	t.Run("an inner component does not release the outer buffer", func(t *testing.T) {
		var sink bytes.Buffer
		outer, releaseOuter := AcquireBuffer(&sink)
		inner, releaseInner := AcquireBuffer(outer)
		if inner != outer {
			t.Error("an inner component must render into the same buffer")
		}
		if _, err := inner.WriteString("inner"); err != nil {
			t.Fatal(err)
		}
		var innerErr error
		releaseInner(&innerErr)
		if innerErr != nil {
			t.Errorf("inner release = %v, want nil", innerErr)
		}
		// The outer component still owns the flush: releasing the inner
		// one must not have written through.
		if sink.Len() != 0 {
			t.Errorf("the inner release flushed the outer buffer: %q", sink.String())
		}
		var outerErr error
		releaseOuter(&outerErr)
		if outerErr != nil {
			t.Errorf("outer release = %v, want nil", outerErr)
		}
		if got := sink.String(); got != "inner" {
			t.Errorf("underlying writer = %q, want inner", got)
		}
	})

	t.Run("a flush failure is reported when the render succeeded", func(t *testing.T) {
		var err error
		func() {
			buf, release := AcquireBuffer(failingWriter{})
			defer release(&err)
			if _, werr := buf.WriteString("x"); werr != nil {
				t.Fatal(werr)
			}
		}()
		if err == nil {
			t.Error("a flush failure must surface as the render's error")
		}
	})

	t.Run("a render error is never overwritten by a flush error", func(t *testing.T) {
		sentinel := errRender
		err := sentinel
		func() {
			buf, release := AcquireBuffer(failingWriter{})
			defer release(&err)
			if _, werr := buf.WriteString("x"); werr != nil {
				t.Fatal(werr)
			}
		}()
		if err != sentinel {
			t.Errorf("err = %v, want the render's own error %v", err, sentinel)
		}
	})

	t.Run("a nil error pointer is tolerated", func(t *testing.T) {
		var sink bytes.Buffer
		buf, release := AcquireBuffer(&sink)
		if _, err := buf.WriteString("x"); err != nil {
			t.Fatal(err)
		}
		release(nil)
		if got := sink.String(); got != "x" {
			t.Errorf("underlying writer = %q, want x", got)
		}
	})
}

var errRender = errors.New("render failed")

// failingWriter fails every write, which is how the buffer's flush is
// made to fail.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errFlush }

var errFlush = errors.New("flush failed")
