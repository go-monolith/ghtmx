// Package installcheck verifies the two supported installation paths
// (NFR-010) on the running platform — the CI matrix runs it on Linux,
// macOS, and Windows — and enforces the changelog discipline: every
// breaking-section entry carries a migration note, and the pre-1.0
// stability posture is stated in the README, the changelog, and the
// documentation site.
//
// installscript_test.go covers scripts/install.sh, which is a wrapper
// around the release-archive path rather than a third path of its own:
// it is bash-only, so those tests skip on Windows.
package installcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/release"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the installcheck source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func embeddedVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".version"))
	if err != nil {
		t.Fatal(err)
	}
	return "v" + strings.TrimSpace(string(raw))
}

func binaryName() string {
	if goruntime.GOOS == "windows" {
		return "ghtmx.exe"
	}
	return "ghtmx"
}

// TestGoInstallPath: `go install ./cmd/ghtmx` produces a working binary
// that reports the embedded version.
func TestGoInstallPath(t *testing.T) {
	if testing.Short() {
		t.Skip("installs the binary")
	}
	gobin := t.TempDir()
	cmd := exec.Command("go", "install", "./cmd/ghtmx")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOBIN="+gobin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install failed: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(gobin, binaryName()), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("installed binary failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != embeddedVersion(t) {
		t.Errorf("installed binary reports %q, want %q", got, embeddedVersion(t))
	}
}

// TestReleaseBinaryPath: the release archive for the running platform
// extracts to a working binary reporting the same version — the second
// supported installation path.
func TestReleaseBinaryPath(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and extracts a release archive")
	}
	root := repoRoot(t)
	version := embeddedVersion(t)
	dst := t.TempDir()
	target := release.Target{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH}
	archive, err := release.BuildOne(root, dst, strings.TrimPrefix(version, "v"), target)
	if err != nil {
		t.Fatalf("release archive build failed: %v", err)
	}

	extracted := t.TempDir()
	if err := release.Extract(filepath.Join(dst, archive), extracted); err != nil {
		t.Fatalf("archive extraction failed: %v", err)
	}
	out, err := exec.Command(filepath.Join(extracted, binaryName()), "version").CombinedOutput()
	if err != nil {
		t.Fatalf("extracted binary failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != version {
		t.Errorf("release binary reports %q, want %q", got, version)
	}
	for _, extra := range []string{"LICENSE", "NOTICE", "README.md"} {
		if _, err := os.Stat(filepath.Join(extracted, extra)); err != nil {
			t.Errorf("archive must ship %s: %v", extra, err)
		}
	}
}

// TestChangelogDiscipline: every entry in a breaking section carries a
// migration note, and the pre-1.0 posture is stated where the AC says.
func TestChangelogDiscipline(t *testing.T) {
	root := repoRoot(t)
	read := func(parts ...string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatalf("required file missing: %v", err)
		}
		return string(raw)
	}

	changelog := read("CHANGELOG.md")
	// Release boundaries ("## [x.y.z]") end any section: without the
	// outer split, a Removed section would bleed into the next release
	// and misattribute its bullets.
	breakingSection := regexp.MustCompile(`(?i)^### (Changed|Removed|Breaking)`)
	for _, releaseBlock := range strings.Split(changelog, "\n## ") {
		for i, section := range strings.Split(releaseBlock, "\n### ") {
			if i == 0 {
				continue // preamble before the first ### heading
			}
			heading := "### " + section
			if !breakingSection.MatchString(heading) {
				continue
			}
			// Each top-level bullet in a breaking section must carry a
			// migration note ("Migration: none required" counts).
			for _, entry := range strings.Split(heading, "\n- ")[1:] {
				if !strings.Contains(entry, "Migration:") {
					summary := strings.SplitN(entry, "\n", 2)[0]
					t.Errorf("breaking changelog entry %q has no Migration: note", strings.TrimSpace(summary))
				}
			}
		}
	}

	for _, doc := range []struct{ name, path, needle string }{
		{"CHANGELOG.md", "CHANGELOG.md", "Pre-1.0 stability posture"},
		{"README.md", "README.md", "pre-1.0"},
		{"docs site index", filepath.Join("docs", "official", "pages", "index.md"), "pre-1.0"},
		{"README.md install", "README.md", "go install github.com/go-monolith/ghtmx/cmd/ghtmx"},
		{"README.md checksums", "README.md", "checksums.txt"},
	} {
		if !strings.Contains(read(strings.Split(doc.path, string(os.PathSeparator))...), doc.needle) {
			t.Errorf("%s must contain %q", doc.name, doc.needle)
		}
	}
}
