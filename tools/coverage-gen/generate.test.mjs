import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import test from "node:test";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const generator = resolve(import.meta.dirname, "generate.mjs");
const target = resolve(root, "packages/coverage-models/src/generated/coverage-v1.ts");
const goTarget = resolve(root, "apps/test-service/internal/coveragemodel/v1/generated.go");
const quicktype = resolve(root, "node_modules/quicktype/dist/index.js");

async function expectStale(mutator) {
  const original = await readFile(target);
  try {
    await writeFile(target, mutator(original));
    const result = spawnSync(process.execPath, [generator, "--check"], {
      cwd: root,
      encoding: "utf8"
    });
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    assert.match(result.stderr, /Generated file is stale: packages\/coverage-models\/src\/generated\/coverage-v1\.ts/);
  } finally {
    await writeFile(target, original);
  }
}

test("check rejects generated TypeScript output with CRLF line endings", async () => {
  await expectStale((source) => Buffer.from(source.toString("utf8").replaceAll("\n", "\r\n")));
});

test("check rejects generated TypeScript output without its final newline", async () => {
  await expectStale((source) => source.subarray(0, -1));
});

test("normal generation does not replace any target when a later generator fails", async () => {
  const originalQuicktype = await readFile(quicktype);
  const originalTypeScript = await readFile(target);
  const failingQuicktype = `const fs = require("node:fs");
const language = process.argv[process.argv.indexOf("--lang") + 1];
const output = process.argv[process.argv.indexOf("--out") + 1];
if (language === "typescript") fs.writeFileSync(output, "export type ShouldNotBeCommitted = true;\\n");
else process.exitCode = 17;
`;
  try {
    await writeFile(quicktype, failingQuicktype);
    const result = spawnSync(process.execPath, [generator], { cwd: root, encoding: "utf8" });
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    assert.deepEqual(await readFile(target), originalTypeScript);
  } finally {
    await writeFile(quicktype, originalQuicktype);
    await writeFile(target, originalTypeScript);
  }
});

test("normal generation restores every destination and removes transaction files when replacement fails", async () => {
  const originalQuicktype = await readFile(quicktype);
  const originalTypeScript = await readFile(target);
  const originalGo = await readFile(goTarget);
  const temporary = await mkdtemp(join(tmpdir(), "coverage gen transaction test-"));
  const preload = join(temporary, "fail-go-rename.cjs");
  const changedQuicktype = `const fs = require("node:fs");
const language = process.argv[process.argv.indexOf("--lang") + 1];
const output = process.argv[process.argv.indexOf("--out") + 1];
const content = language === "typescript"
  ? "export type ChangedTypeScript = true;\\n"
  : "type ChangedGo struct{}\\n";
fs.writeFileSync(output, content);
`;
  const failGoRename = `const fs = require("node:fs/promises");
const { syncBuiltinESMExports } = require("node:module");
const originalRename = fs.rename;
fs.rename = async (source, destination) => {
  if (process.argv[1] === ${JSON.stringify(generator)} &&
      destination === ${JSON.stringify(goTarget)} && String(source).includes(".tmp-")) {
    const error = new Error("injected Go destination rename failure");
    error.code = "EACCES";
    throw error;
  }
  return originalRename(source, destination);
};
syncBuiltinESMExports();
`;
  try {
    await writeFile(quicktype, changedQuicktype);
    await writeFile(preload, failGoRename);
    const result = spawnSync(process.execPath, ["--require", preload, generator], {
      cwd: root,
      encoding: "utf8"
    });
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    assert.match(result.stderr, /injected Go destination rename failure/);
    assert.deepEqual(await readFile(target), originalTypeScript);
    assert.deepEqual(await readFile(goTarget), originalGo);
    for (const destination of [target, goTarget]) {
      const siblings = await readdir(dirname(destination));
      assert.deepEqual(
        siblings.filter((name) => name.includes(".tmp-") || name.includes(".backup-")),
        []
      );
    }
  } finally {
    await writeFile(quicktype, originalQuicktype);
    await writeFile(target, originalTypeScript);
    await writeFile(goTarget, originalGo);
    for (const destination of [target, goTarget]) {
      const siblings = await readdir(dirname(destination));
      await Promise.all(siblings
        .filter((name) => name.includes(".tmp-") || name.includes(".backup-"))
        .map((name) => rm(resolve(dirname(destination), name), { recursive: true, force: true })));
    }
    await rm(temporary, { recursive: true, force: true });
  }
});
