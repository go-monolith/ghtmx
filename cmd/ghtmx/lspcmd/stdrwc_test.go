package lspcmd

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stdrwc is the pipe the editor talks to the language server through, so
// its Close has to shut both halves down and report what went wrong.
// Swallowing a close error leaks the descriptor for the life of the
// editor session; closing only one half leaves the other end waiting on
// a stream nobody will write to again.

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestStdRwcReadsAndWrites(t *testing.T) {
	var out bytes.Buffer
	rwc := newStdRwc(discardLog(), "test", &out, strings.NewReader("hello"))

	buf := make([]byte, 5)
	n, err := rwc.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf[:n]); got != "hello" {
		t.Errorf("Read returned %q, want %q", got, "hello")
	}

	if _, err := rwc.Write([]byte("world")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := out.String(); got != "world" {
		t.Errorf("Write produced %q, want %q", got, "world")
	}
}

// closerRecorder is an io.ReadWriteCloser that records the close and can
// be made to fail it.
type closerRecorder struct {
	closed bool
	err    error
}

func (c *closerRecorder) Read([]byte) (int, error)    { return 0, io.EOF }
func (c *closerRecorder) Write(p []byte) (int, error) { return len(p), nil }
func (c *closerRecorder) Close() error {
	c.closed = true
	return c.err
}

func TestStdRwcCloseClosesBothHalves(t *testing.T) {
	r := &closerRecorder{}
	w := &closerRecorder{}
	rwc := newStdRwc(discardLog(), "test", w, r)

	if err := rwc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !r.closed {
		t.Error("the reader was not closed; the other end would wait on a dead stream")
	}
	if !w.closed {
		t.Error("the writer was not closed")
	}
}

func TestStdRwcCloseReportsFailures(t *testing.T) {
	readErr := errors.New("reader broke")
	writeErr := errors.New("writer broke")

	tests := []struct {
		name  string
		rErr  error
		wErr  error
		wants []error
	}{
		{"reader fails", readErr, nil, []error{readErr}},
		{"writer fails", nil, writeErr, []error{writeErr}},
		// Both must survive: reporting only the first hides the second.
		{"both fail", readErr, writeErr, []error{readErr, writeErr}},
		{"neither fails", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rwc := newStdRwc(discardLog(), "test",
				&closerRecorder{err: tt.wErr}, &closerRecorder{err: tt.rErr})

			err := rwc.Close()
			if len(tt.wants) == 0 {
				if err != nil {
					t.Errorf("Close returned %v, want nil", err)
				}
				return
			}
			for _, want := range tt.wants {
				if !errors.Is(err, want) {
					t.Errorf("Close returned %v, want it to wrap %v", err, want)
				}
			}
		})
	}
}

// TestStdRwcCloseToleratesNonClosers pins the type assertions: stdin and
// stdout are not always io.Closer, and Close must not panic on them.
func TestStdRwcCloseToleratesNonClosers(t *testing.T) {
	rwc := newStdRwc(discardLog(), "test", new(bytes.Buffer), strings.NewReader(""))

	if err := rwc.Close(); err != nil {
		t.Errorf("Close on non-closing halves returned %v, want nil", err)
	}
}

// TestRunReportsAnUnopenableLogFile pins the early failure path: if the
// requested log file cannot be created, the server has to say so rather
// than starting up with logging silently disabled, which would leave
// someone debugging an LSP problem with no trace at all.
func TestRunReportsAnUnopenableLogFile(t *testing.T) {
	// A path whose parent is a regular file cannot be created.
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Run(strings.NewReader(""), io.Discard, io.Discard, Arguments{
		Log: filepath.Join(parent, "lsp.log"),
	})
	if err == nil {
		t.Fatal("Run succeeded with an unopenable log file, want an error")
	}
	if !strings.Contains(err.Error(), "log file") {
		t.Errorf("error %q does not mention the log file", err)
	}
}

// TestRunCreatesTheLogFile covers the other branch: the file is opened,
// appended to, and the server proceeds. It fails afterwards because
// there is no gopls to connect to, which is fine — the assertion is
// about the log file existing.
func TestRunCreatesTheLogFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "lsp.log")

	// stdin is empty, so the stream ends immediately and Run returns.
	_ = Run(strings.NewReader(""), io.Discard, io.Discard, Arguments{
		Log:         logPath,
		GoplsRemote: "127.0.0.1:1", // nothing listening: fails fast
	})

	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("the log file was not created: %v", err)
	}
}
