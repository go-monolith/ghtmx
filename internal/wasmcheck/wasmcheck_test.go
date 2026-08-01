// Package wasmcheck is the NFR-014 WASM build matrix: the fixture
// application (runtime + generated components + chi adapter) and every
// included adapter must compile for GOOS=js GOARCH=wasm and
// GOOS=wasip1 GOARCH=wasm on every test run, so a dependency
// unavailable on a WASM target is caught at the point of introduction.
// A build failure surfaces the go toolchain's output, which names the
// offending package — and, for compile errors, the symbol — and fails
// CI, blocking release.
//
// The matrix, including documented exclusions:
//
//	target        nethttp  chi  echo  gin  fiber
//	js/wasm       yes      yes  yes   yes  excluded (upstream)
//	wasip1/wasm   yes      yes  yes   yes  excluded (upstream)
//
// Exclusions are recorded in adapterMatrix below with their upstream
// reason, and the record is self-honest: the excluded build is
// expected to keep failing, so the day upstream gains WASM support the
// matrix demands updating.
package wasmcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var wasmTargets = []struct{ goos, goarch string }{
	{"js", "wasm"},
	{"wasip1", "wasm"},
}

// adapterMatrix is the explicit AC record: every first-party adapter is
// either in the WASM matrix or excluded with its documented upstream
// reason.
var adapterMatrix = []struct {
	name            string
	dir             string
	exclusionReason string
}{
	{name: "nethttp", dir: "adapters/nethttp"},
	{name: "chi", dir: "adapters/chi"},
	{name: "echo", dir: "adapters/echo"},
	{name: "gin", dir: "adapters/gin"},
	{
		name: "fiber",
		dir:  "adapters/fiber",
		exclusionReason: "fasthttp's tcplisten uses raw socket syscalls the WASM ports lack " +
			"(js: syscall.SOCK_NONBLOCK/SOCK_CLOEXEC; wasip1: syscall.ForkLock)",
	},
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the wasmcheck source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// wasmBuild compiles dir for the target. mainPackage steers the -o
// handling: a single-main-package module needs -o pointed at a
// throwaway dir or the executable lands in the source tree, while
// library-only trees reject a directory -o ("no main packages to
// build") and write nothing anyway.
func wasmBuild(t *testing.T, dir, goos, goarch string, mainPackage bool) (string, error) {
	t.Helper()
	args := []string{"build"}
	if mainPackage {
		args = append(args, "-o", t.TempDir())
	}
	args = append(args, "./...")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestFixtureCompilesForWASMTargets: NFR-014 — the fixture application
// importing the runtime, generated components, and the chi adapter
// compiles for both WASM targets with zero errors.
func TestFixtureCompilesForWASMTargets(t *testing.T) {
	fixture := filepath.Join(moduleRoot(t), "internal", "wasmcheck", "fixture")
	for _, target := range wasmTargets {
		t.Run(target.goos, func(t *testing.T) {
			if out, err := wasmBuild(t, fixture, target.goos, target.goarch, true); err != nil {
				t.Errorf("the WASM fixture does not compile for %s/%s — the toolchain output below names the offending package and symbol:\n%s",
					target.goos, target.goarch, out)
			}
		})
	}
}

// TestAdapterWASMMatrix: every included adapter compiles for both WASM
// targets, and every excluded adapter still fails — so the exclusion
// record cannot silently go stale in either direction.
func TestAdapterWASMMatrix(t *testing.T) {
	root := moduleRoot(t)
	for _, adapter := range adapterMatrix {
		for _, target := range wasmTargets {
			t.Run(adapter.name+"-"+target.goos, func(t *testing.T) {
				out, err := wasmBuild(t, filepath.Join(root, adapter.dir), target.goos, target.goarch, false)
				if adapter.exclusionReason == "" {
					if err != nil {
						t.Errorf("the %s adapter no longer compiles for %s/%s:\n%s", adapter.name, target.goos, target.goarch, out)
					}
					return
				}
				if err == nil {
					t.Errorf("the %s adapter now compiles for %s/%s — upstream gained WASM support; move it into the matrix and drop the exclusion (was: %s)",
						adapter.name, target.goos, target.goarch, adapter.exclusionReason)
					return
				}
				// The failure must be the DOCUMENTED one: a transient
				// toolchain or download error would otherwise masquerade
				// as the exclusion and hide a real transition.
				if !strings.Contains(out, "tcplisten") {
					t.Errorf("the %s exclusion failed for a different reason than documented (%s); investigate and refresh the record:\n%s",
						adapter.name, adapter.exclusionReason, out)
				}
			})
		}
	}
}
