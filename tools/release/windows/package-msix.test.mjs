import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

const packageScript = resolve("tools/release/windows/package-msix.ps1");

async function withTemporaryRoot(t, run) {
  const root = await mkdtemp(join(tmpdir(), "release-msix-"));
  t.after(async () => {
    await rm(root, { recursive: true, force: true });
  });
  await run(root);
}

async function writeFixtureFile(root, relativePath, value) {
  const path = join(root, ...relativePath.split("/"));
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, value);
  return path;
}

async function createStagingFixture(root) {
  const stagingRoot = join(root, "staging");
  await writeFixtureFile(stagingRoot, "app/code-oss", "runtime\n");
  await writeFixtureFile(stagingRoot, "service/unit-test-service", "service\n");
  await writeFixtureFile(stagingRoot, "licenses/NOTICE.txt", "notice\n");
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version: "1.2.3",
    platform: "windows",
    architecture: "x64",
    sourceCommit: "a".repeat(40),
    artifacts: [
      {
        id: "runtime",
        kind: "runtime",
        relativePath: "app/code-oss",
        size: Buffer.byteLength("runtime\n"),
        sha256: "0".repeat(64),
        executable: true,
      },
      {
        id: "service",
        kind: "service",
        relativePath: "service/unit-test-service",
        size: Buffer.byteLength("service\n"),
        sha256: "1".repeat(64),
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
    outputPath: join(root, "dist", "unit-test-ide.msix"),
    stagingRoot,
  };
}

async function createFakeMakeAppx(root) {
  const toolPath = join(root, "fake-makeappx.ps1");
  await writeFile(toolPath, `
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
$directory = $null
$package = $null
for ($index = 0; $index -lt $Arguments.Length; $index += 1) {
  switch ($Arguments[$index]) {
    '/d' { $directory = $Arguments[$index + 1]; $index += 1 }
    '/p' { $package = $Arguments[$index + 1]; $index += 1 }
  }
}
if (-not $directory -or -not $package) {
  throw 'fake makeappx missing required arguments'
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$parent = Split-Path -Parent $package
if ($parent) {
  New-Item -ItemType Directory -Force -Path $parent | Out-Null
}
if (Test-Path -LiteralPath $package) {
  Remove-Item -LiteralPath $package -Force
}
[System.IO.Compression.ZipFile]::CreateFromDirectory($directory, $package)
`.trimStart(), "utf8");
  return toolPath;
}

function runPackage(args, env) {
  return spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    packageScript,
    ...args,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: {
      ...process.env,
      ...env,
    },
    windowsHide: true,
  });
}

const windowsOnly = process.platform === "win32" ? test : test.skip;

windowsOnly("package-msix returns RELEASE_TOOL_MISSING when makeappx.exe is unavailable", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", "1.2.3",
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: join(root, "missing-makeappx.exe"),
      RELEASE_SIGNING_REQUIRED: "0",
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_TOOL_MISSING/u);
  });
});

windowsOnly("package-msix accepts an unsigned development package only when RELEASE_SIGNING_REQUIRED=0", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeMakeAppx = await createFakeMakeAppx(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", "1.2.3",
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
      RELEASE_SIGNING_PFX_PATH: "",
      RELEASE_SIGNING_PFX_PASSWORD: "",
    });

    assert.equal(result.status, 0, result.stderr);
    const bytes = await readFile(fixture.outputPath);
    assert.ok(bytes.length > 0);
  });
});

windowsOnly("package-msix returns RELEASE_SIGNING_REQUIRED when release signing is enabled without a certificate", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeMakeAppx = await createFakeMakeAppx(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", "1.2.3",
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "1",
      RELEASE_SIGNING_PFX_PATH: "",
      RELEASE_SIGNING_PFX_PASSWORD: "",
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_SIGNING_REQUIRED/u);
  });
});
