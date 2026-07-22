import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { Duplex, PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import test from "node:test";
import { MAX_MESSAGE_BYTES, ProtocolClient } from "./client.js";
import { ProtocolError } from "./envelopes.js";
import type { EventSubscription } from "./subscription.js";

type JsonObject = Record<string, unknown>;
const MESSAGE_ID = "fedcba9876543210fedcba9876543210";
const TASK_ID = "11111111111111111111111111111111";
const ARTIFACT_ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const SENT_AT = "2026-07-21T00:00:00Z";

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
  const payload = { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0", padding: "" };
  let line = JSON.stringify(response(request, payload, "1.0"));
  const paddingSize = size - Buffer.byteLength(line);
  assert.ok(paddingSize >= 0, `requested line size ${size} is smaller than response envelope`);
  payload.padding = "x".repeat(paddingSize);
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
    serverStream.write(`${JSON.stringify(response(request, payload, "1.0"))}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.1.0");
  assert.equal((await client.getCapabilities()).platform, "windows");
  await client.shutdown();
  assert.deepEqual(methods, ["handshake", "capabilities/get", "shutdown"]);
  client.close();
});

test("client exposes stable server error codes", async () => {
  const { client } = scriptedClient((request) => error(request, "AUTH_FAILED", false, "1.1"));
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
  assert.equal(result.serviceVersion, "0.1.0");
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
    if (request.protocolVersion === "1.1") return error(request, "UNSUPPORTED_PROTOCOL", false, "1.1");
    return response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
  });
  const negotiated = await fixture.client.handshake("0123456789abcdef", "test", "0.2.0");
  assert.equal(negotiated.negotiatedProtocolVersion, "1.0");
  assert.deepEqual(fixture.requests.map(({ protocolVersion }) => protocolVersion), ["1.1", "1.0"]);
  assert.deepEqual(fixture.requests[0]?.payload, {
    token: "0123456789abcdef",
    clientName: "test",
    clientVersion: "0.2.0",
    supportedProtocolVersions: ["1.1", "1.0"]
  });
  assert.equal("supportedProtocolVersions" in (fixture.requests[1]?.payload as JsonObject), false);
  fixture.client.close();
});

test("client does not downgrade handshake errors other than UNSUPPORTED_PROTOCOL", async () => {
  const fixture = scriptedClient((request) => error(request, "AUTH_FAILED", false, "1.1"));
  await assert.rejects(
    () => fixture.client.handshake("0123456789abcdef", "test", "0.2.0"),
    (failure: unknown) => failure instanceof ProtocolError && failure.code === "AUTH_FAILED"
  );
  assert.equal(fixture.requests.length, 1);
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
  const subscription = await client.subscribeEvents(0);
  first[1].write(`${JSON.stringify(taskEvent(4, "task.started"))}\n`);
  assert.equal((await subscription.next()).value?.sequence, 4);
  await client.reconnect();
  assert.equal(calls, 2);
  assert.deepEqual((requests[1]?.[0]?.payload as JsonObject).supportedProtocolVersions, ["1.1", "1.0"]);
  assert.deepEqual(requests[1]?.[1]?.payload, { afterSequence: 4 });

  first[1].write(`${JSON.stringify(taskEvent(5, "task.output", { payload: { old: true } }))}\n`);
  second[1].write(`${JSON.stringify(taskEvent(5, "task.output"))}\n`);
  const next = await subscription.next();
  assert.equal(next.value?.sequence, 5);
  assert.deepEqual(next.value?.payload, {});
  assert.equal(subscription.lastSequence, 5);
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

test("reconnect rejects a downgraded session when an active subscription must be restored", async () => {
  const first = pair();
  const second = pair();
  const streams = [first[0], second[0]];
  let calls = 0;
  createInterface({ input: first[1] }).on("line", (line) => {
    const request = JSON.parse(line) as JsonObject;
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.1", serviceVersion: "0.1.0" }
      : { afterSequence: 0 };
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
  const subscription = await client.subscribeEvents(0);
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
        reply = error(request, "AUTH_FAILED", false, "1.1");
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
  const subscription = await client.subscribeEvents(0);
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
