# Contributing

## Change workflow

`main` is protected: every change lands through a feature branch and a
pull request, and all 11 CI checks (the 3-OS × 2-Go-version test
matrix, perf-gate, lint, ensure-generated, vulncheck, and fuzz) must
pass before merge. Direct pushes to `main` are rejected, including for
administrators.

1. Branch from `main` (`feat/...`, `fix/...`, or similar).
2. Push the branch and open a pull request against `main`.
3. Merge only when CI is green and any review feedback is addressed.

## Pushing from the dev container

The dev container has no SSH key. Git must talk to GitHub over HTTPS
with the `gh` CLI as the credential helper:

```sh
git remote set-url origin https://github.com/go-monolith/ghtmx.git
gh auth setup-git
```

If a push fails with an SSH "correct access rights" error, the remote
is still on the `git@github.com:` form — rerun the two commands above.

## Local checks

Before opening a pull request, run what CI runs:

```sh
go build ./... && go test ./... && gofmt -l . && go vet ./...
```

`CLAUDE.md` files and `rules/` in the home directory configure agent
sessions; repo-level conventions live here.
