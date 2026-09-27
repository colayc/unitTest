import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { registerTestGenerationCommands, type CommandContext, type DisposableLike, type OutputChannelLike, type TestGenerationCommandHost, type TestGenerationCommandController, type CommandStatus } from "../src/commands.js";

function setup(trust: "trusted" | "blocked-untrusted" = "trusted") {
  const handlers = new Map<string, (...args: unknown[]) => unknown>();
  const errors: string[] = [];
  const info: string[] = [];
  const calls: unknown[] = [];
  const confirmations: string[] = [];
  const host: TestGenerationCommandHost = {
    registerCommand(command, handler) { handlers.set(command, handler); return { dispose() {} } satisfies DisposableLike; },
    showErrorMessage(message) { errors.push(message); },
    showInformationMessage(message) { info.push(message); },
    confirmGeneration: async (message) => { confirmations.push(message); return true; },
    pickGenerationCandidate: async (candidates) => candidates[0],
    pickGenerationSelection: async (scope) => scope === "file" ? { file: "src/main.cpp" } : { symbolId: "opaque-symbol" }
  };
  const controller: TestGenerationCommandController = {
    getState: () => ({ state: "preview", run: { runId: "a".repeat(32) } as any, candidates: { items: [] } as any }),
    async start(selection) { calls.push(["start", selection]); return this.getState(); },
    async refresh() { calls.push(["refresh"]); return this.getState(); },
    async accept(candidateId, confirmCharacterization, displayed) { calls.push(["accept", candidateId, confirmCharacterization, displayed]); return this.getState(); },
    async cancel() { calls.push(["cancel"]); return this.getState(); }
  };
  const status: CommandStatus = { trustState: trust, isActive: () => true, refreshTrust: () => trust, projectService() {} };
  const output: OutputChannelLike = { appendLine() {}, dispose() {} };
  registerTestGenerationCommands({ subscriptions: [] } satisfies CommandContext, controller, status, host, output);
  return { handlers, errors, info, calls, confirmations, controller };
}

test("all generation commands are registered exactly once and map to explicit scopes", async () => {
  const fixture = setup();
  assert.equal(fixture.handlers.size, 8);
  await fixture.handlers.get("unitTestIde.generateTestsForCoverageGap")!({ coverageReportId: "report" });
  assert.deepEqual(fixture.calls[0], ["start", { scope: "coverage-gap", coverageReportId: "report" }]);
});

test("generation commands fail closed on trust loss and acceptance needs a candidate", async () => {
  const blocked = setup("blocked-untrusted");
  await blocked.handlers.get("unitTestIde.generateTests")!();
  assert.equal(blocked.calls.length, 0);
  assert.match(blocked.errors[0]!, /Trust this workspace/);
  const trusted = setup();
  await trusted.handlers.get("unitTestIde.acceptGeneratedTests")!();
  assert.equal(trusted.calls.filter((call) => Array.isArray(call) && call[0] === "accept").length, 0);
  assert.match(trusted.errors[0]!, /candidate/);
});

test("scoped command palette entries prompt for an explicit selection", async () => {
  const fixture = setup();
  await fixture.handlers.get("unitTestIde.generateTestsForSymbol")!();
  assert.deepEqual(fixture.calls[0], ["start", { scope: "symbol", symbolId: "opaque-symbol" }]);
});

test("accept command picks a candidate, binds the displayed preview, and double-confirms characterization", async () => {
  const fixture = setup();
  const candidate = { candidateId: "candidate-1", artifactDigest: "a".repeat(64), codeDigest: "b".repeat(64), kind: "characterization", assertionProvenance: { kind: "observed-output", evidenceDigest: "c".repeat(64) }, baselineCoverage: { functionPercent: 1, linePercent: 2, branchPercent: 3 }, deltaCoverage: { functionPercent: 1, linePercent: 1, branchPercent: 1 }, characterizationConfirmed: false, diagnostics: [], plannedEdits: [] };
  const diff = "--- a/tests/generated.cpp\n+++ b/tests/generated.cpp\n";
  const run = { runId: "a".repeat(32), preview: { candidateSetDigest: "d".repeat(64), diff, diffDigest: createHash("sha256").update(diff).digest("hex"), confirmationDigest: "e".repeat(64) } };
  fixture.controller.refresh = async () => ({ state: "preview", run: run as any, candidates: { items: [candidate] } as any });
  await fixture.handlers.get("unitTestIde.acceptGeneratedTests")!();
  const accept = fixture.calls.find((call) => Array.isArray(call) && call[0] === "accept") as unknown[];
  assert.equal(accept[1], "candidate-1");
  assert.equal((accept[3] as { diffDigest: string }).diffDigest, run.preview.diffDigest);
  assert.equal(fixture.confirmations.length, 2);
});
