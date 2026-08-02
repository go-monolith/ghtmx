package generatecmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ghtmxruntime "github.com/go-monolith/ghtmx/runtime"
)

// Watch mode is the whole dev loop: walk the tree, register watches,
// regenerate on change, and shut down cleanly when the context ends.
// None of it runs during an ordinary generate, so a break in the walk or
// the shutdown only shows up when someone actually runs `ghtmx generate
// -watch` — and then it shows up as a dev server that either never
// regenerates or never exits.
//
// These drive the real loop with a real watcher and cancel it through
// the context, which is exactly how Ctrl-C reaches it. The assertions
// are on what must be true regardless of timing: generation happened,
// and the loop returned rather than hanging.

// runWatchUntilCancelled starts watch mode, waits for the initial pass,
// then cancels and returns whatever Run gave back.
func runWatchUntilCancelled(t *testing.T, dir string, extraArgs ...string) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	args := append([]string{"-path", dir, "-include-version=false", "-watch",
		"-open-browser=false"}, extraArgs...)

	done := make(chan error, 1)
	go func() { done <- Run(ctx, io.Discard, io.Discard, args) }()

	// The initial generation pass happens before the watch loop settles;
	// poll for its output rather than sleeping a fixed amount.
	generated := filepath.Join(dir, "page_ghtmx.go")
	deadline := time.Now().Add(30 * time.Second)
	var generatedOK bool
	for time.Now().Before(deadline) {
		if _, err := os.Stat(generated); err == nil {
			generatedOK = true
			break
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("watch mode exited before generating: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Falling out of the loop without this would leave every caller
	// asserting against a run that never produced anything.
	if !generatedOK {
		cancel()
		t.Fatal("watch mode did not generate within 30s")
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("watch mode did not exit after its context was cancelled; Ctrl-C would hang")
		return nil
	}
}

func TestWatchModeGeneratesAndShutsDown(t *testing.T) {
	dir := newProject(t)

	if err := runWatchUntilCancelled(t, dir); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("watch mode returned %v, want nil or context.Canceled", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "page_ghtmx.go")); err != nil {
		t.Errorf("watch mode did not generate on startup: %v", err)
	}
}

// TestWatchModeRegeneratesOnChange is the loop's reason for existing.
func TestWatchModeRegeneratesOnChange(t *testing.T) {
	dir := newProject(t)
	generated := filepath.Join(dir, "page_ghtmx.go")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, io.Discard, io.Discard, []string{
			"-path", dir, "-include-version=false", "-watch", "-open-browser=false",
		})
	}()

	// Wait for the first pass, then for the content to settle: reading
	// the moment the file appears can capture a partial write, and the
	// change check below would then pass on the first pass completing
	// rather than on the watcher firing.
	waitForFile(t, generated, 30*time.Second)
	before := stableContents(t, generated)
	if !strings.Contains(before, "<h1>") {
		t.Fatalf("the first pass did not produce the pre-edit markup:\n%s", before)
	}

	// Change the template; the watcher should pick it up.
	edited := "package app\n\ntempl Page(title string) {\n\t<section>{ title }</section>\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "page.ghtmx"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	var changed bool
	for time.Now().Before(deadline) {
		after, err := os.ReadFile(generated)
		if err == nil && string(after) != string(before) {
			changed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("watch mode did not exit after cancellation; Ctrl-C would hang")
	}

	if !changed {
		t.Error("the watcher did not regenerate after the template changed")
	}
}

// TestWatchModeWithAnIgnorePattern covers the ignore-pattern branch of
// the walk, which is how large repositories keep vendored trees out of
// the watch set.
func TestWatchModeWithAnIgnorePattern(t *testing.T) {
	dir := newProject(t)

	err := runWatchUntilCancelled(t, dir, "-ignore-pattern", `vendor/`)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("watch mode with an ignore pattern returned %v", err)
	}
}

// TestWatchModeCleansUpDevModeTextFiles pins the shutdown step that
// removes the sidecar text files watch mode writes: leaving one behind
// makes the next ordinary build serve stale literals, which reads as an
// edit that did not take.
func TestWatchModeCleansUpDevModeTextFiles(t *testing.T) {
	dir := newProject(t)

	// The sidecars live outside the project, under a hash of each
	// template's path, so the assertion has to name them directly rather
	// than walking the project tree — walking it would find nothing
	// whether or not cleanup works.
	sidecar := ghtmxruntime.GetDevModeTextFileName(filepath.Join(dir, "page.ghtmx"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, io.Discard, io.Discard, []string{
			"-path", dir, "-include-version=false", "-watch", "-open-browser=false",
		})
	}()

	// Established first: without this the post-shutdown check passes
	// whether cleanup works or the sidecar was never written at all.
	var written bool
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sidecar); err == nil {
			written = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("watch mode did not exit after cancellation")
	}

	if !written {
		t.Skip("watch mode wrote no dev-mode sidecar for this template; nothing to clean up")
	}
	if _, err := os.Stat(sidecar); err == nil {
		t.Errorf("the dev-mode sidecar survived shutdown: %s", sidecar)
	}
}

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s was not created within %s", path, within)
}

// TestFatalErrorAsPopulatesTheTarget covers the method errors.As calls.
// Reporting a match without assigning would hand the caller a zero
// FatalError whose wrapped cause is gone — true, and useless.
func TestFatalErrorAsPopulatesTheTarget(t *testing.T) {
	cause := errors.New("boom")
	fatal := FatalError{Err: cause}

	var target FatalError
	if !fatal.As(&target) {
		t.Fatal("As(*FatalError) = false; the watch loop could not classify this")
	}
	if !errors.Is(target.Err, cause) {
		t.Errorf("target.Err = %v, want the wrapped cause %v", target.Err, cause)
	}

	// And through errors.As, which is how callers actually reach it.
	var viaErrorsAs FatalError
	if !errors.As(error(fatal), &viaErrorsAs) {
		t.Fatal("errors.As did not match")
	}
	if !errors.Is(viaErrorsAs.Err, cause) {
		t.Errorf("errors.As left the target empty: %+v", viaErrorsAs)
	}

	var other *ArgumentError
	if fatal.As(&other) {
		t.Error("As(**ArgumentError) = true; a fatal error matched an unrelated target")
	}
}

// stableContents reads path until two consecutive reads agree, so a
// partially written file is never taken as final.
func stableContents(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var previous string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil {
			if current := string(b); current != "" && current == previous {
				return current
			} else {
				previous = current
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s never settled", path)
	return ""
}
