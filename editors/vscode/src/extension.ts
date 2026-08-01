import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

// The extension is a thin client: `ghtmx lsp` provides diagnostics,
// completion, hover, and definition (proxying gopls for the embedded Go),
// so all language smarts live in the server the module version ships.
export async function activate(
  context: vscode.ExtensionContext
): Promise<void> {
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
    void vscode.window.showErrorMessage(
      `ghtmx: failed to start "${command} lsp" — is the ghtmx binary on PATH? (${err})`
    );
  }
}

export async function deactivate(): Promise<void> {
  if (client) {
    await client.stop();
    client = undefined;
  }
}
