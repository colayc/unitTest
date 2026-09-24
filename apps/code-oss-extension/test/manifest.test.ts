import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import test from "node:test";

const root = resolve(dirname(import.meta.dirname), "..");

test("extension manifest declares workspace extension and safe commands", async () => {
  const manifest = JSON.parse(await readFile(resolve(root, "package.json"), "utf8")) as Record<string, unknown>;
  assert.equal(manifest.name, "code-oss-extension");
  assert.equal(manifest.publisher, "unit-test-ide");
  assert.equal(`${String(manifest.publisher)}.${String(manifest.name)}`, "unit-test-ide.code-oss-extension");
  assert.equal(manifest.main, "./dist/src/extension-entry.cjs");
  assert.deepEqual(manifest.extensionKind, ["workspace"]);
  const contributes = manifest.contributes as { commands: Array<{ command: string }> };
  assert.deepEqual(
    contributes.commands.map((command) => command.command),
    [
      "unitTestIde.startService",
      "unitTestIde.stopService",
      "unitTestIde.inspectWorkspace",
      "unitTestIde.runCoverage",
      "unitTestIde.refreshCoverage",
      "unitTestIde.openCoverageReport",
      "unitTestIde.openCoverageSource"
    ]
  );
});

test("contracts expose explicit lifecycle states", async () => {
  const contracts = await import("../src/contracts.js");
  assert.deepEqual(contracts.TRUST_STATES, ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"]);
  assert.deepEqual(contracts.SERVICE_STATES, ["stopped", "starting", "running", "stopping", "failed"]);
});

test("Code-OSS can require the extension entrypoint before activating its ESM implementation", async () => {
  const manifest = JSON.parse(await readFile(resolve(root, "package.json"), "utf8")) as { main: string };
  const supportsRequireModuleFlag = Number.parseInt(process.versions.node.split(".")[0]!, 10) >= 22;
  const result = spawnSync(process.execPath, [
    ...(supportsRequireModuleFlag ? ["--no-experimental-require-module"] : []),
    "-e",
    "const entrypoint = require(process.argv[1]); process.stdout.write(`${typeof entrypoint.activate},${typeof entrypoint.deactivate}`);",
    resolve(root, manifest.main)
  ], { encoding: "utf8", windowsHide: true });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "function,function");
});
