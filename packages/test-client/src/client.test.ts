import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { Duplex, PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import test from "node:test";
import { MAX_MESSAGE_BYTES, ProtocolClient } from "./client.js";
import { Connection } from "./connection.js";
import { ProtocolError } from "./envelopes.js";
import { EventSubscription } from "./subscription.js";

type JsonObject = Record<string, unknown>;
const MESSAGE_ID = "fedcba9876543210fedcba9876543210";
const TASK_ID = "11111111111111111111111111111111";
const ARTIFACT_ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const WORKSPACE_GENERATION = "2".repeat(64);
const BUILD_PROFILE_ID = "3".repeat(64);
const TARGET_ID = "4".repeat(64);
const SENT_AT = "2026-07-21T00:00:00Z";
const CLIENT_MAX_ARTIFACT_BYTES = 64 * 1024 * 1024;

function pair(emitClose = true): [Duplex, Duplex] {
  const leftToRight = new PassThrough();
  const rightToLeft = new PassThrough();
  const create = (incoming: PassThrough, outgoing: PassThrough) => {
    const value = new Duplex({
      emitClose,
      read() {},
      write(chunk, encoding, callback) { outgoing.write(chunk, encoding, callback); },
      final(callback) { outgoing.end(); callback(); }
    });
    incoming.on("data", (chunk) => value.push(chunk));
    incoming.on("end", () => value.push(null));
    return value;
  };
  return [create(rightToLeft, leftToRight), create(leftToRight, rightToLeft)];
}

function response(request: JsonObject, payload: JsonObject, protocolVersion = request.protocolVersion): JsonObject {
  return {
    protocolVersion,
    kind: "response",
    messageId: MESSAGE_ID,
    requestId: request.messageId,
    method: request.method,
    sentAt: SENT_AT,
    payload
  };
}

function error(
  request: JsonObject,
  code: string,
  retryable: boolean,
  protocolVersion = request.protocolVersion
): JsonObject {
  return {
    protocolVersion,
    kind: "error",
    messageId: MESSAGE_ID,
    requestId: request.messageId,
    sentAt: SENT_AT,
    error: { code, message: code.toLowerCase(), retryable }
  };
}

function taskSnapshot(overrides: JsonObject = {}): JsonObject {
  return {
    taskId: TASK_ID,
    kind: "simulation",
    scenario: "hang",
    status: "running",
    createdAt: SENT_AT,
    startedAt: SENT_AT,
    timeoutMs: 1000,
    lastSequence: 2,
    ...overrides
  };
}

function cmakeTaskSnapshot(overrides: JsonObject = {}): JsonObject {
  return {
    taskId: TASK_ID,
    kind: "cmakeBuild",
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID,
    targetIds: [TARGET_ID],
    jobs: 8,
    timeoutMs: 600_000,
    status: "running",
    createdAt: SENT_AT,
    startedAt: SENT_AT,
    lastSequence: 2,
    ...overrides
  };
}

function taskEvent(sequence: number, eventName: string, overrides: JsonObject = {}): JsonObject {
  return {
    protocolVersion: "1.1",
    kind: "event",
    messageId: sequence.toString(16).padStart(32, "0"),
    sentAt: SENT_AT,
    sequence,
    event: eventName,
    taskId: TASK_ID,
    payloadVersion: 1,
    payload: {},
    ...overrides
  };
}

function responseLineOfSize(request: JsonObject, size: number): string {
  const payload = { negotiatedProtocolVersion: "1.0", serviceVersion: "" };
  let line = JSON.stringify(response(request, payload, "1.0"));
  const serviceVersionSize = size - Buffer.byteLength(line);
  assert.ok(serviceVersionSize >= 1, `requested line size ${size} is smaller than response envelope`);
  payload.serviceVersion = "x".repeat(serviceVersionSize);
  line = JSON.stringify(response(request, payload, "1.0"));
  assert.equal(Buffer.byteLength(line), size);
  return line;
}

function scriptedClient(handler: (request: JsonObject) => JsonObject | undefined): {
  client: ProtocolClient;
  requests: JsonObject[];
  server: Duplex;
} {
  const [clientStream, server] = pair();
  const requests: JsonObject[] = [];
  createInterface({ input: server }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    requests.push(request);
    const reply = handler(request);
    if (reply) server.write(`${JSON.stringify(reply)}\n`);
  });
  return { client: ProtocolClient.attach(clientStream), requests, server };
}

async function take(subscription: EventSubscription, count: number): Promise<JsonObject[]> {
  const values: JsonObject[] = [];
  for await (const event of subscription) {
    values.push(event as unknown as JsonObject);
    if (values.length === count) break;
  }
  return values;
}

test("EventSubscription rejects an unsafe initial sequence", () => {
  assert.throws(() => new EventSubscription(Number.MAX_SAFE_INTEGER + 1), /safe integer/i);
});

test("client prefers protocol 1.2 and accepts a negotiated 1.1 downgrade", async () => {
  for (const negotiated of ["1.2", "1.1"] as const) {
    const fixture = scriptedClient((request) => response(request, {
      negotiatedProtocolVersion: negotiated,
      serviceVersion: "0.3.0"
    }, negotiated));
    const result = await fixture.client.handshake("0123456789abcdef", "test", "0.3.0");
    assert.equal(result.negotiatedProtocolVersion, negotiated);
    assert.equal(fixture.requests[0]?.protocolVersion, "1.2");
    assert.deepEqual((fixture.requests[0]?.payload as JsonObject).supportedProtocolVersions, ["1.2", "1.1", "1.0"]);
    fixture.client.close();
  }
});

