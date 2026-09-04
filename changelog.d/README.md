# changelog.d — one fragment per PR

Every change a release ships gets a changelog entry (CI enforces it).
Instead of editing `CHANGELOG.md` directly, **add a new file here**:

```sh
cat > changelog.d/$(git branch --show-current | tr / -).md <<'EOF'
### Fixed

- The thing that used to happen
EOF
```

Name it after your branch with slashes flattened to dashes
(`feat/thing` → `feat-thing.md`), or anything unique to your PR. It
must be a flat `.md` file directly in `changelog.d/` — the assembler
never looks in subdirectories, and CI rejects them. The name never
appears in the output — it exists only so two PRs can never touch the
same file.

## What goes in it

The same shape a `CHANGELOG.md` release section has, minus the version
heading — the version is added when the release is cut:

```markdown
### Added

- `templateExtension` in `ghtmx.json`: the file extension templates are
  written with, `.ghtmx` by default.

### Fixed

- Dev-mode hot literal reload for projects using a non-default template
  extension.
```

- Use only these section headings, exactly: `### Added`, `### Changed`,
  `### Deprecated`, `### Removed`, `### Fixed`, `### Security`.
  `internal/installcheck` fails the build on anything else, because the
  assembler merges sections by name and a typo would orphan its entries.
- Every entry in a `### Changed` or `### Removed` section must carry a
  `Migration:` note (`Migration: none required` is valid) — the same
  pre-1.0 discipline `CHANGELOG.md` has always enforced.
- Every fragment needs at least one `- ` bullet under a section
  heading, and no other heading-like lines — both are checked by
  `internal/installcheck`.
- Prose above the first heading becomes intro text directly under the
  version heading; almost no fragment needs any.
- Write what the change MEANS for the reader, not what the diff did.
  GitHub already generates a list of PR titles per release; this file is
  for what a title cannot carry — the bounds of a new option, what a
  changed default now does, what a fix stops happening.

## Why not just edit CHANGELOG.md

Because every PR inserted its entries at the same place — the
`[Unreleased]` section of one shared file — so two open PRs always
conflicted on the same lines even when the changes were unrelated. And
nobody could write the real version number: releases are cut
automatically on merge (`RELEASING.md`), so the number is only knowable
after the fact, which is why `[Unreleased]` sat unattributed forever.

One file per PR makes the conflict structurally impossible, and the
release automation supplies the version at the only moment it is a fact.

## What happens to it

On the release cut from your merge, `auto-release.yml` runs
`scripts/assemble-changelog.sh <version>`, which folds every fragment
here into `CHANGELOG.md` under a `## [<version>] - <date>` heading —
inside the release-prep commit the tag points at. `main` is
branch-protected, so the same fold comes back to it as an automated
`changelog/<tag>` pull request; merging that PR updates the changelog
page on the docs site and deletes the fragments here.

**Merge that pull request.** Until it lands, `main`'s `CHANGELOG.md`
stays at the previous version — one release of lag is expected, and the
next release's fold carries the earlier sections too. Two releases of
lag is not expected, and `changelog-gate.sh` fails every pull request
until the outstanding fold is merged: the automation cannot open the PR
itself unless the repository setting *Allow GitHub Actions to create
and approve pull requests* is on, so when it is off the branch is
pushed and nothing else happens. Open it by hand —
`gh pr create --base main --head changelog/<tag>` — and merge it when
green; it is markdown- and docs-only and cuts no release.

Fragments are concatenated in filename order, section by section. If
two entries need a specific order relative to each other, put them in
one fragment.

**Manual releases are the exception** (`RELEASING.md`): the release-prep
commit is yours, so run `bash scripts/assemble-changelog.sh X.Y.Z`
yourself and commit the result along with the re-synced docs copies
(`cd docs/official && go run ./internal/sync`).
