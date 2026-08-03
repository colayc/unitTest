import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import test from "node:test";
import { dirname, resolve } from "node:path";

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

test("normal generation removes sibling staged files when replacement fails", async () => {
  const backup = `${goTarget}.test-backup`;
  try {
    await rename(goTarget, backup);
    await mkdir(goTarget);
    const result = spawnSync(process.execPath, [generator], { cwd: root, encoding: "utf8" });
    assert.notEqual(result.status, 0, result.stdout + result.stderr);
    const siblings = await readdir(dirname(goTarget));
    assert.deepEqual(siblings.filter((name) => name.startsWith("generated.go.tmp-")), []);
  } finally {
    await rm(goTarget, { recursive: true, force: true });
    await rename(backup, goTarget);
    const siblings = await readdir(dirname(goTarget));
    await Promise.all(siblings
      .filter((name) => name.startsWith("generated.go.tmp-"))
      .map((name) => rm(resolve(dirname(goTarget), name), { force: true })));
  }
});
