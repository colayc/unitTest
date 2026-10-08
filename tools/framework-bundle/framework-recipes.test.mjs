import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { verifyFrameworkRecipes, copyFrameworkRecipeInputs } from "./framework-recipes.mjs";

const repositoryRoot = resolve(import.meta.dirname, "../..");
const recipeDirectory = "sdk/cmake/framework-recipes";
async function temporary(t) {
  const root = await mkdtemp(join(tmpdir(), "utide-framework-recipes-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}
async function repository(t) {
  const root = await temporary(t);
  await cp(join(repositoryRoot, recipeDirectory), join(root, recipeDirectory), { recursive: true });
  return root;
}
async function mutateManifest(root, change) {
  const path = join(root, recipeDirectory, "manifest.json");
  const manifest = JSON.parse(await readFile(path, "utf8"));
  change(manifest);
  await writeFile(path, `${JSON.stringify(manifest, null, 2)}\n`);
}

test("copies exactly the authenticated SDK recipe bytes and never overwrites", async (t) => {
  const workspace = await temporary(t);
  await verifyFrameworkRecipes(repositoryRoot);
  await copyFrameworkRecipeInputs(repositoryRoot, workspace);
  for (const name of ["windows-cpputest-4.0.cmake", "windows-unity-2.6.1.cmake"]) {
    assert.deepEqual(await readFile(join(workspace, ".unit-test-ide/inputs/framework-recipes", name)),
      await readFile(join(repositoryRoot, recipeDirectory, name)));
  }
  const target = join(workspace, ".unit-test-ide/inputs/framework-recipes/windows-unity-2.6.1.cmake");
  await writeFile(target, "user bytes");
  await assert.rejects(copyFrameworkRecipeInputs(repositoryRoot, workspace));
  assert.equal(await readFile(target, "utf8"), "user bytes");
});

test("rejects missing resources before creating recipe inputs", async (t) => {
  const root = await repository(t);
  await rm(join(root, recipeDirectory, "windows-unity-2.6.1.cmake"));
  const workspace = await temporary(t);
  await assert.rejects(verifyFrameworkRecipes(root));
  await assert.rejects(copyFrameworkRecipeInputs(root, workspace));
  await assert.rejects(readFile(join(workspace, ".unit-test-ide/inputs/framework-recipes/windows-cpputest-4.0.cmake")));
});

for (const [label, change] of [
  ["unknown inventory field", (m) => { m.recipes[0].command = "execute_process"; }],
  ["unknown manifest field", (m) => { m.extra = true; }],
  ["escaped source", (m) => { m.recipes[0].sources[0] = "../outside.cpp"; }],
  ["escaped recipe path", (m) => { m.recipes[0].path = "../outside.cmake"; }],
  ["changed framework root", (m) => { m.recipes[0].frameworkRoot = "../outside"; }],
  ["duplicate target", (m) => { m.recipes[1].target = "CppUTest"; }],
  ["wrong target version", (m) => { m.recipes[0].version = "4.1"; }],
  ["extra recipe", (m) => { m.recipes.push(m.recipes[0]); }],
  ["missing recipe", (m) => { m.recipes.pop(); }],
  ["changed defines", (m) => { m.recipes[0].defines = []; }],
  ["changed library authority", (m) => { m.recipes[0].systemLibraryVariable = "OTHER_LIBRARY"; }],
]) {
  test(`rejects ${label}`, async (t) => {
    const root = await repository(t);
    await mutateManifest(root, change);
    await assert.rejects(verifyFrameworkRecipes(root));
  });
}

for (const [label, content, updateDigest] of [
  ["digest modification", "# changed\n", false],
  ["Mock source even with updated digest", 'add_library(CppUTest STATIC "../cpputest/src/CppUTestExt/MockSupport.cpp")\n', true],
  ["dynamic commands even with updated digest", "execute_process(COMMAND unsafe)\n", true],
]) {
  test(`rejects ${label}`, async (t) => {
    const root = await repository(t);
    await writeFile(join(root, recipeDirectory, "windows-cpputest-4.0.cmake"), content);
    if (updateDigest) await mutateManifest(root, (m) => {
      m.recipes[0].sha256 = createHash("sha256").update(content).digest("hex");
    });
    await assert.rejects(verifyFrameworkRecipes(root));
  });
}

test("rejects duplicate JSON keys", async (t) => {
  const root = await repository(t);
  const path = join(root, recipeDirectory, "manifest.json");
  await writeFile(path, (await readFile(path, "utf8")).replace('"schemaVersion": 1', '"schemaVersion": 1, "schemaVersion": 1'));
  await assert.rejects(verifyFrameworkRecipes(root));
});

test("rejects linked resource and destination directories without writing outside workspace", async (t) => {
  const root = await repository(t);
  const outside = await temporary(t);
  const resources = join(root, recipeDirectory);
  await cp(resources, join(outside, "recipes"), { recursive: true });
  await rm(resources, { recursive: true });
  await symlink(join(outside, "recipes"), resources, "junction");
  await assert.rejects(verifyFrameworkRecipes(root));
  const workspace = await temporary(t);
  await mkdir(join(workspace, ".unit-test-ide"));
  await symlink(outside, join(workspace, ".unit-test-ide/inputs"), "junction");
  await assert.rejects(copyFrameworkRecipeInputs(repositoryRoot, workspace));
  await assert.rejects(readFile(join(outside, "framework-recipes/windows-unity-2.6.1.cmake")));
});
