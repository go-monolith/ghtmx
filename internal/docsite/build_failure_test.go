package docsite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Build renders the published documentation, so its failures have to
// name the page that broke. A bare "no such file" in a CI log leaves
// whoever renamed a markdown source guessing which of a dozen pages
// stopped resolving.

func TestBuildReportsAMissingSource(t *testing.T) {
	// An empty root: every page's markdown source is absent.
	err := Build(t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("Build succeeded with no markdown sources present")
	}

	// The message has to identify the page, not just the file.
	var named bool
	for _, page := range Pages {
		if strings.Contains(err.Error(), page.Slug) {
			named = true
			break
		}
	}
	if !named {
		t.Errorf("error %q names no page slug", err)
	}
}

// TestBuildReportsAnUnwritableDestination pins the other end: a
// destination that cannot be created has to fail rather than reporting a
// successful build that published nothing.
func TestBuildReportsAnUnwritableDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	if err := Build(t.TempDir(), filepath.Join(parent, "out")); err == nil {
		t.Error("Build succeeded writing under a read-only parent")
	}
}

// TestPagesAreWellFormed guards the page list itself: a duplicate slug
// would have one page silently overwrite another's output file, and an
// empty one would produce ".html".
func TestPagesAreWellFormed(t *testing.T) {
	if len(Pages) == 0 {
		t.Fatal("the page list is empty")
	}
	seen := map[string]bool{}
	for _, page := range Pages {
		if page.Slug == "" {
			t.Errorf("page %q has no slug; its output would be named .html", page.Title)
		}
		if page.Source == "" {
			t.Errorf("page %q has no source", page.Slug)
		}
		if page.Title == "" {
			t.Errorf("page %q has no title", page.Slug)
		}
		if seen[page.Slug] {
			t.Errorf("slug %q appears twice; one page would overwrite the other", page.Slug)
		}
		seen[page.Slug] = true
	}
}
