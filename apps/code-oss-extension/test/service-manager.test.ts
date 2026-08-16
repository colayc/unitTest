import assert from "node:assert/strict";
import type { ChildProcessWithoutNullStreams } from "node:child_process";
import { EventEmitter } from "node:events";
import { access } from "node:fs/promises";
import { dirname } from "node:path";
import { PassThrough } from "node:stream";
import test from "node:test";
import type { ProtocolClient } from "@unit-test-ide/test-client";
import {
  ServiceManager,
  type ServiceManagerOptions,
  type ServiceOperations
} from "../src/service-manager.js";

type ExitCode = number | null;
type ExitSignal = NodeJS.Signals | null;

class FakeChild extends EventEmitter {
  readonly stdin = new PassThrough();
  readonly stdout = new PassThrough();
  readonly stderr = new PassThrough();
  exitCode: ExitCode = null;
  signalCode: ExitSignal = null;
  killCalls = 0;
  delayedKillSignal: NodeJS.Signals | undefined;

  constructor(private readonly deferKillExit = false) {
    super();
  }

  ready(endpoint: string): void {
    this.stdout.write(`READY ${endpoint}\n`);
  }

  exit(code: ExitCode, signal: ExitSignal): void {
    if (this.exitCode !== null || this.signalCode !== null) return;
    this.exitCode = code;
    this.signalCode = signal;
    this.emit("exit", code, signal);
  }

  kill(signal: NodeJS.Signals = "SIGTERM"): boolean {
    this.killCalls++;
    if (this.deferKillExit) this.delayedKillSignal = signal;
    else queueMicrotask(() => this.exit(null, signal));
    return true;
  }

  finishDelayedKill(): void {
    const signal = this.delayedKillSignal;
    assert.ok(signal);
    this.delayedKillSignal = undefined;
    this.exit(null, signal);
  }

  asChildProcess(): ChildProcessWithoutNullStreams {
    return this as unknown as ChildProcessWithoutNullStreams;
  }
}

class FakeClient {
  handshakeCalls = 0;
  capabilitiesCalls = 0;
  shutdownCalls = 0;
  closeCalls = 0;
  readonly #connectionCloseListeners = new Set<(error: Error) => void>();

  constructor(
    private readonly order: string[],
    private readonly child: FakeChild,
    private readonly handshakeFailure?: Error,
    private readonly capabilitiesFailure?: Error
  ) {
  }

  onConnectionClose(listener: (error: Error) => void): () => void {
    this.#connectionCloseListeners.add(listener);
    return () => this.#connectionCloseListeners.delete(listener);
  }

  async handshake(_token: string, _clientName: string, _clientVersion: string): Promise<{ negotiatedProtocolVersion: "1.4" }> {
    this.handshakeCalls++;
    this.order.push("handshake");
    if (this.handshakeFailure) throw this.handshakeFailure;
    return { negotiatedProtocolVersion: "1.4" };
  }

  async getCapabilities(): Promise<Record<string, never>> {
    this.capabilitiesCalls++;
    this.order.push("capabilities");
    if (this.capabilitiesFailure) throw this.capabilitiesFailure;
    return {};
  }

  async shutdown(): Promise<void> {
    this.shutdownCalls++;
    queueMicrotask(() => this.child.exit(0, null));
  }

  close(): void {
    this.closeCalls++;
    this.#notifyConnectionClose(new Error("connection closed"));
  }

  disconnect(error = new Error("connection lost")): void {
    this.#notifyConnectionClose(error);
  }

  #notifyConnectionClose(error: Error): void {
    const listeners = [...this.#connectionCloseListeners];
    this.#connectionCloseListeners.clear();
    for (const listener of listeners) listener(error);
  }

  asProtocolClient(): ProtocolClient {
    return this as unknown as ProtocolClient;
  }
}

interface HarnessOptions {
  trusted?: boolean;
  autoReady?: boolean;
  exitAfterReady?: boolean;
  deferKillExit?: boolean;
  timeoutMs?: number;
  handshakeFailure?: Error;
  capabilitiesFailure?: Error;
}

