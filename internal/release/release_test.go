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
		// The tag reaches the workflow two ways — a human pushing it and
		// auto-release.yml calling in — and both must resolve to one
		// value, or the gates would verify a different tree than the one
		// that ships.
		"TAG: ${{ inputs.tag || github.ref_name }}",
		// Verify-not-stamp: the tagged tree must already carry the
		// version so go-install consumers see it too (RELEASING.md).
		`test "v$(cat .version)" = "$TAG"`,
		`grep -q "github.com/go-monolith/ghtmx $TAG\$"`,
		"git diff --exit-code", // generated code is current
		"adapters/*/go.mod",    // adapter tests + lockstep tags
		// Tagging happens after the gates, and its behaviour is covered
		// by TestPublishTagsIsIdempotent rather than by matching text.
		".github/scripts/publish-tags.sh",
	} {
		if !strings.Contains(workflow, needle) {
			t.Errorf("release.yml lost %q", needle)
		}
	}
	if !strings.Contains(workflow, "dist/release/*") {
		t.Error("release.yml must upload every artifact including checksums.txt")
	}
	// A tag is immutable once the module proxy has seen it, so neither
	// the workflow nor the script it calls may move one — not even to
	// recover a failed run.
	publish, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "scripts", "publish-tags.sh"))
	if err != nil {
		t.Fatalf("the publish script is missing: %v", err)
	}
	for name, body := range map[string]string{"release.yml": workflow, "publish-tags.sh": string(publish)} {
		for line := range strings.SplitSeq(body, "\n") {
			if !strings.Contains(line, "git push") {
				continue
			}
			if strings.Contains(line, "-f") || strings.Contains(line, "--force") {
				t.Errorf("%s force-pushes a tag, which moves a published version: %q", name, strings.TrimSpace(line))
			}
		}
	}
}

// TestReleaseAttachesEditorArtifacts: a release carries the three
// editor integrations alongside the binary matrix. The wiring is easy to
// break silently — a reusable workflow does not inherit the caller's
// permissions, and an upload without contents: write fails only at
// release time, on a tag that has already been published.
func TestReleaseAttachesEditorArtifacts(t *testing.T) {
	release, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("the release workflow is missing: %v", err)
	}
	for _, needle := range []string{
		"uses: ./.github/workflows/editors.yml",
		// Packaging must never stand between a green gate set and a
		// published module, so the artifacts follow the release.
		"needs: release",
		"tag: ${{ inputs.tag || github.ref_name }}",
	} {
		if !strings.Contains(string(release), needle) {
			t.Errorf("release.yml lost %q", needle)
		}
	}

	editors, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "editors.yml"))
	if err != nil {
		t.Fatalf("the editors workflow is missing: %v", err)
	}
	workflow := string(editors)
	for _, needle := range []string{
		"workflow_call:",
		// Without this the upload 403s: workflow_call does not inherit
		// the caller job's permissions, and secrets: inherit does not
		// carry them either.
		"contents: write",
		// One job per integration: the JetBrains build resolves an
		// IntelliJ platform and is the most likely to fail, and it must
		// not take the other two artifacts down with it.
		"  vscode:",
		"  jetbrains:",
		"  nvim:",
		// A PR that touches the editors builds all three for real; it is
		// the only automated exercise the JDK path gets.
		"- \"editors/**\"",
	} {
		if !strings.Contains(workflow, needle) {
			t.Errorf("editors.yml lost %q", needle)
		}
	}
	// Every job must actually attach its artifact, and only when called
	// with a tag — a PR build has no release to upload to.
	if got := strings.Count(workflow, "gh release upload"); got != 3 {
		t.Errorf("editors.yml has %d release uploads, want one per integration", got)
	}
	if got := strings.Count(workflow, "if: inputs.tag != ''"); got != 3 {
		t.Errorf("editors.yml guards %d uploads on a tag, want one per integration", got)
	}
}

// TestAutoReleaseNeverWritesToMain: main is branch-protected with
// enforce_admins, so the automation earns its keep only by tagging an
// off-main prep commit. A push to any branch would break on protection
// and leave a half-cut release, so guard the shape of the workflow.
func TestAutoReleaseNeverWritesToMain(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "auto-release.yml"))
	if err != nil {
		t.Fatalf("the auto-release workflow is missing: %v", err)
	}
	workflow := string(raw)

	for _, needle := range []string{
		"branches: [main]",                      // every merge to main
		"uses: ./.github/workflows/release.yml", // the gates own the tagging
		"pull_request:",                         // merge events on this repo get lost
		// Serialization rests on the group being shared AND on pending
		// runs surviving. Flipping cancel-in-progress to true would
		// silently drop a queued merge's release, so pin the value, not
		// just the presence of a concurrency block.
		"group: auto-release",
		"cancel-in-progress: false",
	} {
		if !strings.Contains(workflow, needle) {
			t.Errorf("auto-release.yml lost %q", needle)
		}
	}

	// Everything the pre-gate half of the release pushes, including the
	// scripts it calls.
	sources := map[string]string{"auto-release.yml": workflow}
	for _, name := range []string{"release-plan.sh", "release-stage.sh"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "scripts", name))
		if err != nil {
			t.Fatalf("%s is missing: %v", name, err)
		}
		sources[name] = string(body)
	}

	for name, body := range sources {
		for line := range strings.SplitSeq(body, "\n") {
			if !strings.Contains(line, "git push") {
				continue
			}
			// The tag must not exist until release.yml has gated the tree
			// behind it: a tag is immutable once the proxy sees it, so a
			// failed gate would otherwise burn the version permanently and
			// publish a root module with no adapter tags beside it.
			if strings.Contains(line, "refs/tags/") {
				t.Errorf("%s tags before the gates run: %q", name, strings.TrimSpace(line))
			}
			// main is branch-protected with enforce_admins; any push to it
			// would be rejected and strand a half-cut release.
			if strings.Contains(line, "refs/heads/main") || strings.Contains(line, "origin main") {
				t.Errorf("%s pushes to main: %q", name, strings.TrimSpace(line))
			}
		}
	}
}
