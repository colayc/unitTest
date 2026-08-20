import assert from "node:assert/strict";
import {
  execFile as execFileCallback,
  spawn,
  type ChildProcessWithoutNullStreams
} from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { once } from "node:events";
import {
  cp,
  lstat,
  mkdir,
  mkdtemp,
  rm,
  writeFile
} from "node:fs/promises";
import { createConnection } from "node:net";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import {
  ProtocolClient,
  ProtocolError,
  TestSelectionModeV14,
  type CoverageReport,
  type CoverageRun,
  type CoverageRunInput,
  type ProtocolArtifactMetadata,
  type ProtocolTaskSnapshot,
  type WorkspaceSnapshot
} from "@unit-test-ide/test-client";
import {
  decodeCoverageDocumentV1,
  type CoverageDocumentV1
} from "@unit-test-ide/coverage-models";
import {
  installWindowsNativeOfflineBoundary,
  type WindowsNativeOfflineBoundary
} from "@unit-test-ide/service-probe/native-network-guard";
import { createCoverageController } from "../src/coverage-controller.js";
import { openCoverageHtml } from "../src/coverage-viewer.js";
import {
  ServiceManager,
  type ServiceOperations
} from "../src/service-manager.js";
import { redactServiceError } from "../src/service-resources.js";
import {
  parseStrictJUnit,
  publishEvidenceAtomically,
  teardownThenPublish
} from "./coverage-service-smoke-support.js";

const execFile = promisify(execFileCallback);
const repositoryRoot = resolve(import.meta.dirname, "../../../..");
const coverageFixtureRoot = join(
  repositoryRoot,
  "apps",
  "code-oss-extension",
  "test",
  "fixtures",
  "coverage"
);
const cmakeBundleRoot = join(repositoryRoot, ".bundled-tools", "cmake");
const evidencePath = join(
  repositoryRoot,
  ".native-e2e",
  "artifacts",
  "windows",
  "coverage-execution-report.json"
);
const SKIP_MESSAGE = "SKIP: verified clang-cl coverage toolset is unavailable";
const COVERAGE_PROFILE_ID = "coverage-clang-cl";
const PROJECT_ID = "coverage-fixture";
const TEST_CONTAINER = "coverage-tests";
const NATIVE_TIMEOUT_MS = 180_000;

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly dataDirectory: string;
  readonly serviceBinary: string;
  readonly goCache: string;
}

interface SelectedClangCL {
  readonly snapshot: WorkspaceSnapshot;
  readonly projectId: string;
  readonly profile: WorkspaceSnapshot["projects"][number]["buildProfiles"][number];
  readonly toolchain: WorkspaceSnapshot["toolchains"][number];
}

interface PublicArtifact {
  readonly kind: "coverage-json" | "junit-xml" | "coverage-html";
  readonly metadata: ProtocolArtifactMetadata;
  readonly bytes: Uint8Array;
}

interface CoverageExecutionEvidence {
  readonly schemaVersion: 1;
  readonly platform: "windows";
  readonly architecture: "x64";
  readonly tools: {
    readonly compiler: { readonly family: "clang-cl"; readonly version: string };
    readonly driver: { readonly name: "llvm-cov"; readonly version: string };
    readonly collector: { readonly name: "llvm-cov"; readonly version: string };
  };
  readonly runOutcome: "available";
  readonly testRunOutcome: "failed";
  readonly summary: CoverageReport["summary"];
  readonly artifacts: ReadonlyArray<{
    readonly kind: PublicArtifact["kind"];
    readonly sizeBytes: number;
    readonly sha256: string;
  }>;
  readonly durationMs: number;
}

class ProtocolWireCapture {
  readonly #chunks: Buffer[] = [];

  record(chunk: Buffer | string): void {
    this.#chunks.push(Buffer.from(chunk));
  }

  reset(): void {
    this.#chunks.length = 0;
  }

  bytes(): Buffer {
    return Buffer.concat(this.#chunks);
  }
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}

async function pathExists(path: string): Promise<boolean> {
  try {
    await lstat(path);
    return true;
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === "ENOENT") return false;
    throw error;
  }
}

