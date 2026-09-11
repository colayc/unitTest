import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { access, mkdtemp, mkdir, readFile, readdir, rename, rm, symlink, writeFile } from "node:fs/promises";
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
      (error) => error.code === "RELEASE_QUALIFIED_STAGING_FAILED" && /qualified output already exists/u.test(error.message),
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
        assert.match(error.message, /Windows package basename is invalid/u);
        assert.equal(error.message.includes(missingSource), false);
        return true;
      },
    );
    await assert.rejects(access(resolve(fixture.input.outRoot)), /ENOENT/u);
  });
});

test("stageQualifiedRelease rejects invalid inputs without publishing output", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate, expected] of [
      ["Windows manifest digest mismatch", (input) => { input.windowsManifestSha256 = "0".repeat(64); }, /Windows manifest SHA-256 does not match/u],
      ["Linux manifest digest mismatch", (input) => { input.linuxManifestSha256 = "0".repeat(64); }, /Linux manifest SHA-256 does not match/u],
      ["unexpected package basename", (input) => { input.windowsPackage = input.qualification; }, /Windows package basename is invalid/u],
    ]) {
      await t.test(name, async () => {
        const caseRoot = await mkdtemp(join(root, "case-"));
        const fixture = await createFixture(caseRoot);
        mutate(fixture.input);
        await assert.rejects(stageQualifiedRelease(fixture.input), (error) => {
          assert.equal(error.code, "RELEASE_QUALIFIED_STAGING_FAILED");
          assert.match(error.message, expected);
          assert.doesNotMatch(error.message, new RegExp(caseRoot.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
          return true;
        });
        await assert.rejects(access(fixture.input.outRoot), /ENOENT/u);
      });
    }

    const existingRoot = join(root, "existing-case");
    const existingFixture = await createFixture(existingRoot);
    await mkdir(existingFixture.input.outRoot);
    await writeFile(join(existingFixture.input.outRoot, "owner-marker"), "preserve\n");
    await assert.rejects(stageQualifiedRelease(existingFixture.input), /qualified output already exists/u);
    assert.equal(await readFile(join(existingFixture.input.outRoot, "owner-marker"), "utf8"), "preserve\n");
  });
});

test("stageQualifiedRelease rejects symbolic-link inputs", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    const linked = join(root, "manifest-target.json");
    try { await rename(fixture.input.windowsManifest, linked); await symlink(linked, fixture.input.windowsManifest); } catch (error) { if (error?.code === "EPERM") return t.skip("file symlink creation is unavailable"); throw error; }
    await assert.rejects(stageQualifiedRelease(fixture.input), (error) => {
      assert.equal(error.code, "RELEASE_QUALIFIED_STAGING_FAILED");
      assert.match(error.message, /Windows manifest must be a real file/u);
      assert.doesNotMatch(error.message, new RegExp(root.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
      return true;
    });
    await assert.rejects(access(fixture.input.outRoot), /ENOENT/u);
  });
});

function cliArgs(input) { return ["--version", input.version, "--out", input.outRoot, "--windows-package", input.windowsPackage, "--windows-manifest", input.windowsManifest, "--windows-manifest-sha256", input.windowsManifestSha256, "--windows-license-audit", input.windowsLicenseAudit, "--linux-package", input.linuxPackage, "--linux-package-manifest", input.linuxPackageManifest, "--linux-manifest", input.linuxManifest, "--linux-manifest-sha256", input.linuxManifestSha256, "--linux-license-audit", input.linuxLicenseAudit, "--qualification", input.qualification]; }
function runCli(args) { return spawnSync(process.execPath, [resolve("tools/release/stage-qualified-release.mjs"), ...args], { encoding: "utf8" }); }

test("stageQualifiedRelease CLI maps exact flags and fails safely", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createFixture(root);
    const success = runCli(cliArgs(fixture.input));
    assert.equal(success.status, 0); assert.equal(success.stderr, "");
    const result = JSON.parse(success.stdout); assert.deepEqual(new Set(result.files), new Set(["license-audit-linux.json", "license-audit-windows.json", "release-qualification.json", `unit-test-ide-${version}.AppImage`, `unit-test-ide-${version}.AppImage.sha256.json`, `unit-test-ide-${version}.linux-x64.release-manifest.json`, `unit-test-ide-${version}.msix`, `unit-test-ide-${version}.windows-x64.release-manifest.json`]));
    const unknown = runCli(["--secret-file", fixture.input.windowsManifest]); assert.equal(unknown.status, 1); assert.match(unknown.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: unknown argument: --secret-file/u); assert.doesNotMatch(unknown.stderr, new RegExp(root.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "u"));
    const secretToken = "--secret=top-secret-C:\\fixture\\private.json";
    const leaked = runCli([secretToken]); assert.equal(leaked.status, 1); assert.match(leaked.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: unknown argument/u); assert.doesNotMatch(leaked.stderr, /top-secret|fixture|private\.json/u);
    const positionalPath = "C:\\fixture\\private-input.json";
    const positional = runCli([positionalPath]); assert.equal(positional.status, 1); assert.doesNotMatch(positional.stderr, /fixture|private-input\.json/u);
    for (const token of ["--secret-file:C:\\fixture\\private.json", "--flag/path-C:\\fixture", "--control\u0007secret"]) {
      const unsafeFlag = runCli([token]); assert.equal(unsafeFlag.status, 1); assert.match(unsafeFlag.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: unknown argument: <token>/u); assert.doesNotMatch(unsafeFlag.stderr, /secret-file|fixture|private\.json|flag\/path|control/u);
    }
    const missingValue = runCli(["--version"]); assert.equal(missingValue.status, 1); assert.match(missingValue.stderr, /RELEASE_QUALIFIED_STAGING_FAILED: missing value for --version/u);
  });
});
