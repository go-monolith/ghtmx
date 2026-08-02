# CLAUDE.md

- Never push directly to `main` — it is branch-protected, admins
  included. CI runs 12 checks; 8 are required to merge (the two ubuntu
  matrix rows plus the six single-OS jobs). The macOS and Windows matrix
  rows run but are not required status checks. Make changes on a feature
  branch and open a pull request.
- Merge only when the PR's CI checks are green.
- Pushing needs HTTPS with the gh credential helper — this container
  has no SSH key. If pushes fail over SSH:
  `git remote set-url origin https://github.com/go-monolith/ghtmx.git && gh auth setup-git`
- Full contribution flow: see CONTRIBUTING.md.
- After changing anything under `examples/` or the reference docs
  (README.md, SYNTAX.md, DIAGNOSTICS.md, CONFIG.md, CONFORMANCE.md,
  TEMPL_SYNTAX_BASELINE.md, CHANGELOG.md, CONTRIBUTING.md, RELEASING.md,
  docs/site/*.md — the full list is docs/official/internal/sync/manifest.go),
  refresh the
  docs site's embedded copies: `cd docs/official && go run ./internal/sync`
  — then commit the updated files under `docs/official/content/`. CI's
  drift gate fails otherwise.
