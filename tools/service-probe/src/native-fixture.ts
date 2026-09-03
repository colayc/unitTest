import { randomBytes } from "node:crypto";
import {
  lstat,
  mkdir,
  open,
  readFile,
  readdir,
  realpath,
  rm,
  writeFile,
} from "node:fs/promises";
import { isAbsolute, join, posix, relative, resolve, win32 } from "node:path";
import type { Diagnostic } from "@unit-test-ide/protocol-models";

export type NativeFixtureName =
  | "preset-project"
  | "fallback-project"
  | "compiler-failure"
  | "linker-failure"
  | "configure-failure";

export interface GoldenDiagnosticExpectation {
  kind: "configure" | "compiler" | "linker";
  severity: "warning" | "error";
  file?: string;
  line?: number;
  codePattern?: string;
  messageContains: string;
}

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const fixtureRoot = join(repositoryRoot, "testdata", "toolchains");
const fixtureLocations: Readonly<Record<NativeFixtureName, readonly string[]>> = {
  "preset-project": ["preset-project"],
  "fallback-project": ["fallback-project"],
  "compiler-failure": ["failures", "compiler"],
  "linker-failure": ["failures", "linker"],
  "configure-failure": ["failures", "configure"],
};

export async function copyNativeFixture(
  fixture: NativeFixtureName,
  destinationParent: string,
  directoryName?: string,
): Promise<string> {
  const location = fixtureLocations[fixture];
  if (location === undefined) {
    throw new Error(`unknown native fixture: ${String(fixture)}`);
  }
  if (!isAbsolute(destinationParent) || destinationParent.includes("\0")) {
    throw new Error("fixture destination parent must be an absolute path");
  }
  await requirePlainDirectory(destinationParent, "fixture destination parent");
  const name = directoryName ?? `${fixture}-${randomBytes(8).toString("hex")}`;
  if (!portableDirectoryName(name)) {
    throw new Error("invalid fixture destination name");
  }
  const destination = join(destinationParent, name);
  const source = join(fixtureRoot, ...location);
  if (!insideRoot(fixtureRoot, source)) {
    throw new Error("unknown native fixture");
  }
  await copyFixtureTree(source, destination);
  return destination;
}

async function copyFixtureTree(source: string, destination: string): Promise<void> {
  await requirePlainDirectory(source, "fixture source");
  try {
    await mkdir(destination, { mode: 0o700 });
  } catch (error: unknown) {
    if ((error as NodeJS.ErrnoException).code === "EEXIST") {
      throw new Error("fixture destination must be a new directory", { cause: error });
    }
    throw error;
  }
  try {
    await copyDirectoryContents(source, destination);
  } catch (error) {
    await rm(destination, { recursive: true, force: true });
    throw error;
  }
}

async function copyDirectoryContents(source: string, destination: string): Promise<void> {
  const before = await lstat(source);
  if (!before.isDirectory() || before.isSymbolicLink()) {
    throw new Error(`unsafe fixture entry: ${source}`);
  }
  const names = await readdir(source);
  names.sort(compareByCodePoint);
  for (const name of names) {
    if (!portableDirectoryName(name)) {
      throw new Error(`unsafe fixture entry: ${name}`);
    }
    const sourcePath = join(source, name);
    const destinationPath = join(destination, name);
    const info = await lstat(sourcePath);
    if (info.isSymbolicLink()) {
      throw new Error(`unsafe fixture entry: ${name}`);
    }
    if (info.isDirectory()) {
      await mkdir(destinationPath, { mode: 0o700 });
      await copyDirectoryContents(sourcePath, destinationPath);
    } else if (info.isFile()) {
      await copyRegularFile(sourcePath, destinationPath, info);
    } else {
      throw new Error(`unsafe fixture entry: ${name}`);
    }
  }
  const after = await lstat(source);
  if (!sameIdentity(before, after)) {
    throw new Error(`unsafe fixture entry changed during copy: ${source}`);
  }
}

