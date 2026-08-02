package generatecmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	for time.Now().Before(deadline) {
		if _, err := os.Stat(generated); err == nil {
			break
		}
		select {
		case err := <-done:
			cancel()
			t.Fatalf("watch mode exited before generating: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
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

	// Wait for the first pass.
	waitForFile(t, generated, 30*time.Second)
	before, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
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
	<-done

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

	if err := runWatchUntilCancelled(t, dir); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("watch mode returned %v", err)
	}

	// No _templ.txt sidecars should survive the shutdown.
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".txt" {
			t.Errorf("a watch-mode sidecar survived shutdown: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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

// TestFatalErrorAs covers the As method, which is what lets the watch
// loop ask "is this fatal" through errors.As rather than a type switch.
func TestFatalErrorAs(t *testing.T) {
	fatal := FatalError{Err: errors.New("boom")}

	var target FatalError
	if !fatal.As(&target) {
		t.Error("As(*FatalError) = false; the watch loop could not classify this")
	}
	var other *ArgumentError
	if fatal.As(&other) {
		t.Error("As(**ArgumentError) = true; a fatal error matched an unrelated target")
	}
}
