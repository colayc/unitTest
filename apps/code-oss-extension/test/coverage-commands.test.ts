import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
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
import type { CoverageSourceSnapshotV14 } from "@unit-test-ide/test-client";
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
  workspaceRoot?: string;
  pickSource?: CoverageSourceSnapshotV14;
} = {}) {
  const handlers = new Map<string, (...args: unknown[]) => void | Promise<void>>();
  const errors: string[] = [];
  const info: string[] = [];
  const output: string[] = [];
  let opened = "";
  let openedSource = "";
  const host: CoverageCommandHost = {
    registerCommand(command, handler) {
      handlers.set(command, handler);
      return { dispose() {} } satisfies DisposableLike;
    },
    showErrorMessage(message) { errors.push(message); },
    showInformationMessage(message) { info.push(message); },
    openCoverageHtml(html) { opened = html; },
    openCoverageSource(path) { openedSource = path; },
    pickCoverageSource: options.pickSource ? async () => options.pickSource : undefined
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
  registerCoverageCommands(context, controller, () => options.client, status, host, channel, () => options.workspaceRoot);
  return { handlers, errors, info, output, get opened() { return opened; }, get openedSource() { return openedSource; } };
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

test("openCoverageSource verifies the selected snapshot before handing it to the host", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-coverage-command-"));
  try {
    const relative = "src/main.cpp";
    const file = join(root, "src", "main.cpp");
    await mkdir(join(root, "src"));
    await writeFile(file, "int main() {}\n", "utf8");
    const sha256 = createHash("sha256").update("int main() {}\n").digest("hex");
    const fixture = setup({ workspaceRoot: root });
    await fixture.handlers.get("unitTestIde.openCoverageSource")!({ uri: relative, sha256 });
    assert.equal(fixture.errors.length, 0);
    assert.equal(fixture.openedSource, file);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("openCoverageSource rejects malformed or unavailable snapshots before opening", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-coverage-command-"));
  try {
    const fixture = setup({ workspaceRoot: root });
    await fixture.handlers.get("unitTestIde.openCoverageSource")!({ uri: "../secret.cpp", sha256: "a".repeat(64) });
    assert.equal(fixture.openedSource, "");
    assert.match(fixture.errors[0]!, /coverage source/i);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("openCoverageSource lets the host pick from the current report when no argument is supplied", async () => {
  const root = await mkdtemp(join(tmpdir(), "unit-test-coverage-command-"));
  try {
    const relative = "src/main.cpp";
    const file = join(root, "src", "main.cpp");
    await mkdir(join(root, "src"));
    await writeFile(file, "int main() {}\n", "utf8");
    const source = { uri: relative, sha256: createHash("sha256").update("int main() {}\n").digest("hex") };
    const fixture = setup({ workspaceRoot: root, state: state({ sources: [source] }), pickSource: source });
    await fixture.handlers.get("unitTestIde.openCoverageSource")!();
    assert.equal(fixture.errors.length, 0);
    assert.equal(fixture.openedSource, file);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
