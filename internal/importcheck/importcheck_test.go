// Package importcheck holds the NFR-012 import-isolation gates: the
// runtime an application imports must stay standard-library only, and
// framework dependencies must never leave adapters/. The checks run as
// ordinary tests so every build executes them, and CI surfaces them as
// a named lint step so the gate cannot silently vanish.
package importcheck

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/generator"
)

// runtimeClosure is the exact in-module package set the app-importable
// packages may pull in. Growing it is a deliberate, reviewed decision —
// add a package here only if it belongs to what an application ships.
var runtimeClosure = map[string]bool{
	"github.com/go-monolith/ghtmx":                   true,
	"github.com/go-monolith/ghtmx/runtime":           true,
	"github.com/go-monolith/ghtmx/internal/safehtml": true,
	"github.com/go-monolith/ghtmx/adapters/nethttp":  true,
	"github.com/go-monolith/ghtmx/auth":              true,
}

// frameworkPrefixes are the module paths of the supported frameworks
// (and their engines: fiber's fasthttp, martini's inject). They may be
// imported only under adapters/.
var frameworkPrefixes = []string{
	"github.com/go-chi/",
	"github.com/labstack/echo",
	"github.com/gin-gonic/",
	"github.com/gofiber/",
	"github.com/valyala/fasthttp",
	"github.com/beego/",
	"github.com/kataras/",
	"github.com/revel/",
	"github.com/go-martini/",
	"github.com/codegangsta/inject",
}

// TestRuntimeTransitiveImportsAreStdlibOnly: NFR-012 — an application
// importing the runtime packages must pull in nothing beyond the
// standard library and the runtime itself. Tooling (CLI, LSP, dev
// server) and framework adapters must never enter this closure. The
// roots are exactly what generated code imports — taken from the
// generator so a new emitted import cannot escape the gate — plus the
// nethttp adapter, the one adapter that ships in the core module and is
// therefore app-importable.
func TestRuntimeTransitiveImportsAreStdlibOnly(t *testing.T) {
	for _, root := range generator.RuntimeImports {
		if !runtimeClosure[root] {
			t.Errorf("generated code imports %s, which the closure allowlist does not cover — the gate would miss its dependencies", root)
		}
	}
	roots := append([]string{}, generator.RuntimeImports...)
	roots = append(roots, "github.com/go-monolith/ghtmx/adapters/nethttp")
	// The auth package is app-importable like the nethttp adapter, and
	// its net/http middleware ships in the core module — it must stay
	// stdlib-only forever.
	roots = append(roots, "github.com/go-monolith/ghtmx/auth")

	args := append([]string{"list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}"}, roots...)
	cmd := exec.Command("go", args...)
	// Hermetic against developer workspaces: a go.work would change what
	// go list resolves.
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		detail := ""
		if errors.As(err, &exitErr) {
			detail = string(exitErr.Stderr)
		}
		t.Fatalf("go list failed: %v\n%s", err, detail)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !runtimeClosure[pkg] {
			t.Errorf("runtime closure gained non-stdlib package %s; the runtime must stay stdlib-only (NFR-012)", pkg)
		}
	}
}

// TestFrameworkImportsOnlyInAdapters: framework packages may be
// imported only where framework dependencies are sanctioned — the
// nested modules (adapters and WASM fixtures), which govern their own
// go.mod and cannot leak into the core module's graph (the core go.mod
// check guards the indirect route). Everything in the core module —
// runtime, compiler, tooling, examples, and the root-module nethttp
// adapter — must stay framework-free. Source-only fixture trees
// (testdata) are skipped: the syntax-only route discovery fixtures name
// frameworks without ever linking them.
func TestFrameworkImportsOnlyInAdapters(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			if rel != "." {
				if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, _ := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if file == nil {
			// Not even partially parseable: such a file cannot compile,
			// so it cannot link a framework either.
			return nil
		}
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			for _, prefix := range frameworkPrefixes {
				if strings.HasPrefix(importPath, prefix) {
					t.Errorf("%s imports %s: framework imports belong under adapters/ only", rel, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCoreModuleRequiresNoFrameworks: the framework adapters live in
// nested modules precisely so the core go.mod stays framework-free; a
// framework require sneaking in — direct or indirect — means that
// isolation broke.
func TestCoreModuleRequiresNoFrameworks(t *testing.T) {
	gomod, err := os.ReadFile(filepath.Join(moduleRoot(t), "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(gomod), "\n") {
		for _, prefix := range frameworkPrefixes {
			if strings.Contains(line, prefix) {
				t.Errorf("core go.mod references %q: framework modules belong to the nested adapter modules", strings.TrimSpace(line))
			}
		}
	}
}

// moduleRoot is the core module's directory. The test's working
// directory is its package directory, two levels below the root —
// hermetic against go.work and GOFLAGS, unlike asking the go tool.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
