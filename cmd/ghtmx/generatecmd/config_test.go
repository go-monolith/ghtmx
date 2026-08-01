package generatecmd

import (
	"bytes"
	"context"
	"github.com/fsnotify/fsnotify"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/modcheck"
	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/routes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTemplate = `package main

templ hello() {
	<div>Hello</div>
}
`

// ghtmxModuleRoot resolves the repository root for scratch-module
// replace directives — never hardcode the checkout path.
func ghtmxModuleRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := modcheck.WalkUp(wd)
	if err != nil {
		t.Fatalf("cannot locate the ghtmx go.mod: %v", err)
	}
	return root
}

func writeFile(t testing.TB, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestArgumentsResolveDefaultsWithoutConfigFile(t *testing.T) {
	t.Chdir(t.TempDir())
	args, _, _, err := NewArguments(io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatalf("a conventional project must load on defaults: %v", err)
	}
	if args.Config.HtmxVersion != "2.0.10" {
		t.Errorf("HtmxVersion = %q", args.Config.HtmxVersion)
	}
	if args.Config.GeneratedSuffix != "_ghtmx.go" {
		t.Errorf("GeneratedSuffix = %q", args.Config.GeneratedSuffix)
	}
	if len(args.SourceDirs) != 1 || args.SourceDirs[0] != "." {
		t.Errorf("SourceDirs = %v", args.SourceDirs)
	}
}

func TestFlagWinsOverConfigFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ghtmx.json", `{"htmxVersion": "2.0.0", "generatedSuffix": "_file.go"}`)
	t.Chdir(dir)

	args, _, _, err := NewArguments(io.Discard, io.Discard, []string{
		"-htmx-version", "2.0.10",
		"-generated-suffix", "_flag.go",
		"-strict-targets",
	})
	if err != nil {
		t.Fatal(err)
	}
	if args.Config.HtmxVersion != "2.0.10" {
		t.Errorf("flag must beat file: HtmxVersion = %q", args.Config.HtmxVersion)
	}
	if args.Config.GeneratedSuffix != "_flag.go" {
		t.Errorf("flag must beat file: GeneratedSuffix = %q", args.Config.GeneratedSuffix)
	}
	if !args.Config.StrictTargets {
		t.Error("-strict-targets must apply")
	}
}

func TestConfigFileValuesApplyWithoutFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ghtmx.json", `{"sourceDirs": ["ui", "pages"], "generatedPackage": {"dir": "gen", "name": "gen"}}`)
	t.Chdir(dir)

	args, _, _, err := NewArguments(io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(args.SourceDirs) != 2 || args.SourceDirs[0] != "ui" || args.SourceDirs[1] != "pages" {
		t.Errorf("SourceDirs = %v", args.SourceDirs)
	}
	if args.Config.GeneratedPackage.Dir != "gen" {
		t.Errorf("GeneratedPackage = %+v", args.Config.GeneratedPackage)
	}
}

func TestExplicitPathNarrowsSourceDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ghtmx.json", `{"sourceDirs": ["ui", "pages"]}`)
	t.Chdir(dir)

	args, _, _, err := NewArguments(io.Discard, io.Discard, []string{"-path", "only"})
	if err != nil {
		t.Fatal(err)
	}
	if len(args.SourceDirs) != 1 || args.SourceDirs[0] != "only" {
		t.Errorf("an explicit -path must narrow generation, got %v", args.SourceDirs)
	}
}

func TestInvalidConfigFileIsPositionedError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ghtmx.json", "{\n\t\"htmxVerison\": \"2.0.10\"\n}")
	t.Chdir(dir)

	_, _, _, err := NewArguments(io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), `"htmxVerison"`) || !strings.Contains(err.Error(), "ghtmx.json:2:") {
		t.Fatalf("expected a positioned error naming the key, got %v", err)
	}
}

func TestUnsupportedHtmxVersionRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	_, _, _, err := NewArguments(io.Discard, io.Discard, []string{"-htmx-version", "1.9.12"})
	if err == nil || !strings.Contains(err.Error(), "2.0.0") {
		t.Fatalf("expected an unsupported-version error naming the range, got %v", err)
	}
}

func TestCheckSeverityFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	args, _, _, err := NewArguments(io.Discard, io.Discard, []string{"-check-severity", "GHTMX-W0101=off"})
	if err != nil {
		t.Fatal(err)
	}
	if args.Config.Checks["GHTMX-W0101"] != "off" {
		t.Errorf("Checks = %v", args.Config.Checks)
	}
	// Malformed values are flag parse errors; unknown IDs fail validation.
	if _, _, _, err := NewArguments(io.Discard, io.Discard, []string{"-check-severity", "nonsense"}); err == nil {
		t.Error("expected an error for a malformed -check-severity value")
	}
	if _, _, _, err := NewArguments(io.Discard, io.Discard, []string{"-check-severity", "GHTMX-X9999=off"}); err == nil {
		t.Error("expected an error for an unknown check ID")
	}
}

