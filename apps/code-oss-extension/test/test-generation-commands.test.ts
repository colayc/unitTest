import assert from "node:assert/strict";
import test from "node:test";
import { registerTestGenerationCommands, type CommandContext, type DisposableLike, type OutputChannelLike, type TestGenerationCommandHost, type TestGenerationCommandController, type CommandStatus } from "../src/commands.js";

function setup(trust: "trusted" | "blocked-untrusted" = "trusted") {
  const handlers = new Map<string, (...args: unknown[]) => unknown>();
  const errors: string[] = [];
  const info: string[] = [];
  const calls: unknown[] = [];
  const host: TestGenerationCommandHost = {
    registerCommand(command, handler) { handlers.set(command, handler); return { dispose() {} } satisfies DisposableLike; },
    showErrorMessage(message) { errors.push(message); },
    showInformationMessage(message) { info.push(message); },
    confirmGeneration: async () => true
  };
  const controller: TestGenerationCommandController = {
    getState: () => ({ state: "preview", run: { runId: "a".repeat(32) } as any, candidates: { items: [] } as any }),
    async start(selection) { calls.push(["start", selection]); return this.getState(); },
    async refresh() { calls.push(["refresh"]); return this.getState(); },
    async accept(candidateId, confirmCharacterization) { calls.push(["accept", candidateId, confirmCharacterization]); return this.getState(); },
    async cancel() { calls.push(["cancel"]); return this.getState(); }
  };
  const status: CommandStatus = { trustState: trust, isActive: () => true, refreshTrust: () => trust, projectService() {} };
  const output: OutputChannelLike = { appendLine() {}, dispose() {} };
  registerTestGenerationCommands({ subscriptions: [] } satisfies CommandContext, controller, status, host, output);
  return { handlers, errors, info, calls };
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
  assert.equal(trusted.calls.length, 0);
  assert.match(trusted.errors[0]!, /candidate/);
});
