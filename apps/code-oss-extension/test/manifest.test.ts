import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import test from "node:test";

const root = resolve(dirname(import.meta.dirname), "..");

test("extension manifest declares workspace extension and safe commands", async () => {
  const manifest = JSON.parse(await readFile(resolve(root, "package.json"), "utf8")) as Record<string, unknown>;
  assert.equal(manifest.name, "@unit-test-ide/code-oss-extension");
  assert.equal(manifest.publisher, "unit-test-ide");
  assert.equal(manifest.main, "./dist/src/extension.js");
  assert.deepEqual(manifest.extensionKind, ["workspace"]);
  const contributes = manifest.contributes as { commands: Array<{ command: string }> };
  assert.deepEqual(
    contributes.commands.map((command) => command.command),
    ["unitTestIde.startService", "unitTestIde.stopService", "unitTestIde.inspectWorkspace"]
  );
});

test("contracts expose explicit lifecycle states", async () => {
  const contracts = await import("../src/contracts.js");
  assert.deepEqual(contracts.TRUST_STATES, ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"]);
  assert.deepEqual(contracts.SERVICE_STATES, ["stopped", "starting", "running", "stopping", "failed"]);
});
