import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, open, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

const packageScript = resolve("tools/release/windows/package-msix.ps1");
const verifyScript = resolve("tools/release/windows/verify-msix.ps1");
const entryPathScript = resolve("tools/release/windows/msix-entry-path.ps1");
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
  const product = "{\"applicationName\":\"code-oss\"}\n";
  const locale = "locale\n";
  const service = "service\n";
  const notice = "notice\n";
  await writeFixtureFile(stagingRoot, "app/code-oss-runtime/Code - OSS.exe", runtime);
  await writeFixtureFile(stagingRoot, "app/code-oss-runtime/resources/app/product.json", product);
  await writeFixtureFile(stagingRoot, "app/code-oss-runtime/locales/en-US.pak", locale);
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
        relativePath: "app/code-oss-runtime/Code - OSS.exe",
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
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$parent = Split-Path -Parent $package
if ($parent) {
  New-Item -ItemType Directory -Force -Path $parent | Out-Null
}
if (Test-Path -LiteralPath $package) {
  Remove-Item -LiteralPath $package -Force
}
$archive = [System.IO.Compression.ZipFile]::Open($package, [System.IO.Compression.ZipArchiveMode]::Create)
try {
  $rootPrefix = $directory.TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar) + [IO.Path]::DirectorySeparatorChar
  foreach ($source in Get-ChildItem -LiteralPath $directory -Recurse -File | Sort-Object FullName) {
    $relativePath = $source.FullName.Substring($rootPrefix.Length).Replace([IO.Path]::DirectorySeparatorChar.ToString(), '/')
    $entry = $archive.CreateEntry($relativePath)
    $input = [IO.File]::OpenRead($source.FullName)
    $output = $entry.Open()
    try {
      $input.CopyTo($output)
    } finally {
      $output.Dispose()
      $input.Dispose()
    }
  }
} finally {
  $archive.Dispose()
}
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

async function readZipEntryBytes(packagePath, entryName) {
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
  $buffer = [IO.MemoryStream]::new()
  $input = $entry.Open()
  try {
    $input.CopyTo($buffer)
  } finally {
    $input.Dispose()
  }
  [Console]::Write([Convert]::ToBase64String($buffer.ToArray()))
  $buffer.Dispose()
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
  assert.equal(result.status, 0, result.stderr);
  return Buffer.from(result.stdout, "base64");
}

