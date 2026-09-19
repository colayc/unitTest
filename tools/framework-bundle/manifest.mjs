import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import { lstat, readFile, readdir } from "node:fs/promises";
import { dirname, join, posix, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const FRAMEWORK_DEPENDENCY_IDS = Object.freeze(["cpputest", "unity", "cmock"]);
export const ADAPTER_FRAMEWORK_IDS = Object.freeze(["cpputest", "unity"]);
const toolDirectory = dirname(fileURLToPath(import.meta.url));
const digest = /^[0-9a-f]{64}$/u;
const expectedManifest = {
  schemaVersion: 2,
  platforms: ["linux-x64", "windows-x64"],
  fixtureTools: {
    cmakeHelper: { path: "sdk/cmake/UnitTestIDE.cmake", sha256: "a9e0ff8bfc676131f4812b69a7f063cf1a79a7b8d52d64845631613e5877f00b" },
    unityRunnerGenerator: { name: "unity-runner-generator", schemaVersion: 1, version: "1.0.0", runnerProtocol: "utide.runner.v1" },
    cmockGenerator: { frameworkId: "cmock", version: "2.7.0", entrypoint: "lib/cmock.rb", containerImage: "docker.io/library/ruby", containerTag: "3.3.6-bookworm", containerPlatform: "linux/amd64", containerDigest: "sha256:7184e67a2927ea0749093abd199f38c1da5f371ab4bf7056b6fff50669031556", generatedAtRuntime: false }
  },
  frameworks: [
    { id: "cpputest", version: "4.0", tag: "v4.0", revision: "b9b841c56c524a10ccd40e88c3acaf9d5ec751c2", source: { filename: "cpputest-4.0.tar.gz", url: "https://github.com/cpputest/cpputest/releases/download/v4.0/cpputest-4.0.tar.gz", sha256: "21c692105db15299b5529af81a11a7ad80397f92c122bd7bf1e4a4b0e85654f7" }, license: { spdx: "BSD-3-Clause", path: "COPYING", sha256: "d8fe282e4047197e1fbd6ef2527bde832a1514be6bd82fac7d1296ce184285c8" }, sourceDirectory: "cpputest-4.0", treeSha256: "c564fb5e4e32836dc66f46efb86edb6f1f2fa6afa255a57052031aa00fc56f04" },
    { id: "unity", version: "2.6.1", tag: "v2.6.1", revision: "cbcd08fa7de711053a3deec6339ee89cad5d2697", source: { filename: "Unity-2.6.1.tar.gz", url: "https://github.com/ThrowTheSwitch/Unity/archive/refs/tags/v2.6.1.tar.gz", sha256: "b41a66d45a6b99758fb3202ace6178177014d52fc524bf1f72687d93e9867292" }, license: { spdx: "MIT", path: "LICENSE.txt", sha256: "907d9e859c6433703c0c183de3ddeaaf4baf3d517382f8f368b2c190fd2581d1" }, sourceDirectory: "Unity-2.6.1", treeSha256: "abfb7b2b7aec36739a7b138490d2e9dd178cc4f00e806ed372cbb8cfe98f73ae" },
    { id: "cmock", version: "2.7.0", tag: "v2.7.0", revision: "6ea503340b1d3fdc0f2bcaf69273ba0160ec83af", source: { filename: "CMock-2.7.0.tar.gz", url: "https://github.com/ThrowTheSwitch/CMock/archive/refs/tags/v2.7.0.tar.gz", sha256: "d96282cf0286682f7628afc31cf2e3ed6ecb66944d63e098824d98196904f04c" }, license: { spdx: "MIT", path: "LICENSE.txt", sha256: "f19bba29498b9405a86ab5fdc6bc58654fffb197603834e6d1423d583649b35c" }, sourceDirectory: "CMock-2.7.0", treeSha256: "19e013d70a3f032decb2e836b875a997d085ca72b34c92c91b528e6ad2e49ac3" }
  ]
};

export function frameworkFailure(code, message, cause) {
  const error = new Error(`${code}: ${message}`, cause === undefined ? undefined : { cause });
  error.code = code;
  return error;
}

export function portableRelativePath(value) {
  if (typeof value !== "string" || value.length === 0 || value.includes("\\") || /[\0\r\n]/u.test(value) || value.startsWith("/") || /^[A-Za-z]:/u.test(value)) return false;
  const normalized = posix.normalize(value);
  return normalized === value && !value.split("/").some((part) => part === "" || part === "." || part === "..");
}

export function validateFrameworkManifest(value) {
  if (!sameClosedValue(value, expectedManifest) || !hasSafePaths(value) || !hasLowercaseDigests(value)) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest does not match the closed schema v2 lock");
  return value;
}

export function parseFrameworkManifestBytes(bytes) {
  if (!Buffer.isBuffer(bytes) || bytes.length === 0 || bytes.length > 128 * 1024) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest byte length is invalid");
  let text;
  let value;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    rejectDuplicateJsonKeys(text);
    value = JSON.parse(text);
  } catch (error) {
    if (error?.code === "FRAMEWORK_MANIFEST_INVALID") throw error;
    throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest is not strict UTF-8 JSON", error);
  }
  validateFrameworkManifest(value);
  if (text !== `${JSON.stringify(value, null, 2)}\n`) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest is not canonical JSON");
  return value;
}

