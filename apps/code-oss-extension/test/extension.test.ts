import assert from "node:assert/strict";
import test from "node:test";
import {
  ProtocolClient,
  type WorkspaceSnapshot as ProtocolWorkspaceSnapshot
} from "@unit-test-ide/test-client";
import {
  createExtensionController,
  type ExtensionHost,
  type LifecycleManager
} from "../src/extension.js";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";
import { createProtocolClient } from "../src/protocol-client.js";
import type { ServiceManagerOptions } from "../src/service-manager.js";

type Listener = () => void | Promise<void>;

class FakeProtocolClient implements ExtensionProtocolClient {
  inspectCalls = 0;
  buildCalls = 0;
  closeCalls = 0;
  inspectFailure: Error | undefined;

  constructor(private readonly workspace: ProtocolWorkspaceSnapshot) {}

  async inspectWorkspace(): Promise<ProtocolWorkspaceSnapshot> {
    this.inspectCalls++;
    if (this.inspectFailure) throw this.inspectFailure;
    return this.workspace;
  }

  close(): void {
    this.closeCalls++;
  }
}

class FakeServiceManager implements LifecycleManager {
  startCalls = 0;
  stopCalls = 0;
  status: LifecycleManager["status"] = { state: "stopped" };
  session: LifecycleManager["session"];
  startFailure: Error | undefined;
  stopFailure: Error | undefined;
  publishStopFailureStatus = true;
  startPromise: Promise<void> | undefined;
  stopPromise: Promise<void> | undefined;

  constructor(client?: ExtensionProtocolClient) {
    if (client) this.session = { client };
  }

  async start(): Promise<{ client: ExtensionProtocolClient }> {
    this.startCalls++;
    this.status = { state: "starting" };
    if (this.startFailure) {
      this.status = { state: "failed", detail: this.startFailure.message };
      throw this.startFailure;
    }
    if (this.startPromise) await this.startPromise;
    const session = this.session ?? { client: new FakeProtocolClient(workspaceSnapshot("created")) };
    this.session = session;
    this.status = { state: "running" };
    return session;
  }

  async stop(): Promise<void> {
    this.stopCalls++;
    this.status = { state: "stopping" };
    if (this.stopPromise) await this.stopPromise;
    if (this.stopFailure) {
      if (this.publishStopFailureStatus) {
        this.status = { state: "failed", detail: this.stopFailure.message };
      }
      throw this.stopFailure;
    }
    this.session = undefined;
    this.status = { state: "stopped" };
  }
}

function workspaceSnapshot(generation: string): ProtocolWorkspaceSnapshot {
  return {
    capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
    diagnostics: [],
    projects: [],
    toolchains: [],
    workspaceGeneration: generation,
    workspaceUri: "file:///workspace"
  };
}

interface HarnessOptions {
  folderCount?: number;
  isTrusted?: boolean;
  autoStart?: boolean;
  manager?: FakeServiceManager;
  stopTimeoutMs?: number;
  serviceExecutable?: string;
  managerFactory?: (options: ServiceManagerOptions) => LifecycleManager;
}

