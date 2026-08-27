import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";

import { validateCodeOssRuntime } from "./code-oss-runtime.mjs";
import { stageRelease } from "./stage.mjs";

const rootEnvironmentName = "UNIT_TEST_IDE_REAL_CODE_OSS_ROOT";
const digestEnvironmentName = "UNIT_TEST_IDE_REAL_CODE_OSS_SHA256";
const digestPattern = /^[0-9a-f]{64}$/u;
const packageScript = resolve("tools/release/windows/package-msix.ps1");
const verifyScript = resolve("tools/release/windows/verify-msix.ps1");
const sourceDateEpoch = "1787616000";
const requiredRealRuntimePaths = [
  "resources/app/extensions/javascript/syntaxes/Regular Expressions (JavaScript).tmLanguage",
  "resources/app/node_modules.asar.unpacked/@parcel/watcher/build/Release/watcher.node",
  "resources/app/node_modules.asar.unpacked/@vscode/ripgrep/bin/rg.exe",
];
const observedNoticePaths = [
  "LICENSES.chromium.html",
  "resources/app/extensions/configuration-editing/dist/configurationEditingMain.js.LICENSE.txt",
  "resources/app/extensions/git/dist/main.js.LICENSE.txt",
  "resources/app/extensions/github/dist/extension.js.LICENSE.txt",
  "resources/app/extensions/latex/cpp-bailout-license.txt",
  "resources/app/extensions/latex/markdown-latex-combined-license.txt",
  "resources/app/extensions/markdown-language-features/dist/serverWorkerMain.js.LICENSE.txt",
  "resources/app/extensions/ms-vscode.js-debug-companion/LICENSE.txt",
  "resources/app/extensions/ms-vscode.js-debug-companion/ThirdPartyNotices.txt",
  "resources/app/extensions/ms-vscode.js-debug/LICENSE.txt",
  "resources/app/extensions/ms-vscode.js-debug/ThirdPartyNotices.txt",
  "resources/app/extensions/ms-vscode.vscode-js-profile-table/ThirdPartyNotices.txt",
  "resources/app/extensions/npm/dist/npmMain.js.LICENSE.txt",
  "resources/app/extensions/theme-seti/ThirdPartyNotices.txt",
  "resources/app/LICENSE.txt",
  "resources/app/ThirdPartyNotices.txt",
];

function resolveOptIn(environment) {
  const rootProvided = Object.hasOwn(environment, rootEnvironmentName);
  const digestProvided = Object.hasOwn(environment, digestEnvironmentName);
  if (!rootProvided && !digestProvided) return null;
  if (!rootProvided || !digestProvided) {
    throw new Error("real Code-OSS E2E requires both explicit environment inputs");
  }

  const root = environment[rootEnvironmentName];
  const launcherSha256 = environment[digestEnvironmentName];
  if (typeof root !== "string" || root.length === 0) {
    throw new Error("real Code-OSS E2E runtime root must be non-empty");
  }
  if (typeof launcherSha256 !== "string" || !digestPattern.test(launcherSha256)) {
    throw new Error("real Code-OSS E2E launcher digest must be a lowercase SHA-256");
  }
  return { launcherSha256, root };
}

function assertExpectedRealRuntimeFileCount(snapshot, label) {
  assert.equal(snapshot.length, 1006, `${label} must contain exactly 1006 regular files`);
}

async function writeFixtureFile(root, relativePath, value) {
  const filePath = join(root, ...relativePath.split("/"));
  await mkdir(dirname(filePath), { recursive: true });
  await writeFile(filePath, value);
  return filePath;
}

async function hashFile(path) {
  const hash = createHash("sha256");
  await new Promise((resolveHash, rejectHash) => {
    const stream = createReadStream(path);
    stream.on("data", (chunk) => hash.update(chunk));
    stream.on("error", rejectHash);
    stream.on("end", resolveHash);
  });
  return hash.digest("hex");
}

