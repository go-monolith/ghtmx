package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// In watch mode the generated code reads its string literals from a
// sidecar .txt file instead of the compiled binary, which is what lets a
// text edit reload without recompiling. Everything here is on the dev
// loop's hot path: a stale cache serves the previous text, and a missing
// file has to report rather than silently render the compiled default,
// or the user edits a template and sees nothing change.

// writeSidecar creates a generated Go file and the .txt beside it that
// watch mode reads its literals from.
func writeSidecar(t *testing.T, dir string, literals ...string) (goPath, txtPath string) {
	t.Helper()
	goPath = filepath.Join(dir, "page_ghtmx.go")
	if err := os.WriteFile(goPath, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// GetWatchedString resolves the path before deriving the sidecar
	// name, so the test has to resolve too: /tmp is a symlink to
	// /private/var on macOS, and Windows hands back an 8.3 short name.
	// Without this the two sides hash different strings and the sidecar
	// is never found — which passes on Linux and fails everywhere else.
	resolved, err := filepath.EvalSymlinks(goPath)
	if err != nil {
		t.Fatal(err)
	}
	txtPath = GetDevModeTextFileName(resolved)
	if err := os.WriteFile(txtPath, []byte(strings.Join(literals, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return goPath, txtPath
}

func TestGetDevModeTextFileName(t *testing.T) {
	// The sidecar lives in the temp directory under a hash of the
	// template's absolute path, so the compiler and the runtime agree on
	// where it is without needing a shared convention about the project
	// layout.
	goPath := filepath.Join("dir", "page_ghtmx.go")
	got := GetDevModeTextFileName(goPath)

	if !strings.HasSuffix(got, ".txt") {
		t.Errorf("GetDevModeTextFileName = %q, want a .txt sidecar", got)
	}
	// Both sides derive the name independently, so it has to be stable.
	if again := GetDevModeTextFileName(goPath); again != got {
		t.Errorf("two calls disagreed: %q then %q", got, again)
	}
	// The generated file and its template must resolve to the same
	// sidecar, or a rebuild writes one and the runtime reads another.
	if fromTempl := GetDevModeTextFileName(filepath.Join("dir", "page.ghtmx")); fromTempl != got {
		t.Errorf("the template resolves to %q but the generated file to %q", fromTempl, got)
	}
	// Two different templates must not collide.
	if other := GetDevModeTextFileName(filepath.Join("dir", "other_ghtmx.go")); other == got {
		t.Error("two different templates share a sidecar path")
	}
}

func TestStringLoaderReadsLiterals(t *testing.T) {
	dir := t.TempDir()
	goPath, _ := writeSidecar(t, dir, "hello", "world")

	sl := NewStringLoader(dir)

	got, err := sl.GetWatchedString(goPath, 1, "compiled default")
	if err != nil {
		t.Fatalf("GetWatchedString: %v", err)
	}
	if got != "hello" {
		t.Errorf("literal 1 = %q, want %q — the compiled default was served instead of the edit", got, "hello")
	}

	got, err = sl.GetWatchedString(goPath, 2, "compiled default")
	if err != nil {
		t.Fatalf("GetWatchedString: %v", err)
	}
	if got != "world" {
		t.Errorf("literal 2 = %q, want %q", got, "world")
	}
}

// TestStringLoaderRejectsAnIndexPastTheEnd pins the bounds check: a
// generated file and its sidecar can fall out of step during a rebuild,
// and reading past the end has to report rather than panic.
func TestStringLoaderRejectsAnIndexPastTheEnd(t *testing.T) {
	dir := t.TempDir()
	goPath, _ := writeSidecar(t, dir, "only one")

	sl := NewStringLoader(dir)

	if _, err := sl.GetWatchedString(goPath, 99, "default"); err == nil {
		t.Error("GetWatchedString accepted an index past the end of the sidecar")
	}
}

// TestStringLoaderReportsAMissingSidecar pins that a missing file is an
// error rather than a silent fallback: silently serving the compiled
// string would make an edit appear to do nothing.
func TestStringLoaderReportsAMissingSidecar(t *testing.T) {
	dir := t.TempDir()
	goPath := filepath.Join(dir, "page_ghtmx.go")
	if err := os.WriteFile(goPath, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sl := NewStringLoader(dir)

	if _, err := sl.GetWatchedString(goPath, 1, "default"); err == nil {
		t.Error("GetWatchedString succeeded with no sidecar file present")
	}
}

// TestStringLoaderServesTheDefaultOutsideTheWatchRoot pins the scoping:
// a dependency compiled in watch mode but living outside the project
// must render its own compiled strings, not go looking for a sidecar
// that was never written.
func TestStringLoaderServesTheDefaultOutsideTheWatchRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	goPath := filepath.Join(outside, "vendored_ghtmx.go")
	if err := os.WriteFile(goPath, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sl := NewStringLoader(root)

	got, err := sl.GetWatchedString(goPath, 1, "compiled default")
	if err != nil {
		t.Fatalf("GetWatchedString: %v", err)
	}
	if got != "compiled default" {
		t.Errorf("got %q, want the compiled default for a file outside the watch root", got)
	}
}

// TestNewStringLoaderReportsABadRoot pins the constructor's error path:
// an unresolvable watch root is recorded and surfaced on first use,
// rather than silently disabling watch mode.
func TestNewStringLoaderReportsABadRoot(t *testing.T) {
	sl := NewStringLoader(filepath.Join(t.TempDir(), "no-such-directory"))

	if _, err := sl.GetWatchedString("anything_ghtmx.go", 1, "default"); err == nil {
		t.Error("a loader built on an unresolvable root reported no error")
	}
}

// TestNewStringLoaderWithNoRoot pins the disabled case: watch mode off
// means an empty root, and the loader must still be usable.
func TestNewStringLoaderWithNoRoot(t *testing.T) {
	dir := t.TempDir()
	goPath, _ := writeSidecar(t, dir, "hello")

	sl := NewStringLoader("")

	// With no root configured the loader falls back to reading whatever
	// sidecar sits beside the file.
	got, err := sl.GetWatchedString(goPath, 1, "default")
	if err != nil {
		t.Fatalf("GetWatchedString: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// TestStringLoaderPicksUpAnEditedSidecar is the property the whole
// mechanism exists for: change the text, reload, see the change. The
// cache holds entries briefly, so this waits past that window rather
// than asserting an immediate refresh.
func TestStringLoaderPicksUpAnEditedSidecar(t *testing.T) {
	dir := t.TempDir()
	goPath, txtPath := writeSidecar(t, dir, "before")

	sl := NewStringLoader(dir)
	if got, err := sl.GetWatchedString(goPath, 1, "default"); err != nil || got != "before" {
		t.Fatalf("first read = %q, %v; want %q", got, err, "before")
	}

	// Past the 100ms cache window, with a newer mtime.
	time.Sleep(150 * time.Millisecond)
	if err := os.WriteFile(txtPath, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(txtPath, future, future); err != nil {
		t.Fatal(err)
	}

	got, err := sl.GetWatchedString(goPath, 1, "default")
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if got != "after" {
		t.Errorf("second read = %q, want %q — the edit did not reach the page", got, "after")
	}
}

// TestStringLoaderServesFromCacheWithinTheWindow covers the other half:
// repeated reads inside the window must not stat the file every time, or
// a page with a thousand literals does a thousand syscalls per render.
func TestStringLoaderServesFromCacheWithinTheWindow(t *testing.T) {
	dir := t.TempDir()
	goPath, txtPath := writeSidecar(t, dir, "cached")

	sl := NewStringLoader(dir)
	if _, err := sl.GetWatchedString(goPath, 1, "default"); err != nil {
		t.Fatal(err)
	}

	// Delete the sidecar: a cached read must still succeed.
	if err := os.Remove(txtPath); err != nil {
		t.Fatal(err)
	}
	got, err := sl.GetWatchedString(goPath, 1, "default")
	if err != nil {
		t.Fatalf("a read inside the cache window hit the filesystem: %v", err)
	}
	if got != "cached" {
		t.Errorf("got %q, want the cached %q", got, "cached")
	}
}

// TestWriteStringRejectsNonGeneratedCallers pins the guard that keeps
// this out of hand-written code: the literal index only means anything
// relative to a generated file's sidecar.
func TestWriteStringRejectsNonGeneratedCallers(t *testing.T) {
	if !developmentMode {
		t.Skip("the caller check only runs in development mode")
	}
	var sb strings.Builder
	if err := WriteString(&sb, 1, "x"); err == nil {
		t.Error("WriteString accepted a call from a non-generated file")
	}
}

// TestWriteStringOutsideDevelopmentMode pins the ordinary path: without
// the dev build tag the string is written straight through.
func TestWriteStringOutsideDevelopmentMode(t *testing.T) {
	if developmentMode {
		t.Skip("this build has development mode enabled")
	}
	var sb strings.Builder
	if err := WriteString(&sb, 1, "literal"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if sb.String() != "literal" {
		t.Errorf("wrote %q, want %q", sb.String(), "literal")
	}
}
