import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import test from "node:test";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const generator = resolve(import.meta.dirname, "generate.mjs");
const target = resolve(root, "packages/coverage-models/src/generated/coverage-v1.ts");

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
