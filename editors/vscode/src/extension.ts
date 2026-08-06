import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";
import {
  INSTALL_ACTION,
  RELOAD_ACTION,
  SET_PATH_ACTION,
  canRunInstaller,
  failureActions,
  failureMessage,
  installCommand,
  windowsInstallHint,
} from "./install";

let client: LanguageClient | undefined;

// The extension is a thin client: `ghtmx lsp` provides diagnostics,
// completion, hover, and definition (proxying gopls for the embedded Go),
// so all language smarts live in the server the module version ships.
// Neither that server nor gopls comes with the .vsix, so the failure
// path below offers to run scripts/install.sh rather than dead-ending.
export async function activate(
  context: vscode.ExtensionContext
): Promise<void> {
  context.subscriptions.push(
    vscode.commands.registerCommand("ghtmx.installTools", () => {
      runInstaller();
    })
  );

  const config = vscode.workspace.getConfiguration("ghtmx");
  const command = config.get<string>("path") || "ghtmx";
  const args = ["lsp"];
  const log = config.get<string>("log");
  if (log) {
    args.push("-log", log);
  }
  const goplsLog = config.get<string>("goplsLog");
  if (goplsLog) {
    args.push("-goplsLog", goplsLog);
  }
  if (config.get<boolean>("goplsRPCTrace")) {
    args.push("-goplsRPCTrace");
  }

  const serverOptions: ServerOptions = { command, args };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "ghtmx" }],
    synchronize: {
      // The server re-runs route discovery and event seeding from Go
      // sources and ghtmx.json.
      fileEvents: vscode.workspace.createFileSystemWatcher(
        "**/{*.ghtmx,*.go,ghtmx.json}"
      ),
    },
  };

  client = new LanguageClient(
    "ghtmx",
    "ghtmx language server",
    serverOptions,
    clientOptions
  );
  try {
    await client.start();
  } catch (err) {
    client = undefined;
    // Not awaited: the notification waits on a click, and activate()
    // must not hang on one. The catch is what keeps that from becoming
    // an unhandled rejection if a command execution fails.
    void reportStartFailure(command, err).catch((reportErr) => {
      console.error("ghtmx: reporting the start failure failed", reportErr);
    });
  }
}

// Opens a terminal with the install command typed but NOT executed:
// sendText's second argument suppresses the newline, so the user reads
// the pipe-to-bash line and presses Enter themselves. The extension
// never runs a downloaded script on its own.
function runInstaller(): void {
  if (!canRunInstaller(process.platform)) {
    void vscode.window.showInformationMessage(windowsInstallHint());
    return;
  }
  const terminal = vscode.window.createTerminal("ghtmx install");
  terminal.show();
  terminal.sendText(installCommand(), false);
}

async function reportStartFailure(
  command: string,
  err: unknown
): Promise<void> {
  const detail = err instanceof Error ? err.message : String(err);
  const choice = await vscode.window.showErrorMessage(
    failureMessage(command, detail, process.platform),
    ...failureActions(process.platform)
  );
  switch (choice) {
    case INSTALL_ACTION:
      runInstaller();
      break;
    case SET_PATH_ACTION:
      // Server options are read once at start, so changing the path
      // only takes effect on the reload offered alongside it.
      await vscode.commands.executeCommand(
        "workbench.action.openSettings",
        "ghtmx.path"
      );
      break;
    case RELOAD_ACTION:
      await vscode.commands.executeCommand("workbench.action.reloadWindow");
      break;
  }
}

export async function deactivate(): Promise<void> {
  if (client) {
    await client.stop();
    client = undefined;
  }
}
