import assert from "node:assert/strict";
import {
  execFile as execFileCallback,
  spawn,
  type ChildProcessWithoutNullStreams
} from "node:child_process";
import {
  access,
  mkdir,
  mkdtemp,
  rm,
  writeFile
} from "node:fs/promises";
import { createConnection } from "node:net";
import { dirname, join, resolve } from "node:path";
import { promisify } from "node:util";
import test from "node:test";
import { ProtocolClient } from "@unit-test-ide/test-client";
import {
  createExtensionController,
  type ExtensionHost,
  type LifecycleManager
} from "../src/extension.js";
import {
  ServiceManager,
  type ServiceManagerOptions,
  type ServiceOperations,
  type ServiceSession
} from "../src/service-manager.js";
import { redactServiceError } from "../src/service-resources.js";
import type {
  TestingController,
  TestingRun,
  TestingRunProfile,
  TestingRunProfileHandler,
  TestingTestItem,
  TestingTestItemCollection
} from "../src/testing-api.js";

const execFile = promisify(execFileCallback);
const repositoryRoot = resolve(import.meta.dirname, "../../../..");
const serviceBinary = process.env.UNIT_TEST_IDE_SERVICE_BINARY?.trim() || join(
  repositoryRoot,
  "build",
  process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service"
);
const cmakeFixture = join(
  repositoryRoot,
  "build",
  process.platform === "win32" ? "cmake-fixture.exe" : "cmake-fixture"
);

interface Fixture {
  readonly root: string;
  readonly workspace: string;
  readonly dataDirectory: string;
}

interface ObservedChild {
  readonly process: ChildProcessWithoutNullStreams;
  exited: boolean;
}

interface Observations {
  readonly order: string[];
  readonly testingCalls: string[];
  readonly tokens: string[];
  readonly preparedTokenFiles: string[];
  readonly spawnArguments: string[][];
  readonly connectedEndpoints: string[];
  readonly children: ObservedChild[];
}

function createObservations(): Observations {
  return {
    order: [],
    testingCalls: [],
    tokens: [],
    preparedTokenFiles: [],
    spawnArguments: [],
    connectedEndpoints: [],
    children: []
  };
}

async function createFixture(): Promise<Fixture> {
  const fixtureParent = join(repositoryRoot, "build");
  await mkdir(fixtureParent, { recursive: true });
  const root = await mkdtemp(join(fixtureParent, "unit-test-ide-extension-smoke-"));
  const workspace = join(root, "workspace");
  await mkdir(join(workspace, ".unit-test-ide"), { recursive: true });
  await writeFile(
    join(workspace, ".unit-test-ide", "workspace.json"),
    JSON.stringify({
      version: 2,
      cmake: { executable: cmakeFixture },
      projects: [{
        id: "root",
        sourceDir: ".",
        fallback: { configurations: ["Debug"] },
        tests: {
          containers: [{ ctestName: "framework-tests", framework: "cpputest" }]
        }
      }]
    }),
    "utf8"
  );
  await writeFile(
    join(workspace, "CMakeLists.txt"),
    [
      "cmake_minimum_required(VERSION 3.25)",
      "project(test_framework_fixture LANGUAGES CXX)",
      "add_executable(fixture-app main.cpp)",
      "add_test(NAME framework-tests COMMAND fixture-app)",
      ""
    ].join("\n"),
    "utf8"
  );
  await writeFile(join(workspace, "main.cpp"), "int main() { return 0; }\n", "utf8");
  await writeFile(
    join(workspace, "CMakePresets.json"),
    JSON.stringify({
      version: 6,
      configurePresets: [{
        name: "fixture",
        generator: "Ninja",
        binaryDir: "${sourceDir}/build-fixture"
      }]
    }),
    "utf8"
  );
  return { root, workspace, dataDirectory: join(root, "service-data") };
}

class SmokeCollection implements TestingTestItemCollection {
  readonly entries = new Map<string, TestingTestItem>();

