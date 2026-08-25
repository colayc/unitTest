import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { auditLicenses } from "./license-audit.mjs";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function writeFixtureFile(root, relativePath, value) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, value);
  return path;
}

async function createStagingFixture(root) {
  const stagingRoot = join(root, "staging");
  const dependencies = {
    schemaVersion: 1,
    python: {
      version: "3.14.6",
      license: "PSF-2.0",
      licenseFile: "Python-3.14.6.txt",
      licenseSource: "https://example.invalid/python-license",
    },
    gcovr: {
      version: "8.6",
      license: "BSD-3-Clause",
      licenseFile: "gcovr-8.6.txt",
      licenseSource: "https://example.invalid/gcovr-license",
    },
    licenseTexts: {
      "BSD-3-Clause": "A complete bundled BSD license text that is deliberately long enough for audit validation. No warranty is provided.",
    },
    packages: [{
      project: "gcovr",
      version: "8.6",
      license: "BSD-3-Clause",
      licenseTextId: "BSD-3-Clause",
      licenseSource: "https://example.invalid/gcovr-license",
      notice: "The gcovr dependency is bundled for offline coverage report generation.",
    }],
  };
  const coverageManifest = {
    schemaVersion: 1,
    python: { version: "3.14.6" },
    gcovr: {
      version: "8.6",
      wheels: [{ project: "gcovr", version: "8.6", kind: "wheel", files: [] }],
    },
  };
  const cmakeManifest = {
    schemaVersion: 1,
    cmakeVersion: "4.3.4",
    license: "BSD-3-Clause",
    archives: {
      "win32-x64": {
        licensePath: "doc/cmake/LICENSE.rst",
      },
    },
  };
  const files = new Map([
    ["licenses/cmake/doc/cmake/LICENSE.rst", "cmake license\n"],
    ["licenses/coverage/licenses/Python-3.14.6.txt", "python license\n"],
    ["licenses/coverage/licenses/dependencies.json", `${JSON.stringify(dependencies, null, 2)}\n`],
    ["licenses/coverage/licenses/gcovr-8.6.txt", "gcovr license\n"],
  ]);
  await writeFixtureFile(stagingRoot, "bundles/cmake/manifest.json", `${JSON.stringify(cmakeManifest, null, 2)}\n`);
  await writeFixtureFile(stagingRoot, "bundles/coverage/manifest.json", `${JSON.stringify(coverageManifest, null, 2)}\n`);
  await writeFixtureFile(
    stagingRoot,
    "bundles/coverage/licenses/dependencies.json",
    `${JSON.stringify(dependencies, null, 2)}\n`,
  );
  for (const [path, value] of files) await writeFixtureFile(stagingRoot, path, value);
  const licenses = [...files].map(([path, value]) => ({
    path,
    size: Buffer.byteLength(value),
    sha256: sha256(value),
  }));
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version: "1.0.0",
    platform: "windows",
    architecture: "x64",
    sourceCommit: "a".repeat(40),
    artifacts: [],
    licenses: [...licenses].reverse(),
    generatedAt: "2026-08-25T00:00:00.000Z",
  };
  await writeFile(join(stagingRoot, "release-manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
  return { dependencies, licenses, stagingRoot };
}

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "release-license-audit-"));
  t.after(async () => rm(root, { recursive: true, force: true }));
  await run(root);
}

test("auditLicenses verifies digest-bearing notices and returns a sorted closed list", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { licenses, stagingRoot } = await createStagingFixture(root);

    const result = await auditLicenses(stagingRoot);

    assert.deepEqual(result, licenses.sort((left, right) => left.path.localeCompare(right.path, "en")));
    assert.deepEqual(Object.keys(result[0]).sort(), ["path", "sha256", "size"]);
  });
});

test("auditLicenses fails when a manifest-listed notice is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    await rm(join(stagingRoot, "licenses", "coverage", "licenses", "gcovr-8.6.txt"));

    await assert.rejects(
      () => auditLicenses(stagingRoot),
      (error) => error?.code === "RELEASE_LICENSE_AUDIT_FAILED" && /missing/u.test(error.message),
    );
  });
});

test("auditLicenses fails when the coverage bundle contains an unlisted dependency", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const manifestPath = join(stagingRoot, "bundles", "coverage", "manifest.json");
    const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
    manifest.gcovr.wheels.push({ project: "unlisted", version: "1.0.0", kind: "wheel", files: [] });
    await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);

    await assert.rejects(
      () => auditLicenses(stagingRoot),
      (error) => error?.code === "RELEASE_LICENSE_AUDIT_FAILED" && /unlisted dependency: unlisted@1\.0\.0/u.test(error.message),
    );
  });
});

test("auditLicenses fails when the dependency notice catalog contains an unknown package", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const dependenciesPath = join(stagingRoot, "bundles", "coverage", "licenses", "dependencies.json");
    const dependencies = JSON.parse(await readFile(dependenciesPath, "utf8"));
    dependencies.packages.push({
      project: "unexpected",
      version: "1.0.0",
      license: "BSD-3-Clause",
      licenseTextId: "BSD-3-Clause",
      licenseSource: "https://example.invalid/unexpected-license",
      notice: "This package is not part of any platform bundle.",
    });
    await writeFile(dependenciesPath, `${JSON.stringify(dependencies, null, 2)}\n`);

    await assert.rejects(
      () => auditLicenses(stagingRoot),
      (error) => error?.code === "RELEASE_LICENSE_AUDIT_FAILED" && /unexpected dependency notice: unexpected@1\.0\.0/u.test(error.message),
    );
  });
});

