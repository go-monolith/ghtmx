package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func fragmentFixture() func(GeneratedComponentInput) error {
	return func(in GeneratedComponentInput) error {
		_, err := in.Writer.Write([]byte(`<tr id="row-1"><td>Alpha</td></tr>`))
		return err
	}
}

// TestGeneratedFragmentMethodsAgree: Render and RenderFragment run the
// same body and write the same bytes.
func TestGeneratedFragmentMethodsAgree(t *testing.T) {
	f := GeneratedFragment(fragmentFixture())
	ctx := context.Background()

	var a, b strings.Builder
	if err := f.Render(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if err := f.RenderFragment(ctx, &b); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() || a.String() == "" {
		t.Errorf("methods must write identical bytes, got %q and %q", a.String(), b.String())
	}
}

// headerRecordingResponseWriter records every interaction beyond plain
// writes, to prove the wrapper touches nothing beyond the body function
// (the full generated pipeline is covered by the examples/fragments
// handler tests).
type headerRecordingResponseWriter struct {
	header      http.Header
	body        strings.Builder
	wroteHeader bool
	headerCalls int
	flushCalls  int
}

func (w *headerRecordingResponseWriter) Flush() {
	w.flushCalls++
}

func (w *headerRecordingResponseWriter) Header() http.Header {
	w.headerCalls++
	return w.header
}

func (w *headerRecordingResponseWriter) Write(p []byte) (int, error) {
	return w.body.Write(p)
}

func (w *headerRecordingResponseWriter) WriteHeader(int) {
	w.wroteHeader = true
}

// TestRenderFragmentWritesNoStatusOrHeaders: on the handler-explicit path
// the engine calls only Write on the ResponseWriter — it never writes a
// status code, never reads or mutates the header map (FR-034).
func TestRenderFragmentWritesNoStatusOrHeaders(t *testing.T) {
	rec := &headerRecordingResponseWriter{header: http.Header{}}
	var w http.ResponseWriter = rec

	f := GeneratedFragment(fragmentFixture())
	if err := f.RenderFragment(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if rec.wroteHeader {
		t.Error("the engine must not write an HTTP status code")
	}
	if rec.headerCalls != 0 || len(rec.header) != 0 {
		t.Errorf("the engine must not touch response headers: %d Header() calls, %v", rec.headerCalls, rec.header)
	}
	if rec.flushCalls != 0 {
		t.Errorf("the engine must not flush the ResponseWriter, got %d Flush() calls", rec.flushCalls)
	}
	if !strings.Contains(rec.body.String(), "row-1") {
		t.Errorf("fragment markup missing, got %q", rec.body.String())
	}
}

// TestGeneratedFragmentErrorsPropagate: FR-090 — render errors return to
// the caller from both methods.
func TestGeneratedFragmentErrorsPropagate(t *testing.T) {
	sentinel := errors.New("body failed")
	f := GeneratedFragment(func(GeneratedComponentInput) error { return sentinel })

	var sb strings.Builder
	if err := f.Render(context.Background(), &sb); !errors.Is(err, sentinel) {
		t.Errorf("Render must propagate the body error, got %v", err)
	}
	if err := f.RenderFragment(context.Background(), &sb); !errors.Is(err, sentinel) {
		t.Errorf("RenderFragment must propagate the body error, got %v", err)
	}
}

// TestReleaseBufferDoesNotFlushUnderlying: end-of-render buffer release
// drains the buffered bytes but never cascades to the underlying
// http.Flusher — that would commit headers the handler did not request
// (FR-034, FR-090).
func TestReleaseBufferDoesNotFlushUnderlying(t *testing.T) {
	rec := &headerRecordingResponseWriter{header: http.Header{}}
	buf, existing := GetBuffer(rec)
	if existing {
		t.Fatal("expected a fresh buffer")
	}
	if _, err := buf.WriteString("<p>hi</p>"); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseBuffer(buf); err != nil {
		t.Fatal(err)
	}
	if rec.body.String() != "<p>hi</p>" {
		t.Errorf("buffered bytes must drain to the writer, got %q", rec.body.String())
	}
	if rec.flushCalls != 0 {
		t.Errorf("release must not invoke the http.Flusher, got %d calls", rec.flushCalls)
	}
}

// TestBufferFlushPropagatesToFlusher: the explicit streaming path
// (ghtmx.Flush()) still reaches the underlying http.Flusher.
func TestBufferFlushPropagatesToFlusher(t *testing.T) {
	rec := &headerRecordingResponseWriter{header: http.Header{}}
	buf, _ := GetBuffer(rec)
	if _, err := buf.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	if err := buf.Flush(); err != nil {
		t.Fatal(err)
	}
	if rec.flushCalls != 1 {
		t.Errorf("an explicit flush must reach the http.Flusher, got %d calls", rec.flushCalls)
	}
	if err := ReleaseBuffer(buf); err != nil {
		t.Fatal(err)
	}
}
