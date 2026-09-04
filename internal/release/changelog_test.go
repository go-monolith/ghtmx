package release

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// The changelog machinery lives in three bash scripts —
// scripts/assemble-changelog.sh (folds changelog.d/ fragments into
// CHANGELOG.md under the released version), the release-stage hook
// that runs it, and .github/scripts/changelog-fold.sh (brings the fold
// back to main) plus .github/scripts/changelog-gate.sh (the CI rule) —
// exercised here against real repositories, the same way plan_test.go
// exercises the release decision.

// bashScript returns the path of a bash script anywhere in the
// repository, skipping on Windows like script() does.
func bashScript(t *testing.T, parts ...string) string {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("the changelog scripts are bash and run on the ubuntu runner")
	}
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("changelog script missing: %v", err)
	}
	return path
}

// runScript runs a script with arguments in dir and returns its
// combined output and exit code.
func runScript(t *testing.T, dir, path string, args []string, env ...string) (log string, code int) {
	t.Helper()
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	raw, err := cmd.CombinedOutput()
	code = cmd.ProcessState.ExitCode()
	if err != nil && code == -1 {
		t.Fatalf("running %s: %v\n%s", path, err, raw)
	}
	return string(raw), code
}

const changelogSeed = "# Changelog\n\nAssembled, not edited.\n\n" +
	"## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n"

// changelogRepo returns a repository whose CHANGELOG.md carries one
// released section, tagged v0.1.0 on main — the shape main is in after
// a release with the fragment system in place.
func changelogRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commit(t, dir, "CHANGELOG.md", changelogSeed, "root")
	git(t, dir, "tag", "v0.1.0")
	return dir
}

// stageRelease simulates what auto-release does with a version: an
// off-main prep commit that assembles the fragments, tagged but never
// merged back — main keeps its own stale CHANGELOG.md and fragments.
func stageRelease(t *testing.T, dir, version string) {
	t.Helper()
	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	tip := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-q", "--detach", tip)
	if log, code := runScript(t, dir, assemble, []string{version}, "RELEASE_DATE=2026-08-08"); code != 0 {
		t.Fatalf("assembling %s for the prep commit exited %d\n%s", version, code, log)
	}
	git(t, dir, "add", "CHANGELOG.md")
	git(t, dir, "commit", "-q", "-am", "Release v"+version)
	git(t, dir, "tag", "v"+version)
	git(t, dir, "checkout", "-q", "main")
}

// TestAssembleFoldsFragmentsUnderTheVersion: fragments merge into one
// dated section — entries in filename order, sections in
// keep-a-changelog order — inserted above the previous release, and
// the fragments are deleted from the index.
func TestAssembleFoldsFragmentsUnderTheVersion(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/b-two.md", "### Fixed\n\n- A fix\n\n### Added\n\n- Second entry\n", "#2 two")
	commit(t, dir, "changelog.d/a-one.md", "### Added\n\n- First entry\n", "#3 one")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"}, "RELEASE_DATE=2026-08-08")
	if code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}

	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	heading := strings.Index(changelog, "## [0.1.1] - 2026-08-08")
	if heading < 0 {
		t.Fatalf("no dated 0.1.1 heading:\n%s", changelog)
	}
	added := strings.Index(changelog, "### Added\n\n- First entry\n- Second entry")
	if added < 0 {
		t.Errorf("Added entries not merged in filename order:\n%s", changelog)
	}
	fixed := strings.Index(changelog, "### Fixed\n\n- A fix")
	if fixed < 0 || fixed < added {
		t.Errorf("Fixed must follow Added regardless of in-fragment order:\n%s", changelog)
	}
	previous := strings.Index(changelog, "## [0.1.0]")
	if previous < heading {
		t.Errorf("the new section must sit above the previous release:\n%s", changelog)
	}
	for _, f := range []string{"changelog.d/a-one.md", "changelog.d/b-two.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s still exists after assembly", f)
		}
	}
	if tracked := git(t, dir, "ls-files", "changelog.d/"); tracked != "" {
		t.Errorf("fragments still tracked after assembly: %s", tracked)
	}
}

