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
import { validateReleaseManifestRecord } from "./release-manifest-validation.mjs";

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

function compileSchema(schema) {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  return ajv.compile(schema);
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

test("buildReleaseManifest accepts internal spaces but rejects unsafe spaced paths", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    const launcher = await writeArtifact(stagingRoot, "app/code-oss-runtime/Code - OSS.exe", "Code - OSS launcher\n");
    input.artifacts[0] = {
      id: "code-oss-launcher",
      kind: "runtime",
      executable: true,
      ...launcher,
    };

    const manifest = await buildManifest(input);
    assert.ok(manifest.artifacts.some(({ relativePath }) => relativePath === "app/code-oss-runtime/Code - OSS.exe"));

    for (const relativePath of [
      "app/code-oss-runtime/../Code - OSS.exe",
      "app/code-oss-runtime/Code - OSS.exe ",
    ]) {
      const { expectedLicenses: ignoredLicenses, ...unsafeInput } = await validInput(stagingRoot);
      void ignoredLicenses;
      unsafeInput.artifacts[0].relativePath = relativePath;
      await assert.rejects(() => buildManifest(unsafeInput), /unsafe artifact path/u);
    }
  });
});

test("manifest schema permits internal spaces but rejects unsafe Windows path components", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, fixture] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const { expectedLicenses, ...input } = fixture;
    void expectedLicenses;
    const launcher = await writeArtifact(stagingRoot, "app/code-oss-runtime/Code - OSS.exe", "Code - OSS launcher\n");
    input.artifacts[0] = {
      id: "code-oss-launcher",
      kind: "runtime",
      executable: true,
      ...launcher,
    };
    const manifest = await buildManifest(input);
    const ajv = new Ajv2020({ allErrors: true, strict: true });
    addFormats(ajv);
    const check = ajv.compile(schema);
    assert.equal(check(manifest), true, ajv.errorsText(check.errors));

    for (const relativePath of [
      "app/code-oss-runtime/ Code - OSS.exe",
      "app/code-oss-runtime/Code - OSS.exe ",
      "app/code-oss-runtime/runtime.",
      "app/code-oss-runtime/CON.txt",
      "app/code-oss-runtime/lpt9.log",
      "app/code-oss-runtime/control\u0001.txt",
    ]) {
      const unsafeManifest = JSON.parse(JSON.stringify(manifest));
      unsafeManifest.artifacts[0].relativePath = relativePath;
      assert.equal(check(unsafeManifest), false, relativePath);
    }
  });
});

test("manifest schema and record validation accept literal real Code-OSS paths", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, fixture] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const { expectedLicenses, ...input } = fixture;
    void expectedLicenses;
    const manifest = await buildManifest(input);
    manifest.artifacts[0].relativePath = "app/code-oss-runtime/resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe";
    manifest.licenses[0].path = "app/code-oss-runtime/resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage";
    manifest.licenses.sort((left, right) => left.path.localeCompare(right.path, "en"));

    const check = compileSchema(schema);
    assert.equal(check(manifest), true, check.errors?.map(({ message }) => message).join("; "));
    assert.equal(validateReleaseManifestRecord(manifest), manifest);
  });
});

test("manifest schema and record validation reject unsafe artifact and license paths", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const [schema, fixture] = await Promise.all([
      readJson("manifest.schema.json"),
      validInput(stagingRoot),
    ]);
    const { expectedLicenses, ...input } = fixture;
    void expectedLicenses;
    const manifest = await buildManifest(input);
    const check = compileSchema(schema);
    const unsafePaths = [
      "app//x",
      "app/x/",
      "/app/x",
      "C:/app/x",
      "app/./x",
      "app/../x",
      "app\\x",
      "app/x:y",
      "app/less<than.txt",
      "app/greater>than.txt",
      "app/quote\"name.txt",
      "app/pipe|name.txt",
      "app/question?.txt",
      "app/star*.txt",
      "app/control\u0001.txt",
      "app/hash#name.txt",
      "app/caf\u00e9.txt",
      "app/ leading.txt",
      "app/trailing ",
      "app/trailing.",
      "app/CON.txt",
      "app/com1.exe",
    ];

    for (const field of ["artifact", "license"]) {
      for (const relativePath of unsafePaths) {
        const unsafeManifest = structuredClone(manifest);
        if (field === "artifact") unsafeManifest.artifacts[0].relativePath = relativePath;
        else {
          unsafeManifest.licenses[0].path = relativePath;
          unsafeManifest.licenses.sort((left, right) => left.path.localeCompare(right.path, "en"));
        }
        assert.equal(check(unsafeManifest), false, `${field}: ${relativePath}`);
        assert.throws(
          () => validateReleaseManifestRecord(unsafeManifest),
          /release manifest schema is invalid|portable/u,
          `${field}: ${relativePath}`,
        );
      }
    }
  });
});