async function snapshotRegularFileTree(root) {
  const snapshot = [];

  async function walk(relativeRoot = "") {
    const currentRoot = relativeRoot ? join(root, ...relativeRoot.split("/")) : root;
    const entries = await readdir(currentRoot, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const relativePath = relativeRoot ? `${relativeRoot}/${entry.name}` : entry.name;
      const absolutePath = join(root, ...relativePath.split("/"));
      const info = await lstat(absolutePath);
      if (entry.isSymbolicLink() || info.isSymbolicLink()) {
        throw new Error(`runtime snapshot contains a link: ${relativePath}`);
      }
      if (info.isDirectory()) {
        await walk(relativePath);
      } else if (info.isFile()) {
        snapshot.push({
          relativePath,
          sha256: await hashFile(absolutePath),
          size: info.size,
        });
      } else {
        throw new Error(`runtime snapshot contains a special entry: ${relativePath}`);
      }
    }
  }

  await walk();
  snapshot.sort((left, right) => left.relativePath.localeCompare(right.relativePath, "en"));
  return snapshot;
}

async function createDisposableReleaseInputs(root) {
  const service = await writeFixtureFile(root, "inputs/service/unit-test-service.exe", "development service fixture\n");
  const extensionRoot = join(root, "inputs/extension");
  await writeFixtureFile(extensionRoot, "dist/src/extension.js", "export const e2e = true;\n");
  await writeFixtureFile(extensionRoot, "package.json", `${JSON.stringify({
    main: "./dist/src/extension.js",
    name: "unit-test-ide-e2e-extension",
    publisher: "unit-test-ide",
    version: "0.0.0-e2e",
  }, null, 2)}\n`);
  const cmakeRoot = join(root, "inputs/cmake");
  await writeFixtureFile(cmakeRoot, "bin/cmake.exe", "development cmake fixture\n");
  await writeFixtureFile(cmakeRoot, "licenses/LICENSE.txt", "development cmake license\n");
  await writeFixtureFile(cmakeRoot, "manifest.json", "{\"tool\":\"cmake\"}\n");
  const coverageRoot = join(root, "inputs/coverage");
  await writeFixtureFile(coverageRoot, "app/gcovr-runner.pyz", "development coverage fixture\n");
  await writeFixtureFile(coverageRoot, "licenses/NOTICE.txt", "development coverage notice\n");
  await writeFixtureFile(coverageRoot, "manifest.json", "{\"tool\":\"coverage\"}\n");
  return { cmakeRoot, coverageRoot, extensionRoot, service };
}

function runPowerShellFile(script, argumentsList, environment = {}) {
  const childEnvironment = { ...process.env };
  delete childEnvironment[rootEnvironmentName];
  delete childEnvironment[digestEnvironmentName];
  delete childEnvironment.RELEASE_MAKEAPPX_PATH;
  childEnvironment.Path = `${dirname(process.execPath)};${childEnvironment.Path ?? ""}`;
  Object.assign(childEnvironment, environment);
  return spawnSync("powershell.exe", [
    "-NoProfile",
    "-NonInteractive",
    "-ExecutionPolicy", "Bypass",
    "-File", script,
    ...argumentsList,
  ], {
    cwd: resolve("."),
    encoding: "utf8",
    env: childEnvironment,
    maxBuffer: 64 * 1024 * 1024,
    windowsHide: true,
  });
}

function processFailure(label, result) {
  const detail = [result.stderr, result.stdout]
    .filter((value) => typeof value === "string" && value.trim().length > 0)
    .join("\n")
    .trim();
  return `${label} failed with exit ${result.status ?? "unknown"}${detail ? `: ${detail}` : ""}`;
}