function createExtensionHarness(options: HarnessOptions = {}) {
  const commands = new Map<string, () => void | Promise<void>>();
  const workspaceListeners = new Set<Listener>();
  const trustListeners = new Set<Listener>();
  const output: string[] = [];
  const errors: string[] = [];
  const subscriptions: Array<{ dispose(): void }> = [];
  const state = {
    folderCount: options.folderCount ?? 1,
    isTrusted: options.isTrusted ?? true,
    workspaceRoot: "C:\\workspace",
    statusText: ""
  };
  const manager = options.manager ?? new FakeServiceManager();

  const disposable = (dispose: () => void) => ({ dispose });
  const host: ExtensionHost = {
    context: { subscriptions },
    extensionPath: "C:\\extension",
    dataDirectory: "C:\\extension-data",
    workspaceSnapshot: () => ({
      folderCount: state.folderCount,
      isTrusted: state.isTrusted,
      workspaceRoot: state.folderCount === 1 ? state.workspaceRoot : undefined
    }),
    configuration: (key, fallback) => {
      if (key === "autoStart") return (options.autoStart ?? true) as typeof fallback;
      if (key === "serviceExecutable") {
        return (options.serviceExecutable ?? fallback) as typeof fallback;
      }
      return fallback;
    },
    createOutputChannel: () => ({
      appendLine(value) { output.push(value); },
      dispose() {}
    }),
    createStatusBarItem: () => ({
      get text() { return state.statusText; },
      set text(value: string) { state.statusText = value; },
      show() {},
      dispose() {}
    }),
    registerCommand(command, handler) {
      commands.set(command, handler);
      return disposable(() => commands.delete(command));
    },
    onDidChangeWorkspaceFolders(listener) {
      workspaceListeners.add(listener);
      return disposable(() => workspaceListeners.delete(listener));
    },
    onDidGrantWorkspaceTrust(listener) {
      trustListeners.add(listener);
      return disposable(() => trustListeners.delete(listener));
    },
    async showErrorMessage(message) { errors.push(message); }
  };
  const controller = createExtensionController(host, {
    ...(options.managerFactory ? { managerFactory: options.managerFactory } : { manager }),
    stopTimeoutMs: options.stopTimeoutMs
  });

  return {
    manager,
    output,
    errors,
    get statusText() { return state.statusText; },
    activate: () => controller.activate(),
    deactivate: () => controller.deactivate(),
    async execute(command: string) {
      const handler = commands.get(command);
      assert.ok(handler, `command ${command} was not registered`);
      await handler();
    },
    async updateWorkspace(folderCount: number, isTrusted: boolean, workspaceRoot = state.workspaceRoot) {
      state.folderCount = folderCount;
      state.isTrusted = isTrusted;
      state.workspaceRoot = workspaceRoot;
      for (const listener of [...workspaceListeners]) await listener();
    },
    async grantTrust() {
      state.isTrusted = true;
      for (const listener of [...trustListeners]) await listener();
    },
    queueTrustChange(isTrusted: boolean) {
      state.isTrusted = isTrusted;
      return Promise.all([...trustListeners].map((listener) => listener())).then(() => undefined);
    },
    queueWorkspaceChange(
      folderCount: number,
      isTrusted: boolean,
      workspaceRoot = state.workspaceRoot
    ) {
      state.folderCount = folderCount;
      state.isTrusted = isTrusted;
      state.workspaceRoot = workspaceRoot;
      return Promise.all([...workspaceListeners].map((listener) => listener())).then(() => undefined);
    }
  };
}

test("untrusted activation publishes blocked status and does not start service", async () => {
  const host = createExtensionHarness({ folderCount: 1, isTrusted: false });

  await host.activate();

  assert.equal(host.manager.startCalls, 0);
  assert.equal(host.statusText, "Unit Test: Untrusted Workspace");
});

test("inspect command delegates only to workspace/inspect", async () => {
  const client = new FakeProtocolClient(workspaceSnapshot("a"));
  const manager = new FakeServiceManager(client);
  manager.status = { state: "running" };
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  await host.execute("unitTestIde.inspectWorkspace");

  assert.equal(client.inspectCalls, 1);
  assert.equal(client.buildCalls, 0);
  assert.equal(JSON.parse(host.output[0] ?? "null").workspaceGeneration, "a");
});

test("trusted single-root activation autostarts and publishes ready status", async () => {
  const host = createExtensionHarness();

  await host.activate();

  assert.equal(host.manager.startCalls, 1);
  assert.equal(host.statusText, "Unit Test: Service Ready");
});