test("record validation rejects ASCII case aliases and the reserved release manifest path", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    const manifest = await buildManifest(input);
    const cases = [
      ["artifact/artifact alias", (value) => {
        value.artifacts.push({
          ...value.artifacts[0],
          id: "case-alias",
          relativePath: value.artifacts[0].relativePath.toUpperCase(),
          sha256: "b".repeat(64),
        });
        value.artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));
      }],
      ["artifact/license alias", (value) => {
        value.licenses.push({
          path: value.artifacts[0].relativePath.toUpperCase(),
          size: value.artifacts[0].size,
          sha256: "c".repeat(64),
        });
        value.licenses.sort((left, right) => left.path.localeCompare(right.path, "en"));
      }],
      ["reserved release manifest alias", (value) => {
        value.artifacts.push({
          ...value.artifacts[0],
          id: "reserved-manifest",
          relativePath: "Release-Manifest.json",
          sha256: "d".repeat(64),
        });
        value.artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));
      }],
    ];

    for (const [name, mutate] of cases) {
      const unsafeManifest = structuredClone(manifest);
      mutate(unsafeManifest);
      assert.throws(
        () => validateReleaseManifestRecord(unsafeManifest),
        /duplicate or reserved release payload path/u,
        name,
      );
    }
  });
});

test("buildReleaseManifest accepts literal real Code-OSS artifact and license paths", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const { expectedLicenses, ...input } = await validInput(stagingRoot);
    void expectedLicenses;
    const artifact = await writeArtifact(
      stagingRoot,
      "app/code-oss-runtime/resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe",
      "ripgrep\n",
    );
    const license = await writeArtifact(
      stagingRoot,
      "app/code-oss-runtime/resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage",
      "grammar\n",
    );
    input.artifacts[0] = {
      id: "ripgrep",
      kind: "runtime",
      executable: true,
      ...artifact,
    };
    input.licenses = [license.relativePath];

    const manifest = await buildManifest(input);
    assert.equal(manifest.artifacts.find(({ id }) => id === "ripgrep")?.relativePath, artifact.relativePath);
    assert.equal(manifest.licenses[0].path, license.relativePath);
  });
});

test("buildReleaseManifest rejects unsafe artifact and license paths before filesystem access", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    const unsafePaths = [
      "app//x",
      "app/x/",
      "/app/x",
      "C:/app/x",
      "app/./x",
      "app/../x",
      "app\\x",
      "app/x:y",
      "app/less<than.txt",
      "app/greater>than.txt",
      "app/quote\"name.txt",
      "app/pipe|name.txt",
      "app/question?.txt",
      "app/star*.txt",
      "app/control\u0001.txt",
      "app/hash#name.txt",
      "app/caf\u00e9.txt",
      "app/ leading.txt",
      "app/trailing ",
      "app/trailing.",
      "app/CON.txt",
      "app/com1.exe",
    ];
    for (const relativePath of unsafePaths) {
      const { expectedLicenses, ...artifactInput } = await validInput(stagingRoot);
      void expectedLicenses;
      artifactInput.artifacts[0].relativePath = relativePath;
      await assert.rejects(() => buildManifest(artifactInput), /unsafe artifact path/u, `artifact: ${relativePath}`);

      const { expectedLicenses: ignoredLicenses, ...licenseInput } = await validInput(stagingRoot);
      void ignoredLicenses;
      licenseInput.licenses = [relativePath];
      await assert.rejects(() => buildManifest(licenseInput), /unsafe license path/u, `license: ${relativePath}`);
    }
  });
});

test("buildReleaseManifest rejects leading-space and Windows-device artifact paths", async (t) => {
  await withStaging(t, async (stagingRoot) => {
    for (const relativePath of ["app/ leading.txt", "CON.txt", "control\u0001.txt"]) {
      const { expectedLicenses, ...input } = await validInput(stagingRoot);
      void expectedLicenses;
      const artifact = relativePath.startsWith("app/")
        ? await writeArtifact(stagingRoot, relativePath, "unsafe artifact\n")
        : input.artifacts[0];
      await assert.rejects(
        () => buildManifest({
          ...input,
          artifacts: [{ ...input.artifacts[0], ...artifact, relativePath }, input.artifacts[1]],
        }),
        /unsafe artifact path/u,
        relativePath,
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
