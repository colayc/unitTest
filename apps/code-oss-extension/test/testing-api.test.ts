import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import type {
  CatalogGetInput,
  EventSubscription,
  ProtocolTestCatalog,
  ProtocolTestRun,
  TestDiscoveryInput,
  TestRunInput,
  WorkspaceSnapshot
} from "@unit-test-ide/test-client";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";
import { TestingApiAdapter, type TestingApiHost } from "../src/testing-api.js";

class FakeProtocolClient implements ExtensionProtocolClient {
  inspectCalls = 0;

  async inspectWorkspace(): Promise<WorkspaceSnapshot> {
    this.inspectCalls++;
    return {
      capabilities: { cmakeBuild: true, targetList: true, workspaceInspect: true },
      diagnostics: [],
      projects: [],
      toolchains: [],
      workspaceGeneration: "trusted-workspace",
      workspaceUri: "file:///workspace"
    };
  }

  async discoverTests(_input: TestDiscoveryInput): Promise<never> {
    throw new Error("not used by the host-contract test");
  }

  async getTestCatalog(_input: CatalogGetInput): Promise<ProtocolTestCatalog> {
    throw new Error("not used by the host-contract test");
  }

  async runTests(_input: TestRunInput): Promise<never> {
    throw new Error("not used by the host-contract test");
  }

  async getTestRun(_runId: string): Promise<ProtocolTestRun> {
    throw new Error("not used by the host-contract test");
  }

  async subscribeEvents(_afterSequence: number): Promise<EventSubscription> {
    throw new Error("not used by the host-contract test");
  }

  close(): void {}
}

test("TestingApiAdapter creates a fakeable host contract without a runtime vscode import", async () => {
  const errors: string[] = [];
  const client = new FakeProtocolClient();
  let controllerDisposed = 0;
  const host: TestingApiHost = {
    workspaceSnapshot: () => ({ folderCount: 1, isTrusted: true, workspaceRoot: "C:\\workspace" }),
    createTestController(id, label) {
      assert.equal(id, "unitTestIde.tests");
      assert.equal(label, "Unit Test IDE");
      return { dispose: () => { controllerDisposed++; } };
    },
    showErrorMessage(message) { errors.push(message); }
  };

  const adapter = new TestingApiAdapter(host, () => client, () => "trusted");

  assert.equal(adapter.readWorkspace().isTrusted, true);
  assert.equal(adapter.currentTrust(), "trusted");
  assert.equal(adapter.currentClient(), client);
  await adapter.presentError(new Error("service failed at C:\\secret-session\\token"));
  assert.equal(errors.length, 1);
  assert.doesNotMatch(errors[0] ?? "", /secret-session|token/);
  adapter.close();
  assert.equal(controllerDisposed, 1);

  const source = await readFile(new URL("../src/testing-api.js", import.meta.url), "utf8");
  assert.doesNotMatch(source, /from\s+["']vscode["']|import\(["']vscode["']\)/);
});
