import { existsSync, lstatSync } from "node:fs";
import { join } from "node:path";

export interface NativeProductionBundleRoots {
  readonly cmakeBundleRoot: string;
  readonly coverageBundleRoot: string;
  readonly testgenBundleRoot: string;
}

export function productionBundleRoots(
  root: string,
  platform: NodeJS.Platform,
): NativeProductionBundleRoots {
  if (platform !== "linux" && platform !== "win32") {
    throw new Error("production bundle platform is unsupported");
  }
  const key = platform === "win32" ? "windows-x64" : "linux-x64";
  const stagedRoot = join(root, ".superpowers", "runtime", "product-bundles", key);
  const stagedInfo = existsSync(stagedRoot) ? lstatSync(stagedRoot) : undefined;
  if (stagedInfo?.isDirectory() && !stagedInfo.isSymbolicLink()) {
    const bundleRoot = join(stagedRoot, "bundles");
    return {
      cmakeBundleRoot: join(bundleRoot, "cmake"),
      coverageBundleRoot: join(bundleRoot, "coverage"),
      testgenBundleRoot: join(bundleRoot, "testgen"),
    };
  }
  return {
    cmakeBundleRoot: join(root, ".bundled-tools", "cmake"),
    coverageBundleRoot: join(root, ".superpowers", "runtime", "coverage-bundle", key),
    testgenBundleRoot: join(root, ".superpowers", "cache", "testgen-bundle", "22.1.8", key),
  };
}
