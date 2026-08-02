package watcher

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// WalkFiles decides what watch mode watches. Too wide and every build
// artifact triggers a rebuild loop; too narrow and the user edits a
// template and nothing happens. The filters are applied in a specific
// order — skip directories first, then the caller's predicate, then the
// watch pattern, then the ignore pattern — and each has to be honoured
// on its own.

// collectWalk runs WalkFiles and returns the base names it emitted.
func collectWalk(t *testing.T, root string, watch, ignore *regexp.Regexp, shouldSkip func(string) bool) []string {
	t.Helper()
	out := make(chan fsnotify.Event, 256)
	if err := WalkFiles(context.Background(), root, watch, ignore, shouldSkip, out); err != nil {
		t.Fatalf("WalkFiles: %v", err)
	}
	close(out)

	var names []string
	for e := range out {
		names = append(names, filepath.Base(e.Name))
	}
	return names
}

// project builds a tree with the shapes the filters have to distinguish.
func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"page.ghtmx":             "package p\n",
		"page_ghtmx.go":          "package p\n",
		"main.go":                "package main\n",
		"README.md":              "docs\n",
		"sub/nested.ghtmx":       "package p\n",
		"excluded/skipped.ghtmx": "package p\n",
		"node_modules/lib.ghtmx": "package p\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func contains(names []string, want string) bool { return slices.Contains(names, want) }

func TestWalkFilesHonoursTheWatchPattern(t *testing.T) {
	root := project(t)
	watch := regexp.MustCompile(`\.ghtmx$`)

	names := collectWalk(t, root, watch, nil, nil)

	if !contains(names, "page.ghtmx") {
		t.Errorf("the template at the root was not emitted: %v", names)
	}
	if !contains(names, "nested.ghtmx") {
		t.Errorf("a template in a subdirectory was not emitted: %v", names)
	}
	// Anything outside the pattern would make every unrelated save
	// trigger a rebuild.
	for _, unwanted := range []string{"main.go", "README.md", "page_ghtmx.go"} {
		if contains(names, unwanted) {
			t.Errorf("%s matched a pattern that only accepts templates: %v", unwanted, names)
		}
	}
}

func TestWalkFilesHonoursTheIgnorePattern(t *testing.T) {
	root := project(t)
	watch := regexp.MustCompile(`\.ghtmx$`)
	ignore := regexp.MustCompile(`excluded`)

	names := collectWalk(t, root, watch, ignore, nil)

	if contains(names, "skipped.ghtmx") {
		t.Errorf("an ignored path was emitted: %v", names)
	}
	if !contains(names, "page.ghtmx") {
		t.Errorf("the ignore pattern swallowed unrelated files: %v", names)
	}
}

func TestWalkFilesHonoursShouldSkip(t *testing.T) {
	root := project(t)
	watch := regexp.MustCompile(`\.ghtmx$`)

	names := collectWalk(t, root, watch, nil, func(path string) bool {
		return strings.HasPrefix(filepath.ToSlash(path), "sub")
	})

	if contains(names, "nested.ghtmx") {
		t.Errorf("a directory the predicate skipped was walked into: %v", names)
	}
	if !contains(names, "page.ghtmx") {
		t.Errorf("the predicate swallowed unrelated files: %v", names)
	}
}

// TestWalkFilesSkipsWellKnownDirectories pins the built-in skip list:
// walking node_modules is slow enough to make startup feel broken, and
// watching it can exhaust the OS watch limit.
func TestWalkFilesSkipsWellKnownDirectories(t *testing.T) {
	root := project(t)
	watch := regexp.MustCompile(`\.ghtmx$`)

	names := collectWalk(t, root, watch, nil, nil)

	if contains(names, "lib.ghtmx") {
		t.Errorf("node_modules was walked: %v", names)
	}
}

// TestWalkFilesToleratesAMissingRoot records a deliberate choice: the
// walk swallows per-entry errors and returns nil, so a root that has
// gone away produces no events rather than failing the watcher. That is
// right for a directory deleted mid-walk — a transient error should not
// kill the dev loop — but it does mean a mistyped -path watches nothing
// and says nothing, which is worth knowing when someone reports a
// watcher that never fires.
func TestWalkFilesToleratesAMissingRoot(t *testing.T) {
	out := make(chan fsnotify.Event, 8)

	err := WalkFiles(context.Background(), filepath.Join(t.TempDir(), "absent"),
		regexp.MustCompile(`\.ghtmx$`), nil, nil, out)
	if err != nil {
		t.Fatalf("WalkFiles on a missing root returned %v, want nil", err)
	}
	close(out)
	if n := len(out); n != 0 {
		t.Errorf("a missing root produced %d events, want none", n)
	}
}
