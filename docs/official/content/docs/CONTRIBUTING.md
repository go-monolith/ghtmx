# Contributing

## Change workflow

`main` is protected: every change lands through a feature branch and a
pull request. CI runs 13 checks — the 3-OS × 2-Go-version test matrix,
plus perf-gate, lint, ensure-generated, vulncheck, fuzz, coverage, and
the changelog-fragment gate.

Eight of them are *required* to merge: the two ubuntu matrix rows,
perf-gate, lint, ensure-generated, vulncheck, fuzz, and the
changelog-fragment gate. The macOS and Windows matrix rows run on every
pull request but are not required status checks, so a failure there
does not block a merge — treat one as a real failure anyway. Direct
pushes to `main` are rejected, including for administrators.

1. Branch from `main` (`feat/...`, `fix/...`, or similar).
2. Push the branch and open a pull request against `main`.
3. Add a changelog fragment — a new flat file
   `changelog.d/$(git branch --show-current | tr / -).md` with your
   entries (see `changelog.d/README.md`; slashes in branch names become
   dashes — subdirectories are rejected). Never edit `CHANGELOG.md`
   itself: it is assembled at release time, and CI rejects hand-written
   sections. Docs- and workflow-only PRs, which no release ships, need
   no fragment.
4. Merge only when CI is green and any review feedback is addressed.

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

### Coverage

The `coverage` job holds statement coverage of the project's own code at
90% or above. "Own code" excludes generated `*_ghtmx.go` output, the
ported `internal/lsp` and `internal/safehtml` trees, and the example and
fixture programs. To run the same check locally:

```sh
go install golang.org/x/tools/gopls@v0.23.0
go test ./... -covermode=atomic -coverpkg=./... -coverprofile=cover.out -timeout 20m
GHTMX_COVERAGE_GATE=1 go test ./internal/covergate/ -count=1 -v
```

The `gopls` install is a real prerequisite, not boilerplate: without it
the `cmd/ghtmx/lspcmd` tests fail and there is no profile to measure. It
is only needed once per machine. On failure the gate prints the
least-covered packages, worst first.

Two flags in that command are load-bearing. `-coverpkg=./...` attributes
coverage across package boundaries, so a package exercised only through
its callers still counts; without it the figure is about ten points
lower and not comparable to the threshold. `-covermode=atomic` is
required because the suite runs tests in parallel and the other modes
race on the counter array.

The gate parses the profile itself rather than calling `go tool cover
-func`, which silently drops coverage blocks inside function literals
assigned to package-level vars — the parser-combinator shape
`internal/parser` is built from — and undercounts by roughly eight
percentage points.

The same job uploads its profile to
[Codecov](https://codecov.io/gh/go-monolith/ghtmx), which is where the
badge, the line-by-line report, and a pull request's coverage diff come
from. Codecov posts its own `codecov/project` and `codecov/patch`
contexts alongside the thirteen checks above; `codecov.yml` marks both
informational, so neither can fail or block a merge. The 90% floor is
the only coverage failure that means anything.

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