test("commands are blocked before touching the manager outside a trusted single-root workspace", async (t) => {
  const cases = [
    { name: "no workspace", folderCount: 0, isTrusted: false, expected: /Open a workspace/ },
    { name: "multi-root", folderCount: 2, isTrusted: true, expected: /Multi-root workspaces are not supported/ },
    { name: "untrusted", folderCount: 1, isTrusted: false, expected: /Trust this workspace/ }
  ];

  for (const item of cases) {
    await t.test(item.name, async () => {
      const host = createExtensionHarness({
        folderCount: item.folderCount,
        isTrusted: item.isTrusted
      });
      await host.activate();

      await host.execute("unitTestIde.startService");
      await host.execute("unitTestIde.stopService");
      await host.execute("unitTestIde.inspectWorkspace");

      assert.equal(host.manager.startCalls, 0);
      assert.equal(host.manager.stopCalls, 0);
      assert.equal(host.errors.length, 3);
      assert.match(host.errors[0] ?? "", item.expected);
    });
  }
});

test("start and stop commands drive service lifecycle and status projection", async () => {
  const host = createExtensionHarness({ autoStart: false });
  await host.activate();

  await host.execute("unitTestIde.startService");
  assert.equal(host.manager.startCalls, 1);
  assert.equal(host.statusText, "Unit Test: Service Ready");

  await host.execute("unitTestIde.stopService");
  assert.equal(host.manager.stopCalls, 1);
  assert.equal(host.statusText, "Unit Test: Service Stopped");
});

test("commands publish starting and stopping while lifecycle operations are pending", async () => {
  let releaseStart: (() => void) | undefined;
  const manager = new FakeServiceManager();
  manager.startPromise = new Promise<void>((resolve) => { releaseStart = resolve; });
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  const starting = host.execute("unitTestIde.startService");
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(host.statusText, "Unit Test: Starting Service");
  assert.ok(releaseStart);
  releaseStart();
  await starting;

  let releaseStop: (() => void) | undefined;
  manager.stopPromise = new Promise<void>((resolve) => { releaseStop = resolve; });
  const stopping = host.execute("unitTestIde.stopService");
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(host.statusText, "Unit Test: Stopping Service");
  assert.ok(releaseStop);
  releaseStop();
  await stopping;
});

test("granting trust autostarts and losing the workspace stops the service", async () => {
  const host = createExtensionHarness({ isTrusted: false });
  await host.activate();

  await host.grantTrust();
  assert.equal(host.manager.startCalls, 1);

  await host.updateWorkspace(0, false);
  assert.equal(host.manager.stopCalls, 1);
  assert.equal(host.statusText, "Unit Test: No Workspace");
});

test("activation replaces the service when the single workspace root changes", async () => {
  const host = createExtensionHarness();
  await host.activate();

  await host.updateWorkspace(1, true, "C:\\other-workspace");

  assert.equal(host.manager.stopCalls, 1);
  assert.equal(host.manager.startCalls, 2);
  assert.equal(host.statusText, "Unit Test: Service Ready");
});

test("manager failures show the redacted manager detail and publish failed status", async () => {
  const manager = new FakeServiceManager();
  manager.startFailure = new Error("service failed at [redacted]");
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  await host.execute("unitTestIde.startService");

  assert.deepEqual(host.errors, ["service failed at [redacted]"]);
  assert.equal(host.statusText, "Unit Test: Service Failed");
});

test("deactivation bounds an unresponsive manager stop", async () => {
  const manager = new FakeServiceManager();
  manager.stopPromise = new Promise<void>(() => undefined);
  const host = createExtensionHarness({ autoStart: false, manager, stopTimeoutMs: 10 });
  await host.activate();
  const started = Date.now();

  await host.deactivate();

  assert.equal(manager.stopCalls, 1);
  assert.ok(Date.now() - started < 500);
});

test("deactivation reports a redacted manager stop failure", async () => {
  const manager = new FakeServiceManager();
  manager.stopFailure = new Error("shutdown failed at [redacted]");
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  await host.deactivate();

  assert.deepEqual(host.errors, ["shutdown failed at [redacted]"]);
  assert.equal(host.statusText, "Unit Test: Service Failed");
});