// TestAssembleCarriesIntroProse: fragment text above the first heading
// becomes prose directly under the version heading, before any section.
func TestAssembleCarriesIntroProse(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/x.md", "A note about this release.\n\n### Added\n\n- An entry\n", "#2 x")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	if log, code := runScript(t, dir, assemble, []string{"0.1.1"}); code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}

	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	intro := strings.Index(changelog, "A note about this release.")
	section := strings.Index(changelog, "### Added\n\n- An entry")
	heading := strings.Index(changelog, "## [0.1.1]")
	if intro < 0 || section < 0 || !(heading < intro && intro < section) {
		t.Errorf("intro prose must sit between the heading and the first section:\n%s", changelog)
	}
}

// TestAssembleRefusesUnknownHeadings: the merge is by exact section
// name, so a typo must stop the assembly rather than orphan entries.
func TestAssembleRefusesUnknownHeadings(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/x.md", "### Fix\n\n- An entry\n", "#2 typo")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"})
	if code == 0 {
		t.Fatalf("assemble accepted an unknown heading\n%s", log)
	}
	if !strings.Contains(log, "fragments may only use") || !strings.Contains(log, "changelog.d/x.md") {
		t.Errorf("the error must name the fragment and the allowed headings, got:\n%s", log)
	}
	if got := readFile(t, filepath.Join(dir, "CHANGELOG.md")); got != changelogSeed {
		t.Errorf("CHANGELOG.md was modified by a refused assembly:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "changelog.d/x.md")); err != nil {
		t.Errorf("the fragment must survive a refused assembly: %v", err)
	}
}

// TestAssembleRefusesAnExistingSectionWithFragmentsLeft: a CHANGELOG
// already carrying the version plus unfolded fragments is an ambiguous
// state the script must not guess its way out of.
func TestAssembleRefusesAnExistingSectionWithFragmentsLeft(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "CHANGELOG.md",
		strings.Replace(changelogSeed, "## [0.1.0]", "## [0.1.1] - 2026-02-01\n\n### Added\n\n- Done by hand\n\n## [0.1.0]", 1),
		"#2 hand section")
	commit(t, dir, "changelog.d/x.md", "### Added\n\n- An entry\n", "#3 fragment")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"})
	if code == 0 {
		t.Fatalf("assemble accepted a version whose section already exists while fragments remain\n%s", log)
	}
	if !strings.Contains(log, "already has") {
		t.Errorf("expected the existing-section refusal, got:\n%s", log)
	}
}

// TestAssembleToleratesAnAlreadyAssembledTree: the manual release path
// assembles inside the feature PR, so the automated stage that follows
// finds the section present and nothing to fold — that is success.
func TestAssembleToleratesAnAlreadyAssembledTree(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "CHANGELOG.md",
		strings.Replace(changelogSeed, "## [0.1.0]", "## [0.1.1] - 2026-02-01\n\n### Added\n\n- Assembled early\n\n## [0.1.0]", 1),
		"#2 manual prep")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"})
	if code != 0 {
		t.Fatalf("assemble exited %d on an already-assembled tree\n%s", code, log)
	}
	if !strings.Contains(log, "already carries") {
		t.Errorf("expected a notice that nothing needed assembling, got:\n%s", log)
	}
}

// TestAssembleWithNothingToFoldIsANoop: a release with nothing to say
// (a merge whose fragment the gate did not require) must not fail the
// pipeline or touch the file.
func TestAssembleWithNothingToFoldIsANoop(t *testing.T) {
	dir := changelogRepo(t)

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"})
	if code != 0 {
		t.Fatalf("assemble exited %d with nothing to do\n%s", code, log)
	}
	if !strings.Contains(log, "leaving CHANGELOG.md unchanged") {
		t.Errorf("expected the no-op notice, got:\n%s", log)
	}
	if got := readFile(t, filepath.Join(dir, "CHANGELOG.md")); got != changelogSeed {
		t.Errorf("CHANGELOG.md changed on a no-op run:\n%s", got)
	}
}

