import type * as vscode from "vscode";

type ExtensionModule = typeof import("./extension.js");

let implementation: Promise<ExtensionModule> | undefined;

function loadImplementation(): Promise<ExtensionModule> {
  implementation ??= import("./extension.js");
  return implementation;
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  await (await loadImplementation()).activate(context);
}

export async function deactivate(): Promise<void> {
  await (await loadImplementation()).deactivate();
}
