import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import test from "node:test";

import { packageAppImage } from "./package-appimage.mjs";
import { verifyAppImage } from "./verify-appimage.mjs";

const sourceDateEpoch = "1787616000";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

const verifyCli = resolve("tools/release/linux/verify-appimage.mjs");
const iconSource = resolve("tools/release/linux/unit-test-ide.svg");

async function withTemporaryRoot(t, run) {
  const baseRoot = join(resolve("."), ".tmp", "release-appimage");
  await mkdir(baseRoot, { recursive: true });
  const root = await mkdtemp(join(baseRoot, "case-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  await run(root);
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function createStagingFixture(root, version = "1.2.3") {
  const stagingRoot = join(root, "staging");
  const runtime = "#!/bin/sh\nexit 0\n";
  const product = "{\"name\":\"Code - OSS\"}\n";
  const locale = "locale data\n";
  const service = "service binary\n";
  const notice = "license notice\n";
  const cmake = "cmake bundle\n";
  const coverage = "coverage bundle\n";
  const launcherPath = await writeFixtureFile(stagingRoot, "app/code-oss-runtime/code-oss", runtime);
  await chmod(launcherPath, 0o755);
  await writeFixtureFile(stagingRoot, "app/code-oss-runtime/resources/app/product.json", product);
  await writeFixtureFile(stagingRoot, "app/code-oss-runtime/locales/en-US.pak", locale);
  await writeFixtureFile(stagingRoot, "service/unit-test-service", service);
  await writeFixtureFile(stagingRoot, "bundles/cmake/bin/cmake", cmake);
  await writeFixtureFile(stagingRoot, "bundles/coverage/app/gcovr-runner.pyz", coverage);
  await writeFixtureFile(stagingRoot, "licenses/NOTICE.txt", notice);
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version,
    platform: "linux",
    architecture: "x64",
    sourceCommit: "a".repeat(40),
    artifacts: [
      {
        id: "cmake",
        kind: "bundle-cmake",
        relativePath: "bundles/cmake/bin/cmake",
        size: Buffer.byteLength(cmake),
        sha256: sha256(cmake),
        executable: false,
      },
      {
        id: "coverage",
        kind: "bundle-coverage",
        relativePath: "bundles/coverage/app/gcovr-runner.pyz",
        size: Buffer.byteLength(coverage),
        sha256: sha256(coverage),
        executable: false,
      },
      {
        id: "runtime",
        kind: "runtime",
        relativePath: "app/code-oss-runtime/code-oss",
        size: Buffer.byteLength(runtime),
        sha256: sha256(runtime),
        executable: true,
      },
      {
        id: "runtime-locale",
        kind: "runtime",
        relativePath: "app/code-oss-runtime/locales/en-US.pak",
        size: Buffer.byteLength(locale),
        sha256: sha256(locale),
        executable: false,
      },
      {
        id: "runtime-product",
        kind: "runtime",
        relativePath: "app/code-oss-runtime/resources/app/product.json",
        size: Buffer.byteLength(product),
        sha256: sha256(product),
        executable: false,
      },
      {
        id: "service",
        kind: "service",
        relativePath: "service/unit-test-service",
        size: Buffer.byteLength(service),
        sha256: sha256(service),
        executable: true,
      },
    ],
    licenses: [
      {
        path: "licenses/NOTICE.txt",
        size: Buffer.byteLength(notice),
        sha256: sha256(notice),
      },
    ],
    generatedAt: "2026-08-25T00:00:00.000Z",
  };
  const manifestPath = await writeFixtureFile(
    stagingRoot,
    "release-manifest.json",
    `${JSON.stringify(manifest, null, 2)}\n`,
  );
  return {
    manifestPath,
    outputPath: join(root, "dist", `unit-test-ide-${version}.AppImage`),
    stagingRoot,
    version,
  };
}

async function createFakeAppImageTool(root) {
  const toolScript = join(root, "fake-appimagetool.mjs");
  await writeFile(toolScript, `
import { createHash } from "node:crypto";
import { mkdir, readFile, readdir, stat, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

async function collect(rootPath, current = "") {
  const absolutePath = current ? join(rootPath, ...current.split("/")) : rootPath;
  const entries = await readdir(absolutePath, { withFileTypes: true });
  entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
  const files = {};
  for (const entry of entries) {
    const relativePath = current ? \`\${current}/\${entry.name}\` : entry.name;
    const entryPath = join(rootPath, ...relativePath.split("/"));
    if (entry.isDirectory()) {
      Object.assign(files, await collect(rootPath, relativePath));
      continue;
    }
    if (!entry.isFile()) {
      throw new Error(\`unsupported fake AppImage entry: \${relativePath}\`);
    }
    const bytes = await readFile(entryPath);
    const info = await stat(entryPath);
    files[relativePath] = {
      sha256: sha256(bytes),
      size: info.size,
      executable: (info.mode & 0o111) !== 0
        || relativePath === "AppRun"
        || relativePath.endsWith("/app/code-oss-runtime/code-oss")
        || relativePath.endsWith("/service/unit-test-service"),
      contentBase64: bytes.toString("base64"),
    };
  }
  return files;
}

const [appDir, output] = process.argv.slice(2);
if (!appDir || !output) {
  throw new Error("fake appimagetool expects <AppDir> <output>");
}
const payload = {
  marker: "UNIT_TEST_IDE_FAKE_APPIMAGE",
  files: await collect(appDir),
};
await mkdir(dirname(output), { recursive: true });
await writeFile(output, JSON.stringify(payload, null, 2));
`, "utf8");
  return {
    path: toolScript,
    sha256: sha256(await readFile(toolScript)),
  };
}

async function parseFakeEnvelope(imagePath) {
  return JSON.parse(await readFile(imagePath, "utf8"));
}

function createFakeEnvelopeExtractor() {
  return async (imagePath) => {
    const envelope = await parseFakeEnvelope(imagePath);
    assert.equal(envelope.marker, "UNIT_TEST_IDE_FAKE_APPIMAGE");
    const files = new Map();
    for (const [relativePath, entry] of Object.entries(envelope.files)) {
      const content = Buffer.from(entry.contentBase64, "base64");
      files.set(relativePath, {
        size: content.length,
        sha256: sha256(content),
        executable: Boolean(entry.executable),
        content,
      });
    }
    return {
      files,
      cleanup: async () => {},
    };
  };
}

async function updateFakeEnvelope(imagePath, mutate) {
  const envelope = await parseFakeEnvelope(imagePath);
  await mutate(envelope);
  await writeFile(imagePath, `${JSON.stringify(envelope, null, 2)}\n`);
}

async function refreshSidecarManifest(manifestPath, imagePath, mutate) {
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  if (mutate) {
    await mutate(manifest);
  }
  manifest.packageSha256 = sha256(await readFile(imagePath));
  await writeFile(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
}

async function mutateEmbeddedReleaseManifest(result, mutate) {
  let embeddedManifestBytes;
  await updateFakeEnvelope(result.outputPath, async (envelope) => {
    const path = "usr/lib/unit-test-ide/release-manifest.json";
    const manifest = JSON.parse(Buffer.from(envelope.files[path].contentBase64, "base64").toString("utf8"));
    mutate(manifest);
    embeddedManifestBytes = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`);
    envelope.files[path].contentBase64 = embeddedManifestBytes.toString("base64");
  });
  await refreshSidecarManifest(result.manifestPath, result.outputPath, async (manifest) => {
    manifest.releaseManifestSha256 = sha256(embeddedManifestBytes);
  });
}

async function packageWithFakeTool(root, version = "1.2.3") {
  const fixture = await createStagingFixture(root, version);
  const fakeTool = await createFakeAppImageTool(root);
  const extractor = createFakeEnvelopeExtractor();
  const result = await packageAppImage({
    stagingRoot: fixture.stagingRoot,
    output: fixture.outputPath,
    appimagetool: fakeTool.path,
    expectedDigest: fakeTool.sha256,
    sourceDateEpoch,
    version: fixture.version,
    architecture: "x64",
    verificationExtractor: extractor,
  });
  return {
    ...fixture,
    ...result,
    extractor,
  };
}

test("packageAppImage fails closed when appimagetool is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    await assert.rejects(
      () => packageAppImage({
        stagingRoot: fixture.stagingRoot,
        output: fixture.outputPath,
        appimagetool: join(root, "missing-appimagetool"),
        expectedDigest: fakeTool.sha256,
        sourceDateEpoch,
        version: fixture.version,
        architecture: "x64",
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_TOOL_MISSING");
        assert.match(error?.message ?? "", /appimagetool/i);
        return true;
      },
    );
  });
});

test("packageAppImage fails closed when AppRun is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    await assert.rejects(
      () => packageAppImage({
        stagingRoot: fixture.stagingRoot,
        output: fixture.outputPath,
        appimagetool: fakeTool.path,
        expectedDigest: fakeTool.sha256,
        sourceDateEpoch,
        version: fixture.version,
        architecture: "x64",
        appRunTemplatePath: join(root, "missing-AppRun"),
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_TEMPLATE_MISSING");
        assert.match(error?.message ?? "", /AppRun/u);
        return true;
      },
    );
  });
});

test("packageAppImage fails closed when the fixed icon template is missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    await assert.rejects(
      () => packageAppImage({
        stagingRoot: fixture.stagingRoot,
        output: fixture.outputPath,
        appimagetool: fakeTool.path,
        expectedDigest: fakeTool.sha256,
        sourceDateEpoch,
        version: fixture.version,
        architecture: "x64",
        iconPath: join(root, "missing-unit-test-ide.svg"),
      }),
      (error) => {
        assert.equal(error?.code, "RELEASE_TEMPLATE_MISSING");
        assert.match(error?.message ?? "", /icon template/u);
        return true;
      },
    );
  });
});

test("packageAppImage fails closed when SOURCE_DATE_EPOCH is absent", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    await assert.rejects(
      () => packageAppImage({
        stagingRoot: fixture.stagingRoot,
        output: fixture.outputPath,
        appimagetool: fakeTool.path,
        expectedDigest: fakeTool.sha256,
        version: fixture.version,
        architecture: "x64",
      }),
      /SOURCE_DATE_EPOCH/u,
    );
  });
});

test("packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);

    const desktop = await readFile(join(result.appDir, "unit-test-ide.desktop"), "utf8");
    assert.match(desktop, /^Exec=usr\/lib\/unit-test-ide\/app\/code-oss-runtime\/code-oss$/mu);
    assert.match(desktop, /^TryExec=usr\/lib\/unit-test-ide\/app\/code-oss-runtime\/code-oss$/mu);

    const iconBytes = await readFile(iconSource);
    const appDirIcon = await readFile(join(result.appDir, "unit-test-ide.svg"));
    assert.deepEqual(appDirIcon, iconBytes);
    const envelope = await parseFakeEnvelope(result.outputPath);
    const embeddedIcon = envelope.files["unit-test-ide.svg"];
    assert.ok(embeddedIcon);
    assert.deepEqual(Buffer.from(embeddedIcon.contentBase64, "base64"), iconBytes);
    assert.equal(embeddedIcon.executable, false);

    const digestManifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    assert.equal(digestManifest.packageFile, basename(result.outputPath));
    assert.equal(digestManifest.launcher, "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss");
    assert.doesNotMatch(JSON.stringify(digestManifest), /https?:\/\//u);
    assert.ok(!JSON.stringify(digestManifest).includes(root.replaceAll("\\", "/")));
    assert.ok(!JSON.stringify(digestManifest).includes(resolve(root)));

    const verification = await verifyAppImage({
      image: result.outputPath,
      manifest: result.manifestPath,
      requireDigest: true,
      extractor: result.extractor,
    });
    assert.equal(verification.packageSha256, digestManifest.packageSha256);
    assert.equal(verification.launcher, "usr/lib/unit-test-ide/app/code-oss-runtime/code-oss");
    assert.equal(verification.releaseManifestSha256, digestManifest.releaseManifestSha256);
  });
});

test("verifyAppImage rejects missing, tampered, executable, and aliased icons", async (t) => {
  const cases = [
    ["missing", (files) => { delete files["unit-test-ide.svg"]; }, /icon is missing/u],
    ["tampered", (files) => {
      files["unit-test-ide.svg"].contentBase64 = Buffer.from("<svg/>\n").toString("base64");
    }, /icon content does not match/u],
    ["executable", (files) => { files["unit-test-ide.svg"].executable = true; }, /icon executable bit/u],
    ["alias", (files) => {
      files["unit-test-ide.png"] = { ...files["unit-test-ide.svg"] };
    }, /unexpected payload path: unit-test-ide\.png/u],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate, expected] of cases) {
      const result = await packageWithFakeTool(join(root, name));
      await updateFakeEnvelope(result.outputPath, async (envelope) => mutate(envelope.files));
      await refreshSidecarManifest(result.manifestPath, result.outputPath);
      await assert.rejects(
        () => verifyAppImage({
          image: result.outputPath,
          manifest: result.manifestPath,
          requireDigest: true,
          extractor: result.extractor,
        }),
        expected,
        name,
      );
    }
  });
});

test("verifyAppImage rejects a tampered launcher even when the sidecar digest is regenerated", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await updateFakeEnvelope(result.outputPath, async (envelope) => {
      envelope.files["usr/lib/unit-test-ide/app/code-oss-runtime/code-oss"].contentBase64 = Buffer.from("#!/bin/sh\necho tampered\n").toString("base64");
    });
    await refreshSidecarManifest(result.manifestPath, result.outputPath);

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /artifact runtime .*sha256|artifact runtime .*size|artifact runtime .*executable/u,
    );
  });
});

test("verifyAppImage rejects a tampered runtime resource even when the sidecar digest is regenerated", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await updateFakeEnvelope(result.outputPath, async (envelope) => {
      envelope.files["usr/lib/unit-test-ide/app/code-oss-runtime/resources/app/product.json"].contentBase64 = Buffer.from("{\"name\":\"tampered\"}\n").toString("base64");
    });
    await refreshSidecarManifest(result.manifestPath, result.outputPath);

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /artifact runtime-product .*sha256|artifact runtime-product .*size/u,
    );
  });
});

test("public verify CLI rejects a fake AppImage envelope marker", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    const cli = spawnSync(process.execPath, [
      verifyCli,
      "--image", result.outputPath,
      "--manifest", result.manifestPath,
      "--require-digest",
    ], {
      cwd: resolve("."),
      encoding: "utf8",
      windowsHide: true,
    });

    assert.equal(cli.status, 1);
    assert.match(cli.stderr, /fake AppImage envelope is not accepted by the public verifier/u);
  });
});

test("verifyAppImage rejects sidecar path substitution instead of trusting redirected layout paths", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await refreshSidecarManifest(result.manifestPath, result.outputPath, async (manifest) => {
      manifest.launcher = "usr/lib/unit-test-ide/bin/alternate-launcher";
    });

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /launcher path must be fixed/u,
    );
  });
});

test("verifyAppImage rejects embedded release-manifest identity drift", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [field, value] of [
      ["product", "other-product"],
      ["version", "9.9.9"],
      ["schemaVersion", 9],
    ]) {
      const result = await packageWithFakeTool(join(root, `${field}`));
      await updateFakeEnvelope(result.outputPath, async (envelope) => {
        const relativePath = "usr/lib/unit-test-ide/release-manifest.json";
        const manifest = JSON.parse(Buffer.from(envelope.files[relativePath].contentBase64, "base64").toString("utf8"));
        manifest[field] = value;
        envelope.files[relativePath].contentBase64 = Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`).toString("base64");
      });
      const envelope = await parseFakeEnvelope(result.outputPath);
      const embeddedManifestBytes = Buffer.from(envelope.files["usr/lib/unit-test-ide/release-manifest.json"].contentBase64, "base64");
      await refreshSidecarManifest(result.manifestPath, result.outputPath, async (manifest) => {
        manifest.releaseManifestSha256 = sha256(embeddedManifestBytes);
      });

      await assert.rejects(
        () => verifyAppImage({
          image: result.outputPath,
          manifest: result.manifestPath,
          requireDigest: true,
          extractor: result.extractor,
        }),
        /embedded release manifest schema\/semantics are invalid/u,
        `${field} drift should fail`,
      );
    }
  });
});