function redactRealRoot(value, realRoot) {
  const resolvedRoot = resolve(realRoot);
  const baseVariants = [
    realRoot,
    resolvedRoot,
    realRoot.replaceAll("\\", "/"),
    resolvedRoot.replaceAll("\\", "/"),
  ];
  const variants = new Set([
    ...baseVariants,
    ...baseVariants.map((variant) => variant.replaceAll("\\", "\\\\")),
  ]);
  let result = String(value);
  for (const variant of [...variants].sort((left, right) => right.length - left.length)) {
    if (variant.length === 0) continue;
    const escaped = variant.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
    result = result.replace(new RegExp(escaped, "giu"), "<real-code-oss-root>");
  }
  return result;
}

async function runRealRuntimeE2E(t, inputs) {
  const temporaryRoot = await mkdtemp(join(tmpdir(), "release-real-e2e-"));
  t.after(async () => {
    await rm(temporaryRoot, { recursive: true, force: true });
  });

  const sourceValidation = await validateCodeOssRuntime({
    expectedLauncherSha256: inputs.launcherSha256,
    platform: "windows",
    root: inputs.root,
  });
  assert.equal(sourceValidation.launcherRelativePath, "Code - OSS.exe");
  assert.equal(sourceValidation.launcherSha256, inputs.launcherSha256);
  assert.deepEqual(sourceValidation.productIdentity, {
    applicationName: "code-oss",
    licenseName: "MIT",
    nameShort: "Code - OSS",
  });

  const sourceSnapshot = await snapshotRegularFileTree(inputs.root);
  assertExpectedRealRuntimeFileCount(sourceSnapshot, "source Code-OSS runtime");
  const sourcePaths = new Set(sourceSnapshot.map(({ relativePath }) => relativePath));
  for (const requiredPath of requiredRealRuntimePaths) {
    assert.ok(sourcePaths.has(requiredPath), `real runtime is missing required portable path: ${requiredPath}`);
  }
  for (const noticePath of observedNoticePaths) {
    assert.ok(sourcePaths.has(noticePath), `real runtime is missing observed notice: ${noticePath}`);
  }

  const disposable = await createDisposableReleaseInputs(temporaryRoot);
  const version = "8.0.0-real-e2e";
  const result = await stageRelease({
    architecture: "x64",
    cmakeRoot: disposable.cmakeRoot,
    codeOssRoot: inputs.root,
    codeOssSha256: inputs.launcherSha256,
    coverageRoot: disposable.coverageRoot,
    extensionRoot: disposable.extensionRoot,
    outRoot: join(temporaryRoot, "out"),
    platform: "windows",
    repositoryRoot: resolve("."),
    service: disposable.service,
    sourceCommit: "a".repeat(40),
    sourceDateEpoch,
    version,
  });

  const stagedRuntimeRoot = join(result.stagingRoot, "app", "code-oss-runtime");
  const stagedValidation = await validateCodeOssRuntime({
    expectedLauncherSha256: inputs.launcherSha256,
    platform: "windows",
    root: stagedRuntimeRoot,
  });
  assert.equal(stagedValidation.launcherSha256, inputs.launcherSha256);
  assert.deepEqual(stagedValidation.productIdentity, sourceValidation.productIdentity);

  const stagedSnapshot = await snapshotRegularFileTree(stagedRuntimeRoot);
  assertExpectedRealRuntimeFileCount(stagedSnapshot, "staged Code-OSS runtime");
  assert.deepEqual(stagedSnapshot, sourceSnapshot, "staged runtime must preserve the exact source file set and bytes");

  const runtimeManifestSnapshot = result.manifest.artifacts
    .filter(({ relativePath }) => relativePath.startsWith("app/code-oss-runtime/"))
    .map(({ relativePath, sha256, size }) => ({
      relativePath: relativePath.slice("app/code-oss-runtime/".length),
      sha256,
      size,
    }))
    .sort((left, right) => left.relativePath.localeCompare(right.relativePath, "en"));
  assertExpectedRealRuntimeFileCount(runtimeManifestSnapshot, "runtime manifest artifact set");
  assert.deepEqual(
    runtimeManifestSnapshot,
    sourceSnapshot,
    "every staged runtime artifact must remain manifest-bound by size and digest",
  );

  const actualNoticeDestinations = result.manifest.licenses
    .map(({ path }) => path)
    .filter((path) => path.startsWith("licenses/code-oss/"))
    .sort((left, right) => left.localeCompare(right, "en"));
  const expectedNoticeDestinations = observedNoticePaths
    .map((path) => `licenses/code-oss/${path}`)
    .sort((left, right) => left.localeCompare(right, "en"));
  assert.deepEqual(
    actualNoticeDestinations,
    expectedNoticeDestinations,
    "all 16 observed Code-OSS notices must have exact manifest-bound destinations",
  );

  const output = join(temporaryRoot, "dist", "unit-test-ide-real-e2e.msix");
  const packageResult = runPowerShellFile(packageScript, [
    "-StagingRoot", result.stagingRoot,
    "-Output", output,
    "-Version", version,
    "-Publisher", "CN=Unit Test IDE",
  ], {
    RELEASE_SIGNING_PFX_PASSWORD: "",
    RELEASE_SIGNING_PFX_PATH: "",
    RELEASE_SIGNING_REQUIRED: "0",
    SOURCE_DATE_EPOCH: sourceDateEpoch,
  });
  if (packageResult.status !== 0) throw new Error(processFailure("real makeappx packaging", packageResult));
  assert.ok((await stat(output)).size > 0, "real makeappx must emit a non-empty MSIX");

  const verifyResult = runPowerShellFile(verifyScript, [
    "-Package", output,
    "-Manifest", result.manifestPath,
  ]);
  if (verifyResult.status !== 0) throw new Error(processFailure("real MSIX verification", verifyResult));
}