test("protocol 1.2 client routes workspace, target, and CMake build APIs", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.2", serviceVersion: "0.3.0" }, "1.2");
    }
    if (request.method === "workspace/inspect") {
      return response(request, {
        workspaceUri: "file:///workspace",
        workspaceGeneration: WORKSPACE_GENERATION,
        capabilities: { workspaceInspect: true, targetList: true, cmakeBuild: true },
        projects: [{
          projectId: "core",
          sourceUri: "file:///workspace/core",
          buildProfiles: [{ buildProfileId: BUILD_PROFILE_ID, name: "Debug" }]
        }]
      }, "1.2");
    }
    if (request.method === "cmake/targets/list") {
      return response(request, {
        workspaceGeneration: WORKSPACE_GENERATION,
        projectId: "core",
        buildProfileId: BUILD_PROFILE_ID,
        targets: [{ targetId: TARGET_ID, name: "unit-tests" }]
      }, "1.2");
    }
    if (request.method === "artifacts/list") {
      return response(request, {
        items: [{
          artifactId: ARTIFACT_ID,
          taskId: TASK_ID,
          kind: "task-summary",
          mimeType: "application/json",
          sizeBytes: 2,
          sha256: "0".repeat(64),
          createdAt: SENT_AT,
          uri: `unit-test-ide://artifact/${ARTIFACT_ID}`
        }]
      }, "1.2");
    }
    if (request.method === "tasks/start" && (request.payload as JsonObject).kind === "simulation") {
      return response(request, taskSnapshot(), "1.2");
    }
    return response(request, cmakeTaskSnapshot(), "1.2");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.3.0");
  const workspace = await fixture.client.inspectWorkspace();
  const targets = await fixture.client.listCMakeTargets({
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID
  });
  const build = await fixture.client.startCMakeBuild({
    idempotencyKey: TASK_ID,
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID,
    targetIds: [TARGET_ID],
    jobs: 8,
    timeoutMs: 600_000
  });
  const simulation = await fixture.client.startTask({
    idempotencyKey: TASK_ID,
    scenario: "success",
    timeoutMs: 1_000
  });
  const artifacts = await fixture.client.listArtifacts(TASK_ID);
  assert.equal(workspace.projects[0]?.sourceUri, "file:///workspace/core");
  assert.equal(targets.buildProfileId, BUILD_PROFILE_ID);
  assert.equal(build.kind, "cmakeBuild");
  assert.ok(build.createdAt instanceof Date);
  assert.equal(simulation.kind, "simulation");
  assert.ok(artifacts.items[0]?.createdAt instanceof Date);
  assert.equal("uri" in artifacts.items[0]!, true);
  assert.deepEqual(fixture.requests.map(({ method }) => method), [
    "handshake",
    "workspace/inspect",
    "cmake/targets/list",
    "tasks/start",
    "tasks/start",
    "artifacts/list"
  ]);
  assert.equal((fixture.requests[3]?.payload as JsonObject).kind, "cmakeBuild");
  assert.equal((fixture.requests[4]?.payload as JsonObject).kind, "simulation");
  fixture.client.close();
});

test("protocol 1.1 rejects Phase 3 methods locally without writing", async () => {
  const fixture = scriptedClient((request) => response(request, {
    negotiatedProtocolVersion: "1.1",
    serviceVersion: "0.2.0"
  }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.3.0");
  const before = fixture.requests.length;
  await assert.rejects(() => fixture.client.inspectWorkspace(), /protocol 1\.2/i);
  await assert.rejects(() => fixture.client.listCMakeTargets({
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID
  }), /protocol 1\.2/i);
  await assert.rejects(() => fixture.client.startCMakeBuild({
    idempotencyKey: TASK_ID,
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID,
    targetIds: [],
    jobs: 8,
    timeoutMs: 600_000
  }), /protocol 1\.2/i);
  assert.equal(fixture.requests.length, before);
  fixture.client.close();
});

test("CMake build inputs are rejected locally before reaching the wire", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.2", serviceVersion: "0.3.0" }, "1.2")
    : response(request, cmakeTaskSnapshot(), "1.2"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.3.0");
  const valid = {
    idempotencyKey: TASK_ID,
    workspaceGeneration: WORKSPACE_GENERATION,
    projectId: "core",
    buildProfileId: BUILD_PROFILE_ID,
    targetIds: [TARGET_ID],
    jobs: 8,
    timeoutMs: 600_000
  };
  const invalid = [
    { ...valid, workspaceGeneration: "bad" },
    { ...valid, projectId: "/outside" },
    { ...valid, buildProfileId: "bad" },
    { ...valid, targetIds: ["bad"] },
    { ...valid, jobs: 0 },
    { ...valid, jobs: Number.NaN },
    { ...valid, timeoutMs: 0 },
    { ...valid, timeoutMs: Number.MAX_SAFE_INTEGER + 1 }
  ];
  for (const input of invalid) await assert.rejects(() => fixture.client.startCMakeBuild(input));
  assert.equal(fixture.requests.length, 1);
  fixture.client.close();
});

test("protocol 1.2 task decoder rejects unsafe integers, invalid dates, and unknown union kinds", async () => {
  for (const invalidTask of [
    cmakeTaskSnapshot({ lastSequence: Number.MAX_SAFE_INTEGER + 1 }),
    cmakeTaskSnapshot({ createdAt: "999999-01-01T00:00:00Z" }),
    cmakeTaskSnapshot({ kind: "shell" })
  ]) {
    const fixture = scriptedClient((request) => request.method === "handshake"
      ? response(request, { negotiatedProtocolVersion: "1.2", serviceVersion: "0.3.0" }, "1.2")
      : response(request, invalidTask, "1.2"));
    await fixture.client.handshake("0123456789abcdef", "test", "0.3.0");
    await assert.rejects(() => fixture.client.startCMakeBuild({
      idempotencyKey: TASK_ID,
      workspaceGeneration: WORKSPACE_GENERATION,
      projectId: "core",
      buildProfileId: BUILD_PROFILE_ID,
      targetIds: [TARGET_ID],
      jobs: 8,
      timeoutMs: 600_000
    }));
    fixture.client.close();
  }
});

test("EventSubscription accepts protocol 1.2 events and preserves sequence", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, {
        negotiatedProtocolVersion: "1.2",
        serviceVersion: "0.3.0"
      }, "1.2"))}\n`);
      return;
    }
    serverStream.write(`${JSON.stringify(response(request, { afterSequence: 0 }, "1.2"))}\n`);
    serverStream.write(`${JSON.stringify({
      protocolVersion: "1.2",
      kind: "event",
      messageId: MESSAGE_ID,
      sentAt: SENT_AT,
      sequence: 1,
      event: "task.step_started",
      taskId: TASK_ID,
      payloadVersion: 1,
      payload: { stepId: "configure", kind: "configure", status: "running" }
    })}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.3.0");
  const subscription = await client.subscribeEvents(0);
  const event = (await take(subscription, 1))[0];
  assert.equal(event?.protocolVersion, "1.2");
  assert.equal(event?.sequence, 1);
  assert.equal(subscription.lastSequence, 1);
  client.close();
});

