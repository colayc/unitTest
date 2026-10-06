import { createHash, randomBytes } from "node:crypto";
import { cp, mkdir, mkdtemp, readFile, rename, rm, writeFile } from "node:fs/promises";
import { join, posix, resolve, win32 } from "node:path";
import { pathToFileURL } from "node:url";
import {
  ProtocolError,
  TestSelectionModeV14,
  type CoverageRun,
  type ProtocolClient,
  type WorkspaceSnapshot,
} from "@unit-test-ide/test-client";
import { CoverageDriver } from "@unit-test-ide/protocol-models";

import { prepareLinuxFrameworkInputs, type LinuxFrameworkInputManifest } from "./linux-framework-inputs.js";
import { productionBundleRoots } from "./native-production-bundles.js";
import { startService } from "./probe.js";
import {
  executeNativeManagedGeneration,
  type NativeCoverageRefreshBinding,
  type NativeManagedGenerationReceipt,
} from "./test-generation-e2e.js";

type NativePlatform = "linux" | "win32";
type Framework = "cpputest" | "unity";

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const projectId = "classifier";
const coverageProfileId = "generation-coverage";
const operationTimeoutMs = 300_000;
const pollIntervalMs = 100;
const hex64 = /^[0-9a-f]{64}$/u;

export function parseNativeTestGenerationArguments(arguments_: readonly string[]): { platform: NativePlatform } {
  if (arguments_.length !== 2 || arguments_[0] !== "--platform" ||
      arguments_[1] !== "linux" && arguments_[1] !== "win32") {
    throw new Error("native test-generation arguments are invalid");
  }
  return { platform: arguments_[1] };
}

export function buildNativeGenerationWorkspaceConfig(
  framework: Framework,
  baseBuildProfileId: string,
  toolchain?: NativeGenerationToolchainConfig,
): unknown {
  if (framework !== "cpputest" && framework !== "unity") {
    throw new Error("native test-generation framework is invalid");
  }
  if (!hex64.test(baseBuildProfileId)) {
    throw new Error("native test-generation base profile is invalid");
  }
  const config: Record<string, unknown> = {
    version: 3,
    projects: [{
      id: projectId,
      sourceDir: ".",
      fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
      tests: { containers: [{ ctestName: "classifier-tests", framework }] },
    }],
    coverageProfiles: [{
      id: coverageProfileId,
      baseBuildProfileId,
      include: ["src/**"],
      exclude: ["tests/**", ".unit-test-ide/**"],
    }],
  };
  if (toolchain !== undefined) config.toolchains = [toolchain];
  return config;
}

export interface NativeGenerationToolchainConfig {
  readonly id: string;
  readonly family: "clang" | "clang-cl";
  readonly cCompiler: string;
  readonly cppCompiler: string;
}

export function buildNativeGenerationToolchainConfig(
  platform: NativePlatform,
  bundleRoot: string,
): NativeGenerationToolchainConfig {
  const pathApi = platform === "linux" ? posix : win32;
  if (bundleRoot.length === 0 || !pathApi.isAbsolute(bundleRoot)) {
    throw new Error("native generation LLVM bundle root must be absolute");
  }
  if (platform === "linux") {
    return {
      id: "approved-clang-llvm",
      family: "clang",
      cCompiler: pathApi.join(bundleRoot, "bin", "clang"),
      cppCompiler: pathApi.join(bundleRoot, "bin", "clang++"),
    };
  }
  return {
    id: "approved-clang-cl-llvm",
    family: "clang-cl",
    cCompiler: pathApi.join(bundleRoot, "bin", "clang-cl.exe"),
    cppCompiler: pathApi.join(bundleRoot, "bin", "clang-cl.exe"),
  };
}

export function buildNativeGenerationBootstrapConfig(
  framework: Framework,
  toolchain?: NativeGenerationToolchainConfig,
): unknown {
  if (framework !== "cpputest" && framework !== "unity") {
    throw new Error("native test-generation framework is invalid");
  }
  const config: Record<string, unknown> = {
    version: 2,
    projects: [{
      id: projectId,
      sourceDir: ".",
      fallback: { configurations: ["Debug"], preferredGenerator: "Ninja" },
      tests: { containers: [{ ctestName: "classifier-tests", framework }] },
    }],
  };
  if (toolchain !== undefined) config.toolchains = [toolchain];
  return config;
}

export interface SelectedNativeGenerationProfile {
  readonly snapshot: WorkspaceSnapshot;
  readonly profile: WorkspaceSnapshot["projects"][number]["buildProfiles"][number];
  readonly toolchain: WorkspaceSnapshot["toolchains"][number];
}

