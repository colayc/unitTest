import { join, resolve } from "node:path";

const supportedPlatforms = new Set(["windows-x64", "linux-x64"]);

export function platformKey(platform = process.platform, arch = process.arch) {
  const key = platform === "win32" ? `windows-${arch}` : `${platform}-${arch}`;
  if (!supportedPlatforms.has(key)) {
    throw new Error(`unsupported coverage bundle platform: ${platform}-${arch}`);
  }
  return key;
}

export function bundleDirectory(repositoryRoot, key = platformKey()) {
  if (!supportedPlatforms.has(key)) {
    throw new Error(`unsupported coverage bundle platform: ${key}`);
  }
  return join(resolve(repositoryRoot), ".superpowers", "runtime", "coverage-bundle", key);
}

export function cacheDirectory(repositoryRoot) {
  return join(resolve(repositoryRoot), ".superpowers", "cache", "coverage-bundle");
}

export const coverageBundlePlatforms = Object.freeze([...supportedPlatforms]);
