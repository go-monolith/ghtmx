package sloghandler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fatih/color"
)

// The handler is what every ghtmx command's output goes through, so the
// things worth pinning are the ones a user would notice: the level
// icons, that attributes are rendered, that the timestamp is stripped
// (it would make CI logs and golden output non-deterministic), and that
// concurrent writes do not interleave.

// noColor forces colour off for the duration of a test. The fatih/color
// package decides at init time based on whether stdout is a terminal,
// which would otherwise make these assertions depend on how the test
// binary was invoked.
func noColor(t *testing.T) {
	t.Helper()
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

func TestHandlerRendersLevelIcons(t *testing.T) {
	noColor(t)
	tests := []struct {
		name     string
		level    slog.Level
		wantIcon string
	}{
		{"debug", slog.LevelDebug, "(✓)"},
		{"info", slog.LevelInfo, "(✓)"},
		{"warn", slog.LevelWarn, "(!)"},
		{"error", slog.LevelError, "(✗)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(NewHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			log.Log(context.Background(), tt.level, "hello")

			got := buf.String()
			if !strings.Contains(got, tt.wantIcon) {
				t.Errorf("output %q does not contain the %s icon %q", got, tt.name, tt.wantIcon)
			}
			if !strings.Contains(got, "hello") {
				t.Errorf("output %q does not contain the message", got)
			}
			if !strings.HasSuffix(got, "\n") {
				t.Errorf("output %q does not end in a newline", got)
			}
		})
	}
}

func TestHandlerRendersAttributes(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	log := slog.New(NewHandler(&buf, nil))

	log.Info("generated", "file", "index.ghtmx", "count", 3)

	got := buf.String()
	for _, want := range []string{"generated", "file=index.ghtmx", "count=3", "[", "]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}
}

// TestHandlerOmitsBracketsWithoutAttributes pins the other side of the
// branch above: a bare message must not pick up an empty "[ ]".
func TestHandlerOmitsBracketsWithoutAttributes(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	log := slog.New(NewHandler(&buf, nil))

	log.Info("bare message")

	if got := buf.String(); strings.Contains(got, "[") {
		t.Errorf("output %q contains attribute brackets for a message with no attributes", got)
	}
}

func TestNewHandlerToleratesNilOptions(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	h := NewHandler(&buf, nil)
	if h == nil {
		t.Fatal("NewHandler(w, nil) returned nil")
	}
	// The default level is Info, so Debug must be filtered out.
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Debug is enabled under default options, want Info as the floor")
	}
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Info is not enabled under default options")
	}
}

func TestWithAttrsAndWithGroupShareTheWriterAndLock(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	h := NewHandler(&buf, nil)

	withAttrs, ok := h.WithAttrs([]slog.Attr{slog.String("run", "1")}).(*Handler)
	if !ok {
		t.Fatal("WithAttrs did not return a *Handler")
	}
	withGroup, ok := h.WithGroup("phase").(*Handler)
	if !ok {
		t.Fatal("WithGroup did not return a *Handler")
	}

	// Derived handlers must keep writing to the same place, under the
	// same mutex — otherwise two loggers derived from one handler
	// interleave mid-line.
	for _, d := range []*Handler{withAttrs, withGroup} {
		if d.w != h.w {
			t.Error("a derived handler writes to a different writer")
		}
		if d.m != h.m {
			t.Error("a derived handler uses a different mutex; concurrent writes could interleave")
		}
	}
}

// TestHandlerSerialisesConcurrentWrites is the reason the handler holds
// a mutex at all. Without it, two goroutines logging at once can split
// each other's lines.
func TestHandlerSerialisesConcurrentWrites(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	log := slog.New(NewHandler(&buf, nil))

	const goroutines, perGoroutine = 8, 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				log.Info("concurrent")
			}
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if got, want := len(lines), goroutines*perGoroutine; got != want {
		t.Fatalf("wrote %d lines, want %d — writes interleaved", got, want)
	}
	for i, line := range lines {
		if !strings.Contains(line, "concurrent") {
			t.Fatalf("line %d is torn: %q", i, line)
		}
	}
}

