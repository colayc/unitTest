import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { access, mkdtemp, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";

import { stageQualifiedRelease } from "./stage-qualified-release.mjs";

const version = "0.1.0";
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

async function writeFixtureFile(root, relativePath, contents) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(join(path, ".."), { recursive: true });
  await writeFile(path, contents);
  return path;
}

async function createFixture(root) {
  const windowsManifestBytes = Buffer.from('{"platform":"windows"}\n');
  const linuxManifestBytes = Buffer.from('{"platform":"linux"}\n');
  const windowsRoot = join(root, "windows");
  const linuxRoot = join(root, "linux");
  const evidenceRoot = join(root, "evidence");
  return {
    input: {
      version,
      outRoot: join(root, "qualified"),
      windowsPackage: await writeFixtureFile(windowsRoot, `unit-test-ide-${version}.msix`, "windows package\n"),
      windowsManifest: await writeFixtureFile(windowsRoot, `unit-test-ide-${version}.release-manifest.json`, windowsManifestBytes),
      windowsManifestSha256: sha256(windowsManifestBytes),
      windowsLicenseAudit: await writeFixtureFile(windowsRoot, "license-audit-windows.json", "{}\n"),
      linuxPackage: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.AppImage`, "linux package\n"),
      linuxPackageManifest: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.AppImage.sha256.json`, "{}\n"),
      linuxManifest: await writeFixtureFile(linuxRoot, `unit-test-ide-${version}.release-manifest.json`, linuxManifestBytes),
      linuxManifestSha256: sha256(linuxManifestBytes),
      linuxLicenseAudit: await writeFixtureFile(linuxRoot, "license-audit-linux.json", "{}\n"),
      qualification: await writeFixtureFile(evidenceRoot, "release-qualification.json", "{}\n"),
    },
    windowsManifestBytes,
    linuxManifestBytes,
  };
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "qualified-release-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await run(root);
}

test("stageQualifiedRelease preserves both platform manifests in an exact flat file set", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    const result = await stageQualifiedRelease(fixture.input);
    const expectedFiles = [
      "license-audit-linux.json",
      "license-audit-windows.json",
      "release-qualification.json",
      `unit-test-ide-${version}.AppImage`,
      `unit-test-ide-${version}.AppImage.sha256.json`,
      `unit-test-ide-${version}.linux-x64.release-manifest.json`,
      `unit-test-ide-${version}.msix`,
      `unit-test-ide-${version}.windows-x64.release-manifest.json`,
    ].sort((left, right) => left.localeCompare(right, "en"));

    assert.deepEqual(result, { outputRoot: resolve(fixture.input.outRoot), files: expectedFiles });
    const entries = await readdir(result.outputRoot, { withFileTypes: true });
    assert.deepEqual(entries.map(({ name }) => name).sort((left, right) => left.localeCompare(right, "en")), expectedFiles);
    assert.ok(entries.every((entry) => entry.isFile() && !entry.isSymbolicLink()));
    assert.deepEqual(
      await readFile(join(result.outputRoot, `unit-test-ide-${version}.windows-x64.release-manifest.json`)),
      fixture.windowsManifestBytes,
    );
    assert.deepEqual(
      await readFile(join(result.outputRoot, `unit-test-ide-${version}.linux-x64.release-manifest.json`)),
      fixture.linuxManifestBytes,
    );
    await assert.rejects(access(join(result.outputRoot, `unit-test-ide-${version}.release-manifest.json`)), /ENOENT/u);
  });
});

test("stageQualifiedRelease reports an existing output directory with a stable error", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    await mkdir(fixture.input.outRoot, { recursive: true });

    await assert.rejects(
      stageQualifiedRelease(fixture.input),
      (error) => error.code === "RELEASE_QUALIFIED_STAGING_FAILED" && error.message === "Qualified release staging failed",
    );
  });
});

test("stageQualifiedRelease wraps source filesystem failures without leaking diagnostics", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    const missingSource = join(root, "missing-source-with-sensitive-name.msix");

    await assert.rejects(
      stageQualifiedRelease({ ...fixture.input, windowsPackage: missingSource }),
      (error) => {
        assert.equal(error.code, "RELEASE_QUALIFIED_STAGING_FAILED");
        assert.equal(error.message, "Qualified release staging failed");
        assert.equal(error.message.includes(missingSource), false);
        return true;
      },
    );
    await assert.rejects(access(resolve(fixture.input.outRoot)), /ENOENT/u);
  });
});