export async function readFrameworkManifest(path = join(toolDirectory, "manifest.json")) {
  const bytes = await readFile(path);
  return { manifest: parseFrameworkManifestBytes(bytes), bytes, manifestSha256: frameworkManifestSha256(bytes) };
}
export function frameworkManifestSha256(bytes) {
  if (!Buffer.isBuffer(bytes)) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest bytes must be a Buffer");
  return createHash("sha256").update(bytes).digest("hex");
}
export async function sha256File(path) {
  const hash = createHash("sha256");
  await new Promise((done, fail) => { const stream = createReadStream(path); stream.on("data", (chunk) => hash.update(chunk)); stream.on("end", done); stream.on("error", fail); });
  return hash.digest("hex");
}
export async function directoryDigest(root) {
  const hash = createHash("sha256");
  async function visit(directory, prefix) {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0);
    for (const entry of entries) {
      const path = join(directory, entry.name); const relativeName = prefix ? `${prefix}/${entry.name}` : entry.name;
      const metadata = await lstat(path);
      if (!portableRelativePath(relativeName) || metadata.isSymbolicLink() || (!metadata.isDirectory() && !metadata.isFile())) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "directory contains an unsafe entry");
      hash.update(`${metadata.isDirectory() ? "d" : "f"}:${relativeName}\0`);
      if (metadata.isDirectory()) await visit(path, relativeName); else hash.update(await readFile(path));
    }
  }
  await visit(resolve(root), ""); return hash.digest("hex");
}
function sameClosedValue(actual, expected) {
  if (typeof expected !== "object" || expected === null) return Object.is(actual, expected);
  if (!actual || typeof actual !== "object" || Array.isArray(actual) !== Array.isArray(expected)) return false;
  if (Array.isArray(expected)) return actual.length === expected.length && actual.every((item, index) => sameClosedValue(item, expected[index]));
  if (Object.getPrototypeOf(actual) !== Object.prototype) return false;
  const actualKeys = Object.keys(actual); const expectedKeys = Object.keys(expected);
  return actualKeys.length === expectedKeys.length && actualKeys.every((key, index) => key === expectedKeys[index] && sameClosedValue(actual[key], expected[key]));
}
function hasSafePaths(manifest) { return portableRelativePath(manifest.fixtureTools.cmakeHelper.path) && manifest.frameworks.every((framework) => portableRelativePath(framework.source.filename) && portableRelativePath(framework.license.path) && portableRelativePath(framework.sourceDirectory)); }
function hasLowercaseDigests(manifest) { const values = [manifest.fixtureTools.cmakeHelper.sha256, manifest.fixtureTools.cmockGenerator.containerDigest.slice("sha256:".length)]; for (const framework of manifest.frameworks) values.push(framework.source.sha256, framework.license.sha256, framework.treeSha256); return values.every((value) => digest.test(value)); }
function rejectDuplicateJsonKeys(text) {
  let index = 0; const whitespace = () => { while (/\s/u.test(text[index] ?? "")) index += 1; };
  const string = () => { if (text[index] !== '"') throw new SyntaxError("expected JSON string"); const start = index; index += 1; while (index < text.length) { const character = text[index++]; if (character === '"') return JSON.parse(text.slice(start, index)); if (character === "\\") index += 1; else if (character.charCodeAt(0) < 0x20) throw new SyntaxError("invalid JSON control character"); } throw new SyntaxError("unterminated JSON string"); };
  const primitive = () => { const match = /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/u.exec(text.slice(index)); if (!match) throw new SyntaxError("invalid JSON value"); index += match[0].length; };
  const value = () => { whitespace(); if (text[index] === '"') { string(); return; } if (text[index] === "{") { object(); return; } if (text[index] === "[") { array(); return; } primitive(); };
  const object = () => { index += 1; whitespace(); const keys = new Set(); if (text[index] === "}") { index += 1; return; } while (true) { whitespace(); const key = string(); if (keys.has(key)) throw frameworkFailure("FRAMEWORK_MANIFEST_INVALID", "manifest contains a duplicate JSON key"); keys.add(key); whitespace(); if (text[index++] !== ":") throw new SyntaxError("expected JSON colon"); value(); whitespace(); if (text[index] === "}") { index += 1; return; } if (text[index++] !== ",") throw new SyntaxError("expected JSON comma"); } };
  const array = () => { index += 1; whitespace(); if (text[index] === "]") { index += 1; return; } while (true) { value(); whitespace(); if (text[index] === "]") { index += 1; return; } if (text[index++] !== ",") throw new SyntaxError("expected JSON comma"); } };
  value(); whitespace(); if (index !== text.length) throw new SyntaxError("trailing JSON input");
}
