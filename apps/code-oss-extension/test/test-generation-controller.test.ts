import assert from "node:assert/strict";
import test from "node:test";
import { TestGenerationController } from "../src/test-generation-controller.js";
import type { TestGenerationControllerState } from "../src/test-generation-controller.js";

const id = "a".repeat(32);
const runBase = { runId: id, taskId: "b".repeat(32), projectId: "core", workspaceGeneration: "c".repeat(64), state: "queued", createdAt: new Date(0), lastSequence: 0 };
const candidate = { candidateId: "candidate-1", artifactDigest: "d".repeat(64), codeDigest: "e".repeat(64), kind: "verified", assertionProvenance: { kind: "independent-oracle", evidenceDigest: "f".repeat(64) }, baselineCoverage: { functionPercent: 10, linePercent: 20, branchPercent: 30 }, deltaCoverage: { functionPercent: 1, linePercent: 2, branchPercent: 3 }, characterizationConfirmed: false, diagnostics: [], plannedEdits: [] };

function setup(overrides: { trust?: "trusted" | "blocked-untrusted"; capable?: boolean } = {}) {
  let run: any = { ...runBase };
  let sequence = 0;
  const events = [{ sequence: 1, state: "queued", occurredAt: new Date(0) }];
  const accepted: any[] = [];
  let currentTrust = overrides.trust ?? "trusted";
  const client: any = {
    async getCapabilities() { return { testGeneration: overrides.capable !== false, maxTestGenerationCandidates: 8 }; },
    async listTestGenerationTargets() { return { items: [] }; },
    async startTestGeneration() { run = { ...run, state: "awaiting_confirmation", lastSequence: 1, preview: { candidateSetDigest: "1".repeat(64), diff: "--- a/tests/generated.cpp\n+++ b/tests/generated.cpp\n", diffDigest: "2".repeat(64), confirmationDigest: "3".repeat(64) } }; return run; },
    async getTestGenerationRun() { return run; },
    async cancelTestGeneration() { run = { ...run, state: "cancelled" }; return run; },
    async replayTestGenerationEvents() { return { items: events, nextAfterSequence: sequence }; },
    async listTestGenerationCandidates() { return { items: [candidate] }; },
    async acceptTestGeneration(input: any) { accepted.push(input); run = { ...run, state: "accepted" }; return run; }
  };
  let currentClient = client;
  const states: TestGenerationControllerState[] = [];
  const controller = new TestGenerationController({
    readContext: () => ({ trust: currentTrust, client: currentClient, projectId: "core", workspaceGeneration: "c".repeat(64) }),
    onStateChanged: (state) => states.push(state)
  });
  return { controller, client, states, accepted, setSequence(value: number) { sequence = value; }, setContext(value: { trust?: "trusted" | "blocked-untrusted"; client?: any }) { currentTrust = value.trust ?? currentTrust; currentClient = value.client ?? currentClient; } };
}

test("controller gates generation on trust and advertised capability", async () => {
  await assert.rejects(() => setup({ trust: "blocked-untrusted" }).controller.start({ scope: "workspace" as any }), /Trust this workspace/);
  await assert.rejects(() => setup({ capable: false }).controller.start({ scope: "workspace" as any }), /capability is unavailable/);
});

test("controller starts all scopes, replays deduplicated events, and never accepts cached preview", async () => {
  const fixture = setup();
  await fixture.controller.start({ scope: "workspace" as any });
  assert.equal(fixture.controller.getState().state, "preview");
  await fixture.controller.refresh();
  assert.equal(fixture.controller.getState().events?.items.length, 1);
  await fixture.controller.accept("candidate-1");
  assert.equal(fixture.accepted[0].confirmationDigest, "3".repeat(64));
  assert.equal(fixture.controller.getState().state, "accepted");
});

test("controller requires explicit characterization consent and cancellation is service-owned", async () => {
  const fixture = setup();
  await fixture.controller.start({ scope: "workspace" as any });
  fixture.client.listTestGenerationCandidates = async () => ({ items: [{ ...candidate, kind: "characterization" }] });
  await assert.rejects(() => fixture.controller.accept("candidate-1"), /explicit confirmation/);
  await fixture.controller.cancel();
  assert.equal(fixture.controller.getState().state, "cancelled");
});

test("controller rejects a restarted run from another workspace", async () => {
  const fixture = setup();
  fixture.client.getTestGenerationRun = async () => ({ ...runBase, workspaceGeneration: "9".repeat(64) });
  await assert.rejects(() => fixture.controller.restore(id), /stale/);
});

