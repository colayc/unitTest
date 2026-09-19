import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, readFile, rm, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";
import { loadF1FrameworkIdentity } from "./consume.mjs";

const sourceRoot = join(import.meta.dirname, "..", "..");
const manifestSha256 = "6ac0e8fd1393c0d84d882445d7ab49ca2cec6b87ee9f1e4b44cfd828125a9441";
const cMockProvenanceSha256 = "e039f3f6ed52900ef70ddd8f3892bbd695bf97747903a860e2d919f124358bb6";

async function identityFixture() {
  const root = await mkdtemp(join(tmpdir(), "utide-f1-identity-"));
  for (const relative of [
    "tools/framework-bundle/manifest.json",
    "tools/framework-bundle/licenses/dependencies.json",
    "sdk/cmake/UnitTestIDE.cmake",
  ]) {
    const target = join(root, relative);
    await mkdir(dirname(target), { recursive: true });
    await cp(join(sourceRoot, relative), target);
  }
  await cp(join(sourceRoot, "testdata/frameworks/cpputest"), join(root, "testdata/frameworks/cpputest"), { recursive: true });
  await cp(join(sourceRoot, "testdata/frameworks/unity"), join(root, "testdata/frameworks/unity"), { recursive: true });
  return root;
}

test("F1 identity loader returns only the locked manifest, tree, provenance, and fixture identities", async () => {
  const root = await identityFixture();
  try {
    const identity = await loadF1FrameworkIdentity(root);
    assert.equal(identity.manifestSha256, manifestSha256);
    assert.deepEqual(identity.frameworkTreeSha256, {
      cpputest: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04",
      unity: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae",
      cmock: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3",
    });
    assert.equal(identity.cMockProvenanceSha256, cMockProvenanceSha256);
    assert.deepEqual(Object.keys(identity.fixtures), ["cpputest", "unity"]);
    assert.deepEqual(identity.fixtures, {
      cpputest: {
        metadataSha256: "6eef50ec7940e4a6b80891d0ff452ed503b5996de313df711ecad607185761ee",
        sourceSha256: "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2",
        executableSha256: "fed16783995c7fe860a931f8d783e50b1bac04c64bd98843fa61613b41a821b3",
      },
      unity: {
        metadataSha256: "1287993f09fb2d8079933ac89a85bd464122edac7b3c92621692a02484536333",
        sourceSha256: "cfec3aea0fec4f1c6a84a4e835edb060415266deb156893d1c10de5ddb0fae15",
        executableSha256: "c094e9ebbf45592d8b087410f6905844d82132bec626dbc549f1db75a9d5e44e",
      },
    });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("F1 identity loader canonicalizes checkout line endings across platforms", async () => {
  const root = await identityFixture();
  try {
    for (const relative of [
      "testdata/frameworks/cpputest/CMakeLists.txt",
      "testdata/frameworks/cpputest/tests/framework_tests.cpp",
      "testdata/frameworks/unity/tests/framework_tests.c",
    ]) {
      const path = join(root, relative);
      await writeFile(path, (await readFile(path, "utf8")).replaceAll("\r\n", "\n"));
    }
    const identity = await loadF1FrameworkIdentity(root);
    assert.equal(identity.fixtures.cpputest.sourceSha256, "114b3d7c6aadcc487b2df01a917c4c0702ba1fdb381456b12c406839181ac5f2");
    assert.equal(identity.fixtures.unity.sourceSha256, "cfec3aea0fec4f1c6a84a4e835edb060415266deb156893d1c10de5ddb0fae15");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("F1 identity loader rejects canonical CMock provenance drift", async () => {
  const root = await identityFixture();
  try {
    const path = join(root, "testdata/frameworks/unity/mocks/cmock-generation.json");
    const value = JSON.parse(await readFile(path, "utf8"));
    value.outputSha256 = "0".repeat(64);
    await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
    await assert.rejects(loadF1FrameworkIdentity(root), /CMOCK_PROVENANCE_INVALID/u);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("F1 identity loader rejects manifest tree drift and fixture byte drift", async () => {
  for (const mutate of [
    async (root) => {
      const path = join(root, "tools/framework-bundle/manifest.json");
      const value = JSON.parse(await readFile(path, "utf8"));
      value.frameworks[0].treeSha256 = "0".repeat(64);
      await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
    },
    async (root) => {
      const path = join(root, "testdata/frameworks/cpputest/tests/framework_tests.cpp");
      await writeFile(path, `${await readFile(path, "utf8")}\n// drift\n`);
    },
  ]) {
    const root = await identityFixture();
    try {
      await mutate(root);
      await assert.rejects(loadF1FrameworkIdentity(root), /FRAMEWORK_(?:MANIFEST_INVALID|FIXTURE_VALIDATION_FAILED)/u);
    } finally {
      await rm(root, { recursive: true, force: true });
    }
  }
});

test("F1 identity loader rejects missing generated output and non-byte-identical cache", async () => {
  const missing = await identityFixture();
  try {
    await unlink(join(missing, "testdata/frameworks/unity/mocks/MockDependency.c"));
    await assert.rejects(loadF1FrameworkIdentity(missing), /CMOCK_PROVENANCE_INVALID/u);
  } finally {
    await rm(missing, { recursive: true, force: true });
  }

  const cached = await identityFixture();
  try {
    const manifest = JSON.parse(await readFile(join(cached, "tools/framework-bundle/manifest.json"), "utf8"));
    const input = manifest.frameworks[0];
    const cache = join(cached, ".superpowers/cache/framework-bundle");
    await mkdir(cache, { recursive: true });
    await writeFile(join(cache, `${input.source.sha256}-${input.source.filename}`), "substituted archive bytes");
    await assert.rejects(loadF1FrameworkIdentity(cached), /FRAMEWORK_CACHE_INVALID/u);
  } finally {
    await rm(cached, { recursive: true, force: true });
  }
});
