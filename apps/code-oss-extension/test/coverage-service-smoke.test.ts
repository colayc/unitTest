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
import { buildWfpOfflineReport } from "@unit-test-ide/service-probe/coverage-bundle";
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
  executeCoverageServiceSmoke,
  parseStrictJUnit,
  publishEvidenceAtomically,
  runAfterVerifiedCoverageToolsetPreflight,
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
const firewallGuardianStateRoot = join(
  repositoryRoot,
  ".native-e2e",
  "runtime",
  "windows-firewall-guardians"
);
const COVERAGE_PROFILE_ID = "coverage-clang-cl";
const PROJECT_ID = "coverage-fixture";
const TEST_CONTAINER = "coverage-tests";
const NATIVE_TIMEOUT_MS = 180_000;

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly dataDirectory: string;
  readonly serviceBinary: string;
  readonly toolsetPreflightBinary: string;
  readonly guardianBinary: string;
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
  readonly outcome: "passed";
  readonly reason: "None";
  readonly toolchainDigest: string;
  readonly guardianOutcome: "released";
  readonly filterAuditOutcome: "passed";
  readonly startedAt: string;
  readonly finishedAt: string;
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
      toolsetPreflightBinary: join(root, "coverage-toolset-preflight.exe"),
      guardianBinary: join(root, "native-offline-guardian.exe"),
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
  await execFile(
    executable,
    [
      "build",
      "-trimpath",
      "-o",
      fixture.toolsetPreflightBinary,
      "./apps/test-service/cmd/coverage-toolset-preflight"
    ],
    {
      cwd: repositoryRoot,
      env: environment,
      timeout: NATIVE_TIMEOUT_MS,
      windowsHide: true,
      maxBuffer: 16 * 1024 * 1024
    }
  );
  await execFile(
    executable,
    [
      "build",
      "-trimpath",
      "-o",
      fixture.guardianBinary,
      "./apps/test-service/cmd/native-offline-guardian"
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

async function preflightCoverageToolset(fixture: Fixture): Promise<
  { readonly status: "unavailable"; readonly digest: string } |
  { readonly status: "verified"; readonly version: string; readonly digest: string }
> {
  const result = await execFile(fixture.toolsetPreflightBinary, [], {
    cwd: repositoryRoot,
    encoding: "utf8",
    timeout: 90_000,
    windowsHide: true,
    maxBuffer: 64 * 1024
  });
  assert.equal(result.stderr, "", "coverage toolset preflight must not emit diagnostics");
  assert.match(result.stdout, /^\{[^\r\n]+\}\n$/u, "coverage toolset preflight must emit one JSON object");
  const parsed = JSON.parse(result.stdout) as Record<string, unknown>;
  assert.equal(parsed.schemaVersion, 1);
  assert.equal(parsed.platform, "windows");
  assert.equal(parsed.architecture, "x64");
  if (parsed.status === "unavailable") {
    assert.deepEqual(Object.keys(parsed).sort(), [
      "architecture", "platform", "schemaVersion", "status"
    ]);
    const digest = createHash("sha256").update(JSON.stringify({
      schemaVersion: 1,
      platform: "windows",
      architecture: "x64",
      status: "unavailable"
    }), "utf8").digest("hex");
    return { status: "unavailable", digest };
  }
  assert.equal(parsed.status, "verified");
  assert.deepEqual(Object.keys(parsed).sort(), [
    "architecture", "platform", "schemaVersion", "status", "toolchainDigest", "version"
  ]);
  assert.equal(typeof parsed.version, "string");
  assert.match(parsed.version as string, /^[0-9]+\.[0-9]+(?:\.[0-9]+)?$/u);
  assert.equal(typeof parsed.toolchainDigest, "string");
  assert.match(parsed.toolchainDigest as string, /^[0-9a-f]{64}$/u);
  return {
    status: "verified",
    version: parsed.version as string,
    digest: parsed.toolchainDigest as string
  };
}

function coverageOperations(
  fixture: Fixture,
  wire: ProtocolWireCapture,
  tokens: string[],
  hostileEnvironmentValue: string,
  useCMakeBundle: boolean,
  boundaryEnvironment: Readonly<Record<string, string>>
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
          ...boundaryEnvironment,
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
  toolchainDigest: string,
  startedAt: Date,
  finishedAt: Date
): CoverageExecutionEvidence {
  const evidence = {
    schemaVersion: 1,
    outcome: "passed",
    reason: "None",
    toolchainDigest,
    guardianOutcome: "released",
    filterAuditOutcome: "passed",
    startedAt: startedAt.toISOString(),
    finishedAt: finishedAt.toISOString(),
  } satisfies CoverageExecutionEvidence;
  buildWfpOfflineReport(evidence);
  assert.deepEqual(Object.keys(evidence).sort(), [
    "filterAuditOutcome",
    "finishedAt",
    "guardianOutcome",
    "outcome",
    "reason",
    "schemaVersion",
    "startedAt",
    "toolchainDigest"
  ]);
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
        [stopService, closeOfflineBoundary, cleanupFixture],
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
      fixture.toolsetPreflightBinary,
      fixture.guardianBinary,
      fixture.goCache,
      cmakeBundleRoot
    );
    await buildService(fixture);
    const gate = await runAfterVerifiedCoverageToolsetPreflight({
      required: requiredClangCL(),
      preflight: () => preflightCoverageToolset(fixture!),
      skip: (message) => t.skip(message),
      async installBoundary() {
        offlineBoundary = await installWindowsNativeOfflineBoundary({
          stateRoot: firewallGuardianStateRoot,
          nativeExecutablePath: fixture!.serviceBinary
        });
        return offlineBoundary;
      },
      async execute(_boundary, toolset) {
        return toolset;
      }
    });
    if (gate.status === "skipped") return;
    const verifiedToolset = gate.value;
    const installedBoundary = offlineBoundary;
    if (installedBoundary === undefined) {
      throw new Error("verified coverage execution has no WFP boundary");
    }
    const installedFixture = fixture;
    if (installedFixture === undefined) {
      throw new Error("verified coverage execution has no fixture");
    }
    await executeCoverageServiceSmoke({
      boundary: installedBoundary,
      async execute(boundarySignal) {
        boundarySignal.throwIfAborted();
        const useCMakeBundle = await pathExists(cmakeBundleRoot);
        assert.equal(useCMakeBundle, true, "prepared CMake bundle root is unavailable");
        manager = new ServiceManager({
          serviceExecutable: installedFixture.serviceBinary,
          workspaceRoot: installedFixture.workspace,
          dataDirectory: installedFixture.dataDirectory,
          timeoutMs: 120_000,
          trusted: () => true,
          operations: coverageOperations(
            installedFixture,
            wire,
            tokens,
            hostileEnvironmentValue,
            useCMakeBundle,
            installedBoundary.registrationEnvironment
          )
        });
        const session = await manager.start();
        boundarySignal.throwIfAborted();
        sensitive.push(session.endpoint, session.tokenFile, session.sessionDirectory, ...tokens);
        wire.reset();

        const capabilities = await session.client.getCapabilities();
        assert.equal("coverageRun" in capabilities && capabilities.coverageRun, true);
        assert.equal("coverageReport" in capabilities && capabilities.coverageReport, true);
        const initial = await inspectSelected(session.client);
        if (initial === undefined) {
          throw new Error("preflight-verified clang-cl coverage toolset disappeared before inspection");
        }
        assert.equal(initial.toolchain.version, verifiedToolset.version);

        await writeWorkspaceConfig(installedFixture.workspace, initial.profile.buildProfileId);
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
        const coverageStartedAt = new Date();
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
        const coverageFinishedAt = new Date();
        assert.equal(run.status, "finished");
        const testRun = await session.client.getTestRun(run.testRunId);
        if (run.outcome !== "available") {
          console.error("coverage-debug-status", JSON.stringify({
            outcome: run.outcome,
            reason: run.reason,
            manager: manager?.status,
            testRun: {
              status: testRun.status,
              outcome: testRun.outcome,
              incomplete: testRun.incomplete,
              summary: testRun.summary
            }
          }));
        }
        assert.equal(run.outcome, "available");
        assert.equal(run.reason, undefined);
        assert.ok(run.reportId);

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

        const evidence = buildEvidence(verifiedToolset.digest, coverageStartedAt, coverageFinishedAt);
        const expectedEvidenceBytes = Buffer.from(`${JSON.stringify(evidence)}\n`, "utf8");
        assertNoSensitiveBytes("coverage execution evidence", expectedEvidenceBytes, sensitive);
        boundarySignal.throwIfAborted();
        return expectedEvidenceBytes;
      },
      stopService,
      closeOfflineBoundary,
      cleanupFixture,
      publish: (bytes) => publishEvidenceAtomically(evidencePath, bytes)
    });
  } catch (error) {
    throw redactServiceError(error, sensitive);
  }
});
