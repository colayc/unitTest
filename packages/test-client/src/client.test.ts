import assert from "node:assert/strict";
import { Duplex, PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import test from "node:test";
import { MAX_MESSAGE_BYTES, ProtocolClient } from "./client.js";
import { ProtocolError } from "./envelopes.js";

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

function response(request: Record<string, unknown>, payload: Record<string, unknown>) {
  return { protocolVersion: "1.0", kind: "response", messageId: "fedcba9876543210fedcba9876543210", requestId: request.messageId, method: request.method, sentAt: "2026-07-21T00:00:00Z", payload };
}

function responseLineOfSize(request: Record<string, unknown>, size: number): string {
  const payload = { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0", padding: "" };
  let line = JSON.stringify(response(request, payload));
  const paddingSize = size - Buffer.byteLength(line);
  assert.ok(paddingSize >= 0, `requested line size ${size} is smaller than response envelope`);
  payload.padding = "x".repeat(paddingSize);
  line = JSON.stringify(response(request, payload));
  assert.equal(Buffer.byteLength(line), size);
  return line;
}

test("client performs handshake, capabilities, and shutdown in order", async () => {
  const [clientStream, serverStream] = pair();
  const methods: string[] = [];
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line);
    methods.push(request.method);
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }
      : request.method === "capabilities/get"
        ? { platform: "windows", transports: ["named-pipe"], toolchains: [], frameworks: [], coverageTools: [] }
        : { accepted: true };
    serverStream.write(`${JSON.stringify(response(request, payload))}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.1.0");
  assert.equal((await client.getCapabilities()).platform, "windows");
  await client.shutdown();
  assert.deepEqual(methods, ["handshake", "capabilities/get", "shutdown"]);
  client.close();
});

test("client exposes stable server error codes", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line);
    serverStream.write(`${JSON.stringify({ protocolVersion: "1.0", kind: "error", messageId: "fedcba9876543210fedcba9876543210", requestId: request.messageId, sentAt: "2026-07-21T00:00:00Z", error: { code: "AUTH_FAILED", message: "authentication failed", retryable: false } })}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("wrong-token-value", "test", "0.1.0"), (error: unknown) => error instanceof ProtocolError && error.code === "AUTH_FAILED");
  client.close();
});

test("client accepts a fragmented exact-limit response with CRLF", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line);
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
    const request = JSON.parse(line);
    serverStream.write(responseLineOfSize(request, MAX_MESSAGE_BYTES + 1));
    serverStream.write("\r");
    setImmediate(() => serverStream.write("\n"));
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