func TestGenerateWithCustomSuffix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/suffix\n\ngo 1.25\n")
	writeFile(t, dir, "hello.ghtmx", testTemplate)
	t.Chdir(dir)

	err := Run(context.Background(), io.Discard, io.Discard, []string{"-generated-suffix", "_gen.go", "-include-version=false"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello_gen.go")); err != nil {
		t.Errorf("expected hello_gen.go to be generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello_ghtmx.go")); err == nil {
		t.Error("default-suffix file must not be written when a custom suffix is configured")
	}
}

func TestGenerateWalksAllConfiguredSourceDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/multi\n\ngo 1.25\n")
	writeFile(t, dir, "ghtmx.json", `{"sourceDirs": ["ui", "pages"]}`)
	writeFile(t, dir, "ui/a.ghtmx", testTemplate)
	writeFile(t, dir, "pages/b.ghtmx", testTemplate)
	writeFile(t, dir, "skipped/c.ghtmx", testTemplate)
	t.Chdir(dir)

	err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"})
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	for _, want := range []string{"ui/a_ghtmx.go", "pages/b_ghtmx.go"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("expected %s to be generated: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "skipped/c_ghtmx.go")); err == nil {
		t.Error("directories outside sourceDirs must not be generated")
	}
}

func TestGenerateFailsOnInvalidHxAttributes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/attrs\n\ngo 1.25\n")
	writeFile(t, dir, "bad.ghtmx", `package main

templ page() {
	<button hx-pots="/x" hx-swap="bogus">Go</button>
}
`)
	t.Chdir(dir)

	err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"})
	if err == nil {
		t.Fatal("expected generate to fail on invalid hx-* attributes")
	}
	// Generation of the failing file is skipped (constitution P2).
	if _, statErr := os.Stat(filepath.Join(dir, "bad_ghtmx.go")); statErr == nil {
		t.Error("no output must be written for a file with attribute errors")
	}
}

func TestGenerateSucceedsWithValidHxAttributes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/attrs\n\ngo 1.25\n")
	writeFile(t, dir, "good.ghtmx", `package main

templ page() {
	<button hx-swap="outerHTML" hx-target="#out" hx-trigger="click">Go</button>
}
`)
	t.Chdir(dir)

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("valid hx-* attributes must not fail generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "good_ghtmx.go")); err != nil {
		t.Errorf("expected generated output: %v", err)
	}
}

func TestGenerateWarningsAloneDoNotFail(t *testing.T) {
	// Valid attributes with strict-targets on still succeed (no targets
	// are dangling here); the W0101 warning case is
	// TestGenerateUnusedFragmentWarnsButSucceeds.
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/warn\n\ngo 1.25\n")
	writeFile(t, dir, "ok.ghtmx", `package main

templ page() {
	<div hx-trigger="click" hx-target="#present"></div>
	<div id="present"></div>
}
`)
	t.Chdir(dir)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false", "-strict-targets"}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
}

const bindingHandlers = `package handlers

import "net/http"

func CreateUser(w http.ResponseWriter, r *http.Request) {}
func ListUsers(w http.ResponseWriter, r *http.Request)  {}
`

const bindingRoutes = `package main

import (
	"net/http"

	"example.com/bind/handlers"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", handlers.CreateUser)
	mux.HandleFunc("GET /users", handlers.ListUsers)
	_ = mux
}
`

func TestGenerateResolvesSymbolBindings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/bind\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", bindingRoutes)
	writeFile(t, dir, "handlers/handlers.go", bindingHandlers)
	writeFile(t, dir, "page.ghtmx", `package main

import "example.com/bind/handlers"

templ page() {
	<button hx-post={ handlers.CreateUser } hx-target="#out">Create</button>
}
`)
	t.Chdir(dir)

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "page_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	// The handler's registered path is emitted as a static attribute value.
	if !strings.Contains(string(generated), `hx-post=\"/users\"`) {
		t.Errorf("expected the binding to emit the registered path as a static literal, got:\n%s", generated)
	}
	// The expression is folded out of the render path: no runtime call
	// remains. The symbol survives only as a compile-checked blank
	// reference, so renaming the handler still breaks this file.
	if strings.Contains(string(generated), "ResolveAttributeValue") {
		t.Errorf("no runtime attribute resolution must remain for the binding, got:\n%s", generated)
	}
	if !strings.Contains(string(generated), "_ = handlers.CreateUser") {
		t.Errorf("expected a compile-checked reference to the bound handler, got:\n%s", generated)
	}
}

func TestGenerateFailsOnUnknownHandlerBinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/bind\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", bindingRoutes)
	writeFile(t, dir, "handlers/handlers.go", bindingHandlers)
	writeFile(t, dir, "page.ghtmx", `package main

import "example.com/bind/handlers"

templ page() {
	<button hx-post={ handlers.Missing }>x</button>
}
`)
	t.Chdir(dir)

	err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"})
	if err == nil {
		t.Fatal("expected generate to fail on an unknown handler binding")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "page_ghtmx.go")); statErr == nil {
		t.Error("no output must be written for a file with binding errors")
	}
}

func TestGenerateFailsOnVerbMismatchBinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/bind\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", bindingRoutes)
	writeFile(t, dir, "handlers/handlers.go", bindingHandlers)
	writeFile(t, dir, "page.ghtmx", `package main

import "example.com/bind/handlers"

templ page() {
	<button hx-get={ handlers.CreateUser }>x</button>
}
`)
	t.Chdir(dir)

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err == nil {
		t.Fatal("expected generate to fail on a verb mismatch")
	}
}

// TestConstructorEndToEndCompile is the FR-021 acceptance proof: a module
// with a parameterised route generates a typed constructor; a template
// calling it compiles, produces a correctly-substituted URL, and a
// wrong-arity call is a Go compile error at the call site.
func TestConstructorEndToEndCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", `module example.com/ctor

go 1.25

require github.com/go-monolith/ghtmx v0.0.0

replace github.com/go-monolith/ghtmx => `+ghtmxModuleRoot(t)+`
`)
	writeFile(t, dir, "main.go", `package main

import (
	"net/http"

	"example.com/ctor/handlers"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", handlers.GetUser)
	_ = mux
}
`)
	writeFile(t, dir, "handlers/handlers.go", `package handlers

import "net/http"

func GetUser(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, dir, "page.ghtmx", `package main

import "example.com/ctor/ghtmxgen"

templ userLink(id string) {
	<a hx-get={ ghtmxgen.GetUser(id) } hx-target="#detail">User</a>
}
`)
	t.Chdir(dir)

	// The module graph (require + local replace) needs sums resolved.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "ghtmxgen", "routes_ghtmx.go"))
	if err != nil {
		t.Fatalf("central package missing: %v", err)
	}
	if !strings.Contains(string(generated), "func GetUser(id string) ghtmx.SafeURL") {
		t.Fatalf("expected the typed constructor, got:\n%s", generated)
	}

	// The generated files import ghtmx; resolve the requirement via the
	// local replace.
	tidy = exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("post-generation go mod tidy failed: %v\n%s", err, out)
	}

	// The whole module compiles: constructor call site type-checks.
	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("module with a constructor call must compile: %v\n%s", err, out)
	}

	// The constructor substitutes and escapes the parameter.
	writeFile(t, dir, "urlcheck_test.go", `package main

import (
	"testing"

	"example.com/ctor/ghtmxgen"
)

func TestURL(t *testing.T) {
	if got := string(ghtmxgen.GetUser("4 2")); got != "/users/4%202" {
		t.Errorf("got %q", got)
	}
}
`)
	testRun := exec.Command("go", "test", "-run", "TestURL", ".")
	testRun.Dir = dir
	if out, err := testRun.CombinedOutput(); err != nil {
		t.Fatalf("URL substitution check failed: %v\n%s", err, out)
	}

	// Wrong arity at a Go call site is a compile error (D9).
	writeFile(t, dir, "badarity.go", `package main

import "example.com/ctor/ghtmxgen"

var bad = ghtmxgen.GetUser("a", "b")
`)
	badBuild := exec.Command("go", "build", "./...")
	badBuild.Dir = dir
	if out, err := badBuild.CombinedOutput(); err == nil {
		t.Fatalf("wrong constructor arity must fail to compile:\n%s", out)
	}
	if err := os.Remove(filepath.Join(dir, "badarity.go")); err != nil {
		t.Fatal(err)
	}

	// Re-pathing the route regenerates the constructor and breaks stale
	// call sites: the template still references GetUser, whose route now
	// has two parameters.
	writeFile(t, dir, "main.go", `package main

import (
	"net/http"

	"example.com/ctor/handlers"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/{org}/users/{id}", handlers.GetUser)
	_ = mux
}
`)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err == nil {
		t.Fatal("expected the stale one-argument template call site to fail analysis after re-pathing")
	}
}

