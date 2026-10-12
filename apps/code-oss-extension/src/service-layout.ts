import { lstat, realpath } from "node:fs/promises";
import { posix, win32, type PlatformPath } from "node:path";

export interface ProductLayout {
  readonly productRoot: string;
  readonly serviceExecutable: string;
  readonly cmakeBundleRoot: string;
  readonly coverageBundleRoot: string;
  readonly testgenBundleRoot: string;
}

const unavailable = () => new Error("product layout is unavailable");

function pathApi(platform: NodeJS.Platform): PlatformPath {
  return platform === "win32" ? win32 : posix;
}

function samePath(left: string, right: string, platform: NodeJS.Platform): boolean {
  return platform === "win32"
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
}

function unsafeWindowsRoot(value: string): boolean {
  return value.startsWith("\\\\") || value.startsWith("\\\\?\\") || value.startsWith("\\\\.\\");
}

export function resolveProductLayout(
  extensionPath: string,
  platform: NodeJS.Platform,
  developmentMode: boolean
): ProductLayout {
  const api = pathApi(platform);
  if (typeof extensionPath !== "string" || extensionPath.length === 0 || !api.isAbsolute(extensionPath) || extensionPath.includes("\0")) {
    throw unavailable();
  }
  if (platform === "win32" && unsafeWindowsRoot(extensionPath)) throw unavailable();

  const normalized = api.resolve(extensionPath);
  let productRoot: string;
  let serviceExecutable: string;
  let bundleRoot: string;
  if (developmentMode) {
    productRoot = api.resolve(normalized, "..", "..");
    serviceExecutable = api.join(normalized, "bin", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
    bundleRoot = api.join(productRoot, ".bundled-tools");
  } else {
    productRoot = api.resolve(normalized, "..", "..", "..");
    const expected = api.join(productRoot, "app", "extensions", "unit-test-ide");
    if (!samePath(normalized, expected, platform)) throw unavailable();
    serviceExecutable = api.join(productRoot, "service", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
    bundleRoot = api.join(productRoot, "bundles");
  }
  return Object.freeze({
    productRoot,
    serviceExecutable,
    cmakeBundleRoot: api.join(bundleRoot, "cmake"),
    coverageBundleRoot: api.join(bundleRoot, "coverage"),
    testgenBundleRoot: api.join(bundleRoot, "testgen")
  });
}

async function requireDirectEntry(path: string, directory: boolean): Promise<string> {
  let info;
  try {
    info = await lstat(path);
  } catch {
    throw unavailable();
  }
  if (info.isSymbolicLink() || (directory ? !info.isDirectory() : !info.isFile())) throw unavailable();
  try {
    return await realpath(path);
  } catch {
    throw unavailable();
  }
}

function contained(root: string, candidate: string): boolean {
  const relative = win32.isAbsolute(root) ? win32.relative(root, candidate) : posix.relative(root, candidate);
  return relative !== "" && relative !== ".." && !relative.startsWith(`..${win32.isAbsolute(root) ? "\\" : "/"}`) &&
    !(win32.isAbsolute(root) ? win32.isAbsolute(relative) : posix.isAbsolute(relative));
}

export async function validateProductLayout(layout: ProductLayout): Promise<void> {
  const root = await requireDirectEntry(layout.productRoot, true);
  const entries = await Promise.all([
    requireDirectEntry(layout.serviceExecutable, false),
    requireDirectEntry(layout.cmakeBundleRoot, true),
    requireDirectEntry(layout.coverageBundleRoot, true),
    requireDirectEntry(layout.testgenBundleRoot, true)
  ]);
  if (entries.some((entry) => !contained(root, entry))) throw unavailable();
}
