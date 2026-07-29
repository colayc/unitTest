import assert from "node:assert/strict";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import {
  EventSubscription,
  type ProtocolClient,
  type ProtocolTaskEvent,
  type ProtocolTaskSnapshot
} from "@unit-test-ide/test-client";
import { endpointForDirectory } from "./endpoint.js";
import { assertProcessGone, prepareTokenFile, runProbe, startService, startTaskService, withNamedTimeout } from "./probe.js";

const root = resolve(import.meta.dirname, "../../..");
const binary = join(root, "build", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
const cmakeFixture = join(root, "build", process.platform === "win32" ? "cmake-fixture.exe" : "cmake-fixture");
const EVENT_TIMEOUT_MS = 8_000;
const WORKSPACE_INSPECTION_TIMEOUT_MS = process.platform === "win32" ? 60_000 : 30_000;
const V11_EVENT_NAMES = new Set([
  "task.created",
  "task.started",
  "task.output",
  "task.cancellation_requested",
  "task.finished",
  "artifact.created"
]);
const V12_EVENT_NAMES = new Set([
  ...V11_EVENT_NAMES,
  "task.step_started",
  "task.step_finished",
  "task.diagnostic"
]);

test("Unix endpoint stays within sockaddr_un when the workspace path is long", async () => {
  const longWorkspace = `/home/runner/work/${"repository-".repeat(16)}/${"repository-".repeat(16)}/build`;
  const resource = await endpointForDirectory(longWorkspace, "linux", async () => "/tmp/utide-123456");
  assert.ok(Buffer.byteLength(resource.path, "utf8") + 1 <= 108);
  assert.equal(resource.path, "/tmp/utide-123456/s");
});

test("Windows endpoint remains an isolated Named Pipe", async () => {
  let temporaryDirectoryRequested = false;
  const resource = await endpointForDirectory("C:\\a".repeat(100), "win32", async () => {
    temporaryDirectoryRequested = true;
    return "unused";
  });
  assert.match(resource.path, /^\\\\\.\\pipe\\unit-test-ide-[0-9a-f-]{36}$/);
  assert.equal(resource.directory, undefined);
  assert.equal(temporaryDirectoryRequested, false);
});

test("named timeout bounds a pending RPC with a safe operation label", async () => {
  await assert.rejects(
    withNamedTimeout("secondary task lookup", new Promise<never>(() => undefined), 20),
    /secondary task lookup timed out after 20ms/
  );
});

test("service fixture bounds pending token preparation", async () => {
  const directory = await mkdtemp(join(dirname(binary), "unit-test-ide-timeout-"));
  try {
    await assert.rejects(
      startService(binary, directory, {
        timeoutMs: 20,
        operations: { prepareTokenFile: () => new Promise<void>(() => undefined) }
      }),
      /token file preparation timed out after 20ms/
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("owned startup timeout ignores late token preparation and removes its fixture directory", async () => {
  let fixtureDirectory: string | undefined;
  let spawnCalled = false;

  await assert.rejects(
    startTaskService(binary, {
      timeoutMs: 100,
      operations: {
        prepareTokenFile: async (_serviceBinary: string, tokenFile: string) => {
          fixtureDirectory = dirname(tokenFile);
          await new Promise((resolveDelay) => setTimeout(resolveDelay, 300));
        },
        spawnService: () => {
          spawnCalled = true;
          throw new Error("late token preparation must not reach spawn");
        }
      }
    }),
    /token file preparation timed out after 100ms/
  );

  await new Promise((resolveDelay) => setTimeout(resolveDelay, 350));
  assert.equal(spawnCalled, false);
  assert.ok(fixtureDirectory);
  await assert.rejects(
    readFile(fixtureDirectory),
    (error: unknown) => (error as NodeJS.ErrnoException).code === "ENOENT"
  );
});

test("dispose serializes with an in-flight restart and cleans the restarted service", async () => {
  let fixtureDirectory: string | undefined;
  let handshakeCount = 0;
  let enterRestartHandshake!: () => void;
  let releaseRestartHandshake!: () => void;
  const restartHandshakeEntered = new Promise<void>((resolveEntered) => { enterRestartHandshake = resolveEntered; });
  const restartHandshakeReleased = new Promise<void>((resolveReleased) => { releaseRestartHandshake = resolveReleased; });
  const children: Array<{ child: ChildProcessWithoutNullStreams; exit: Promise<void> }> = [];
  let gateReleased = false;
  const releaseGate = () => {
    if (gateReleased) return;
    gateReleased = true;
    releaseRestartHandshake();
  };

  let fixture: Awaited<ReturnType<typeof startTaskService>> | undefined;
  try {
    fixture = await startTaskService(binary, {
      timeoutMs: 2_000,
      operations: {
        spawnService: (serviceBinary, args) => {
          const child = spawn(serviceBinary, args, { windowsHide: true });
          const exit = new Promise<void>((resolveExit) => { child.once("exit", () => resolveExit()); });
          children.push({ child, exit });
          const dataDirectoryIndex = args.indexOf("--data-dir");
          const dataDirectory = args[dataDirectoryIndex + 1];
          assert.ok(dataDirectory);
          fixtureDirectory = dirname(dataDirectory);
          return child;
        },
        handshakeClient: async (client, token) => {
          handshakeCount++;
          if (handshakeCount === 2) {
            enterRestartHandshake();
            await restartHandshakeReleased;
          }
          return client.handshake(token, "service-probe", "0.1.0");
        }
      }
    });
    await fixture.stopGracefully();

    const restarting = fixture.restart();
    await withNamedTimeout("restart handshake gate", restartHandshakeEntered, 2_000);
    const restartChild = children.at(-1)?.child;
    const restartPID = restartChild?.pid;
    assert.ok(restartPID);

    const disposing = fixture.dispose();
    releaseGate();
    const [restartResult, disposeResult] = await Promise.allSettled([restarting, disposing]);
    assert.equal(
      disposeResult.status,
      "fulfilled",
      disposeResult.status === "rejected" ? `dispose rejected: ${String(disposeResult.reason)}` : undefined
    );
    if (restartResult.status === "rejected") {
      assert.match(String(restartResult.reason), /disposed/);
    }
    await assertProcessGone(restartPID);
    assert.ok(fixtureDirectory);
    await assert.rejects(
      readFile(fixtureDirectory),
      (error: unknown) => (error as NodeJS.ErrnoException).code === "ENOENT"
    );
    await fixture.dispose();
  } finally {
    releaseGate();
    for (const { child } of children) {
      if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    }
    await Promise.all(children.map(({ exit }) => withNamedTimeout("test child cleanup", exit, 8_000)));
    if (fixtureDirectory) {
      await rm(fixtureDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
    }
  }
});

test("service fixture bounds a pending handshake RPC and cleans up its process", async () => {
  const directory = await mkdtemp(join(dirname(binary), "unit-test-ide-rpc-timeout-"));
  let servicePID: number | undefined;
  try {
    await assert.rejects(
      startService(binary, directory, {
        timeoutMs: 500,
        operations: {
          spawnService: (serviceBinary, args) => {
            const child = spawn(serviceBinary, args, { windowsHide: true });
            servicePID = child.pid;
            return child;
          },
          handshakeClient: () => new Promise<never>(() => undefined)
        }
      }),
      /task protocol handshake timed out after 500ms/
    );
    assert.ok(servicePID);
    await assertProcessGone(servicePID);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

function serializeErrorTree(value: unknown, seen = new Set<unknown>()): string {
  if (!(value instanceof Error)) return JSON.stringify(value);
  if (seen.has(value)) return "<cycle>";
  seen.add(value);
  return JSON.stringify({
    name: value.name,
    message: value.message,
    stack: value.stack,
    cause: serializeErrorTree(value.cause, seen)
  });
}

test("token preparation failures recursively redact nested diagnostics", async () => {
  const directory = await mkdtemp(join(dirname(binary), "unit-test-ide-diagnostic-"));
  const environmentSentinel = "UNIT_TEST_SECRET_ENV=do-not-print";
  let generatedToken = "";
  try {
    await assert.rejects(
      startService(binary, directory, {
        operations: {
          prepareTokenFile: async (serviceBinary, tokenFile, token) => {
            generatedToken = token;
            const inner = new Error(`inner ${serviceBinary} ${tokenFile} ${token} ${environmentSentinel}`);
            throw new Error(`outer ${directory}`, { cause: inner });
          }
        }
      }),
      (error: unknown) => {
        const serialized = serializeErrorTree(error);
        for (const sensitive of [binary, directory, generatedToken, environmentSentinel]) {
          assert.equal(serialized.includes(sensitive), false, `diagnostics leaked ${sensitive}`);
        }
        assert.equal((error as Error).cause, undefined);
        return true;
      }
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

async function assertLaunchStageRedacted(
  stage: string,
  operations: NonNullable<Parameters<typeof startService>[2]>["operations"],
  captured: string[],
  environmentSentinel: string
): Promise<void> {
  const directory = await mkdtemp(join(dirname(binary), `unit-test-ide-${stage}-`));
  captured.push(directory, binary);
  try {
    await assert.rejects(
      startService(binary, directory, { operations }),
      (error: unknown) => {
        const serialized = serializeErrorTree(error);
        for (const sensitive of [...captured, environmentSentinel]) {
          assert.equal(serialized.includes(sensitive), false, `${stage} diagnostics leaked ${sensitive}`);
        }
        assert.equal((error as Error).cause, undefined);
        return true;
      }
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
}

test("spawn failures recursively redact nested diagnostics", async () => {
  const captured: string[] = [];
  const environmentSentinel = "SPAWN_SECRET_ENV=do-not-print";
  await assertLaunchStageRedacted("spawn", {
    spawnService: (serviceBinary, args) => {
      captured.push(serviceBinary, ...args);
      throw new Error(`spawn outer ${environmentSentinel}`, {
        cause: new Error(`spawn inner ${args.join(" ")}`)
      });
    }
  }, captured, environmentSentinel);
});

test("service launch forwards workspace trust and CMake options as isolated arguments", async () => {
  const directory = await mkdtemp(join(dirname(binary), "unit-test-ide-options-"));
  const workspaceRoot = join(directory, "workspace");
  const cmakeBundleRoot = join(directory, "cmake-bundle");
  const devCMakeExecutable = join(directory, "cmake-dev");
  let checked = false;
  try {
    await assert.rejects(
      startService(binary, directory, {
        workspaceRoot,
        trustedWorkspace: true,
        cmakeBundleRoot,
        devCMakeExecutable,
        operations: {
          spawnService: (_serviceBinary, args) => {
            assert.deepEqual(args.slice(-7), [
              "--workspace-root", workspaceRoot,
              "--trusted-workspace=true",
              "--cmake-bundle-root", cmakeBundleRoot,
              "--dev-cmake-executable", devCMakeExecutable
            ]);
            checked = true;
            throw new Error(`expected launch stop ${workspaceRoot} ${cmakeBundleRoot} ${devCMakeExecutable}`);
          }
        }
      }),
      (error: unknown) => {
        const serialized = serializeErrorTree(error);
        for (const sensitive of [workspaceRoot, cmakeBundleRoot, devCMakeExecutable]) {
          assert.equal(serialized.includes(sensitive), false);
        }
        return true;
      }
    );
    assert.equal(checked, true);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("connect failures recursively redact nested diagnostics", async () => {
  const captured: string[] = [];
  const environmentSentinel = "CONNECT_SECRET_ENV=do-not-print";
  await assertLaunchStageRedacted("connect", {
    connectClient: async (serviceEndpoint) => {
      captured.push(serviceEndpoint);
      throw new Error(`connect outer ${environmentSentinel}`, {
        cause: new Error(`connect inner ${serviceEndpoint}`)
      });
    }
  }, captured, environmentSentinel);
});

test("handshake failures recursively redact nested diagnostics", async () => {
  const captured: string[] = [];
  const environmentSentinel = "HANDSHAKE_SECRET_ENV=do-not-print";
  await assertLaunchStageRedacted("handshake", {
    handshakeClient: async (_client, token, serviceEndpoint) => {
      captured.push(token, serviceEndpoint);
      throw new Error(`handshake outer ${environmentSentinel}`, {
        cause: new Error(`handshake inner ${token} ${serviceEndpoint}`)
      });
    }
  }, captured, environmentSentinel);
});

function eventWithin(subscription: EventSubscription, label: string): Promise<IteratorResult<ProtocolTaskEvent>> {
  return withNamedTimeout(label, subscription.next(), EVENT_TIMEOUT_MS);
}

async function waitForEvent(
  subscription: EventSubscription,
  taskId: string,
  predicate: (event: ProtocolTaskEvent) => boolean,
  label: string,
  seen: ProtocolTaskEvent[]
): Promise<ProtocolTaskEvent> {
  for (;;) {
    const next = await eventWithin(subscription, label);
    if (next.done) throw new Error(`${label} ended before the expected event`);
    seen.push(next.value);
    if (next.value.taskId === taskId && predicate(next.value)) return next.value;
  }
}

async function waitForChildPID(
  subscription: EventSubscription,
  taskId: string,
  seen: ProtocolTaskEvent[]
): Promise<number> {
  let output = "";
  for (;;) {
    const event = await waitForEvent(subscription, taskId, (value) => value.event === "task.output", "child PID event", seen);
    const payload = event.payload as { text?: unknown };
    output += typeof payload.text === "string" ? payload.text : "";
    const match = /(?:^|\n)CHILD_PID=(\d+)(?:\n|$)/.exec(output);
    if (!match?.[1]) continue;
    const pid = Number(match[1]);
    if (Number.isSafeInteger(pid) && pid > 0) return pid;
    throw new Error("child PID event contained an invalid PID");
  }
}

async function waitForFinished(
  subscription: EventSubscription,
  taskId: string,
  seen: ProtocolTaskEvent[]
): Promise<ProtocolTaskEvent> {
  return waitForEvent(subscription, taskId, (event) => event.event === "task.finished", "task finished event", seen);
}

async function collectThroughSequence(
  subscription: EventSubscription,
  sequence: number,
  afterSequence = 0,
  label = "event replay"
): Promise<ProtocolTaskEvent[]> {
  const events: ProtocolTaskEvent[] = [];
  let lastConsumedSequence = afterSequence;
  while (lastConsumedSequence < sequence) {
    const next = await eventWithin(subscription, label);
    if (next.done) throw new Error(`${label} ended before the target sequence`);
    assert.equal(next.value.sequence, lastConsumedSequence + 1, `${label} sequence must be continuous`);
    events.push(next.value);
    lastConsumedSequence = next.value.sequence;
  }
  return events;
}

function queuedTaskEvent(sequence: number): ProtocolTaskEvent {
  return {
    event: sequence === 4 ? "task.finished" : "task.output",
    kind: "event",
    messageId: `${sequence}`.padStart(32, "0"),
    payload: sequence === 4 ? { outcome: "interrupted" } : { text: `${sequence}` },
    payloadVersion: 1,
    protocolVersion: "1.1",
    sentAt: new Date("2026-07-23T00:00:00.000Z"),
    sequence,
    taskId: "1".repeat(32)
  } as ProtocolTaskEvent;
}

test("collector consumes queued replay through the stable target sequence", async () => {
  const subscription = new EventSubscription(0);
  for (let sequence = 1; sequence <= 4; sequence++) assert.equal(subscription.push(queuedTaskEvent(sequence)), true);
  assert.equal(subscription.lastSequence, 4, "the receive watermark must already be ahead of consumption");
  const events = await collectThroughSequence(subscription, 4);
  assert.deepEqual(events.map((event) => event.sequence), [1, 2, 3, 4]);
});

function assertContinuousUniqueSequences(events: ProtocolTaskEvent[]): void {
  assert.ok(events.length > 0);
  assert.equal(new Set(events.map((event) => event.sequence)).size, events.length, "event sequences must be unique");
  for (let index = 1; index < events.length; index++) {
    assert.equal(events[index]?.sequence, (events[index - 1]?.sequence ?? 0) + 1, "event sequences must have no gaps");
  }
}

function assertSimulationProtocolEvents(events: ProtocolTaskEvent[], afterSequence = 0): void {
  assertContinuousUniqueSequences(events);
  assert.equal(events[0]?.sequence, afterSequence + 1, "event replay must start immediately after its cursor");
  const version = events[0]?.protocolVersion;
  for (const event of events) {
    assert.equal(event.protocolVersion, version, "event replay mixed protocol versions");
    const allowedNames = version === "1.2" ? V12_EVENT_NAMES : V11_EVENT_NAMES;
    assert.equal(allowedNames.has(event.event), true, `${version} leaked event type ${event.event}`);
    if (event.event !== "task.output") continue;
    const expectedKeys = version === "1.2"
      ? ["stepId", "stream", "text", "truncated"]
      : ["stream", "text", "truncated"];
    assert.deepEqual(
      Object.keys(event.payload).sort(),
      expectedKeys,
      `${version} task.output sequence ${event.sequence} changed its exact payload`
    );
    const payload = event.payload as { stream?: unknown; text?: unknown; truncated?: unknown };
    assert.equal(typeof payload.stream, "string");
    assert.equal(typeof payload.text, "string");
    assert.equal(typeof payload.truncated, "boolean");
  }
}

function assertSimulationSnapshot(snapshot: ProtocolTaskSnapshot): void {
  assert.equal(snapshot.kind, "simulation");
  for (const field of ["activeStep", "steps", "workspaceGeneration", "planFingerprint", "request"]) {
    assert.equal(field in snapshot, false, `simulation Task Snapshot leaked ${field}`);
  }
}

function assertInterrupted(snapshot: ProtocolTaskSnapshot): void {
  assertSimulationSnapshot(snapshot);
  assert.equal(snapshot.status, "finished");
  assert.equal(snapshot.outcome, "interrupted");
}

test("prepares the token file before writing the secret", async () => {
  const directory = await mkdtemp(join(tmpdir(), "unit-test-ide-token-"));
  const tokenFile = join(directory, "token");
  const token = "0123456789abcdef0123456789abcdef";
  try {
    await prepareTokenFile(binary, tokenFile, token);
    assert.equal(await readFile(tokenFile, "utf8"), token);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("probe authenticates, reads capabilities, and shuts the service down", async () => {
  const capabilities = await runProbe(binary);
  assert.deepEqual(capabilities, { workspaceInspect: true, targetList: true, cmakeBuild: true });
});

test("successful simulation preserves the complete negotiated event stream", async () => {
  const fixture = await startTaskService(binary);
  try {
    const subscription = await withNamedTimeout(
      "success event subscription",
      fixture.client.subscribeEvents(0),
      EVENT_TIMEOUT_MS
    );
    const started = await withNamedTimeout(
      "successful task start",
      fixture.client.startTask({
        idempotencyKey: randomBytes(16).toString("hex"),
        scenario: "success",
        timeoutMs: 30_000
      }),
      EVENT_TIMEOUT_MS
    );
    assertSimulationSnapshot(started);

    const seen: ProtocolTaskEvent[] = [];
    const finished = await waitForFinished(subscription, started.taskId, seen);
    assert.equal((finished.payload as { outcome?: unknown }).outcome, "succeeded");
    assertSimulationProtocolEvents(seen);
    assert.deepEqual(
      seen.filter((event) =>
        event.event === "task.step_started" ||
        event.event === "task.step_finished" ||
        event.event === "task.diagnostic"
      ),
      [],
      "simulation tasks must not expose CMake step or diagnostic events"
    );
    const artifactIndex = seen.findIndex((event) => event.event === "artifact.created");
    const finishedIndex = seen.findIndex((event) => event.event === "task.finished");
    assert.ok(artifactIndex >= 0 && artifactIndex < finishedIndex, "artifact.created must precede task.finished");

    const durable = await withNamedTimeout(
      "successful durable task lookup",
      fixture.client.getTask(started.taskId),
      EVENT_TIMEOUT_MS
    );
    assertSimulationSnapshot(durable);
    assert.equal(durable.status, "finished");
    assert.equal(durable.outcome, "succeeded");
    assert.equal(durable.lastSequence, seen.at(-1)?.sequence);
  } finally {
    await fixture.dispose();
  }
});

test("trusted workspace completes deterministic CMake builds and skips the second configure", async () => {
  const workspaceDirectory = await mkdtemp(join(dirname(binary), "unit-test-ide-cmake-workspace-"));
  const serviceDirectory = await mkdtemp(join(dirname(binary), "unit-test-ide-cmake-service-"));
  let fixture: Awaited<ReturnType<typeof startService>> | undefined;
  let stage = "prepare workspace";
  try {
    await mkdir(join(workspaceDirectory, ".unit-test-ide"), { recursive: true });
    await writeFile(
      join(workspaceDirectory, ".unit-test-ide", "workspace.json"),
      JSON.stringify({
        version: 1,
        projects: [{
          id: "root",
          sourceDir: ".",
          fallback: { configurations: ["Debug"] }
        }]
      })
    );
    await writeFile(
      join(workspaceDirectory, "CMakeLists.txt"),
      "cmake_minimum_required(VERSION 3.25)\nproject(fixture LANGUAGES CXX)\nadd_executable(fixture-app main.cpp)\n"
    );
    await writeFile(join(workspaceDirectory, "main.cpp"), "int main() { return 0; }\n");
    await writeFile(
      join(workspaceDirectory, "CMakePresets.json"),
      JSON.stringify({
        version: 6,
        configurePresets: [{
          name: "fixture",
          generator: "Ninja",
          binaryDir: "${sourceDir}/build-fixture"
        }]
      })
    );

    stage = "start trusted service";
    fixture = await startService(binary, serviceDirectory, {
      timeoutMs: WORKSPACE_INSPECTION_TIMEOUT_MS,
      workspaceRoot: workspaceDirectory,
      trustedWorkspace: true,
      devCMakeExecutable: cmakeFixture
    });
    stage = "inspect workspace";
    const workspace = await withNamedTimeout(
      "deterministic workspace inspection",
      fixture.client.inspectWorkspace(),
      WORKSPACE_INSPECTION_TIMEOUT_MS
    );
    const project = workspace.projects.find((candidate) => candidate.projectId === "root");
    const profile = project?.buildProfiles[0];
    assert.ok(project, "fixture project must be inspectable");
    assert.ok(profile, "fixture project must have a verified platform toolchain profile");

    stage = "subscribe first build";
    const firstSubscription = await withNamedTimeout(
      "first CMake event subscription",
      fixture.client.subscribeEvents(0),
      EVENT_TIMEOUT_MS
    );
    stage = "start first build";
    const first = await withNamedTimeout(
      "first deterministic CMake build",
      fixture.client.startCMakeBuild({
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: workspace.workspaceGeneration,
        projectId: project.projectId,
        buildProfileId: profile.buildProfileId,
        targetIds: [],
        jobs: 2,
        timeoutMs: 30_000
      }),
      EVENT_TIMEOUT_MS
    );
    const firstEvents: ProtocolTaskEvent[] = [];
    stage = "wait for first build";
    const firstFinished = await waitForFinished(firstSubscription, first.taskId, firstEvents);
    assert.equal((firstFinished.payload as { outcome?: unknown }).outcome, "succeeded");
    assertContinuousUniqueSequences(firstEvents);
    assert.deepEqual(
      firstEvents
        .filter((event) => event.event === "task.step_started")
        .map((event) => (event.payload as { stepId?: unknown }).stepId),
      ["configure", "build"]
    );
    assert.deepEqual(
      firstEvents
        .filter((event) => event.event === "task.step_finished")
        .map((event) => (event.payload as { stepId?: unknown }).stepId),
      ["configure", "build"]
    );
    const diagnostics = firstEvents.filter((event) => event.event === "task.diagnostic");
    assert.ok(diagnostics.length >= 1, "build output must produce a diagnostic event");
    assert.equal(
      diagnostics.some((event) =>
        (event.payload as { diagnostic?: { severity?: unknown } }).diagnostic?.severity === "warning"
      ),
      true
    );

    stage = "list first artifacts";
    const artifacts = await withNamedTimeout(
      "first CMake artifact list",
      fixture.client.listArtifacts(first.taskId),
      EVENT_TIMEOUT_MS
    );
    assert.deepEqual(
      artifacts.items.map((artifact) => artifact.kind).sort(),
      ["build-summary", "diagnostics", "execution-plan", "stderr", "stdout"]
    );
    assert.deepEqual(
      firstEvents
        .filter((event) => event.event === "artifact.created")
        .map((event) => (event.payload as { kind?: unknown }).kind)
        .sort(),
      ["build-summary", "diagnostics", "execution-plan", "stderr", "stdout"]
    );
    stage = "read first artifacts";
    const artifactText = (
      await Promise.all(artifacts.items.map((artifact) => fixture!.client.readArtifact(artifact.artifactId)))
    ).map((bytes) => new TextDecoder().decode(bytes)).join("\n");
    for (const forbidden of ["UNIT_TEST_SERVICE_TOKEN", "UNIT_TEST_IDE_TOKEN", "\"env\"", "\"environment\""]) {
      assert.equal(artifactText.includes(forbidden), false, `artifact leaked ${forbidden}`);
    }

    stage = "list CMake targets";
    const targets = await withNamedTimeout(
      "deterministic CMake target list",
      fixture.client.listCMakeTargets({
        workspaceGeneration: workspace.workspaceGeneration,
        projectId: project.projectId,
        buildProfileId: profile.buildProfileId
      }),
      EVENT_TIMEOUT_MS
    );
    assert.deepEqual(targets.targets.map((target) => target.name), ["fixture-app"]);

    stage = "read first durable task";
    const firstDurable = await withNamedTimeout(
      "first deterministic durable task",
      fixture.client.getTask(first.taskId),
      EVENT_TIMEOUT_MS
    );
    stage = "subscribe second build";
    const secondSubscription = await withNamedTimeout(
      "second CMake event subscription",
      fixture.client.subscribeEvents(firstDurable.lastSequence),
      EVENT_TIMEOUT_MS
    );
    stage = "start second build";
    const second = await withNamedTimeout(
      "second deterministic CMake build",
      fixture.client.startCMakeBuild({
        idempotencyKey: randomBytes(16).toString("hex"),
        workspaceGeneration: workspace.workspaceGeneration,
        projectId: project.projectId,
        buildProfileId: profile.buildProfileId,
        targetIds: [],
        jobs: 2,
        timeoutMs: 30_000
      }),
      EVENT_TIMEOUT_MS
    );
    const secondEvents: ProtocolTaskEvent[] = [];
    stage = "wait for second build";
    const secondFinished = await waitForFinished(secondSubscription, second.taskId, secondEvents);
    assert.equal((secondFinished.payload as { outcome?: unknown }).outcome, "succeeded");
    assertContinuousUniqueSequences(secondEvents);
    assert.equal(secondEvents[0]?.sequence, firstDurable.lastSequence + 1);
    assert.deepEqual(
      secondEvents
        .filter((event) => event.event === "task.step_started")
        .map((event) => (event.payload as { stepId?: unknown }).stepId),
      ["build"],
      "second build must skip configure"
    );

    stage = "read fixture state";
    const state = JSON.parse(
      await readFile(
        join(workspaceDirectory, "build-fixture", ".unit-test-ide-cmake-fixture.json"),
        "utf8"
      )
    ) as { configureCount?: unknown; buildCount?: unknown };
    assert.deepEqual(
      { configureCount: state.configureCount, buildCount: state.buildCount },
      { configureCount: 1, buildCount: 2 }
    );
  } catch (error) {
    throw new Error(`deterministic CMake E2E failed during ${stage}`, { cause: error });
  } finally {
    await fixture?.dispose();
    await rm(workspaceDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
    await rm(serviceDirectory, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
});

test("task survives reconnect, cancels its tree, persists history and artifact", async () => {
  const fixture = await startTaskService(binary);
  const seen: ProtocolTaskEvent[] = [];
  let secondary: ProtocolClient | undefined;
  let reconnectGate: ReturnType<typeof fixture.pauseNextReconnect> | undefined;
  try {
    const client: ProtocolClient = fixture.client;
    const subscription = await withNamedTimeout("primary event subscription", client.subscribeEvents(0), EVENT_TIMEOUT_MS);
    const running = await withNamedTimeout(
      "spawn-child task start",
      client.startTask({
        idempotencyKey: randomBytes(16).toString("hex"),
        scenario: "spawn-child",
        timeoutMs: 30_000
      }),
      EVENT_TIMEOUT_MS
    );
    const childPID = await waitForChildPID(subscription, running.taskId, seen);
    const beforeReconnect = seen.at(-1)?.sequence;
    assert.ok(beforeReconnect);
    assertSimulationSnapshot(running);
    assertSimulationProtocolEvents(seen);

    reconnectGate = fixture.pauseNextReconnect();
    const reconnecting = withNamedTimeout("primary client reconnect", client.reconnect(), EVENT_TIMEOUT_MS);
    await withNamedTimeout("reconnect connector gate", reconnectGate.entered, EVENT_TIMEOUT_MS);
    const secondaryClient = await fixture.connectClient();
    secondary = secondaryClient;
    const secondarySubscription = await withNamedTimeout(
      "secondary event subscription",
      secondaryClient.subscribeEvents(beforeReconnect),
      EVENT_TIMEOUT_MS
    );
    await withNamedTimeout("secondary task cancellation", secondaryClient.cancelTask(running.taskId), EVENT_TIMEOUT_MS);
    const secondarySeen: ProtocolTaskEvent[] = [];
    const secondaryFinished = await waitForFinished(secondarySubscription, running.taskId, secondarySeen);
    assertSimulationProtocolEvents(secondarySeen, beforeReconnect);
    assert.equal((secondaryFinished.payload as { outcome?: unknown }).outcome, "cancelled");
    const durable = await withNamedTimeout("secondary durable task lookup", secondaryClient.getTask(running.taskId), EVENT_TIMEOUT_MS);
    assertSimulationSnapshot(durable);
    assert.equal(durable.status, "finished");
    assert.equal(durable.outcome, "cancelled");
    assert.ok(durable.lastSequence > beforeReconnect);
    secondaryClient.close();
    secondary = undefined;

    reconnectGate.release();
    reconnectGate = undefined;
    await reconnecting;
    const replayed = await collectThroughSequence(
      subscription,
      durable.lastSequence,
      beforeReconnect,
      "primary reconnect replay"
    );
    assert.deepEqual(
      replayed.map((event) => event.sequence),
      Array.from({ length: durable.lastSequence - beforeReconnect }, (_, index) => beforeReconnect + index + 1)
    );
    assert.equal(new Set(replayed.map((event) => event.sequence)).size, replayed.length);
    assertSimulationProtocolEvents(replayed, beforeReconnect);
    await assertProcessGone(childPID);

    const artifacts = await withNamedTimeout("artifact list", client.listArtifacts(running.taskId), EVENT_TIMEOUT_MS);
    assert.equal(artifacts.items.length, 1);
    const metadata = artifacts.items[0];
    assert.ok(metadata);
    const bytes = await withNamedTimeout("summary artifact read", client.readArtifact(metadata.artifactId), EVENT_TIMEOUT_MS);
    assert.equal(bytes.byteLength, metadata.sizeBytes);
    assert.equal(createHash("sha256").update(bytes).digest("hex"), metadata.sha256);
    const summary = JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>;
    assert.equal(summary.outcome, "cancelled");
    assert.equal(summary.taskId, running.taskId);

    await fixture.stopGracefully();
    const restarted = await fixture.restart();
    const persisted = await withNamedTimeout("persisted task lookup", restarted.client.getTask(running.taskId), EVENT_TIMEOUT_MS);
    assertSimulationSnapshot(persisted);
    assert.equal(persisted.status, "finished");
    assert.equal(persisted.outcome, "cancelled");
  } finally {
    reconnectGate?.release();
    secondary?.close();
    await fixture.dispose();
  }
});

test("service crash recovers an active task exactly once as interrupted", async () => {
  const fixture = await startTaskService(binary);
  try {
    const subscription = await withNamedTimeout(
      "crash event subscription",
      fixture.client.subscribeEvents(0),
      EVENT_TIMEOUT_MS
    );
    const running = await withNamedTimeout(
      "crash spawn-child task start",
      fixture.client.startTask({
        idempotencyKey: randomBytes(16).toString("hex"),
        scenario: "spawn-child",
        timeoutMs: 30_000
      }),
      EVENT_TIMEOUT_MS
    );
    assert.equal(running.status, "running");
    const beforeCrash: ProtocolTaskEvent[] = [];
    const childPID = await waitForChildPID(subscription, running.taskId, beforeCrash);
    assertSimulationSnapshot(running);
    assertSimulationProtocolEvents(beforeCrash);
    await fixture.kill();
    await assertProcessGone(childPID);

    const restarted = await fixture.restart();
    const recovered = await withNamedTimeout(
      "recovered task lookup",
      restarted.client.getTask(running.taskId),
      EVENT_TIMEOUT_MS
    );
    assertInterrupted(recovered);
    const recoverySubscription = await withNamedTimeout(
      "recovery event subscription",
      restarted.client.subscribeEvents(0),
      EVENT_TIMEOUT_MS
    );
    const events = await collectThroughSequence(recoverySubscription, recovered.lastSequence, 0, "recovery event replay");
    assertSimulationProtocolEvents(events);
    const recoveredFinished = events.filter((event) => event.taskId === running.taskId && event.event === "task.finished");
    assert.equal(recoveredFinished.length, 1);
    assert.equal((recoveredFinished[0]?.payload as { outcome?: unknown }).outcome, "interrupted");
    assert.equal(JSON.stringify({ recovered, events }).includes("test_failed"), false);
  } finally {
    await fixture.dispose();
  }
});
