import { mkdir, open, writeFile } from "node:fs/promises";
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

function assertSafeJsonValue(value, ancestors = new WeakSet()) {
  if (value === null) return;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "value is not valid JSON");
    return;
  }
  if (typeof value === "string" || typeof value === "boolean") return;
  if (Array.isArray(value)) {
    if (ancestors.has(value)) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "value contains a cycle");
    ancestors.add(value);
    for (const item of value) assertSafeJsonValue(item, ancestors);
    ancestors.delete(value);
    return;
  }
  if (typeof value === "object") {
    if (ancestors.has(value)) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "value contains a cycle");
    ancestors.add(value);
    for (const [key, item] of Object.entries(value)) {
      if (typeof key !== "string") throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "value is not valid JSON");
      assertSafeJsonValue(item, ancestors);
    }
    ancestors.delete(value);
    return;
  }
  throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "value is not valid JSON");
}

export function encodeCanonicalJson(value) {
  if (value === null || Array.isArray(value) || typeof value !== "object") {
    throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", "top-level value must be an object");
  }
  assertSafeJsonValue(value);
  return `${JSON.stringify(canonicalValue(value), null, 2)}\n`;
}

export async function readCanonicalJson(path, { label, maxBytes }) {
  const chunks = [];
  let total = 0;
  const handle = await open(path, "r");
  try {
    while (total <= maxBytes) {
      const size = Math.min(65536, maxBytes + 1 - total);
      if (size <= 0) break;
      const chunk = Buffer.allocUnsafe(size);
      const { bytesRead } = await handle.read(chunk, 0, size, null);
      if (bytesRead === 0) break;
      chunks.push(chunk.subarray(0, bytesRead));
      total += bytesRead;
    }
  } finally {
    await handle.close();
  }
  if (total === 0 || total > maxBytes) throw phase9Failure("PHASE9_GATE_SCHEMA_INVALID", `${label} byte length is invalid`);
  const bytes = Buffer.concat(chunks, total);
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
