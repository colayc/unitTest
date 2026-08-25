import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import {
  buildReleaseManifest,
  toDeterministicManifestBytes,
} from "./manifest.mjs";

const root = new URL("./", import.meta.url);
const sourceDateEpoch = "1787616000";
const generatedAt = "2026-08-25T00:00:00.000Z";

function buildManifest(input, options = {}) {
  return buildReleaseManifest(input, { sourceDateEpoch, ...options });
}

function sha256Text(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function readJson(name) {
  return JSON.parse(await readFile(new URL(name, root), "utf8"));
}

function validate(schema, value) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const check = ajv.compile(schema);
  assert.equal(check(value), true, ajv.errorsText(check.errors));
}

async function withStaging(t, run) {
  const directory = await mkdtemp(join(tmpdir(), "release-manifest-"));
  t.after(async () => {
    await rm(directory, { recursive: true, force: true });
  });
  await run(directory);
}

async function writeArtifact(rootDirectory, relativePath, bytes) {
  const path = join(rootDirectory, ...relativePath.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, bytes);
  const info = await stat(path);
  return {
    relativePath,
    size: info.size,
    sha256: sha256Text(bytes),
  };
}

async function validInput(stagingRoot) {
  const alpha = await writeArtifact(stagingRoot, "bin/unit-test-ide.exe", "alpha executable\n");
  const beta = await writeArtifact(stagingRoot, "share/doc/readme.txt", "beta readme\n");
  const pythonLicense = await writeArtifact(stagingRoot, "licenses/Python-3.14.6.txt", "fixture python license\n");
  const gcovrLicense = await writeArtifact(stagingRoot, "licenses/gcovr-8.6.txt", "fixture gcovr license\n");
  return {
    version: "1.2.3",
    platform: "windows",
    architecture: "x64",
    stagingRoot,
    sourceCommit: "a".repeat(40),
    licenses: ["licenses/Python-3.14.6.txt", "licenses/gcovr-8.6.txt"],
    expectedLicenses: [
      { path: pythonLicense.relativePath, size: pythonLicense.size, sha256: pythonLicense.sha256 },
      { path: gcovrLicense.relativePath, size: gcovrLicense.size, sha256: gcovrLicense.sha256 },
    ].sort((left, right) => left.path.localeCompare(right.path, "en")),
    artifacts: [
      {
        id: "readme",
        kind: "documentation",
        executable: false,
        ...beta,
      },
      {
        id: "cli",
        kind: "executable",
        executable: true,
        ...alpha,
      },
    ],
  };
}

test("buildReleaseManifest sorts artifacts deterministically and emits only the closed contract", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, fixture] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const { expectedLicenses, ...input } = fixture;
    const manifest = await buildManifest(input);
    validate(schema, manifest);
    assert.deepEqual(Object.keys(manifest).sort(), [
      "architecture",
      "artifacts",
      "generatedAt",
      "licenses",
      "platform",
      "product",
      "schemaVersion",
      "sourceCommit",
      "version",
    ]);
    assert.equal(manifest.product, "unit-test-ide");
    assert.equal(manifest.schemaVersion, 1);
    assert.equal(manifest.generatedAt, generatedAt);
    assert.deepEqual(
      manifest.artifacts.map(({ id, relativePath }) => ({ id, relativePath })),
      [
        { id: "cli", relativePath: "bin/unit-test-ide.exe" },
        { id: "readme", relativePath: "share/doc/readme.txt" },
      ],
    );
    assert.deepEqual(manifest.licenses, expectedLicenses);
  });
});

test("buildReleaseManifest rejects absolute artifact paths and parent traversal", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    for (const [name, relativePath] of [
      ["absolute POSIX path", "/escape.txt"],
      ["absolute Windows path", "C:/escape.txt"],
      ["parent traversal", "../escape.txt"],
    ]) {
      const { expectedLicenses, ...input } = await validInput(stagingRoot);
      void expectedLicenses;
      input.artifacts[0].relativePath = relativePath;
      await assert.rejects(
        () => buildManifest(input),
        /unsafe artifact path/u,
        name,
      );
    }
  });
});

test("buildReleaseManifest rejects duplicate artifact ids", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    input.artifacts[1].id = input.artifacts[0].id;
    await assert.rejects(
      () => buildManifest(input),
      /duplicate artifact id/u,
    );
  });
});

