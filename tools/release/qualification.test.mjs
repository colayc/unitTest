import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import test from "node:test";

import { qualifyRelease } from "./qualification.mjs";

const execFileAsync = promisify(execFile);
const sourceCommit = "a".repeat(40);
const baselineSourceCommit = "d".repeat(40);
const linuxPackageSha256 = "1".repeat(64);
const windowsPackageSha256 = "2".repeat(64);
const linuxManifestSha256 = "3".repeat(64);
const windowsManifestSha256 = "4".repeat(64);
const linuxBaselinePackageSha256 = "8".repeat(64);
const windowsBaselinePackageSha256 = "9".repeat(64);
const linuxBaselineManifestSha256 = "b".repeat(64);
const windowsBaselineManifestSha256 = "c".repeat(64);
const generatedAt = "2026-08-25T00:00:00.000Z";
const baselineGeneratedAt = "2026-08-24T00:00:00.000Z";

const lifecyclePass = Object.freeze({
  install: "pass",
  launchHandshake: "pass",
  upgrade: "pass",
  upgradeLaunch: "failed-as-expected",
  rollback: "pass",
  rollbackLaunch: "pass",
  repeatedRollback: "pass",
  uninstall: "pass",
  userDataPreserved: "pass",
  packageResidueAbsent: "pass",
});

function license(platform) {
  return {
    path: `licenses/${platform}-NOTICE.txt`,
    size: 17,
    sha256: platform === "linux" ? "5".repeat(64) : "6".repeat(64),
  };
}

function releaseManifest(platform) {
  return {
    schemaVersion: 1,
    product: "unit-test-ide",
    version: "1.2.3",
    platform,
    architecture: "x64",
    sourceCommit,
    artifacts: [{
      id: "desktop",
      kind: "executable",
      relativePath: platform === "windows"
        ? "app/code-oss-runtime/Code - OSS.exe"
        : "app/code-oss-runtime/code-oss",
      size: 23,
      sha256: "7".repeat(64),
      executable: true,
    }],
    licenses: [license(platform)],
    generatedAt,
  };
}

function baselineReleaseManifest(platform) {
  return {
    ...releaseManifest(platform),
    version: "1.2.2",
    sourceCommit: baselineSourceCommit,
    generatedAt: baselineGeneratedAt,
  };
}

function platformEvidence(platform) {
  return {
    schemaVersion: 1,
    product: "unit-test-ide",
    platform,
    architecture: "x64",
    sourceCommit,
    generatedAt,
    packageFilename: platform === "linux" ? "unit-test-ide-1.2.3.AppImage" : "unit-test-ide-1.2.3.msix",
    version: "1.2.3",
    packageSha256: platform === "linux" ? linuxPackageSha256 : windowsPackageSha256,
    manifestSha256: platform === "linux" ? linuxManifestSha256 : windowsManifestSha256,
    rollbackVersion: "1.2.2",
    rollbackPackageFilename: platform === "linux" ? "unit-test-ide-1.2.2.AppImage" : "unit-test-ide-1.2.2.msix",
    rollbackPackageSha256: platform === "linux" ? linuxBaselinePackageSha256 : windowsBaselinePackageSha256,
    rollbackManifestSha256: platform === "linux" ? linuxBaselineManifestSha256 : windowsBaselineManifestSha256,
    outcomes: { ...lifecyclePass },
  };
}

function licenseEvidence(platform) {
  return {
    schemaVersion: 1,
    product: "unit-test-ide",
    version: "1.2.3",
    platform,
    sourceCommit,
    licenses: [license(platform)],
    passed: true,
  };
}

function completeInput() {
  return {
    linuxEvidence: platformEvidence("linux"),
    windowsEvidence: platformEvidence("windows"),
    manifests: {
      linux: {
        releaseManifest: releaseManifest("linux"),
        packageFilename: "unit-test-ide-1.2.3.AppImage",
        packageSha256: linuxPackageSha256,
        manifestSha256: linuxManifestSha256,
        baselineReleaseManifest: baselineReleaseManifest("linux"),
        baselinePackageFilename: "unit-test-ide-1.2.2.AppImage",
        baselinePackageSha256: linuxBaselinePackageSha256,
        baselineManifestSha256: linuxBaselineManifestSha256,
      },
      windows: {
        releaseManifest: releaseManifest("windows"),
        packageFilename: "unit-test-ide-1.2.3.msix",
        packageSha256: windowsPackageSha256,
        manifestSha256: windowsManifestSha256,
        baselineReleaseManifest: baselineReleaseManifest("windows"),
        baselinePackageFilename: "unit-test-ide-1.2.2.msix",
        baselinePackageSha256: windowsBaselinePackageSha256,
        baselineManifestSha256: windowsBaselineManifestSha256,
      },
    },
    licenseAudit: {
      linux: licenseEvidence("linux"),
      windows: licenseEvidence("windows"),
    },
    signatures: {
      windows: { required: true, outcome: "verified" },
    },
  };
}

