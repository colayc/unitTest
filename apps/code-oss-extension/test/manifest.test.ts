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
  const contributes = manifest.contributes as { commands: Array<{ command: string }>; views: { explorer: Array<{ id: string; when?: string }> }; menus: Record<string, Array<{ command: string; when?: string }>> };
  assert.deepEqual(
    contributes.commands.map((command) => command.command),
    [
      "unitTestIde.startService",
      "unitTestIde.stopService",
      "unitTestIde.inspectWorkspace",
      "unitTestIde.runCoverage",
      "unitTestIde.refreshCoverage",
      "unitTestIde.openCoverageReport",
      "unitTestIde.openCoverageSource",
      "unitTestIde.coverageFilter.all",
      "unitTestIde.coverageFilter.uncovered",
      "unitTestIde.coverageFilter.regressed",
      "unitTestIde.coverageFilter.incomplete",
      "unitTestIde.generateTests",
      "unitTestIde.generateTestsForSymbol",
      "unitTestIde.generateTestsForFile",
      "unitTestIde.generateTestsForTarget",
      "unitTestIde.generateTestsForCoverageGap",
      "unitTestIde.reviewGeneratedTests",
      "unitTestIde.acceptGeneratedTests",
      "unitTestIde.cancelTestGeneration",
      "unitTestIde.generateManagedTestsForFunction",
      "unitTestIde.generateManagedTestsForFile",
      "unitTestIde.generateManagedTestsForCoverageGap",
      "unitTestIde.listManagedTests",
      "unitTestIde.reviewManagedTests",
      "unitTestIde.applyManagedReview",
      "unitTestIde.rejectManagedReview"
    ]
  );
  assert.ok(contributes.views.explorer.some((view) => view.id === "unitTestIde.coverageDetails" && view.when === "unitTestIde.coverageDetailsAvailable"));
  for (const command of contributes.commands.filter((item) => item.command.startsWith("unitTestIde.coverageFilter."))) {
    assert.ok(contributes.menus.commandPalette?.some((item) => item.command === command.command && item.when === "unitTestIde.coverageDetailsAvailable"));
  }
  for (const command of contributes.commands.filter((item) => /unitTestIde\.(?:generateManaged|listManaged|reviewManaged|applyManaged|rejectManaged)/.test(item.command))) {
    const when = command.command === "unitTestIde.applyManagedReview" ? "unitTestIde.managedReviewReady" : "unitTestIde.managedTestsAvailable";
    assert.ok(contributes.menus.commandPalette?.some((item) => item.command === command.command && item.when === when));
  }
});

test("contracts expose explicit lifecycle states", async () => {
  const contracts = await import("../src/contracts.js");
  assert.deepEqual(contracts.TRUST_STATES, ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"]);
  assert.deepEqual(contracts.SERVICE_STATES, ["stopped", "starting", "running", "stopping", "failed"]);
  assert.equal(contracts.TEST_GENERATION_COMMANDS.length, 8);
});

test("generation menus use resource/editor contexts and do not shell out", async () => {
  const manifest = JSON.parse(await readFile(resolve(root, "package.json"), "utf8")) as { contributes: { menus: Record<string, Array<{ command: string; when?: string }>> } };
  const menus = manifest.contributes.menus;
  assert.ok(menus["editor/context"]?.some((item) => item.command === "unitTestIde.generateTestsForSymbol" && item.when?.includes("editorLangId")));
  assert.ok(menus["explorer/context"]?.some((item) => item.command === "unitTestIde.generateTestsForFile" && item.when?.includes("resourceLangId")));
  assert.deepEqual(menus["editor/context"]?.map((item) => item.command), ["unitTestIde.generateTestsForSymbol"]);
  assert.ok(menus["view/item/context"]?.some((item) => item.command === "unitTestIde.generateManagedTestsForFile" && item.when?.includes("viewItem == coverage-file")));
  assert.ok(menus["view/item/context"]?.some((item) => item.command === "unitTestIde.generateManagedTestsForFunction" && item.when?.includes("viewItem == coverage-function")));
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
