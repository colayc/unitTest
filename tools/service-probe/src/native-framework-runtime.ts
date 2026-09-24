import { createHash } from "node:crypto";
import { cp, lstat, mkdir, readFile, realpath, rm } from "node:fs/promises";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import { startService, type TaskServiceFixture } from "./probe.js";
import type { FrameworkPlatform } from "./native-framework-report.js";
import type {
  F1FrameworkIdentity,
  FrameworkPlatformOptions,
  FrameworkPlatformFrameworkOptions,
} from "./native-framework-matrix.js";
import { stableFrameworkIdDigest } from "./native-framework-matrix.js";
import { parseFrameworkRuntimeManifest } from "./native-framework-runtime-contract.js";
import { acquireFrameworkRuntimeLock } from "./native-framework-publish.js";
import { collectAuditedFrameworkBenchmark } from "./native-framework-benchmark.js";
import { hashCompiledFrameworkExecutable, verifyFrameworkWorkspaceSources } from "./native-framework-workspace.js";

export const frameworkRequiredEnvironment = "UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED";

export interface LoadedFrameworkRuntime {
  readonly identity: F1FrameworkIdentity;
  readonly options: FrameworkPlatformOptions;
  dispose(): Promise<void>;
}

interface FrameworkRuntimeDependencies {
  readonly loadFrameworkIdentity: (repositoryRoot: string) => Promise<F1FrameworkIdentity>;
  readonly startService: typeof startService;
  readonly collectBenchmark?: typeof collectAuditedFrameworkBenchmark;
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
  // Published workspaces and executable evidence are immutable inputs. Services
  // own (and recursively delete) their runtime directory, so run only copies.
  const lockRoot = join(repositoryRoot, ".native-e2e/framework-runtime", `${platformName}.lock`);
  const lockOwner = await readFile(join(lockRoot, "owner"), "utf8");
  const consumptionRoot = join(lockRoot, "consumer");
  await mkdir(consumptionRoot, { mode: 0o700 });
  const consumptionIdentity = await lstat(consumptionRoot);
  const canonicalLockRoot = await realpath(lockRoot);
  const canonicalConsumptionRoot = join(canonicalLockRoot, "consumer");
  const cleanup = async () => {
    const current = await lstat(consumptionRoot);
    if (!current.isDirectory() || current.isSymbolicLink() ||
        current.dev !== consumptionIdentity.dev || current.ino !== consumptionIdentity.ino ||
        await realpath(consumptionRoot) !== canonicalConsumptionRoot ||
        await readFile(join(lockRoot, "owner"), "utf8") !== lockOwner) {
      throw new Error("framework consumer ownership changed");
    }
    await rm(consumptionRoot, { recursive: true });
  };
  try {
    const serviceBinary = join(repositoryRoot, "build", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
    const bundleRoot = join(repositoryRoot, ".bundled-tools", "cmake");
    const toolchains = [] as Array<FrameworkPlatformOptions["toolchains"][number]>;
    for (const toolchain of manifest.toolchains) {
      const family = toolchain.family;
      const frameworks: FrameworkPlatformFrameworkOptions[] = [];
      for (const input of toolchain.frameworks) {
        const frameworkId = input.frameworkId;
        const publishedBase = join(repositoryRoot, ".native-e2e", "framework-work", platformName, family, frameworkId);
        const workspaceBase = join(consumptionRoot, platformName, family, frameworkId);
        await cp(publishedBase, workspaceBase, {
          recursive: true, force: false, errorOnExist: true,
          filter: async (source) => {
            const info = await lstat(source);
            const canonicalSource = await realpath(source);
            const canonicalBase = await realpath(publishedBase);
            const child = relative(canonicalBase, canonicalSource);
            if (info.isSymbolicLink() || (!info.isDirectory() && !info.isFile()) ||
                child === ".." || child.startsWith(`..${sep}`) || isAbsolute(child)) {
              throw new Error("framework consumer input is unsafe");
            }
            // Build-profile identities include the workspace path. A fresh Service
            // must rebuild its own tree, not mix producer and consumer profiles.
            // `fs.cp` may pass a differently-normalized spelling of the
            // source path on Windows.  Compare the canonical relative child
            // instead of relying on string identity, otherwise the producer's
            // service directory is copied into the consumer and the fresh
            // service directory creation below fails with EEXIST.
            if (child === "service") return false;
            return true;
          },
        });
        await mkdir(join(workspaceBase, "service"), { mode: 0o700 });
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
          executionEvidence: async (discovery) => {
            if (stableFrameworkIdDigest(frameworkId, discovery.catalog, identity) !== input.stableIdDigest) {
              throw new Error("consumer catalog identity does not match validated F1 input");
            }
            await verifyFrameworkWorkspaceSources(join(workspaceBase, "workspace"), identity, contract);
            const current = await fixture.client.inspectWorkspace();
            const compiler = current.toolchains.find(({ toolchainId }) => toolchainId === discovery.toolchain.toolchainId);
            if (compiler?.family !== family || compiler.version !== discovery.toolchain.version || compiler.compilerSha256 !== discovery.toolchain.compilerSha256) {
              throw new Error("consumer compiler identity changed during execution");
            }
            return {
              sourceArtifactSha256: identity.fixtures[frameworkId].sourceSha256,
              sourceLocationDigest: identity.fixtures[frameworkId].metadataSha256,
              executableArtifactSha256: await hashCompiledFrameworkExecutable(consumptionRoot, platform, family, frameworkId),
            };
          },
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
      collectBenchmark: () => (dependencies.collectBenchmark ?? collectAuditedFrameworkBenchmark)(repositoryRoot),
      candidateCommit: manifest.candidateCommit,
      platform,
      toolchains,
    };
    return {
      identity,
      options,
      async dispose() {
        await disposeFixtures(fixtures);
        await cleanup();
      },
    };
  } catch (error) {
    await disposeFixtures(fixtures);
    await cleanup();
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
