package release

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// The release decision, the prep-commit rewriting, and tag publishing
// live in .github/scripts/*.sh so they can be exercised here against
// real repositories. Asserting the workflow YAML contains the right
// strings proves only that the text is present; these tests prove the
// behaviour, which is what a wrong regex or wrong arithmetic breaks.

func script(t *testing.T, name string) string {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("the release scripts are bash and run on the ubuntu runner")
	}
	path := filepath.Join(repoRoot(t), ".github", "scripts", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("release script missing: %v", err)
	}
	return path
}

// git runs a command in dir and fails the test on error.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes path and commits it with the given message. A message
// may carry a body after a blank line, which is how trailers arrive.
func commit(t *testing.T, dir, path, content, message string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", message)
}

// newRepo returns a repository with one code commit tagged v0.1.0, the
// shape main is in immediately after a release.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "main.go", "package p\n", "root")
	commit(t, dir, "README.md", "docs\n", "#1 readme")
	git(t, dir, "tag", "v0.1.0")
	return dir
}

// plan runs release-plan.sh and returns its outputs plus the exit code.
func plan(t *testing.T, dir string) (outputs map[string]string, log string, code int) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(script(t, "release-plan.sh"))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+out)
	raw, err := cmd.CombinedOutput()
	code = cmd.ProcessState.ExitCode()
	if err != nil && code == -1 {
		t.Fatalf("running the plan script: %v\n%s", err, raw)
	}
	body, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatal(readErr)
	}
	outputs = map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			outputs[key] = value
		}
	}
	return outputs, string(raw), code
}

// TestReleasePlanDecidesTheVersion covers the arithmetic and the marker
// detection that decide what gets published.
func TestReleasePlanDecidesTheVersion(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history func(t *testing.T, dir string)
		release string
		tag     string
	}{{
		name:    "a code merge bumps the patch",
		history: func(t *testing.T, dir string) { commit(t, dir, "main.go", "package p // v2\n", "#2 fix a bug") },
		release: "true", tag: "v0.1.1",
	}, {
		name: "a breaking subject bumps the minor instead",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "feat!: drop the old API")
		},
		release: "true", tag: "v0.2.0",
	}, {
		name: "a BREAKING CHANGE trailer bumps the minor",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 rework\n\nBREAKING CHANGE: renamed Foo")
		},
		release: "true", tag: "v0.2.0",
	}, {
		// The marker sits in an earlier merge, not the tip. The release
		// spans everything since the tag, so it must still count.
		name: "a breaking marker in an earlier unreleased commit still counts",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "feat!: drop the old API")
			commit(t, dir, "other.go", "package p\n", "#3 unrelated tidy")
		},
		release: "true", tag: "v0.2.0",
	}, {
		// A true merge commit's own subject carries no marker; the
		// branch's commits do.
		name: "a breaking marker behind a merge commit still counts",
		history: func(t *testing.T, dir string) {
			git(t, dir, "checkout", "-q", "-b", "feat")
			commit(t, dir, "main.go", "package p // v2\n", "feat!: another break")
			git(t, dir, "checkout", "-q", "main")
			git(t, dir, "merge", "-q", "--no-ff", "feat", "-m", "Merge pull request #9 from u/feat")
		},
		release: "true", tag: "v0.2.0",
	}, {
		// Guarding the opposite direction: prose must not force a bump.
		name: "prose mentioning a breaking change does not bump the minor",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 normal\n\nthis is not a BREAKING CHANGE really")
		},
		release: "true", tag: "v0.1.1",
	}, {
		name: "the tip can opt out",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 wip [skip release]")
		},
		release: "false",
	}, {
		name: "docs-only changes since the tag do not ship",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "README.md", "more docs\n", "#2 docs")
			commit(t, dir, "docs/site/index.md", "page\n", "#3 more docs")
		},
		release: "false",
	}, {
		name: "workflow-only changes do not ship",
		history: func(t *testing.T, dir string) {
			commit(t, dir, ".github/workflows/ci.yml", "name: CI\n", "#2 tweak CI")
		},
		release: "false",
	}, {
		// Docs on top of unreleased code must not mask the code: this is
		// what a per-push delta got wrong.
		name: "a docs merge after a code merge still ships the code",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 real code")
			commit(t, dir, "README.md", "more docs\n", "#3 docs")
		},
		release: "true", tag: "v0.1.1",
	}, {
		// v0.2.0-rc1 sorts above v0.2.0 under -v:refname, which would
		// skip v0.2.0 entirely.
		name: "a prerelease tag is not used as the baseline",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.2.0-rc1")
			commit(t, dir, "main.go", "package p // v2\n", "#2 fix")
		},
		release: "true", tag: "v0.1.1",
	}, {
		// The prep commit's own .version/go.mod edits must not read as
		// shipped changes, or the docs-only skip could never fire.
		name: "an off-main prep commit as the tag does not mask a docs-only range",
		history: func(t *testing.T, dir string) {
			tip := git(t, dir, "rev-parse", "HEAD")
			git(t, dir, "checkout", "-q", "--detach", tip)
			commit(t, dir, ".version", "0.1.1", "Release v0.1.1")
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "checkout", "-q", "main")
			commit(t, dir, "README.md", "more docs\n", "#2 docs")
		},
		release: "false",
	}, {
		name: "an off-main prep commit as the tag still sees new code",
		history: func(t *testing.T, dir string) {
			tip := git(t, dir, "rev-parse", "HEAD")
			git(t, dir, "checkout", "-q", "--detach", tip)
			commit(t, dir, ".version", "0.1.1", "Release v0.1.1")
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "checkout", "-q", "main")
			commit(t, dir, "main.go", "package p // v2\n", "#2 code")
		},
		release: "true", tag: "v0.1.2",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := newRepo(t)
			tc.history(t, dir)

			outputs, log, code := plan(t, dir)
			if code != 0 {
				t.Fatalf("plan exited %d, want 0\n%s", code, log)
			}
			if got := outputs["release"]; got != tc.release {
				t.Errorf("release = %q, want %q\n%s", got, tc.release, log)
			}
			if got := outputs["tag"]; got != tc.tag {
				t.Errorf("tag = %q, want %q\n%s", got, tc.tag, log)
			}
		})
	}
}

