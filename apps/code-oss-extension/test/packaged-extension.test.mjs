import assert from "node:assert/strict";
import { access, cp, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, parse, resolve } from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";

async function assertNoAncestorRuntimeDependencies(path) {
  const runtimeDependencies = [
    join("node_modules", "@unit-test-ide", "test-client"),
    join("node_modules", "ajv"),
    join("node_modules", "ajv-formats"),
  ];
  let current = resolve(path);
  const root = parse(current).root;
  while (true) {
    for (const dependency of runtimeDependencies) {
      await assert.rejects(access(join(current, dependency)), { code: "ENOENT" });
    }
    if (current === root) return;
    current = dirname(current);
  }
}

test("release-shaped extension implementation imports without repository node_modules", async (t) => {
  const repositoryRoot = resolve(import.meta.dirname, "../../..");
  const sourceExtension = join(repositoryRoot, "apps", "code-oss-extension");
  const temporaryRoot = await mkdtemp(join(tmpdir(), "unit-test-ide-packaged-extension-"));
  const packagedExtension = join(temporaryRoot, "release shape with spaces", "unit-test-ide");
  t.after(() => rm(temporaryRoot, { recursive: true, force: true }));

  await mkdir(packagedExtension, { recursive: true });
  await cp(join(sourceExtension, "package.json"), join(packagedExtension, "package.json"), {
    recursive: false,
  });
  await cp(join(sourceExtension, "dist"), join(packagedExtension, "dist"), {
    recursive: true,
  });
  await assertNoAncestorRuntimeDependencies(packagedExtension);

  const manifest = JSON.parse(await readFile(join(packagedExtension, "package.json"), "utf8"));
  assert.equal(manifest.main, "./dist/src/extension-entry.cjs");
  const implementation = await import(
    `${pathToFileURL(join(packagedExtension, "dist", "src", "extension.js")).href}?isolated=${Date.now()}`
  );

  assert.equal(typeof implementation.activate, "function");
  assert.equal(typeof implementation.deactivate, "function");
});
