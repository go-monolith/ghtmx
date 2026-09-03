package docsite

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the docsite source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestDocumentedInvariantsHold: the documents the site presents still
// state what the acceptance criteria name. This reads the markdown
// rather than rendered HTML — the assertions were always about the
// prose, and docs/official is what renders it now.
func TestDocumentedInvariantsHold(t *testing.T) {
	root := repoRoot(t)
	read := func(parts ...string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
		if err != nil {
			t.Fatalf("documented source missing: %v", err)
		}
		return string(raw)
	}
	assertContains := func(name, doc string, needles ...string) {
		t.Helper()
		for _, needle := range needles {
			if !strings.Contains(doc, needle) {
				t.Errorf("%s must contain %q", name, needle)
			}
		}
	}

	// The pinned version and range derive from the source of truth, so
	// a pin advance stales the page loudly instead of silently. The
	// root helper sorts numerically (a lexical sort puts 2.0.9 after
	// 2.0.10). This is the one assertion here with no substitute
	// anywhere else: it is the only thing tying prose to Go constants.
	supported := ghtmx.SupportedHtmxVersions()
	needles := []string{config.DefaultHtmxVersion, supported[0], supported[len(supported)-1]}
	// One range per family ("2.0.0 – 2.0.10", "4.0.0"): a single
	// first-to-last span would read as if every version between were
	// supported.
	needles = append(needles, htmxsurface.SupportedRanges()...)
	needles = append(needles,
		"GOOS=js GOARCH=wasm",
		"wasip1",
		"fiber",
		"Compile-time guarantee only")
	assertContains("build-targets.md", read("docs", "official", "pages", "build-targets.md"), needles...)

	// The htmx versions page enumerates every supported version by
	// hand, so a pin added to the surface without a row here stales
	// the page loudly; it also has to keep the contrast section.
	versionRows := []string{config.DefaultHtmxVersion + "` | htmx 2 | **default pin**"}
	for _, v := range supported {
		versionRows = append(versionRows, "| `"+v+"` | htmx ")
	}
	versionRows = append(versionRows,
		"## Supported versions",
		"## htmx 2.x versus 4.x",
		"## Moving a project from 2 to 4",
		"GHTMX-E0502", "GHTMX-E0501", "GHTMX-W0202",
		":inherited", "hx-status:404", "HX-Source")
	assertContains("htmx-versions.md", read("docs", "official", "pages", "htmx-versions.md"), versionRows...)

	assertContains("getting-started.md", read("docs", "official", "pages", "getting-started.md"),
		"go install github.com/go-monolith/ghtmx/cmd/ghtmx",
		"ghtmx generate",
		"HX-Request",
		"Hello, htmx!")

	// The site dogfoods a supported pin, and the versions page says
	// which — tying that sentence to docs/official/ghtmx.json keeps the
	// claim from outliving a pin change.
	var siteConfig struct {
		HtmxVersion string `json:"htmxVersion"`
	}
	if err := json.Unmarshal([]byte(read("docs", "official", "ghtmx.json")), &siteConfig); err != nil {
		t.Fatalf("docs/official/ghtmx.json: %v", err)
	}
	if !slices.Contains(supported, siteConfig.HtmxVersion) {
		t.Errorf("docs/official/ghtmx.json pins htmx %q, which is not a supported version %v", siteConfig.HtmxVersion, supported)
	}
	assertContains("htmx-versions.md", read("docs", "official", "pages", "htmx-versions.md"),
		"ghtmx.dev itself is pinned to `"+siteConfig.HtmxVersion+"`")
	// The landing page has to reach the rest of the site. It used to
	// link to <slug>.html, which only resolved in a builder that no
	// longer exists; docs/official serves /getting-started and
	// /docs/<slug>, so those are the targets to hold it to.
	assertContains("index.md", read("docs", "official", "pages", "index.md"),
		"](/getting-started)",
		"](/docs/syntax)",
		"](/docs/build-targets)")
	assertContains("SYNTAX.md", read("SYNTAX.md"), "TEMPL_SYNTAX_BASELINE")
	assertContains("DIAGNOSTICS.md", read("DIAGNOSTICS.md"), "GHTMX-E0101")
	assertContains("CONFIG.md", read("CONFIG.md"), "htmxVersion")
}

// TestSitePagesLinkToRealRoutes: a .html link target is a leftover from
// the retired builder and would 404 on ghtmx.dev, which serves clean
// paths. Nothing else catches it — the markdown renders either way.
func TestSitePagesLinkToRealRoutes(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(repoRoot(t), "docs", "official", "pages", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatal("no site pages found")
	}
	for _, page := range pages {
		raw, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range linkTargets(string(raw)) {
			if strings.HasPrefix(target, "http") {
				continue // someone else's site, someone else's URL shape
			}
			if strings.HasSuffix(target, ".html") {
				t.Errorf("%s links to %q; the site serves clean paths, not .html",
					filepath.Base(page), target)
			}
		}
	}
}

// linkTargets returns the destinations of every inline markdown link.
func linkTargets(doc string) []string {
	var targets []string
	for _, match := range linkPattern.FindAllStringSubmatch(doc, -1) {
		targets = append(targets, match[1])
	}
	return targets
}

var linkPattern = regexp.MustCompile(`\]\(([^)\s]+)`)

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
	raw, err := os.ReadFile(filepath.Join(root, "docs", "official", "pages", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)

	mainGo := fence(t, doc, "go")
	pageGhtmx := fence(t, doc, "templ")
	ghtmxJSON := fence(t, doc, "json")
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
	// The guide pins htmx 4; generation below reads this file, so the
	// walkthrough is compiled against the surface it tells the reader
	// about.
	write("ghtmx.json", ghtmxJSON)
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

	// The pin the guide wrote is the build the generated helper serves.
	var pinned struct {
		HtmxVersion string `json:"htmxVersion"`
	}
	if err := json.Unmarshal([]byte(ghtmxJSON), &pinned); err != nil || pinned.HtmxVersion == "" {
		t.Fatalf("the guide's ghtmx.json fence does not pin an htmxVersion: %v", err)
	}
	central, err := os.ReadFile(filepath.Join(dir, "ghtmxgen", "routes_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(central), `HTMXScriptTag("`+pinned.HtmxVersion+`"`) {
		t.Errorf("the guide's project does not serve the htmx %s it pins:\n%s", pinned.HtmxVersion, central)
	}
}