test("queued trust reconciliation cannot start after deactivation begins", async () => {
  const host = createExtensionHarness({ isTrusted: false });
  await host.activate();

  const transition = host.queueTrustChange(true);
  const firstDeactivate = host.deactivate();
  const secondDeactivate = host.deactivate();
  assert.equal(firstDeactivate, secondDeactivate);
  await Promise.all([transition, firstDeactivate]);

  assert.equal(host.manager.startCalls, 0);
  assert.equal(host.manager.stopCalls, 1);
  assert.equal(host.manager.status.state, "stopped");
  assert.equal(host.manager.session, undefined);

  await host.queueTrustChange(true);
  assert.equal(host.manager.startCalls, 0);
  assert.equal(host.manager.stopCalls, 1);
});

test("inspect command rechecks live trust while workspace stop is pending", async () => {
  const client = new FakeProtocolClient(workspaceSnapshot("live-trust"));
  const manager = new FakeServiceManager(client);
  manager.status = { state: "running" };
  let releaseStop: (() => void) | undefined;
  manager.stopPromise = new Promise<void>((resolve) => { releaseStop = resolve; });
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  const rootTransition = host.queueWorkspaceChange(1, true, "C:\\replacement");
  await new Promise<void>((resolve) => setImmediate(resolve));
  assert.equal(manager.stopCalls, 1);
  const trustTransition = host.queueWorkspaceChange(1, false, "C:\\replacement");
  await host.execute("unitTestIde.inspectWorkspace");

  assert.equal(client.inspectCalls, 0);
  assert.match(host.errors.at(-1) ?? "", /Trust this workspace/);
  assert.ok(releaseStop);
  releaseStop();
  await Promise.all([rootTransition, trustTransition]);
});

test("default activation resolves an empty executable setting to the bundled service", async () => {
  const manager = new FakeServiceManager();
  let captured: ServiceManagerOptions | undefined;
  const host = createExtensionHarness({
    serviceExecutable: "",
    managerFactory(options) {
      captured = options;
      return manager;
    }
  });

  await host.activate();

  assert.ok(captured);
  assert.notEqual(captured.serviceExecutable, "");
  assert.match(captured.serviceExecutable, /bin[\\/]+unit-test-service(?:\.exe)?$/);
  assert.equal(manager.startCalls, 1);
});

test("protocol adapter delegates its endpoint to ProtocolClient.connect", async () => {
  const original = ProtocolClient.connect;
  const client = new FakeProtocolClient(workspaceSnapshot("adapter"));
  let received: Parameters<typeof ProtocolClient.connect>[0] | undefined;
  ProtocolClient.connect = async (endpoint) => {
    received = endpoint;
    return client as unknown as ProtocolClient;
  };
  try {
    const connected = await createProtocolClient("\\\\.\\pipe\\adapter-test");
    assert.equal(connected, client);
    assert.equal(received, "\\\\.\\pipe\\adapter-test");
  } finally {
    ProtocolClient.connect = original;
  }
});

test("inspect errors never present raw paths or tokens", async () => {
  const client = new FakeProtocolClient(workspaceSnapshot("inspect-error"));
  const token = "vK4MPRV9Ih3oqoJr48fLQW1z4oYwz0LVEGs6x7sVQwA";
  client.inspectFailure = new Error(`inspect failed ${token} at C:\\private\\workspace`);
  const manager = new FakeServiceManager(client);
  manager.status = { state: "running" };
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  await host.execute("unitTestIde.inspectWorkspace");

  assert.equal(host.errors.length, 1);
  assert.doesNotMatch(host.errors[0] ?? "", /private|vK4MPR/);
});

test("cleanup errors without manager detail never present raw paths or tokens", async () => {
  const manager = new FakeServiceManager();
  const token = "uX9pJ6xNvz1qPyE6C9yCbY0KJ2mN5qR8WvT4aHs7FgA";
  manager.stopFailure = new Error(`cleanup failed ${token} at C:\\private\\session`);
  manager.publishStopFailureStatus = false;
  const host = createExtensionHarness({ autoStart: false, manager });
  await host.activate();

  await host.execute("unitTestIde.stopService");

  assert.equal(host.errors.length, 1);
  assert.doesNotMatch(host.errors[0] ?? "", /private|session|uX9pJ6/);
});