export function selectNativeGenerationProfile(
  snapshot: WorkspaceSnapshot,
  platform: NativePlatform,
): SelectedNativeGenerationProfile {
  const family = platform === "win32" ? "clang-cl" : "clang";
  const project = snapshot.projects.find((candidate) => candidate.projectId === projectId);
  const candidates = snapshot.toolchains
    .filter((toolchain) => toolchain.family === family &&
      toolchain.hostArchitecture === "x64" && toolchain.targetArchitecture === "x64" &&
      toolchain.capabilities.coverageDrivers.includes(CoverageDriver.LlvmCov))
    .sort((left, right) => left.toolchainId.localeCompare(right.toolchainId, "en"));
  for (const toolchain of candidates) {
    const profile = project?.buildProfiles.find((candidate) =>
      candidate.toolchainId === toolchain.toolchainId && candidate.origin === "generated" &&
      candidate.generator === "Ninja" && candidate.configuration === "Debug");
    if (profile !== undefined) return { snapshot, profile, toolchain };
  }
  throw new Error(`native test-generation ${family} LLVM profile is unavailable`);
}

interface CoverageFilePageLike {
  readonly nextCursor?: string;
  readonly items: ReadonlyArray<{ readonly fileId: string; readonly relativePath: string; readonly status: string }>;
}

interface CoverageFunctionPageLike {
  readonly nextCursor?: string;
  readonly items: ReadonlyArray<{
    readonly functionId: string;
    readonly fileId: string;
    readonly qualifiedName: string;
    readonly status: string;
  }>;
}

export function selectNativeGenerationTarget(
  files: CoverageFilePageLike,
  functions: CoverageFunctionPageLike,
  relativePath: string,
): { fileId: string; functionId: string } {
  const matchingFiles = files.items.filter((file) => file.relativePath === relativePath && file.status === "current");
  const file = matchingFiles.length === 1 && files.nextCursor === undefined ? matchingFiles[0] : undefined;
  const matchingFunctions = file === undefined ? [] : functions.items.filter((fn) =>
    fn.fileId === file.fileId && fn.qualifiedName.length > 0 && fn.status === "current");
  const fn = matchingFunctions.length === 1 && functions.items.length === 1 && functions.nextCursor === undefined
    ? matchingFunctions[0]
    : undefined;
  if (file === undefined || fn === undefined || !hex64.test(file.fileId) || !hex64.test(fn.functionId)) {
    throw new Error("native test-generation coverage target is unavailable");
  }
  return { fileId: file.fileId, functionId: fn.functionId };
}

interface CoverageCycleResult extends NativeCoverageRefreshBinding {
  readonly profileId: string;
}

interface FrameworkBoundary {
  readonly cpputestRoot: string;
  readonly unityRoot: string;
}

async function prepareFrameworkBoundary(root: string, platform: NativePlatform): Promise<FrameworkBoundary> {
  const manifestBytes = await readFile(join(root, "tools/framework-bundle/manifest.json"));
  const manifestSha256 = createHash("sha256").update(manifestBytes).digest("hex");
  const boundary = await prepareLinuxFrameworkInputs({
    manifest: JSON.parse(manifestBytes.toString("utf8")) as LinuxFrameworkInputManifest,
    manifestSha256,
    repositoryRoot: root,
    cacheRoot: join(root, ".superpowers/cache/framework-bundle"),
    sourceRoot: join(root, ".superpowers/runtime/framework-bundle/v2", manifestSha256),
    helperPath: join(root, "sdk/cmake/UnitTestIDE.cmake"),
    generatorPath: join(root, "build", platform === "win32" ? "unity-runner-generator.exe" : "unity-runner-generator"),
  });
  return {
    cpputestRoot: boundary.environment.UNIT_TEST_IDE_TEST_CPPUTEST_ROOT!,
    unityRoot: boundary.environment.UNIT_TEST_IDE_TEST_UNITY_ROOT!,
  };
}

async function selectProfileEventually(
  client: ProtocolClient,
  platform: NativePlatform,
  previousGeneration?: string,
): Promise<SelectedNativeGenerationProfile> {
  const deadline = Date.now() + 120_000;
  let lastError: unknown;
  for (;;) {
    try {
      const selected = selectNativeGenerationProfile(await client.inspectWorkspace(), platform);
      if (previousGeneration === undefined || selected.snapshot.workspaceGeneration !== previousGeneration) return selected;
      lastError = new Error("workspace generation has not advanced");
    } catch (error) {
      lastError = error;
    }
    if (Date.now() >= deadline) {
      throw new Error(`native test-generation profile discovery timed out: ${lastError instanceof Error ? lastError.message : String(lastError)}`);
    }
    await delay(250);
  }
}