async function readZipEntry(packagePath, entryName) {
  return (await readZipEntryBytes(packagePath, entryName)).toString("utf8");
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

async function deleteZipEntry(packagePath, entryName) {
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
  if ($null -eq $entry) {
    throw 'fixture archive entry is missing'
  }
  $entry.Delete()
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
  assert.equal(result.status, 0, result.stderr);
}

async function replaceZipEntryPath(packagePath, sourceName, replacementName) {
  const script = `
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::Open(${powerShellLiteral(packagePath)}, [System.IO.Compression.ZipArchiveMode]::Update)
try {
  $source = $null
  foreach ($candidate in $archive.Entries) {
    $normalizedName = $candidate.FullName -replace '\\\\', '/'
    if ($normalizedName -ceq ${powerShellLiteral(sourceName)}) {
      $source = $candidate
    }
    if ($normalizedName -ceq ${powerShellLiteral(replacementName)}) {
      throw 'fixture replacement entry already exists'
    }
  }
  if ($null -eq $source) {
    throw 'fixture source entry is missing'
  }

  $buffer = [IO.MemoryStream]::new()
  $input = $source.Open()
  try {
    $input.CopyTo($buffer)
  } finally {
    $input.Dispose()
  }
  $bytes = $buffer.ToArray()
  $buffer.Dispose()
  $source.Delete()

  $replacement = $archive.CreateEntry(${powerShellLiteral(replacementName)})
  $output = $replacement.Open()
  try {
    $output.Write($bytes, 0, $bytes.Length)
  } finally {
    $output.Dispose()
  }
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
  assert.equal(result.status, 0, result.stderr);
}

async function listZipEntryNames(packagePath) {
  const script = `
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [System.IO.Compression.ZipFile]::OpenRead(${powerShellLiteral(packagePath)})
try {
  $names = @($archive.Entries | ForEach-Object { $_.FullName -replace '\\\\', '/' })
  [Console]::Write((ConvertTo-Json -Compress -InputObject $names))
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
  assert.equal(result.status, 0, result.stderr);
  return JSON.parse(result.stdout);
}

function assertDoesNotContainHostPath(value, hostPath, label) {
  const normalizedValue = value.replaceAll("\\", "/").toLowerCase();
  const normalizedPath = hostPath.replaceAll("\\", "/").toLowerCase();
  assert.equal(normalizedValue.includes(normalizedPath), false, `${label} must remain path-free`);
}

async function setZipEntryFromFile(packagePath, entryName, sourcePath) {
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
  $input = [IO.File]::OpenRead(${powerShellLiteral(sourcePath)})
  $output = $replacement.Open()
  try {
    $input.CopyTo($output)
  } finally {
    $output.Dispose()
    $input.Dispose()
  }
} finally {
  $archive.Dispose()
}
`;
  const result = runPowerShellCommand(script);
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

test("package-windows consumes closed trusted coordinates before digest verification and staging", async () => {
  const workflow = (await readFile(workflowPath, "utf8")).replace(/\r\n?/gu, "\n");
  const start = workflow.indexOf("  package-windows:");
  const end = workflow.indexOf("\n  package-linux:", start);
  const job = workflow.slice(start, end);
  assert.ok(start >= 0 && end > start);
  assert.match(job, /^    needs:\n      - verify-windows\n      - verify-linux\n      - verify-release-input-run$/mu);
  assert.match(job, /^      RELEASE_INPUT_RUN_ID: \$\{\{ needs\.verify-release-input-run\.outputs\.run_id \}\}$/mu);
  assert.match(job, /^      RELEASE_INPUT_RUN_ATTEMPT: \$\{\{ needs\.verify-release-input-run\.outputs\.run_attempt \}\}$/mu);
  assert.match(job, /^      WINDOWS_ARTIFACT_ID: \$\{\{ needs\.verify-release-input-run\.outputs\.windows_artifact_id \}\}$/mu);
  assert.match(job, /^      WINDOWS_ARTIFACT_DIGEST: \$\{\{ needs\.verify-release-input-run\.outputs\.windows_artifact_digest \}\}$/mu);
  assert.match(job, /^      CODE_OSS_SHA256: \$\{\{ needs\.verify-release-input-run\.outputs\.windows_launcher_sha256 \}\}$/mu);
  assert.doesNotMatch(job, /(?:inputs\.(?:release_input_run_id|windows_code_oss_sha256)|vars\.(?:RELEASE_INPUT_RUN_ID|RELEASE_CODE_OSS_WINDOWS_SHA256))/u);
  const beforeAttempt = job.indexOf("name: Validate producer attempt before Windows artifact download");
  const download = job.indexOf("name: Download trusted Windows Code-OSS runtime");
  const afterAttempt = job.indexOf("name: Validate producer attempt after Windows artifact download");
  const digest = job.indexOf("Get-FileHash -LiteralPath $launcher -Algorithm SHA256");
  const exportRuntime = job.indexOf('"CODE_OSS_RUNTIME_ROOT=$runtimeRoot"');
  const staging = job.indexOf("name: Stage and package Windows MSIX");
  assert.ok(0 <= beforeAttempt && beforeAttempt < download && download < afterAttempt && afterAttempt < digest && digest < exportRuntime && exportRuntime < staging);
  assert.match(job, /artifact-ids: \$\{\{ needs\.verify-release-input-run\.outputs\.windows_artifact_id \}\}[\s\S]*?merge-multiple: true/u);
  assert.equal(job.match(/trusted-run\.mjs validate-attempt/gu)?.length, 2);
  assert.equal(job.match(/GH_TOKEN: \$\{\{ github\.token \}\}/gu)?.length, 2);
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
    assert.match(manifestXml, /Executable="app\\code-oss-runtime\\Code - OSS\.exe"/u);
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

windowsOnly("verify-msix reports missing required manifest entries with stable path-free errors", async (t) => {
  const cases = [
    ["AppxManifest.xml", "RELEASE_VERIFICATION_FAILED: package does not contain AppxManifest.xml"],
    ["release-manifest.json", "RELEASE_VERIFICATION_FAILED: package does not contain release-manifest.json"],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [entryName, expectedError] of cases) {
      const fixture = await packageWithFakeTools(join(root, entryName.replaceAll(".", "-")));
      await deleteZipEntry(fixture.outputPath, entryName);
      const result = runVerify([
        "-Package", fixture.outputPath,
        "-Manifest", fixture.manifestPath,
      ], {});

      assert.equal(result.status, 1, entryName);
      assert.equal(result.stdout.trim(), "", entryName);
      assert.equal(result.stderr.trim(), expectedError, entryName);
      assertDoesNotContainHostPath(result.stderr, fixture.outputPath, "MSIX error");
      assertDoesNotContainHostPath(result.stderr, root, "MSIX error");
      assertDoesNotContainHostPath(result.stderr, resolve("."), "MSIX error");
    }
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
    await addZipEntry(fixture.outputPath, "app\\code-oss-runtime\\Code - OSS.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /unsafe|duplicate|alias|backslash/u);
  });
});

windowsOnly("verify-msix rejects a case-aliased payload entry before payload-set comparison", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "APP/code-oss-runtime/Code - OSS.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /duplicate|alias|case/u);
  });
});

windowsOnly("verify-msix rejects a sole case-mismatched payload entry with unchanged bytes", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const canonicalPath = "app/code-oss-runtime/Code - OSS.exe";
    const wrongCasePath = "APP/code-oss-runtime/Code - OSS.exe";
    const before = await readZipEntryBytes(fixture.outputPath, canonicalPath);
    await replaceZipEntryPath(fixture.outputPath, canonicalPath, wrongCasePath);
    const after = await readZipEntryBytes(fixture.outputPath, wrongCasePath);
    const entryNames = await listZipEntryNames(fixture.outputPath);

    assert.deepEqual(after, before);
    assert.equal(after.byteLength, before.byteLength);
    assert.equal(sha256(after), sha256(before));
    assert.equal(entryNames.includes(canonicalPath), false);
    assert.equal(entryNames.includes(wrongCasePath), true);
    assert.equal(
      entryNames.filter((entryName) => entryName.toLowerCase() === canonicalPath.toLowerCase()).length,
      1,
    );

    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.equal(result.stdout.trim(), "");
    assert.equal(
      result.stderr.trim(),
      "RELEASE_VERIFICATION_FAILED: package payload does not match the expected staged payload set",
    );
    assertDoesNotContainHostPath(result.stderr, fixture.outputPath, "MSIX error");
    assertDoesNotContainHostPath(result.stderr, root, "MSIX error");
    assertDoesNotContainHostPath(result.stderr, resolve("."), "MSIX error");
  });
});

windowsOnly("verify-msix rejects a dot-segment payload entry before payload-set comparison", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "app/../app/code-oss-runtime/Code - OSS.exe", "aliased-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /\.\.|dot|unsafe/u);
  });
});

windowsOnly("MSIX entry canonicalization decodes only makeappx forms of portable punctuation", () => {
  const encoded = "app/%40scope/C%2B%2B%20Regular%20Expressions%20%28JavaScript%29.tmLanguage";
  const result = runPowerShellCommand(`
. ${powerShellLiteral(entryPathScript)}
$decoded = ConvertFrom-CanonicalMsixEntryPath -Path ${powerShellLiteral(encoded)}
[Console]::Write($decoded)
`);

  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout, "app/@scope/C++ Regular Expressions (JavaScript).tmLanguage");
});

windowsOnly("verify-msix rejects raw and makeappx-encoded aliases for portable punctuation", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await addZipEntry(fixture.outputPath, "AppxMetadata/@scope/Name+(One).txt", "raw");
    await addZipEntry(fixture.outputPath, "AppxMetadata/%40scope/Name%2B%28One%29.txt", "encoded");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /duplicate archive entry alias/u);
  });
});

windowsOnly("verify-msix rejects encoded archive entry identities before payload-set comparison", async (t) => {
  const maliciousEntries = [
    ["encoded metadata separator", "AppxMetadata%2Fevil"],
    ["encoded backslash", "app%5Ccode-oss-runtime%5Cevil"],
    ["encoded colon", "app%3Aevil"],
    ["encoded NUL", "app/control%00entry"],
    ["encoded DEL", "app/control%7Fentry"],
    ["double-encoded separator", "AppxMetadata%252Fevil"],
    ["encoded dot segment", "app/%2E%2E/evil"],
    ["malformed escape", "app/invalid%2"],
    ["non-canonical escape", "app/noncanonical%2fentry"],
    ["double-encoded portable punctuation", "AppxMetadata/%2540scope/name%252B%2528one%2529.txt"],
    ["decoded launcher alias", "app/code-oss-runtime/Code%20-%20OSS.exe"],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, entryName] of maliciousEntries) {
      const fixture = await packageWithFakeTools(join(root, name.replaceAll(" ", "-")));
      await addZipEntry(fixture.outputPath, entryName, "malicious");
      const result = runVerify([
        "-Package", fixture.outputPath,
        "-Manifest", fixture.manifestPath,
      ], {});

      assert.equal(result.status, 1, name);
      assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u, name);
      assert.match(result.stderr, /unsafe|non-canonical|duplicate|alias/u, name);
    }
  });
});

windowsOnly("verify-msix rejects unsafe Windows metadata entry components before filtering", async (t) => {
  const maliciousEntries = [
    ["leading space", "AppxMetadata/ leading.txt"],
    ["trailing space", "AppxMetadata/trailing.txt "],
    ["trailing dot", "AppxMetadata/trailing."],
    ["device basename extension", "AppxMetadata/CON.txt"],
    ["superscript COM1", "AppxMetadata/COM¹.txt"],
    ["superscript COM2", "AppxMetadata/COM².log"],
    ["superscript LPT3", "AppxMetadata/LPT³.bin"],
    ["console input device", "AppxMetadata/CONIN$.txt"],
    ["console output device", "AppxMetadata/CONOUT$.log"],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, entryName] of maliciousEntries) {
      const fixture = await packageWithFakeTools(join(root, name.replaceAll(" ", "-")));
      await addZipEntry(fixture.outputPath, entryName, "malicious");
      const result = runVerify([
        "-Package", fixture.outputPath,
        "-Manifest", fixture.manifestPath,
      ], {});

      assert.equal(result.status, 1, name);
      assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u, name);
      assert.match(result.stderr, /unsafe|device|space|dot/u, name);
    }
  });
});

windowsOnly("verify-msix rejects manifest payload aliases before case-insensitive expected-map insertion", async (t) => {
  const cases = [
    ["artifact/artifact alias with different digest", (manifest) => {
      manifest.artifacts.push({
        ...manifest.artifacts[0],
        id: "runtime-case-alias",
        relativePath: manifest.artifacts[0].relativePath.toUpperCase(),
        sha256: "b".repeat(64),
      });
      manifest.artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));
    }],
    ["artifact/license alias", (manifest) => {
      manifest.licenses.push({
        path: manifest.artifacts[0].relativePath.toUpperCase(),
        size: manifest.artifacts[0].size,
        sha256: "c".repeat(64),
      });
      manifest.licenses.sort((left, right) => left.path.localeCompare(right.path, "en"));
    }],
    ["reserved release manifest alias", (manifest) => {
      manifest.artifacts.push({
        ...manifest.artifacts[0],
        id: "reserved-manifest",
        relativePath: "Release-Manifest.json",
        sha256: "d".repeat(64),
      });
      manifest.artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));
    }],
  ];

  await withTemporaryRoot(t, async (root) => {
    for (const [name, mutate] of cases) {
      const fixture = await packageWithFakeTools(join(root, name.replaceAll("/", "-").replaceAll(" ", "-")));
      await rewriteReleaseManifest(fixture, mutate);
      const result = runVerify([
        "-Package", fixture.outputPath,
        "-Manifest", fixture.manifestPath,
      ], {});

      assert.equal(result.status, 1, name);
      assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u, name);
      assert.match(result.stderr, /duplicate or reserved release payload path/u, name);
    }
  });
});

windowsOnly("verify-msix rejects a tampered packaged Code-OSS launcher whose hash no longer matches the staged manifest", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await setZipEntry(fixture.outputPath, "app/code-oss-runtime/Code - OSS.exe", "tampered-runtime");
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /(?:size|hash) does not match/u);
  });
});

windowsOnly("verify-msix streams large runtime payload hashes and still rejects a digest mismatch", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await createStagingFixture(root);
    const relativePath = "app/code-oss-runtime/large-streaming-fixture.bin";
    const payloadPath = await writeFixtureFile(fixture.stagingRoot, relativePath, "");
    const payloadSize = 192 * 1024 * 1024;
    const handle = await open(payloadPath, "r+");
    try {
      await handle.truncate(payloadSize);
    } finally {
      await handle.close();
    }
    const digest = createHash("sha256");
    const zeroChunk = Buffer.alloc(1024 * 1024);
    for (let offset = 0; offset < payloadSize; offset += zeroChunk.length) digest.update(zeroChunk);

    const manifest = JSON.parse(await readFile(fixture.manifestPath, "utf8"));
    manifest.artifacts.push({
      executable: false,
      id: "large-streaming-fixture",
      kind: "runtime",
      relativePath,
      sha256: digest.digest("hex"),
      size: payloadSize,
    });
    manifest.artifacts.sort((left, right) => left.id.localeCompare(right.id, "en"));
    await writeFile(fixture.manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);

    const fakeMakeAppx = await createFakeMakeAppx(root);
    const packageResult = runPackage([
      "-StagingRoot", fixture.stagingRoot,
      "-Output", fixture.outputPath,
      "-Version", fixture.version,
      "-Publisher", "CN=Unit Test IDE",
    ], {
      RELEASE_MAKEAPPX_PATH: fakeMakeAppx,
      RELEASE_SIGNING_REQUIRED: "0",
    });
    assert.equal(packageResult.status, 0, packageResult.stderr);

    await rewriteReleaseManifest(fixture, (embeddedManifest) => {
      embeddedManifest.artifacts.find((artifact) => artifact.id === "large-streaming-fixture").sha256 = "f".repeat(64);
    });
    const mismatchResult = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});
    assert.equal(mismatchResult.status, 1);
    assert.match(mismatchResult.stderr, /artifact hash does not match the release manifest/u);
  });
});

windowsOnly("verify-msix rejects oversized embedded XML metadata before reading it", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    const oversizedManifest = join(root, "oversized-AppxManifest.xml");
    await writeFile(oversizedManifest, Buffer.alloc((1024 * 1024) + 1, 0x20));
    await setZipEntryFromFile(fixture.outputPath, "AppxManifest.xml", oversizedManifest);

    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});
    assert.equal(result.status, 1);
    assert.match(result.stderr, /embedded AppxManifest\.xml exceeds the package metadata size limit/u);
  });
});

windowsOnly("verify-msix requires the fixed Code-OSS launcher to remain a runtime executable while allowing the service executable", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await rewriteReleaseManifest(fixture, (manifest) => {
      manifest.artifacts[0].kind = "service";
    });
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /runtime.*launcher|launcher.*runtime/u);
  });
});

windowsOnly("verify-msix rejects a tampered packaged Code-OSS product metadata file", async (t) => {
  await withTemporaryRoot(t, async (root) => {
    const fixture = await packageWithFakeTools(root);
    await setZipEntry(
      fixture.outputPath,
      "app/code-oss-runtime/resources/app/product.json",
      "{\"applicationName\":\"tampered\"}\n",
    );
    const result = runVerify([
      "-Package", fixture.outputPath,
      "-Manifest", fixture.manifestPath,
    ], {});

    assert.equal(result.status, 1);
    assert.match(result.stderr, /RELEASE_VERIFICATION_FAILED/u);
    assert.match(result.stderr, /artifact .*hash does not match|artifact .*size does not match/u);
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
