import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("workspace pins supported toolchains", async () => {
  assert.equal((await readFile(".node-version", "utf8")).trim(), "24.18.0");
  assert.equal((await readFile(".go-version", "utf8")).trim(), "1.26.5");
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(manifest.packageManager, "pnpm@11.4.0");
  assert.equal(manifest.engines.node, ">=24.18.0 <25");
});

test("README contains the complete local verification gate", async () => {
  const readme = await readFile("README.md", "utf8");
  for (const command of ["pnpm check:protocol-generated", "pnpm test", "pnpm test:e2e"]) {
    assert.match(readme, new RegExp(command.replaceAll(" ", "\\s+")));
  }
});

test("dependency metadata uses the official npm registry", async () => {
  assert.equal((await readFile(".npmrc", "utf8")).trim(), "registry=https://registry.npmjs.org/");
  assert.doesNotMatch(await readFile("pnpm-lock.yaml", "utf8"), /registry\.npmmirror\.com/);
});

test("documentation records pre-write token file preparation", async () => {
  const readme = await readFile("README.md", "utf8");
  const decision = await readFile("docs/decisions/0001-local-ipc-and-protocol-v1.md", "utf8");
  assert.match(readme, /--prepare-token-file/);
  assert.match(decision, /--prepare-token-file/);
  assert.doesNotMatch(readme, /removes inherited Windows permissions after writing/i);
});