func TestDanglingTargetIsWarningByDefaultAndErrorUnderStrict(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/targets\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", `package main

templ page() {
	<button hx-trigger="click" hx-target="#nowhere">Go</button>
}
`)
	t.Chdir(dir)

	// Default: a warning only — the exit code is unaffected (FR-042).
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("a dangling target must not fail the build by default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "page_ghtmx.go")); err != nil {
		t.Errorf("output must still be generated: %v", err)
	}

	// Strict mode promotes the warning to an error.
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false", "-strict-targets"}); err == nil {
		t.Fatal("strict mode must fail the build on a dangling target")
	}
}

func TestUnboundRouteIsWarningOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/unbound\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", `package main

import "net/http"

func orphan(w http.ResponseWriter, r *http.Request) {}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orphan", orphan)
	_ = mux
}
`)
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("an unbound route must not fail the build: %v", err)
	}
}

// TestGenerateUnusedFragmentWarnsButSucceeds: FR-033 — GHTMX-W0101 names
// the declaration site and does not fail the build.
func TestGenerateUnusedFragmentWarnsButSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/orphan\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", `package main

fragment Orphan(x string) {
	<tr><td>{ x }</td></tr>
}

templ page() {
	<p>nothing references Orphan</p>
}
`)
	t.Chdir(dir)
	var stderr bytes.Buffer
	if err := Run(context.Background(), io.Discard, &stderr, []string{"-include-version=false"}); err != nil {
		t.Fatalf("a warning must not fail generation, got %v", err)
	}
	log := stderr.String()
	if !strings.Contains(log, "GHTMX-W0101") || !strings.Contains(log, "Orphan") || !strings.Contains(log, "page.ghtmx:3") {
		t.Errorf("expected a W0101 warning naming the declaration site, got:\n%s", log)
	}
}

// TestGenerateReferenceCycleFails: FR-053 — GHTMX-E0306 lists the chain
// and fails generation.
func TestGenerateReferenceCycleFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/cycle\n\ngo 1.25\n")
	writeFile(t, dir, "pages.ghtmx", `package main

templ pageA() {
	@pageB()
}

templ pageB() {
	@pageA()
}
`)
	t.Chdir(dir)
	var stderr bytes.Buffer
	if err := Run(context.Background(), io.Discard, &stderr, []string{"-include-version=false"}); err == nil {
		t.Fatal("a reference cycle must fail generation")
	}
	log := stderr.String()
	if !strings.Contains(log, "GHTMX-E0306") || !strings.Contains(log, "pageA -> pageB -> pageA") {
		t.Errorf("expected an E0306 error listing the chain, got:\n%s", log)
	}
}

// TestGenerateDuplicateEventFails: FR-037 — a duplicate event name across
// the compiled set is a compile error naming both declarations.
func TestGenerateDuplicateEventFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/events\n\ngo 1.25\n")
	writeFile(t, dir, "a.ghtmx", `package main

event UserCreated(id string)

templ page() {
	<div hx-on:user-created="refresh()"></div>
}
`)
	writeFile(t, dir, "b.ghtmx", `package main

event UserCreated(id string)
`)
	t.Chdir(dir)
	var stderr bytes.Buffer
	if err := Run(context.Background(), io.Discard, &stderr, []string{"-include-version=false"}); err == nil {
		t.Fatal("a duplicate event must fail generation")
	}
	log := stderr.String()
	if !strings.Contains(log, "GHTMX-E0305") || !strings.Contains(log, "a.ghtmx:3") || !strings.Contains(log, "b.ghtmx:3") {
		t.Errorf("expected an E0305 naming both declarations, got:\n%s", log)
	}
}

// TestGenerateUndeclaredEventReferenceFails: FR-037 — a template-side
// reference to an undeclared event is a compile error naming the event.
func TestGenerateUndeclaredEventReferenceFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/events\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", `package main

templ page() {
	<div hx-on:user-created="refresh()"></div>
}
`)
	t.Chdir(dir)
	var stderr bytes.Buffer
	if err := Run(context.Background(), io.Discard, &stderr, []string{"-include-version=false"}); err == nil {
		t.Fatal("an undeclared event reference must fail generation")
	}
	log := stderr.String()
	if !strings.Contains(log, "GHTMX-E0304") || !strings.Contains(log, `"user-created"`) {
		t.Errorf("expected an E0304 naming the event, got:\n%s", log)
	}
}

// TestGenerateUnreferencedEventWarnsButSucceeds: FR-037 — a declared but
// unreferenced event warns without failing the build.
func TestGenerateUnreferencedEventWarnsButSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/events\n\ngo 1.25\n")
	writeFile(t, dir, "events.ghtmx", `package main

