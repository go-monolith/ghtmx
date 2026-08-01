package release

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the release source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// TestReleaseArtifacts: the builder produces one archive per supported
// platform, the checksums file verifies against the archives, and the
// local platform's binary reports the embedded single version.
// Env-gated: six cross-target stdlib compiles are minutes cold, so the
// release workflow's gates job runs this rather than every test run.
func TestReleaseArtifacts(t *testing.T) {
	if os.Getenv("GHTMX_RELEASE_GATE") == "" {
		t.Skip("set GHTMX_RELEASE_GATE=1 to build the full release matrix")
	}
	root := repoRoot(t)
	embedded, err := os.ReadFile(filepath.Join(root, ".version"))
	if err != nil {
		t.Fatal(err)
	}
	version := "v" + strings.TrimSpace(string(embedded))
	dst := t.TempDir()

	if err := Build(root, version, dst); err != nil {
		t.Fatalf("release build failed: %v", err)
	}

	bare := strings.TrimPrefix(version, "v")
	for _, target := range Targets {
		ext := ".tar.gz"
		if target.GOOS == "windows" {
			ext = ".zip"
		}
		name := fmt.Sprintf("ghtmx_%s_%s_%s%s", bare, target.GOOS, target.GOARCH, ext)
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("missing release artifact for %s/%s: %v", target.GOOS, target.GOARCH, err)
		}
	}

	// checksums.txt must verify, cover every archive, and nothing else.
	sums, err := os.Open(filepath.Join(dst, "checksums.txt"))
	if err != nil {
		t.Fatalf("checksums file missing: %v", err)
	}
	defer sums.Close()
	counted := 0
	scanner := bufio.NewScanner(sums)
	for scanner.Scan() {
		var wantSum, name string
		if _, err := fmt.Sscanf(scanner.Text(), "%s  %s", &wantSum, &name); err != nil {
			t.Fatalf("malformed checksum line %q: %v", scanner.Text(), err)
		}
		f, err := os.Open(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("checksummed artifact %s missing: %v", name, err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if got := fmt.Sprintf("%x", h.Sum(nil)); got != wantSum {
			t.Errorf("checksum mismatch for %s", name)
		}
		counted++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if counted != len(Targets) {
		t.Errorf("checksums.txt covers %d artifacts, want %d", counted, len(Targets))
	}

	// The local platform's binary must report the single embedded
	// version — the same version every component of the module carries.
	binary := filepath.Join(t.TempDir(), "ghtmx")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/ghtmx")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("local build failed: %v\n%s", err, out)
	}
	out, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("ghtmx version failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != version {
		t.Errorf("ghtmx version = %q, want the embedded %q", got, version)
	}
}

// TestReleaseWorkflowRunsTheGates: the release workflow must keep the
// full gate set — the AC names govulncheck and the WASM matrix — and
// the lockstep adapter tagging.
func TestReleaseWorkflowRunsTheGates(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("the release workflow is missing: %v", err)
	}
	workflow := string(raw)
	for _, needle := range []string{
		"go test ./...",     // includes the WASM matrix and coverage gates
		"GHTMX_VULN_GATE=1", // NFR-008
		"GHTMX_PERF_GATE=1", // NFR-001/NFR-002/NFR-003
		"TestLSPLatencyGate",
		"GHTMX_RELEASE_GATE=1", // the artifact matrix itself
		"needs: gates",         // artifacts only after the gates
		// Verify-not-stamp: the tagged tree must already carry the
		// version so go-install consumers see it too (RELEASING.md).
		`test "v$(cat .version)" = "$GITHUB_REF_NAME"`,
		`grep -q "github.com/go-monolith/ghtmx $GITHUB_REF_NAME"`,
		"git diff --exit-code",               // generated code is current
		"adapters/*/go.mod",                  // adapter tests + lockstep tags
		`git tag -f "$dir/$GITHUB_REF_NAME"`, // lockstep versioning
	} {
		if !strings.Contains(workflow, needle) {
			t.Errorf("release.yml lost %q", needle)
		}
	}
	if !strings.Contains(workflow, "dist/release/*") {
		t.Error("release.yml must upload every artifact including checksums.txt")
	}
}
