import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

import { isPortableReleasePath } from "./portable-path.mjs";

const toolDirectory = dirname(fileURLToPath(import.meta.url));
const schema = JSON.parse(await readFile(join(toolDirectory, "manifest.schema.json"), "utf8"));
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateSchema = ajv.compile(schema);

const canonicalSemverPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/u;

export function isCanonicalSemver(value) {
  const match = typeof value === "string" ? canonicalSemverPattern.exec(value) : null;
  if (!match) return false;
  const prerelease = match[4];
  return prerelease === undefined || prerelease.split(".").every((identifier) =>
    !/^\d+$/u.test(identifier) || identifier === "0" || !identifier.startsWith("0"));
}

function compareIdentifiers(left, right) {
  const leftNumeric = /^\d+$/u.test(left);
  const rightNumeric = /^\d+$/u.test(right);
  if (leftNumeric && rightNumeric) return BigInt(left) < BigInt(right) ? -1 : BigInt(left) > BigInt(right) ? 1 : 0;
  if (leftNumeric !== rightNumeric) return leftNumeric ? -1 : 1;
  return left < right ? -1 : left > right ? 1 : 0;
}

export function compareCanonicalVersions(left, right) {
  const leftMatch = canonicalSemverPattern.exec(left);
  const rightMatch = canonicalSemverPattern.exec(right);
  if (!leftMatch || !rightMatch || !isCanonicalSemver(left) || !isCanonicalSemver(right)) {
    throw new Error("release versions must be canonical semantic versions");
  }
  for (let index = 1; index <= 3; index += 1) {
    const leftPart = BigInt(leftMatch[index]);
    const rightPart = BigInt(rightMatch[index]);
    if (leftPart !== rightPart) return leftPart < rightPart ? -1 : 1;
  }
  const leftPre = leftMatch[4]?.split(".");
  const rightPre = rightMatch[4]?.split(".");
  if (!leftPre && !rightPre) return 0;
  if (!leftPre) return 1;
  if (!rightPre) return -1;
  for (let index = 0; index < Math.max(leftPre.length, rightPre.length); index += 1) {
    if (leftPre[index] === undefined) return -1;
    if (rightPre[index] === undefined) return 1;
    const compared = compareIdentifiers(leftPre[index], rightPre[index]);
    if (compared !== 0) return compared;
  }
  return 0;
}

function requireSortedUnique(records, key, label) {
  const values = records.map((record) => record[key]);
  if (new Set(values).size !== values.length) throw new Error(`duplicate ${label}`);
  const sorted = [...values].sort((left, right) => left.localeCompare(right, "en"));
  if (values.some((value, index) => value !== sorted[index])) throw new Error(`${label} must be sorted`);
}

function requireUnique(records, key, label) {
  const values = records.map((record) => record[key]);
  if (new Set(values).size !== values.length) throw new Error(`duplicate ${label}`);
}

function asciiCaseFold(value) {
  return value.replace(/[A-Z]/gu, (character) => character.toLowerCase());
}

function schemaError() {
  return validateSchema.errors
    ?.map(({ instancePath, message }) => `${instancePath || "/"} ${message}`)
    .join("; ") ?? "unknown schema failure";
}

export function validateReleaseManifestRecord(value, {
  architecture,
  platform,
  version,
} = {}) {
  if (!validateSchema(value)) throw new Error(`release manifest schema is invalid: ${schemaError()}`);
  if (value.artifacts.some(({ relativePath }) => !isPortableReleasePath(relativePath))) {
    throw new Error("release manifest artifact path is not portable");
  }
  if (value.licenses.some(({ path }) => !isPortableReleasePath(path))) {
    throw new Error("release manifest license path is not portable");
  }
  if (!isCanonicalSemver(value.version)) throw new Error("release manifest version is not canonical");
  if (!Number.isFinite(Date.parse(value.generatedAt)) || new Date(value.generatedAt).toISOString() !== value.generatedAt) {
    throw new Error("release manifest generatedAt is not canonical UTC ISO");
  }
  if (value.artifacts.length === 0) throw new Error("release manifest artifacts must not be empty");
  if (
    value.artifacts.some(({ size }) => !Number.isSafeInteger(size))
    || value.licenses.some(({ size }) => !Number.isSafeInteger(size))
  ) {
    throw new Error("release manifest sizes must be safe integers");
  }
  requireSortedUnique(value.artifacts, "id", "artifact id");
  requireUnique(value.artifacts, "relativePath", "artifact path");
  requireSortedUnique(value.licenses, "path", "license path");
  const payloadPaths = [
    ...value.artifacts.map(({ relativePath }) => relativePath),
    ...value.licenses.map(({ path }) => path),
  ];
  const foldedPayloadPaths = payloadPaths.map(asciiCaseFold);
  if (
    new Set(foldedPayloadPaths).size !== foldedPayloadPaths.length
    || foldedPayloadPaths.includes("release-manifest.json")
  ) {
    throw new Error("duplicate or reserved release payload path");
  }
  if (platform !== undefined && value.platform !== platform) throw new Error(`release manifest platform must be ${platform}`);
  if (architecture !== undefined && value.architecture !== architecture) {
    throw new Error(`release manifest architecture must be ${architecture}`);
  }
  if (version !== undefined && value.version !== version) throw new Error(`release manifest version must be ${version}`);
  return value;
}