function clone(value) {
  return structuredClone(value);
}

function reasonMessages(result) {
  return result.report.qualificationOutcome.reasons;
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

async function writeJson(path, value) {
  const bytes = `${JSON.stringify(value, null, 2)}\n`;
  await writeFile(path, bytes);
  return bytes;
}

test("qualifyRelease accepts only a complete two-platform evidence set tied to one source commit", () => {
  const result = qualifyRelease(completeInput());

  assert.equal(result.qualified, true);
  assert.deepEqual(result.report, {
    schemaVersion: 1,
    sourceCommit,
    packageDigests: {
      linux: {
        packageSha256: linuxPackageSha256,
        manifestSha256: linuxManifestSha256,
        rollbackPackageSha256: linuxBaselinePackageSha256,
        rollbackManifestSha256: linuxBaselineManifestSha256,
      },
      windows: {
        packageSha256: windowsPackageSha256,
        manifestSha256: windowsManifestSha256,
        rollbackPackageSha256: windowsBaselinePackageSha256,
        rollbackManifestSha256: windowsBaselineManifestSha256,
      },
    },
    signatureOutcomes: { windows: "verified" },
    lifecycleOutcomes: {
      linux: { ...lifecyclePass },
      windows: { ...lifecyclePass },
    },
    licenseOutcome: { linux: "pass", windows: "pass" },
    qualificationOutcome: { qualified: true, reasons: [] },
  });
});

test("qualifyRelease accepts safe internal ASCII spaces in the Windows runtime launcher", () => {
  const input = completeInput();
  input.manifests.windows.releaseManifest.artifacts[0].relativePath = "app/code-oss-runtime/Code - OSS.exe";
  input.manifests.windows.baselineReleaseManifest.artifacts[0].relativePath = "app/code-oss-runtime/Code - OSS.exe";

  assert.equal(qualifyRelease(input).qualified, true);
});

test("qualifyRelease rejects unsafe portable release paths", () => {
  for (const relativePath of [
    "/app/code-oss",
    "C:/app/code-oss",
    "app\\code-oss",
    "app/../code-oss",
    "app/code<oss",
    "app/control\u0001.txt",
    "app/CON.txt",
    "app/com1.exe",
    "app/ leading.txt",
    "app/trailing ",
    "app/trailing.",
  ]) {
    const input = completeInput();
    input.manifests.windows.releaseManifest.artifacts[0].relativePath = relativePath;

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false, relativePath);
    assert.ok(
      reasonMessages(result).includes("windows release manifest artifacts are invalid"),
      `${relativePath}: ${reasonMessages(result).join("; ")}`,
    );
  }
});

test("qualifyRelease rejects each missing platform evidence record with an explicit reason", () => {
  for (const platform of ["linux", "windows"]) {
    const input = completeInput();
    input[`${platform}Evidence`] = undefined;

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false);
    assert.ok(reasonMessages(result).includes(`${platform} evidence is missing`));
  }
});

test("qualifyRelease rejects skipped install and rollback outcomes", () => {
  for (const [platform, outcome] of [["linux", "install"], ["windows", "rollback"]]) {
    const input = completeInput();
    input[`${platform}Evidence`].outcomes[outcome] = "skipped";

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false);
    assert.ok(reasonMessages(result).includes(`${platform} ${outcome} outcome must be pass`));
  }
});

test("qualifyRelease rejects an unsigned Windows package when signing is required", () => {
  const input = completeInput();
  input.signatures.windows.outcome = "unsigned";

  const result = qualifyRelease(input);

  assert.equal(result.qualified, false);
  assert.ok(reasonMessages(result).includes("required Windows package signature is not verified"));
});

test("qualifyRelease rejects package and manifest digest mismatches", () => {
  for (const [platform, field] of [["linux", "packageSha256"], ["windows", "manifestSha256"]]) {
    const input = completeInput();
    input.manifests[platform][field] = "f".repeat(64);

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false);
    assert.ok(reasonMessages(result).includes(`${platform} ${field} does not match install evidence`));
  }
});