event CartCleared()

templ page() {
	<p>nothing listens</p>
}
`)
	t.Chdir(dir)
	var stderr bytes.Buffer
	if err := Run(context.Background(), io.Discard, &stderr, []string{"-include-version=false"}); err != nil {
		t.Fatalf("a warning must not fail generation, got %v", err)
	}
	log := stderr.String()
	if !strings.Contains(log, "GHTMX-W0102") || !strings.Contains(log, "CartCleared") {
		t.Errorf("expected a W0102 warning naming the event, got:\n%s", log)
	}
}

// TestEventContractEndToEndCompile: FR-037's structural enforcement. A
// declared event's emitter compiles and merges headers; an undeclared
// event fails Go compilation as an undefined identifier; a payload type
// mismatch is a Go compile error at the call site.
func TestEventContractEndToEndCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a module")
	}
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", `module example.com/events

go 1.25

require github.com/go-monolith/ghtmx v0.0.0

replace github.com/go-monolith/ghtmx => `+ghtmxModuleRoot(t)+`
`)
	writeFile(t, dir, "page.ghtmx", `package main

event ItemSaved(id string)

event CartCleared()

templ page() {
	<div hx-on:item-saved="refresh()" hx-trigger="cart-cleared from:body"></div>
}
`)
	writeFile(t, dir, "main.go", `package main

func main() {}
`)
	t.Chdir(dir)

	runTidy := func(stage string) {
		t.Helper()
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = dir
		if out, err := tidy.CombinedOutput(); err != nil {
			t.Fatalf("%s go mod tidy failed: %v\n%s", stage, err, out)
		}
	}
	runTidy("pre-generation")
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	// With the emitters generated, handler code can call them.
	writeFile(t, dir, "main.go", `package main

import (
	"net/http"

	"example.com/events/ghtmxgen"
)

func save(w http.ResponseWriter, r *http.Request) {
	_ = ghtmxgen.EmitItemSaved(w, ghtmxgen.ItemSavedPayload{Id: "1"})
	_ = ghtmxgen.EmitCartCleared(w)
}

func main() {
	http.HandleFunc("POST /save", save)
}
`)
	runTidy("post-generation")

	build := exec.Command("go", "build", "./...")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("declared emitters must compile: %v\n%s", err, out)
	}

	// AC: emitting an undeclared event is an undefined identifier.
	writeFile(t, dir, "undeclared.go", `package main

import (
	"net/http"

	"example.com/events/ghtmxgen"
)

func bad(w http.ResponseWriter, r *http.Request) {
	_ = ghtmxgen.EmitNeverDeclared(w)
}
`)
	build = exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "undefined") || !strings.Contains(string(out), "EmitNeverDeclared") {
		t.Fatalf("an undeclared event must fail as an undefined identifier, got err=%v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(dir, "undeclared.go")); err != nil {
		t.Fatal(err)
	}

	// AC: a payload type mismatch is a compile error at the call site.
	writeFile(t, dir, "mismatch.go", `package main

import (
	"net/http"

	"example.com/events/ghtmxgen"
)