// TestReleasePlanRefusesToBootstrap: with no tag to bump from, the run
// must stop rather than invent a baseline for a module that is about to
// be published.
//
// The script's other refusal — the computed tag already existing — has
// no test because it is unreachable by construction, not because it is
// unchecked: the next version is always the highest tag plus one, so a
// tag equal to it would itself have been the highest. It stays in the
// script as cheap defence against a future change to how the baseline
// is picked.
func TestReleasePlanRefusesToBootstrap(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "main.go", "package p\n", "root")

	_, log, code := plan(t, dir)
	if code == 0 {
		t.Fatalf("plan succeeded with no tag to bump from\n%s", log)
	}
	if !strings.Contains(log, "cut the first release by hand") {
		t.Errorf("the error must say how to bootstrap, got:\n%s", log)
	}
}

// modTree lays down the module files the staging script rewrites, plus a
// bare remote to push to.
func modTree(t *testing.T, from string) (dir, remote string) {
	t.Helper()
	remote = t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "remote", "add", "origin", remote)

	if err := os.WriteFile(filepath.Join(dir, ".version"), []byte(strings.TrimPrefix(from, "v")), 0o644); err != nil {
		t.Fatal(err)
	}
	root := "require github.com/go-monolith/ghtmx " + from + "\nreplace github.com/go-monolith/ghtmx => ../..\n"
	chi := root + "require github.com/go-monolith/ghtmx/adapters/chi " + from + "\n"
	for path, body := range map[string]string{
		"adapters/chi/go.mod":               root,
		"adapters/echo/go.mod":              root,
		"docs/official/go.mod":              chi,
		"internal/wasmcheck/fixture/go.mod": chi,
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "tree")
	git(t, dir, "push", "-q", "origin", "HEAD:main")
	return dir, remote
}

func run(t *testing.T, dir, name string, env ...string) (log string, code int) {
	t.Helper()
	cmd := exec.Command(script(t, name))
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	raw, err := cmd.CombinedOutput()
	code = cmd.ProcessState.ExitCode()
	if err != nil && code == -1 {
		t.Fatalf("running %s: %v\n%s", name, err, raw)
	}
	return string(raw), code
}

