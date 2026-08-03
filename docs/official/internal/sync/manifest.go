// Command sync copies the documentation sources and example sources
// this site embeds into docs/official/content/. The copies are
// committed so the module builds from a module zip; drift_test.go
// fails whenever a copy no longer matches its source of truth.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Entry maps one source-of-truth file to its committed copy under
// docs/official/content/. Both paths are absolute.
type Entry struct {
	Src string
	Dst string
}

// referenceDocs are the single-source markdown files presented on the
// site: repo-root-relative source → file name under content/docs.
// Distinct destination names disambiguate colliding base names
// (editors/README.md vs the root README.md).
var referenceDocs = []struct {
	Src string
	Dst string
}{
	{"README.md", "README.md"},
	{"SYNTAX.md", "SYNTAX.md"},
	{"DIAGNOSTICS.md", "DIAGNOSTICS.md"},
	{"CONFIG.md", "CONFIG.md"},
	{"CONFORMANCE.md", "CONFORMANCE.md"},
	{"TEMPL_SYNTAX_BASELINE.md", "TEMPL_SYNTAX_BASELINE.md"},
	{"CHANGELOG.md", "CHANGELOG.md"},
	{"CONTRIBUTING.md", "CONTRIBUTING.md"},
	{"RELEASING.md", "RELEASING.md"},
	{"editors/README.md", "editors.md"},
	{"docs/site/index.md", "index.md"},
	{"docs/site/getting-started.md", "getting-started.md"},
	{"docs/site/build-targets.md", "build-targets.md"},
}

// exampleDirs are the example applications shown on the site,
// relative to the repo root.
var exampleDirs = []string{
	"examples/hello-world",
	"examples/hx-bindings",
	"examples/fragments",
	"examples/events",
	"examples/crud",
}

// moduleRoot returns the absolute path of docs/official.
func moduleRoot() (string, error) {
	// file = <repo>/docs/official/internal/sync/manifest.go
	if _, file, _, ok := runtime.Caller(0); ok && filepath.IsAbs(file) {
		return filepath.Dir(filepath.Dir(filepath.Dir(file))), nil
	}
	// Under -trimpath the caller path is module-relative; fall back to
	// walking up from the working directory to this module's go.mod.
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(mod), "module github.com/go-monolith/ghtmx/docs/official") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("cannot locate the docs/official module root; run from within docs/official")
		}
		dir = parent
	}
}

// repoRoot returns the absolute path of the repository root.
func repoRoot() (string, error) {
	mod, err := moduleRoot()
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(mod)), nil
}

// Entries computes the full copy manifest. Example entries are
// discovered by walking the example directories, so a file added to an
// example shows up as drift instead of being silently missing.
func Entries() ([]Entry, error) {
	repo, err := repoRoot()
	if err != nil {
		return nil, err
	}
	mod, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	content := filepath.Join(mod, "content")

	var entries []Entry
	for _, doc := range referenceDocs {
		entries = append(entries, Entry{
			Src: filepath.Join(repo, filepath.FromSlash(doc.Src)),
			Dst: filepath.Join(content, "docs", doc.Dst),
		})
	}

	for _, dir := range exampleDirs {
		srcDir := filepath.Join(repo, filepath.FromSlash(dir))
		dstDir := filepath.Join(content, "examples", filepath.Base(dir))
		err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(srcDir, path)
			if err != nil {
				return err
			}
			dst, ok := copyName(rel)
			if !ok {
				return nil
			}
			entries = append(entries, Entry{Src: path, Dst: filepath.Join(dstDir, dst)})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", srcDir, err)
		}
	}
	return entries, nil
}

// copyName decides whether an example file is copied and under what
// relative name. Go and template sources gain a .txt suffix so the
// copies are invisible to go build, gofmt, and ghtmx generate;
// stylesheets need no such hiding and are copied verbatim. Generated
// pairs and tests are not shown on the site. Dot- and
// underscore-prefixed names are never copied — go:embed's plain
// patterns exclude them, so a copy would silently miss the binary.
func copyName(rel string) (string, bool) {
	slash := filepath.ToSlash(rel)
	for segment := range strings.SplitSeq(slash, "/") {
		if strings.HasPrefix(segment, ".") || strings.HasPrefix(segment, "_") {
			return "", false
		}
	}
	base := filepath.Base(slash)
	switch {
	case base == "README.md", strings.HasSuffix(base, ".css"):
		return slash, true
	case strings.HasSuffix(base, "_ghtmx.go"), strings.HasSuffix(base, "_test.go"):
		return "", false
	case strings.HasSuffix(base, ".go"), strings.HasSuffix(base, ".ghtmx"):
		return slash + ".txt", true
	default:
		return "", false
	}
}

// managedRoots returns the content subtrees the manifest owns; any
// file found there that the manifest does not produce is an orphan.
func managedRoots() ([]string, error) {
	mod, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	content := filepath.Join(mod, "content")
	return []string{filepath.Join(content, "docs"), filepath.Join(content, "examples")}, nil
}

// orphans lists files under the managed roots that are not manifest
// destinations.
func orphans(entries []Entry) ([]string, error) {
	dsts := make(map[string]bool, len(entries))
	for _, e := range entries {
		dsts[e.Dst] = true
	}
	roots, err := managedRoots()
	if err != nil {
		return nil, err
	}
	var extra []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			if !d.IsDir() && !dsts[path] {
				extra = append(extra, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return extra, nil
}
