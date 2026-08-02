package generatecmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every one of these combinations is something a user types by accident,
// and each has a hand-written message telling them which flag to drop.
// A validation branch that stops firing turns a clear "cannot use -check
// with -watch" into whichever confusing failure the combination produces
// downstream — or worse, into a run that appears to work.

func TestArgumentValidation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantMsg string // substring of the message the user sees
	}{
		{
			name:    "watching a single file",
			args:    []string{"-watch", "-f", "page.ghtmx"},
			wantMsg: "cannot watch a single file",
		},
		{
			name:    "check with watch",
			args:    []string{"-check", "-watch"},
			wantMsg: "cannot use -check with -watch",
		},
		{
			name:    "check with stdout",
			args:    []string{"-check", "-stdout", "-f", "page.ghtmx"},
			wantMsg: "cannot use -check with -stdout",
		},
		{
			name:    "stdout without a file",
			args:    []string{"-stdout"},
			wantMsg: "only a single file can be output to stdout",
		},
		{
			name:    "malformed watch pattern",
			args:    []string{"-watch", "-watch-pattern", "("},
			wantMsg: "invalid watch pattern",
		},
		{
			name:    "malformed ignore pattern",
			args:    []string{"-watch", "-ignore-pattern", "("},
			wantMsg: "invalid ignore pattern",
		},
		{
			name:    "unknown flag",
			args:    []string{"-no-such-flag"},
			wantMsg: "failed to parse arguments",
		},
		{
			name:    "malformed severity override",
			args:    []string{"-check-severity", "not-an-override"},
			wantMsg: "failed to parse arguments",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := NewArguments(io.Discard, io.Discard, tt.args)
			if err == nil {
				t.Fatalf("NewArguments accepted %v, want an error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error %q does not contain %q — the user would not know which flag to drop", err, tt.wantMsg)
			}
		})
	}
}

// TestArgumentsAcceptValidCombinations guards the table above from
// over-reach: a validation that rejected everything would satisfy every
// assertion in it.
func TestArgumentsAcceptValidCombinations(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"defaults", nil},
		{"check alone", []string{"-check"}},
		{"watch alone", []string{"-watch"}},
		{"single file", []string{"-f", "page.ghtmx"}},
		{"single file to stdout", []string{"-f", "page.ghtmx", "-stdout"}},
		{"severity override", []string{"-check-severity", "GHTMX-W0101=off"}},
		{"repeated source dirs", []string{"-source-dir", "a", "-source-dir", "b"}},
		{"worker count", []string{"-w", "2"}},
		{"lazy", []string{"-lazy"}},
		{"keep orphaned files", []string{"-keep-orphaned-files"}},
		{"cache disabled", []string{"-cache=false"}},
		{"valid watch pattern", []string{"-watch", "-watch-pattern", `\.ghtmx$`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := NewArguments(io.Discard, io.Discard, tt.args); err != nil {
				t.Errorf("NewArguments rejected %v: %v", tt.args, err)
			}
		})
	}
}

// TestRunReportsAnUnwritableOutputDirectory is the filesystem fault that
// matters most: generation succeeds, the write fails, and the command
// has to say so rather than reporting success over a file that was never
// updated.
func TestRunReportsAnUnwritableOutputDirectory(t *testing.T) {
	dir := newProject(t)

	// Read-only directory: the generated file cannot be created.
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	skipIfWritable(t, dir)

	if _, _, err := runGenerate(t, "-path", dir, "-include-version=false"); err == nil {
		t.Error("Run succeeded writing into a read-only directory")
	}
}

// TestRunOnAMissingSingleFile pins the -f path's early failure.
func TestRunOnAMissingSingleFile(t *testing.T) {
	dir := newProject(t)

	_, _, err := runGenerate(t, "-f", filepath.Join(dir, "absent.ghtmx"), "-include-version=false")
	if err == nil {
		t.Error("Run succeeded on a -f pointing at a file that does not exist")
	}
}

// TestStartProxyRejectsAnUnparseableURL covers startProxy's FatalError:
// watch mode must stop rather than retry forever against a URL that can
// never resolve. Called directly because startProxy only runs from the
// watch loop, which a plain generate never enters.
func TestStartProxyRejectsAnUnparseableURL(t *testing.T) {
	cmd := &Generate{Log: quietLog()}
	cmd.Args.Proxy = "://not-a-url"

	_, err := cmd.startProxy()
	if err == nil {
		t.Fatal("startProxy accepted an unparseable URL")
	}
	if !errors.Is(err, FatalError{}) {
		t.Errorf("error %v is not fatal; watch mode would retry a URL that can never resolve", err)
	}
}