// TestReleaseStageRewritesEveryModule: the gates verify all six requires,
// so a rewrite that misses one fails the release. The `replace` lines
// carry no version and must survive untouched, or in-repo development
// stops building against the working tree.
func TestReleaseStageRewritesEveryModule(t *testing.T) {
	dir, _ := modTree(t, "v0.1.0")
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	log, code := run(t, dir, "release-stage.sh", "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("stage exited %d\n%s", code, log)
	}

	if got := readFile(t, filepath.Join(dir, ".version")); got != "0.1.1" {
		t.Errorf(".version = %q, want %q (no trailing newline)", got, "0.1.1")
	}
	for _, mod := range []string{
		"adapters/chi/go.mod", "adapters/echo/go.mod",
		"docs/official/go.mod", "internal/wasmcheck/fixture/go.mod",
	} {
		body := readFile(t, filepath.Join(dir, mod))
		if strings.Contains(body, "ghtmx v0.1.0") {
			t.Errorf("%s still requires v0.1.0:\n%s", mod, body)
		}
		if !strings.Contains(body, "require github.com/go-monolith/ghtmx v0.1.1") {
			t.Errorf("%s does not require the new version:\n%s", mod, body)
		}
		if !strings.Contains(body, "replace github.com/go-monolith/ghtmx => ../..") {
			t.Errorf("%s lost its replace directive:\n%s", mod, body)
		}
	}
	for _, mod := range []string{"docs/official/go.mod", "internal/wasmcheck/fixture/go.mod"} {
		if !strings.Contains(readFile(t, filepath.Join(dir, mod)), "adapters/chi v0.1.1") {
			t.Errorf("%s does not require adapters/chi v0.1.1", mod)
		}
	}
	// The rewrite goes through a temporary file; a plain move would carry
	// mktemp's 0600 onto tracked sources.
	for _, mod := range []string{"adapters/chi/go.mod", "docs/official/go.mod"} {
		info, err := os.Stat(filepath.Join(dir, mod))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("%s mode = %#o after rewriting, want 0644", mod, got)
		}
	}

	if got := readOutput(t, out)["ref"]; got != "auto-release/v0.1.1" {
		t.Errorf("ref = %q, want the scratch branch", got)
	}
}

// TestReleaseStageTolersatesAnAlreadyStagedTree: a hand-made prep commit
// for the same version may already sit on main, leaving nothing to
// rewrite. Committing nothing is an error, so the tip is staged as-is.
func TestReleaseStageToleratesAnAlreadyStagedTree(t *testing.T) {
	dir, _ := modTree(t, "v0.1.1")
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	log, code := run(t, dir, "release-stage.sh", "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("stage exited %d on an already-staged tree\n%s", code, log)
	}
	if !strings.Contains(log, "already carries") {
		t.Errorf("expected a notice that nothing needed staging, got:\n%s", log)
	}
	if got := readOutput(t, out)["ref"]; got != "auto-release/v0.1.1" {
		t.Errorf("ref = %q, want the scratch branch even with nothing to commit", got)
	}
}

// TestPublishTagsIsIdempotent: a re-run must be able to finish a partial
// release, and must never move a tag that is already published.
func TestPublishTagsIsIdempotent(t *testing.T) {
	dir, remote := modTree(t, "v0.1.1")

	log, code := run(t, dir, "publish-tags.sh", "TAG=v0.1.1")
	if code != 0 {
		t.Fatalf("first publish exited %d\n%s", code, log)
	}
	for _, want := range []string{"v0.1.1", "adapters/chi/v0.1.1", "adapters/echo/v0.1.1"} {
		if !strings.Contains(remoteTags(t, dir), want+"\n") {
			t.Errorf("%s was not published:\n%s", want, log)
		}
	}

	if log, code = run(t, dir, "publish-tags.sh", "TAG=v0.1.1"); code != 0 {
		t.Fatalf("re-running a complete release exited %d\n%s", code, log)
	}
	if strings.Count(log, "already points here") != 3 {
		t.Errorf("a repeat run must be a no-op for every tag, got:\n%s", log)
	}

	// A partial release: one adapter tag never made it. The re-run must
	// fill only that gap. This is the case a local existence check got
	// wrong — it would mint a fresh tag object and be rejected on push.
	git(t, remote, "update-ref", "-d", "refs/tags/adapters/echo/v0.1.1")
	if log, code = run(t, dir, "publish-tags.sh", "TAG=v0.1.1"); code != 0 {
		t.Fatalf("recovering a partial release exited %d\n%s", code, log)
	}
	if !strings.Contains(log, "published adapters/echo/v0.1.1") {
		t.Errorf("the missing tag was not republished:\n%s", log)
	}

	// A published version must never move under consumers.
	commit(t, dir, "main.go", "package p\n", "#2 later work")
	log, code = run(t, dir, "publish-tags.sh", "TAG=v0.1.1")
	if code == 0 {
		t.Fatalf("publishing from a different commit succeeded\n%s", log)
	}
	if !strings.Contains(log, "refusing to move") {
		t.Errorf("expected a refusal to move the tag, got:\n%s", log)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readOutput(t *testing.T, path string) map[string]string {
	t.Helper()
	outputs := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(readFile(t, path)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			outputs[key] = value
		}
	}
	return outputs
}

func remoteTags(t *testing.T, dir string) string {
	t.Helper()
	var tags strings.Builder
	for line := range strings.SplitSeq(git(t, dir, "ls-remote", "--tags", "origin"), "\n") {
		_, ref, ok := strings.Cut(line, "refs/tags/")
		if ok && !strings.HasSuffix(ref, "^{}") {
			tags.WriteString(ref + "\n")
		}
	}
	return tags.String()
}
