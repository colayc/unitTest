import { createHash, randomUUID } from "node:crypto";
import { lstat, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { isAbsolute, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { collectAuditedFrameworkBenchmark } from "./native-framework-benchmark.js";
import { publishFrameworkRuntime } from "./native-framework-publish.js";
import { prepareLinuxFrameworkInputs, type LinuxFrameworkInputManifest } from "./linux-framework-inputs.js";
import { verifyPreparedCMakeBundle } from "./native-build.js";
import { discoverFrameworkCatalog, loadMatrixContract, stableFrameworkIdDigest, type F1FrameworkIdentity } from "./native-framework-matrix.js";
import type { FrameworkPlatform, FrameworkToolchainFamily } from "./native-framework-report.js";
import { buildFrameworkRuntimeManifest, type FrameworkRuntimeManifest, type FrameworkRuntimeFramework, type FrameworkRuntimeToolchain } from "./native-framework-runtime-contract.js";
import { readCompiledFrameworkExecutable, stageFrameworkWorkspace, validateOwnedFrameworkStage } from "./native-framework-workspace.js";
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
  let failureStage = "validate-options";
  try {
    return await prepareFrameworkRuntimeInternal(options, dependencies, (stage) => { failureStage = stage; });
  } catch {
    // Nothing from filesystem errors, child output, injected operations, or causes
    // crosses this boundary. Even the stack is path-free for CLI diagnostics.
    process.stderr.write(`framework runtime preparation failed [stage=${failureStage}]\n`);
    const error = Object.assign(new Error("framework runtime preparation failed"), { code: "FRAMEWORK_RUNTIME_PREPARE_FAILED" });
    error.name = "FrameworkRuntimePrepareError";
    error.stack = `${error.name}: ${error.message}`;
    throw error;
  }
}

async function prepareFrameworkRuntimeInternal(
  options: FrameworkRuntimePrepareOptions,
  dependencies?: FrameworkRuntimePrepareDependencies,
  markStage: (stage: string) => void = () => {},
): Promise<PreparedFrameworkRuntime> {
  markStage("validate-options");
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
  markStage("load-f1-identity");
  let identity: F1FrameworkIdentity;
  try {
    identity = await loadF1FrameworkIdentity(repositoryRoot);
  } catch (error) {
    const code = error !== null && typeof error === "object" && "code" in error && typeof error.code === "string" ? error.code : "unknown";
    markStage(`load-f1-identity:${code}`);
    throw error;
  }
  markStage("load-matrix-contracts");
  await loadMatrixContract("cpputest", repositoryRoot);
  await loadMatrixContract("unity", repositoryRoot);
  markStage("load-locked-manifest");
  const { manifest: locked, manifestSha256 } = await readFrameworkManifest(join(repositoryRoot, "tools/framework-bundle/manifest.json")) as { manifest: LinuxFrameworkInputManifest; manifestSha256: string };
  if (manifestSha256 !== identity.manifestSha256) throw new Error("framework manifest identity changed during preparation");
  markStage("load-cmock-provenance");
  const provenance = await readCMockGeneration(join(repositoryRoot, "testdata/frameworks/unity/mocks/cmock-generation.json"), { root: repositoryRoot, manifest: locked, manifestSha256 });
  if (provenance.cMockProvenanceSha256 !== identity.cMockProvenanceSha256) throw new Error("CMock provenance changed during preparation");
  markStage("verify-locked-inputs");
  const preparedFrameworkRoots = dependencies.verifyInputs !== undefined
    ? await dependencies.verifyInputs(options, locked, identity)
    : await verifyInputs(options, locked, identity, markStage);
  const platformName = options.platform === "win32" ? "windows" : "linux";
  const ownershipId = randomUUID();
  const workRoot = join(repositoryRoot, ".native-e2e/framework-work/.staging", ownershipId);
  const ownedStagingRoots: string[] = [];
  const toolchains: FrameworkRuntimeToolchain[] = [];
  const families: readonly FrameworkToolchainFamily[] = options.platform === "win32" ? ["clang-cl", "msvc"] : ["clang", "gcc"];
  try {
    for (const family of families) {
      const frameworks: FrameworkRuntimeFramework[] = [];
      let compilerVersion: string | undefined;
      let compilerSha256: string | undefined;
      for (const frameworkId of ["cpputest", "unity"] as const) {
        markStage(`stage-workspace:${family}:${frameworkId}`);
        const stageRoot = join(workRoot, platformName, family, frameworkId);
        const staged = await stageFrameworkWorkspace({
          repositoryRoot, stageRoot, ownershipId, candidateCommit: options.candidateCommit, platform: options.platform,
          family, frameworkId, preparedFrameworkRoots,
          cmakeHelper: join(repositoryRoot, "sdk/cmake/UnitTestIDE.cmake"),
          unityRunnerGenerator: join(repositoryRoot, "build", options.platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"),
        });
        ownedStagingRoots.push(stageRoot);
        markStage(`start-service:${family}:${frameworkId}`);
        const fixture = await (dependencies.startService ?? startService)(
          join(repositoryRoot, "build", options.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"),
          staged.serviceDirectory,
          { workspaceRoot: staged.workspaceRoot, trustedWorkspace: true, timeoutMs: 120_000, cmakeBundleRoot: join(repositoryRoot, ".bundled-tools/cmake") },
        );
        let compiled: Awaited<ReturnType<typeof readCompiledFrameworkExecutable>>;
        try {
          markStage(`discover-catalog:${family}:${frameworkId}`);
          let discovery: Awaited<ReturnType<typeof discoverFrameworkCatalog>>;
          try {
            discovery = await discoverFrameworkCatalog({ fixture, repositoryRoot, frameworkId, toolchainFamily: family, timeoutMs: 120_000 });
          } catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            const taskOutcome = message.match(/discovery finished with ([a-z-]+)/u)?.[1];
            const taskCode = message.match(/\[code=([a-z0-9_-]+)\]/u)?.[1];
            const taskDetail = message.match(/detail=([a-z0-9-]+)/u)?.[1];
            const kind = taskOutcome !== undefined
              ? `task-${taskOutcome}${taskCode === undefined ? "" : `-${taskCode}`}${taskDetail === undefined ? "" : `-${taskDetail}`}`
              : message.includes("catalog is incomplete or unbound")
                ? "catalog-incomplete"
                : message.includes("catalog has no matching container")
                    ? "catalog-framework"
                    : message.includes("catalog does not satisfy")
                      ? "contract-container"
                      : message.match(/catalog contract containers missing \[([a-z,]+)\]/u) !== null
                        ? `contract-container-${message.match(/catalog contract containers missing \[([a-z,]+)\]/u)![1]}`
                      : message.includes("catalog is missing the")
                      ? "contract-case"
              : message.includes("workspace inspection")
              ? "workspace"
              : message.includes("discovery start")
                ? "start"
                : message.includes("discovery finished")
                  ? "task"
                  : message.includes("catalog read")
                    ? "catalog-read"
                : message.includes("artifact")
                      ? classifyDiscoveryArtifactError(message)
                      : classifyDiscoveryValidationError(message);
            markStage(`discover-catalog:${family}:${frameworkId}:${kind}`);
            throw error;
          }
          const toolchain = discovery.toolchain;
          if (typeof toolchain.compilerSha256 !== "string" || !/^[0-9a-f]{64}$/u.test(toolchain.compilerSha256)) throw new Error("verified Service compiler digest is required");
          if (compilerVersion !== undefined && (compilerVersion !== toolchain.version || compilerSha256 !== toolchain.compilerSha256)) throw new Error("Service compiler identity changed across frameworks");
          compilerVersion = toolchain.version;
          compilerSha256 = toolchain.compilerSha256;
          const dependency = locked.frameworks.find(({ id }) => id === frameworkId)!;
          markStage(`read-executable:${family}:${frameworkId}`);
          compiled = await readCompiledFrameworkExecutable(workRoot, options.platform, family, frameworkId);
          frameworks.push({
            frameworkId, dependencyVersion: dependency.version, dependencySha256: dependency.source.sha256,
            dependencyTreeSha256: identity.frameworkTreeSha256[frameworkId],
            catalogArtifactSha256: discovery.catalogArtifactSha256,
            stableIdDigest: stableFrameworkIdDigest(frameworkId, discovery.catalog, identity), timeoutMs: 120_000,
            evidence: {
              sourceArtifactSha256: identity.fixtures[frameworkId].sourceSha256,
              sourceLocationDigest: identity.fixtures[frameworkId].metadataSha256,
              executableArtifactSha256: compiled.sha256,
            },
            ...(frameworkId === "unity" ? { cMockProvenance: {
              revision: provenance.value.cmock.revision, generatorVersion: provenance.value.generator.version,
              inputSha256: provenance.value.input.sha256, outputSha256: provenance.outputSha256,
              manifestSha256: provenance.cMockProvenanceSha256, generatedAtRuntime: false,
            } } : {}),
          });
        } finally {
          try {
            await fixture.dispose();
          } catch (error) {
            const message = error instanceof Error ? error.message : String(error);
            const kind = message.includes("forced service process exit")
              ? "process-exit"
              : message.includes("fixture directory cleanup")
                ? "directory-cleanup"
                : "unknown";
            markStage(`dispose-service:${family}:${frameworkId}:${kind}`);
            throw new Error("framework Service cleanup failed");
          }
        }
        // Real disposal removes the Service directory, including its build tree.
        // Restore only the validated executable, never sessions, databases, or credentials.
        markStage(`restore-executable:${family}:${frameworkId}`);
        await validateOwnedFrameworkStage(stageRoot, ownershipId);
        await mkdir(staged.serviceDirectory, { mode: 0o700 });
        const executableDirectory = join(staged.buildRoot, compiled.profileId, "bin");
        await mkdir(executableDirectory, { recursive: true, mode: 0o700 });
        await writeFile(join(executableDirectory, `phase9_${frameworkId}${options.platform === "win32" ? ".exe" : ""}`), compiled.bytes, { flag: "wx", mode: 0o700 });
      }
      toolchains.push({ family, compilerVersion: compilerVersion!, compilerSha256: compilerSha256!, frameworks });
    }
    markStage("build-runtime-manifest");
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

function classifyDiscoveryValidationError(message: string): string {
  const value = message.toLowerCase();
  if (value.includes("source uri")) return "source-uri";
  if (value.includes("artifact")) return "artifact";
  if (value.includes("catalog")) return "catalog";
  if (value.includes("workspace")) return "workspace-validation";
  if (value.includes("toolchain") || value.includes("compiler")) return "toolchain";
  return "validation";
}

function classifyDiscoveryArtifactError(message: string): string {
  const value = message.toLowerCase();
  if (value.includes("unexpectedly paginated")) return "artifact-pagination";
  if (value.includes("exactly one")) return "artifact-count";
  if (value.includes("metadata is invalid")) return "artifact-metadata";
  if (value.includes("artifact bytes")) return "artifact-bytes";
  if (value.includes("artifact read")) return "artifact-read";
  return "artifact";
}

async function verifyInputs(options: FrameworkRuntimePrepareOptions, manifest: LinuxFrameworkInputManifest, identity: F1FrameworkIdentity, markStage: (stage: string) => void): Promise<PreparedRoots> {
  const root = resolve(options.repositoryRoot);
  markStage("verify-cmake-bundle");
  await verifyPreparedCMakeBundle(join(root, ".bundled-tools/cmake"), options.platform, "x64", { manifestPath: join(root, "tools/cmake-bundle/manifest.json") });
  markStage("verify-service-binary");
  const binary = await lstat(join(root, "build", options.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"));
  if (!binary.isFile() || binary.isSymbolicLink()) throw new Error("framework Service binary is unsafe");
  // Despite its historical name this validator accepts both locked manifest platforms.
  markStage("prepare-locked-framework-inputs");
  const boundary = await prepareLinuxFrameworkInputs({
    manifest, manifestSha256: identity.manifestSha256, repositoryRoot: root,
    cacheRoot: join(root, ".superpowers/cache/framework-bundle"),
    sourceRoot: join(root, ".superpowers/runtime/framework-bundle/v2", identity.manifestSha256),
    helperPath: join(root, "sdk/cmake/UnitTestIDE.cmake"),
    generatorPath: join(root, "build", options.platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"),
    markStage,
  });
  markStage("return-prepared-inputs");
  return { cpputest: boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT!, unity: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT!, cmock: boundary.environment.UNIT_TEST_IDE_TEST_CMOCK_ROOT! };
}

export function parseFrameworkPrepareArguments(arguments_: readonly string[]): { platform: FrameworkPlatform; candidateCommit: string } {
  if (arguments_.length !== 4 || arguments_[0] !== "--platform" || arguments_[2] !== "--candidate" ||
      (arguments_[1] !== "win32" && arguments_[1] !== "linux") || !/^[0-9a-f]{40}$/u.test(arguments_[3]!)) {
    throw new Error("framework prepare arguments are invalid");
  }
  return { platform: arguments_[1], candidateCommit: arguments_[3]! };
}

async function main(): Promise<void> {
  // pnpm's forwarding delimiter is removed only at the process boundary.
  const args = process.argv.slice(2);
  const options = parseFrameworkPrepareArguments(args[0] === "--" ? args.slice(1) : args);
  if (options.platform !== process.platform) throw new Error("framework prepare platform is unavailable");
  const repositoryRoot = resolve(import.meta.dirname, "../../..");
  const benchmark = await collectAuditedFrameworkBenchmark(repositoryRoot);
  const prepared = await prepareFrameworkRuntime({ repositoryRoot, ...options }, { benchmark });
  process.stdout.write(`${JSON.stringify(await publishFrameworkRuntime(prepared))}\n`);
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch(() => {
    process.stderr.write("framework runtime producer failed\n");
    process.exitCode = 1;
  });
}