test("client performs handshake, capabilities, and shutdown in order", async () => {
  const [clientStream, serverStream] = pair();
  const methods: string[] = [];
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    methods.push(request.method as string);
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }
      : request.method === "capabilities/get"
        ? { platform: "windows", transports: ["named-pipe"], toolchains: [], frameworks: [], coverageTools: [] }
        : { accepted: true };
    serverStream.write(`${JSON.stringify(response(request, payload, request.method === "handshake" ? "1.0" : request.protocolVersion))}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.1.0");
  const capabilities = await client.getCapabilities();
  assert.ok("platform" in capabilities);
  assert.equal(capabilities.platform, "windows");
  await client.shutdown();
  assert.deepEqual(methods, ["handshake", "capabilities/get", "shutdown"]);
  client.close();
});

test("client exposes stable server error codes", async () => {
  const { client } = scriptedClient((request) => error(request, "AUTH_FAILED", false, "1.2"));
  await assert.rejects(
    () => client.handshake("wrong-token-value", "test", "0.1.0"),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "AUTH_FAILED"
  );
  client.close();
});

test("client accepts a fragmented exact-limit response with CRLF", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    serverStream.write(responseLineOfSize(request, MAX_MESSAGE_BYTES));
    serverStream.write("\r");
    setImmediate(() => serverStream.write("\n"));
  });
  const client = ProtocolClient.attach(clientStream);
  const result = await client.handshake("0123456789abcdef", "test", "0.1.0");
  assert.equal(result.negotiatedProtocolVersion, "1.0");
  client.close();
});

test("client rejects a Max+1 response body with CRLF", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    serverStream.write(responseLineOfSize(request, MAX_MESSAGE_BYTES + 1));
    serverStream.write("\r");
    setImmediate(() => serverStream.write("\n"));
  });
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("0123456789abcdef", "test", "0.1.0"), /1 MiB/);
  client.close();
});

test("protocol line limit uses UTF-8 bytes rather than JavaScript string length", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", () => {
    serverStream.write(`${JSON.stringify({ value: "界".repeat(400_000) })}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("0123456789abcdef", "test", "0.1.0"), /1 MiB/);
  client.close();
});

test("manual close rejects pending requests when the stream does not emit close", async () => {
  const [clientStream, serverStream] = pair(false);
  const client = ProtocolClient.attach(clientStream);
  const pending = client.handshake("0123456789abcdef", "test", "0.1.0");
  client.close();
  client.close();

  const timeout = new Promise<never>((_, reject) => {
    setTimeout(() => reject(new Error("pending request was not rejected")), 50);
  });
  await assert.rejects(Promise.race([pending, timeout]), /service connection is closed/);
  serverStream.destroy();
});

test("readable EOF rejects a pending request even when the stream never emits close", async () => {
  const [clientStream, serverStream] = pair(false);
  const client = ProtocolClient.attach(clientStream);
  const pending = client.handshake("0123456789abcdef", "test", "0.1.0");
  serverStream.end();
  const timeout = new Promise<never>((_, reject) => {
    setTimeout(() => reject(new Error("pending request was not rejected after EOF")), 50);
  });
  await assert.rejects(Promise.race([pending, timeout]), /ended/);
  client.close();
});

test("invalid JSON closes the connection and rejects every pending request", async () => {
  const [clientStream, serverStream] = pair();
  const client = ProtocolClient.attach(clientStream);
  const handshake = client.handshake("0123456789abcdef", "test", "0.1.0");
  serverStream.write("not-json\n");
  await assert.rejects(handshake, /invalid JSON/);
  await assert.rejects(() => client.handshake("0123456789abcdef", "test", "0.1.0"), /closed/);
});

test("unknown protocol versions close the connection", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "2.0", serviceVersion: "0.1.0" }, "2.0"))}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("0123456789abcdef", "test", "0.1.0"), /unsupported protocol version/);
});

test("client falls back to an exact 1.0 handshake", async () => {
  const fixture = scriptedClient((request) => {
    if (request.protocolVersion === "1.2") return error(request, "UNSUPPORTED_PROTOCOL", false, "1.0");
    return response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
  });
  const negotiated = await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  assert.equal(negotiated.negotiatedProtocolVersion, "1.0");
  assert.deepEqual(fixture.requests.map(({ protocolVersion }) => protocolVersion), ["1.2", "1.0"]);
  assert.deepEqual(fixture.requests[0]?.payload, {
    token: "0123456789abcdef",
    clientName: "test",
    clientVersion: "0.2.0",
    supportedProtocolVersions: ["1.2", "1.1", "1.0"]
  });
  assert.equal("supportedProtocolVersions" in (fixture.requests[1]?.payload as JsonObject), false);
  fixture.client.close();
});