// TestAssembleSkipsReleasedFragmentsAndRestoresTheirSections: the
// scenario the base-delta rule exists for. Release one is staged off
// main and tagged, but its fold-back PR never merges; release two must
// fold only the new fragment, restore release one's section from its
// tag, and still delete the stale fragment.
func TestAssembleSkipsReleasedFragmentsAndRestoresTheirSections(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/f1.md", "### Added\n\n- Feature one\n", "#2 one")
	stageRelease(t, dir, "0.1.1")
	commit(t, dir, "changelog.d/f2.md", "### Added\n\n- Feature two\n", "#3 two")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.2"}, "RELEASE_DATE=2026-08-09")
	if code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}
	if !strings.Contains(log, "restoring '## [0.1.1]' from its tag") {
		t.Errorf("expected the restore notice, got:\n%s", log)
	}

	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if strings.Count(changelog, "- Feature one") != 1 {
		t.Errorf("the released fragment must appear exactly once (in its restored section):\n%s", changelog)
	}
	newSection := strings.Index(changelog, "## [0.1.2] - 2026-08-09")
	restored := strings.Index(changelog, "## [0.1.1] - 2026-08-08")
	oldest := strings.Index(changelog, "## [0.1.0]")
	if newSection < 0 || restored < 0 || !(newSection < restored && restored < oldest) {
		t.Fatalf("sections must be reverse-chronological (0.1.2, 0.1.1, 0.1.0):\n%s", changelog)
	}
	if between := changelog[newSection:restored]; !strings.Contains(between, "- Feature two") ||
		strings.Contains(between, "- Feature one") {
		t.Errorf("0.1.2 must carry only the new fragment:\n%s", between)
	}
	for _, f := range []string{"changelog.d/f1.md", "changelog.d/f2.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s still exists after assembly", f)
		}
	}
}

// TestAssembleFoldsWhatAHandCutTagShippedUnassembled: a tag cut
// without running the script keeps its fragments in the tag's tree —
// the proof they were never folded. The next assembly must fold them
// rather than mistake them for released and delete their content.
func TestAssembleFoldsWhatAHandCutTagShippedUnassembled(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/f1.md", "### Added\n\n- Feature one\n", "#2 one")
	git(t, dir, "tag", "v0.1.1")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.2"})
	if code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}
	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if !strings.Contains(changelog, "- Feature one") {
		t.Errorf("the unassembled fragment's content was lost:\n%s", changelog)
	}
	if !strings.Contains(changelog, "## [0.1.2]") {
		t.Errorf("the late fold must land under the version being assembled:\n%s", changelog)
	}
}

// TestAssembleSkipsPreSystemTags: tags cut before the fragment system
// carry no section of their own; a later assembly must not fail on
// them or invent sections for them.
func TestAssembleSkipsPreSystemTags(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "code.go", "package p\n", "#2 code")
	git(t, dir, "tag", "v0.1.1")
	commit(t, dir, "changelog.d/f.md", "### Added\n\n- New work\n", "#3 fragment")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.2"})
	if code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}
	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if !strings.Contains(changelog, "## [0.1.2]") || !strings.Contains(changelog, "- New work") {
		t.Errorf("the new section is missing:\n%s", changelog)
	}
	if strings.Contains(changelog, "## [0.1.1]") {
		t.Errorf("a pre-system tag must not gain a section:\n%s", changelog)
	}
}

// TestReleaseStageAssemblesTheChangelog: the prep commit carries the
// folded CHANGELOG.md and no fragments, alongside the version stamps
// the other stage tests verify.
func TestReleaseStageAssemblesTheChangelog(t *testing.T) {
	dir, _ := modTree(t, "v0.1.0")
	commit(t, dir, "CHANGELOG.md", changelogSeed, "#2 changelog")
	commit(t, dir, "changelog.d/f.md", "### Added\n\n- Staged entry\n", "#3 fragment")
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	log, code := run(t, dir, "release-stage.sh", "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("stage exited %d\n%s", code, log)
	}

	committed := git(t, dir, "show", "HEAD:CHANGELOG.md")
	if !strings.Contains(committed, "## [0.1.1]") || !strings.Contains(committed, "- Staged entry") {
		t.Errorf("the prep commit's CHANGELOG.md is not assembled:\n%s", committed)
	}
	if tracked := git(t, dir, "ls-tree", "--name-only", "HEAD", "changelog.d/"); tracked != "" {
		t.Errorf("the prep commit still carries fragments: %s", tracked)
	}
}

