import { join } from "node:path";

export interface NativeProductionBundleRoots {
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
  return {
    coverageBundleRoot: join(root, ".superpowers", "runtime", "coverage-bundle", key),
    testgenBundleRoot: join(root, ".superpowers", "cache", "testgen-bundle", "22.1.8", key),
  };
}
