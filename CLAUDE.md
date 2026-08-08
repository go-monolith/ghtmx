# CLAUDE.md

- Never push directly to `main` — it is branch-protected, admins
  included. CI runs 13 checks; 8 are required to merge (the two ubuntu
  matrix rows, perf-gate, lint, ensure-generated, vulncheck, fuzz, and
  the changelog-fragment gate). The macOS and Windows matrix rows and
  the coverage job run but are not required status checks. Make changes
  on a feature branch and open a pull request.
- Merge only when the PR's CI checks are green.
- Every PR that changes code a release will ship MUST add a changelog
  fragment: a new flat file `changelog.d/<branch-name>.md` — flatten
  slashes in the branch name to dashes (`feat/x` → `feat-x.md`);
  subdirectories are rejected — with `### Added` / `### Changed` /
  `### Fixed` sections (see `changelog.d/README.md`).
  Never edit `CHANGELOG.md` by hand and never write a version number —
  the release automation folds fragments in under the real version, and
  CI rejects hand-written sections. `Changed`/`Removed` entries need a
  `Migration:` note. Docs-, markdown-, and workflow-only PRs need no
  fragment.
- Pushing needs HTTPS with the gh credential helper — this container
  has no SSH key. If pushes fail over SSH:
  `git remote set-url origin https://github.com/go-monolith/ghtmx.git && gh auth setup-git`
- Full contribution flow: see CONTRIBUTING.md.
- After changing anything under `examples/` or the reference docs
  (README.md, SYNTAX.md, DIAGNOSTICS.md, CONFIG.md, CONFORMANCE.md,
  TEMPL_SYNTAX_BASELINE.md, CHANGELOG.md, CONTRIBUTING.md, RELEASING.md,
  docs/official/pages/*.md — the full list is
  docs/official/internal/sync/manifest.go),
  refresh the
  docs site's embedded copies: `cd docs/official && go run ./internal/sync`
  — then commit the updated files under `docs/official/content/`. CI's
  drift gate fails otherwise.