// foldRepo builds the post-release shape changelog-fold.sh runs
// against: main pushed to a bare remote, one release staged off main
// and tagged, one fragment merged after the release started.
func foldRepo(t *testing.T) (dir, remote string) {
	t.Helper()
	remote = t.TempDir()
	git(t, remote, "init", "-q", "--bare")
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "remote", "add", "origin", remote)
	commit(t, dir, "CHANGELOG.md", changelogSeed, "root")
	git(t, dir, "tag", "v0.1.0")
	commit(t, dir, "changelog.d/f1.md", "### Added\n\n- Feature one\n", "#2 one")
	stageRelease(t, dir, "0.1.1")
	commit(t, dir, "changelog.d/f2.md", "### Added\n\n- Feature two\n", "#3 late merge")
	git(t, dir, "push", "-q", "origin", "HEAD:main")
	return dir, remote
}

// TestChangelogFoldBringsMainInLine: after a release, the fold branch
// carries the tag's CHANGELOG.md, drops the fragments that release
// folded, keeps the ones it did not, and is pushed for a PR.
func TestChangelogFoldBringsMainInLine(t *testing.T) {
	dir, _ := foldRepo(t)
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	fold := bashScript(t, ".github", "scripts", "changelog-fold.sh")
	log, code := runScript(t, dir, fold, nil, "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("fold exited %d\n%s", code, log)
	}

	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if !strings.Contains(changelog, "## [0.1.1] - 2026-08-08") || !strings.Contains(changelog, "- Feature one") {
		t.Errorf("CHANGELOG.md does not carry the released section:\n%s", changelog)
	}
	if _, err := os.Stat(filepath.Join(dir, "changelog.d/f1.md")); err == nil {
		t.Errorf("the folded fragment must be deleted")
	}
	if _, err := os.Stat(filepath.Join(dir, "changelog.d/f2.md")); err != nil {
		t.Errorf("a fragment merged after the release started must stay: %v", err)
	}
	if subject := git(t, dir, "log", "-1", "--pretty=%s"); subject != "Fold the v0.1.1 changelog into main" {
		t.Errorf("fold commit subject = %q", subject)
	}
	if got := readOutput(t, out)["branch"]; got != "changelog/v0.1.1" {
		t.Errorf("branch = %q, want changelog/v0.1.1", got)
	}
	heads := git(t, dir, "ls-remote", "--heads", "origin", "changelog/v0.1.1")
	if !strings.Contains(heads, "changelog/v0.1.1") {
		t.Errorf("the fold branch was not pushed:\n%s", heads)
	}
}

// TestChangelogFoldIsANoopOnceMerged: with main already in line (here:
// the fold committed locally), a re-run must report nothing to fold
// and name no branch, so the workflow skips the PR step.
func TestChangelogFoldIsANoopOnceMerged(t *testing.T) {
	dir, _ := foldRepo(t)
	fold := bashScript(t, ".github", "scripts", "changelog-fold.sh")
	if log, code := runScript(t, dir, fold, nil, "TAG=v0.1.1"); code != 0 {
		t.Fatalf("first fold exited %d\n%s", code, log)
	}

	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log, code := runScript(t, dir, fold, nil, "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("second fold exited %d\n%s", code, log)
	}
	if !strings.Contains(log, "nothing to fold") {
		t.Errorf("expected the nothing-to-fold notice, got:\n%s", log)
	}
	if got := readOutput(t, out)["branch"]; got != "" {
		t.Errorf("branch = %q, want empty on a no-op", got)
	}
}

// TestChangelogFoldPreservesPreambleEdits: the fold inserts the tag's
// section into main's file rather than replacing the file, so a
// preamble edit merged while the release ran survives.
func TestChangelogFoldPreservesPreambleEdits(t *testing.T) {
	dir, _ := foldRepo(t)
	commit(t, dir, "CHANGELOG.md",
		strings.Replace(changelogSeed, "Assembled, not edited.", "Assembled, not edited.\nPreamble note merged mid-release.", 1),
		"#4 preamble edit")

	fold := bashScript(t, ".github", "scripts", "changelog-fold.sh")
	if log, code := runScript(t, dir, fold, nil, "TAG=v0.1.1"); code != 0 {
		t.Fatalf("fold exited %d\n%s", code, log)
	}
	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if !strings.Contains(changelog, "Preamble note merged mid-release.") {
		t.Errorf("the fold reverted a preamble edit merged during the release:\n%s", changelog)
	}
	if !strings.Contains(changelog, "## [0.1.1] - 2026-08-08") {
		t.Errorf("the released section was not restored:\n%s", changelog)
	}
}

