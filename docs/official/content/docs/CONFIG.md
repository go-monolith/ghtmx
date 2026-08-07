# Configuration Reference

ghtmx reads `ghtmx.json` at the module root (FR-071). Absence is fine:
every setting has a default. Precedence, highest first: **CLI flag >
`ghtmx.json` > default**. `internal/catalogcheck` fails the build if a
setting exists in `internal/config` without an entry here, or a
`ghtmx generate` flag is registered without being documented.

## Settings

| Setting | Default | CLI flag (`ghtmx generate`) | Meaning |
| --- | --- | --- | --- |
| `htmxVersion` | `2.0.10` | `-htmx-version` | The pinned htmx version: attribute validation surface and the `ghtmxgen.HTMXScript()` asset. |
| `sourceDirs` | `["."]` | `-source-dir` (repeatable) | Directories walked for templates. |
| `routeScope` | `["./..."]` | `-route-scope` (repeatable) | Package patterns route discovery loads. |
| `generatedPackage.dir` | `ghtmxgen` | `-generated-pkg-dir` | Directory of the central generated package. |
| `generatedPackage.name` | `ghtmxgen` | `-generated-pkg-name` | Package name of the central generated package. |
| `generatedSuffix` | `_ghtmx.go` | `-generated-suffix` | Suffix of generated files. |
| `templateExtension` | `.ghtmx` | `-template-extension` | Extension templates are written with: `.ghtmx` or `.htmx`. A project uses exactly one — files with the other extension are not templates, so they are neither generated from nor served by the language server. |
| `checks` | `{}` | `-check-severity` (repeatable, `ID=severity`) | Per-check severity overrides (`error`, `warning`, `off`). Warning-class checks only; errors cannot be silenced. See `DIAGNOSTICS.md`. |
| `strictTargets` | `false` | `-strict-targets` | Promotes `GHTMX-W0201` (dangling targets) to an error. |

## `ghtmx generate` flags without a `ghtmx.json` setting

Behavioral flags for a single invocation:

| Flag | Default | Meaning |
| --- | --- | --- |
| `-f` | | Generate a single file instead of walking. |
| `-path` | `.` | Root to walk (where `ghtmx.json` is loaded from). |
| `-stdout` | `false` | Write generated output to stdout (only with `-f`; rejected with `-check`). |
| `-source-map-visualisations` | `false` | Emit HTML source-map visualisations. |
| `-include-version` | `true` | Stamp the generator version into output headers. |
| `-include-timestamp` | `false` | Stamp generation time into output headers. |
| `-watch` | `false` | Watch mode: regenerate on change, two-tier invalidation. |
| `-watch-pattern` | `(.+\.go$)\|(.+\.ghtmx$)` | Regexp of files the watcher observes. The default follows `templateExtension`. |
| `-ignore-pattern` | | Regexp of paths the watcher ignores. |
| `-cmd` | | Command to run (and restart on Go changes) after generation. |
| `-open-browser` | `true` | Open the proxy URL once serving (with `-proxy`). |
| `-proxy` | | Dev-server target URL for SSE live reload. |
| `-proxyport` | `7331` | Dev-server listen port. |
| `-proxybind` | `127.0.0.1` | Dev-server bind address. |
| `-proxy-tls-crt` / `-proxy-tls-key` | | TLS material for the dev proxy. |
| `-notify-proxy` | `false` | Ping a running dev server to reload, then exit. |
| `-w` | CPU count | Worker parallelism. |
| `-pprof` | `0` | pprof listen port (0 disables). |
| `-keep-orphaned-files` | `false` | Keep generated files whose template is gone. |
| `-lazy` | `false` | Skip regeneration when the artifact is newer than the template (forced off by `-check`). |
| `-cache` | `true` | Use the on-disk content-hash build cache. |
| `-check` | `false` | Verify mode: write nothing, report `GHTMX-W0301` drift, exit non-zero on findings. |
| `-v` / `-log-level` | `info` | Logging (`-v` = debug). |
| `-help` | | Usage. |

The ignore files `.ghtmxignore_generate` and `.ghtmxignore_fmt` (glob
patterns, directory-prefix matched) exclude paths from the generate
walk and `ghtmx fmt` respectively.

## Other subcommands

The flags most likely to end up in a Makefile or CI job:

| Command | Flag | Meaning |
| --- | --- | --- |
| `ghtmx fmt` | `-fail` | Write nothing; exit non-zero if any file would change — the CI form of `ghtmx fmt`. |
| `ghtmx routes` | `-json` | Print the discovered route table as JSON (verb, path, handler, origin, recognizer, source position) instead of text. |

The full flag lists are in `ghtmx <cmd> -help`; the LSP's are also
listed in `editors/vscode/README.md`.

## Dev-mode environment variables

In watch mode the generator writes each template's string literals to a
sidecar text file, and generated code re-reads them at render time —
so edits to template text show up on the next request without
recompiling (Go-code changes still rebuild). `ghtmx generate -watch
-cmd <run your app>` sets the variables for the child process
automatically; set them by hand only when running the application some
other way during development:

| Variable | Meaning |
| --- | --- |
| `GHTMX_DEV_MODE` | `true` makes generated code read string literals from the sidecar files. |
| `GHTMX_DEV_MODE_WATCH_ROOT` | Directory tree whose templates participate; files outside it render their compiled strings. |
| `GHTMX_DEV_MODE_ROOT` | Where sidecar files live (default: the OS temp directory). |

Never set `GHTMX_DEV_MODE` in production: it trades rendering speed for
reloadability.
