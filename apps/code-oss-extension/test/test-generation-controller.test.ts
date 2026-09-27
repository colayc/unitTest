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
  const states: TestGenerationControllerState[] = [];
  const controller = new TestGenerationController({
    readContext: () => ({ trust: overrides.trust ?? "trusted", client, projectId: "core", workspaceGeneration: "c".repeat(64) }),
    onStateChanged: (state) => states.push(state)
  });
  return { controller, client, states, accepted, setSequence(value: number) { sequence = value; } };
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
