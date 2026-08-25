import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

const packageScript = resolve("tools/release/windows/package-msix.ps1");
const verifyScript = resolve("tools/release/windows/verify-msix.ps1");
const workflowPath = resolve(".github/workflows/foundation.yml");
const sourceDateEpoch = "1787616000";

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function powerShellLiteral(value) {
  if (value === null || value === undefined) {
    return "$null";
  }
  return `'${String(value).replace(/'/g, "''")}'`;
}

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

async function createStagingFixture(root, version = "1.2.3") {
  const stagingRoot = join(root, "staging");
  const runtime = "runtime\n";
  const service = "service\n";
  const notice = "notice\n";
  await writeFixtureFile(stagingRoot, "app/code-oss.exe", runtime);
  await writeFixtureFile(stagingRoot, "service/unit-test-service.exe", service);
  await writeFixtureFile(stagingRoot, "licenses/NOTICE.txt", notice);
  const manifest = {
    schemaVersion: 1,
    product: "unit-test-ide",
    version,
    platform: "windows",
    architecture: "x64",
    sourceCommit: "a".repeat(40),
    artifacts: [
      {
        id: "runtime",
        kind: "runtime",
        relativePath: "app/code-oss.exe",
        size: Buffer.byteLength(runtime),
        sha256: sha256(runtime),
        executable: true,
      },
      {
        id: "service",
        kind: "service",
        relativePath: "service/unit-test-service.exe",
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
    outputPath: join(root, "dist", "unit-test-ide.msix"),
    stagingRoot,
    version,
  };
}

async function createFakeMakeAppx(root, mode = "success") {
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
  [Console]::Error.WriteLine('fake makeappx missing required arguments')
  exit 2
}
if (${JSON.stringify(mode)} -eq 'invalid-manifest') {
  [Console]::Error.WriteLine('error C00CE169: App manifest validation error: The appx manifest is invalid.')
  exit 11
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

async function createFakeSignTool(root, options = {}) {
  const toolPath = join(root, "fake-signtool.ps1");
  const expectedCertificatePath = options.expectedCertificatePath ?? null;
  const expectedPassword = options.expectedPassword ?? null;
  await writeFile(toolPath, `
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$Arguments)
Add-Type -AssemblyName System.IO.Compression.FileSystem
if ($Arguments.Length -eq 0) {
  [Console]::Error.WriteLine('fake signtool missing mode')
  exit 2
}
switch ($Arguments[0].ToLowerInvariant()) {
  'sign' {
    $expectedCertificatePath = ${powerShellLiteral(expectedCertificatePath)}
    $expectedPassword = ${powerShellLiteral(expectedPassword)}
    for ($index = 0; $index -lt $Arguments.Length; $index += 1) {
      if ($Arguments[$index] -eq '/f' -and $expectedCertificatePath -ne $null) {
        if ($Arguments[$index + 1] -ne $expectedCertificatePath) {
          [Console]::Error.WriteLine("certificate-path-mismatch: " + $Arguments[$index + 1])
          exit 3
        }
      }
      if ($Arguments[$index] -eq '/p' -and $expectedPassword -ne $null) {
        if ($Arguments[$index + 1] -ne $expectedPassword) {
          [Console]::Error.WriteLine("password-mismatch: " + $Arguments[$index + 1])
          exit 4
        }
      }
    }
    $package = $Arguments[-1]
    $archive = [System.IO.Compression.ZipFile]::Open($package, [System.IO.Compression.ZipArchiveMode]::Update)
    try {
      foreach ($name in @('AppxSignature.p7x', 'AppxMetadata/CodeIntegrity.cat')) {
        $existing = $archive.GetEntry($name)
        if ($null -ne $existing) {
          $existing.Delete()
        }
      }
      $signature = $archive.CreateEntry('AppxSignature.p7x')
      $writer = [IO.StreamWriter]::new($signature.Open())
      try {
        $writer.Write('signed-by-fake-signtool')
      } finally {
        $writer.Dispose()
      }
      $catalog = $archive.CreateEntry('AppxMetadata/CodeIntegrity.cat')
      $catalogWriter = [IO.StreamWriter]::new($catalog.Open())
      try {
        $catalogWriter.Write('fake-catalog')
      } finally {
        $catalogWriter.Dispose()
      }
    } finally {
      $archive.Dispose()
    }
    exit 0
  }
  'verify' {
    $package = $Arguments[-1]
    $archive = [System.IO.Compression.ZipFile]::OpenRead($package)
    try {
      $entry = $archive.GetEntry('AppxSignature.p7x')
      if ($null -eq $entry) {
        [Console]::Error.WriteLine('SignTool Error: missing signature')
        exit 1
      }
      $reader = [IO.StreamReader]::new($entry.Open())
      try {
        $content = $reader.ReadToEnd()
      } finally {
        $reader.Dispose()
      }
      if ($content -ne 'signed-by-fake-signtool') {
        [Console]::Error.WriteLine('SignTool Error: signature digest mismatch')
        exit 1
      }
    } finally {
      $archive.Dispose()
    }
    exit 0
  }
  default {
    [Console]::Error.WriteLine('fake signtool mode not supported')
    exit 2
  }
}
`.trimStart(), "utf8");
  return toolPath;
}

function runPowerShellFile(filePath, args, env) {
  return spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-File",
    filePath,
    ...args,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: {
      ...process.env,
      SOURCE_DATE_EPOCH: sourceDateEpoch,
      ...env,
    },
    windowsHide: true,
  });
}

async function rewriteReleaseManifest(fixture, mutate) {
  const manifest = JSON.parse(await readFile(fixture.manifestPath, "utf8"));
  mutate(manifest);
  const bytes = `${JSON.stringify(manifest, null, 2)}\n`;
  await writeFile(fixture.manifestPath, bytes);
  await setZipEntry(fixture.outputPath, "release-manifest.json", bytes);
}

function runPackage(args, env) {
  return runPowerShellFile(packageScript, args, env);
}

function runVerify(args, env) {
  return runPowerShellFile(verifyScript, args, env);
}

function runPowerShellCommand(command, env = {}) {
  return spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-Command",
    command,
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

async function readZipEntry(packagePath, entryName) {
  const script = `
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::OpenRead(${powerShellLiteral(packagePath)})
try {
  $entry = $null
  foreach ($candidate in $archive.Entries) {
    if (($candidate.FullName -replace '\\\\', '/') -eq ${powerShellLiteral(entryName)}) {
      $entry = $candidate
      break
    }
  }
  if ($null -eq $entry) {
    throw "missing entry: ${entryName}"
  }
  $reader = [IO.StreamReader]::new($entry.Open())
  try {
    [Console]::OutputEncoding = [Text.Encoding]::UTF8
    [Console]::Write($reader.ReadToEnd())
  } finally {
    $reader.Dispose()
  }
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
}

async function setZipEntry(packagePath, entryName, content) {
  const script = `
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::Open(${powerShellLiteral(packagePath)}, [System.IO.Compression.ZipArchiveMode]::Update)
try {
  $entry = $null
  foreach ($candidate in $archive.Entries) {
    if (($candidate.FullName -replace '\\\\', '/') -eq ${powerShellLiteral(entryName)}) {
      $entry = $candidate
      break
    }
  }
  if ($null -ne $entry) {
    $entry.Delete()
  }
  $replacement = $archive.CreateEntry(${powerShellLiteral(entryName)})
  $writer = [IO.StreamWriter]::new($replacement.Open())
  try {
    $writer.Write(${powerShellLiteral(content)})
  } finally {
    $writer.Dispose()
  }
} finally {
  $archive.Dispose()
}
`;
  const result = spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-Command",
    script,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr);
}

async function addZipEntry(packagePath, entryName, content) {
  const script = `
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::Open(${powerShellLiteral(packagePath)}, [System.IO.Compression.ZipArchiveMode]::Update)
try {
  $entry = $archive.CreateEntry(${powerShellLiteral(entryName)})
  $writer = [IO.StreamWriter]::new($entry.Open())
  try {
    $writer.Write(${powerShellLiteral(content)})
  } finally {
    $writer.Dispose()
  }
} finally {
  $archive.Dispose()
}
`;
  const result = spawnSync("powershell.exe", [
    "-NoProfile",
    "-ExecutionPolicy",
    "Bypass",
    "-Command",
    script,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    windowsHide: true,
  });
  assert.equal(result.status, 0, result.stderr);
}

function resolveRealMakeAppx() {
  const result = runPowerShellCommand(`
$command = Get-Command -Name 'makeappx.exe' -ErrorAction SilentlyContinue | Select-Object -First 1
if ($null -ne $command) {
  [Console]::Write($command.Source)
  exit 0
}
$kitsRoot = \${env:ProgramFiles(x86)}
if (-not [string]::IsNullOrWhiteSpace($kitsRoot)) {
  $candidateRoot = Join-Path $kitsRoot 'Windows Kits\\10\\bin'
  if (Test-Path -LiteralPath $candidateRoot) {
    $candidate = Get-ChildItem -LiteralPath $candidateRoot -Filter 'makeappx.exe' -Recurse -File -ErrorAction SilentlyContinue |
      Sort-Object -Property FullName -Descending |
      Select-Object -First 1
    if ($null -ne $candidate) {
      [Console]::Write($candidate.FullName)
      exit 0
    }
  }
}
exit 1
`);
  return result.status === 0 ? result.stdout.trim() : null;
}

async function packageWithFakeTools(root, version = "1.2.3", publisher = "CN=Unit Test IDE") {
  const fixture = await createStagingFixture(root, version);
  const fakeMakeAppx = await createFakeMakeAppx(root);
  const result = runPackage([
    "-StagingRoot", fixture.stagingRoot,
    "-Output", fixture.outputPath,
    "-Version", version,
    "-Publisher", publisher,
  ], {
    RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
    RELEASE_SIGNING_REQUIRED: "0",
    RELEASE_SIGNING_PFX_PATH: "",
    RELEASE_SIGNING_PFX_PASSWORD: "",
  });
  assert.equal(result.status, 0, result.stderr);
  return fixture;
}

const windowsOnly = process.platform === "win32" ? test : test.skip;

test("package-windows workflow passes RELEASE_VERSION through env instead of interpolating the manual input inside the script body", async () => {
  const workflow = await readFile(workflowPath, "utf8");
  assert.match(workflow, /RELEASE_VERSION:/u);
  assert.match(workflow, /\$env:RELEASE_VERSION/u);
  assert.doesNotMatch(workflow, /\$version = '\$\{\{ inputs\.release_version \}\}'/u);
});

windowsOnly("package-msix returns RELEASE_TOOL_MISSING when makeappx.exe is unavailable", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: join(root, "missing-makeappx.exe"),
      RELEASE_SIGNING_REQUIRED: "0",
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_TOOL_MISSING/u);
  });
});

windowsOnly("package-msix surfaces an existing makeappx invalid-manifest failure without misclassifying it as tool-missing", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeMakeAppx = await createFakeMakeAppx(root, "invalid-manifest");
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_PACKAGING_FAILED/u);
    assert.match(result.stderr, /App manifest validation error/u);
    assert.doesNotMatch(result.stderr, /RELEASE_TOOL_MISSING/u);
  });
});

windowsOnly("package-msix emits the required logo and dependency manifest elements and packages the placeholder logo", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const manifestXml = await readZipEntry(fixture.outputPath, "AppxManifest.xml");
    const logoBytes = await readZipEntry(fixture.outputPath, "Assets/StoreLogo.png");

    assert.match(manifestXml, /<Logo>Assets\/StoreLogo\.png<\/Logo>/u);
    assert.match(manifestXml, /<Dependencies>/u);
    assert.match(manifestXml, /TargetDeviceFamily/u);
    assert.ok(logoBytes.length > 0);
  });
});

windowsOnly("package-msix declares the staged Code-OSS executable as a runnable application entry point", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const manifestXml = await readZipEntry(fixture.outputPath, "AppxManifest.xml");

    assert.match(manifestXml, /<Applications>[\s\S]*<Application\b/u);
    assert.match(manifestXml, /Executable="app\\code-oss\.exe"/u);
    assert.match(manifestXml, /EntryPoint="Windows\.FullTrustApplication"/u);
  });
});

windowsOnly("verify-msix rejects a package whose AppxManifest has no application entry point", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const manifestXml = await readZipEntry(fixture.outputPath, "AppxManifest.xml");
    await setZipEntry(
      fixture.outputPath,
      "AppxManifest.xml",
      manifestXml.replace(/\s*<Applications>[\s\S]*?<\/Applications>/u, ""),
    );
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /application entry point|Applications/u);
  });
});

windowsOnly("package-msix fails closed when SOURCE_DATE_EPOCH is absent", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const fakeMakeAppx = await createFakeMakeAppx(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      SOURCE_DATE_EPOCH: "",
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /SOURCE_DATE_EPOCH/u);
  });
});

windowsOnly("package-msix XML-escapes the Publisher and normalizes semver-like prerelease versions for AppxManifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root, "1.2.3-beta+build.5", "CN=AT&T <Test>");
    const manifestXml = await readZipEntry(fixture.outputPath, "AppxManifest.xml");

    assert.match(manifestXml, /Publisher="CN=AT&amp;T &lt;Test&gt;"/u);
    assert.match(manifestXml, /Version="1\.2\.3\.0"/u);
  });
});

windowsOnly("package-msix accepts an unsigned development package only when RELEASE_SIGNING_REQUIRED=0", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const bytes = await readFile(fixture.outputPath);
    assert.ok(bytes.length > 0);
  });
});

windowsOnly("package-msix preserves spaced tool, certificate, output, and password arguments when signing is required", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const spacedRoot = join(root, "tool path with spaces");
    await mkdir(spacedRoot, { recursive: true });
    const fixture = await createStagingFixture(root);
    fixture.outputPath = join(root, "dist with spaces", "unit test ide.msix");
    const fakeMakeAppx = await createFakeMakeAppx(spacedRoot);
    const certificatePath = await writeFixtureFile(root, "certs with spaces/release signing cert.pfx", "fake certificate");
    const password = "space rich password value";
    const fakeSignTool = await createFakeSignTool(spacedRoot, {
      expectedCertificatePath: certificatePath,
      expectedPassword: password,
    });
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNTOOL_PATH: fakeSignTool,
      RELEASE_SIGNING_REQUIRED: "1",
      RELEASE_SIGNING_PFX_PATH: certificatePath,
      RELEASE_SIGNING_PFX_PASSWORD: password,
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
      "-Version", fixture.version,
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

windowsOnly("verify-msix rejects a forged signature entry when RequireSignature is set", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const fakeSignTool = await createFakeSignTool(root);
    await setZipEntry(fixture.outputPath, "AppxSignature.p7x", "forged-signature");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
      "-RequireSignature",
    ], {
      RELEASE_SIGNTOOL_PATH: fakeSignTool,
    });

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_SIGNATURE_INVALID/u);
  });
});

windowsOnly("verify-msix rejects a duplicate slash-aliased payload entry before payload-set comparison", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "app\\code-oss.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /duplicate|alias|backslash/u);
  });
});

windowsOnly("verify-msix rejects a case-aliased payload entry before payload-set comparison", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "APP/code-oss.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /duplicate|alias|case/u);
  });
});

windowsOnly("verify-msix rejects a dot-segment payload entry before payload-set comparison", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "app/../app/code-oss.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /\.\.|dot|unsafe/u);
  });
});

windowsOnly("verify-msix rejects a tampered packaged payload whose hash no longer matches the staged manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await setZipEntry(fixture.outputPath, "app/code-oss.exe", "tampered-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /(?:size|hash) does not match/u);
  });
});

windowsOnly("verify-msix rejects a tampered packaged license whose hash no longer matches the staged manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await setZipEntry(fixture.outputPath, "licenses/NOTICE.txt", "tampered-license");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /license .*hash does not match|license .*size does not match/u);
  });
});

windowsOnly("verify-msix rejects open and duplicate-path release manifests before payload verification", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate] of [
      ["open manifest", (manifest) => { manifest.unreviewed = true; }],
      ["empty artifact list", (manifest) => { manifest.artifacts = []; }],
      ["unsafe artifact size", (manifest) => { manifest.artifacts[0].size = Number.MAX_SAFE_INTEGER + 1; }],
      ["duplicate artifact path", (manifest) => {
        manifest.artifacts.push({ ...manifest.artifacts[0], id: "alternate-runtime" });
      }],
    ]) {
      const fixture = await packageWithFakeTools(join(root, name.replaceAll(" ", "-")));
      await rewriteReleaseManifest(fixture, mutate);
      const result = runVerify([
        "-Package", fixture.outputPath,
        "-Manifest", fixture.manifestPath,
      ], {});

      assert.equal(result.status, 1, name);
      assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u, name);
      assert.match(result.stderr, /release manifest.*(?:closed|schema|duplicate|invalid)/iu, name);
    }
  });
});

windowsOnly("package-msix succeeds with the real Windows SDK makeappx when it is available", async (t) => {
  const realMakeAppx = resolveRealMakeAppx();
  if (!realMakeAppx) {
    t.skip("makeappx.exe is unavailable in this environment");
    return;
  }

  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const result = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: realMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
    });

    assert.equal(result.status, 0, result.stderr);
    const bytes = await readFile(fixture.outputPath);
    assert.ok(bytes.length > 0);
  });
});
