import assert from "node:assert/strict";
import test from "node:test";
import {
  registerCoverageCommands,
  type CoverageCommandController,
  type CoverageCommandHost,
  type CommandContext,
  type CommandStatus,
  type DisposableLike,
  type OutputChannelLike
} from "../src/commands.js";
import type { CoverageControllerState } from "../src/coverage-controller.js";
import type { ExtensionProtocolClient } from "../src/protocol-client.js";

const id = "0123456789abcdef0123456789abcdef";

function state(overrides: Partial<CoverageControllerState> = {}): CoverageControllerState {
  return {
    state: "available",
    coverageRunId: id,
    taskId: id,
    reportId: id,
    ...overrides
  };
}

function setup(options: {
  trust?: "trusted" | "blocked-untrusted";
  state?: CoverageControllerState;
  client?: ExtensionProtocolClient;
} = {}) {
  const handlers = new Map<string, () => void | Promise<void>>();
  const errors: string[] = [];
  const info: string[] = [];
  const output: string[] = [];
  let opened = "";
  const host: CoverageCommandHost = {
    registerCommand(command, handler) {
      handlers.set(command, handler);
      return { dispose() {} } satisfies DisposableLike;
    },
    showErrorMessage(message) { errors.push(message); },
    showInformationMessage(message) { info.push(message); },
    openCoverageHtml(html) { opened = html; }
  };
  const status: CommandStatus = {
    trustState: options.trust ?? "trusted",
    isActive: () => true,
    refreshTrust: () => options.trust ?? "trusted",
    projectService() {}
  };
  const controller: CoverageCommandController = {
    getState: () => options.state ?? state(),
    startCurrent: async () => options.state ?? state(),
    refreshCurrent: async () => options.state ?? state()
  };
  const context: CommandContext = { subscriptions: [] };
  const channel: OutputChannelLike = { appendLine: (value) => output.push(value), dispose() {} };
  registerCoverageCommands(context, controller, () => options.client, status, host, channel);
  return { handlers, errors, info, output, get opened() { return opened; } };
}

test("coverage commands fail closed when trust is lost", async () => {
  const fixture = setup({ trust: "blocked-untrusted" });
  await fixture.handlers.get("unitTestIde.runCoverage")!();
  await fixture.handlers.get("unitTestIde.refreshCoverage")!();
  await fixture.handlers.get("unitTestIde.openCoverageReport")!();
  assert.equal(fixture.errors.length, 3);
  assert.deepEqual(fixture.output, []);
});

test("runCoverage publishes source snapshots without native paths", async () => {
  const fixture = setup({ state: state({
    completeness: { outcome: "available", reasons: [] } as NonNullable<CoverageControllerState["completeness"]>,
    sources: [{ uri: "src/main.cpp", sha256: "a".repeat(64) }]
  }) });
  await fixture.handlers.get("unitTestIde.runCoverage")!();
  assert.equal(fixture.errors.length, 0);
  const published = JSON.parse(fixture.output[0]!);
  assert.deepEqual(published.sources, [{ uri: "src/main.cpp", sha256: "a".repeat(64) }]);
  assert.equal("nativePath" in published, false);
});

test("openCoverageReport reads exactly one HTML artifact through the protocol", async () => {
  const client = {
    listArtifacts: async () => ({ items: [{ artifactId: id, taskId: id, kind: "coverage-html", mimeType: "text/html", sizeBytes: 20, sha256: "a".repeat(64), createdAt: new Date(0), uri: "artifact://" + id }] }),
    readArtifact: async (artifactId: string) => {
      assert.equal(artifactId, id);
      return new TextEncoder().encode("<h1>Coverage</h1>");
    }
  } as unknown as ExtensionProtocolClient;
  const fixture = setup({ client });
  await fixture.handlers.get("unitTestIde.openCoverageReport")!();
  assert.match(fixture.opened, /Content-Security-Policy/);
  assert.equal(fixture.errors.length, 0);
});

test("openCoverageReport rejects duplicate HTML artifacts before reading bytes", async () => {
  let reads = 0;
  const client = {
    listArtifacts: async () => ({ items: [
      { artifactId: id, taskId: id, kind: "coverage-html", mimeType: "text/html", sizeBytes: 1, sha256: "a".repeat(64), createdAt: new Date(0), uri: "artifact://" + id },
      { artifactId: "fedcba9876543210fedcba9876543210", taskId: id, kind: "coverage-html", mimeType: "text/html", sizeBytes: 1, sha256: "b".repeat(64), createdAt: new Date(0), uri: "artifact://other" }
    ] }),
    readArtifact: async () => { reads++; return new Uint8Array(); }
  } as unknown as ExtensionProtocolClient;
  const fixture = setup({ client });
  await fixture.handlers.get("unitTestIde.openCoverageReport")!();
  assert.equal(reads, 0);
  assert.match(fixture.errors[0]!, /exactly one/);
});