async function createFixture(): Promise<Fixture> {
  const fixtureParent = join(repositoryRoot, "build");
  await mkdir(fixtureParent, { recursive: true });
  const root = await mkdtemp(join(fixtureParent, "unit-test-ide-coverage-smoke-"));
  const workspace = join(root, "workspace");
  try {
    assert.equal(
      await pathExists(coverageFixtureRoot),
      true,
      "the tracked deterministic coverage fixture must exist"
    );
    await cp(coverageFixtureRoot, workspace, {
      recursive: true,
      errorOnExist: true,
      force: false
    });
    await writeWorkspaceConfig(workspace);
    return {
      root,
      workspace,
      dataDirectory: join(root, "service-data"),
      serviceBinary: join(root, "unit-test-service.exe"),
      goCache: join(root, "go-cache")
    };
  } catch (error) {
    await rm(root, { recursive: true, force: true });
    throw error;
  }
}

async function writeWorkspaceConfig(workspace: string, baseBuildProfileId?: string): Promise<void> {
  const directory = join(workspace, ".unit-test-ide");
  await mkdir(directory, { recursive: true });
  const document = {
    version: 3,
    projects: [{
      id: PROJECT_ID,
      sourceDir: ".",
      fallback: {
        configurations: ["Debug"],
        preferredGenerator: "Ninja"
      },
      tests: {
        containers: [{ ctestName: TEST_CONTAINER, framework: "cpputest" }]
      }
    }],
    ...(baseBuildProfileId === undefined ? {} : {
      coverageProfiles: [{
        id: COVERAGE_PROFILE_ID,
        baseBuildProfileId,
        include: ["src/**"],
        exclude: ["test/**"]
      }]
    })
  };
  await writeFile(
    join(directory, "workspace.json"),
    `${JSON.stringify(document, null, 2)}\n`,
    "utf8"
  );
}

async function buildService(fixture: Fixture): Promise<string> {
  await mkdir(fixture.goCache, { recursive: true });
  const executable = process.env.UNIT_TEST_IDE_GO_EXECUTABLE?.trim() || "go";
  const environment = {
    ...process.env,
    GOENV: "off",
    GOTOOLCHAIN: "local",
    GOCACHE: fixture.goCache
  };
  const version = await execFile(executable, ["version"], {
    cwd: repositoryRoot,
    encoding: "utf8",
    env: environment,
    timeout: 30_000,
    windowsHide: true
  });
  assert.match(version.stdout, /^go version go1\.26\.6 windows\/amd64\s*$/u);
  await execFile(
    executable,
    [
      "build",
      "-trimpath",
      "-o",
      fixture.serviceBinary,
      "./apps/test-service/cmd/unit-test-service"
    ],
    {
      cwd: repositoryRoot,
      env: environment,
      timeout: NATIVE_TIMEOUT_MS,
      windowsHide: true,
      maxBuffer: 16 * 1024 * 1024
    }
  );
  return version.stdout.trim().replace(/^go version /u, "");
}

function coverageOperations(
  fixture: Fixture,
  wire: ProtocolWireCapture,
  tokens: string[],
  hostileEnvironmentValue: string,
  useCMakeBundle: boolean
): Partial<ServiceOperations> {
  return {
    async prepareTokenFile(binary, tokenFile, token) {
      tokens.push(token);
      await execFile(binary, ["--prepare-token-file", tokenFile], {
        windowsHide: true,
        timeout: 30_000
      });
      await writeFile(tokenFile, token, { flag: "r+" });
    },
    spawnService(binary, args): ChildProcessWithoutNullStreams {
      const launchArguments = useCMakeBundle
        ? [...args, "--cmake-bundle-root", cmakeBundleRoot]
        : [...args];
      return spawn(binary, launchArguments, {
        windowsHide: true,
        stdio: "pipe",
        env: {
          ...process.env,
          UNIT_TEST_IDE_COVERAGE_SMOKE_SECRET: hostileEnvironmentValue,
          LLVM_PROFILE_FILE: join(fixture.root, `${hostileEnvironmentValue}-%p.profraw`)
        }
      });
    },
    async connect(endpoint) {
      const socket = createConnection(endpoint);
      const write = socket.write.bind(socket) as unknown as (...args: unknown[]) => boolean;
      socket.write = ((chunk: unknown, ...args: unknown[]) => {
        if (typeof chunk === "string") {
          wire.record(chunk);
        } else if (chunk instanceof Uint8Array) {
          wire.record(Buffer.from(chunk));
        }
        return write(chunk, ...args);
      }) as typeof socket.write;
      socket.on("data", (chunk: Buffer) => wire.record(chunk));
      try {
        await once(socket, "connect");
      } catch (error) {
        socket.destroy();
        throw error;
      }
      return ProtocolClient.attach(socket);
    }
  };
}