test("real Code-OSS file-count gate rejects incomplete and extra fixture trees", () => {
  for (const count of [1005, 1007]) {
    assert.throws(
      () => assertExpectedRealRuntimeFileCount(new Array(count), "fixture runtime"),
      /fixture runtime must contain exactly 1006 regular files/u,
    );
  }
  assert.doesNotThrow(() => assertExpectedRealRuntimeFileCount(new Array(1006), "fixture runtime"));
});

test("real Code-OSS E2E opt-in accepts only a complete explicit input pair", () => {
  assert.equal(resolveOptIn({}), null);
  assert.throws(
    () => resolveOptIn({ [rootEnvironmentName]: "runtime" }),
    /requires both explicit environment inputs/u,
  );
  assert.throws(
    () => resolveOptIn({ [digestEnvironmentName]: "a".repeat(64) }),
    /requires both explicit environment inputs/u,
  );
  assert.throws(
    () => resolveOptIn({ [rootEnvironmentName]: "runtime", [digestEnvironmentName]: "invalid" }),
    /lowercase SHA-256/u,
  );
  assert.deepEqual(
    resolveOptIn({ [rootEnvironmentName]: "runtime", [digestEnvironmentName]: "a".repeat(64) }),
    { root: "runtime", launcherSha256: "a".repeat(64) },
  );
  assert.equal(
    redactRealRoot(
      "failure at c:\\real\\runtime, C:/REAL/RUNTIME, and C:\\\\REAL\\\\RUNTIME",
      "C:\\Real\\Runtime",
    ),
    "failure at <real-code-oss-root>, <real-code-oss-root>, and <real-code-oss-root>",
  );
});

const optInRequested = Object.hasOwn(process.env, rootEnvironmentName)
  || Object.hasOwn(process.env, digestEnvironmentName);

test("real Code-OSS runtime stages and verifies as an unsigned development MSIX", {
  skip: optInRequested ? false : "real Code-OSS inputs are not configured",
}, async (t) => {
  const inputs = resolveOptIn(process.env);
  try {
    await runRealRuntimeE2E(t, inputs);
  } catch (error) {
    throw new Error(redactRealRoot(error?.message ?? error, inputs.root));
  }
});
