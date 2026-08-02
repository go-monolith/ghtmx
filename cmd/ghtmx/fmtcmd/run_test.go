package fmtcmd

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `ghtmx fmt` rewrites the user's source in place, so its failure modes
// are the expensive kind. Reading a file it cannot parse must leave that
// file alone and say why; -stdout must not also write; and -fail is what
// CI relies on to reject an unformatted branch, so it has to notice a
// change without making one.

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

const unformatted = "package app\n\ntempl Page() {\n<div></div>\n}\n"
const formatted = "package app\n\ntempl Page() {\n\t<div></div>\n}\n"

func writeTemplate(t *testing.T, contents string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "page.ghtmx")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestRunFormatsAFileInPlace(t *testing.T) {
	_, path := writeTemplate(t, unformatted)

	if err := Run(quiet(), strings.NewReader(""), io.Discard, Arguments{Files: []string{path}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != formatted {
		t.Errorf("the file was not reformatted:\n%q", got)
	}
}

// TestRunToStdoutLeavesTheFileAlone pins the flag editors use to preview
// a format: writing the file as well would defeat the point.
func TestRunToStdoutLeavesTheFileAlone(t *testing.T) {
	_, path := writeTemplate(t, unformatted)

	var stdout bytes.Buffer
	err := Run(quiet(), strings.NewReader(""), &stdout,
		Arguments{Files: []string{path}, ToStdout: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(stdout.String(), "\t<div></div>") {
		t.Errorf("stdout does not carry the formatted source:\n%s", stdout.String())
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != unformatted {
		t.Errorf("-stdout rewrote the file:\n%q", onDisk)
	}
}

// TestRunFailIfChange is what CI runs. It has to report the change
// without applying it, or a formatting check would quietly fix the
// branch it was meant to reject.
func TestRunFailIfChange(t *testing.T) {
	t.Run("unformatted source fails", func(t *testing.T) {
		_, path := writeTemplate(t, unformatted)

		err := Run(quiet(), strings.NewReader(""), io.Discard,
			Arguments{Files: []string{path}, FailIfChanged: true})
		if err == nil {
			t.Fatal("Run succeeded on unformatted source with -fail")
		}
	})

	t.Run("formatted source passes", func(t *testing.T) {
		_, path := writeTemplate(t, formatted)

		if err := Run(quiet(), strings.NewReader(""), io.Discard,
			Arguments{Files: []string{path}, FailIfChanged: true}); err != nil {
			t.Errorf("Run failed on already-formatted source: %v", err)
		}
	})
}

// TestRunReportsAnUnparseableFile pins that a broken template is named
// and left alone, rather than being overwritten with whatever partial
// output the formatter managed.
func TestRunReportsAnUnparseableFile(t *testing.T) {
	const broken = "package app\n\ntempl Page() {\n\t<div>\n}\n"
	_, path := writeTemplate(t, broken)

	err := Run(quiet(), strings.NewReader(""), io.Discard, Arguments{Files: []string{path}})
	if err == nil {
		t.Fatal("Run succeeded on an unparseable template")
	}
	if !strings.Contains(err.Error(), "page.ghtmx") {
		t.Errorf("error %q does not name the offending file", err)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != broken {
		t.Errorf("an unparseable file was rewritten:\n%q", onDisk)
	}
}

func TestRunReportsAMissingFile(t *testing.T) {
	dir := t.TempDir()

	err := Run(quiet(), strings.NewReader(""), io.Discard,
		Arguments{Files: []string{filepath.Join(dir, "absent.ghtmx")}})
	if err == nil {
		t.Error("Run succeeded on a file that does not exist")
	}
}

func TestRunFromStdin(t *testing.T) {
	var stdout bytes.Buffer

	err := Run(quiet(), strings.NewReader(unformatted), &stdout,
		Arguments{StdinFilepath: "page.ghtmx"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "\t<div></div>") {
		t.Errorf("stdout is not the reindented source:\n%s", stdout.String())
	}
}

// TestRunFromStdinReportsAnUnparseableBuffer pins that format-on-save
// with a half-typed buffer fails rather than handing the editor a
// truncated document to write back.
func TestRunFromStdinReportsAnUnparseableBuffer(t *testing.T) {
	var stdout bytes.Buffer

	err := Run(quiet(), strings.NewReader("package app\n\ntempl X() {\n\t<div>\n}\n"), &stdout,
		Arguments{StdinFilepath: "page.ghtmx"})
	if err == nil {
		t.Fatal("Run succeeded on an unparseable stdin buffer")
	}
	if stdout.Len() != 0 {
		t.Errorf("a failed format still wrote to stdout: %q", stdout.String())
	}
}

// TestRunOverSeveralFiles exercises the worker pool, which is the path
// every real invocation takes.
func TestRunOverSeveralFiles(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for _, name := range []string{"a.ghtmx", "b.ghtmx", "c.ghtmx"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(unformatted), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}

	if err := Run(quiet(), strings.NewReader(""), io.Discard,
		Arguments{Files: paths, WorkerCount: 2}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range paths {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != formatted {
			t.Errorf("%s was not formatted:\n%q", filepath.Base(path), got)
		}
	}
}
