import { lstat, mkdir, open, rename, rm } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import type { NativeScenarioResult, PreparedCMakeBundle } from "./native-build.js";

export interface NativeToolchainReport {
  schemaVersion: 1;
  platform: NodeJS.Platform;
  architecture: string;
  cmake: {
    version: string;
    archiveSha256: string;
  };
  results: readonly NativeScenarioResult[];
}

export async function writeNativeToolchainReport(
  artifactDirectory: string,
  platform: NodeJS.Platform,
  architecture: string,
  bundle: PreparedCMakeBundle,
  results: readonly NativeScenarioResult[],
): Promise<string> {
  if (!isAbsolute(artifactDirectory) || artifactDirectory.includes("\0")) {
    throw new Error("native artifact directory must be an absolute path");
  }
  const report = buildReport(platform, architecture, bundle, results);
  const directory = resolve(artifactDirectory);
  await mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await lstat(directory);
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error("native artifact directory is unsafe");
  }
  const target = join(directory, "toolchain-report.json");
  const temporary = join(directory, `.toolchain-report-${process.pid}.tmp`);
  const bytes = Buffer.from(`${JSON.stringify(report, null, 2)}\n`, "utf8");
  let handle;
  try {
    handle = await open(temporary, "wx", 0o600);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(temporary, target);
  } catch (error) {
    await handle?.close().catch(() => undefined);
    await rm(temporary, { force: true }).catch(() => undefined);
    throw error;
  }
  return target;
}

function buildReport(
  platform: NodeJS.Platform,
  architecture: string,
  bundle: PreparedCMakeBundle,
  results: readonly NativeScenarioResult[],
): NativeToolchainReport {
  if (
    platform !== "linux" && platform !== "win32" ||
    architecture !== "x64" ||
    !/^[0-9]+\.[0-9]+\.[0-9]+$/u.test(bundle.cmakeVersion) ||
    !/^[0-9a-f]{64}$/u.test(bundle.archiveSha256)
  ) {
    throw new Error("invalid native report identity");
  }
  for (const result of results) {
    if (
      result.platform !== platform ||
      !safeReportString(result.toolchainVersion) ||
      !safeReportString(result.generator) ||
      result.cmakeVersion !== bundle.cmakeVersion
    ) {
      throw new Error("invalid native scenario report");
    }
    for (const [name, status] of Object.entries(result.scenarios)) {
      if (!/^[a-z0-9][a-z0-9-]{0,63}$/u.test(name) || status !== "passed" && status !== "skipped") {
        throw new Error("invalid native scenario status");
      }
    }
  }
  return {
    schemaVersion: 1,
    platform,
    architecture,
    cmake: {
      version: bundle.cmakeVersion,
      archiveSha256: bundle.archiveSha256,
    },
    results,
  };
}

function safeReportString(value: string): boolean {
  return (
    value.length > 0 &&
    value.length <= 256 &&
    !value.includes("\0") &&
    !isAbsolute(value) &&
    !/^[A-Za-z]:[\\/]/u.test(value) &&
    !value.startsWith("\\\\")
  );
}

export const __testing = Object.freeze({ buildReport });