// TestChangelogFoldSurvivesStackedReleases: two releases staged off
// the same main with neither fold merged. The newer release's fold
// carries both sections, and re-running the older release's fold
// afterwards must find nothing to do — sections are inserted, never
// the file overwritten, so a stale fold can never delete a newer one.
func TestChangelogFoldSurvivesStackedReleases(t *testing.T) {
	dir, _ := foldRepo(t)
	stageRelease(t, dir, "0.1.2")

	fold := bashScript(t, ".github", "scripts", "changelog-fold.sh")
	if log, code := runScript(t, dir, fold, nil, "TAG=v0.1.2"); code != 0 {
		t.Fatalf("fold for the newer release exited %d\n%s", code, log)
	}
	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	newer := strings.Index(changelog, "## [0.1.2]")
	older := strings.Index(changelog, "## [0.1.1]")
	oldest := strings.Index(changelog, "## [0.1.0]")
	if newer < 0 || older < 0 || !(newer < older && older < oldest) {
		t.Fatalf("the newer fold must carry both sections in order:\n%s", changelog)
	}
	if !strings.Contains(changelog, "- Feature one") || !strings.Contains(changelog, "- Feature two") {
		t.Fatalf("both releases' entries must be present:\n%s", changelog)
	}

	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(out, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	log, code := runScript(t, dir, fold, nil, "TAG=v0.1.1", "GITHUB_OUTPUT="+out)
	if code != 0 {
		t.Fatalf("re-running the older release's fold exited %d\n%s", code, log)
	}
	if !strings.Contains(log, "nothing to fold") {
		t.Errorf("the stale fold must be a no-op, got:\n%s", log)
	}
	if got := readOutput(t, out)["branch"]; got != "" {
		t.Errorf("branch = %q, want empty for a stale fold", got)
	}
	if after := readFile(t, filepath.Join(dir, "CHANGELOG.md")); after != changelog {
		t.Errorf("a stale fold changed CHANGELOG.md:\n%s", after)
	}
}

// TestAssembleDiscardsEmptyFragments: a blank fragment must not
// publish an empty version section; it is deleted with a warning.
func TestAssembleDiscardsEmptyFragments(t *testing.T) {
	dir := changelogRepo(t)
	commit(t, dir, "changelog.d/empty.md", "\n", "#2 empty")

	assemble := bashScript(t, "scripts", "assemble-changelog.sh")
	log, code := runScript(t, dir, assemble, []string{"0.1.1"})
	if code != 0 {
		t.Fatalf("assemble exited %d\n%s", code, log)
	}
	if !strings.Contains(log, "empty") {
		t.Errorf("expected a warning about the empty fragment, got:\n%s", log)
	}
	changelog := readFile(t, filepath.Join(dir, "CHANGELOG.md"))
	if strings.Contains(changelog, "## [0.1.1]") {
		t.Errorf("an empty fragment must not publish a section:\n%s", changelog)
	}
	if _, err := os.Stat(filepath.Join(dir, "changelog.d/empty.md")); err == nil {
		t.Errorf("the empty fragment must still be deleted")
	}
}

// gate runs changelog-gate.sh against the repository's main (base) and
// current HEAD.
func gate(t *testing.T, dir string) (log string, code int) {
	t.Helper()
	script := bashScript(t, ".github", "scripts", "changelog-gate.sh")
	base := git(t, dir, "rev-parse", "main")
	head := git(t, dir, "rev-parse", "HEAD")
	return runScript(t, dir, script, nil, "BASE="+base, "HEAD="+head)
}

// TestChangelogGate covers the CI rule: a releasing change needs an
// added fragment, non-releasing changes need nothing, and hand-written
// CHANGELOG.md sections are rejected unless they belong to a fold PR
// (released tag) or the manual release-prep path (.version match).
func TestChangelogGate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history func(t *testing.T, dir string)
		pass    bool
		message string
	}{{
		name: "a code change without a fragment fails",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 code")
		},
		pass: false, message: "adds no changelog fragment",
	}, {
		name: "a code change with an added fragment passes",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 code")
			commit(t, dir, "changelog.d/feat.md", "### Added\n\n- An entry\n", "#2 fragment")
		},
		pass: true, message: "changelog fragment(s) present",
	}, {
		name: "docs and markdown changes need no fragment",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "README.md", "docs v2\n", "#2 docs")
			commit(t, dir, "docs/official/pages/index.md", "page\n", "#3 site")
			commit(t, dir, ".github/workflows/ci.yml", "name: CI\n", "#4 workflow")
		},
		pass: true, message: "no releasing changes",
	}, {
		name: "editing someone else's fragment does not satisfy the rule",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 code")
			commit(t, dir, "changelog.d/old.md", "### Added\n\n- Reworded\n", "#2 edit")
		},
		pass: false, message: "adds no changelog fragment",
	}, {
		name: "a hand-written section for an unreleased version fails",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [9.9.9] - 2026-08-08\n\n### Added\n\n- By hand\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n",
				"#2 hand section")
		},
		pass: false, message: "Do not write changelog sections by hand",
	}, {
		name: "a fold PR restoring released sections passes",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [0.1.1] - 2026-08-08\n\n### Added\n\n- Released work\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n",
				"#2 fold")
			commit(t, dir, "docs/official/content/docs/CHANGELOG.md", "synced copy\n", "#2 sync")
		},
		pass: true, message: "no releasing changes",
	}, {
		// The released-tag section shape must not exempt the rest of
		// the PR: only the manual-prep shape (below) short-circuits.
		name: "editing a released heading does not exempt code from the fragment rule",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [0.1.0] - 2026-01-02\n\n### Added\n\n- First release\n",
				"#2 date fix")
			commit(t, dir, "main.go", "package p // v2\n", "#3 code")
		},
		pass: false, message: "adds no changelog fragment",
	}, {
		// The assembler only folds flat files (find -maxdepth 1); a
		// nested path passing the gate would ship no entry, ever.
		name: "a fragment in a subdirectory does not count",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "main.go", "package p // v2\n", "#2 code")
			commit(t, dir, "changelog.d/feat/nested.md", "### Added\n\n- An entry\n", "#2 nested")
		},
		pass: false, message: "adds no changelog fragment",
	}, {
		name: "a manual release-prep PR assembling its own section passes",
		history: func(t *testing.T, dir string) {
			commit(t, dir, ".version", "0.2.0", "#2 bump")
			commit(t, dir, "adapters/chi/go.mod", "require github.com/go-monolith/ghtmx v0.2.0\n", "#2 stamp")
			commit(t, dir, "internal/wasmcheck/fixture/go.mod", "require github.com/go-monolith/ghtmx v0.2.0\n", "#2 stamp")
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [0.2.0] - 2026-08-08\n\n### Added\n\n- Minor release work\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n",
				"#3 assemble")
		},
		pass: true, message: "manual release-prep PR assembles its own",
	}, {
		// A forged .version plus a hand-written section must not exempt
		// unrelated code from the fragment rule — the release-prep
		// exemption is earned by the pure stamp shape only.
		name: "a manual-prep shape smuggling extra code fails",
		history: func(t *testing.T, dir string) {
			commit(t, dir, ".version", "9.9.9", "#2 forged bump")
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [9.9.9] - 2026-08-08\n\n### Added\n\n- Forged\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n",
				"#3 forged section")
			commit(t, dir, "main.go", "package p // smuggled\n", "#4 smuggled code")
		},
		pass: false, message: "beyond the version stamps",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "-q", "-b", "main")
			commit(t, dir, "main.go", "package p\n", "root")
			commit(t, dir, "CHANGELOG.md",
				"# Changelog\n\n## [0.1.0] - 2026-01-01\n\n### Added\n\n- First release\n", "#1 changelog")
			commit(t, dir, "changelog.d/old.md", "### Added\n\n- Old entry\n", "#1 old fragment")
			git(t, dir, "tag", "v0.1.0")
			git(t, dir, "checkout", "-q", "-b", "feature")
			tc.history(t, dir)

			log, code := gate(t, dir)
			if tc.pass && code != 0 {
				t.Errorf("gate exited %d, want 0\n%s", code, log)
			}
			if !tc.pass && code == 0 {
				t.Errorf("gate passed, want failure\n%s", log)
			}
			if !strings.Contains(log, tc.message) {
				t.Errorf("log must contain %q, got:\n%s", tc.message, log)
			}
		})
	}
}

