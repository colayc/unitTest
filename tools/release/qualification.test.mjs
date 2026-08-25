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
const linuxPackageSha256 = "1".repeat(64);
const windowsPackageSha256 = "2".repeat(64);
const linuxManifestSha256 = "3".repeat(64);
const windowsManifestSha256 = "4".repeat(64);

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
      relativePath: "app/code-oss",
      size: 23,
      sha256: "7".repeat(64),
      executable: true,
    }],
    licenses: [license(platform)],
    generatedAt: "2026-08-25T00:00:00.000Z",
  };
}

function platformEvidence(platform) {
  return {
    schemaVersion: 1,
    product: "unit-test-ide",
    platform,
    sourceCommit,
    packageFilename: platform === "linux" ? "unit-test-ide-1.2.3.AppImage" : "unit-test-ide-1.2.3.msix",
    version: "1.2.3",
    packageSha256: platform === "linux" ? linuxPackageSha256 : windowsPackageSha256,
    manifestSha256: platform === "linux" ? linuxManifestSha256 : windowsManifestSha256,
    rollbackVersion: "1.2.2",
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
        packageSha256: linuxPackageSha256,
        manifestSha256: linuxManifestSha256,
      },
      windows: {
        releaseManifest: releaseManifest("windows"),
        packageSha256: windowsPackageSha256,
        manifestSha256: windowsManifestSha256,
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
      linux: { packageSha256: linuxPackageSha256, manifestSha256: linuxManifestSha256 },
      windows: { packageSha256: windowsPackageSha256, manifestSha256: windowsManifestSha256 },
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

test("qualification CLI hashes package inputs and emits only closed path-free release evidence", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "release-qualification-cli-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const input = completeInput();
  const paths = {};

  for (const platform of ["linux", "windows"]) {
    paths[`${platform}Package`] = join(root, platform === "linux" ? "release.AppImage" : "release.msix");
    paths[`${platform}Manifest`] = join(root, `${platform}-release-manifest.json`);
    paths[`${platform}Evidence`] = join(root, `${platform}-install.json`);
    paths[`${platform}License`] = join(root, `${platform}-license.json`);
    const packageBytes = Buffer.from(`${platform} package bytes\n`);
    await writeFile(paths[`${platform}Package`], packageBytes);
    const manifestBytes = await writeJson(paths[`${platform}Manifest`], input.manifests[platform].releaseManifest);
    input[`${platform}Evidence`].packageSha256 = sha256(packageBytes);
    input[`${platform}Evidence`].manifestSha256 = sha256(manifestBytes);
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
});