test("a new Connection grants its first 1.1 handshake one legacy unsupported response", async () => {
  const [clientStream, serverStream] = pair();
  const requests: JsonObject[] = [];
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    requests.push(request);
    const reply = request.protocolVersion === "1.1"
      ? error(request, "UNSUPPORTED_PROTOCOL", false, "1.0")
      : response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
    serverStream.write(`${JSON.stringify(reply)}\n`);
  });
  const connection = new Connection(clientStream);
  const handshakePayload = { token: "0123456789abcdef", clientName: "test", clientVersion: "0.2.0" };
  await assert.rejects(
    () => connection.request("1.1", "handshake", {
      ...handshakePayload,
      supportedProtocolVersions: ["1.1", "1.0"]
    }),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "UNSUPPORTED_PROTOCOL"
  );
  const negotiated = await connection.request("1.0", "handshake", handshakePayload);
  assert.equal(negotiated.negotiatedProtocolVersion, "1.0");
  assert.deepEqual(requests.map(({ protocolVersion }) => protocolVersion), ["1.1", "1.0"]);
  connection.close();
});

test("client does not downgrade handshake errors other than UNSUPPORTED_PROTOCOL", async () => {
  const fixture = scriptedClient((request) => error(request, "AUTH_FAILED", false, "1.0"));
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    /protocol version/
  );
  assert.equal(fixture.requests.length, 1);
  await assert.rejects(() => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"), /closed/);
});

test("a same-version handshake failure consumes the Connection legacy opportunity", async () => {
  let handshakeCount = 0;
  const fixture = scriptedClient((request) => {
    handshakeCount++;
    if (handshakeCount === 1) return error(request, "AUTH_FAILED", false, "1.2");
    if (handshakeCount === 2) return error(request, "UNSUPPORTED_PROTOCOL", false, "1.0");
    return response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
  });
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "AUTH_FAILED"
  );
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    /protocol version/
  );
  assert.deepEqual(fixture.requests.map(({ protocolVersion }) => protocolVersion), ["1.2", "1.2"]);
});

test("an authenticated connection rejects a legacy-version handshake error", async () => {
  let handshakeCount = 0;
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake" && handshakeCount++ === 0) {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    return error(request, "AUTH_FAILED", false, "1.0");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    /protocol version/
  );
  await assert.rejects(() => fixture.client.getCapabilities(), /closed/);
});

test("a 1.0 fallback handshake rejects response payload fields outside the legacy shape", async () => {
  const fixture = scriptedClient((request) => request.protocolVersion === "1.1"
    ? error(request, "UNSUPPORTED_PROTOCOL", false, "1.1")
    : response(request, {
      negotiatedProtocolVersion: "1.0",
      serviceVersion: "0.1.0",
      supportedProtocolVersions: ["1.0"]
    }, "1.0"));
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    /invalid handshake response/
  );
  fixture.client.close();
});

test("client routes interleaved responses and deduplicates events", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    if (request.method === "events/subscribe") return response(request, { afterSequence: 0 }, "1.1");
    return undefined;
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await fixture.client.subscribeEvents(0);
  const taskPromise = fixture.client.getTask(TASK_ID);
  await new Promise<void>((resolve) => setImmediate(resolve));
  const taskRequest = fixture.requests.find(({ method }) => method === "tasks/get");
  assert.ok(taskRequest);
  fixture.server.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
  fixture.server.write(`${JSON.stringify(response(taskRequest, taskSnapshot(), "1.1"))}\n`);
  fixture.server.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
  fixture.server.write(`${JSON.stringify(taskEvent(2, "task.started"))}\n`);
  const events = await take(subscription, 2);
  assert.deepEqual(events.map(({ sequence }) => sequence), [1, 2]);
  assert.equal(subscription.lastSequence, 2);
  assert.equal((await taskPromise).taskId, TASK_ID);
  fixture.client.close();
});

test("subscribe installs its event sink before the subscribe response can be followed by an event", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
      return;
    }
    serverStream.write(
      `${JSON.stringify(response(request, { afterSequence: 0 }, "1.1"))}\n${JSON.stringify(taskEvent(1, "task.created"))}\n`
    );
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(0);
  const timeout = new Promise<never>((_, reject) => setTimeout(() => reject(new Error("first event was lost")), 50));
  const next = await Promise.race([subscription.next(), timeout]);
  assert.equal(next.value?.sequence, 1);
  client.close();
});

test("a replacement subscription keeps pre-ack events on the old subscription", async () => {
  const [clientStream, serverStream] = pair();
  let subscribeCount = 0;
  let replacementRequest: JsonObject | undefined;
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
    } else if (subscribeCount++ === 0) {
      serverStream.write(`${JSON.stringify(response(request, { afterSequence: 10 }, "1.1"))}\n`);
    } else {
      replacementRequest = request;
    }
  });
  const client = ProtocolClient.attach(clientStream);
  try {
    await client.handshake("0123456789abcdef", "test", "0.2.0");
    const oldSubscription = await client.subscribeEvents(10);
    const replacementPromise = client.subscribeEvents(0);
    await new Promise<void>((resolve) => setImmediate(resolve));
    assert.ok(replacementRequest);
    serverStream.write(`${JSON.stringify(taskEvent(11, "task.output"))}\n`);
    serverStream.write(
      `${JSON.stringify(response(replacementRequest, { afterSequence: 0 }, "1.1"))}\n${JSON.stringify(taskEvent(1, "task.created"))}\n`
    );
    const replacement = await replacementPromise;
    const oldNext = await Promise.race([
      oldSubscription.next(),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("old subscription lost pre-ack event")), 50))
    ]);
    assert.equal(oldNext.value?.sequence, 11);
    assert.equal((await replacement.next()).value?.sequence, 1);
  } finally {
    client.close();
  }
});

