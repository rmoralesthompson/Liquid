// Thin LSP client: starts `liquid lsp` for .lsx documents and degrades to
// grammar-only highlighting when the server (or its npm dependency) is
// unavailable. All language smarts live server-side in cmd/liquid.
"use strict";

const { window, workspace } = require("vscode");

let client;

async function activate() {
  let LanguageClient;
  try {
    ({ LanguageClient } = require("vscode-languageclient/node"));
  } catch {
    // node_modules not installed (grammar-only dev install): highlighting
    // still works, so stay quiet.
    return;
  }

  const command =
    workspace.getConfiguration("liquid.lsx").get("serverPath") || "liquid";
  client = new LanguageClient(
    "liquid-lsx",
    "Liquid LSX",
    { command, args: ["lsp"] },
    { documentSelector: [{ scheme: "file", language: "lsx" }] }
  );

  try {
    await client.start();
  } catch {
    client = undefined;
    window.showInformationMessage(
      `Liquid: could not start "${command} lsp" — syntax highlighting still works. ` +
        "Install the liquid CLI (go install github.com/rmoralesthompson/liquid/cmd/liquid@latest) " +
        "or point liquid.lsx.serverPath at it."
    );
  }
}

function deactivate() {
  return client ? client.stop() : undefined;
}

module.exports = { activate, deactivate };
