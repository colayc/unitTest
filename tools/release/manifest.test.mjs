import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import test from "node:test";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import {
  buildReleaseManifest,
  toDeterministicManifestBytes,
} from "./manifest.mjs";

const root = new URL("./", import.meta.url);

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
  return {
    version: "1.2.3",
    platform: "windows",
    architecture: "x64",
    stagingRoot,
    sourceCommit: "a".repeat(40),
    licenses: ["licenses/Python-3.14.6.txt", "licenses/gcovr-8.6.txt"],
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
    const [schema, input] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const manifest = await buildReleaseManifest(input);
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
    assert.match(manifest.generatedAt, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z$/u);
    assert.deepEqual(
      manifest.artifacts.map(({ id, relativePath }) => ({ id, relativePath })),
      [
        { id: "cli", relativePath: "bin/unit-test-ide.exe" },
        { id: "readme", relativePath: "share/doc/readme.txt" },
      ],
    );
  });
});

test("buildReleaseManifest rejects absolute artifact paths and parent traversal", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    for (const [name, relativePath] of [
      ["absolute POSIX path", "/escape.txt"],
      ["absolute Windows path", "C:/escape.txt"],
      ["parent traversal", "../escape.txt"],
    ]) {
      const input = await validInput(stagingRoot);
      input.artifacts[0].relativePath = relativePath;
      await assert.rejects(
        () => buildReleaseManifest(input),
        /unsafe artifact path/u,
        name,
      );
    }
  });
});

test("buildReleaseManifest rejects duplicate artifact ids", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const input = await validInput(stagingRoot);
    input.artifacts[1].id = input.artifacts[0].id;
    await assert.rejects(
      () => buildReleaseManifest(input),
      /duplicate artifact id/u,
    );
  });
});

test("buildReleaseManifest rejects size and digest mismatches", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    for (const [name, mutate] of [
      ["size mismatch", (artifact) => { artifact.size += 1; }],
      ["digest mismatch", (artifact) => { artifact.sha256 = "b".repeat(64); }],
    ]) {
      const input = await validInput(stagingRoot);
      mutate(input.artifacts[0]);
      await assert.rejects(
        () => buildReleaseManifest(input),
        /artifact (?:size|sha256) mismatch/u,
        name,
      );
    }
  });
});

test("schema rejects unknown top-level keys", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, input] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const manifest = await buildReleaseManifest(input);
    manifest.unexpected = true;
    const ajv = new Ajv2020({ allErrors: true, strict: true });
    addFormats(ajv);
    const check = ajv.compile(schema);
    assert.equal(check(manifest), false);
  });
});

test("deterministic manifest bytes omit generatedAt", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const input = await validInput(stagingRoot);
    const manifest = await buildReleaseManifest(input);
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
