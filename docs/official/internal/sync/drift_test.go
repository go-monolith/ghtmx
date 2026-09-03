package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestContentCopiesMatchSources is the embed-drift gate: every
// committed copy under content/ must be byte-identical to its source
// of truth, and content/ must hold nothing the manifest does not own.
func TestContentCopiesMatchSources(t *testing.T) {
	entries, err := Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("empty manifest — repo layout changed under the sync tool")
	}
	for _, e := range entries {
		src, err := os.ReadFile(e.Src)
		if err != nil {
			t.Errorf("source of truth missing: %v", err)
			continue
		}
		dst, err := os.ReadFile(e.Dst)
		if err != nil {
			t.Errorf("content copy missing (run `go run ./internal/sync` from docs/official): %v", err)
			continue
		}
		if !bytes.Equal(src, dst) {
			t.Errorf("content copy %s is stale — run `go run ./internal/sync` from docs/official", e.Dst)
		}
	}
	extra, err := orphans(entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range extra {
		t.Errorf("orphan file %s — run `go run ./internal/sync` from docs/official", path)
	}
}

// TestExampleDirsMatchRepository: exampleDirs must name exactly the
// example directories that exist in the repository, so a new example
// cannot be silently invisible to the site.
func TestExampleDirsMatchRepository(t *testing.T) {
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, "examples"))
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			actual[e.Name()] = true
		}
	}
	for _, dir := range exampleDirs {
		name := filepath.Base(dir)
		if !actual[name] {
			t.Errorf("exampleDirs lists %s but examples/%s does not exist", dir, name)
		}
		delete(actual, name)
	}
	for name := range actual {
		t.Errorf("repository example examples/%s is missing from exampleDirs — add it there and to site.Examples", name)
	}
}

// TestCopyName pins the copy rules: sources gain .txt, generated and
// test files are skipped, READMEs and stylesheets pass through.
func TestCopyName(t *testing.T) {
	cases := []struct {
		rel  string
		want string
		ok   bool
	}{
		{"main.go", "main.go.txt", true},
		{"crud.ghtmx", "crud.ghtmx.txt", true},
		{"handlers/handlers.go", "handlers/handlers.go.txt", true},
		{"README.md", "README.md", true},
		{"crud.css", "crud.css", true},
		{"ghtmx.json", "ghtmx.json.txt", true},
		{"other.json", "", false},
		{"crud_ghtmx.go", "", false},
		{"main_test.go", "", false},
		{"notes.txt", "", false},
		{"_helpers.go", "", false},
		{".env.go", "", false},
		{"_private/util.go", "", false},
		{"sub/.hidden.ghtmx", "", false},
	}
	for _, tc := range cases {
		got, ok := copyName(tc.rel)
		if got != tc.want || ok != tc.ok {
			t.Errorf("copyName(%q) = (%q, %v), want (%q, %v)", tc.rel, got, ok, tc.want, tc.ok)
		}
	}
}