// TestChangelogFoldLagGate covers the fold-lag rule (issue #46): every
// release parks its assembled sections on a changelog/<tag> branch that
// a human has to merge, so one unfolded release is expected lag and two
// mean the fold pull requests are being dropped. The count is taken at
// HEAD so a fold PR — whose own base is the lagging main — passes.
func TestChangelogFoldLagGate(t *testing.T) {
	// releasedSection is a CHANGELOG.md whose newest heading is v.
	releasedSection := func(versions ...string) string {
		out := "# Changelog\n\nAssembled, not edited.\n"
		for _, v := range versions {
			out += "\n## [" + v + "] - 2026-08-08\n\n### Added\n\n- Work for " + v + "\n"
		}
		return out
	}

	for _, tc := range []struct {
		name    string
		history func(t *testing.T, dir string)
		pass    bool
		message string
	}{{
		// The shape of a normal pull request opened the day after a
		// release: the fold for that release is still open.
		name: "one unfolded release is expected lag",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			commit(t, dir, "README.md", "docs\n", "#2 docs")
		},
		pass: true, message: "one release behind",
	}, {
		// Exactly the state issue #46 reports on main.
		name: "two unfolded releases fail",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "tag", "v0.1.2")
			commit(t, dir, "README.md", "docs\n", "#2 docs")
		},
		pass: false, message: "2 releases have shipped since",
	}, {
		name: "the failure names the fold branch to merge",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "tag", "v0.1.2")
			git(t, dir, "tag", "v0.1.3")
			commit(t, dir, "README.md", "docs\n", "#2 docs")
		},
		pass: false, message: "gh pr create --base main --head changelog/v0.1.3",
	}, {
		// The deadlock this rule must not create: the fold PR's base is
		// the lagging main, so a base-measured gate would fail the one
		// pull request that fixes the lag.
		name: "the fold pull request that fixes the lag passes",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "tag", "v0.1.2")
			commit(t, dir, "CHANGELOG.md", releasedSection("0.1.2", "0.1.1", "0.1.0"), "#2 fold")
		},
		pass: true, message: "no releasing changes",
	}, {
		// Tags cut before the fragment system have no section and never
		// will; the assembler skips them, so the gate must too.
		name: "releases older than the newest section are not counted",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.0.8")
			git(t, dir, "tag", "v0.0.9")
			commit(t, dir, "README.md", "docs\n", "#2 docs")
		},
		pass: true, message: "no releasing changes",
	}, {
		// The false positive the base guard exists for: an ordinary
		// branch cut before the outstanding fold merged carries a stale
		// CHANGELOG.md at its head, but its base is current. The lag is
		// the branch's, not the repository's, so the fold-lag rule must
		// not claim the folds are being dropped.
		name: "a stale branch whose base is current passes",
		history: func(t *testing.T, dir string) {
			commit(t, dir, "README.md", "docs\n", "#2 docs")
			git(t, dir, "checkout", "-q", "main")
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "tag", "v0.1.2")
			commit(t, dir, "CHANGELOG.md",
				releasedSection("0.1.2", "0.1.1", "0.1.0"), "#3 fold")
			git(t, dir, "checkout", "-q", "feature")
		},
		pass: true, message: "its base is current",
	}, {
		// A section for a version that has not shipped is the manual
		// release-prep shape: nothing has been folded because nothing
		// has been released.
		name: "an unreleased newest section reports no lag",
		history: func(t *testing.T, dir string) {
			git(t, dir, "tag", "v0.1.1")
			git(t, dir, "tag", "v0.1.2")
			commit(t, dir, ".version", "0.2.0", "#2 bump")
			commit(t, dir, "CHANGELOG.md", releasedSection("0.2.0", "0.1.0"), "#3 assemble")
		},
		pass: true, message: "manual release-prep PR assembles its own",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			git(t, dir, "init", "-q", "-b", "main")
			commit(t, dir, "main.go", "package p\n", "root")
			commit(t, dir, "CHANGELOG.md", releasedSection("0.1.0"), "#1 changelog")
			git(t, dir, "tag", "v0.1.0")
			git(t, dir, "checkout", "-q", "-b", "feature")
			tc.history(t, dir)

			log, code := gate(t, dir)
			if tc.pass && code != 0 {
				t.Errorf("gate exited %d, want 0\n%s", code, log)
			}
			if !tc.pass && code == 0 {
				t.Errorf("gate passed, want failure\n%s", log)
			}
			if !strings.Contains(log, tc.message) {
				t.Errorf("log must contain %q, got:\n%s", tc.message, log)
			}
		})
	}
}