func alsoBad(w http.ResponseWriter, r *http.Request) {
	_ = ghtmxgen.EmitItemSaved(w, "not a payload struct")
}
`)
	build = exec.Command("go", "build", "./...")
	build.Dir = dir
	out, err = build.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot use") {
		t.Fatalf("a payload type mismatch must be a compile error, got err=%v\n%s", err, out)
	}
}

// TestRoutelessProjectGetsHTMXScript: FR-091 — a fresh project with no
// routes and no events still gets the central package, because the
// configured htmx version makes HTMXScript() content.
func TestRoutelessProjectGetsHTMXScript(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/plain\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "ghtmxgen", "routes_ghtmx.go"))
	if err != nil {
		t.Fatalf("central package missing in a routeless project: %v", err)
	}
	if !strings.Contains(string(generated), `ghtmx.HTMXScriptTag("2.0.10"`) {
		t.Errorf("HTMXScript must bake the configured version:\n%s", generated)
	}
}

// TestBuildCacheAcrossRuns: D6/NFR-001 — a second run (a new process's
// worth of state) serves unchanged units from the on-disk cache, and
// identical content in two files stays unit-distinct because the file
// name participates in the key.
func TestBuildCacheAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GHTMX_CACHE_DIR", filepath.Join(dir, "ghtmx-cache"))
	// Test binaries are devel builds with no stable toolchain identity;
	// pin one so the cache engages.
	t.Setenv("GHTMX_BUILD_CACHE_SALT", "test-toolchain")
	writeFile(t, dir, "go.mod", "module example.com/cache\n\ngo 1.25\n")
	// A template with an expression embeds the file name in its error
	// handlers, making the two outputs unit-distinct.
	const exprTemplate = "package main\n\ntempl block(name string) {\n\t<p>{ name }</p>\n}\n"
	writeFile(t, dir, "a.ghtmx", strings.ReplaceAll(exprTemplate, "block", "blockA"))
	writeFile(t, dir, "b.ghtmx", strings.ReplaceAll(exprTemplate, "block", "blockB"))
	t.Chdir(dir)

	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("first run failed: %v", err)
	}
	firstA, err := os.ReadFile(filepath.Join(dir, "a_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	firstB, err := os.ReadFile(filepath.Join(dir, "b_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstA), "`a.ghtmx`") || string(firstA) == string(firstB) {
		t.Fatal("the file name must be embedded, keeping units distinct")
	}

	// Deleting the output makes the hit load-bearing: only the cached
	// payload (or regeneration) can restore it.
	if err := os.Remove(filepath.Join(dir, "a_ghtmx.go")); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Run(context.Background(), io.Discard, &log, []string{"-include-version=false"}); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if !strings.Contains(log.String(), "Build cache") || strings.Contains(log.String(), "hits=0") {
		t.Errorf("the second run must report cache hits, got:\n%s", log.String())
	}
	secondA, err := os.ReadFile(filepath.Join(dir, "a_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstA) != string(secondA) {
		t.Error("cached output must be byte-identical to generated output")
	}
}

// TestHandleDependentBypassesFreshnessGate: FR-061 tier two — a dependent
// unit's own mtime has not advanced, so HandleEvent skips it; the
// dependent path must re-process it anyway.
func TestHandleDependentBypassesFreshnessGate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/dep\n\ngo 1.25\n")
	target := writeFile(t, dir, "page.ghtmx", "package main\n\nfragment Row(x string) {\n\t<tr><td>{ x }</td></tr>\n}\n\ntempl page(x string) {\n\t@Row(x)\n}\n")
	t.Chdir(dir)

	sa := analyzer.NewSetAnalysis()
	fseh := NewFSEventHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		dir, false, nil, false, false, FileWriter, false,
		WithSetAnalysis(sa),
	)
	ctx := context.Background()
	if _, err := fseh.HandleEvent(ctx, fsnotify.Event{Name: target, Op: fsnotify.Create}); err != nil {
		t.Fatal(err)
	}
	if len(sa.FileDependencyFacts()[target].Decls) == 0 {
		t.Fatal("first pass must collect facts")
	}

	// Change the content but keep the mtime in the past: a plain event is
	// skipped, so the facts stay stale.
	old := time.Now().Add(-time.Hour)
	writeFile(t, dir, "page.ghtmx", "package main\n\ntempl page(x string) {\n\t<p>{ x }</p>\n}\n")
	if err := os.Chtimes(target, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := fseh.HandleEvent(ctx, fsnotify.Event{Name: target, Op: fsnotify.Write}); err != nil {
		t.Fatal(err)
	}
	if len(sa.FileDependencyFacts()[target].Decls) != 2 {
		t.Fatalf("the plain event must be mtime-skipped, got %v", sa.FileDependencyFacts()[target].Decls)
	}

	// The dependent path resets the gate and re-processes.
	if _, err := fseh.HandleDependent(ctx, target); err != nil {
		t.Fatal(err)
	}
	if got := sa.FileDependencyFacts()[target].Decls; len(got) != 1 {
		t.Fatalf("HandleDependent must re-analyze, got %v", got)
	}
}

// TestUpdateRouteBindingsSwapsLowering: FR-061 tier one — after a
// re-discovery swap, analysis resolves symbol bindings against the new
// route table.
func TestUpdateRouteBindingsSwapsLowering(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/swap\n\ngo 1.25\n")
	target := writeFile(t, dir, "page.ghtmx", "package main\n\ntempl page() {\n\t<button hx-get={ list }>Go</button>\n}\n")
	t.Chdir(dir)

	handlerRef := routes.SymbolRef{PkgPath: "example.com/swap", Name: "list"}
	oldTable := routes.NewTable()
	oldTable.Add(routes.Route{Verb: routes.GET, Path: "/v1", Handler: handlerRef})
	oldNames, _ := central.Naming(oldTable)

	fseh := NewFSEventHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		dir, false, nil, false, false, FileWriter, false,
		WithSetAnalysis(analyzer.NewSetAnalysis()),
		attributeValidationOption(slog.New(slog.NewTextHandler(io.Discard, nil)), config.Default()),
		WithRouteBindings(oldTable, "example.com/swap", "ghtmxgen", oldNames),
	)
	ctx := context.Background()
	if _, err := fseh.HandleEvent(ctx, fsnotify.Event{Name: target, Op: fsnotify.Create}); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(dir, "page_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `hx-get=\"/v1\"`) {
		t.Fatalf("initial binding must lower to /v1, got:\n%s", generated)
	}

	newTable := routes.NewTable()
	newTable.Add(routes.Route{Verb: routes.GET, Path: "/v2", Handler: handlerRef})
	newNames, _ := central.Naming(newTable)
	fseh.UpdateRouteBindings(newTable, newNames)

	if _, err := fseh.HandleDependent(ctx, target); err != nil {
		t.Fatal(err)
	}
	generated, err = os.ReadFile(filepath.Join(dir, "page_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `hx-get=\"/v2\"`) {
		t.Fatalf("after the swap the binding must lower to /v2, got:\n%s", generated)
	}
}