test("buildReleaseManifest rejects intermediate junction or symlink parents", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const linkedDirectory = join(stagingRoot, "linked-bin");
    await mkdir(linkedDirectory, { recursive: true });
    const bytes = "linked executable\n";
    const linkedArtifact = await writeArtifact(linkedDirectory, "unit-test-ide.exe", bytes);
    await writeArtifact(stagingRoot, "licenses/Python-3.14.6.txt", "fixture python license\n");
    await symlink(linkedDirectory, join(stagingRoot, "bin"), "junction");
    const input = {
      version: "1.2.3",
      platform: "windows",
      architecture: "x64",
      stagingRoot,
      sourceCommit: "a".repeat(40),
      licenses: ["licenses/Python-3.14.6.txt"],
      artifacts: [{
      id: "cli",
      kind: "executable",
      relativePath: "bin/unit-test-ide.exe",
      size: linkedArtifact.size,
      sha256: linkedArtifact.sha256,
      executable: true,
      }],
    };
    await assert.rejects(
      () => buildManifest(input),
      /unsafe artifact path/u,
    );
  });
});

test("buildReleaseManifest rejects size and digest mismatches", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    for (const [name, mutate] of [
      ["size mismatch", (artifact) => { artifact.size += 1; }],
      ["digest mismatch", (artifact) => { artifact.sha256 = "b".repeat(64); }],
    ]) {
      const { expectedLicenses, ...input } = await validInput(stagingRoot);
      void expectedLicenses;
      mutate(input.artifacts[0]);
      await assert.rejects(
        () => buildManifest(input),
        /artifact (?:size|sha256) mismatch/u,
        name,
      );
    }
  });
});

test("buildReleaseManifest rejects license size and digest mismatches", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...baseInput } = await validInput(stagingRoot);
    const input = {
      ...baseInput,
      licenses: expectedLicenses,
    };
    await writeFile(join(stagingRoot, "licenses", "Python-3.14.6.txt"), "tampered license bytes\n");
    await assert.rejects(
      () => buildManifest(input),
      /license (?:size|sha256) mismatch/u,
    );
  });
});

test("release manifest CLI with --config writes the configured output artifact", async (t) => {
  const outputPath = resolve("tools/release/manifest.generated.json");
  t.after(async () => {
    await rm(outputPath, { force: true });
  });
  execFileSync(process.execPath, [
    resolve("tools/release/manifest.mjs"),
    "--config",
    resolve("tools/release/release-config.json"),
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: { ...process.env, SOURCE_DATE_EPOCH: sourceDateEpoch },
    windowsHide: true,
  });

  const manifest = JSON.parse(await readFile(outputPath, "utf8"));
    assert.equal(manifest.product, "unit-test-ide");
    assert.equal(manifest.version, "1.2.3");
});

test("schema rejects unknown top-level keys", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, input] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const { expectedLicenses, ...buildInput } = input;
    void expectedLicenses;
    const manifest = await buildManifest(buildInput);
    manifest.unexpected = true;
    const ajv = new Ajv2020({ allErrors: true, strict: true });
    addFormats(ajv);
    const check = ajv.compile(schema);
    assert.equal(check(manifest), false);
  });
});

test("deterministic manifest bytes omit generatedAt", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    const manifest = await buildManifest(input);
    const later = { ...manifest, generatedAt: "2026-08-26T00:00:00.000Z" };
    assert.equal(
      toDeterministicManifestBytes(manifest).toString("utf8"),
      toDeterministicManifestBytes(later).toString("utf8"),
    );
    const parsed = JSON.parse(toDeterministicManifestBytes(manifest).toString("utf8"));
    assert.equal(parsed.generatedAt, undefined);
    assert.equal(parsed.product, "unit-test-ide");
  });
});

test("release manifest generation fails closed without a reproducible source epoch", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    await assert.rejects(
      () => buildReleaseManifest(input, { sourceDateEpoch: undefined }),
      /SOURCE_DATE_EPOCH/u,
    );
  });
});

test("identical inputs and SOURCE_DATE_EPOCH yield identical full manifest bytes", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    const first = await buildManifest(input);
    const second = await buildManifest(input);
    const serialize = (value) => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);

    assert.equal(first.generatedAt, generatedAt);
    assert.deepEqual(serialize(first), serialize(second));
  });
});