test("auditLicenses fails when the CMake bundle notice is absent from the closed release license list", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const releaseManifestPath = join(stagingRoot, "release-manifest.json");
    const releaseManifest = JSON.parse(await readFile(releaseManifestPath, "utf8"));
    releaseManifest.licenses = releaseManifest.licenses.filter(({ path }) => !path.includes("LICENSE.rst"));
    await writeFile(releaseManifestPath, `${JSON.stringify(releaseManifest, null, 2)}\n`);

    await assert.rejects(
      () => auditLicenses(stagingRoot),
      (error) => error?.code === "RELEASE_LICENSE_AUDIT_FAILED" && /CMake license notice is unlisted/u.test(error.message),
    );
  });
});

test("auditLicenses accepts the packaged CMake install-root layout when its digest-listed notice is present", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    await rm(join(stagingRoot, "bundles", "cmake", "manifest.json"));

    const result = await auditLicenses(stagingRoot);

    assert.ok(result.some(({ path }) => path === "licenses/cmake/doc/cmake/LICENSE.rst"));
  });
});

test("auditLicenses consumes the packaged coverage resolved lock when the source manifest is absent", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    await rm(join(stagingRoot, "bundles", "coverage", "manifest.json"));
    await writeFixtureFile(stagingRoot, "bundles/coverage/manifest.resolved.json", `${JSON.stringify({
      schemaVersion: 1,
      platform: "windows-x64",
      pythonVersion: "3.14.6",
      gcovrVersion: "8.6",
      inputs: {
        pythonArtifact: {},
        wheels: [{ project: "gcovr", version: "8.6", kind: "wheel", filename: "gcovr.whl", url: "https://example.invalid/gcovr.whl", sha256: "a".repeat(64) }],
        buildSources: [],
        provenance: {},
      },
      outputs: [],
    }, null, 2)}\n`);

    const result = await auditLicenses(stagingRoot);

    assert.ok(result.some(({ path }) => path.endsWith("dependencies.json")));
  });
});

test("auditLicenses accepts the real Linux-resolved wheel lock when Windows-only colorama is omitted", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const sourceManifest = JSON.parse(await readFile(resolve("tools/coverage-bundle/manifest.json"), "utf8"));
    const dependencies = JSON.parse(await readFile(resolve("tools/coverage-bundle/licenses/dependencies.json"), "utf8"));
    const linuxWheels = sourceManifest.gcovr.wheels.flatMap((wheel) => {
      const selected = wheel.files.find(({ platforms }) => platforms.includes("linux-x64"));
      return selected === undefined ? [] : [{
        project: wheel.project,
        version: wheel.version,
        kind: wheel.kind,
        filename: selected.filename,
        url: selected.url,
        sha256: selected.sha256,
      }];
    });
    assert.equal(linuxWheels.some(({ project }) => project === "colorama"), false);
    assert.equal(dependencies.packages.some(({ project }) => project === "colorama"), true);

    await rm(join(stagingRoot, "bundles", "cmake", "manifest.json"));
    await rm(join(stagingRoot, "bundles", "coverage", "manifest.json"));
    await writeFixtureFile(stagingRoot, "bundles/coverage/manifest.resolved.json", `${JSON.stringify({
      schemaVersion: 1,
      platform: "linux-x64",
      pythonVersion: sourceManifest.python.version,
      gcovrVersion: sourceManifest.gcovr.version,
      inputs: {
        pythonArtifact: {},
        wheels: linuxWheels,
        buildSources: [],
        provenance: {},
      },
      outputs: [],
    }, null, 2)}\n`);
    await writeFixtureFile(
      stagingRoot,
      "bundles/coverage/licenses/dependencies.json",
      `${JSON.stringify(dependencies, null, 2)}\n`,
    );
    const releaseManifestPath = join(stagingRoot, "release-manifest.json");
    const releaseManifest = JSON.parse(await readFile(releaseManifestPath, "utf8"));
    releaseManifest.platform = "linux";
    await writeFile(releaseManifestPath, `${JSON.stringify(releaseManifest, null, 2)}\n`);

    const result = await auditLicenses(stagingRoot);

    assert.ok(result.some(({ path }) => path.endsWith("dependencies.json")));
  });
});

test("license audit CLI writes closed non-secret JSON evidence", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const { stagingRoot } = await createStagingFixture(root);
    const evidencePath = join(root, "evidence", "license-audit.json");

    const result = spawnSync(process.execPath, [
      resolve("tools/release/license-audit.mjs"),
      "--staging-root",
      stagingRoot,
      "--out",
      evidencePath,
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      windowsHide: true,
    });

    assert.equal(result.status, 0, result.stderr);
    const evidence = JSON.parse(await readFile(evidencePath, "utf8"));
    assert.deepEqual(Object.keys(evidence), [
      "schemaVersion",
      "product",
      "version",
      "platform",
      "sourceCommit",
      "licenses",
      "passed",
    ]);
    assert.equal(evidence.passed, true);
    assert.doesNotMatch(JSON.stringify(evidence), /release-license-audit-|[A-Z]:\\/iu);
  });
});
