import { createHash, randomUUID } from "node:crypto";
import { lstat, readFile, rm } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import { prepareLinuxFrameworkInputs, type LinuxFrameworkInputManifest } from "./linux-framework-inputs.js";
import { verifyPreparedCMakeBundle } from "./native-build.js";
import { discoverFrameworkCatalog, loadMatrixContract, stableFrameworkIdDigest, type F1FrameworkIdentity } from "./native-framework-matrix.js";
import type { FrameworkPlatform, FrameworkToolchainFamily } from "./native-framework-report.js";
import { buildFrameworkRuntimeManifest, type FrameworkRuntimeManifest, type FrameworkRuntimeFramework, type FrameworkRuntimeToolchain } from "./native-framework-runtime-contract.js";
import { hashCompiledFrameworkExecutable, stageFrameworkWorkspace, validateOwnedFrameworkStage } from "./native-framework-workspace.js";
import { startService } from "./probe.js";

type PreparedRoots = Readonly<Record<"cpputest" | "unity" | "cmock", string>>;
export interface FrameworkRuntimePrepareOptions {
  readonly repositoryRoot: string;
  readonly platform: FrameworkPlatform;
  readonly candidateCommit: string;
}
export interface FrameworkRuntimePrepareDependencies {
  /** Audited backend evidence, never inferred from framework discovery. */
  readonly benchmark: FrameworkRuntimeManifest["benchmark"];
  readonly startService?: typeof startService;
  readonly verifyInputs?: (options: FrameworkRuntimePrepareOptions, manifest: LinuxFrameworkInputManifest, identity: F1FrameworkIdentity) => Promise<PreparedRoots>;
}
export interface PreparedFrameworkRuntime {
  readonly manifest: FrameworkRuntimeManifest;
  readonly ownershipId: string;
  readonly ownedStagingRoots: readonly string[];
}

