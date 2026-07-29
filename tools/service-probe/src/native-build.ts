import { execFile as execFileCallback } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createReadStream } from "node:fs";
import {
  appendFile,
  lstat,
  mkdir,
  mkdtemp,
  readFile,
  realpath,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { arch as hostArchitecture, tmpdir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { promisify } from "node:util";
import {
  Generator,
  type BuildProfileElement,
  type Diagnostic,
  type ToolchainElement,
  type WorkspaceSnapshot,
} from "@unit-test-ide/protocol-models";
import {
  ProtocolError,
  type EventSubscription,
  type ProtocolClient,
  type ProtocolTaskEvent,
  type ProtocolTaskSnapshot,
} from "@unit-test-ide/test-client";
import {
  copyNativeFixture,
  normalizeNativeDiagnostic,
  type GoldenDiagnosticExpectation,
  type NativeFixtureName,
} from "./native-fixture.js";
import {
  startService,
  withNamedTimeout,
  type StartServiceOptions,
  type TaskServiceFixture,
} from "./probe.js";
import { writeNativeToolchainReport } from "./native-report.js";

const execFile = promisify(execFileCallback);
const repositoryRoot = resolve(import.meta.dirname, "../../..");
const nativeTimeoutMs = 120_000;
const nativeEventHeartbeatMs = 5_000;
const requiredEnvironmentName = "UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS";
const families = ["gcc", "clang", "msvc", "clang-cl"] as const;
const platformFamilies: Readonly<Record<"linux" | "win32", readonly RequiredToolchainFamily[]>> = {
  linux: ["gcc", "clang"],
  win32: ["msvc", "clang-cl"],
};

export type RequiredToolchainFamily = typeof families[number];

export interface NativeScenarioResult {
  platform: NodeJS.Platform;
  toolchainFamily: RequiredToolchainFamily;
  toolchainVersion: string;
  generator: string;
  cmakeVersion: string;
  scenarios: Record<string, "passed" | "skipped">;
}

export interface PreparedCMakeBundle {
  bundleRoot: string;
  installRoot: string;
  executable: string;
  key: "linux-x64" | "win32-x64";
  cmakeVersion: string;
  archiveSha256: string;
}

export interface NativeMatrixOptions {
  platform: NodeJS.Platform;
  requiredFamilies: readonly RequiredToolchainFamily[];
  artifactDirectory: string;
  workDirectory?: string;
}

interface FamilyWorkspace {
  root: string;
  workspaceRoot: string;
  serviceDirectory: string;
}

interface SelectedProfile {
  snapshot: WorkspaceSnapshot;
  projectId: string;
  profile: BuildProfileElement;
  toolchain: ToolchainElement;
}

interface FamilyExecutionContext extends SelectedProfile {
  family: RequiredToolchainFamily;
  fixture: TaskServiceFixture;
  workspaceRoot: string;
  serviceBinary: string;
  bundle: PreparedCMakeBundle;
}

interface NativeMatrixDependencies {
  environment: NodeJS.ProcessEnv;
  architecture: string;
  repositoryRoot: string;
  verifyBundle: (
    root: string,
    platform: NodeJS.Platform,
    architecture: string,
  ) => Promise<PreparedCMakeBundle>;
  createWorkspace: (
    root: string,
    platform: NodeJS.Platform,
    family: RequiredToolchainFamily,
  ) => Promise<FamilyWorkspace>;
  launchService: (
    serviceBinary: string,
    directory: string,
    options: StartServiceOptions,
  ) => Promise<TaskServiceFixture>;
  executeScenarios: (context: FamilyExecutionContext) => Promise<Record<string, "passed">>;
  cleanupWorkspace: (workspace: FamilyWorkspace) => Promise<void>;
  writeReport: typeof writeNativeToolchainReport;
}

const defaultDependencies: NativeMatrixDependencies = {
  environment: process.env,
  architecture: hostArchitecture(),
  repositoryRoot,
  verifyBundle: verifyPreparedCMakeBundle,
  createWorkspace: createFamilyWorkspace,
  launchService: startService,
  executeScenarios: executeCoreScenarios,
  cleanupWorkspace: (workspace) => rm(workspace.root, { recursive: true, force: true }),
  writeReport: writeNativeToolchainReport,
};

export async function runNativeMatrix(
  options: NativeMatrixOptions,
): Promise<readonly NativeScenarioResult[]> {
  return runNativeMatrixWithDependencies(options, defaultDependencies);
}

async function runNativeMatrixWithDependencies(
  options: NativeMatrixOptions,
  dependencies: NativeMatrixDependencies,
): Promise<readonly NativeScenarioResult[]> {
  validateMatrixOptions(options, dependencies.architecture);
  const enforced = parseRequiredToolchains(dependencies.environment[requiredEnvironmentName]);
  for (const family of enforced) {
    if (!options.requiredFamilies.includes(family)) {
      throw new Error(`required native toolchain ${family} is outside the requested matrix`);
    }
  }

  const bundleRoot = join(dependencies.repositoryRoot, ".bundled-tools", "cmake");
  const bundle = await dependencies.verifyBundle(
    bundleRoot,
    options.platform,
    dependencies.architecture,
  );
  const serviceBinary = join(
    dependencies.repositoryRoot,
    "build",
    options.platform === "win32" ? "unit-test-service.exe" : "unit-test-service",
  );
  await requireDirectFile(serviceBinary, "native Service binary");

  const results: NativeScenarioResult[] = [];
  const workDirectory = options.workDirectory ??
    join(tmpdir(), "uti-native");
  for (const family of options.requiredFamilies) {
    const workspace = await dependencies.createWorkspace(
      workDirectory,
      options.platform,
      family,
    );
    let fixture: TaskServiceFixture | undefined;
    try {
      fixture = await dependencies.launchService(serviceBinary, workspace.serviceDirectory, {
        timeoutMs: nativeTimeoutMs,
        workspaceRoot: workspace.workspaceRoot,
        trustedWorkspace: true,
        cmakeBundleRoot: bundle.bundleRoot,
      });
      const snapshot = await withNamedTimeout(
        `${family} workspace inspection`,
        fixture.client.inspectWorkspace(),
        nativeTimeoutMs,
      );
      const selected = selectGeneratedProfile(snapshot, family);
      if (selected === undefined) {
        if (enforced.has(family)) {
          const codes = [...new Set(snapshot.diagnostics.map((diagnostic) => diagnostic.code))];
          throw new Error(
            `required native toolchain ${family} was not discovered` +
            (codes.length === 0 ? "" : `; diagnostics=${codes.join(",")}`),
          );
        }
        results.push(skippedResult(options.platform, family, bundle.cmakeVersion));
        continue;
      }
      const scenarios = await dependencies.executeScenarios({
        ...selected,
        family,
        fixture,
        workspaceRoot: workspace.workspaceRoot,
        serviceBinary,
        bundle,
      });
      results.push({
        platform: options.platform,
        toolchainFamily: family,
        toolchainVersion: selected.toolchain.version,
        generator: selected.profile.generator,
        cmakeVersion: bundle.cmakeVersion,
        scenarios,
      });
    } finally {
      await fixture?.dispose();
      await dependencies.cleanupWorkspace(workspace);
    }
  }
  await dependencies.writeReport(
    options.artifactDirectory,
    options.platform,
    dependencies.architecture,
    bundle,
    results,
  );
  return results;
}

function validateMatrixOptions(options: NativeMatrixOptions, architecture: string): void {
  if (options.platform !== "linux" && options.platform !== "win32") {
    throw new Error(`unsupported native E2E platform: ${options.platform}`);
  }
  if (architecture !== "x64") {
    throw new Error(`unsupported native E2E architecture: ${architecture}`);
  }
  if (!isAbsolute(options.artifactDirectory) || options.artifactDirectory.includes("\0")) {
    throw new Error("native artifact directory must be an absolute path");
  }
  if (
    options.requiredFamilies.length === 0 ||
    new Set(options.requiredFamilies).size !== options.requiredFamilies.length
  ) {
    throw new Error("native matrix families must be non-empty and unique");
  }
  const allowed = platformFamilies[options.platform];
  for (const family of options.requiredFamilies) {
    if (!allowed.includes(family)) {
      throw new Error(`toolchain family ${family} is incompatible with ${options.platform}`);
    }
  }
}

export function parseRequiredToolchains(value: string | undefined): ReadonlySet<RequiredToolchainFamily> {
  if (value === undefined || value.trim() === "") {
    return new Set();
  }
  const result = new Set<RequiredToolchainFamily>();
  for (const raw of value.split(",")) {
    const family = raw.trim();
    if (!(families as readonly string[]).includes(family)) {
      throw new Error(`invalid required native toolchain family: ${family}`);
    }
    if (result.has(family as RequiredToolchainFamily)) {
      throw new Error(`duplicate required native toolchain family: ${family}`);
    }
    result.add(family as RequiredToolchainFamily);
  }
  return result;
}

function selectGeneratedProfile(
  snapshot: WorkspaceSnapshot,
  family: RequiredToolchainFamily,
): SelectedProfile | undefined {
  const matches = snapshot.toolchains.filter((toolchain) => toolchain.family === family);
  for (const toolchain of matches) {
    for (const project of snapshot.projects) {
      const profile = project.buildProfiles.find((candidate) =>
        candidate.origin === "generated" &&
        candidate.toolchainId === toolchain.toolchainId
      );
      if (profile !== undefined) {
        return { snapshot, projectId: project.projectId, profile, toolchain };
      }
    }
  }
  return undefined;
}

function skippedResult(
  platform: NodeJS.Platform,
  family: RequiredToolchainFamily,
  cmakeVersion: string,
): NativeScenarioResult {
  return {
    platform,
    toolchainFamily: family,
    toolchainVersion: "unavailable",
    generator: "unavailable",
    cmakeVersion,
    scenarios: { discovery: "skipped" },
  };
}

async function createFamilyWorkspace(
  workDirectory: string,
  platform: NodeJS.Platform,
  family: RequiredToolchainFamily,
): Promise<FamilyWorkspace> {
  const platformSegment = platform === "win32" ? "w" : "l";
  const familySegment: Readonly<Record<RequiredToolchainFamily, string>> = {
    gcc: "g",
    clang: "c",
    msvc: "m",
    "clang-cl": "cc",
  };
  const workRoot = join(workDirectory, platformSegment);
  await mkdir(workRoot, { recursive: true, mode: 0o700 });
  const familyRoot = await mkdtemp(join(workRoot, `${familySegment[family]}-`));
  try {
    const workspaceRoot = await copyNativeFixture(
      "fallback-project",
      familyRoot,
      "workspace 空格-测试",
    );
    const serviceDirectory = join(familyRoot, "s");
    await mkdir(serviceDirectory, { mode: 0o700 });
    await writeWorkspaceConfig(workspaceRoot);
    return { root: familyRoot, workspaceRoot, serviceDirectory };
  } catch (error) {
    await rm(familyRoot, { recursive: true, force: true });
    throw error;
  }
}

async function writeWorkspaceConfig(workspaceRoot: string): Promise<void> {
  await mkdir(join(workspaceRoot, ".unit-test-ide"), { mode: 0o700 });
  await writeFile(
    join(workspaceRoot, ".unit-test-ide", "workspace.json"),
    `${JSON.stringify({
      version: 1,
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { configurations: ["Debug"] },
      }],
    }, null, 2)}\n`,
    { flag: "wx", mode: 0o600 },
  );
}

async function overwriteWorkspaceConfig(
  workspaceRoot: string,
  sourceDir: string,
): Promise<void> {
  await writeFile(
    join(workspaceRoot, ".unit-test-ide", "workspace.json"),
    `${JSON.stringify({
      version: 1,
      projects: [{
        id: "root",
        sourceDir,
        fallback: { configurations: ["Debug"] },
      }],
    }, null, 2)}\n`,
  );
}

async function executeCoreScenarios(
  context: FamilyExecutionContext,
): Promise<Record<string, "passed">> {
  const client = context.fixture.client;
  reportNativeScenario(context.family, "default-build");
  const subscription = await withNamedTimeout(
    `${context.family} native event subscription`,
    client.subscribeEvents(0),
    nativeTimeoutMs,
  );
  const first = await startFamilyBuildAtCheckpoint(
    client,
    context.family,
    "default-build",
    [],
    60_000,
  );
  const firstEvents = await waitForTask(client, subscription, first.taskId);
  assertStepOrder(firstEvents, ["configure", "build"], "first native build");

  reportNativeScenario(context.family, "secondary-target");
  const second = await startNamedTargetBuildAtCheckpoint(
    client,
    context.family,
    "secondary-target",
    "secondary_app",
    60_000,
  );
  const secondEvents = await waitForTask(client, subscription, second.taskId);
  assertStepOrder(secondEvents, ["build"], "unchanged native build");

  reportNativeScenario(context.family, "configure-invalidation");
  await appendFile(
    join(context.workspaceRoot, "CMakeLists.txt"),
    "\n# Native E2E configure invalidation\n",
    "utf8",
  );
  const third = await startFamilyBuildAtCheckpoint(
    client,
    context.family,
    "configure-invalidation",
    [],
    60_000,
  );
  const thirdEvents = await waitForTask(client, subscription, third.taskId);
  assertStepOrder(thirdEvents, ["configure", "build"], "changed CMake input build");

  reportNativeScenario(context.family, "typed-rejections");
  await withEstablishedFamilyCheckpoint(
    client,
    context.family,
    "unknown-target-rejected",
    (selected) =>
      expectProtocolError(
        () => startBuild(client, selected, ["f".repeat(64)], 60_000),
        "TARGET_NOT_FOUND",
      ),
  );
  await withEstablishedFamilyCheckpoint(
    client,
    context.family,
    "stale-generation-rejected",
    (selected) =>
      expectProtocolError(
        () => client.startCMakeBuild({
          idempotencyKey: randomBytes(16).toString("hex"),
          workspaceGeneration: "0".repeat(64),
          projectId: selected.projectId,
          buildProfileId: selected.profile.buildProfileId,
          targetIds: [],
          jobs: 2,
          timeoutMs: 60_000,
        }),
        "WORKSPACE_CHANGED",
      ),
  );

  reportNativeScenario(context.family, "cancellation-reconnect");
  const cancellable = await startNamedTargetBuildAtCheckpoint(
    client,
    context.family,
    "cancellation-reconnect",
    "slow_target",
    60_000,
  );
  const cancellationEvents = await waitForStep(
    subscription,
    cancellable.taskId,
    "build",
  );
  await withNamedTimeout(
    `${context.family} native sequence reconnect`,
    client.reconnect(),
    nativeTimeoutMs,
  );
  await withNamedTimeout(
    `${context.family} native cancellation`,
    client.cancelTask(cancellable.taskId),
    nativeTimeoutMs,
  );
  const cancelledEvents = await waitForTask(
    client,
    subscription,
    cancellable.taskId,
    "cancelled",
    cancellationEvents,
  );
  assertContinuousSequences(cancelledEvents, `${context.family} cancellation reconnect`);

  reportNativeScenario(context.family, "timeout");
  const timed = await startNamedTargetBuildAtCheckpoint(
    client,
    context.family,
    "timeout",
    "slow_target",
    1_000,
  );
  await waitForTask(client, subscription, timed.taskId, "timed_out");

  reportNativeScenario(context.family, "preset-build");
  await runPresetBuildScenario(context);

  const compilerGolden = context.family === "msvc" || context.family === "clang-cl"
    ? "compiler-msvc-clang-cl.json"
    : "compiler-gcc-clang.json";
  const linkerGolden = context.family === "msvc" || context.family === "clang-cl"
    ? "linker-msvc-clang-cl.json"
    : "linker-gcc-clang.json";
  reportNativeScenario(context.family, "compiler-diagnostic");
  await runFailureScenario(context, "compiler-failure", compilerGolden);
  reportNativeScenario(context.family, "linker-diagnostic");
  await runFailureScenario(context, "linker-failure", linkerGolden);
  reportNativeScenario(context.family, "configure-diagnostic");
  await runFailureScenario(context, "configure-failure", "configure.json");
  reportNativeScenario(context.family, "workspace-boundaries");
  await runWorkspaceRejectionScenario(
    context,
    "parent-traversal",
    async (workspaceRoot) => overwriteWorkspaceConfig(workspaceRoot, "../outside"),
    "WORKSPACE_INVALID_CONFIG",
  );
  await runWorkspaceRejectionScenario(
    context,
    "external-preset-include",
    async (workspaceRoot, parent) => {
      await writeFile(join(parent, "outside-presets.json"), "{}\n", { flag: "wx" });
      const presetPath = join(workspaceRoot, "CMakePresets.json");
      const presets = JSON.parse(await readFile(presetPath, "utf8")) as Record<string, unknown>;
      presets.include = ["../outside-presets.json"];
      await writeFile(presetPath, `${JSON.stringify(presets, null, 2)}\n`);
    },
    "CMAKE_PRESET_INVALID",
    "preset-project",
  );
  if (context.bundle.key === "linux-x64") {
    await runWorkspaceRejectionScenario(
      context,
      "symlink-escape",
      async (workspaceRoot, parent) => {
        const outside = join(parent, `outside-project-${randomBytes(4).toString("hex")}`);
        await mkdir(outside, { mode: 0o700 });
        await writeFile(
          join(outside, "CMakeLists.txt"),
          "cmake_minimum_required(VERSION 3.31)\nproject(outside LANGUAGES CXX)\n",
          { flag: "wx" },
        );
        await symlink(outside, join(workspaceRoot, "linked-project"), "dir");
        await overwriteWorkspaceConfig(workspaceRoot, "linked-project");
      },
      "WORKSPACE_INVALID_CONFIG",
    );
  }

  reportNativeScenario(context.family, "service-recovery");
  const recoveryReady = await startFamilyBuildAtCheckpoint(
    context.fixture.client,
    context.family,
    "service-recovery-ready",
    [],
    60_000,
  );
  await waitForTask(
    context.fixture.client,
    subscription,
    recoveryReady.taskId,
  );
  const recoverable = await startNamedTargetBuildAtCheckpoint(
    context.fixture.client,
    context.family,
    "service-recovery-interruption",
    "slow_target",
    60_000,
  );
  await waitForStep(subscription, recoverable.taskId, "build");
  await context.fixture.kill();
  await context.fixture.restart();
  const recovered = await withNamedTimeout(
    `${context.family} interrupted task recovery`,
    context.fixture.client.getTask(recoverable.taskId),
    nativeTimeoutMs,
  );
  if (recovered.status !== "finished" || recovered.outcome !== "interrupted") {
    throw new Error(
      `${context.family} recovered task = ${recovered.status}/${String(recovered.outcome)}`,
    );
  }
  const restartedSnapshot = await withNamedTimeout(
    `${context.family} restarted workspace inspection`,
    context.fixture.client.inspectWorkspace(),
    nativeTimeoutMs,
  );
  if (
    restartedSnapshot.workspaceGeneration !==
      recoverable.selected.snapshot.workspaceGeneration
  ) {
    throw new Error(
      `${context.family} workspace generation changed across restart: ` +
      `${recoverable.selected.snapshot.workspaceGeneration} -> ` +
      `${restartedSnapshot.workspaceGeneration}`,
    );
  }
  return {
    "default-build": "passed",
    "secondary-target": "passed",
    "configure-reuse": "passed",
    "configure-invalidation": "passed",
    "unknown-target-rejected": "passed",
    "stale-generation-rejected": "passed",
    cancellation: "passed",
    timeout: "passed",
    "preset-build": "passed",
    "compiler-diagnostic": "passed",
    "linker-diagnostic": "passed",
    "configure-diagnostic": "passed",
    "parent-traversal-rejected": "passed",
    "external-preset-include-rejected": "passed",
    ...(context.bundle.key === "linux-x64"
      ? { "symlink-escape-rejected": "passed" as const }
      : {}),
    "service-recovery": "passed",
  };
}

function reportNativeScenario(
  family: RequiredToolchainFamily,
  scenario: string,
): void {
  process.stderr.write(`native-e2e: ${family} ${scenario}\n`);
}

async function runPresetBuildScenario(
  context: FamilyExecutionContext,
): Promise<void> {
  const parent = dirname(context.workspaceRoot);
  const suffix = randomBytes(6).toString("hex");
  const workspaceRoot = await copyNativeFixture(
    "preset-project",
    parent,
    `preset-build-${suffix}`,
  );
  const serviceDirectory = join(parent, `p-${suffix}`);
  await mkdir(serviceDirectory, { mode: 0o700 });
  let fixture: TaskServiceFixture | undefined;
  try {
    const expectedCompiler = await configurePresetFixture(
      workspaceRoot,
      context.family,
      context.snapshot,
    );
    await writeWorkspaceConfig(workspaceRoot);
    fixture = await startService(context.serviceBinary, serviceDirectory, {
      timeoutMs: nativeTimeoutMs,
      workspaceRoot,
      trustedWorkspace: true,
      cmakeBundleRoot: context.bundle.bundleRoot,
    });
    const selected = await inspectPresetProfile(
      fixture.client,
      context.toolchain,
      `${context.family} preset-build`,
    );
    const subscription = await withNamedTimeout(
      `${context.family} preset event subscription`,
      fixture.client.subscribeEvents(0),
      nativeTimeoutMs,
    );
    const task = await startPresetBuildWithStaleRetry(
      fixture.client,
      selected,
      context.toolchain,
      `${context.family} preset-build`,
    );
    const events = await waitForTask(
      fixture.client,
      subscription,
      task.taskId,
    );
    assertStepOrder(events, ["configure", "build"], `${context.family} preset build`);
    assertPresetCompiler(
      events,
      context.family,
      expectedCompiler,
      context.toolchain.version,
    );
  } finally {
    await fixture?.dispose();
    await rm(workspaceRoot, { recursive: true, force: true });
    await rm(serviceDirectory, { recursive: true, force: true });
  }
}

async function configurePresetFixture(
  workspaceRoot: string,
  family: RequiredToolchainFamily,
  snapshot: WorkspaceSnapshot,
): Promise<string> {
  const path = join(workspaceRoot, "CMakePresets.json");
  const document = JSON.parse(await readFile(path, "utf8")) as {
    configurePresets?: Array<Record<string, unknown>>;
    buildPresets?: Array<Record<string, unknown>>;
  };
  const configure = document.configurePresets?.[0];
  const build = document.buildPresets?.[0];
  if (configure === undefined || build === undefined) {
    throw new Error("native preset fixture has no configure/build preset");
  }
  build.configuration = "Debug";
  switch (family) {
    case "gcc":
      requirePresetGenerator(snapshot, family, Generator.Ninja);
      configure.cacheVariables = {
        CMAKE_BUILD_TYPE: "Debug",
        CMAKE_CXX_COMPILER: "g++",
      };
      break;
    case "clang":
      requirePresetGenerator(snapshot, family, Generator.Ninja);
      configure.cacheVariables = {
        CMAKE_BUILD_TYPE: "Debug",
        CMAKE_CXX_COMPILER: "clang++",
      };
      break;
    case "msvc":
      configure.generator = requireVisualStudioGenerator(snapshot);
      configure.architecture = "x64";
      delete configure.cacheVariables;
      break;
    case "clang-cl":
      requirePresetGenerator(snapshot, family, Generator.Ninja);
      configure.generator = Generator.Ninja;
      configure.cacheVariables = {
        CMAKE_BUILD_TYPE: "Debug",
        CMAKE_CXX_COMPILER: "clang-cl",
      };
      break;
  }
  await writeFile(path, `${JSON.stringify(document, null, 2)}\n`);
  return family === "gcc" ? "GNU" : family === "msvc" ? "MSVC" : "Clang";
}

function requirePresetGenerator(
  snapshot: WorkspaceSnapshot,
  family: RequiredToolchainFamily,
  generator: Generator,
): void {
  if (
    !snapshot.toolchains.some((toolchain) =>
      toolchain.family === family && toolchain.generators.includes(generator)
    )
  ) {
    throw new Error(`native preset fixture requires verified ${family} ${generator}`);
  }
}

function requireVisualStudioGenerator(snapshot: WorkspaceSnapshot): string {
  const msvc = snapshot.toolchains.filter((toolchain) => toolchain.family === "msvc");
  for (const generator of [
    Generator.VisualStudio182026,
    Generator.VisualStudio172022,
  ]) {
    if (msvc.some((toolchain) => toolchain.generators.includes(generator))) {
      return generator;
    }
  }
  throw new Error("native preset fixture requires a verified Visual Studio generator");
}

async function inspectPresetProfile(
  client: ProtocolClient,
  toolchain: ToolchainElement,
  scenario: string,
): Promise<SelectedProfile> {
  for (let attempt = 1; attempt <= 2; attempt++) {
    const snapshot = await withNamedTimeout(
      `${scenario} inspection attempt ${attempt}`,
      client.inspectWorkspace(),
      nativeTimeoutMs,
    );
    for (const project of snapshot.projects) {
      const profile = project.buildProfiles.find((candidate) =>
        candidate.origin === "preset" &&
        candidate.name === "unit-test-ide-debug"
      );
      if (profile !== undefined) {
        return { snapshot, projectId: project.projectId, profile, toolchain };
      }
    }
  }
  throw new Error(`${scenario} preset profile is absent after one bounded retry`);
}

async function startPresetBuildWithStaleRetry(
  client: ProtocolClient,
  selected: SelectedProfile,
  toolchain: ToolchainElement,
  scenario: string,
) {
  try {
    return await startBuild(client, selected, [], 60_000);
  } catch (error) {
    if (!(error instanceof ProtocolError) || error.code !== "WORKSPACE_CHANGED") {
      throw error;
    }
  }
  const refreshed = await inspectPresetProfile(client, toolchain, scenario);
  return startBuild(client, refreshed, [], 60_000);
}

function assertPresetCompiler(
  events: readonly ProtocolTaskEvent[],
  family: RequiredToolchainFamily,
  expectedCompiler: string,
  expectedVersion: string,
): void {
  const output = events
    .filter((event) => event.event === "task.output")
    .map((event) => String((event.payload as { text?: unknown }).text ?? ""))
    .join("");
  if (
    !output.includes(`CXX compiler identification is ${expectedCompiler}`) ||
    !output.includes(expectedVersion)
  ) {
    throw new Error(
      `${family} preset build did not identify ${expectedCompiler} ${expectedVersion}; output=${
        scrubNativeErrorText(output)
      }`,
    );
  }
}

async function runWorkspaceRejectionScenario(
  context: FamilyExecutionContext,
  name: string,
  mutate: (workspaceRoot: string, parent: string) => Promise<void>,
  expectedCode: string,
  fixtureName: NativeFixtureName = "fallback-project",
): Promise<void> {
  const parent = dirname(context.workspaceRoot);
  const suffix = randomBytes(6).toString("hex");
  const workspaceRoot = await copyNativeFixture(
    fixtureName,
    parent,
    `${name}-${suffix}`,
  );
  const serviceDirectory = join(parent, "r");
  await mkdir(serviceDirectory, { mode: 0o700 });
  let fixture: TaskServiceFixture | undefined;
  try {
    await writeWorkspaceConfig(workspaceRoot);
    await mutate(workspaceRoot, parent);
    fixture = await startService(context.serviceBinary, serviceDirectory, {
      timeoutMs: nativeTimeoutMs,
      workspaceRoot,
      trustedWorkspace: true,
      cmakeBundleRoot: context.bundle.bundleRoot,
    });
    const snapshot = await withNamedTimeout(
      `${context.family} ${name} inspection`,
      fixture.client.inspectWorkspace(),
      nativeTimeoutMs,
    );
    if (
      snapshot.projects.some((project) => project.buildProfiles.length !== 0) ||
      !snapshot.diagnostics.some((diagnostic) => diagnostic.code === expectedCode)
    ) {
      throw new Error(
        `${context.family} ${name} was not rejected as ${expectedCode}: ` +
        JSON.stringify(snapshot.diagnostics),
      );
    }
  } finally {
    await fixture?.dispose();
    await rm(workspaceRoot, { recursive: true, force: true });
    await rm(serviceDirectory, { recursive: true, force: true });
  }
}

async function runFailureScenario(
  context: FamilyExecutionContext,
  fixtureName: NativeFixtureName,
  goldenName: string,
): Promise<void> {
  const parent = dirname(context.workspaceRoot);
  const suffix = randomBytes(6).toString("hex");
  const workspaceRoot = await copyNativeFixture(
    fixtureName,
    parent,
    `${fixtureName}-${suffix}`,
  );
  const serviceDirectory = join(parent, "f");
  await mkdir(serviceDirectory, { mode: 0o700 });
  let fixture: TaskServiceFixture | undefined;
  try {
    await writeWorkspaceConfig(workspaceRoot);
    fixture = await startService(context.serviceBinary, serviceDirectory, {
      timeoutMs: nativeTimeoutMs,
      workspaceRoot,
      trustedWorkspace: true,
      cmakeBundleRoot: context.bundle.bundleRoot,
    });
    const selected = await inspectEstablishedFamily(
      fixture.client,
      context.family,
      fixtureName,
    );
    const subscription = await withNamedTimeout(
      `${context.family} ${fixtureName} event subscription`,
      fixture.client.subscribeEvents(0),
      nativeTimeoutMs,
    );
    const task = await startFailureBuildWithStaleRetry(
      fixture.client,
      selected,
      context.family,
      fixtureName,
    );
    const events = await waitForTask(
      fixture.client,
      subscription,
      task.taskId,
      "command_failed",
    );
    const diagnostics = events
      .filter((event) => event.event === "task.diagnostic")
      .map((event) =>
        normalizeNativeDiagnostic(
          (event.payload as unknown as { diagnostic: Diagnostic }).diagnostic,
          {
            workspace: workspaceRoot,
            build: join(workspaceRoot, ".service-build-unreachable"),
            external: [context.bundle.installRoot],
          },
        )
      );
    const golden = await loadGoldenDiagnostics(goldenName);
    try {
      assertGoldenDiagnostics(diagnostics, golden, `${context.family} ${fixtureName}`);
    } catch (error) {
      const outputTail = events
        .filter((event) => event.event === "task.output")
        .map((event) =>
          scrubNativeErrorText(String(
            (event.payload as unknown as { text?: unknown }).text,
          ))
        )
        .join("")
        .slice(-4096);
      const message = error instanceof Error ? error.message : String(error);
      throw new Error(`${message}; output=${outputTail}`, { cause: error });
    }
  } finally {
    await fixture?.dispose();
    await rm(workspaceRoot, { recursive: true, force: true });
    await rm(serviceDirectory, { recursive: true, force: true });
  }
}

async function inspectEstablishedFamily(
  client: ProtocolClient,
  family: RequiredToolchainFamily,
  scenario: string,
): Promise<SelectedProfile> {
  let lastSnapshot: WorkspaceSnapshot | undefined;
  for (let attempt = 1; attempt <= 2; attempt++) {
    const snapshot = await withNamedTimeout(
      `${family} ${scenario} inspection attempt ${attempt}`,
      client.inspectWorkspace(),
      nativeTimeoutMs,
    );
    const selected = selectGeneratedProfile(snapshot, family);
    if (selected !== undefined) {
      return selected;
    }
    lastSnapshot = snapshot;
  }
  const codes = [
    ...new Set((lastSnapshot?.diagnostics ?? []).map((diagnostic) => diagnostic.code)),
  ];
  throw new Error(
    `${family} disappeared in ${scenario} after one bounded retry; diagnostics=${
      codes.length === 0 ? "none" : codes.join(",")
    }`,
  );
}

async function withEstablishedFamilyCheckpoint<T>(
  client: ProtocolClient,
  family: RequiredToolchainFamily,
  scenario: string,
  operation: (selected: SelectedProfile) => Promise<T>,
): Promise<{ value: T; selected: SelectedProfile }> {
  for (let attempt = 1; attempt <= 2; attempt++) {
    const selected = await inspectEstablishedFamily(client, family, scenario);
    try {
      return {
        value: await operation(selected),
        selected,
      };
    } catch (error) {
      if (
        !(error instanceof ProtocolError) ||
        error.code !== "WORKSPACE_CHANGED" ||
        attempt === 2
      ) {
        throw error;
      }
    }
  }
  throw new Error(`${family} ${scenario} exhausted checkpoint retries`);
}

async function startFamilyBuildAtCheckpoint(
  client: ProtocolClient,
  family: RequiredToolchainFamily,
  scenario: string,
  targetIds: string[],
  timeoutMs: number,
): Promise<ProtocolTaskSnapshot & { selected: SelectedProfile }> {
  const checkpoint = await withEstablishedFamilyCheckpoint(
    client,
    family,
    scenario,
    (selected) => startBuild(client, selected, targetIds, timeoutMs),
  );
  return {
    ...checkpoint.value,
    selected: checkpoint.selected,
  };
}

async function startNamedTargetBuildAtCheckpoint(
  client: ProtocolClient,
  family: RequiredToolchainFamily,
  scenario: string,
  targetName: string,
  timeoutMs: number,
): Promise<ProtocolTaskSnapshot & { selected: SelectedProfile }> {
  const checkpoint = await withEstablishedFamilyCheckpoint(
    client,
    family,
    scenario,
    async (selected) => {
      const targets = await withNamedTimeout(
        `${family} ${scenario} target listing`,
        client.listCMakeTargets({
          workspaceGeneration: selected.snapshot.workspaceGeneration,
          projectId: selected.projectId,
          buildProfileId: selected.profile.buildProfileId,
        }),
        nativeTimeoutMs,
      );
      const target = targets.targets.find((candidate) => candidate.name === targetName);
      if (target === undefined) {
        throw new Error(`${family} ${scenario} target ${targetName} is absent`);
      }
      return startBuild(client, selected, [target.targetId], timeoutMs);
    },
  );
  return {
    ...checkpoint.value,
    selected: checkpoint.selected,
  };
}

async function startFailureBuildWithStaleRetry(
  client: ProtocolClient,
  selected: SelectedProfile,
  family: RequiredToolchainFamily,
  scenario: string,
) {
  try {
    return await startBuild(client, selected, [], 60_000);
  } catch (error) {
    if (!(error instanceof ProtocolError) || error.code !== "WORKSPACE_CHANGED") {
      throw error;
    }
  }
  const refreshed = await inspectEstablishedFamily(client, family, scenario);
  return startBuild(client, refreshed, [], 60_000);
}

async function loadGoldenDiagnostics(
  name: string,
): Promise<readonly GoldenDiagnosticExpectation[]> {
  if (
    !/^(compiler-gcc-clang|linker-gcc-clang|compiler-msvc-clang-cl|linker-msvc-clang-cl|configure)\.json$/u.test(name)
  ) {
    throw new Error(`unsupported native diagnostic golden: ${name}`);
  }
  const document = JSON.parse(
    await readFile(join(repositoryRoot, "testdata", "toolchains", "golden", name), "utf8"),
  ) as { minimum?: unknown };
  if (!Array.isArray(document.minimum) || document.minimum.length === 0) {
    throw new Error(`invalid native diagnostic golden: ${name}`);
  }
  return document.minimum as GoldenDiagnosticExpectation[];
}

function assertGoldenDiagnostics(
  diagnostics: readonly Diagnostic[],
  expectations: readonly GoldenDiagnosticExpectation[],
  label: string,
): void {
  for (const expectation of expectations) {
    const matched = diagnostics.some((diagnostic) =>
      diagnostic.severity === expectation.severity &&
      diagnostic.message.includes(expectation.messageContains) &&
      (expectation.file === undefined || diagnostic.sourceUri === expectation.file) &&
      (expectation.line === undefined || diagnostic.line === expectation.line) &&
      (
        expectation.codePattern === undefined ||
        new RegExp(expectation.codePattern, "u").test(diagnostic.code)
      )
    );
    if (!matched) {
      throw new Error(
        `${label} diagnostics did not satisfy ${JSON.stringify(expectation)}: ` +
        JSON.stringify(diagnostics),
      );
    }
  }
}

function startBuild(
  client: ProtocolClient,
  context: SelectedProfile,
  targetIds: string[],
  timeoutMs: number,
) {
  return withNamedTimeout(
    "native CMake build start",
    client.startCMakeBuild({
      idempotencyKey: randomBytes(16).toString("hex"),
      workspaceGeneration: context.snapshot.workspaceGeneration,
      projectId: context.projectId,
      buildProfileId: context.profile.buildProfileId,
      targetIds,
      jobs: 2,
      timeoutMs,
    }),
    nativeTimeoutMs,
  );
}

async function waitForTask(
  client: ProtocolClient,
  subscription: EventSubscription,
  taskId: string,
  expectedOutcome = "succeeded",
  initialEvents: ProtocolTaskEvent[] = [],
): Promise<ProtocolTaskEvent[]> {
  const events = [...initialEvents];
  const deadline = Date.now() + nativeTimeoutMs;
  let pending = subscription.next();
  let lastSnapshot: ProtocolTaskSnapshot | undefined;
  let terminalReplayRequested = false;
  for (;;) {
    const remaining = deadline - Date.now();
    if (remaining <= 0) {
      throw nativeTaskCompletionTimeout(
        taskId,
        subscription.lastSequence,
        lastSnapshot,
      );
    }
    const result = await waitForNativeEvent(
      pending,
      Math.min(nativeEventHeartbeatMs, remaining),
    );
    if (result.kind === "heartbeat") {
      try {
        lastSnapshot = await withNamedTimeout(
          `native task ${taskId} liveness lookup`,
          client.getTask(taskId),
          Math.min(nativeEventHeartbeatMs, Math.max(deadline - Date.now(), 1)),
        );
        if (lastSnapshot.status === "finished" && !terminalReplayRequested) {
          terminalReplayRequested = true;
          await withNamedTimeout(
            `native task ${taskId} terminal replay reconnect`,
            client.reconnect(),
            Math.min(nativeEventHeartbeatMs, Math.max(deadline - Date.now(), 1)),
          );
        }
      } catch (error) {
        if (error instanceof ProtocolError) {
          throw error;
        }
        await withNamedTimeout(
          `native task ${taskId} liveness reconnect`,
          client.reconnect(),
          Math.min(nativeEventHeartbeatMs, Math.max(deadline - Date.now(), 1)),
        ).catch((reconnectError: unknown) => {
          throw new Error(
            `native task ${taskId} liveness recovery failed after sequence ` +
            `${subscription.lastSequence}`,
            { cause: reconnectError },
          );
        });
      }
      continue;
    }
    const next = result.next;
    pending = subscription.next();
    if (next.done) {
      throw new Error(`native event subscription closed before task ${taskId} finished`);
    }
    if (next.value.taskId !== taskId) {
      continue;
    }
    events.push(next.value);
    if (next.value.event === "task.finished") {
      const outcome = (next.value.payload as { outcome?: unknown }).outcome;
      if (outcome !== expectedOutcome) {
        const stepSummary = events
          .filter((event) => event.event === "task.step_finished")
          .map((event) => {
            const payload = event.payload as {
              stepId?: unknown;
              status?: unknown;
              errorCode?: unknown;
            };
            return `${String(payload.stepId)}:${String(payload.status)}:${String(payload.errorCode ?? "")}`;
          });
        const diagnosticCodes = events
          .filter((event) => event.event === "task.diagnostic")
          .map((event) =>
            String((event.payload as unknown as { diagnostic?: { code?: unknown } }).diagnostic?.code)
          );
        const diagnosticMessages = events
          .filter((event) => event.event === "task.diagnostic")
          .map((event) =>
            scrubNativeErrorText(String(
              (event.payload as unknown as { diagnostic?: { message?: unknown } }).diagnostic?.message,
            ))
          );
        const outputTail = events
          .filter((event) => event.event === "task.output")
          .map((event) =>
            scrubNativeErrorText(String(
              (event.payload as unknown as { text?: unknown }).text,
            ))
          )
          .join("")
          .slice(-4096);
        const nativeErrorFragments = events
          .filter((event) => event.event === "task.output")
          .flatMap((event) => {
            const text = String((event.payload as unknown as { text?: unknown }).text);
            return text.match(/\berror\s+[A-Z]+\d+:[^\r\n]*/giu) ?? [];
          })
          .map(scrubNativeErrorText);
        throw new Error(
          `native task ${taskId} finished with ${String(outcome)}, want ${expectedOutcome}; ` +
          `steps=${stepSummary.join(",")}; diagnostics=${diagnosticCodes.join(",")}; ` +
          `messages=${diagnosticMessages.join(" | ")}; native-errors=${nativeErrorFragments.join(" | ")}; ` +
          `output=${outputTail}`,
        );
      }
      return events;
    }
  }
}

function waitForNativeEvent(
  pending: Promise<IteratorResult<ProtocolTaskEvent>>,
  milliseconds: number,
): Promise<
  | { kind: "event"; next: IteratorResult<ProtocolTaskEvent> }
  | { kind: "heartbeat" }
> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () => resolve({ kind: "heartbeat" }),
      milliseconds,
    );
    pending.then(
      (next) => {
        clearTimeout(timer);
        resolve({ kind: "event", next });
      },
      (error: unknown) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}

function nativeTaskCompletionTimeout(
  taskId: string,
  lastSequence: number,
  snapshot: ProtocolTaskSnapshot | undefined,
): Error {
  const state = snapshot === undefined
    ? "unavailable"
    : `${snapshot.status}/${String(snapshot.outcome ?? "")}`;
  return new Error(
    `native task ${taskId} completion timed out after ${nativeTimeoutMs}ms; ` +
    `lastSequence=${lastSequence}; durableState=${state}`,
  );
}

function scrubNativeErrorText(value: string): string {
  return value
    .replace(/[A-Za-z]:[\\/][^\r\n;"]+/gu, "<absolute-path>")
    .replace(/(^|[\s=(])\/[^\s\r\n;"]+/gu, "$1<absolute-path>")
    .slice(0, 2048);
}

async function waitForStep(
  subscription: EventSubscription,
  taskId: string,
  stepId: string,
): Promise<ProtocolTaskEvent[]> {
  const events: ProtocolTaskEvent[] = [];
  for (;;) {
    const next = await withNamedTimeout(
      `native task ${taskId} step ${stepId}`,
      subscription.next(),
      nativeTimeoutMs,
    );
    if (next.done) {
      throw new Error(`native event subscription closed before task ${taskId} step ${stepId}`);
    }
    if (next.value.taskId !== taskId) {
      continue;
    }
    events.push(next.value);
    if (
      next.value.event === "task.step_started" &&
      (next.value.payload as { stepId?: unknown }).stepId === stepId
    ) {
      return events;
    }
    if (next.value.event === "task.finished") {
      throw new Error(`native task ${taskId} finished before step ${stepId}`);
    }
  }
}

function assertStepOrder(
  events: readonly ProtocolTaskEvent[],
  expected: readonly string[],
  label: string,
): void {
  const steps = events
    .filter((event) => event.event === "task.step_started")
    .map((event) => (event.payload as { stepId?: unknown }).stepId);
  if (JSON.stringify(steps) !== JSON.stringify(expected)) {
    throw new Error(`${label} steps = ${JSON.stringify(steps)}, want ${JSON.stringify(expected)}`);
  }
}

function assertContinuousSequences(
  events: readonly ProtocolTaskEvent[],
  label: string,
): void {
  const seen = new Set<number>();
  for (let index = 0; index < events.length; index++) {
    const sequence = events[index]!.sequence;
    if (seen.has(sequence)) {
      throw new Error(`${label} duplicated event sequence ${sequence}`);
    }
    seen.add(sequence);
    if (index > 0 && sequence !== events[index - 1]!.sequence + 1) {
      throw new Error(
        `${label} event sequence jumped from ${events[index - 1]!.sequence} to ${sequence}`,
      );
    }
  }
}

async function expectProtocolError(
  operation: () => Promise<unknown>,
  code: string,
): Promise<void> {
  try {
    await operation();
  } catch (error) {
    if (error instanceof ProtocolError && error.code === code) {
      return;
    }
    throw error;
  }
  throw new Error(`native request unexpectedly succeeded; wanted ${code}`);
}

export async function verifyPreparedCMakeBundle(
  bundleRoot: string,
  platform: NodeJS.Platform,
  architecture: string,
  operations: {
    sha256File?: (path: string) => Promise<string>;
    readCapabilities?: (executable: string) => Promise<unknown>;
    manifestPath?: string;
  } = {},
): Promise<PreparedCMakeBundle> {
  if (platform !== "linux" && platform !== "win32" || architecture !== "x64") {
    throw new Error(`unsupported CMake bundle platform: ${platform}-${architecture}`);
  }
  if (!isAbsolute(bundleRoot) || bundleRoot.includes("\0")) {
    throw new Error("prepared CMake bundle root must be absolute");
  }
  await requireDirectDirectory(bundleRoot, "prepared CMake bundle root");
  const manifestPath = operations.manifestPath ??
    join(repositoryRoot, "tools", "cmake-bundle", "manifest.json");
  const trackedBytes = await readFile(manifestPath);
  const publishedPath = join(bundleRoot, "manifest.json");
  await requireDirectFile(publishedPath, "prepared CMake bundle manifest");
  const publishedBytes = await readFile(publishedPath);
  if (!publishedBytes.equals(trackedBytes)) {
    throw new Error("prepared CMake bundle manifest mismatch");
  }
  const manifest = parseObject(trackedBytes, "tracked CMake bundle manifest");
  if (
    manifest.schemaVersion !== 1 ||
    manifest.cmakeVersion !== "4.3.4" ||
    manifest.license !== "BSD-3-Clause"
  ) {
    throw new Error("tracked CMake bundle manifest identity mismatch");
  }
  const key = `${platform}-${architecture}` as PreparedCMakeBundle["key"];
  const archives = requirePlainObject(manifest.archives, "CMake bundle archives");
  const archive = requirePlainObject(archives[key], `CMake bundle archive ${key}`);
  const archiveSha256 = requireDigest(archive.archiveSha256, "archive SHA-256");
  const rootDirectory = requireSafeSegment(archive.rootDirectory, "archive root directory");
  const executableRelative = requirePortablePath(archive.executable, "CMake executable");
  const installedFiles = requirePlainObject(archive.installedFiles, "installed CMake files");
  const expectedState = {
    schemaVersion: 1,
    key,
    cmakeVersion: manifest.cmakeVersion,
    archiveSha256,
    installedFiles,
  };

  const platformRoot = join(bundleRoot, String(manifest.cmakeVersion), key);
  await requireDirectDirectory(platformRoot, "prepared CMake platform directory");
  const statePath = join(platformRoot, "bundle-state.json");
  await requireDirectFile(statePath, "prepared CMake bundle state");
  const state = parseObject(await readFile(statePath), "prepared CMake bundle state");
  if (canonicalJSON(state) !== canonicalJSON(expectedState)) {
    throw new Error("prepared CMake bundle state mismatch");
  }
  const installRoot = join(platformRoot, rootDirectory);
  await requireDirectDirectory(installRoot, "prepared CMake install root");
  const digestFile = operations.sha256File ?? sha256File;
  for (const [relativePath, expectedDigestValue] of Object.entries(installedFiles)) {
    const portablePath = requirePortablePath(relativePath, "installed CMake file");
    const expectedDigest = requireDigest(expectedDigestValue, "installed CMake file SHA-256");
    const path = join(installRoot, ...portablePath.split("/"));
    await requireDirectFile(path, "installed CMake file");
    if (!withinRoot(await realpath(installRoot), await realpath(path))) {
      throw new Error("installed CMake file escapes the bundle root");
    }
    if (await digestFile(path) !== expectedDigest) {
      throw new Error(`installed CMake file SHA-256 mismatch: ${portablePath}`);
    }
  }
  const executable = join(installRoot, ...executableRelative.split("/"));
  const capabilities = await (
    operations.readCapabilities ?? readCMakeCapabilities
  )(executable) as { version?: { string?: unknown } };
  if (capabilities?.version?.string !== manifest.cmakeVersion) {
    throw new Error("prepared CMake bundle version mismatch");
  }
  return {
    bundleRoot: resolve(bundleRoot),
    installRoot,
    executable,
    key,
    cmakeVersion: String(manifest.cmakeVersion),
    archiveSha256,
  };
}

async function sha256File(path: string): Promise<string> {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) {
    hash.update(chunk);
  }
  return hash.digest("hex");
}

async function readCMakeCapabilities(executable: string): Promise<unknown> {
  const { stdout } = await execFile(executable, ["-E", "capabilities"], {
    encoding: "utf8",
    env: { LANG: "C", LC_ALL: "C" },
    timeout: 15_000,
    maxBuffer: 1024 * 1024,
    windowsHide: true,
  });
  return JSON.parse(stdout);
}

async function requireDirectDirectory(path: string, label: string): Promise<void> {
  const info = await lstat(path).catch((error: unknown) => {
    throw new Error(`${label} is unavailable`, { cause: error });
  });
  if (!info.isDirectory() || info.isSymbolicLink()) {
    throw new Error(`${label} is unsafe`);
  }
}

async function requireDirectFile(path: string, label: string): Promise<void> {
  const info = await lstat(path).catch((error: unknown) => {
    throw new Error(`${label} is unavailable`, { cause: error });
  });
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new Error(`${label} is unsafe`);
  }
}

function parseObject(bytes: Buffer, label: string): Record<string, unknown> {
  try {
    return requirePlainObject(JSON.parse(bytes.toString("utf8")), label);
  } catch (error) {
    throw new Error(`${label} is invalid`, { cause: error });
  }
}

function requirePlainObject(value: unknown, label: string): Record<string, unknown> {
  if (
    value === null ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.getPrototypeOf(value) !== Object.prototype
  ) {
    throw new Error(`${label} must be a plain object`);
  }
  return value as Record<string, unknown>;
}

function requireDigest(value: unknown, label: string): string {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/u.test(value)) {
    throw new Error(`${label} is invalid`);
  }
  return value;
}

function requireSafeSegment(value: unknown, label: string): string {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value === "." ||
    value === ".." ||
    /[<>:"/\\|?*\u0000-\u001f\u007f]/u.test(value)
  ) {
    throw new Error(`${label} is invalid`);
  }
  return value;
}

function requirePortablePath(value: unknown, label: string): string {
  if (
    typeof value !== "string" ||
    value.length === 0 ||
    value.includes("\\") ||
    isAbsolute(value) ||
    value.split("/").some((segment) => segment === "" || segment === "." || segment === "..")
  ) {
    throw new Error(`${label} is invalid`);
  }
  return value;
}

function canonicalJSON(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(canonicalJSON).join(",")}]`;
  }
  if (value !== null && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return `{${Object.keys(record).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(record[key])}`
    ).join(",")}}`;
  }
  return JSON.stringify(value);
}

function withinRoot(root: string, candidate: string): boolean {
  const suffix = relative(root, candidate);
  return suffix === "" || (
    suffix !== ".." &&
    !suffix.startsWith(`..${sep}`) &&
    !isAbsolute(suffix)
  );
}

export const __testing = Object.freeze({
  runNativeMatrixWithDependencies,
  selectGeneratedProfile,
  startFamilyBuildAtCheckpoint,
  startNamedTargetBuildAtCheckpoint,
  startFailureBuildWithStaleRetry,
});