function requiredClangCL(): boolean {
  return (process.env.UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS ?? "")
    .split(",")
    .some((value) => value.trim() === "clang-cl");
}

function selectClangCL(
  snapshot: WorkspaceSnapshot,
  preferredToolchainId?: string
): SelectedClangCL | undefined {
  const toolchains = snapshot.toolchains
    .filter((toolchain) =>
      toolchain.family === "clang-cl" &&
      toolchain.hostArchitecture === "x64" &&
      toolchain.targetArchitecture === "x64" &&
      toolchain.version.length > 0 &&
      toolchain.capabilities.coverageDrivers.some((driver) => driver === "llvm-cov") &&
      (preferredToolchainId === undefined || toolchain.toolchainId === preferredToolchainId)
    )
    .sort((left, right) => left.toolchainId.localeCompare(right.toolchainId, "en"));
  for (const toolchain of toolchains) {
    const project = snapshot.projects.find((candidate) => candidate.projectId === PROJECT_ID);
    const profile = project?.buildProfiles
      .filter((candidate) =>
        candidate.origin === "generated" &&
        candidate.toolchainId === toolchain.toolchainId &&
        candidate.generator === "Ninja" &&
        candidate.configuration === "Debug"
      )
      .sort((left, right) => left.buildProfileId.localeCompare(right.buildProfileId, "en"))[0];
    if (project !== undefined && profile !== undefined) {
      return { snapshot, projectId: project.projectId, profile, toolchain };
    }
  }
  return undefined;
}

async function inspectSelected(
  client: ProtocolClient,
  preferredToolchainId?: string
): Promise<SelectedClangCL | undefined> {
  let selected: SelectedClangCL | undefined;
  for (let attempt = 0; attempt < 2; attempt++) {
    selected = selectClangCL(await client.inspectWorkspace(), preferredToolchainId);
    if (selected !== undefined) return selected;
    await delay(100);
  }
  return selected;
}

async function waitForTask(
  client: ProtocolClient,
  taskId: string,
  label: string
): Promise<ProtocolTaskSnapshot> {
  const deadline = Date.now() + NATIVE_TIMEOUT_MS;
  for (;;) {
    const task = await client.getTask(taskId);
    if (task.status === "finished") {
      assert.equal(task.outcome, "succeeded", `${label} must succeed`);
      return task;
    }
    if (Date.now() >= deadline) throw new Error(`${label} exceeded its bounded completion timeout`);
    await delay(250);
  }
}

async function buildBaseProfile(
  client: ProtocolClient,
  initial: SelectedClangCL
): Promise<SelectedClangCL> {
  let selected = initial;
  for (let attempt = 0; attempt < 2; attempt++) {
    try {
      const task = await client.startCMakeBuild({
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: selected.snapshot.workspaceGeneration,
        projectId: selected.projectId,
        buildProfileId: selected.profile.buildProfileId,
        targetIds: [],
        jobs: 2,
        timeoutMs: NATIVE_TIMEOUT_MS
      });
      await waitForTask(client, task.taskId, "base clang-cl fixture build");
      const refreshed = await inspectSelected(client, selected.toolchain.toolchainId);
      if (refreshed === undefined) throw new Error("clang-cl profile disappeared after the base build");
      assert.equal(refreshed.profile.buildProfileId, selected.profile.buildProfileId);
      return refreshed;
    } catch (error) {
      if (!(error instanceof ProtocolError) || error.code !== "WORKSPACE_CHANGED" || attempt === 1) {
        throw error;
      }
      const refreshed = await inspectSelected(client, selected.toolchain.toolchainId);
      if (refreshed === undefined) throw new Error("clang-cl profile disappeared after workspace refresh");
      selected = refreshed;
    }
  }
  throw new Error("base clang-cl fixture build exhausted its bounded retry");
}

