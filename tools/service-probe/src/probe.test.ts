import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import type { EventSubscription, ProtocolClient, TaskEvent, TaskSnapshot } from "@unit-test-ide/test-client";
import { assertProcessGone, prepareTokenFile, runProbe, startTaskService } from "./probe.js";

const root = resolve(import.meta.dirname, "../../..");
const binary = join(root, "build", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
const EVENT_TIMEOUT_MS = 8_000;

function eventWithin(subscription: EventSubscription, label: string): Promise<IteratorResult<TaskEvent>> {
  return new Promise((resolvePromise, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out after ${EVENT_TIMEOUT_MS}ms`)), EVENT_TIMEOUT_MS);
    subscription.next().then(
      (value) => {
        clearTimeout(timer);
        resolvePromise(value);
      },
      (error: unknown) => {
        clearTimeout(timer);
        reject(error);
      }
    );
  });
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

async function collectThroughSequence(subscription: EventSubscription, sequence: number): Promise<TaskEvent[]> {
  const events: TaskEvent[] = [];
  while (subscription.lastSequence < sequence) {
    const next = await eventWithin(subscription, "recovery event replay");
    if (next.done) throw new Error("recovery event replay ended before the snapshot sequence");
    events.push(next.value);
  }
  return events;
}

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
  try {
    const client: ProtocolClient = fixture.client;
    const subscription = await client.subscribeEvents(0);
    const running = await client.startTask({
      idempotencyKey: randomBytes(16).toString("hex"),
      scenario: "spawn-child",
      timeoutMs: 30_000
    });
    const childPID = await waitForChildPID(subscription, running.taskId, seen);
    const beforeReconnect = subscription.lastSequence;
    await client.reconnect();
    assert.ok(subscription.lastSequence >= beforeReconnect);
    await client.cancelTask(running.taskId);
    const finishedEvent = await waitForFinished(subscription, running.taskId, seen);
    assert.equal(finishedEvent.payload.outcome, "cancelled");
    assertContinuousUniqueSequences(seen);
    await assertProcessGone(childPID);

    const artifacts = await client.listArtifacts(running.taskId);
    assert.equal(artifacts.items.length, 1);
    const metadata = artifacts.items[0];
    assert.ok(metadata);
    const bytes = await client.readArtifact(metadata.artifactId);
    assert.equal(bytes.byteLength, metadata.sizeBytes);
    assert.equal(createHash("sha256").update(bytes).digest("hex"), metadata.sha256);
    const summary = JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>;
    assert.equal(summary.outcome, "cancelled");
    assert.equal(summary.taskId, running.taskId);

    await fixture.stopGracefully();
    const restarted = await fixture.restart();
    const persisted = await restarted.client.getTask(running.taskId);
    assert.equal(persisted.status, "finished");
    assert.equal(persisted.outcome, "cancelled");
  } finally {
    await fixture.dispose();
  }
});

test("service crash recovers an active task exactly once as interrupted", async () => {
  const fixture = await startTaskService(binary);
  try {
    const running = await fixture.client.startTask({
      idempotencyKey: randomBytes(16).toString("hex"),
      scenario: "hang",
      timeoutMs: 30_000
    });
    assert.equal(running.status, "running");
    await fixture.kill();

    const restarted = await fixture.restart();
    const recovered = await restarted.client.getTask(running.taskId);
    assertInterrupted(recovered);
    const subscription = await restarted.client.subscribeEvents(0);
    const events = await collectThroughSequence(subscription, recovered.lastSequence);
    assertContinuousUniqueSequences(events);
    const recoveredFinished = events.filter((event) => event.taskId === running.taskId && event.event === "task.finished");
    assert.equal(recoveredFinished.length, 1);
    assert.equal(recoveredFinished[0]?.payload.outcome, "interrupted");
    assert.equal(JSON.stringify({ recovered, events }).includes("test_failed"), false);
  } finally {
    await fixture.dispose();
  }
});
