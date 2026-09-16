import { createHash } from "node:crypto";
import { lstat, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { startService, type TaskServiceFixture } from "./probe.js";
import type {
  FrameworkPlatform,
  FrameworkToolchainFamily,
} from "./native-framework-report.js";
import type {
  F1FrameworkIdentity,
  FrameworkPlatformOptions,
  FrameworkPlatformFrameworkOptions,
} from "./native-framework-matrix.js";

const DIGEST = /^[0-9a-f]{64}$/u;
const COMMIT = /^[0-9a-f]{40}$/u;
const RUNTIME_KEYS = ["benchmark", "candidateCommit", "contractSha256", "platform", "schemaVersion", "toolchains"] as const;

export const frameworkRequiredEnvironment = "UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED";

export interface LoadedFrameworkRuntime {
  readonly identity: F1FrameworkIdentity;
  readonly options: FrameworkPlatformOptions;
  dispose(): Promise<void>;
}

interface FrameworkRuntimeDependencies {
  readonly loadFrameworkIdentity: (repositoryRoot: string) => Promise<F1FrameworkIdentity>;
  readonly startService: typeof startService;
}

const defaultDependencies: FrameworkRuntimeDependencies = {
  loadFrameworkIdentity,
  startService,
};

export async function loadRequiredFrameworkRuntime(
  repositoryRoot: string,
  platform: FrameworkPlatform,
  artifactDirectory: string,
  dependencies: FrameworkRuntimeDependencies = defaultDependencies,
): Promise<LoadedFrameworkRuntime> {
  const platformName = platform === "win32" ? "windows" : "linux";
  const manifestPath = join(repositoryRoot, ".native-e2e", "framework-runtime", `${platformName}.json`);
  const info = await lstat(manifestPath).catch(() => undefined);
  if (info === undefined || !info.isFile() || info.isSymbolicLink()) {
    throw new Error(`required framework runtime manifest is missing for ${platformName}`);
  }
  const raw = await readFile(manifestPath);
  const manifest = parseObject(raw, "framework runtime manifest");
  closedKeys(manifest, RUNTIME_KEYS, "framework runtime manifest");
  if (manifest.schemaVersion !== 1 || manifest.platform !== platform ||
      typeof manifest.candidateCommit !== "string" || !COMMIT.test(manifest.candidateCommit) ||
      typeof manifest.contractSha256 !== "string" || !DIGEST.test(manifest.contractSha256) ||
      !Array.isArray(manifest.toolchains)) {
    throw new Error("required framework runtime manifest identity is invalid");
  }
  const contract = await readFile(join(repositoryRoot, "testdata", "framework-matrix", "contract.json"));
  if (digest(contract) !== manifest.contractSha256) {
    throw new Error("required framework runtime manifest is not bound to the matrix contract");
  }
  const identity = await dependencies.loadFrameworkIdentity(repositoryRoot);

  const fixtures: TaskServiceFixture[] = [];
  try {
    const serviceBinary = join(repositoryRoot, "build", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
    const bundleRoot = join(repositoryRoot, ".bundled-tools", "cmake");
    const toolchains = [] as Array<FrameworkPlatformOptions["toolchains"][number]>;
    for (const value of manifest.toolchains) {
      const toolchain = requireObject(value, "framework runtime toolchain");
      closedKeys(toolchain, ["compilerSha256", "compilerVersion", "family", "frameworks"], "framework runtime toolchain");
      if (!validFamily(platform, toolchain.family) || typeof toolchain.compilerVersion !== "string" ||
          typeof toolchain.compilerSha256 !== "string" || !DIGEST.test(toolchain.compilerSha256) ||
          !Array.isArray(toolchain.frameworks)) {
        throw new Error("framework runtime toolchain identity is invalid");
      }
      const family = toolchain.family as FrameworkToolchainFamily;
      const frameworks: FrameworkPlatformFrameworkOptions[] = [];
      for (const value of toolchain.frameworks) {
        const input = requireObject(value, "framework runtime framework");
        closedKeys(input, [
          "cMockProvenance", "catalogArtifactSha256", "dependencySha256", "dependencyTreeSha256",
          "dependencyVersion", "evidence", "frameworkId", "stableIdDigest", "timeoutMs",
        ], "framework runtime framework");
        const frameworkId = input.frameworkId;
        if (frameworkId !== "cpputest" && frameworkId !== "unity") {
          throw new Error("framework runtime framework ID is invalid");
        }
        const workspaceBase = join(repositoryRoot, ".native-e2e", "framework-work", platformName, family, frameworkId);
        const fixture = await dependencies.startService(serviceBinary, join(workspaceBase, "service"), {
          timeoutMs: 120_000,
          workspaceRoot: join(workspaceBase, "workspace"),
          trustedWorkspace: true,
          cmakeBundleRoot: bundleRoot,
        });
        fixtures.push(fixture);
        frameworks.push({
          ...(input as unknown as Omit<FrameworkPlatformFrameworkOptions,
            "candidateCommit" | "fixture" | "platform" | "toolchainFamily">),
          candidateCommit: manifest.candidateCommit,
          fixture,
          frameworkId,
          platform,
          toolchainFamily: family,
        });
      }
      toolchains.push({
        family,
        compilerVersion: toolchain.compilerVersion as string,
        compilerSha256: toolchain.compilerSha256 as string,
        frameworks,
      });
    }
    const options: FrameworkPlatformOptions = {
      artifactDirectory: resolve(artifactDirectory),
      benchmark: manifest.benchmark as FrameworkPlatformOptions["benchmark"],
      candidateCommit: manifest.candidateCommit,
      platform,
      toolchains,
    };
    return {
      identity,
      options,
      async dispose() {
        await disposeFixtures(fixtures);
      },
    };
  } catch (error) {
    await disposeFixtures(fixtures).catch(() => undefined);
    throw error;
  }
}

async function loadFrameworkIdentity(root: string): Promise<F1FrameworkIdentity> {
  // @ts-expect-error consume.mjs is validated by its direct Node test suite.
  const module = await import("../../framework-bundle/consume.mjs") as {
    loadF1FrameworkIdentity(repositoryRoot: string): Promise<F1FrameworkIdentity>;
  };
  return module.loadF1FrameworkIdentity(root);
}

export function frameworkMatrixRequired(environment: NodeJS.ProcessEnv): boolean {
  const value = environment[frameworkRequiredEnvironment];
  if (value === undefined || value === "0") return false;
  if (value === "1") return true;
  throw new Error(`${frameworkRequiredEnvironment} must be 0 or 1`);
}

function validFamily(platform: FrameworkPlatform, value: unknown): value is FrameworkToolchainFamily {
  return platform === "linux" ? value === "clang" || value === "gcc" : value === "clang-cl" || value === "msvc";
}

function parseObject(bytes: Uint8Array, label: string): Record<string, unknown> {
  try {
    return requireObject(JSON.parse(Buffer.from(bytes).toString("utf8")), label);
  } catch (error) {
    if (error instanceof Error && error.message.includes(label)) throw error;
    throw new Error(`${label} is not valid JSON`);
  }
}

function requireObject(value: unknown, label: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) {
    throw new Error(`${label} must be a plain object`);
  }
  return value as Record<string, unknown>;
}

function closedKeys(value: Record<string, unknown>, allowed: readonly string[], label: string): void {
  const extras = Object.keys(value).filter((key) => !allowed.includes(key));
  if (extras.length > 0) throw new Error(`${label} has unexpected fields: ${extras.join(",")}`);
}

function digest(bytes: Uint8Array): string {
  return createHash("sha256").update(bytes).digest("hex");
}

async function disposeFixtures(fixtures: TaskServiceFixture[]): Promise<void> {
  const results = await Promise.allSettled([...fixtures].reverse().map((fixture) => fixture.dispose()));
  const failures = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");
  if (failures.length > 0) throw new AggregateError(failures.map(({ reason }) => reason), "framework fixture cleanup failed");
}