test("qualifyRelease rejects a license audit that omits a manifest-listed notice", () => {
  const input = completeInput();
  input.licenseAudit.linux.licenses = [];

  const result = qualifyRelease(input);

  assert.equal(result.qualified, false);
  assert.ok(reasonMessages(result).includes("linux license audit does not match the release manifest"));
  assert.equal(result.report.licenseOutcome.linux, "fail");
});

test("qualifyRelease rejects unknown input records instead of ignoring unqualified evidence", () => {
  const input = completeInput();
  input.unreviewedEvidence = { passed: true };

  const result = qualifyRelease(input);

  assert.equal(result.qualified, false);
  assert.ok(reasonMessages(result).includes("release qualification input is not closed"));
});

test("qualifyRelease rejects evidence from different source commits", () => {
  const input = completeInput();
  input.windowsEvidence.sourceCommit = "b".repeat(40);

  const result = qualifyRelease(input);

  assert.equal(result.qualified, false);
  assert.ok(reasonMessages(result).includes("windows evidence source commit does not match the release"));
});

test("qualifyRelease rejects cross-platform release version drift", () => {
  const input = completeInput();
  input.manifests.windows.releaseManifest.version = "1.2.4";
  input.windowsEvidence.version = "1.2.4";
  input.licenseAudit.windows.version = "1.2.4";

  const result = qualifyRelease(input);

  assert.equal(result.qualified, false);
  assert.ok(reasonMessages(result).includes("windows release version does not match canonical version 1.2.3"));
});

test("qualifyRelease rejects empty manifest and audit license evidence on either platform", () => {
  for (const platform of ["linux", "windows"]) {
    const input = completeInput();
    input.manifests[platform].releaseManifest.licenses = [];
    input.licenseAudit[platform].licenses = [];

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false);
    assert.ok(reasonMessages(result).includes(`${platform} release manifest must contain at least one license notice`));
    assert.equal(result.report.licenseOutcome[platform], "fail");
  }
});

test("qualifyRelease rejects empty, malformed, and open artifact records", () => {
  const cases = [
    [],
    [{ ...releaseManifest("linux").artifacts[0], unexpected: true }],
    [{ ...releaseManifest("linux").artifacts[0], relativePath: "../outside", sha256: "invalid" }],
  ];
  for (const artifacts of cases) {
    const input = completeInput();
    input.manifests.linux.releaseManifest.artifacts = artifacts;

    const result = qualifyRelease(input);

    assert.equal(result.qualified, false);
    assert.ok(reasonMessages(result).includes("linux release manifest artifacts are invalid"));
  }
});

test("qualifyRelease applies the closed release-manifest schema to current and baseline manifests", () => {
  const cases = [
    ["unknown top-level field", (manifest) => { manifest.unreviewed = true; }],
    ["duplicate artifact id", (manifest) => { manifest.artifacts.push({ ...manifest.artifacts[0] }); }],
    ["duplicate artifact path", (manifest) => { manifest.artifacts.push({ ...manifest.artifacts[0], id: "alternate" }); }],
    ["invalid canonical generation time", (manifest) => { manifest.generatedAt = "2026-08-25T08:00:00+08:00"; }],
    ["invalid canonical version", (manifest) => { manifest.version = "01.2.3"; }],
  ];
  for (const [name, mutate] of cases) {
    for (const field of ["releaseManifest", "baselineReleaseManifest"]) {
      const input = completeInput();
      mutate(input.manifests.linux[field]);
      const result = qualifyRelease(input);
      assert.equal(result.qualified, false, `${name} in ${field}`);
      const expected = field === "releaseManifest"
        ? "linux release manifest schema/semantics are invalid"
        : "linux baseline release manifest schema/semantics are invalid";
      assert.ok(reasonMessages(result).includes(expected), `${name} in ${field}: ${reasonMessages(result).join("; ")}`);
    }
  }
});

test("qualifyRelease rejects evidence missing canonical identity and package binding fields", () => {
  for (const field of [
    "architecture",
    "generatedAt",
    "packageFilename",
    "packageSha256",
    "manifestSha256",
    "rollbackVersion",
    "rollbackPackageFilename",
    "rollbackPackageSha256",
    "rollbackManifestSha256",
  ]) {
    const input = completeInput();
    delete input.linuxEvidence[field];
    const result = qualifyRelease(input);
    assert.equal(result.qualified, false, field);
    assert.ok(reasonMessages(result).includes(`linux evidence ${field} is invalid or missing`), field);
  }
});

