import { createHash } from "node:crypto";
import { lstatSync } from "node:fs";
import { lstat, open } from "node:fs/promises";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import type { CoverageSourceSnapshotV14 } from "@unit-test-ide/test-client";

export type CoverageSourceSnapshot = CoverageSourceSnapshotV14;

export interface VerifiedCoverageSource {
  readonly path: string;
  readonly sha256: string;
}

export const MAX_COVERAGE_SOURCE_BYTES = 64 * 1024 * 1024;

export function resolveCoverageSourcePath(workspaceRoot: string, uri: string): string {
  if (!isAbsolute(workspaceRoot) || typeof uri !== "string" || uri.length === 0 || uri.includes("\0") || uri.includes("\\") || uri.startsWith("/")) {
    throw new Error("Invalid coverage source path.");
  }
  const segments = uri.split("/");
  if (segments.some((segment) => segment.length === 0)) throw new Error("Invalid coverage source path.");
  const decoded: string[] = [];
  for (const segment of segments) {
    let value: string;
    try {
      value = decodeURIComponent(segment);
    } catch {
      throw new Error("Invalid coverage source path.");
    }
    if (value.length === 0 || value === "." || value === ".." || value.includes("\0") || value.includes("/") || value.includes("\\") || /^[A-Za-z]:/.test(value)) {
      throw new Error("Invalid coverage source path.");
    }
    decoded.push(value);
  }
  const root = resolve(workspaceRoot);
  const candidate = resolve(root, ...decoded);
  const escaped = relative(root, candidate);
  if (escaped === "" || escaped === ".." || escaped.startsWith(`..${sep}`) || isAbsolute(escaped)) {
    throw new Error("Invalid coverage source path.");
  }
  return candidate;
}

export async function verifyCoverageSource(workspaceRoot: string, source: CoverageSourceSnapshot): Promise<VerifiedCoverageSource> {
  if (!/^[0-9a-f]{64}$/.test(source.sha256)) throw new Error("Invalid coverage source digest.");
  const path = resolveCoverageSourcePath(workspaceRoot, source.uri);
  assertNoSymlinkComponents(resolve(workspaceRoot), path);
  const before = await lstat(path).catch(() => { throw new Error("Coverage source is unavailable."); });
  if (!before.isFile()) throw new Error("Coverage source is not a regular file.");
  if (before.size > MAX_COVERAGE_SOURCE_BYTES) throw new Error("Coverage source exceeds the client size limit.");
  const handle = await open(path, "r").catch(() => { throw new Error("Coverage source is unavailable."); });
  try {
    const opened = await handle.stat();
    if (!opened.isFile() || !sameIdentity(before, opened)) throw new Error("Coverage source changed during verification.");
    const bytes = await handle.readFile();
    if (bytes.byteLength > MAX_COVERAGE_SOURCE_BYTES) throw new Error("Coverage source exceeds the client size limit.");
    const sha256 = createHash("sha256").update(bytes).digest("hex");
    const after = await lstat(path).catch(() => undefined);
    if (!after || !sameIdentity(opened, after)) throw new Error("Coverage source changed during verification.");
    if (sha256 !== source.sha256) throw new Error("Coverage source digest does not match the report.");
    return { path, sha256 };
  } finally {
    await handle.close();
  }
}

function sameIdentity(left: { dev: number; ino: number; size: number }, right: { dev: number; ino: number; size: number }): boolean {
  if (left.dev !== 0 || left.ino !== 0 || right.dev !== 0 || right.ino !== 0) {
    return left.dev === right.dev && left.ino === right.ino && left.size === right.size;
  }
  return left.size === right.size;
}

function assertNoSymlinkComponents(root: string, candidate: string): void {
  const escaped = relative(root, candidate);
  let current = root;
  for (const component of escaped.split(sep)) {
    current = join(current, component);
    try {
      if (lstatSync(current).isSymbolicLink()) throw new Error("Coverage source path contains a symbolic link.");
    } catch (error) {
      if (error instanceof Error && error.message.includes("symbolic link")) throw error;
      throw new Error("Coverage source is unavailable.");
    }
  }
}