// errWriter fails every write, which is the only way to reach Handle's
// error return.
type errWriter struct{ err error }

func (w errWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHandlerReportsWriteFailures(t *testing.T) {
	noColor(t)
	sentinel := errors.New("disk full")
	h := NewHandler(errWriter{err: sentinel}, nil)

	err := h.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "msg", 0))
	if !errors.Is(err, sentinel) {
		t.Errorf("Handle returned %v, want %v — a failed write must not be swallowed", err, sentinel)
	}
}

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name        string
		logLevel    string
		verbose     bool
		wantEnabled []slog.Level
		wantFiltred []slog.Level
	}{
		{
			name:        "default is info",
			logLevel:    "",
			wantEnabled: []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelError},
			wantFiltred: []slog.Level{slog.LevelDebug},
		},
		{
			name:        "debug",
			logLevel:    "debug",
			wantEnabled: []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelError},
		},
		{
			name:        "warn",
			logLevel:    "warn",
			wantEnabled: []slog.Level{slog.LevelWarn, slog.LevelError},
			wantFiltred: []slog.Level{slog.LevelDebug, slog.LevelInfo},
		},
		{
			name:        "error",
			logLevel:    "error",
			wantEnabled: []slog.Level{slog.LevelError},
			wantFiltred: []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn},
		},
		{
			// verbose wins over an explicit level: -v is the escape
			// hatch a user reaches for when the level they set is
			// hiding what they need.
			name:        "verbose overrides error",
			logLevel:    "error",
			verbose:     true,
			wantEnabled: []slog.Level{slog.LevelDebug, slog.LevelInfo},
		},
		{
			name:        "unrecognised level falls back to info",
			logLevel:    "chatty",
			wantEnabled: []slog.Level{slog.LevelInfo},
			wantFiltred: []slog.Level{slog.LevelDebug},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := NewLogger(tt.logLevel, tt.verbose, io.Discard)
			for _, level := range tt.wantEnabled {
				if !log.Enabled(context.Background(), level) {
					t.Errorf("%s is filtered out, want it enabled", level)
				}
			}
			for _, level := range tt.wantFiltred {
				if log.Enabled(context.Background(), level) {
					t.Errorf("%s is enabled, want it filtered out", level)
				}
			}
		})
	}
}

// TestNewLoggerWritesToTheGivenWriter pins that the logger actually
// reaches the writer it was handed — the reason every command passes
// its own stderr rather than letting the package pick one.
func TestNewLoggerWritesToTheGivenWriter(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	NewLogger("info", false, &buf).Info("routed")
	if !strings.Contains(buf.String(), "routed") {
		t.Errorf("output %q does not contain the logged message", buf.String())
	}
}

// TestHandlerStripsTimestamps pins a deliberate formatting choice: the
// time attribute is dropped, so command output is byte-stable across
// runs and usable in golden comparisons.
func TestHandlerStripsTimestamps(t *testing.T) {
	noColor(t)
	var first, second bytes.Buffer
	slog.New(NewHandler(&first, nil)).Info("stable")
	slog.New(NewHandler(&second, nil)).Info("stable")

	if first.String() != second.String() {
		t.Errorf("two runs differ, so something time-varying leaked in:\n%q\n%q", first.String(), second.String())
	}
}

// TestReplaceAttrIsChained pins that a caller-supplied ReplaceAttr still
// runs, rather than being silently replaced by the handler's own.
func TestReplaceAttrIsChained(t *testing.T) {
	noColor(t)
	var buf bytes.Buffer
	h := NewHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == "secret" {
				return slog.String("secret", "REDACTED")
			}
			return a
		},
	})
	// Attributes go through the underlying text handler, which is where
	// ReplaceAttr applies; WithAttrs is the path that reaches it.
	slog.New(h).With("secret", "hunter2").Info("msg")

	if got := buf.String(); strings.Contains(got, "hunter2") {
		t.Errorf("output %q leaked the value a caller ReplaceAttr redacted", got)
	}
}

var _ io.Writer = errWriter{}
