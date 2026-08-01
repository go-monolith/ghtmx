// Package corpusgate is the D11 CI-phase validation gate (NFR-005):
// go vet and full compilation over the golden-file corpus and every
// fixture application.
//
// The split it enforces, per solution.md D11: generate-time
// self-validation is self-contained — the compiler parses and analyzes
// before writing, refuses to emit invalid output, and detects drift
// (GHTMX-W0301) — but it cannot type-check against the surrounding
// package, which may not even compile yet on a clean checkout. This
// gate is the other phase: it runs where package context is complete
// and every generated file is present, so `go vet` findings and
// compile errors in generated code fail the build here, not at
// generate time. It runs in every `go test ./...` and as a named CI
// step.
package corpusgate

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/testproject"
)

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed in %s: %v\n%s", name, strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// moduleRoot is derived from this source file's compiled-in path, so
// the gate works regardless of the test binary's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the corpusgate source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestGoldenCorpusEntriesStayInTheBuild: every golden corpus template
// must have its committed generated pair, and every committed pair its
// template. A missing pair would not break anything by itself — the
// package would simply vanish from the build, and with it the
// guarantee that the entry compiles; an orphaned pair would keep dead
// generated code compiling unnoticed.
func TestGoldenCorpusEntriesStayInTheBuild(t *testing.T) {
	root := moduleRoot(t)
	entries, err := filepath.Glob(filepath.Join(root, "internal", "generator", "test-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 40 {
		t.Fatalf("only %d golden corpus entries found — the corpus moved?", len(entries))
	}
	for _, dir := range entries {
		templates, err := filepath.Glob(filepath.Join(dir, "*.ghtmx"))
		if err != nil {
			t.Fatal(err)
		}
		for _, template := range templates {
			pair := strings.TrimSuffix(template, ".ghtmx") + "_ghtmx.go"
			if _, err := os.Stat(pair); err != nil {
				t.Errorf("%s has no committed generated pair: %v", filepath.Base(dir), err)
			}
		}
		pairs, err := filepath.Glob(filepath.Join(dir, "*_ghtmx.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range pairs {
			template := strings.TrimSuffix(pair, "_ghtmx.go") + ".ghtmx"
			if _, err := os.Stat(template); err != nil {
				t.Errorf("%s has an orphaned generated file %s: %v", filepath.Base(dir), filepath.Base(pair), err)
			}
		}
	}
}

// TestGeneratedCodeCompilesAndPassesVet: the named NFR-005 enforcement
// point — go vet over every package tree that carries generated code,
// re-verifying the generate-time gofmt clause over the committed
// corpus. The repo-wide build and vet cover these too; this test is
// the gate that names the guarantee and cannot be lost to a CI
// refactor.
func TestGeneratedCodeCompilesAndPassesVet(t *testing.T) {
	root := moduleRoot(t)
	packages := []string{
		"./cmd/...",
		"./internal/generator/...",
		"./examples/...",
		"./conformance/...",
		"./benchmarks/...",
		"./ghtmxgen/...",
	}
	run(t, root, "go", append([]string{"build"}, packages...)...)
	run(t, root, "go", append([]string{"vet"}, packages...)...)

	var generated []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_ghtmx.go") {
			generated = append(generated, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(generated) < 50 {
		t.Fatalf("only %d generated files found — the corpus moved?", len(generated))
	}
	if out := run(t, root, "gofmt", append([]string{"-l"}, generated...)...); strings.TrimSpace(out) != "" {
		t.Errorf("generated files are not gofmt-clean:\n%s", out)
	}
}

// materializeFixture copies an embedded-style fixture directory
// (go.mod.embed with a {moduleRoot} placeholder) into a temp module.
func materializeFixture(t *testing.T, root, fixtureDir string) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(fixtureDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		target := entry.Name()
		if target == "go.mod.embed" {
			data = bytes.ReplaceAll(data, []byte("{moduleRoot}"), []byte(root))
			target = "go.mod"
		}
		if err := os.WriteFile(filepath.Join(dir, target), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestFixtureApplicationsCompileAndVet: every fixture application
// builds and vets as a whole program. The LSP test project and the
// watch fixture live in testdata directories the go tool never
// compiles, so this gate materializes and builds them for real; the
// adapter fixtures are nested modules the root build cannot see.
func TestFixtureApplicationsCompileAndVet(t *testing.T) {
	root := moduleRoot(t)

	t.Run("lsp-testproject", func(t *testing.T) {
		appDir, err := testproject.Create(root)
		if appDir != "" {
			t.Cleanup(func() { _ = os.RemoveAll(appDir) })
		}
		if err != nil {
			t.Fatalf("failed to materialize the LSP test project: %v", err)
		}
		run(t, appDir, "go", "build", "./...")
		run(t, appDir, "go", "vet", "./...")

		// The fixture dir is excluded from the repo-wide regeneration
		// (its routes are invisible at the repo root), so committed
		// pairs can go stale without failing compilation. Regenerate in
		// the materialized module — where its routes resolve — and any
		// difference is drift to re-record.
		if err := generatecmd.Run(context.Background(), io.Discard, io.Discard,
			[]string{"-path", appDir, "-include-version=false"}); err != nil {
			t.Fatalf("regeneration failed: %v", err)
		}
		pairs, err := filepath.Glob(filepath.Join(appDir, "*_ghtmx.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range pairs {
			regenerated, err := os.ReadFile(pair)
			if err != nil {
				t.Fatal(err)
			}
			committed, err := os.ReadFile(filepath.Join(root, "cmd", "ghtmx", "testproject", "testdata", filepath.Base(pair)))
			if err != nil {
				t.Errorf("%s is generated but has no committed fixture pair: %v", filepath.Base(pair), err)
				continue
			}
			if !bytes.Equal(regenerated, committed) {
				t.Errorf("%s drifted from the current generator; refresh the committed fixture pair", filepath.Base(pair))
			}
		}
	})

	t.Run("watch-fixture", func(t *testing.T) {
		dir := materializeFixture(t, root, filepath.Join(root, "cmd", "ghtmx", "generatecmd", "testwatch", "testdata"))
		run(t, dir, "go", "build", "./...")
		run(t, dir, "go", "vet", "./...")
	})

	t.Run("wasm-fixture", func(t *testing.T) {
		// The WASM matrix compiles this one cross-target; here it gets
		// its native build and vet. -o keeps the single-main-package
		// module from dropping its executable into the source tree.
		dir := filepath.Join(root, "internal", "wasmcheck", "fixture")
		run(t, dir, "go", "build", "-o", t.TempDir(), "./...")
		run(t, dir, "go", "vet", "./...")
	})

	adapters, err := filepath.Glob(filepath.Join(root, "adapters", "*", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if len(adapters) == 0 {
		t.Fatal("no nested adapter modules found — the layout moved?")
	}
	for _, mod := range adapters {
		dir := filepath.Dir(mod)
		t.Run("adapter-"+filepath.Base(dir), func(t *testing.T) {
			run(t, dir, "go", "build", "./...")
			run(t, dir, "go", "vet", "./...")
		})
	}
}