  add(item: TestingTestItem): void { this.entries.set(item.id, item); }
  delete(id: string): void { this.entries.delete(id); }
  get(id: string): TestingTestItem | undefined { return this.entries.get(id); }
  replace(items: readonly TestingTestItem[]): void {
    this.entries.clear();
    for (const item of items) this.entries.set(item.id, item);
  }
}

interface SmokeRunCapture {
  readonly started: string[];
  readonly passed: string[];
  readonly failed: string[];
  readonly skipped: string[];
  readonly errored: string[];
  ends: number;
}

class SmokeTestingController implements TestingController {
  readonly items = new SmokeCollection();
  readonly profiles: Array<{ handler: TestingRunProfileHandler; profile: TestingRunProfile }> = [];
  readonly runs: SmokeRunCapture[] = [];
  refreshHandler: (() => void | Promise<void>) | undefined;

  dispose(): void {}

  createTestItem(id: string, label: string, uri?: unknown): TestingTestItem {
    return { id, label, uri, children: new SmokeCollection() };
  }

  createRunProfile(
    label: string,
    _kind: "run",
    handler: TestingRunProfileHandler
  ): TestingRunProfile {
    const profile = { label, dispose() {} };
    this.profiles.push({ handler, profile });
    return profile;
  }

  createTestRun(): TestingRun {
    const capture: SmokeRunCapture = {
      started: [], passed: [], failed: [], skipped: [], errored: [], ends: 0
    };
    this.runs.push(capture);
    return {
      started: (item) => { capture.started.push(item.id); },
      passed: (item) => { capture.passed.push(item.id); },
      failed: (item) => { capture.failed.push(item.id); },
      skipped: (item) => { capture.skipped.push(item.id); },
      errored: (item) => { capture.errored.push(item.id); },
      end: () => { capture.ends++; },
      dispose() {}
    };
  }
}

function nestedItems(collection: SmokeCollection): TestingTestItem[] {
  const result: TestingTestItem[] = [];
  for (const item of collection.entries.values()) {
    result.push(item);
    if (item.children instanceof SmokeCollection) result.push(...nestedItems(item.children));
  }
  return result;
}

async function eventually(assertion: () => void): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 600; attempt++) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 50));
    }
  }
  throw lastError;
}

async function assertPathMissing(path: string): Promise<void> {
  await assert.rejects(
    () => access(path),
    (error: unknown) => (error as NodeJS.ErrnoException).code === "ENOENT"
  );
}

async function withRedactedFailure<T>(
  sensitive: string[],
  action: () => Promise<T>
): Promise<T> {
  try {
    return await action();
  } catch (error) {
    throw redactServiceError(error, sensitive);
  }
}

function createRealOperations(
  observations: Observations,
  expected: Pick<ServiceManagerOptions, "serviceExecutable" | "workspaceRoot" | "dataDirectory">
): ServiceOperations {
  return {
    async prepareTokenFile(binary, tokenFile, token) {
      observations.order.push("prepare");
      observations.tokens.push(token);
      observations.preparedTokenFiles.push(tokenFile);
      assert.equal(binary, expected.serviceExecutable);
      await execFile(binary, ["--prepare-token-file", tokenFile], { windowsHide: true });
      await writeFile(tokenFile, token, { flag: "r+" });
    },
    spawnService(binary, args) {
      observations.order.push("spawn");
      observations.spawnArguments.push([...args]);
      const endpoint = args[1];
      const tokenFile = args[3];
      assert.ok(endpoint);
      assert.ok(tokenFile);
      assert.equal(binary, expected.serviceExecutable);
      assert.deepEqual(args, [
        "--endpoint", endpoint,
        "--token-file", tokenFile,
        "--data-dir", expected.dataDirectory,
        "--workspace-root", expected.workspaceRoot,
        "--trusted-workspace=true"
      ]);
      const child = spawn(binary, args, { windowsHide: true, stdio: "pipe" });
      const observed = { process: child, exited: false };
      observations.children.push(observed);
      child.once("exit", () => { observed.exited = true; });
      return child;
    },
    async connect(endpoint) {
      observations.order.push("connect");
      observations.connectedEndpoints.push(endpoint);
      const client = await ProtocolClient.connect(endpoint);
      const tracked = new Map<PropertyKey, string>([
        ["inspectWorkspace", "workspace/inspect"],
        ["discoverTests", "discoverTests"],
        ["getTestCatalog", "catalog"],
        ["runTests", "runTests"]
      ]);
      return new Proxy(client, {
        get(target, property) {
          const value = Reflect.get(target, property, target);
          if (typeof value !== "function") return value;
          return (...args: unknown[]) => {
            const label = tracked.get(property);
            if (label) observations.testingCalls.push(label);
            return Reflect.apply(value, target, args);
          };
        }
      });
    }
  };
}