async function waitForTask(client: ProtocolClient, taskId: string, label: string): Promise<void> {
  const deadline = Date.now() + operationTimeoutMs;
  for (;;) {
    const task = await client.getTask(taskId);
    if (task.status === "finished") {
      if (task.outcome !== "succeeded") {
        throw new Error(`${label} failed with ${String(task.outcome)} (${String(task.errorCode ?? "no-code")})`);
      }
      return;
    }
    if (Date.now() >= deadline) throw new Error(`${label} timed out`);
    await delay(pollIntervalMs);
  }
}

async function waitForCoverage(client: ProtocolClient, coverageRunId: string): Promise<CoverageRun> {
  const deadline = Date.now() + operationTimeoutMs;
  for (;;) {
    const run = await client.getCoverageRun(coverageRunId);
    if (run.status === "finished") {
      if (run.outcome !== "available" || run.reportId === undefined) {
        throw new Error(`native test-generation coverage is ${String(run.outcome)} (${String(run.reason ?? "no-reason")})`);
      }
      return run;
    }
    if (Date.now() >= deadline) throw new Error("native test-generation coverage timed out");
    await delay(pollIntervalMs);
  }
}

async function runCoverageCycle(
  client: ProtocolClient,
  platform: NativePlatform,
  relativePath: string,
): Promise<CoverageCycleResult> {
  for (let attempt = 0; attempt < 2; attempt++) {
    const selected = await selectProfileEventually(client, platform);
    try {
      const build = await client.startCMakeBuild({
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: selected.snapshot.workspaceGeneration,
        projectId,
        buildProfileId: selected.profile.buildProfileId,
        targetIds: [],
        jobs: 2,
        timeoutMs: operationTimeoutMs,
      });
      await waitForTask(client, build.taskId, "native test-generation build");
      const current = await selectProfileEventually(client, platform);
      const discovery = await client.discoverTests({
        idempotencyKey: randomBytes(16).toString("hex"),
        projectId,
        profileId: current.profile.buildProfileId,
      });
      await waitForTask(client, discovery.taskId, "native test-generation discovery");
      const rebound = await selectProfileEventually(client, platform);
      const catalog = await client.getTestCatalog({ projectId, profileId: rebound.profile.buildProfileId, limit: 200 });
      if (catalog.partial || catalog.nextCursor !== undefined || catalog.items.filter((item) => item.kind === "case").length < 1) {
        throw new Error("native test-generation catalog is incomplete");
      }
      const started = await client.startCoverage({
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: rebound.snapshot.workspaceGeneration,
        projectId,
        coverageProfileId,
        catalogRevision: catalog.revision,
        selection: { mode: TestSelectionModeV14.All },
        repeatCount: 1,
        timeoutMs: operationTimeoutMs,
      });
      const finished = await waitForCoverage(client, started.coverageRunId);
      const reportId = finished.reportId!;
      const project = await client.getCoverageProject({
        workspaceGeneration: finished.workspaceGeneration,
        coverageReportId: reportId,
        projectId,
      });
      if (project.status !== "current") throw new Error("native test-generation coverage project is stale");
      const files = await client.listCoverageFiles({
        workspaceGeneration: finished.workspaceGeneration,
        coverageReportId: reportId,
        projectId,
        limit: 200,
      });
      const exactFile = files.items.find((item) => item.relativePath === relativePath);
      if (exactFile === undefined) throw new Error("native test-generation coverage source file is missing");
      const functions = await client.listCoverageFunctions({
        workspaceGeneration: finished.workspaceGeneration,
        coverageReportId: reportId,
        fileId: exactFile.fileId,
        limit: 200,
      });
      const target = selectNativeGenerationTarget(files, functions, relativePath);
      return {
        workspaceGeneration: finished.workspaceGeneration,
        coverageReportId: reportId,
        ...target,
        profileId: rebound.profile.buildProfileId,
      };
    } catch (error) {
      if (!(error instanceof ProtocolError) || error.code !== "WORKSPACE_CHANGED" || attempt > 0) throw error;
    }
  }
  throw new Error("native test-generation coverage cycle exhausted retries");
}