test("a failed replacement subscription closes the retired old subscription", async () => {
  const [clientStream, serverStream] = pair();
  let subscribeCount = 0;
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
    } else if (subscribeCount++ === 0) {
      serverStream.write(`${JSON.stringify(response(request, { afterSequence: 0 }, "1.1"))}\n`);
    } else {
      serverStream.write(
        `${JSON.stringify(error(request, "STORAGE_UNAVAILABLE", true, "1.1"))}\n${JSON.stringify(taskEvent(2, "task.started"))}\n`
      );
    }
  });
  const client = ProtocolClient.attach(clientStream);
  try {
    await client.handshake("0123456789abcdef", "test", "0.2.0");
    const oldSubscription = await client.subscribeEvents(0);
    serverStream.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
    assert.equal((await oldSubscription.next()).value?.sequence, 1);
    await assert.rejects(() => client.subscribeEvents(1), /storage_unavailable/i);
    assert.equal(oldSubscription.lastSequence, 1);
    const closed = await Promise.race([
      oldSubscription.next(),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("retired subscription stayed open")), 50))
    ]);
    assert.deepEqual(closed, { value: undefined, done: true });
  } finally {
    client.close();
  }
});

test("subscribe rejects an acknowledgement for a different cursor", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, { afterSequence: 4 }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.subscribeEvents(5), /cursor|afterSequence/);
  fixture.client.close();
});

test("legacy task.output keeps exact payload keys while contiguous sequence gaps remain fatal", async () => {
  const subscription = new EventSubscription(0);
  const created = taskEvent(1, "task.created");
  const compatibilityOutput = taskEvent(2, "task.output", {
    payload: { stream: "service", text: "", truncated: false }
  });

  assert.equal(subscription.push(created as never), true);
  assert.equal(subscription.push(compatibilityOutput as never), true);
  assert.equal(subscription.lastSequence, 2);
  const events = await take(subscription, 2);
  assert.deepEqual(events.map(({ sequence }) => sequence), [1, 2]);
  assert.deepEqual(events[1]?.payload, { stream: "service", text: "", truncated: false });
  assert.deepEqual(Object.keys(events[1]?.payload as JsonObject).sort(), ["stream", "text", "truncated"]);

  assert.equal(
    subscription.push(taskEvent(4, "task.output", {
      payload: { stream: "service", text: "", truncated: false }
    }) as never),
    false
  );
  assert.equal(subscription.lastSequence, 2);
});

test("a forward event gap does not advance the cursor and invalidates the connection", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, { afterSequence: 0 }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await fixture.client.subscribeEvents(0);
  fixture.server.write(`${JSON.stringify(taskEvent(2, "task.started"))}\n`);
  assert.deepEqual(await subscription.next(), { value: undefined, done: true });
  assert.equal(subscription.lastSequence, 0);
  await assert.rejects(() => fixture.client.getTask(TASK_ID), /closed|gap/);
});

test("an attached stream failure rejects pending requests and closes the subscription", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
    } else if (request.method === "events/subscribe") {
      serverStream.write(`${JSON.stringify(response(request, { afterSequence: 0 }, "1.1"))}\n`);
    }
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(0);
  const pending = client.getTask(TASK_ID);
  clientStream.destroy(new Error("network lost"));
  await assert.rejects(pending, /network lost/);
  assert.deepEqual(await subscription.next(), { value: undefined, done: true });
});

test("phase 2 methods reject a negotiated 1.0 session locally", async () => {
  const fixture = scriptedClient((request) => response(
    request,
    { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" },
    "1.0"
  ));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(
    () => fixture.client.startTask({ idempotencyKey: TASK_ID, scenario: "success", timeoutMs: 1000 }),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "PROTOCOL_FEATURE_UNAVAILABLE"
  );
  assert.equal(fixture.requests.length, 1);
  fixture.client.close();
});

test("typed task responses are validated instead of trusted as arbitrary objects", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, { taskId: TASK_ID, status: "invented" }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.getTask(TASK_ID), /invalid tasks\/get response/);
  fixture.client.close();
});

test("wire date-time strings decode to Date values in generated public models", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    if (request.method === "events/subscribe") return response(request, { afterSequence: 0 }, "1.1");
    if (request.method === "artifacts/list") return response(request, {
      items: [{
        artifactId: ARTIFACT_ID,
        taskId: TASK_ID,
        kind: "task-summary",
        mimeType: "application/json",
        sizeBytes: 0,
        sha256: "0".repeat(64),
        createdAt: SENT_AT
      }]
    }, "1.1");
    return response(request, taskSnapshot({
      status: "finished",
      outcome: "succeeded",
      finishedAt: SENT_AT
    }), "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  const task = await fixture.client.getTask(TASK_ID);
  const artifacts = await fixture.client.listArtifacts(TASK_ID);
  const subscription = await fixture.client.subscribeEvents(0);
  fixture.server.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
  const event = (await subscription.next()).value;
  assert.ok(task.createdAt instanceof Date);
  assert.ok(task.startedAt instanceof Date);
  assert.ok(task.finishedAt instanceof Date);
  assert.ok(artifacts.items[0]?.createdAt instanceof Date);
  assert.ok(event?.sentAt instanceof Date);
  fixture.client.close();
});

test("a schema-valid date-time that cannot become a Date is rejected", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, taskSnapshot({ createdAt: "2026-12-31T23:59:60Z" }), "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.getTask(TASK_ID), /date/i);
  fixture.client.close();
});