// TestGoSourceUpdatedFlag: generated files churn during regeneration and
// must not trigger route re-discovery; hand-written Go must.
func TestGoSourceUpdatedFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/flag\n\ngo 1.25\n")
	handWritten := writeFile(t, dir, "routes.go", "package main\n")
	// The generated file has a live source, so it is not an orphan.
	writeFile(t, dir, "page.ghtmx", testTemplate)
	generated := writeFile(t, dir, "page_ghtmx.go", "package main\n")

	fseh := NewFSEventHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		dir, false, nil, false, false, FileWriter, false,
		WithGeneratedSuffix("_ghtmx.go"),
	)
	ctx := context.Background()
	r, err := fseh.HandleEvent(ctx, fsnotify.Event{Name: handWritten, Op: fsnotify.Write})
	if err != nil {
		t.Fatal(err)
	}
	if !r.GoSourceUpdated {
		t.Error("hand-written Go must set GoSourceUpdated")
	}
	r, err = fseh.HandleEvent(ctx, fsnotify.Event{Name: generated, Op: fsnotify.Write})
	if err != nil {
		t.Fatal(err)
	}
	if r.GoSourceUpdated || r.WatchedFileUpdated {
		t.Errorf("generated files bypass the watched-file path entirely, got %+v", r)
	}
}

// TestRapidEditsCoalesceKeepingFinalState: FR-061 — successive edits are
// each processed in order (the mtime gate admits every advance), so the
// final write wins; the 100ms grouping coalesces the notifications.
func TestRapidEditsCoalesceKeepingFinalState(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/rapid\n\ngo 1.25\n")
	target := filepath.Join(dir, "page.ghtmx")

	fseh := NewFSEventHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		dir, false, nil, false, false, FileWriter, false,
	)
	ctx := context.Background()
	for i, body := range []string{"one", "two", "final"} {
		writeFile(t, dir, "page.ghtmx", "package main\n\ntempl page() {\n\t<p>"+body+"</p>\n}\n")
		// Distinct mtimes model real editor saves.
		mt := time.Now().Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(target, mt, mt); err != nil {
			t.Fatal(err)
		}
		if _, err := fseh.HandleEvent(ctx, fsnotify.Event{Name: target, Op: fsnotify.Write}); err != nil {
			t.Fatal(err)
		}
	}
	generated, err := os.ReadFile(filepath.Join(dir, "page_ghtmx.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "final") || strings.Contains(string(generated), "two") {
		t.Fatalf("the final state must win, got:\n%s", generated)
	}
}

// TestGroupingCoalescesFlags: FR-061 — rapid successive notifications
// coalesce into one batch, OR-ing every flag (including GoSourceUpdated)
// so no signal from the burst is dropped.
func TestGroupingCoalescesFlags(t *testing.T) {
	cmd := Generate{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ch := make(chan *GenerationEvent, 8)
	ch <- &GenerationEvent{GoFileWritten: true}
	ch <- &GenerationEvent{GoSourceUpdated: true, WatchedFileUpdated: true}
	ch <- &GenerationEvent{TemplFileTextUpdated: true}
	close(ch)

	grouped, updates, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil || !ok {
		t.Fatalf("grouping failed: ok=%v err=%v", ok, err)
	}
	if !grouped.GoFileWritten || !grouped.GoSourceUpdated || !grouped.WatchedFileUpdated || !grouped.TemplFileTextUpdated {
		t.Errorf("all flags must survive coalescing, got %+v", grouped)
	}
	if updates != 1 {
		t.Errorf("updates counts written files, got %d", updates)
	}
}

// TestGoSourceRemovalTriggersTierOne: deleting a hand-written Go file can
// remove route registrations; the event must carry the tier-one signal.
func TestGoSourceRemovalTriggersTierOne(t *testing.T) {
	dir := t.TempDir()
	fseh := NewFSEventHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		dir, false, nil, false, false, FileWriter, false,
		WithGeneratedSuffix("_ghtmx.go"),
	)
	r, err := fseh.HandleEvent(context.Background(), fsnotify.Event{Name: filepath.Join(dir, "routes.go"), Op: fsnotify.Remove})
	if err != nil {
		t.Fatal(err)
	}
	if !r.GoSourceUpdated || !r.WatchedFileUpdated {
		t.Errorf("a removed Go source must trigger re-discovery, got %+v", r)
	}
}

