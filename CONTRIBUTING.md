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

`go build ./...` type-checks every package without writing binaries, so
it leaves no artifacts behind.

## Build output

Compiled binaries go in `bin/`, which is git-ignored. Always pass `-o`:

```sh
go build -o bin/ghtmx ./cmd/ghtmx
go build -o bin/crud ./examples/crud/cmd
```

A bare `go build ./cmd/ghtmx` writes the binary into the current
directory instead, and a Go binary has no file extension for the usual
`*.exe`/`*.so` ignore rules to catch. That combination once put two 9 MB
example binaries into the repo root and into git history; they have
since been purged, and the root-anchored entries in `.gitignore` guard
against a repeat.

## Dev environment

Checked-in tooling config, all optional to use:

| File | Purpose |
| --- | --- |
| `.devcontainer/devcontainer.json` | Dev container image, mounts, and VS Code extensions |
| `.mcp.json` | MCP servers available to agent sessions in this repo |
| `.claude/settings.json` | Claude Code defaults for this repo |
| `.codex/config.toml` | Codex CLI defaults for this repo |

`CLAUDE.md` files and `rules/` in the home directory configure agent
sessions too; repo-level conventions live here.