test("generated task and artifact models reject unsafe sequence and size integers", async () => {
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    if (request.method === "artifacts/list") return response(request, {
      items: [{
        artifactId: ARTIFACT_ID,
        taskId: TASK_ID,
        kind: "task-summary",
        mimeType: "application/json",
        sizeBytes: Number.MAX_SAFE_INTEGER + 1,
        sha256: "0".repeat(64),
        createdAt: SENT_AT
      }]
    }, "1.1");
    return response(request, taskSnapshot({ lastSequence: Number.MAX_SAFE_INTEGER + 1 }), "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.getTask(TASK_ID), /safe integer/i);
  await assert.rejects(() => fixture.client.listArtifacts(TASK_ID), /safe integer/i);
  fixture.client.close();
});

test("runtime-invalid requests are rejected locally without reaching the wire", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : error(request, "INVALID_MESSAGE", false, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  const requestsBeforeInvalidCalls = fixture.requests.length;
  const invalidCalls: Array<() => Promise<unknown>> = [
    () => fixture.client.startTask({ idempotencyKey: "bad", scenario: "success", timeoutMs: 1000 } as never),
    () => fixture.client.startTask({ idempotencyKey: TASK_ID, scenario: "success", timeoutMs: 0 }),
    () => fixture.client.startTask({ idempotencyKey: TASK_ID, scenario: "success", timeoutMs: Number.NaN }),
    () => fixture.client.getTask("bad"),
    () => fixture.client.cancelTask("bad"),
    () => fixture.client.listTasks({ cursor: "" }),
    () => fixture.client.listTasks({ limit: 201 }),
    () => fixture.client.listTasks({ limit: Number.NaN }),
    () => fixture.client.subscribeEvents(Number.NaN),
    () => fixture.client.listArtifacts("bad", { cursor: "" }),
    () => fixture.client.readArtifact("bad")
  ];
  for (const invalidCall of invalidCalls) await assert.rejects(invalidCall);
  assert.equal(fixture.requests.length, requestsBeforeInvalidCalls);
  fixture.client.close();
});

test("an oversized outbound request rejects only that request and leaves the connection usable", async () => {
  const [clientStream, serverStream] = pair();
  const requests: JsonObject[] = [];
  let pendingCapabilitiesRequest: JsonObject | undefined;
  const capabilities = {
    platform: "windows",
    transports: ["named-pipe"],
    toolchains: [],
    frameworks: [],
    coverageTools: [],
    taskExecution: true,
    eventReplay: true,
    sqliteHistory: true,
    artifactRead: true,
    processTreeControl: "job-object"
  };
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    requests.push(request);
    if (request.method === "handshake") {
      serverStream.write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
    } else if (!pendingCapabilitiesRequest) {
      pendingCapabilitiesRequest = request;
    } else {
      serverStream.write(`${JSON.stringify(response(request, capabilities, "1.1"))}\n`);
    }
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const pendingCapabilities = client.getCapabilities();
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.ok(pendingCapabilitiesRequest);

  await assert.rejects(() => client.listTasks({ cursor: "x".repeat(MAX_MESSAGE_BYTES) }), /1 MiB/);
  assert.equal(requests.some(({ method }) => method === "tasks/list"), false);

  serverStream.write(`${JSON.stringify(response(pendingCapabilitiesRequest, capabilities, "1.1"))}\n`);
  const firstCapabilities = await pendingCapabilities;
  const secondCapabilities = await client.getCapabilities();
  assert.ok("platform" in firstCapabilities && "platform" in secondCapabilities);
  assert.equal(firstCapabilities.platform, "windows");
  assert.equal(secondCapabilities.platform, "windows");
  client.close();
});

test("Connection validates all handshake request versions before writing", async () => {
  const [clientStream, serverStream] = pair();
  const requests: JsonObject[] = [];
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    requests.push(request);
    serverStream.write(`${JSON.stringify(error(request, "INVALID_MESSAGE", false, request.protocolVersion))}\n`);
  });
  const connection = new Connection(clientStream);
  await assert.rejects(() => connection.request("1.2", "handshake", {
    token: "short",
    clientName: "test",
    clientVersion: "0.3.0",
    supportedProtocolVersions: []
  }));
  await assert.rejects(() => connection.request("1.1", "handshake", {
    token: "short",
    clientName: "test",
    clientVersion: "0.2.0",
    supportedProtocolVersions: []
  }));
  await assert.rejects(() => connection.request("1.0", "handshake", {
    token: "short",
    clientName: "test",
    clientVersion: "0.2.0"
  }));
  assert.equal(requests.length, 0);
  connection.close();
});

test("a response protocol version mismatch closes the connection", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, { accepted: true }, "1.0"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.shutdown(), /protocol version/);
  await assert.rejects(() => fixture.client.getCapabilities(), /closed/);
});

test("task and artifact methods send typed payloads and validate list responses", async () => {
  const methods: string[] = [];
  const fixture = scriptedClient((request) => {
    methods.push(request.method as string);
    if (request.method === "handshake") return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    if (request.method === "tasks/list") return response(request, { items: [taskSnapshot()], nextCursor: "next" }, "1.1");
    if (request.method === "artifacts/list") return response(request, {
      items: [{ artifactId: ARTIFACT_ID, taskId: TASK_ID, kind: "task-summary", mimeType: "application/json", sizeBytes: 0, sha256: "0".repeat(64), createdAt: SENT_AT }]
    }, "1.1");
    return response(request, taskSnapshot(), "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await fixture.client.startTask({ idempotencyKey: TASK_ID, scenario: "success", timeoutMs: 1000 });
  await fixture.client.getTask(TASK_ID);
  const page = await fixture.client.listTasks({ cursor: "cursor", limit: 5 });
  await fixture.client.cancelTask(TASK_ID);
  const artifacts = await fixture.client.listArtifacts(TASK_ID, { limit: 5 });
  assert.equal(page.nextCursor, "next");
  assert.equal(artifacts.items[0]?.artifactId, ARTIFACT_ID);
  assert.deepEqual(methods, ["handshake", "tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "artifacts/list"]);
  fixture.client.close();
});

test("attach clients reject reconnect with a stable explicit error", async () => {
  const [clientStream, serverStream] = pair();
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.reconnect(), /connector is not available/);
  client.close();
  serverStream.destroy();
});

test("reconnect reuses credentials and the active subscription cursor", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  const requests: JsonObject[][] = [[], []];
  for (const [index, server] of [first[1], second[1]].entries()) {
    createInterface({ input: server }).on("line", (line) => {
      const request = JSON.parse(line) as JsonObject;
      requests[index]?.push(request);
      const payload = request.method === "handshake"
        ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
        : { afterSequence: (request.payload as JsonObject).afterSequence };
      server.write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
    });
  }
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(3);
  first[1].write(`${JSON.stringify(taskEvent(4, "task.started"))}\n`);
  assert.equal((await subscription.next()).value?.sequence, 4);
  await client.reconnect();
  assert.equal(calls, 2);
  assert.deepEqual((requests[1]?.[0]?.payload as JsonObject).supportedProtocolVersions, ["1.2", "1.1", "1.0"]);
  assert.deepEqual(requests[1]?.[1]?.payload, { afterSequence: 4 });

  first[1].write(`${JSON.stringify(taskEvent(5, "task.output", { payload: { old: true } }))}\n`);
  second[1].write(`${JSON.stringify(taskEvent(5, "task.output"))}\n`);
  const next = await subscription.next();
  assert.equal(next.value?.sequence, 5);
  assert.deepEqual(next.value?.payload, {});
  assert.equal(subscription.lastSequence, 5);
  client.close();
});

test("reconnect validates its acknowledgement before accepting a same-chunk replay event", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: (request.payload as JsonObject).afterSequence };
    first[1].write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
  });
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      second[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
      return;
    }
    second[1].write(
      `${JSON.stringify(response(request, { afterSequence: 0 }, "1.1"))}\n${JSON.stringify(taskEvent(1, "task.created"))}\n`
    );
  });
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(0);

  await client.reconnect();

  assert.equal((await subscription.next()).value?.sequence, 1);
  client.close();
});