async function discoverCatalog(client: ProtocolClient, selected: SelectedClangCL) {
  const discovery = await client.discoverTests({
    idempotencyKey: randomBytes(16).toString("hex"),
    projectId: selected.projectId,
    profileId: selected.profile.buildProfileId
  });
  await waitForTask(client, discovery.taskId, "coverage fixture discovery");
  const catalog = await client.getTestCatalog({
    projectId: selected.projectId,
    profileId: selected.profile.buildProfileId,
    limit: 100
  });
  assert.equal(catalog.partial, false);
  assert.equal(catalog.containers.length, 1);
  assert.deepEqual(
    catalog.items
      .filter((item) => item.kind === "case")
      .map((item) => item.logicalName)
      .sort(),
    ["coversBranch", "failsAfterInstrumentedCode"]
  );
  return catalog;
}

async function waitForCoverageFinished(
  client: ProtocolClient,
  initial: CoverageRun
): Promise<CoverageRun> {
  const deadline = Date.now() + NATIVE_TIMEOUT_MS;
  let run = initial;
  for (;;) {
    if (run.status === "finished") return run;
    if (Date.now() >= deadline) throw new Error("coverage run exceeded its bounded completion timeout");
    await delay(250);
    run = await client.getCoverageRun(initial.coverageRunId);
  }
}

async function listAllArtifacts(
  client: ProtocolClient,
  taskId: string
): Promise<ProtocolArtifactMetadata[]> {
  const result: ProtocolArtifactMetadata[] = [];
  let cursor: string | undefined;
  do {
    const page = await client.listArtifacts(taskId, { limit: 200, ...(cursor ? { cursor } : {}) });
    result.push(...page.items);
    cursor = page.nextCursor;
  } while (cursor !== undefined);
  return result;
}

async function fetchPublicArtifacts(
  client: ProtocolClient,
  taskId: string
): Promise<PublicArtifact[]> {
  const publicKinds = ["coverage-json", "junit-xml", "coverage-html"] as const;
  const metadata = await listAllArtifacts(client, taskId);
  const artifacts: PublicArtifact[] = [];
  for (const kind of publicKinds) {
    const matches = metadata.filter((artifact) => artifact.kind === kind);
    assert.equal(matches.length, 1, `expected exactly one ${kind} artifact`);
    const item = matches[0]!;
    const bytes = await client.readArtifact(item.artifactId);
    assert.equal(bytes.byteLength, item.sizeBytes, `${kind} size must match metadata`);
    assert.equal(
      createHash("sha256").update(bytes).digest("hex"),
      item.sha256,
      `${kind} digest must match metadata`
    );
    artifacts.push({ kind, metadata: item, bytes });
  }
  assert.equal(new Set(artifacts.map((artifact) => artifact.metadata.artifactId)).size, 3);
  return artifacts;
}

function artifactByKind(
  artifacts: readonly PublicArtifact[],
  kind: PublicArtifact["kind"]
): PublicArtifact {
  const artifact = artifacts.find((candidate) => candidate.kind === kind);
  if (artifact === undefined) throw new Error(`missing ${kind} artifact`);
  return artifact;
}

function decodeCoverageJSON(artifact: PublicArtifact): CoverageDocumentV1 {
  assert.equal(artifact.kind, "coverage-json");
  const text = new TextDecoder("utf-8", { fatal: true }).decode(artifact.bytes);
  return decodeCoverageDocumentV1(JSON.parse(text));
}