export async function prepareFrameworkRuntime(
  options: FrameworkRuntimePrepareOptions,
  dependencies?: FrameworkRuntimePrepareDependencies,
): Promise<PreparedFrameworkRuntime> {
  if (options === null || typeof options !== "object" || Object.getPrototypeOf(options) !== Object.prototype ||
      Reflect.ownKeys(options).length !== 3 || !["repositoryRoot", "platform", "candidateCommit"].every((key) => Object.hasOwn(options, key)) ||
      !isAbsolute(options.repositoryRoot) || options.repositoryRoot.includes("\0") ||
      (options.platform !== "linux" && options.platform !== "win32") || !/^[0-9a-f]{40}$/u.test(options.candidateCommit)) {
    throw new Error("framework runtime preparation options are invalid");
  }
  if (dependencies?.benchmark === undefined) throw new Error("verified framework benchmark evidence is required");
  const repositoryRoot = resolve(options.repositoryRoot);
  // @ts-expect-error F1 loader is validated by its direct Node suite.
  const { loadF1FrameworkIdentity } = await import("../../framework-bundle/consume.mjs");
  // @ts-expect-error Locked manifest reader is validated by its direct Node suite.
  const { readFrameworkManifest } = await import("../../framework-bundle/manifest.mjs");
  // @ts-expect-error Canonical provenance reader is validated by its direct Node suite.
  const { readCMockGeneration } = await import("../../framework-bundle/cmock-provenance.mjs");
  const identity: F1FrameworkIdentity = await loadF1FrameworkIdentity(repositoryRoot);
  await loadMatrixContract("cpputest", repositoryRoot);
  await loadMatrixContract("unity", repositoryRoot);
  const { manifest: locked, manifestSha256 } = await readFrameworkManifest(join(repositoryRoot, "tools/framework-bundle/manifest.json")) as { manifest: LinuxFrameworkInputManifest; manifestSha256: string };
  if (manifestSha256 !== identity.manifestSha256) throw new Error("framework manifest identity changed during preparation");
  const provenance = await readCMockGeneration(join(repositoryRoot, "testdata/frameworks/unity/mocks/cmock-generation.json"), { root: repositoryRoot, manifest: locked, manifestSha256 });
  if (provenance.cMockProvenanceSha256 !== identity.cMockProvenanceSha256) throw new Error("CMock provenance changed during preparation");
  const preparedFrameworkRoots = await (dependencies.verifyInputs ?? verifyInputs)(options, locked, identity);
  const platformName = options.platform === "win32" ? "windows" : "linux";
  const workRoot = join(repositoryRoot, ".native-e2e/framework-work");
  const ownershipId = randomUUID();
  const ownedStagingRoots: string[] = [];
  const toolchains: FrameworkRuntimeToolchain[] = [];
  const families: readonly FrameworkToolchainFamily[] = options.platform === "win32" ? ["clang-cl", "msvc"] : ["clang", "gcc"];
  try {
    for (const family of families) {
      const frameworks: FrameworkRuntimeFramework[] = [];
      let compilerVersion: string | undefined;
      let compilerSha256: string | undefined;
      for (const frameworkId of ["cpputest", "unity"] as const) {
        const stageRoot = join(workRoot, platformName, family, frameworkId);
        const staged = await stageFrameworkWorkspace({
          repositoryRoot, stageRoot, ownershipId, candidateCommit: options.candidateCommit, platform: options.platform,
          family, frameworkId, preparedFrameworkRoots,
          cmakeHelper: join(repositoryRoot, "sdk/cmake/UnitTestIDE.cmake"),
          unityRunnerGenerator: join(repositoryRoot, "build", options.platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"),
        });
        ownedStagingRoots.push(stageRoot);
        const fixture = await (dependencies.startService ?? startService)(
          join(repositoryRoot, "build", options.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"),
          staged.serviceDirectory,
          { workspaceRoot: staged.workspaceRoot, trustedWorkspace: true, timeoutMs: 120_000, cmakeBundleRoot: join(repositoryRoot, ".bundled-tools/cmake") },
        );
        try {
          const discovery = await discoverFrameworkCatalog({ fixture, frameworkId, toolchainFamily: family, timeoutMs: 120_000 });
          const toolchain = discovery.toolchain;
          if (typeof toolchain.compilerSha256 !== "string" || !/^[0-9a-f]{64}$/u.test(toolchain.compilerSha256)) throw new Error("verified Service compiler digest is required");
          if (compilerVersion !== undefined && (compilerVersion !== toolchain.version || compilerSha256 !== toolchain.compilerSha256)) throw new Error("Service compiler identity changed across frameworks");
          compilerVersion = toolchain.version;
          compilerSha256 = toolchain.compilerSha256;
          const dependency = locked.frameworks.find(({ id }) => id === frameworkId)!;
          frameworks.push({
            frameworkId, dependencyVersion: dependency.version, dependencySha256: dependency.source.sha256,
            dependencyTreeSha256: identity.frameworkTreeSha256[frameworkId],
            catalogArtifactSha256: discovery.catalogArtifactSha256,
            stableIdDigest: stableFrameworkIdDigest(frameworkId, discovery.catalog, identity), timeoutMs: 120_000,
            evidence: {
              sourceArtifactSha256: identity.fixtures[frameworkId].sourceSha256,
              sourceLocationDigest: identity.fixtures[frameworkId].metadataSha256,
              executableArtifactSha256: await hashCompiledFrameworkExecutable(workRoot, options.platform, family, frameworkId),
            },
            ...(frameworkId === "unity" ? { cMockProvenance: {
              revision: provenance.value.cmock.revision, generatorVersion: provenance.value.generator.version,
              inputSha256: provenance.value.input.sha256, outputSha256: provenance.outputSha256,
              manifestSha256: provenance.cMockProvenanceSha256, generatedAtRuntime: false,
            } } : {}),
          });
        } finally {
          try { await fixture.dispose(); } catch { throw new Error("framework Service cleanup failed"); }
        }
      }
      toolchains.push({ family, compilerVersion: compilerVersion!, compilerSha256: compilerSha256!, frameworks });
    }
    const manifest = buildFrameworkRuntimeManifest({
      schemaVersion: 1, candidateCommit: options.candidateCommit, platform: options.platform,
      contractSha256: createHash("sha256").update(await readFile(join(repositoryRoot, "testdata/framework-matrix/contract.json"))).digest("hex"),
      toolchains, benchmark: dependencies.benchmark,
    });
    return { manifest, ownershipId, ownedStagingRoots: Object.freeze([...ownedStagingRoots]) };
  } catch (error) {
    for (const stageRoot of [...ownedStagingRoots].reverse()) {
      await validateOwnedFrameworkStage(stageRoot, ownershipId);
      await rm(stageRoot, { recursive: true });
    }
    throw error;
  }
}

async function verifyInputs(options: FrameworkRuntimePrepareOptions, manifest: LinuxFrameworkInputManifest, identity: F1FrameworkIdentity): Promise<PreparedRoots> {
  const root = resolve(options.repositoryRoot);
  await verifyPreparedCMakeBundle(join(root, ".bundled-tools/cmake"), options.platform, "x64", { manifestPath: join(root, "tools/cmake-bundle/manifest.json") });
  const binary = await lstat(join(root, "build", options.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"));
  if (!binary.isFile() || binary.isSymbolicLink()) throw new Error("framework Service binary is unsafe");
  // Despite its historical name this validator accepts both locked manifest platforms.
  const boundary = await prepareLinuxFrameworkInputs({
    manifest, manifestSha256: identity.manifestSha256, repositoryRoot: root,
    cacheRoot: join(root, ".superpowers/cache/framework-bundle"),
    sourceRoot: join(root, ".superpowers/runtime/framework-bundle/v2", identity.manifestSha256),
    helperPath: join(root, "sdk/cmake/UnitTestIDE.cmake"),
    generatorPath: join(root, "build", options.platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"),
  });
  return { cpputest: boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT!, unity: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT!, cmock: boundary.environment.UNIT_TEST_IDE_TEST_CMOCK_ROOT! };
}