test("reconnect discards a same-chunk replay event when the acknowledgement cursor mismatches", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: (request.payload as JsonObject).afterSequence };
    first[1].write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
  });
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      second[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
      return;
    }
    second[1].write(
      `${JSON.stringify(response(request, { afterSequence: 99 }, "1.1"))}\n${JSON.stringify(taskEvent(1, "task.created"))}\n`
    );
  });
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(0);

  await assert.rejects(() => client.reconnect(), /cursor|afterSequence/);

  assert.equal(subscription.lastSequence, 0);
  client.close();
});

test("concurrent reconnect is rejected and does not create extra connections", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    first[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const reconnecting = client.reconnect();
  await assert.rejects(() => client.reconnect(), /already in progress/);
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    second[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
  });
  await reconnecting;
  assert.equal(calls, 2);
  client.close();
});

test("subscribe is rejected without disturbing an active reconnect", async () => {
  const first = pair();
  const second = pair();
  let connectorCalls = 0;
  let resolveSecond: ((stream: Duplex) => void) | undefined;
  const delayedSecond = new Promise<Duplex>((resolve) => { resolveSecond = resolve; });
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: (request.payload as JsonObject).afterSequence };
    first[1].write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
  });
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: (request.payload as JsonObject).afterSequence };
    second[1].write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
  });
  const client = await ProtocolClient.connect(() => connectorCalls++ === 0 ? first[0] : delayedSecond);
  try {
    await client.handshake("0123456789abcdef", "test", "0.2.0");
    const subscription = await client.subscribeEvents(0);
    const reconnecting = client.reconnect();

    await assert.rejects(
      () => client.subscribeEvents(0),
      /client lifecycle operation is already in progress/
    );

    resolveSecond?.(second[0]);
    await reconnecting;
    second[1].write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
    assert.equal((await subscription.next()).value?.sequence, 1);
  } finally {
    client.close();
  }
});

test("reconnect is rejected without disturbing an active subscribe", async () => {
  const first = pair();
  const second = pair();
  let connectorCalls = 0;
  let subscribeRequest: JsonObject | undefined;
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    if (request.method === "handshake") {
      first[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
    } else {
      subscribeRequest = request;
    }
  });
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    second[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
  });
  const client = await ProtocolClient.connect(() => connectorCalls++ === 0 ? first[0] : second[0]);
  try {
    await client.handshake("0123456789abcdef", "test", "0.2.0");
    const subscribing = client.subscribeEvents(0);
    void subscribing.catch(() => {});
    await new Promise<void>((resolve) => setImmediate(resolve));
    assert.ok(subscribeRequest);

    await assert.rejects(
      () => client.reconnect(),
      /client lifecycle operation is already in progress/
    );
    assert.equal(connectorCalls, 1);

    first[1].write(
      `${JSON.stringify(response(subscribeRequest, { afterSequence: 0 }, "1.1"))}\n${JSON.stringify(taskEvent(1, "task.created"))}\n`
    );
    const subscription = await subscribing;
    assert.equal((await subscription.next()).value?.sequence, 1);
  } finally {
    client.close();
  }
});

test("close invalidates a reconnect waiting for its connector and destroys the late stream", async () => {
  const first = pair();
  const late = pair();
  let connectorCalls = 0;
  let resolveLate: ((stream: Duplex) => void) | undefined;
  const lateStream = new Promise<Duplex>((resolve) => { resolveLate = resolve; });
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    first[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
  });
  createInterface({ input: late[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    late[1].write(`${JSON.stringify(response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1"))}\n`);
  });
  const client = await ProtocolClient.connect(() => connectorCalls++ === 0 ? first[0] : lateStream);
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const reconnecting = client.reconnect();
  client.close();
  resolveLate?.(late[0]);
  await assert.rejects(reconnecting, /closed|cancel/i);
  assert.equal(late[0].destroyed, true);
});

test("reconnect rejects a downgraded session when an active subscription must be restored", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: (request.payload as JsonObject).afterSequence };
    first[1].write(`${JSON.stringify(response(request, payload, "1.1"))}\n`);
  });
  createInterface({ input: second[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const reply = request.protocolVersion === "1.1"
      ? error(request, "UNSUPPORTED_PROTOCOL", false, "1.1")
      : response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
    second[1].write(`${JSON.stringify(reply)}\n`);
  });
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(1);
  first[1].write(`${JSON.stringify(taskEvent(2, "task.started"))}\n`);
  assert.equal((await subscription.next()).value?.sequence, 2);
  await assert.rejects(
    () => client.reconnect(),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "PROTOCOL_FEATURE_UNAVAILABLE"
  );
  assert.equal(subscription.lastSequence, 2);
  client.close();
});