test("controller rejects a preview that changes after the user confirms", async () => {
  const fixture = setup();
  await fixture.controller.start({ scope: "workspace" as any });
  const shown = fixture.controller.getState();
  const preview = shown.run!.preview!;
  const selected = shown.candidates!.items[0]!;
  const binding = {
    runId: shown.run!.runId,
    candidateId: selected.candidateId,
    candidateSetDigest: preview.candidateSetDigest,
    candidateArtifactDigest: selected.artifactDigest,
    candidateCodeDigest: selected.codeDigest,
    diffDigest: preview.diffDigest,
    confirmationDigest: preview.confirmationDigest
  };
  fixture.client.getTestGenerationRun = async () => ({ ...runBase, state: "awaiting_confirmation", preview: { ...preview, confirmationDigest: "9".repeat(64) } });
  await assert.rejects(() => fixture.controller.accept(selected.candidateId, false, binding), /preview is stale/);
  assert.equal(fixture.accepted.length, 0);
  assert.equal(fixture.controller.getState().state, "preview");
});

test("controller rejects trust or session changes while an RPC is pending", async () => {
  let release: (() => void) | undefined;
  const fixture = setup();
  const original = fixture.client.getTestGenerationRun;
  fixture.client.getTestGenerationRun = async () => {
    await new Promise<void>((resolve) => { release = resolve; });
    return original();
  };
  const operation = fixture.controller.restore(id);
  await new Promise<void>((resolve) => setImmediate(resolve));
  fixture.setContext({ trust: "blocked-untrusted" });
  release!();
  await assert.rejects(operation, /workspace or service session changed|stale/);
});

test("late accept responses cannot republish the old preview after a workspace switch", async () => {
  const fixture = setup();
  await fixture.controller.start({ scope: "workspace" as any });
  const shown = fixture.controller.getState();
  const preview = shown.run!.preview!;
  const selected = shown.candidates!.items[0]!;
  const binding = { runId: shown.run!.runId, candidateId: selected.candidateId, candidateSetDigest: preview.candidateSetDigest, candidateArtifactDigest: selected.artifactDigest, candidateCodeDigest: selected.codeDigest, diffDigest: preview.diffDigest, confirmationDigest: preview.confirmationDigest };
  let release: (() => void) | undefined;
  fixture.client.acceptTestGeneration = async () => {
    await new Promise<void>((resolve) => { release = resolve; });
    return { ...runBase, state: "accepted" };
  };
  const operation = fixture.controller.accept(selected.candidateId, false, binding);
  await new Promise<void>((resolve) => setImmediate(resolve));
  fixture.setContext({ trust: "blocked-untrusted" });
  release!();
  await assert.rejects(operation, /workspace or service session changed|stale/);
  assert.equal(fixture.controller.getState().state, "unavailable");
  assert.equal(fixture.controller.getState().run, undefined);
});

test("v1.6 managed generation starts only with authoritative function, file, or gap IDs", async () => {
  const fixture = setup();
  const requests: any[] = [];
  fixture.client.getCapabilities = async () => ({ testGeneration: true, managedTests: true });
  fixture.client.startTestGeneration = async (request: any) => { requests.push(request); return { ...runBase, state: "queued" }; };
  const functionId = "1".repeat(32);
  const fileId = "2".repeat(32);
  const coverageGapId = "3".repeat(32);
  await fixture.controller.startManaged({ scope: "symbol", functionId });
  await fixture.controller.startManaged({ scope: "file", fileId });
  await fixture.controller.startManaged({ scope: "coverage-gap", coverageGapId, coverageReportId: "4".repeat(32) });
  assert.deepEqual(requests.map(({ scope, functionId, fileId, coverageGapId, coverageReportId }) => ({ scope, functionId, fileId, coverageGapId, coverageReportId })), [
    { scope: "symbol", functionId, fileId: undefined, coverageGapId: undefined, coverageReportId: undefined },
    { scope: "file", functionId: undefined, fileId, coverageGapId: undefined, coverageReportId: undefined },
    { scope: "coverage-gap", functionId: undefined, fileId: undefined, coverageGapId, coverageReportId: "4".repeat(32) }
  ]);
  await assert.rejects(() => fixture.controller.startManaged({ scope: "symbol", functionId: "not-an-id" }), /invalid/i);
  assert.equal(requests.length, 3);
});

test("managed generation is unavailable on v1.5 and checks trust after an in-flight start", async () => {
  const old = setup();
  await assert.rejects(() => old.controller.startManaged({ scope: "file", fileId: "1".repeat(32) }), /v1.6|managed/i);
  const fixture = setup();
  fixture.client.getCapabilities = async () => ({ testGeneration: true, managedTests: true });
  let release: (() => void) | undefined;
  fixture.client.startTestGeneration = async () => { await new Promise<void>((resolve) => { release = resolve; }); return { ...runBase, state: "queued" }; };
  const operation = fixture.controller.startManaged({ scope: "file", fileId: "1".repeat(32) });
  await new Promise<void>((resolve) => setImmediate(resolve));
  fixture.setContext({ trust: "blocked-untrusted" });
  release!();
  await assert.rejects(operation, /workspace|stale|session/i);
});