function assertProvenance(report: CoverageReport, document: CoverageDocumentV1): string {
  assert.equal(report.toolProvenance.platform, "windows");
  assert.equal(report.toolProvenance.architecture, "x64");
  assert.equal(report.toolProvenance.compiler.family, "clang-cl");
  assert.equal(report.toolProvenance.driver.name, "llvm-cov");
  assert.equal(report.toolProvenance.collector.name, "llvm-cov");
  const version = report.toolProvenance.compiler.version;
  assert.notEqual(version, "");
  assert.equal(report.toolProvenance.driver.version, version);
  assert.equal(report.toolProvenance.collector.version, version);
  for (const component of [
    report.toolProvenance.compiler,
    report.toolProvenance.driver,
    report.toolProvenance.collector
  ]) {
    assert.equal("executable" in component, false);
    assert.equal("path" in component, false);
  }
  assert.deepEqual(document.provenance, report.toolProvenance);
  return version;
}

function pathVariants(value: string): string[] {
  return [...new Set([
    value,
    value.replaceAll("\\", "/"),
    value.replaceAll("/", "\\")
  ].filter((candidate) => candidate.length > 0))];
}

function assertNoSensitiveBytes(
  label: string,
  bytes: Uint8Array,
  sensitive: readonly string[]
): void {
  const text = Buffer.from(bytes).toString("utf8");
  const lower = text.toLowerCase();
  for (const value of sensitive) {
    for (const variant of pathVariants(value)) {
      assert.equal(
        lower.includes(variant.toLowerCase()),
        false,
        `${label} contains a sensitive value`
      );
    }
  }
  assert.doesNotMatch(text, /(?:[A-Za-z]:[\\/]|file:\/{2,3}[A-Za-z]:|\\\\[?.]\\)/u);
  assert.doesNotMatch(lower, /(?:llvm_profile_file|\.profraw|\.profdata)/u);
}

function buildEvidence(
  report: CoverageReport,
  artifacts: readonly PublicArtifact[],
  durationMs: number
): CoverageExecutionEvidence {
  assert.equal(Number.isSafeInteger(durationMs) && durationMs >= 0, true);
  const evidence: CoverageExecutionEvidence = {
    schemaVersion: 1,
    platform: "windows",
    architecture: "x64",
    tools: {
      compiler: {
        family: "clang-cl",
        version: report.toolProvenance.compiler.version
      },
      driver: {
        name: "llvm-cov",
        version: report.toolProvenance.driver.version
      },
      collector: {
        name: "llvm-cov",
        version: report.toolProvenance.collector.version
      }
    },
    runOutcome: "available",
    testRunOutcome: "failed",
    summary: {
      lines: { ...report.summary.lines },
      branches: { ...report.summary.branches },
      functions: { ...report.summary.functions }
    },
    artifacts: artifacts.map((artifact) => ({
      kind: artifact.kind,
      sizeBytes: artifact.metadata.sizeBytes,
      sha256: artifact.metadata.sha256
    })),
    durationMs
  };
  assert.deepEqual(Object.keys(evidence).sort(), [
    "architecture",
    "artifacts",
    "durationMs",
    "platform",
    "runOutcome",
    "schemaVersion",
    "summary",
    "testRunOutcome",
    "tools"
  ]);
  assert.deepEqual(
    evidence.artifacts.map((artifact) => artifact.kind),
    ["coverage-json", "junit-xml", "coverage-html"]
  );
  assert.deepEqual(Object.keys(evidence.tools).sort(), ["collector", "compiler", "driver"]);
  assert.deepEqual(Object.keys(evidence.tools.compiler).sort(), ["family", "version"]);
  assert.deepEqual(Object.keys(evidence.tools.driver).sort(), ["name", "version"]);
  assert.deepEqual(Object.keys(evidence.tools.collector).sort(), ["name", "version"]);
  for (const metric of [evidence.summary.lines, evidence.summary.branches, evidence.summary.functions]) {
    assert.deepEqual(Object.keys(metric).sort(), ["covered", "total"]);
  }
  for (const artifact of evidence.artifacts) {
    assert.deepEqual(Object.keys(artifact).sort(), ["kind", "sha256", "sizeBytes"]);
  }
  return evidence;
}

