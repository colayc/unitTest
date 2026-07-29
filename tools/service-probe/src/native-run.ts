import { resolve } from "node:path";
import { tmpdir } from "node:os";
import { pathToFileURL } from "node:url";
import type { RequiredToolchainFamily } from "./native-build.js";
import { installNativeHttpNetworkGuard } from "./native-network-guard.js";
import { resolveNativeWorkDirectory } from "./native-work-root.js";

installNativeHttpNetworkGuard();

const repositoryRoot = resolve(import.meta.dirname, "../../..");

function parsePlatform(arguments_: readonly string[]): "linux" | "win32" {
  if (arguments_.length === 0) {
    if (process.platform === "linux" || process.platform === "win32") {
      return process.platform;
    }
    throw new Error(`native E2E does not support ${process.platform}`);
  }
  if (
    arguments_.length !== 2 ||
    arguments_[0] !== "--platform" ||
    arguments_[1] !== "linux" && arguments_[1] !== "win32"
  ) {
    throw new Error("usage: native-run.js [--platform <linux|win32>]");
  }
  return arguments_[1];
}

export async function main(arguments_: readonly string[] = process.argv.slice(2)): Promise<void> {
  const { runNativeMatrix } = await import("./native-build.js");
  const platform = parsePlatform(arguments_);
  if (platform !== process.platform) {
    throw new Error(`native E2E for ${platform} must run on ${platform}`);
  }
  const requiredFamilies: readonly RequiredToolchainFamily[] = platform === "linux"
    ? ["gcc", "clang"]
    : ["msvc", "clang-cl"];
  const results = await runNativeMatrix({
    platform,
    requiredFamilies,
    artifactDirectory: resolve(
      repositoryRoot,
      ".native-e2e",
      "artifacts",
      platform === "linux" ? "linux" : "windows",
    ),
    workDirectory: resolveNativeWorkDirectory(repositoryRoot, platform, tmpdir()),
  });
  process.stdout.write(`${JSON.stringify({
    platform,
    results: results.map((result) => ({
      family: result.toolchainFamily,
      version: result.toolchainVersion,
      generator: result.generator,
      scenarios: result.scenarios,
    })),
  })}\n`);
}

if (
  process.argv[1] !== undefined &&
  import.meta.url === pathToFileURL(resolve(process.argv[1])).href
) {
  main().catch((error: unknown) => {
    const message = error instanceof Error ? error.message : String(error);
    process.stderr.write(`native-e2e: ${message}\n`);
    process.exitCode = 1;
  });
}

export const __testing = Object.freeze({ parsePlatform });