async function runFramework(
  root: string,
  platform: NativePlatform,
  framework: Framework,
  boundary: FrameworkBoundary,
  workRoot: string,
): Promise<NativeManagedGenerationReceipt> {
  const workspaceRoot = join(workRoot, framework, "workspace");
  const serviceDirectory = join(workRoot, framework, "service");
  await mkdir(serviceDirectory, { recursive: true });
  await cp(join(root, "tools/service-probe/fixtures/test-generation", framework), workspaceRoot, {
    recursive: true,
    force: false,
    errorOnExist: true,
  });
  const llvmBundleRoot = platform === "linux" ? process.env.UTIDE_NATIVE_LLVM_BUNDLE : undefined;
  const toolchain = llvmBundleRoot === undefined
    ? undefined
    : buildNativeGenerationToolchainConfig(platform, llvmBundleRoot);
  if (toolchain !== undefined) {
    await writeFile(
      join(workspaceRoot, ".unit-test-ide/workspace.json"),
      `${JSON.stringify(buildNativeGenerationBootstrapConfig(framework, toolchain), null, 2)}\n`,
      "utf8",
    );
  }
  const frameworkRoot = framework === "cpputest" ? boundary.cpputestRoot : boundary.unityRoot;
  await mkdir(join(workspaceRoot, ".unit-test-ide/inputs"), { recursive: true });
  await cp(frameworkRoot, join(workspaceRoot, ".unit-test-ide/inputs", framework), {
    recursive: true,
    force: false,
    errorOnExist: true,
  });
  const serviceBinary = join(root, "build", platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
  const fixture = await startService(serviceBinary, serviceDirectory, {
    workspaceRoot,
    trustedWorkspace: true,
    timeoutMs: operationTimeoutMs,
    startupTimeoutMs: 120_000,
    handshakeSupportedProtocolVersions: ["1.6", "1.5", "1.4", "1.3", "1.2", "1.1", "1.0"],
    ...productionBundleRoots(root, platform),
  });
  try {
    const initial = await selectProfileEventually(fixture.client, platform);
    const config = buildNativeGenerationWorkspaceConfig(framework, initial.profile.buildProfileId, toolchain);
    await writeFile(join(workspaceRoot, ".unit-test-ide/workspace.json"), `${JSON.stringify(config, null, 2)}\n`, "utf8");
    await selectProfileEventually(fixture.client, platform, initial.snapshot.workspaceGeneration);
    const relativePath = framework === "cpputest" ? "src/classifier.cpp" : "src/classifier.c";
    const baseline = await runCoverageCycle(fixture.client, platform, relativePath);
    return await executeNativeManagedGeneration({
      client: fixture.client,
      projectId,
      workspaceGeneration: baseline.workspaceGeneration,
      coverageReportId: baseline.coverageReportId,
      fileId: baseline.fileId,
      functionId: baseline.functionId,
      framework,
      maxPollAttempts: 600,
      pollIntervalMs: 500,
      refreshCoverage: async () => runCoverageCycle(fixture.client, platform, relativePath),
    });
  } finally {
    await fixture.dispose();
  }
}

export async function runNativeTestGenerationAcceptance(
  root: string,
  platform: NativePlatform,
): Promise<readonly { framework: Framework; receipt: NativeManagedGenerationReceipt }[]> {
  if (platform !== process.platform) throw new Error(`native test-generation ${platform} must run on ${process.platform}`);
  const boundary = await prepareFrameworkBoundary(root, platform);
  const workParent = join(root, ".native-e2e/test-generation-work");
  await mkdir(workParent, { recursive: true });
  const workRoot = await mkdtemp(join(workParent, `${platform}-`));
  try {
    const results: Array<{ framework: Framework; receipt: NativeManagedGenerationReceipt }> = [];
    for (const framework of ["cpputest", "unity"] as const) {
      process.stderr.write(`native-test-generation: ${platform} ${framework}\n`);
      results.push({ framework, receipt: await runFramework(root, platform, framework, boundary, workRoot) });
    }
    return Object.freeze(results.map((result) => Object.freeze(result)));
  } finally {
    await rm(workRoot, { recursive: true, force: true });
  }
}

async function publishReport(
  root: string,
  platform: NativePlatform,
  candidateCommit: string,
  results: readonly { framework: Framework; receipt: NativeManagedGenerationReceipt }[],
): Promise<void> {
  if (!/^[0-9a-f]{40}$/u.test(candidateCommit)) throw new Error("native test-generation candidate commit is invalid");
  const directory = join(root, ".native-e2e/artifacts", platform === "win32" ? "windows" : "linux");
  await mkdir(directory, { recursive: true });
  const path = join(directory, "test-generation-report.json");
  const temporary = `${path}.tmp-${randomBytes(8).toString("hex")}`;
  await writeFile(temporary, `${JSON.stringify({
    schemaVersion: 1,
    candidateCommit,
    platform: platform === "win32" ? "windows-x64" : "linux-x64",
    frameworks: results,
  })}\n`, { flag: "wx" });
  await rename(temporary, path);
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  const { platform } = parseNativeTestGenerationArguments(args[0] === "--" ? args.slice(1) : args);
  const candidateCommit = process.env.GITHUB_SHA ?? "";
  if (!/^[0-9a-f]{40}$/u.test(candidateCommit)) throw new Error("native test-generation candidate commit is invalid");
  const results = await runNativeTestGenerationAcceptance(repositoryRoot, platform);
  await publishReport(repositoryRoot, platform, candidateCommit, results);
  process.stdout.write(`${JSON.stringify({ platform, frameworks: results.map(({ framework }) => framework) })}\n`);
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main().catch((error: unknown) => {
    process.stderr.write(`native-test-generation: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
