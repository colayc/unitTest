import { lstat, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const CONFIG_INVALID = "RELEASE_PRODUCER_CONFIG_INVALID";
const UNTRUSTED = "RELEASE_PRODUCER_UNTRUSTED";

const EXPECTED_MANIFEST = {
  schemaVersion: 1,
  codeOss: {
    repository: "https://github.com/microsoft/vscode.git",
    commit: "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    version: "1.92.0",
    nodeVersion: "20.14.0",
    yarnVersion: "1.22.22",
    windowsTarget: "vscode-win32-x64",
    windowsOutput: "VSCode-win32-x64",
    linuxTarget: "vscode-linux-x64",
    linuxOutput: "VSCode-linux-x64",
  },
  appimagetool: {
    repository: "AppImage/appimagetool",
    assetId: 324406882,
    assetName: "appimagetool-x86_64.AppImage",
    size: 15092216,
    sha256: "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0",
  },
};

const EXPECTED_INVOCATION = {
  repository: "colayc/unitTest",
  event: "workflow_dispatch",
  ref: "refs/heads/master",
  workflowRef: "colayc/unitTest/.github/workflows/release-inputs.yml@refs/heads/master",
};

const OUTPUT_FIELDS = [
  ["code_oss_repository", ["codeOss", "repository"]],
  ["code_oss_commit", ["codeOss", "commit"]],
  ["code_oss_version", ["codeOss", "version"]],
  ["code_oss_node_version", ["codeOss", "nodeVersion"]],
  ["code_oss_yarn_version", ["codeOss", "yarnVersion"]],
  ["code_oss_windows_target", ["codeOss", "windowsTarget"]],
  ["code_oss_windows_output", ["codeOss", "windowsOutput"]],
  ["code_oss_linux_target", ["codeOss", "linuxTarget"]],
  ["code_oss_linux_output", ["codeOss", "linuxOutput"]],
  ["appimagetool_repository", ["appimagetool", "repository"]],
  ["appimagetool_asset_id", ["appimagetool", "assetId"]],
  ["appimagetool_asset_name", ["appimagetool", "assetName"]],
  ["appimagetool_size", ["appimagetool", "size"]],
  ["appimagetool_sha256", ["appimagetool", "sha256"]],
];

class ReleaseProducerError extends Error {
  constructor(code, message) {
    super(`${code}: ${message}`);
    this.code = code;
  }
}

function fail(code, message) {
  throw new ReleaseProducerError(code, message);
}

function isPlainObject(value) {
  return value !== null && typeof value === "object" && Object.getPrototypeOf(value) === Object.prototype;
}

function hasExactKeys(value, keys) {
  return isPlainObject(value)
    && Object.keys(value).length === keys.length
    && keys.every((key) => Object.hasOwn(value, key));
}

function matchesExpected(value, expected) {
  if (!hasExactKeys(value, Object.keys(expected))) return false;
  return Object.entries(expected).every(([key, expectedValue]) => {
    const actual = value[key];
    if (isPlainObject(expectedValue)) return matchesExpected(actual, expectedValue);
    return actual === expectedValue;
  });
}

function frozenExpectedManifest() {
  return Object.freeze({
    schemaVersion: EXPECTED_MANIFEST.schemaVersion,
    codeOss: Object.freeze({ ...EXPECTED_MANIFEST.codeOss }),
    appimagetool: Object.freeze({ ...EXPECTED_MANIFEST.appimagetool }),
  });
}

export function validateSourceManifest(value) {
  if (!matchesExpected(value, EXPECTED_MANIFEST)) fail(CONFIG_INVALID, "source manifest is not the fixed contract");
  return frozenExpectedManifest();
}

async function realFile(path, errorCode, message) {
  try {
    const info = await lstat(path);
    if (!info.isFile() || info.isSymbolicLink()) fail(errorCode, message);
    return await readFile(path, "utf8");
  } catch (error) {
    if (error instanceof ReleaseProducerError) throw error;
    fail(errorCode, message);
  }
}

export async function loadSourceManifest(path) {
  const text = await realFile(path, CONFIG_INVALID, "source manifest is not a real JSON file");
  try {
    return validateSourceManifest(JSON.parse(text));
  } catch (error) {
    if (error instanceof ReleaseProducerError) throw error;
    fail(CONFIG_INVALID, "source manifest is not valid JSON");
  }
}

export function validateProducerInvocation(value) {
  if (!matchesExpected(value, EXPECTED_INVOCATION)) fail(UNTRUSTED, "producer invocation is not trusted");
  return Object.freeze({ ...EXPECTED_INVOCATION, provenancePath: ".github/workflows/release-inputs.yml" });
}

function validateElectronTarget(yarnrc) {
  const targetLines = yarnrc.replace(/\r\n?/gu, "\n").split("\n").filter((line) => /^\s*target(?:\s|$)/u.test(line));
  if (targetLines.length !== 1 || !/^\s*target\s+"30\.1\.2"\s*$/u.test(targetLines[0])) {
    fail(UNTRUSTED, "upstream Electron target is not trusted");
  }
}

function containsQuoted(text, value) {
  return new RegExp(`["']${value}["']`, "u").test(text);
}

function validateGulpTargets(gulp) {
  for (const value of [
    EXPECTED_MANIFEST.codeOss.windowsTarget,
    EXPECTED_MANIFEST.codeOss.windowsOutput,
    EXPECTED_MANIFEST.codeOss.linuxTarget,
    EXPECTED_MANIFEST.codeOss.linuxOutput,
  ]) {
    if (!containsQuoted(gulp, value)) fail(UNTRUSTED, "upstream Gulp targets are not trusted");
  }
}

export async function verifyCodeOssCheckout({ root, actualCommit, manifest }) {
  validateSourceManifest(manifest);
  if (actualCommit !== EXPECTED_MANIFEST.codeOss.commit) fail(UNTRUSTED, "upstream commit is not trusted");
  if (typeof root !== "string" || root.length === 0) fail(UNTRUSTED, "upstream checkout is not trusted");
  const rootInfo = await lstat(root).catch(() => null);
  if (!rootInfo?.isDirectory() || rootInfo.isSymbolicLink()) fail(UNTRUSTED, "upstream checkout is not trusted");
  const [nodeVersion, packageText, yarnrc, gulp] = await Promise.all([
    realFile(join(root, ".nvmrc"), UNTRUSTED, "upstream Node version is not trusted"),
    realFile(join(root, "package.json"), UNTRUSTED, "upstream package version is not trusted"),
    realFile(join(root, ".yarnrc"), UNTRUSTED, "upstream Electron target is not trusted"),
    realFile(join(root, "build", "gulpfile.vscode.js"), UNTRUSTED, "upstream Gulp targets are not trusted"),
  ]);
  if (nodeVersion.trimEnd() !== EXPECTED_MANIFEST.codeOss.nodeVersion) fail(UNTRUSTED, "upstream Node version is not trusted");
  let packageJson;
  try {
    packageJson = JSON.parse(packageText);
  } catch {
    fail(UNTRUSTED, "upstream package version is not trusted");
  }
  if (!isPlainObject(packageJson) || packageJson.version !== EXPECTED_MANIFEST.codeOss.version) fail(UNTRUSTED, "upstream package version is not trusted");
  validateElectronTarget(yarnrc);
  validateGulpTargets(gulp);
  return Object.freeze({ commit: EXPECTED_MANIFEST.codeOss.commit, version: EXPECTED_MANIFEST.codeOss.version });
}

function parseCommand(argv) {
  const [command, ...rest] = argv;
  const options = {};
  for (let index = 0; index < rest.length; index += 1) {
    const name = rest[index];
    const value = rest[index + 1];
    if (!name.startsWith("--") || value === undefined || Object.hasOwn(options, name)) fail(CONFIG_INVALID, "command arguments are invalid");
    options[name] = value;
    index += 1;
  }
  return { command, options };
}

function requireOnlyOptions(options, names) {
  if (!hasExactKeys(options, names)) fail(CONFIG_INVALID, "command arguments are invalid");
}

function outputBytes(manifest) {
  const lines = OUTPUT_FIELDS.map(([name, [section, field]]) => {
    const value = String(manifest[section][field]);
    if (!/^[A-Za-z0-9._:/-]+$/u.test(value)) fail(CONFIG_INVALID, "GitHub output value is invalid");
    return `${name}=${value}`;
  });
  return `${lines.join("\n")}\n`;
}

async function writeOutputAtomically(path, bytes) {
  const temporaryPath = `${path}.tmp-${process.pid}-${Date.now()}`;
  try {
    await mkdir(dirname(path), { recursive: true });
    await writeFile(temporaryPath, bytes, { encoding: "utf8", flag: "wx" });
    await rename(temporaryPath, path);
  } catch {
    await rm(temporaryPath, { force: true }).catch(() => {});
    fail(CONFIG_INVALID, "GitHub output could not be written");
  }
}

async function main(argv) {
  const { command, options } = parseCommand(argv);
  if (command === "export") {
    requireOnlyOptions(options, ["--manifest", "--github-output"]);
    const manifest = await loadSourceManifest(options["--manifest"]);
    await writeOutputAtomically(options["--github-output"], outputBytes(manifest));
    return;
  }
  if (command === "authorize") {
    requireOnlyOptions(options, ["--repository", "--event", "--ref", "--workflow-ref"]);
    const result = validateProducerInvocation({
      repository: options["--repository"],
      event: options["--event"],
      ref: options["--ref"],
      workflowRef: options["--workflow-ref"],
    });
    process.stdout.write(`${JSON.stringify({ provenancePath: result.provenancePath })}\n`);
    return;
  }
  if (command === "verify-checkout") {
    requireOnlyOptions(options, ["--manifest", "--root", "--actual-commit"]);
    const manifest = await loadSourceManifest(options["--manifest"]);
    const result = await verifyCodeOssCheckout({ root: options["--root"], actualCommit: options["--actual-commit"], manifest });
    process.stdout.write(`${JSON.stringify(result)}\n`);
    return;
  }
  fail(CONFIG_INVALID, "command is invalid");
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((error) => {
    const message = error instanceof ReleaseProducerError
      ? error.message
      : `${CONFIG_INVALID}: operation rejected`;
    process.stderr.write(`${message}\n`);
    process.exitCode = 1;
  });
}