test("qualifyRelease binds an older rollback version to the supplied baseline package and manifest", () => {
  const cases = [
    ["not older", (input) => {
      input.linuxEvidence.rollbackVersion = "1.2.3";
      input.manifests.linux.baselineReleaseManifest.version = "1.2.3";
    }],
    ["package digest drift", (input) => { input.linuxEvidence.rollbackPackageSha256 = "d".repeat(64); }],
    ["manifest digest drift", (input) => { input.linuxEvidence.rollbackManifestSha256 = "e".repeat(64); }],
    ["filename drift", (input) => { input.linuxEvidence.rollbackPackageFilename = "other.AppImage"; }],
    ["missing baseline", (input) => { delete input.manifests.linux.baselineReleaseManifest; }],
  ];
  for (const [name, mutate] of cases) {
    const input = completeInput();
    mutate(input);
    const result = qualifyRelease(input);
    assert.equal(result.qualified, false, name);
    assert.ok(reasonMessages(result).includes(`linux baseline binding is invalid: ${name}`), name);
  }
});

test("qualification CLI hashes package inputs and emits only closed path-free release evidence", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "release-qualification-cli-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const input = completeInput();
  const paths = {};

  for (const platform of ["linux", "windows"]) {
    paths[`${platform}Package`] = join(root, platform === "linux" ? "release.AppImage" : "release.msix");
    paths[`${platform}BaselinePackage`] = join(root, platform === "linux" ? "baseline.AppImage" : "baseline.msix");
    paths[`${platform}Manifest`] = join(root, `${platform}-release-manifest.json`);
    paths[`${platform}BaselineManifest`] = join(root, `${platform}-baseline-release-manifest.json`);
    paths[`${platform}Evidence`] = join(root, `${platform}-install.json`);
    paths[`${platform}License`] = join(root, `${platform}-license.json`);
    const packageBytes = Buffer.from(`${platform} package bytes\n`);
    const baselinePackageBytes = Buffer.from(`${platform} baseline package bytes\n`);
    await writeFile(paths[`${platform}Package`], packageBytes);
    await writeFile(paths[`${platform}BaselinePackage`], baselinePackageBytes);
    const manifestBytes = await writeJson(paths[`${platform}Manifest`], input.manifests[platform].releaseManifest);
    const baselineManifestBytes = await writeJson(paths[`${platform}BaselineManifest`], input.manifests[platform].baselineReleaseManifest);
    input[`${platform}Evidence`].packageFilename = platform === "linux" ? "release.AppImage" : "release.msix";
    input[`${platform}Evidence`].packageSha256 = sha256(packageBytes);
    input[`${platform}Evidence`].manifestSha256 = sha256(manifestBytes);
    input[`${platform}Evidence`].rollbackPackageFilename = platform === "linux" ? "baseline.AppImage" : "baseline.msix";
    input[`${platform}Evidence`].rollbackPackageSha256 = sha256(baselinePackageBytes);
    input[`${platform}Evidence`].rollbackManifestSha256 = sha256(baselineManifestBytes);
    await writeJson(paths[`${platform}Evidence`], input[`${platform}Evidence`]);
    await writeJson(paths[`${platform}License`], input.licenseAudit[platform]);
  }
  const output = join(root, "release-qualification.json");

  await execFileAsync(process.execPath, [
    resolve("tools/release/qualification.mjs"),
    "--linux-evidence", paths.linuxEvidence,
    "--windows-evidence", paths.windowsEvidence,
    "--linux-manifest", paths.linuxManifest,
    "--windows-manifest", paths.windowsManifest,
    "--linux-package", paths.linuxPackage,
    "--windows-package", paths.windowsPackage,
    "--linux-baseline-manifest", paths.linuxBaselineManifest,
    "--windows-baseline-manifest", paths.windowsBaselineManifest,
    "--linux-baseline-package", paths.linuxBaselinePackage,
    "--windows-baseline-package", paths.windowsBaselinePackage,
    "--linux-license-audit", paths.linuxLicense,
    "--windows-license-audit", paths.windowsLicense,
    "--windows-signature-required", "1",
    "--windows-signature-outcome", "verified",
    "--out", output,
  ]);

  const report = JSON.parse(await readFile(output, "utf8"));
  assert.deepEqual(Object.keys(report), [
    "schemaVersion",
    "sourceCommit",
    "packageDigests",
    "signatureOutcomes",
    "lifecycleOutcomes",
    "licenseOutcome",
    "qualificationOutcome",
  ]);
  assert.equal(report.qualificationOutcome.qualified, true);
  assert.doesNotMatch(JSON.stringify(report), /release-qualification-cli-|[A-Z]:\\/u);
});