// TestStaleOutputDetection: FR-054 — normal mode reports stale outputs as
// GHTMX-W0301 and regenerates them; check mode writes nothing and exits
// non-zero, reporting the same diagnostic.
func TestStaleOutputDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/stale\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)

	// Generate once, then corrupt the artifact to model drift.
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatalf("initial generate failed: %v", err)
	}
	target := filepath.Join(dir, "page_ghtmx.go")
	fresh, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "page_ghtmx.go", "package main // hand-edited drift\n")

	// Check mode: nothing written, non-zero exit, W0301 reported.
	var checkLog bytes.Buffer
	err = Run(context.Background(), io.Discard, &checkLog, []string{"-include-version=false", "-check"})
	if err == nil {
		t.Fatal("check mode must exit non-zero on drift")
	}
	if got := strings.Count(checkLog.String(), "GHTMX-W0301"); got != 1 || !strings.Contains(checkLog.String(), "page_ghtmx.go") {
		t.Errorf("drift must be exactly one W0301 naming the file (got %d):\n%s", got, checkLog.String())
	}
	drifted, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(drifted), "hand-edited drift") {
		t.Error("check mode must not write")
	}

	// Normal mode: reports W0301 and regenerates to the fresh content.
	var genLog bytes.Buffer
	if err := Run(context.Background(), io.Discard, &genLog, []string{"-include-version=false"}); err != nil {
		t.Fatalf("regeneration failed: %v", err)
	}
	if !strings.Contains(genLog.String(), "GHTMX-W0301") {
		t.Errorf("normal mode must report the stale output, got:\n%s", genLog.String())
	}
	regenerated, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(regenerated) != string(fresh) {
		t.Error("normal mode must regenerate the stale artifact")
	}

	// A clean tree: check passes, no W0301.
	var cleanLog bytes.Buffer
	if err := Run(context.Background(), io.Discard, &cleanLog, []string{"-include-version=false", "-check"}); err != nil {
		t.Fatalf("check on a clean tree must pass: %v", err)
	}
	if strings.Contains(cleanLog.String(), "GHTMX-W0301") {
		t.Errorf("no drift, no diagnostic, got:\n%s", cleanLog.String())
	}
}

// TestMissingGeneratedOutputIsDrift: a deleted artifact is drift too.
func TestMissingGeneratedOutputIsDrift(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/missing\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "page_ghtmx.go")); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := Run(context.Background(), io.Discard, &log, []string{"-include-version=false", "-check"}); err == nil {
		t.Fatal("a missing artifact is drift")
	}
	if !strings.Contains(log.String(), "GHTMX-W0301") {
		t.Errorf("expected W0301, got:\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "page_ghtmx.go")); !os.IsNotExist(err) {
		t.Error("check mode must not create the missing file")
	}
}

// TestCheckLazyStillDetectsDrift: -lazy skips artifacts newer than their
// source — exactly what a hand edit produces — so check mode must ignore
// it (FR-054).
func TestCheckLazyStillDetectsDrift(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/lazycheck\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "page_ghtmx.go", "package main // drift, newer than source\n")
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false", "-check", "-lazy"}); err == nil {
		t.Fatal("-check -lazy must still detect drift")
	}
}

// TestStaleSeverityOffSilencesButStillFails: GHTMX-W0301=off silences the
// log line; the check-mode non-zero exit is independent of severity.
func TestStaleSeverityOffSilencesButStillFails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/offsev\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)
	if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "page_ghtmx.go", "package main // drift\n")
	var log bytes.Buffer
	err := Run(context.Background(), io.Discard, &log, []string{"-include-version=false", "-check", "-check-severity", "GHTMX-W0301=off"})
	if err == nil {
		t.Fatal("silencing the diagnostic must not make drift pass")
	}
	if strings.Contains(log.String(), "GHTMX-W0301") {
		t.Errorf("=off must silence the log line, got:\n%s", log.String())
	}
}