function managerOptions(
  fixture: Fixture,
  observations: Observations,
  trusted: () => boolean
): ServiceManagerOptions {
  const options = {
    serviceExecutable: serviceBinary,
    workspaceRoot: fixture.workspace,
    dataDirectory: fixture.dataDirectory,
    timeoutMs: 10_000,
    trusted
  };
  return {
    ...options,
    operations: createRealOperations(observations, options)
  };
}

function collectSessionSecrets(
  session: ServiceSession,
  observations: Observations,
  sensitive: string[]
): void {
  sensitive.push(session.endpoint, session.tokenFile, session.sessionDirectory);
  sensitive.push(...observations.tokens);
  if (process.platform !== "win32") sensitive.push(dirname(session.endpoint));
}

function assertPlatformEndpoint(endpoint: string): void {
  if (process.platform === "win32") {
    assert.match(
      endpoint,
      /^\\\\\.\\pipe\\unit-test-ide-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
    );
    return;
  }
  assert.equal(process.platform, "linux", "the real-service smoke supports Windows and Linux");
  assert.equal(endpoint.endsWith("/s"), true);
  assert.equal(Buffer.byteLength(endpoint, "utf8") + 1 <= 108, true);
}

async function connectToEndpoint(endpoint: string): Promise<void> {
  await new Promise<void>((resolveConnection, rejectConnection) => {
    const socket = createConnection(endpoint);
    const timer = setTimeout(() => {
      socket.destroy();
      rejectConnection(new Error("endpoint connection timed out"));
    }, 2_000);
    socket.once("connect", () => {
      clearTimeout(timer);
      socket.destroy();
      resolveConnection();
    });
    socket.once("error", (error) => {
      clearTimeout(timer);
      socket.destroy();
      rejectConnection(error);
    });
  });
}

test("trusted real service completes handshake, capabilities, and workspace inspection over the platform endpoint", async (t) => {
  const fixture = await createFixture();
  const observations = createObservations();
  const sensitive = [fixture.root, fixture.workspace, fixture.dataDirectory, serviceBinary, cmakeFixture];
  const manager = new ServiceManager(managerOptions(fixture, observations, () => true));
  t.after(async () => {
    await withRedactedFailure(sensitive, async () => {
      await manager.stop();
      await rm(fixture.root, { recursive: true, force: true });
    });
  });

  await withRedactedFailure(sensitive, async () => {
    await access(serviceBinary);
    await access(cmakeFixture);
    const session = await manager.start();
    collectSessionSecrets(session, observations, sensitive);

    assert.equal(manager.status.state, "running");
    assert.deepEqual(observations.order, ["prepare", "spawn", "connect"]);
    assert.equal(observations.preparedTokenFiles.length, 1);
    assert.equal(observations.spawnArguments.length, 1);
    assert.equal(observations.connectedEndpoints.length, 1);
    assertPlatformEndpoint(session.endpoint);

    const capabilities = await session.client.getCapabilities();
    assert.equal("coverageRun" in capabilities && capabilities.coverageRun, true, "trusted production service must expose coverageRun");
    assert.equal("coverageReport" in capabilities && capabilities.coverageReport, true, "trusted production service must expose coverageReport");

    const snapshot = await session.client.inspectWorkspace();
    assert.equal(snapshot.capabilities.workspaceInspect, true);
    assert.equal(Array.isArray(snapshot.projects), true);
    assert.equal(Array.isArray(snapshot.toolchains), true);
    assert.notEqual(snapshot.workspaceGeneration, "");

    await manager.stop();
    assert.equal(manager.status.state, "stopped");
    assert.equal(observations.children[0]?.exited, true);
  });
});

