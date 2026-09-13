import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname } from "node:path";

export class Phase9GateError extends Error {
  constructor(code, message, cause) {
    super(`${code}: ${message}`, cause ? { cause } : undefined);
    this.code = code;
  }
}

export function phase9Failure(code, message, cause) {
  return new Phase9GateError(code, message, cause);
}

function canonicalValue(value) {
  if (Array.isArray(value)) return value.map(canonicalValue);
  if (value !== null && typeof value === "object") {
    return Object.fromEntries(
      Object.keys(value)
        .sort((left, right) => left.localeCompare(right, "en"))
        .map((key) => [key, canonicalValue(value[key])]),
    );
  }
  return value;
}

export function encodeCanonicalJson(value) {
  return `${JSON.stringify(canonicalValue(value), null, 2)}\n`;
}

export async function readCanonicalJson(path, { label, maxBytes }) {
  const bytes = await readFile(path);
  if (bytes.length === 0 || bytes.length > maxBytes) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} byte length is invalid`);
  }
  let text;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not UTF-8`, error);
  }
  let value;
  try {
    value = JSON.parse(text);
  } catch (error) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not JSON`, error);
  }
  if (value === null || Array.isArray(value) || typeof value !== "object" || encodeCanonicalJson(value) !== text) {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} is not canonical JSON`);
  }
  return value;
}

export async function writeCanonicalJson(path, value) {
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, encodeCanonicalJson(value), { encoding: "utf8", flag: "w" });
}