test("failed reconnect handshakes and subscriptions retain the active cursor for a later retry", async () => {
  const pairs = [pair(), pair(), pair(), pair()];
  const streams = pairs.map(([clientStream]) => clientStream);
  const requests = pairs.map((): JsonObject[] => []);
  let calls = 0;
  for (const [index, [, server]] of pairs.entries()) {
    createInterface({ input: server }).on("line", (line) => {
      const request = JSON.parse(line) as JsonObject;
      requests[index]?.push(request);
      let reply: JsonObject;
      if (index === 1) {
        reply = error(request, "AUTH_FAILED", false, request.protocolVersion);
      } else if (index === 2 && request.method === "events/subscribe") {
        reply = error(request, "STORAGE_UNAVAILABLE", true, "1.1");
      } else {
        const payload = request.method === "handshake"
          ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
          : { afterSequence: (request.payload as JsonObject).afterSequence };
        reply = response(request, payload, "1.1");
      }
      server.write(`${JSON.stringify(reply)}\n`);
    });
  }
  const client = await ProtocolClient.connect(async () => {
    const stream = streams[calls++];
    assert.ok(stream);
    return stream;
  });
  await client.handshake("0123456789abcdef", "test", "0.2.0");
  const subscription = await client.subscribeEvents(6);
  pairs[0]?.[1].write(`${JSON.stringify(taskEvent(7, "task.output"))}\n`);
  assert.equal((await subscription.next()).value?.sequence, 7);

  await assert.rejects(
    () => client.reconnect(),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "AUTH_FAILED"
  );
  await assert.rejects(
    () => client.reconnect(),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "STORAGE_UNAVAILABLE"
  );
  await client.reconnect();
  assert.deepEqual(requests[3]?.[1]?.payload, { afterSequence: 7 });
  assert.equal(subscription.lastSequence, 7);
  client.close();
});

test("readArtifact joins 64 KiB chunks and verifies SHA-256", async () => {
  const content = Buffer.concat([Buffer.alloc(65_536, 0x61), Buffer.from("tail")]);
  const digest = createHash("sha256").update(content).digest("hex");
  const lengths: number[] = [];
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    const payload = request.payload as JsonObject;
    const offset = payload.offset as number;
    const length = payload.length as number;
    lengths.push(length);
    const data = content.subarray(offset, offset + length);
    const nextOffset = offset + data.byteLength;
    return response(request, {
      data: data.toString("base64url"),
      nextOffset,
      eof: nextOffset === content.byteLength,
      sizeBytes: content.byteLength,
      sha256: digest
    }, "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  const actual = await fixture.client.readArtifact(ARTIFACT_ID);
  assert.deepEqual(Buffer.from(actual), content);
  assert.deepEqual(lengths, [65_536, 65_536]);
  fixture.client.close();
});

test("readArtifact rejects an incorrect digest", async () => {
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, { data: "YQ", nextOffset: 1, eof: true, sizeBytes: 1, sha256: "0".repeat(64) }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /SHA-256/);
  fixture.client.close();
});

test("readArtifact rejects malformed Base64URL and non-progressing offsets", async () => {
  for (const chunk of [
    { data: "a+/=", nextOffset: 1, eof: true, sizeBytes: 1, sha256: "0".repeat(64) },
    { data: "", nextOffset: 0, eof: false, sizeBytes: 1, sha256: "0".repeat(64) }
  ]) {
    const fixture = scriptedClient((request) => request.method === "handshake"
      ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
      : response(request, chunk, "1.1"));
    await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
    await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /artifact chunk/);
    fixture.client.close();
  }
});

test("readArtifact rejects metadata changes between chunks", async () => {
  let chunks = 0;
  const fixture = scriptedClient((request) => request.method === "handshake"
    ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
    : response(request, chunks++ === 0
      ? { data: "YQ", nextOffset: 1, eof: false, sizeBytes: 2, sha256: "0".repeat(64) }
      : { data: "Yg", nextOffset: 2, eof: true, sizeBytes: 3, sha256: "1".repeat(64) }, "1.1"));
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /metadata changed/);
  fixture.client.close();
});

test("readArtifact rejects a declared total above the client download limit", async () => {
  let reads = 0;
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    reads++;
    const offset = (request.payload as JsonObject).offset as number;
    return response(request, offset === 0
      ? { data: "YQ", nextOffset: 1, eof: false, sizeBytes: CLIENT_MAX_ARTIFACT_BYTES + 1, sha256: "0".repeat(64) }
      : { data: "", nextOffset: 1, eof: true, sizeBytes: CLIENT_MAX_ARTIFACT_BYTES + 1, sha256: "0".repeat(64) }, "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /limit|too large/i);
  assert.equal(reads, 1);
  fixture.client.close();
});

test("readArtifact rejects unsafe size and offset integers", async () => {
  for (const chunk of [
    { data: "", nextOffset: 0, eof: true, sizeBytes: Number.MAX_SAFE_INTEGER + 1, sha256: "0".repeat(64) },
    { data: "YQ", nextOffset: Number.MAX_SAFE_INTEGER + 1, eof: true, sizeBytes: 1, sha256: "0".repeat(64) }
  ]) {
    const fixture = scriptedClient((request) => request.method === "handshake"
      ? response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1")
      : response(request, chunk, "1.1"));
    await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
    await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /safe integer/i);
    fixture.client.close();
  }
});

test("readArtifact rejects a non-EOF chunk that already reaches the declared size", async () => {
  const digest = createHash("sha256").update("a").digest("hex");
  let reads = 0;
  const fixture = scriptedClient((request) => {
    if (request.method === "handshake") {
      return response(request, { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }, "1.1");
    }
    reads++;
    return response(request, reads === 1
      ? { data: "YQ", nextOffset: 1, eof: false, sizeBytes: 1, sha256: digest }
      : { data: "", nextOffset: 1, eof: true, sizeBytes: 1, sha256: digest }, "1.1");
  });
  await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  await assert.rejects(() => fixture.client.readArtifact(ARTIFACT_ID), /EOF/i);
  assert.equal(reads, 1);
  fixture.client.close();
});