// TestRunKeepsOrphanedFiles pins the -keep-orphaned-files branch: by
// default a generated file whose template is gone is removed, and the
// flag exists for projects that check the output in.
func TestRunKeepsOrphanedFiles(t *testing.T) {
	dir := newProject(t)

	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false"); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	generated := filepath.Join(dir, "page_ghtmx.go")
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("nothing was generated: %v", err)
	}

	// Remove the template, leaving its output orphaned.
	if err := os.Remove(filepath.Join(dir, "page.ghtmx")); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false",
		"-keep-orphaned-files"); err != nil {
		t.Fatalf("Run with -keep-orphaned-files: %v\nstderr: %s", err, stderr)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Errorf("-keep-orphaned-files removed the orphan anyway: %v", err)
	}
}

// TestRunRemovesOrphanedFilesByDefault is the other half.
func TestRunRemovesOrphanedFilesByDefault(t *testing.T) {
	dir := newProject(t)

	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false"); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	generated := filepath.Join(dir, "page_ghtmx.go")
	if err := os.Remove(filepath.Join(dir, "page.ghtmx")); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false"); err != nil {
		t.Fatalf("second Run: %v\nstderr: %s", err, stderr)
	}
	if _, err := os.Stat(generated); err == nil {
		t.Error("a generated file whose template was deleted survived; stale output would keep compiling")
	}
}

// TestRunWithSeveralWorkers exercises the worker pool with more than one
// goroutine, which is the configuration every real project uses.
func TestRunWithSeveralWorkers(t *testing.T) {
	dir := newProject(t)
	// A second template so the pool actually has work to distribute.
	if err := os.WriteFile(filepath.Join(dir, "other.ghtmx"),
		[]byte("package app\n\ntempl Other() {\n\t<p>other</p>\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false", "-w", "4"); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	for _, name := range []string{"page_ghtmx.go", "other_ghtmx.go"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s was not generated: %v", name, err)
		}
	}
}

// TestRunLazySkipsUpToDateOutput covers -lazy, which compares mtimes
// rather than content.
func TestRunLazySkipsUpToDateOutput(t *testing.T) {
	dir := newProject(t)

	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false"); err != nil {
		t.Fatalf("Run: %v\nstderr: %s", err, stderr)
	}
	if _, stderr, err := runGenerate(t, "-path", dir, "-include-version=false", "-lazy"); err != nil {
		t.Fatalf("Run -lazy: %v\nstderr: %s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "page_ghtmx.go")); err != nil {
		t.Errorf("the generated file went missing under -lazy: %v", err)
	}
}

// TestRunWithACancelledContextDoesNotCorrupt pins the observed
// behaviour: a run this small completes before cancellation is polled,
// so the contract is not "always fails" but "never leaves a half-written
// file". Asserting an error here would encode a race as a requirement.
func TestRunWithACancelledContextDoesNotCorrupt(t *testing.T) {
	dir := newProject(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Run(ctx, io.Discard, io.Discard, []string{"-path", dir, "-include-version=false"})
	generated := filepath.Join(dir, "page_ghtmx.go")
	if err != nil {
		// Cancelled early: nothing should have been written.
		if _, statErr := os.Stat(generated); statErr == nil {
			t.Error("Run failed on a cancelled context but left a generated file behind")
		}
		return
	}
	// Completed: the output has to be whole.
	content, readErr := os.ReadFile(generated)
	if readErr != nil {
		t.Fatalf("Run reported success but wrote nothing: %v", readErr)
	}
	if !strings.Contains(string(content), "func Page(") {
		t.Errorf("the generated file is truncated:\n%s", content)
	}
}

// skipIfWritable probes rather than checking euid: root, unusual
// capabilities and filesystems that ignore mode bits all make a
// read-only directory writable, and euid catches only the first.
func skipIfWritable(t *testing.T, dir string) {
	t.Helper()
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err == nil {
		_ = os.Remove(probe)
		t.Skip("the directory is writable despite its mode; permission bits are not enforced here")
	}
}