function createHarness(options: HarnessOptions = {}): {
  manager: ServiceManager;
  order: string[];
  tokens: string[];
  tokenFiles: string[];
  endpoints: string[];
  children: FakeChild[];
  clients: FakeClient[];
  calls: { prepare: number; spawn: number; connect: number };
} {
  const order: string[] = [];
  const tokens: string[] = [];
  const tokenFiles: string[] = [];
  const endpoints: string[] = [];
  const children: FakeChild[] = [];
  const clients: FakeClient[] = [];
  const calls = { prepare: 0, spawn: 0, connect: 0 };

  const operations: ServiceOperations = {
    async prepareTokenFile(_binary, tokenFile, token) {
      calls.prepare++;
      tokens.push(token);
      tokenFiles.push(tokenFile);
      order.push("prepare");
    },
    spawnService(_binary, args) {
      calls.spawn++;
      order.push("spawn");
      const endpoint = args[args.indexOf("--endpoint") + 1];
      assert.ok(endpoint);
      endpoints.push(endpoint);
      const child = new FakeChild(options.deferKillExit);
      children.push(child);
      if (options.autoReady !== false) {
        queueMicrotask(() => {
          order.push("READY");
          child.ready(endpoint);
          if (options.exitAfterReady) child.exit(8, null);
        });
      }
      return child.asChildProcess();
    },
    async connect(endpoint) {
      calls.connect++;
      order.push("connect");
      assert.equal(endpoint, endpoints.at(-1));
      const child = children.at(-1);
      assert.ok(child);
      const client = new FakeClient(
        order,
        child,
        options.handshakeFailure,
        options.capabilitiesFailure
      );
      clients.push(client);
      return client.asProtocolClient();
    }
  };
  const managerOptions: ServiceManagerOptions = {
    serviceExecutable: "C:\\private\\bin\\unit-test-service.exe",
    workspaceRoot: "C:\\private\\workspace",
    dataDirectory: "C:\\private\\data",
    timeoutMs: options.timeoutMs ?? 100,
    trusted: () => options.trusted !== false,
    operations
  };

  return {
    manager: new ServiceManager(managerOptions),
    order,
    tokens,
    tokenFiles,
    endpoints,
    children,
    clients,
    calls
  };
}

async function waitFor(predicate: () => boolean): Promise<void> {
  const deadline = Date.now() + 1_000;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error("condition was not reached");
    await new Promise<void>((resolve) => setImmediate(resolve));
  }
}

async function assertPathMissing(path: string): Promise<void> {
  await assert.rejects(
    () => access(path),
    (error: unknown) => (error as NodeJS.ErrnoException).code === "ENOENT"
  );
}

test("service lifecycle starts in prepare, spawn, READY, connect, handshake, capabilities order", async () => {
  const harness = createHarness();
  const session = await harness.manager.start();

  assert.deepEqual(harness.order, ["prepare", "spawn", "READY", "connect", "handshake", "capabilities"]);
  assert.equal(harness.manager.status.state, "running");
  assert.equal(harness.manager.session, session);
  await harness.manager.stop();
});

test("service lifecycle trust gate performs no external operation when untrusted", async () => {
  const harness = createHarness({ trusted: false });

  await assert.rejects(() => harness.manager.start(), /workspace is not trusted/);
  assert.deepEqual(harness.calls, { prepare: 0, spawn: 0, connect: 0 });
  assert.equal(harness.manager.status.state, "stopped");
});

test("service lifecycle startup failures enter failed and clean owned resources", async (t) => {
  const cases: Array<{ name: string; options: HarnessOptions; error: RegExp }> = [
    { name: "READY timeout", options: { autoReady: false, timeoutMs: 10 }, error: /service startup readiness timed out after 10ms/ },
    { name: "handshake", options: { handshakeFailure: new Error("handshake denied") }, error: /handshake denied/ },
    { name: "capabilities", options: { capabilitiesFailure: new Error("capabilities denied") }, error: /capabilities denied/ }
  ];

  for (const item of cases) {
    await t.test(item.name, async () => {
      const harness = createHarness(item.options);
      await assert.rejects(() => harness.manager.start(), item.error);
      assert.equal(harness.manager.status.state, "failed");
      assert.equal(harness.manager.session, undefined);
      assert.equal(harness.children[0]?.killCalls, 1);
      if (harness.clients[0]) assert.equal(harness.clients[0].closeCalls, 1);
      const tokenFile = harness.tokenFiles[0];
      assert.ok(tokenFile);
      await assertPathMissing(dirname(tokenFile));
      const endpoint = harness.endpoints[0];
      if (process.platform !== "win32" && endpoint) await assertPathMissing(dirname(endpoint));
    });
  }
});

test("service lifecycle stop is idempotent for the connection and child", async () => {
  const harness = createHarness();
  await harness.manager.start();

  await Promise.all([harness.manager.stop(), harness.manager.stop()]);
  await harness.manager.stop();

  assert.equal(harness.clients[0]?.shutdownCalls, 1);
  assert.equal(harness.clients[0]?.closeCalls, 1);
  assert.equal(harness.children[0]?.killCalls, 0);
  assert.equal(harness.manager.status.state, "stopped");
});

