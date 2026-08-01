package docsite

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/internal/config"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the docsite source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestSiteBuilds: the site renders every page with the content the
// acceptance criteria name — the pinned htmx version and range, the
// WASM guarantee and its limits, and a getting-started flow reaching a
// rendered fragment.
func TestSiteBuilds(t *testing.T) {
	dst := t.TempDir()
	if err := Build(repoRoot(t), dst); err != nil {
		t.Fatalf("site build failed: %v", err)
	}

	pages := map[string]string{}
	for _, page := range Pages {
		raw, err := os.ReadFile(filepath.Join(dst, page.Slug+".html"))
		if err != nil {
			t.Fatalf("page %s missing: %v", page.Slug, err)
		}
		pages[page.Slug] = string(raw)
	}

	assertContains := func(slug string, needles ...string) {
		t.Helper()
		for _, needle := range needles {
			if !strings.Contains(pages[slug], needle) {
				t.Errorf("%s.html must contain %q", slug, needle)
			}
		}
	}

	assertContains("index", "getting-started.html", "syntax.html", "build-targets.html")
	assertContains("getting-started",
		"go install github.com/go-monolith/ghtmx/cmd/ghtmx",
		"ghtmx generate",
		"HX-Request",
		"Hello, htmx!")
	// The pinned version and range derive from the source of truth, so
	// a pin advance stales the page loudly instead of silently. The
	// root helper sorts numerically (a lexical sort puts 2.0.9 after
	// 2.0.10).
	supported := ghtmx.SupportedHtmxVersions()
	assertContains("build-targets",
		config.DefaultHtmxVersion,
		supported[0]+" – "+supported[len(supported)-1],
		"GOOS=js GOARCH=wasm",
		"wasip1",
		"fiber",
		"Compile-time guarantee only")
	assertContains("syntax", "TEMPL_SYNTAX_BASELINE")
	assertContains("diagnostics", "GHTMX-E0101")
	assertContains("config", "htmxVersion")
	// Cross-links between repository documents rewrite to site pages.
	assertContains("diagnostics", `href="config.html"`)
}

// fence returns the single ```info fence of the guide; requiring
// exactly one keeps an added example from silently shifting which
// snippet gets compiled and asserted.
func fence(t *testing.T, doc, info string) string {
	t.Helper()
	pattern := regexp.MustCompile("(?s)```" + info + "\n(.*?)```")
	matches := pattern.FindAllStringSubmatch(doc, -1)
	if len(matches) != 1 {
		t.Fatalf("getting-started.md must have exactly one ```%s fence, found %d — update the extraction deliberately", info, len(matches))
	}
	return matches[0][1]
}

// TestGettingStartedGuideCompilesAndRenders: the guide's own code is
// extracted, generated, built, and its promised fragment output
// rendered — the walkthrough cannot rot.
func TestGettingStartedGuideCompilesAndRenders(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a scratch module")
	}
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "site", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	mainGo := fence(t, doc, "go")
	pageGhtmx := fence(t, doc, "templ")
	expectedFragment := strings.TrimSpace(fence(t, doc, "html"))

	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", mainGo)
	write("page.ghtmx", pageGhtmx)
	// A real user gets the require via `go install` + tidy; the replace
	// stands in for @latest, everything else follows the guide's exact
	// command order: generate FIRST (it creates ghtmxgen/), then tidy.
	write("go.mod", "module example.com/hello\n\ngo 1.25.0\n\nrequire github.com/go-monolith/ghtmx v0.0.0\n\nreplace github.com/go-monolith/ghtmx => "+root+"\n")
	// The guide's promised output is asserted by rendering the actual
	// generated fragment.
	write("guide_probe_test.go", `package main

import (
	"context"
	"strings"
	"testing"
)

func TestRenderedFragment(t *testing.T) {
	var b strings.Builder
	if err := greetingFragment("htmx").RenderFragment(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	if b.String() != `+"`"+expectedFragment+"`"+` {
		t.Fatalf("the guide promises %q, the fragment renders %q", `+"`"+expectedFragment+"`"+`, b.String())
	}
}
`)

	runGo := func(args ...string) {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := generatecmd.Run(context.Background(), io.Discard, os.Stderr,
		[]string{"-path", dir, "-include-version=false"}); err != nil {
		t.Fatalf("the guide's project does not generate: %v", err)
	}
	runGo("mod", "tidy")
	runGo("test", ".")
}