test("foundation release publication is downstream of a successful qualification gate", async () => {
  const workflow = await readFile(resolve(".github/workflows/foundation.yml"), "utf8");

  assert.match(workflow, /release-qualification:\r?\n[\s\S]*?needs:\r?\n\s+- install-smoke-windows\r?\n\s+- install-smoke-linux/u);
  assert.match(workflow, /node tools\/release\/qualification\.mjs[\s\S]*?release-qualification\.json/u);
  assert.match(workflow, /signature_required=\$env:RELEASE_SIGNING_REQUIRED/u);
  assert.match(workflow, /qualificationOutcome\.qualified[\s\S]*?actions\/upload-artifact@v7/u);
  assert.match(workflow, /id: canonical-release-version[\s\S]*?name: qualified-release-\$\{\{ steps\.canonical-release-version\.outputs\.version \}\}/u);
});

test("release package jobs materialize digest-pinned runtime inputs before packaging", async () => {
  const workflow = await readFile(resolve(".github/workflows/foundation.yml"), "utf8");
  assert.match(workflow, /permissions:\r?\n\s+actions: read\r?\n\s+contents: read/u);
  assert.doesNotMatch(workflow, /CODE_OSS_EXECUTABLE:\s*\$\{\{\s*vars\.CODE_OSS_EXECUTABLE/u);
  assert.doesNotMatch(workflow, /RELEASE_APPIMAGETOOL_PATH:\s*\$\{\{\s*vars\.RELEASE_APPIMAGETOOL_PATH/u);
  assert.match(workflow, /uses: actions\/download-artifact@[0-9a-f]{40}/u);

  for (const [jobName, nextJobName, packageStep, requiredDigests] of [
    ["package-windows", "package-linux", "Stage and package Windows MSIX", ["CODE_OSS_SHA256"]],
    ["package-linux", "install-smoke-windows", "Stage and package Linux AppImage", ["CODE_OSS_SHA256", "APPIMAGETOOL_SHA256"]],
  ]) {
    const start = workflow.indexOf(`  ${jobName}:`);
    const end = workflow.indexOf(`\n  ${nextJobName}:`, start + 3);
    const job = workflow.slice(start, end);
    const requireInputs = job.indexOf("Require release input coordinates");
    const download = job.search(/uses: actions\/download-artifact@[0-9a-f]{40}/u);
    const verifyDigest = job.indexOf("Verify and export release inputs");
    const packageIndex = job.indexOf(packageStep);
    assert.ok(0 <= requireInputs && requireInputs < download && download < verifyDigest && verifyDigest < packageIndex, jobName);
    assert.match(job, /RELEASE_INPUT_MISSING/u, jobName);
    assert.match(job, /CODE_OSS_RUNTIME_ROOT/u, `${jobName} runtime root`);
    assert.equal(job.match(/--code-oss-root/gu)?.length, 2, `${jobName} target and baseline runtime roots`);
    assert.equal(job.match(/--code-oss-sha256/gu)?.length, 2, `${jobName} target and baseline launcher digests`);
    assert.doesNotMatch(job, /--code-oss(?:\s|$)/u, `${jobName} removed single-file staging flag`);
    for (const digest of requiredDigests) {
      assert.match(job, new RegExp(`${digest}[\\s\\S]*(?:Get-FileHash|sha256sum)`, "u"), `${jobName} ${digest}`);
    }
    if (jobName === "package-windows") {
      assert.match(job, /\.release\/inputs\/windows-code-oss/u);
      assert.match(job, /Join-Path\s+\$runtimeRoot\s+'Code - OSS\.exe'/u);
    } else {
      assert.match(job, /\.release\/inputs\/linux-code-oss/u);
      assert.match(job, /launcher="\$runtime_root\/code-oss"/u);
      assert.match(job, /\[\[\s+-x\s+"\$launcher"\s+\]\]/u);
    }
  }
});