test("verifyAppImage rejects open and duplicate-path embedded release manifests", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate] of [
      ["open manifest", (manifest) => { manifest.unreviewed = true; }],
      ["empty artifact list", (manifest) => { manifest.artifacts = []; }],
      ["unsafe artifact size", (manifest) => { manifest.artifacts[0].size = Number.MAX_SAFE_INTEGER + 1; }],
      ["duplicate artifact path", (manifest) => {
        manifest.artifacts.push({ ...manifest.artifacts[0], id: "alternate-runtime" });
      }],
    ]) {
      const result = await packageWithFakeTool(join(root, name.replaceAll(" ", "-")));
      await mutateEmbeddedReleaseManifest(result, mutate);

      await assert.rejects(
        () => verifyAppImage({
          image: result.outputPath,
          manifest: result.manifestPath,
          requireDigest: true,
          extractor: result.extractor,
        }),
        /release manifest.*(?:closed|schema|duplicate|invalid)/iu,
        name,
      );
    }
  });
});

test("verifyAppImage rejects desktop or launcher contract mismatches", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await updateFakeEnvelope(result.outputPath, async (envelope) => {
      envelope.files["unit-test-ide.desktop"].contentBase64 = Buffer.from([
        "[Desktop Entry]",
        "Type=Application",
        "Name=Unit Test IDE",
        "Exec=usr/lib/unit-test-ide/app/not-code-oss",
        "TryExec=usr/lib/unit-test-ide/app/not-code-oss",
        "Icon=unit-test-ide",
        "Categories=Development;IDE;",
        "Terminal=false",
        "X-AppImage-Version=1.2.3",
        "",
      ].join("\n")).toString("base64");
    });
    const envelope = await parseFakeEnvelope(result.outputPath);
    const desktopBytes = Buffer.from(envelope.files["unit-test-ide.desktop"].contentBase64, "base64");
    await refreshSidecarManifest(result.manifestPath, result.outputPath, async (manifest) => {
      manifest.packageSha256 = sha256(await readFile(result.outputPath));
      manifest.releaseManifestSha256 = manifest.releaseManifestSha256;
      void desktopBytes;
    });

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /desktop entry .*Exec|desktop entry .*TryExec/u,
    );
  });
});

test("verifyAppImage rejects unexpected payload files outside the closed AppDir contract", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await updateFakeEnvelope(result.outputPath, async (envelope) => {
      envelope.files["usr/lib/unit-test-ide/extra.txt"] = {
        executable: false,
        contentBase64: Buffer.from("unexpected\n").toString("base64"),
      };
    });
    await refreshSidecarManifest(result.manifestPath, result.outputPath);

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /unexpected payload path/u,
    );
  });
});

test("verifyAppImage rejects a tampered license even when the sidecar digest is regenerated", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const result = await packageWithFakeTool(root);
    await updateFakeEnvelope(result.outputPath, async (envelope) => {
      envelope.files["usr/lib/unit-test-ide/licenses/NOTICE.txt"].contentBase64 = Buffer.from("tampered license\n").toString("base64");
    });
    await refreshSidecarManifest(result.manifestPath, result.outputPath);

    await assert.rejects(
      () => verifyAppImage({
        image: result.outputPath,
        manifest: result.manifestPath,
        requireDigest: true,
        extractor: result.extractor,
      }),
      /license .*sha256|license .*size/u,
    );
  });
});
