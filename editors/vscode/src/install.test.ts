// Namespace import, not default: tsconfig.json leaves esModuleInterop
// off, matching extension.ts's `import * as vscode`.
import * as assert from "node:assert/strict";
import { test } from "node:test";

import {
  INSTALL_ACTION,
  INSTALL_SCRIPT_URL,
  RELOAD_ACTION,
  SET_PATH_ACTION,
  canRunInstaller,
  failureActions,
  failureMessage,
  installCommand,
  windowsInstallHint,
} from "./install";

test("the installer is offered on the platforms its script supports", () => {
  assert.equal(canRunInstaller("linux"), true);
  assert.equal(canRunInstaller("darwin"), true);
  assert.equal(canRunInstaller("win32"), false);
});

test("the install command fetches the script over https", () => {
  const command = installCommand();
  assert.ok(command.includes(INSTALL_SCRIPT_URL));
  assert.ok(INSTALL_SCRIPT_URL.startsWith("https://"));
  assert.ok(command.startsWith("curl "));
});

// The script prompts about installing this extension when it is run by
// hand. Run from inside the extension it must not: the answer is known,
// and the terminal it opens is not a place to ask.
test("the install command tells the script not to prompt", () => {
  const command = installCommand();
  assert.ok(command.includes("| bash -s -- --no-interactive"));
});

test("Windows drops the install action but keeps the rest", () => {
  const posix = failureActions("linux");
  assert.deepEqual(posix, [INSTALL_ACTION, SET_PATH_ACTION, RELOAD_ACTION]);

  const windows = failureActions("win32");
  assert.deepEqual(windows, [SET_PATH_ACTION, RELOAD_ACTION]);
});

test("failureActions returns a fresh array each call", () => {
  const first = failureActions("linux");
  first.push("mutated");
  assert.deepEqual(failureActions("linux"), [
    INSTALL_ACTION,
    SET_PATH_ACTION,
    RELOAD_ACTION,
  ]);
});

test("the failure message names both binaries and the underlying error", () => {
  const message = failureMessage("ghtmx", "spawn ENOENT", "linux");
  assert.ok(message.includes("ghtmx"));
  assert.ok(message.includes("gopls"));
  assert.ok(message.includes("ghtmx.path"));
  assert.ok(message.includes("spawn ENOENT"));
});

test("the failure message repeats the configured command", () => {
  const message = failureMessage("/opt/bin/ghtmx", "spawn ENOENT", "linux");
  assert.ok(message.includes('"/opt/bin/ghtmx lsp"'));
});

test("Windows is told where to get the binaries instead", () => {
  const message = failureMessage("ghtmx", "spawn ENOENT", "win32");
  assert.ok(message.includes("release archive"));
  assert.ok(windowsInstallHint().includes("releases"));
  assert.ok(windowsInstallHint().includes("gopls"));
});
