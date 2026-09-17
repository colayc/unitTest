import { createHash } from "node:crypto";
import { lstat, readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { startService, type TaskServiceFixture } from "./probe.js";
import type { FrameworkPlatform } from "./native-framework-report.js";
import type {
  F1FrameworkIdentity,
  FrameworkPlatformOptions,
  FrameworkPlatformFrameworkOptions,
} from "./native-framework-matrix.js";
import { parseFrameworkRuntimeManifest } from "./native-framework-runtime-contract.js";
import { acquireFrameworkRuntimeLock } from "./native-framework-publish.js";

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
  const release = await acquireFrameworkRuntimeLock(repositoryRoot, platform);
  try {
    const loaded = await loadLockedFrameworkRuntime(repositoryRoot, platform, artifactDirectory, dependencies);
    let disposed = false;
    return { ...loaded, async dispose() {
      if (disposed) return;
      disposed = true;
      try { await loaded.dispose(); } finally { await release(); }
    } };
  } catch (error) { await release(); throw error; }
}

async function loadLockedFrameworkRuntime(
  repositoryRoot: string,
  platform: FrameworkPlatform,
  artifactDirectory: string,
  dependencies: FrameworkRuntimeDependencies,
): Promise<LoadedFrameworkRuntime> {
  const platformName = platform === "win32" ? "windows" : "linux";
  const manifestPath = join(repositoryRoot, ".native-e2e", "framework-runtime", `${platformName}.json`);
  const info = await lstat(manifestPath).catch(() => undefined);
  if (info === undefined || !info.isFile() || info.isSymbolicLink()) {
    throw new Error(`required framework runtime manifest is missing for ${platformName}`);
  }
  const raw = await readFile(manifestPath);
  const contract = await readFile(join(repositoryRoot, "testdata", "framework-matrix", "contract.json"));
  const manifest = parseFrameworkRuntimeManifest(raw, platform, digest(contract));
  const identity = await dependencies.loadFrameworkIdentity(repositoryRoot);

  const fixtures: TaskServiceFixture[] = [];
  try {
    const serviceBinary = join(repositoryRoot, "build", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
    const bundleRoot = join(repositoryRoot, ".bundled-tools", "cmake");
    const toolchains = [] as Array<FrameworkPlatformOptions["toolchains"][number]>;
    for (const toolchain of manifest.toolchains) {
      const family = toolchain.family;
      const frameworks: FrameworkPlatformFrameworkOptions[] = [];
      for (const input of toolchain.frameworks) {
        const frameworkId = input.frameworkId;
        const workspaceBase = join(repositoryRoot, ".native-e2e", "framework-work", platformName, family, frameworkId);
        const fixture = await dependencies.startService(serviceBinary, join(workspaceBase, "service"), {
          timeoutMs: 120_000,
          workspaceRoot: join(workspaceBase, "workspace"),
          trustedWorkspace: true,
          cmakeBundleRoot: bundleRoot,
        });
        fixtures.push(fixture);
        frameworks.push({
          candidateCommit: manifest.candidateCommit,
          catalogArtifactSha256: input.catalogArtifactSha256,
          ...(input.cMockProvenance === undefined ? {} : { cMockProvenance: input.cMockProvenance }),
          dependencySha256: input.dependencySha256,
          dependencyTreeSha256: input.dependencyTreeSha256,
          dependencyVersion: input.dependencyVersion,
          evidence: input.evidence,
          fixture,
          frameworkId,
          platform,
          stableIdDigest: input.stableIdDigest,
          timeoutMs: input.timeoutMs,
          toolchainFamily: family,
        });
      }
      toolchains.push({
        family,
        compilerVersion: toolchain.compilerVersion,
        compilerSha256: toolchain.compilerSha256,
        frameworks,
      });
    }
    const options: FrameworkPlatformOptions = {
      artifactDirectory: resolve(artifactDirectory),
      benchmark: {
        ...manifest.benchmark,
        allocationsPerOperation: [
          manifest.benchmark.allocationsPerOperation[0],
          manifest.benchmark.allocationsPerOperation[1],
          manifest.benchmark.allocationsPerOperation[2],
        ],
      },
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

function digest(bytes: Uint8Array): string {
  return createHash("sha256").update(bytes).digest("hex");
}

async function disposeFixtures(fixtures: TaskServiceFixture[]): Promise<void> {
  const results = await Promise.allSettled([...fixtures].reverse().map((fixture) => fixture.dispose()));
  const failures = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");
  if (failures.length > 0) throw new AggregateError(failures.map(({ reason }) => reason), "framework fixture cleanup failed");
}
