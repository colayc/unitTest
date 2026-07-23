import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { EventSubscription, type ProtocolClient, type TaskEvent, type TaskSnapshot } from "@unit-test-ide/test-client";
import { endpointForDirectory } from "./endpoint.js";
import { assertProcessGone, prepareTokenFile, runProbe, startService, startTaskService, withNamedTimeout } from "./probe.js";

const root = resolve(import.meta.dirname, "../../..");
const binary = join(root, "build", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
const EVENT_TIMEOUT_MS = 8_000;

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
      /protocol 1\.1 handshake timed out after 500ms/
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

function eventWithin(subscription: EventSubscription, label: string): Promise<IteratorResult<TaskEvent>> {
  return withNamedTimeout(label, subscription.next(), EVENT_TIMEOUT_MS);
}

async function waitForEvent(
  subscription: EventSubscription,
  taskId: string,
  predicate: (event: TaskEvent) => boolean,
  label: string,
  seen: TaskEvent[]
): Promise<TaskEvent> {
  for (;;) {
    const next = await eventWithin(subscription, label);
    if (next.done) throw new Error(`${label} ended before the expected event`);
    seen.push(next.value);
    if (next.value.taskId === taskId && predicate(next.value)) return next.value;
  }
}

async function waitForChildPID(subscription: EventSubscription, taskId: string, seen: TaskEvent[]): Promise<number> {
  let output = "";
  for (;;) {
    const event = await waitForEvent(subscription, taskId, (value) => value.event === "task.output", "child PID event", seen);
    output += typeof event.payload.text === "string" ? event.payload.text : "";
    const match = /(?:^|\n)CHILD_PID=(\d+)(?:\n|$)/.exec(output);
    if (!match?.[1]) continue;
    const pid = Number(match[1]);
    if (Number.isSafeInteger(pid) && pid > 0) return pid;
    throw new Error("child PID event contained an invalid PID");
  }
}

async function waitForFinished(subscription: EventSubscription, taskId: string, seen: TaskEvent[]): Promise<TaskEvent> {
  return waitForEvent(subscription, taskId, (event) => event.event === "task.finished", "task finished event", seen);
}

async function collectThroughSequence(
  subscription: EventSubscription,
  sequence: number,
  afterSequence = 0,
  label = "event replay"
): Promise<TaskEvent[]> {
  const events: TaskEvent[] = [];
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

function queuedTaskEvent(sequence: number): TaskEvent {
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
  } as TaskEvent;
}

test("collector consumes queued replay through the stable target sequence", async () => {
  const subscription = new EventSubscription(0);
  for (let sequence = 1; sequence <= 4; sequence++) assert.equal(subscription.push(queuedTaskEvent(sequence)), true);
  assert.equal(subscription.lastSequence, 4, "the receive watermark must already be ahead of consumption");
  const events = await collectThroughSequence(subscription, 4);
  assert.deepEqual(events.map((event) => event.sequence), [1, 2, 3, 4]);
});

function assertContinuousUniqueSequences(events: TaskEvent[]): void {
  assert.ok(events.length > 0);
  assert.equal(new Set(events.map((event) => event.sequence)).size, events.length, "event sequences must be unique");
  for (let index = 1; index < events.length; index++) {
    assert.equal(events[index]?.sequence, (events[index - 1]?.sequence ?? 0) + 1, "event sequences must have no gaps");
  }
}

function assertInterrupted(snapshot: TaskSnapshot): void {
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
  assert.equal(capabilities.platform, process.platform === "win32" ? "windows" : "linux");
  assert.deepEqual(capabilities.transports, [process.platform === "win32" ? "named-pipe" : "unix-socket"]);
});

test("task survives reconnect, cancels its tree, persists history and artifact", async () => {
  const fixture = await startTaskService(binary);
  const seen: TaskEvent[] = [];
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
    const secondarySeen: TaskEvent[] = [];
    const secondaryFinished = await waitForFinished(secondarySubscription, running.taskId, secondarySeen);
    assert.equal(secondaryFinished.payload.outcome, "cancelled");
    const durable = await withNamedTimeout("secondary durable task lookup", secondaryClient.getTask(running.taskId), EVENT_TIMEOUT_MS);
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
    const beforeCrash: TaskEvent[] = [];
    const childPID = await waitForChildPID(subscription, running.taskId, beforeCrash);
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
    assertContinuousUniqueSequences(events);
    const recoveredFinished = events.filter((event) => event.taskId === running.taskId && event.event === "task.finished");
    assert.equal(recoveredFinished.length, 1);
    assert.equal(recoveredFinished[0]?.payload.outcome, "interrupted");
    assert.equal(JSON.stringify({ recovered, events }).includes("test_failed"), false);
  } finally {
    await fixture.dispose();
  }
});