test("service restart replaces the token, endpoint, and client", async () => {
  const harness = createHarness();
  const first = await harness.manager.start();
  const second = await harness.manager.restart();

  assert.notEqual(harness.tokens[0], harness.tokens[1]);
  assert.notEqual(first.endpoint, second.endpoint);
  assert.notEqual(first.client, second.client);
  await harness.manager.stop();
});

test("service lifecycle enters failed when the child exits", async () => {
  const harness = createHarness();
  await harness.manager.start();

  harness.children[0]?.exit(9, null);
  await waitFor(() => harness.manager.status.state === "failed" && harness.manager.session === undefined);

  assert.equal(harness.clients[0]?.closeCalls, 1);
  assert.equal(harness.manager.session, undefined);
});

test("service lifecycle rejects a child exit between READY and connection", async () => {
  const harness = createHarness({ exitAfterReady: true });

  await assert.rejects(() => harness.manager.start(), /service exited/);

  assert.equal(harness.manager.status.state, "failed");
  assert.equal(harness.manager.session, undefined);
  assert.equal(harness.calls.connect, 0);
});

test("service lifecycle enters failed when the connection closes", async () => {
  const harness = createHarness();
  await harness.manager.start();
  assert.equal(harness.clients[0] instanceof EventEmitter, false);

  harness.clients[0]?.disconnect();
  await waitFor(() => harness.manager.status.state === "failed" && harness.manager.session === undefined);

  assert.equal(harness.children[0]?.killCalls, 1);
  assert.equal(harness.manager.session, undefined);
});

test("service lifecycle serializes delayed failure cleanup before replacement start", async () => {
  const harness = createHarness({ deferKillExit: true });
  const first = await harness.manager.start();

  harness.clients[0]?.disconnect(new Error("delayed disconnect"));
  const replacementPromise = harness.manager.start();
  await waitFor(() => harness.children[0]?.killCalls === 1);
  await new Promise<void>((resolve) => setImmediate(resolve));

  assert.equal(harness.calls.prepare, 1);
  assert.equal(harness.manager.session, first);
  harness.children[0]?.finishDelayedKill();

  const replacement = await replacementPromise;
  assert.notEqual(replacement, first);
  assert.equal(harness.manager.session, replacement);
  assert.equal(harness.manager.status.state, "running");
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(harness.manager.status.state, "running");
  await harness.manager.stop();
});

test("service lifecycle diagnostics retain only redacted stdout and stderr", async () => {
  const harness = createHarness();
  await harness.manager.start();
  const token = harness.tokens[0];
  assert.ok(token);
  harness.children[0]?.stdout.write(`safe-out ${token} C:\\private\\workspace\n`);
  harness.children[0]?.stderr.write("safe-err C:\\private\\bin\\unit-test-service.exe\n");

  harness.children[0]?.exit(7, null);
  await waitFor(() => harness.manager.status.state === "failed");

  const detail = harness.manager.status.detail ?? "";
  assert.match(detail, /stdout=[\s\S]*safe-out/);
  assert.match(detail, /stderr=[\s\S]*safe-err/);
  assert.doesNotMatch(detail, new RegExp(token));
  assert.doesNotMatch(detail, /private|unit-test-service\.exe/);
});

test("repeated service lifecycle never reuses resources or cleans an owner twice", async (t) => {
  const harness = createHarness();
  const clients: ProtocolClient[] = [];
  const unhandled: unknown[] = [];
  const onUnhandled = (error: unknown) => unhandled.push(error);
  process.on("unhandledRejection", onUnhandled);
  t.after(() => process.off("unhandledRejection", onUnhandled));

  for (let iteration = 0; iteration < 50; iteration++) {
    const first = await harness.manager.start();
    clients.push(first.client);
    await harness.manager.stop();
    await assertPathMissing(first.sessionDirectory);
    if (process.platform !== "win32") await assertPathMissing(dirname(first.endpoint));
    const second = await harness.manager.start();
    clients.push(second.client);
    await harness.manager.stop();
    await assertPathMissing(second.sessionDirectory);
    if (process.platform !== "win32") await assertPathMissing(dirname(second.endpoint));
  }
  await new Promise<void>((resolve) => setImmediate(resolve));

  assert.equal(new Set(harness.tokens).size, 100);
  assert.equal(new Set(harness.endpoints).size, 100);
  assert.equal(new Set(clients).size, 100);
  assert.ok(harness.clients.every((client) => client.shutdownCalls === 1 && client.closeCalls === 1));
  assert.ok(harness.children.every((child) => child.killCalls === 0));
  assert.deepEqual(unhandled, []);
});
