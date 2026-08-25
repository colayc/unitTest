import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import test from "node:test";

import { packageAppImage } from "./package-appimage.mjs";
import { verifyAppImage } from "./verify-appimage.mjs";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

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
  const service = "service binary\n";
  const notice = "license notice\n";
  const cmake = "cmake bundle\n";
  const coverage = "coverage bundle\n";
  await writeFixtureFile(stagingRoot, "app/code-oss", runtime);
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
        id: "runtime",
        kind: "runtime",
        relativePath: "app/code-oss",
        size: Buffer.byteLength(runtime),
        sha256: sha256(runtime),
        executable: true,
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
    licenses: ["licenses/NOTICE.txt"],
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
      executable: (info.mode & 0o111) !== 0 || relativePath === "AppRun" || relativePath.endsWith("/app/code-oss"),
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

test("packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeTool = await createFakeAppImageTool(root);
    const result = await packageAppImage({
      stagingRoot: fixture.stagingRoot,
      output: fixture.outputPath,
      appimagetool: fakeTool.path,
      expectedDigest: fakeTool.sha256,
      version: fixture.version,
      architecture: "x64",
    });

    const desktop = await readFile(join(result.appDir, "unit-test-ide.desktop"), "utf8");
    assert.match(desktop, /^Exec=usr\/lib\/unit-test-ide\/app\/code-oss$/mu);

    const digestManifest = JSON.parse(await readFile(result.manifestPath, "utf8"));
    assert.equal(digestManifest.packageFile, basename(fixture.outputPath));
    assert.equal(digestManifest.launcher, "usr/lib/unit-test-ide/app/code-oss");
    assert.doesNotMatch(JSON.stringify(digestManifest), /https?:\/\//u);
    assert.ok(!JSON.stringify(digestManifest).includes(root.replaceAll("\\", "/")));
    assert.ok(!JSON.stringify(digestManifest).includes(resolve(root)));

    const verification = await verifyAppImage({
      image: fixture.outputPath,
      manifest: result.manifestPath,
      requireDigest: true,
    });
    assert.equal(verification.packageSha256, digestManifest.packageSha256);
    assert.equal(verification.launcher, "usr/lib/unit-test-ide/app/code-oss");
    assert.equal(verification.releaseManifestSha256, digestManifest.releaseManifestSha256);
  });
});
