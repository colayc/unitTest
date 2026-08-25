import { lstat, readdir, utimes } from "node:fs/promises";
import { join } from "node:path";

function configurationFailure(message) {
  const error = new Error(`RELEASE_CONFIG_MISSING: ${message}`);
  error.code = "RELEASE_CONFIG_MISSING";
  return error;
}

export function resolveSourceDateEpoch(value = process.env.SOURCE_DATE_EPOCH) {
  const text = typeof value === "number" ? String(value) : value;
  if (typeof text !== "string" || !/^(?:0|[1-9]\d*)$/u.test(text)) {
    throw configurationFailure("SOURCE_DATE_EPOCH must be an explicit non-negative integer number of UTC seconds");
  }
  const seconds = Number(text);
  const milliseconds = seconds * 1000;
  if (!Number.isSafeInteger(seconds) || !Number.isFinite(milliseconds)) {
    throw configurationFailure("SOURCE_DATE_EPOCH is outside the supported range");
  }
  const date = new Date(milliseconds);
  if (Number.isNaN(date.valueOf())) throw configurationFailure("SOURCE_DATE_EPOCH is outside the supported range");
  let iso;
  try {
    iso = date.toISOString();
  } catch {
    throw configurationFailure("SOURCE_DATE_EPOCH is outside the supported range");
  }
  return Object.freeze({ seconds, iso });
}

export async function normalizePathTimestamp(path, epoch) {
  await utimes(path, epoch.seconds, epoch.seconds);
}

export async function normalizeTreeTimestamps(root, epoch) {
  const directories = [];
  async function walk(current) {
    const entries = await readdir(current, { withFileTypes: true });
    entries.sort((left, right) => left.name.localeCompare(right.name, "en"));
    for (const entry of entries) {
      const path = join(current, entry.name);
      const info = await lstat(path);
      if (info.isSymbolicLink()) throw configurationFailure(`refusing to normalize redirected package input: ${path}`);
      if (info.isDirectory()) {
        await walk(path);
        directories.push(path);
      } else if (info.isFile()) {
        await normalizePathTimestamp(path, epoch);
      } else {
        throw configurationFailure(`unsupported package input while normalizing timestamps: ${path}`);
      }
    }
  }
  await walk(root);
  for (const directory of directories) await normalizePathTimestamp(directory, epoch);
  await normalizePathTimestamp(root, epoch);
}
