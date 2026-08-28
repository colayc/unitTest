import { lstat, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { dirname, join, parse, resolve } from "node:path";
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
    && Reflect.ownKeys(value).length === keys.length
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

async function realPathInfo(path, errorCode, message) {
  try {
    const absolutePath = resolve(path);
    const anchor = parse(absolutePath).root;
    const components = absolutePath.slice(anchor.length).split(/[\\/]+/u).filter(Boolean);
    let current = anchor;
    let info = await lstat(current);
    if (!info.isDirectory() || info.isSymbolicLink()) fail(errorCode, message);
    for (let index = 0; index < components.length; index += 1) {
      current = join(current, components[index]);
      info = await lstat(current);
      const leaf = index === components.length - 1;
      if (info.isSymbolicLink() || (!leaf && !info.isDirectory())) fail(errorCode, message);
    }
    return info;
  } catch (error) {
    if (error instanceof ReleaseProducerError) throw error;
    fail(errorCode, message);
  }
}

async function realFile(path, errorCode, message) {
  const info = await realPathInfo(path, errorCode, message);
  if (!info.isFile()) fail(errorCode, message);
  try {
    return await readFile(path, "utf8");
  } catch {
    fail(errorCode, message);
  }
}

async function realCheckoutFile(root, components, message) {
  let current = root;
  try {
    for (let index = 0; index < components.length; index += 1) {
      current = join(current, components[index]);
      const info = await lstat(current);
      const finalComponent = index === components.length - 1;
      if (info.isSymbolicLink() || (finalComponent ? !info.isFile() : !info.isDirectory())) fail(UNTRUSTED, message);
    }
    return await readFile(current, "utf8");
  } catch {
    fail(UNTRUSTED, message);
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

function tokenizeJavaScript(source) {
  const tokens = [];
  for (let index = 0; index < source.length;) {
    const character = source[index];
    const next = source[index + 1];
    if (/\s/u.test(character)) {
      index += 1;
    } else if (character === "/" && next === "/") {
      index = source.indexOf("\n", index + 2);
      if (index === -1) break;
    } else if (character === "/" && next === "*") {
      const end = source.indexOf("*/", index + 2);
      if (end === -1) fail(UNTRUSTED, "upstream Gulp targets are not trusted");
      index = end + 2;
    } else if (character === "'" || character === '"') {
      const quote = character;
      let value = "";
      index += 1;
      while (index < source.length && source[index] !== quote) {
        if (source[index] === "\\") index += 1;
        if (index >= source.length) fail(UNTRUSTED, "upstream Gulp targets are not trusted");
        value += source[index];
        index += 1;
      }
      if (source[index] !== quote) fail(UNTRUSTED, "upstream Gulp targets are not trusted");
      tokens.push({ type: "string", value });
      index += 1;
    } else if (character === "`") {
      let value = "";
      index += 1;
      while (index < source.length && source[index] !== "`") {
        if (source[index] === "\\") index += 1;
        if (index >= source.length) fail(UNTRUSTED, "upstream Gulp targets are not trusted");
        value += source[index];
        index += 1;
      }
      if (source[index] !== "`") fail(UNTRUSTED, "upstream Gulp targets are not trusted");
      tokens.push({ type: "template", value });
      index += 1;
    } else if (/[A-Za-z_$]/u.test(character)) {
      const start = index;
      index += 1;
      while (index < source.length && /[A-Za-z0-9_$]/u.test(source[index])) index += 1;
      tokens.push({ type: "identifier", value: source.slice(start, index) });
    } else if (character === "=" && next === ">") {
      tokens.push({ type: "operator", value: "=>" });
      index += 2;
    } else {
      tokens.push({ type: "punctuation", value: character });
      index += 1;
    }
  }
  return tokens;
}

function matchingIndex(tokens, start, opening, closing) {
  let depth = 0;
  for (let index = start; index < tokens.length; index += 1) {
    if (tokens[index].value === opening) depth += 1;
    else if (tokens[index].value === closing) {
      depth -= 1;
      if (depth === 0) return index;
    }
  }
  return -1;
}

function hasSequence(tokens, values) {
  return values.every((value, index) => tokens[index]?.value === value);
}

function buildTargetsContain(tokens, platform, arch) {
  for (let index = 0; index < tokens.length - 3; index += 1) {
    if (!hasSequence(tokens.slice(index), ["const", "BUILD_TARGETS", "=", "["])) continue;
    const end = matchingIndex(tokens, index + 3, "[", "]");
    if (end === -1) return false;
    for (let objectStart = index + 4; objectStart < end; objectStart += 1) {
      if (tokens[objectStart].value !== "{") continue;
      const objectEnd = matchingIndex(tokens, objectStart, "{", "}");
      if (objectEnd === -1 || objectEnd > end) return false;
      let matchedPlatform = false;
      let matchedArch = false;
      for (let field = objectStart + 1; field < objectEnd - 2; field += 1) {
        if (tokens[field].value === "platform" && tokens[field + 1].value === ":" && tokens[field + 2].type === "string") matchedPlatform = tokens[field + 2].value === platform;
        if (tokens[field].value === "arch" && tokens[field + 1].value === ":" && tokens[field + 2].type === "string") matchedArch = tokens[field + 2].value === arch;
      }
      if (matchedPlatform && matchedArch) return true;
      objectStart = objectEnd;
    }
  }
  return false;
}

function buildTargetLoopBody(tokens) {
  for (let index = 0; index < tokens.length - 5; index += 1) {
    if (!hasSequence(tokens.slice(index), ["BUILD_TARGETS", ".", "forEach", "("])) continue;
    let arrow = index + 4;
    while (arrow < tokens.length && tokens[arrow].value !== "=>") arrow += 1;
    if (arrow === tokens.length || tokens[arrow + 1]?.value !== "{") continue;
    const end = matchingIndex(tokens, arrow + 1, "{", "}");
    if (end !== -1) return tokens.slice(arrow + 2, end);
  }
  return [];
}

function bodyDefinesTrustedGulpRelationship(body) {
  const expectedOutputTemplate = "VSCode${dashed(platform)}${dashed(arch)}";
  const expectedTargetTemplate = "vscode${dashed(platform)}${dashed(arch)}${dashed(minified)}";
  let platformBinding = -1;
  let archBinding = -1;
  let outputDefinition = -1;
  let targetDefinition = -1;
  let outputPackaged = -1;
  for (let index = 0; index < body.length; index += 1) {
    if (hasSequence(body.slice(index), ["const", "platform", "=", "buildTarget", ".", "platform"])) platformBinding = index;
    if (hasSequence(body.slice(index), ["const", "arch", "=", "buildTarget", ".", "arch"])) archBinding = index;
    if (hasSequence(body.slice(index), ["const", "destinationFolderName", "="])
      && body[index + 3]?.type === "template" && body[index + 3].value === expectedOutputTemplate) outputDefinition = index;
    if (hasSequence(body.slice(index), ["const", "vscodeTask", "=", "task", ".", "define", "("])
      && body[index + 7]?.type === "template" && body[index + 7].value === expectedTargetTemplate) targetDefinition = index;
    if (hasSequence(body.slice(index), ["packageTask", "(", "platform", ",", "arch", ",", "sourceFolderName", ",", "destinationFolderName", ",", "opts", ")"])) outputPackaged = index;
  }
  const bindingsEnd = Math.max(platformBinding, archBinding);
  return platformBinding !== -1
    && archBinding !== -1
    && outputDefinition > bindingsEnd
    && targetDefinition > bindingsEnd
    && outputPackaged > bindingsEnd;
}

function validateGulpTargets(gulp) {
  const tokens = tokenizeJavaScript(gulp);
  if (!buildTargetsContain(tokens, "win32", "x64")
    || !buildTargetsContain(tokens, "linux", "x64")
    || !bodyDefinesTrustedGulpRelationship(buildTargetLoopBody(tokens))) {
    fail(UNTRUSTED, "upstream Gulp targets are not trusted");
  }
}

export async function verifyCodeOssCheckout({ root, actualCommit, manifest }) {
  validateSourceManifest(manifest);
  if (actualCommit !== EXPECTED_MANIFEST.codeOss.commit) fail(UNTRUSTED, "upstream commit is not trusted");
  if (typeof root !== "string" || root.length === 0) fail(UNTRUSTED, "upstream checkout is not trusted");
  const rootInfo = await realPathInfo(root, UNTRUSTED, "upstream checkout is not trusted");
  if (!rootInfo?.isDirectory() || rootInfo.isSymbolicLink()) fail(UNTRUSTED, "upstream checkout is not trusted");
  const [nodeVersion, packageText, yarnrc, gulp] = await Promise.all([
    realCheckoutFile(root, [".nvmrc"], "upstream Node version is not trusted"),
    realCheckoutFile(root, ["package.json"], "upstream package version is not trusted"),
    realCheckoutFile(root, [".yarnrc"], "upstream Electron target is not trusted"),
    realCheckoutFile(root, ["build", "gulpfile.vscode.js"], "upstream Gulp targets are not trusted"),
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