async function copyRegularFile(
  source: string,
  destination: string,
  expected: Awaited<ReturnType<typeof lstat>>,
): Promise<void> {
  const handle = await open(source, "r");
  let data: Buffer;
  try {
    const opened = await handle.stat();
    if (!opened.isFile() || !sameIdentity(expected, opened)) {
      throw new Error(`unsafe fixture entry changed during copy: ${source}`);
    }
    data = await handle.readFile();
    const after = await handle.stat();
    if (!sameIdentity(opened, after)) {
      throw new Error(`unsafe fixture entry changed during copy: ${source}`);
    }
  } finally {
    await handle.close();
  }
  await writeFile(destination, data, { flag: "wx", mode: 0o600 });
}

async function requirePlainDirectory(path: string, label: string): Promise<void> {
  let info;
  try {
    info = await lstat(path);
  } catch (error) {
    throw new Error(`${label} is unavailable`, { cause: error });
  }
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error(`${label} is unsafe`);
  }
  const canonical = await realpath(path);
  if (!isAbsolute(canonical)) {
    throw new Error(`${label} is unsafe`);
  }
}

function sameIdentity(
  left: Awaited<ReturnType<typeof lstat>>,
  right: Awaited<ReturnType<typeof lstat>>,
): boolean {
  return (
    left.dev === right.dev &&
    left.ino === right.ino &&
    left.size === right.size &&
    left.mtimeMs === right.mtimeMs &&
    left.ctimeMs === right.ctimeMs
  );
}

function insideRoot(root: string, candidate: string): boolean {
  const path = relative(resolve(root), resolve(candidate));
  return path === "" || (!path.startsWith("..") && !isAbsolute(path));
}

