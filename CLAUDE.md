# CLAUDE.md

- Never push directly to `main` — it is branch-protected (all 11 CI
  checks required, admins included). Make changes on a feature branch,
  open a pull request, and put `@CharlieHelps please review` in the PR
  description.
- Merge only when the PR's CI checks are green.
- Pushing needs HTTPS with the gh credential helper — this container
  has no SSH key. If pushes fail over SSH:
  `git remote set-url origin https://github.com/go-monolith/ghtmx.git && gh auth setup-git`
- Full contribution flow: see CONTRIBUTING.md.
