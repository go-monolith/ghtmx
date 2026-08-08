### Changed

- How changelog entries are written: add a fragment in `changelog.d/`
  instead of editing `CHANGELOG.md`'s `[Unreleased]` section. One file
  per PR means two open PRs can no longer conflict on the same lines,
  and nobody writes a version number — the release automation folds the
  fragments into `CHANGELOG.md` under the version it actually assigns,
  inside the release-prep commit the tag points at, and opens a
  `changelog/<tag>` pull request that brings the fold back to the
  protected `main` (and, through it, to the changelog page on the docs
  site). Migration: none required for users; contributors write
  `changelog.d/<branch>.md` files from now on — see
  `changelog.d/README.md`.

### Added

- A CI check that fails a pull request changing code a release will
  ship without a changelog fragment, and rejects hand-written
  `CHANGELOG.md` version sections, so the mandatory-changelog rule is
  enforced rather than remembered. `internal/installcheck` lints the
  fragments themselves: known section headings only, and the pre-1.0
  `Migration:` discipline applies before entries ever reach
  `CHANGELOG.md`.
