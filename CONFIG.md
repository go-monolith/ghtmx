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
| `sourceDirs` | `["."]` | `-source-dir` (repeatable) | Directories walked for `.ghtmx` templates. |
| `routeScope` | `["./..."]` | `-route-scope` (repeatable) | Package patterns route discovery loads. |
| `generatedPackage.dir` | `ghtmxgen` | `-generated-pkg-dir` | Directory of the central generated package. |
| `generatedPackage.name` | `ghtmxgen` | `-generated-pkg-name` | Package name of the central generated package. |
| `generatedSuffix` | `_ghtmx.go` | `-generated-suffix` | Suffix of generated files. |
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
| `-watch-pattern` | `(.+\.go$)\|(.+\.ghtmx$)` | Regexp of files the watcher observes. |
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

Other subcommands (`fmt`, `routes`, `lsp`) document their flags in
`ghtmx <cmd> -help`; the LSP's are also listed in
`editors/vscode/README.md`.