test("real Protocol v1.4 Windows clang-cl coverage publishes and opens a failed TestRun report", async (t) => {
  assert.equal(process.platform, "win32", "coverage service smoke is Windows-only");
  await rm(evidencePath, { force: true });
  let fixture: Fixture | undefined;
  let manager: ServiceManager | undefined;
  let offlineBoundary: WindowsNativeOfflineBoundary | undefined;
  const wire = new ProtocolWireCapture();
  const tokens: string[] = [];
  const hostileEnvironmentValue = `coverage-smoke-secret-${randomBytes(12).toString("hex")}`;
  const sensitive: string[] = [hostileEnvironmentValue];

  const stopService = async (): Promise<void> => {
    if (manager === undefined) return;
    await manager.stop();
    manager = undefined;
  };
  const cleanupFixture = async (): Promise<void> => {
    if (fixture === undefined) return;
    await rm(fixture.root, { recursive: true, force: true });
    fixture = undefined;
  };
  const closeOfflineBoundary = async (): Promise<void> => {
    if (offlineBoundary === undefined) return;
    await offlineBoundary.close();
    offlineBoundary = undefined;
  };

  t.after(async () => {
    try {
      await teardownThenPublish(
        [stopService, cleanupFixture, closeOfflineBoundary],
        async () => undefined
      );
    } catch (error) {
      throw redactServiceError(error, sensitive);
    }
  });

  try {
    fixture = await createFixture();
    sensitive.push(
      fixture.root,
      fixture.workspace,
      fixture.dataDirectory,
      fixture.serviceBinary,
      fixture.goCache,
      cmakeBundleRoot
    );
    const useCMakeBundle = await pathExists(cmakeBundleRoot);
    if (!useCMakeBundle) {
      if (requiredClangCL()) {
        throw new Error("required verified clang-cl coverage toolset is unavailable");
      }
      t.skip(SKIP_MESSAGE);
      return;
    }
    await buildService(fixture);
    offlineBoundary = await installWindowsNativeOfflineBoundary();
    manager = new ServiceManager({
      serviceExecutable: fixture.serviceBinary,
      workspaceRoot: fixture.workspace,
      dataDirectory: fixture.dataDirectory,
      timeoutMs: 120_000,
      trusted: () => true,
      operations: coverageOperations(
        fixture,
        wire,
        tokens,
        hostileEnvironmentValue,
        useCMakeBundle
      )
    });
    const session = await manager.start();
    sensitive.push(session.endpoint, session.tokenFile, session.sessionDirectory, ...tokens);
    wire.reset();

    const capabilities = await session.client.getCapabilities();
    assert.equal("coverageRun" in capabilities && capabilities.coverageRun, true);
    assert.equal("coverageReport" in capabilities && capabilities.coverageReport, true);
    const initial = await inspectSelected(session.client);
    if (initial === undefined) {
      if (requiredClangCL()) {
        throw new Error("required verified clang-cl coverage toolset is unavailable");
      }
      t.skip(SKIP_MESSAGE);
      return;
    }

    await writeWorkspaceConfig(fixture.workspace, initial.profile.buildProfileId);
    const configured = await inspectSelected(session.client, initial.toolchain.toolchainId);
    if (configured === undefined) throw new Error("configured clang-cl coverage profile is unavailable");
    assert.equal(configured.profile.buildProfileId, initial.profile.buildProfileId);
    let selected = await buildBaseProfile(session.client, configured);
    const catalog = await discoverCatalog(session.client, selected);
    selected = (await inspectSelected(session.client, selected.toolchain.toolchainId)) ?? selected;

    // Workspace inspection intentionally carries workspace URIs. The leak gate
    // below is scoped to the coverage request/run/report/artifact exchange,
    // whose public contract must never carry native execution paths.
    wire.reset();
    const coverageStartedAt = Date.now();
    const request: CoverageRunInput = {
      idempotencyKey: randomBytes(16).toString("hex"),
      workspaceGeneration: selected.snapshot.workspaceGeneration,
      projectId: selected.projectId,
      coverageProfileId: COVERAGE_PROFILE_ID,
      catalogRevision: catalog.revision,
      selection: { mode: TestSelectionModeV14.All },
      repeatCount: 1,
      timeoutMs: NATIVE_TIMEOUT_MS
    };
    const started = await session.client.startCoverage(request);
    const run = await waitForCoverageFinished(session.client, started);
    const durationMs = Date.now() - coverageStartedAt;
    assert.equal(run.status, "finished");
    assert.equal(run.outcome, "available");
    assert.equal(run.reason, undefined);
    assert.ok(run.reportId);

    const testRun = await session.client.getTestRun(run.testRunId);
    assert.equal(testRun.status, "completed");
    assert.equal(testRun.outcome, "failed");
    assert.equal(testRun.incomplete, false);
    assert.deepEqual(
      {
        total: testRun.summary.total,
        completed: testRun.summary.completed,
        passed: testRun.summary.passed,
        failed: testRun.summary.failed
      },
      { total: 2, completed: 2, passed: 1, failed: 1 }
    );

    const report = await session.client.getCoverageReport(run.reportId);
    assert.equal(report.coverageRunId, run.coverageRunId);
    assert.equal(report.testRunId, run.testRunId);
    assert.equal(report.completeness.outcome, "available");
    assert.deepEqual(report.completeness.reasons, []);
    assert.ok(report.summary.lines.covered > 0 && report.summary.lines.total > 0);
    assert.ok(report.summary.branches.covered > 0);
    assert.ok(report.summary.branches.covered < report.summary.branches.total);
    assert.ok(report.summary.functions.covered > 0 && report.summary.functions.total > 0);

    const artifacts = await fetchPublicArtifacts(session.client, run.taskId);
    assert.equal(artifactByKind(artifacts, "coverage-json").metadata.artifactId, report.artifactId);
    const document = decodeCoverageJSON(artifactByKind(artifacts, "coverage-json"));
    assert.deepEqual(document.summary, report.summary);
    assert.equal(document.files.some((file) => file.uri === "src/math.cpp"), true);
    assert.equal(document.files.some((file) => file.uri.startsWith("test/")), false);
    const junitArtifact = artifactByKind(artifacts, "junit-xml");
    assert.equal(junitArtifact.kind, "junit-xml");
    const junit = parseStrictJUnit(junitArtifact.bytes);
    assert.deepEqual(junit, { tests: 2, failures: 1, errors: 0, skipped: 0 });

    let openedHTML = "";
    await openCoverageHtml(
      { openCoverageHtml: (html) => { openedHTML = html; } },
      {
        kind: "coverage-html",
        bytes: artifactByKind(artifacts, "coverage-html").bytes
      }
    );
    assert.match(openedHTML, /Content-Security-Policy/u);
    assert.match(openedHTML, /default-src 'none'/u);
    assert.doesNotMatch(openedHTML, /https?:\/\//iu);

    const toolVersion = assertProvenance(report, document);
    assert.equal(toolVersion, selected.toolchain.version);
    const coverageController = createCoverageController({
      readContext: () => ({
        trust: "trusted",
        client: session.client,
        serviceRunning: true,
        workspaceGeneration: selected.snapshot.workspaceGeneration,
        catalog: {
          projectId: selected.projectId,
          profileId: selected.profile.buildProfileId,
          revision: catalog.revision,
          workspaceGeneration: selected.snapshot.workspaceGeneration
        },
        coverageProfileId: COVERAGE_PROFILE_ID
      })
    });
    try {
      const extensionState = await coverageController.refresh(run.coverageRunId);
      assert.equal(extensionState.state, "available");
      assert.equal(extensionState.reportId, report.reportId);
      assert.deepEqual(extensionState.summary, report.summary);
    } finally {
      coverageController.dispose();
    }

    const protocolBytes = wire.bytes();
    assertNoSensitiveBytes("Protocol v1.4 coverage exchange", protocolBytes, sensitive);
    for (const artifact of artifacts) {
      assertNoSensitiveBytes(`${artifact.kind} report`, artifact.bytes, sensitive);
    }

    const evidence = buildEvidence(report, artifacts, durationMs);
    const expectedEvidenceBytes = Buffer.from(`${JSON.stringify(evidence)}\n`, "utf8");
    assertNoSensitiveBytes("coverage execution evidence", expectedEvidenceBytes, sensitive);
    await teardownThenPublish(
      [stopService, cleanupFixture, closeOfflineBoundary],
      () => publishEvidenceAtomically(evidencePath, expectedEvidenceBytes)
    );
  } catch (error) {
    throw redactServiceError(error, sensitive);
  }
});