test("trusted extension adapter completes inspect, discovery, catalog, run, and item results against the real service", async (t) => {
  const fixture = await createFixture();
  const observations = createObservations();
  const sensitive = [fixture.root, fixture.workspace, fixture.dataDirectory, serviceBinary, cmakeFixture];
  const testing = new SmokeTestingController();
  const subscriptions: Array<{ dispose(): void }> = [];
  let manager: ServiceManager | undefined;
  const disposable = () => ({ dispose() {} });
  const host: ExtensionHost = {
    context: { subscriptions },
    extensionPath: resolve(repositoryRoot, "apps/code-oss-extension"),
    dataDirectory: fixture.dataDirectory,
    developmentMode: true,
    workspaceSnapshot: () => ({
      folderCount: 1,
      isTrusted: true,
      workspaceRoot: fixture.workspace
    }),
    configuration: (key, fallback) => {
      if (key === "autoStart") return true as typeof fallback;
      if (key === "serviceExecutable") return serviceBinary as typeof fallback;
      if (key === "serviceStartupTimeoutMs") return 120_000 as typeof fallback;
      return fallback;
    },
    createOutputChannel: () => ({ appendLine() {}, dispose() {} }),
    createStatusBarItem: () => ({ text: "", show() {}, dispose() {} }),
    createTestController: () => testing,
    registerCommand: () => disposable(),
    onDidChangeWorkspaceFolders: () => disposable(),
    onDidGrantWorkspaceTrust: () => disposable(),
    showErrorMessage: () => undefined
  };
  const controller = createExtensionController(host, {
    managerFactory: (options): LifecycleManager => {
      manager = new ServiceManager({
        ...options,
        operations: createRealOperations(observations, options)
      });
      return manager;
    }
  });
  t.after(async () => {
    await withRedactedFailure(sensitive, async () => {
      await controller.deactivate();
      await rm(fixture.root, { recursive: true, force: true });
    });
  });

  await withRedactedFailure(sensitive, async () => {
    await access(serviceBinary);
    await access(cmakeFixture);
    await controller.activate();
    assert.ok(manager?.session);
    collectSessionSecrets(manager.session, observations, sensitive);

    const profile = testing.profiles[0];
    assert.ok(profile, "the activated adapter must register a run profile");
    const items = nestedItems(testing.items);
    const passingItem = items.find((item) => item.label === "passes");
    const failingItem = items.find((item) => item.label === "fails");
    assert.ok(passingItem, "real discovery must publish the passing case");
    assert.ok(failingItem, "real discovery must publish the failing case");

    await profile.handler({});
    await eventually(() => {
      const run = testing.runs[0];
      assert.ok(run);
      assert.equal(run.ends, 1);
      assert.equal(run.passed.includes(passingItem.id), true);
      assert.equal(run.failed.includes(failingItem.id), true);
      assert.equal(run.errored.includes(passingItem.id), false);
      assert.equal(run.errored.includes(failingItem.id), false);
      const inspectIndex = observations.testingCalls.indexOf("workspace/inspect");
      const discoveryIndex = observations.testingCalls.indexOf("discoverTests");
      const catalogIndex = observations.testingCalls.indexOf("catalog");
      const runIndex = observations.testingCalls.indexOf("runTests");
      assert.equal(
        inspectIndex >= 0 &&
        inspectIndex < discoveryIndex &&
        discoveryIndex < catalogIndex &&
        catalogIndex < runIndex,
        true,
        "real Testing API calls must preserve inspect → discovery → catalog → run order"
      );
    });
  });
});

