// Everything the "ghtmx is missing" flow needs to decide what to say and
// what to run, with no dependency on the vscode API — so it is unit
// testable without an editor host. extension.ts holds the API calls.

// The installer is fetched from the default branch rather than a tag:
// an extension version identifies a module *series* (a 0.1.* extension
// works with any v0.1.* module), not a single release, so there is no
// tag for it to name. See editors/README.md.
//
// The consequence, accepted pre-1.0: the script installs the newest
// release, which is not guaranteed to be in this extension's series.
// Users who need a specific one set GHTMX_VERSION and run it themselves;
// editors/vscode/README.md says so.
export const INSTALL_SCRIPT_URL =
  "https://raw.githubusercontent.com/go-monolith/ghtmx/main/scripts/install.sh";

export const INSTALL_ACTION = "Install ghtmx…";
export const SET_PATH_ACTION = "Set path…";
export const RELOAD_ACTION = "Reload Window";

// scripts/install.sh is bash-only, so the install button is offered
// everywhere except native Windows, where the manual steps in the README
// are the supported route.
export function canRunInstaller(platform: string): boolean {
  return platform !== "win32";
}

// `bash -s --` is what puts a flag on a script arriving over a pipe:
// the script is stdin, so everything after `--` becomes its arguments.
//
// --no-interactive because this command runs on behalf of an extension
// that is, by definition, already installed. Without it the script would
// end by offering to install the extension the user is looking at, and
// would stop for an answer in a terminal they did not open to answer
// questions. VS Code's integrated terminal is a pty, so that prompt
// would really appear.
//
// The flag is therefore part of this extension's contract with the
// script on `main`, which rejects options it does not know: every
// already-published extension emits this line, so the flag cannot be
// renamed or dropped without breaking their Install button.
export function installCommand(): string {
  return `curl -fsSL ${INSTALL_SCRIPT_URL} | bash -s -- --no-interactive`;
}

export function failureActions(platform: string): string[] {
  const actions = [SET_PATH_ACTION, RELOAD_ACTION];
  if (canRunInstaller(platform)) {
    actions.unshift(INSTALL_ACTION);
  }
  return actions;
}

// Both binaries are named because they fail differently: without ghtmx
// there is no server at all, and without gopls the server runs but the
// embedded Go goes dark. The user cannot tell those apart from a toast.
export function failureMessage(
  command: string,
  detail: string,
  platform: string
): string {
  const lead =
    `ghtmx: failed to start "${command} lsp". ` +
    "The extension needs the ghtmx binary on your PATH, and gopls for embedded-Go support.";
  const how = canRunInstaller(platform)
    ? "Install them, or point ghtmx.path at an existing binary."
    : "Install them from the release archive (see the ghtmx README), or point ghtmx.path at an existing binary.";
  return `${lead} ${how} (${detail})`;
}

export function windowsInstallHint(): string {
  return (
    "ghtmx: the installer script is bash-only. On Windows, download the release archive " +
    "from https://github.com/go-monolith/ghtmx/releases and put ghtmx.exe on your PATH, " +
    "then run: go install golang.org/x/tools/gopls@latest"
  );
}