function portableDirectoryName(name: string): boolean {
  if (
    name.length === 0 ||
    name === "." ||
    name === ".." ||
    name.endsWith(".") ||
    name.endsWith(" ") ||
    /[<>:"/\\|?*\u0000-\u001f\u007f]/u.test(name)
  ) {
    return false;
  }
  const base = name.split(".", 1)[0]?.toUpperCase();
  return !(
    base === undefined ||
    ["CON", "PRN", "AUX", "NUL"].includes(base) ||
    /^(COM|LPT)[1-9]$/u.test(base)
  );
}

function compareByCodePoint(left: string, right: string): number {
  const leftPoints = Array.from(left, (value) => value.codePointAt(0) ?? 0);
  const rightPoints = Array.from(right, (value) => value.codePointAt(0) ?? 0);
  for (let index = 0; index < Math.min(leftPoints.length, rightPoints.length); index++) {
    const difference = (leftPoints[index] ?? 0) - (rightPoints[index] ?? 0);
    if (difference !== 0) {
      return difference;
    }
  }
  return leftPoints.length - rightPoints.length;
}

type PathFlavor = "windows" | "posix";

interface NativePath {
  flavor: PathFlavor;
  value: string;
}

interface RootMapping extends NativePath {
  marker: "<workspace>" | "<build>" | "<external>";
}

export function normalizeNativeDiagnostic(
  diagnostic: Diagnostic,
  roots: { workspace: string; build: string; external?: readonly string[] },
): Diagnostic {
  const mappings: RootMapping[] = [
    { ...requireNativeAbsolutePath(roots.build), marker: "<build>" },
    ...(roots.external ?? []).map((root): RootMapping => ({
      ...requireNativeAbsolutePath(root),
      marker: "<external>",
    })),
    { ...requireNativeAbsolutePath(roots.workspace), marker: "<workspace>" },
  ];
  let sourceUri = diagnostic.sourceUri;
  if (sourceUri !== undefined) {
    const source = parseDiagnosticPath(sourceUri, requireNativeAbsolutePath(roots.workspace));
    if (source !== undefined) {
      sourceUri = mapNativePath(source, mappings) ?? sourceUri;
    }
  }
  let message = diagnostic.message;
  for (const mapping of mappings) {
    message = replaceRootInMessage(message, mapping);
  }
  message = normalizeMarkerSeparators(message);
  return {
    ...diagnostic,
    message,
    ...(sourceUri === undefined ? {} : { sourceUri }),
  };
}

function requireNativeAbsolutePath(value: string): NativePath {
  const parsed = parseNativeAbsolutePath(value);
  if (parsed === undefined) {
    throw new Error(`native diagnostic root must be absolute: ${value}`);
  }
  return parsed;
}

function parseDiagnosticPath(value: string, workspaceRoot: NativePath): NativePath | undefined {
  if (value.startsWith("file:")) {
    try {
      const url = new URL(value);
      if (url.protocol !== "file:" || url.username !== "" || url.password !== "") {
        return undefined;
      }
      const decoded = decodeURIComponent(url.pathname);
      if (url.hostname !== "" && url.hostname !== "localhost") {
        return parseNativeAbsolutePath(
          `\\\\${url.hostname}${decoded.replaceAll("/", "\\")}`,
        );
      }
      if (/^\/[A-Za-z]:\//u.test(decoded)) {
        return parseNativeAbsolutePath(decoded.slice(1).replaceAll("/", "\\"));
      }
      return parseNativeAbsolutePath(decoded);
    } catch {
      return undefined;
    }
  }
  if (value.startsWith("workspace:")) {
    if (!value.startsWith("workspace:///")) {
      return undefined;
    }
    const queryIndex = value.indexOf("?", "workspace:///".length);
    const fragmentIndex = value.indexOf("#", "workspace:///".length);
    if (queryIndex !== -1 || fragmentIndex !== -1) {
      return undefined;
    }
    try {
      const rawPath = value.slice("workspace:///".length);
      if (rawPath.length === 0) {
        return undefined;
      }
      const decoded = decodeURIComponent(rawPath);
      const segments = decoded.split("/");
      if (
        segments.length === 0 ||
        segments.some((segment) =>
          segment.length === 0 ||
          segment === "." ||
          segment === ".." ||
          /^[A-Za-z]:/u.test(segment) ||
          segment.includes("\\") ||
          segment.includes("\0"),
        )
      ) {
        return undefined;
      }
      const api = workspaceRoot.flavor === "windows" ? win32 : posix;
      const joined = api.join(workspaceRoot.value, ...segments);
      return parseNativeAbsolutePath(joined);
    } catch {
      return undefined;
    }
  }
  return parseNativeAbsolutePath(value);
}

function parseNativeAbsolutePath(value: string): NativePath | undefined {
  if (typeof value !== "string" || value.includes("\0")) {
    return undefined;
  }
  if (win32.isAbsolute(value) && (/^[A-Za-z]:[\\/]/u.test(value) || value.startsWith("\\\\"))) {
    return { flavor: "windows", value: trimNativeRoot(win32.normalize(value), "windows") };
  }
  if (posix.isAbsolute(value)) {
    return { flavor: "posix", value: trimNativeRoot(posix.normalize(value), "posix") };
  }
  return undefined;
}

function trimNativeRoot(value: string, flavor: PathFlavor): string {
  const rootLength = flavor === "windows" ? win32.parse(value).root.length : posix.parse(value).root.length;
  while (value.length > rootLength && (value.endsWith("/") || value.endsWith("\\"))) {
    value = value.slice(0, -1);
  }
  return value;
}

function mapNativePath(path: NativePath, mappings: readonly RootMapping[]): string | undefined {
  for (const root of mappings) {
    if (root.flavor !== path.flavor) {
      continue;
    }
    const api = path.flavor === "windows" ? win32 : posix;
    const suffix = api.relative(root.value, path.value);
    if (
      suffix === "" ||
      (!api.isAbsolute(suffix) && suffix !== ".." && !suffix.startsWith(`..${api.sep}`))
    ) {
      const portable = suffix.split(api.sep).filter(Boolean).join("/");
      return portable === "" ? root.marker : `${root.marker}/${portable}`;
    }
  }
  return undefined;
}

function replaceRootInMessage(message: string, root: RootMapping): string {
  const variants = root.flavor === "windows"
    ? [root.value, root.value.replaceAll("\\", "/")]
    : [root.value];
  let result = message;
  for (const variant of [...new Set(variants)].sort((left, right) => right.length - left.length)) {
    const expression = new RegExp(
      `${escapeRegularExpression(variant)}(?=$|[\\\\/():,\\s])`,
      root.flavor === "windows" ? "giu" : "gu",
    );
    result = result.replace(expression, root.marker);
  }
  return result;
}

function normalizeMarkerSeparators(message: string): string {
  return message.replace(
    /<(workspace|build|external)>(?:[\\/][^():\r\n]*)*/gu,
    (value) => value.replaceAll("\\", "/"),
  );
}

function escapeRegularExpression(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

export const __testing = Object.freeze({
  compareByCodePoint,
  copyFixtureTree,
});