test("untrusted real-service fixture creates no process, token, endpoint, or data directory", async (t) => {
  const fixture = await createFixture();
  const observations = createObservations();
  const sensitive = [fixture.root, fixture.workspace, fixture.dataDirectory, serviceBinary, cmakeFixture];
  const manager = new ServiceManager(managerOptions(fixture, observations, () => false));
  t.after(() => withRedactedFailure(
    sensitive,
    () => rm(fixture.root, { recursive: true, force: true })
  ));

  const assertNoExternalResources = async () => {
    assert.deepEqual(observations.order, []);
    assert.deepEqual(observations.preparedTokenFiles, []);
    assert.deepEqual(observations.spawnArguments, []);
    assert.deepEqual(observations.connectedEndpoints, []);
    assert.deepEqual(observations.children, []);
    await assertPathMissing(fixture.dataDirectory);
  };

  await withRedactedFailure(sensitive, async () => {
    await assertNoExternalResources();
    await assert.rejects(() => manager.start(), /workspace is not trusted/);
    await assertNoExternalResources();
    assert.equal(manager.status.state, "stopped");
    assert.equal(manager.session, undefined);
  });
});

test("host deactivation after trust loss stops the real child and makes its old endpoint unreachable", async (t) => {
  const fixture = await createFixture();
  const observations = createObservations();
  const sensitive = [fixture.root, fixture.workspace, fixture.dataDirectory, serviceBinary, cmakeFixture];
  const state = { trusted: true };
  const subscriptions: Array<{ dispose(): void }> = [];
  let manager: ServiceManager | undefined;

  const disposable = (dispose: () => void = () => undefined) => ({ dispose });
  const host: ExtensionHost = {
    context: { subscriptions },
    extensionPath: resolve(repositoryRoot, "apps/code-oss-extension"),
    dataDirectory: fixture.dataDirectory,
    developmentMode: true,
    workspaceSnapshot: () => ({
      folderCount: 1,
      isTrusted: state.trusted,
      workspaceRoot: fixture.workspace
    }),
    configuration: (key, fallback) => {
      if (key === "autoStart") return true as typeof fallback;
      if (key === "serviceExecutable") return serviceBinary as typeof fallback;
      if (key === "serviceStartupTimeoutMs") return 10_000 as typeof fallback;
      return fallback;
    },
    createOutputChannel: () => ({ appendLine() {}, dispose() {} }),
    createStatusBarItem: () => ({ text: "", show() {}, dispose() {} }),
    registerCommand: () => disposable(),
    onDidChangeWorkspaceFolders: () => disposable(),
    onDidGrantWorkspaceTrust: () => disposable(),
    showErrorMessage: () => undefined
  };

  const controller = createExtensionController(host, {
    managerFactory: (options): LifecycleManager => {
      manager = new ServiceManager({
        ...options,
        operations: createRealOperations(observations, options)
      });
      return manager;
    }
  });
  t.after(async () => {
    await withRedactedFailure(sensitive, async () => {
      await controller.deactivate();
      await rm(fixture.root, { recursive: true, force: true });
    });
  });

  await withRedactedFailure(sensitive, async () => {
    await access(serviceBinary);
    await access(cmakeFixture);
    await controller.activate();
    assert.ok(manager);
    const session = manager.session;
    assert.ok(session);
    collectSessionSecrets(session, observations, sensitive);
    const oldEndpoint = session.endpoint;
    assert.equal(observations.children.length, 1);

    // Code-OSS exposes a grant event only; trust loss tears down/reloads the Extension Host.
    state.trusted = false;
    await controller.deactivate();

    assert.equal(manager.status.state, "stopped");
    assert.equal(manager.session, undefined);
    assert.equal(observations.children[0]?.exited, true);
    await assert.rejects(() => connectToEndpoint(oldEndpoint));
  });
});
