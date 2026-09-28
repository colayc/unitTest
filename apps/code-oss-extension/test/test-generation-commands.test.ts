import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import { registerManagedTestCommands, registerTestGenerationCommands, type CommandContext, type DisposableLike, type OutputChannelLike, type TestGenerationCommandHost, type TestGenerationCommandController, type CommandStatus } from "../src/commands.js";
import { ManagedTestReviewController } from "../src/managed-test-review.js";

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
    pickGenerationSelection: async (scope) => scope === "file" ? { file: "src/main.cpp" } : { symbolId: "opaque-symbol" },
    workspaceRoot: () => "C:\\workspace"
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
  return { handlers, errors, info, calls, confirmations, controller, host };
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

test("URI-shaped context arguments become safe relative files and symbols use an explicit picker", async () => {
  const fixture = setup();
  await fixture.handlers.get("unitTestIde.generateTestsForFile")!({ fsPath: "C:\\workspace\\src\\main.cpp", path: "/workspace/src/main.cpp" });
  assert.deepEqual(fixture.calls[0], ["start", { scope: "file", file: "src/main.cpp" }]);
  await fixture.handlers.get("unitTestIde.generateTestsForSymbol")!({ fsPath: "C:\\workspace\\src\\main.cpp", path: "/workspace/src/main.cpp" });
  assert.deepEqual(fixture.calls[1], ["start", { scope: "symbol", symbolId: "opaque-symbol" }]);
});

test("invalid URI contexts are rejected without falling back to another editor target", async () => {
  const fixture = setup();
  await fixture.handlers.get("unitTestIde.generateTestsForFile")!({ fsPath: "C:\\other-workspace\\wrong.cpp" });
  assert.equal(fixture.calls.length, 0);
  assert.match(fixture.errors[0]!, /invalid|outside the workspace/i);
});

test("unsupported opaque scopes fail closed when no service-backed picker exists", async () => {
  const fixture = setup();
  fixture.host.pickGenerationSelection = async () => undefined;
  await fixture.handlers.get("unitTestIde.generateTestsForTarget")!();
  assert.equal(fixture.calls.length, 0);
  assert.match(fixture.errors[0]!, /picker is available/i);
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

test("managed commands use only authoritative IDs and leave the v1.5 command set unchanged", async () => {
  const fixture = setup();
  const output: string[] = [];
  const managedCalls: unknown[] = [];
  const review: any = {
    getState: () => ({ review: undefined, choices: {}, canApply: false, applying: false }),
    async available() { return true; },
    async list(status?: string) { managedCalls.push(["list", status]); return { items: [{ caseId: `utc_${"a".repeat(32)}`, status: "orphaned", fileId: "b".repeat(32), functionId: "c".repeat(32) }] }; },
    async load(reviewId: string) { managedCalls.push(["load", reviewId]); return { review: { reviewId, reviewDigest: "d".repeat(64), cases: [{ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", diff: "+TEST(foo)\n", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64) }] }, choices: {}, canApply: false, applying: false }; },
    choose(caseId: string, choice: string) { managedCalls.push(["choose", caseId, choice]); },
    async apply(digest: string) { managedCalls.push(["apply", digest]); },
    reject() { managedCalls.push(["reject"]); }
  };
  const managedHost: any = { ...fixture.host, pickManagedConflictChoice: async () => "keep-current", openManagedCaseDiff: async (_title: string, diff: string) => output.push(diff) };
  const generation = { startManaged: async (selection: unknown) => { managedCalls.push(["generate", selection]); } };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, generation, review, status, managedHost, { appendLine: (line: string) => output.push(line), dispose() {} });
  await fixture.handlers.get("unitTestIde.generateManagedTestsForFunction")!({ functionId: "1".repeat(32) });
  await fixture.handlers.get("unitTestIde.generateManagedTestsForFile")!({ fileId: "2".repeat(32) });
  await fixture.handlers.get("unitTestIde.generateManagedTestsForCoverageGap")!({ coverageGapId: "3".repeat(32), coverageReportId: "4".repeat(32) });
  assert.deepEqual(managedCalls.slice(0, 3), [
    ["generate", { scope: "symbol", functionId: "1".repeat(32) }],
    ["generate", { scope: "file", fileId: "2".repeat(32) }],
    ["generate", { scope: "coverage-gap", coverageGapId: "3".repeat(32), coverageReportId: "4".repeat(32) }]
  ]);
  await fixture.handlers.get("unitTestIde.generateManagedTestsForFunction")!({ functionId: "../wrong" });
  assert.equal(managedCalls.filter((call) => Array.isArray(call) && call[0] === "generate").length, 3);
  assert.equal(output.some((line) => line.includes("../wrong")), false);
});

test("managed command delegates explicit Apply only when its review facade reports readiness", async () => {
  const fixture = setup();
  const calls: unknown[] = [];
  const reviewId = "c".repeat(32);
  const caseId = `utc_${"a".repeat(32)}`;
  let state: any = { review: undefined, choices: {}, canApply: false, applying: false };
  const review: any = {
    getState: () => state, available: async () => true, list: async () => ({ items: [] }),
    load: async () => { state = { review: { reviewId, reviewDigest: "d".repeat(64), cases: [{ caseId, status: "conflicted", diff: "+TEST(foo)\n", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64) }] }, choices: {}, canApply: false, applying: false, previewAvailable: true }; return state; },
    choose: (_id: string, choice: string) => { calls.push(choice); state = { ...state, canApply: true }; },
    markDisplayed: () => state,
    apply: async (digest: string) => { calls.push(["apply", digest]); return { state: "confirmed", result: { reviewId, reviewDigest: digest, applied: true } }; }, reject: () => { calls.push("reject"); }
  };
  const host: any = { ...fixture.host, pickManagedConflictChoice: async () => "convert-to-manual", openManagedCaseDiff: async (_title: string, diff: string) => { calls.push(["diff", diff]); } };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async () => undefined }, review, status, host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.reviewManagedTests")!({ reviewId });
  assert.deepEqual(calls, [["diff", "+TEST(foo)\n"], "convert-to-manual"]);
  await fixture.handlers.get("unitTestIde.applyManagedReview")!();
  assert.deepEqual(calls.at(-1), ["apply", "d".repeat(64)]);
});

test("coverage tree context supplies authoritative file and function IDs to managed generation", async () => {
  const fixture = setup();
  const starts: unknown[] = [];
  const review: any = { available: async () => true };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async (value: unknown) => { starts.push(value); } }, review, status, fixture.host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.generateManagedTestsForFile")!({ kind: "file", id: "1".repeat(32) });
  await fixture.handlers.get("unitTestIde.generateManagedTestsForFunction")!({ kind: "function", id: "2".repeat(32) });
  assert.deepEqual(starts, [{ scope: "file", fileId: "1".repeat(32) }, { scope: "symbol", functionId: "2".repeat(32) }]);
});

test("a digest-only review stays visible but never arms Apply", async () => {
  const fixture = setup();
  let rejected = 0;
  const review: any = {
    available: async () => true,
    load: async () => ({ review: { reviewId: "c".repeat(32), reviewDigest: "d".repeat(64), cases: [{ caseId: `utc_${"a".repeat(32)}`, status: "current", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64) }] }, canApply: true }),
    markDisplayed: () => undefined,
    reject: () => { rejected++; },
    getState: () => ({ review: undefined, canApply: false })
  };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async () => undefined }, review, status, fixture.host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.reviewManagedTests")!({ reviewId: "c".repeat(32) });
  assert.equal(rejected, 0);
  assert.equal(fixture.errors.length, 0);
  assert.ok(fixture.info.some((message) => /preview unavailable/i.test(message)));
});

test("digest-only managed review says preview unavailable and never opens an exact diff", async () => {
  const fixture = setup();
  const opened: string[] = [];
  const review: any = {
    available: async () => true,
    load: async () => ({ review: { reviewId: "c".repeat(32), reviewDigest: "d".repeat(64), cases: [{ caseId: `utc_${"a".repeat(32)}`, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64) }] }, canApply: false }),
    markDisplayed: () => undefined,
    choose: () => undefined,
    reject: () => undefined,
    getState: () => ({ canApply: false, previewAvailable: false })
  };
  const host: any = { ...fixture.host, openManagedCaseDiff: async (_title: string, diff: string) => { opened.push(diff); } };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async () => undefined }, review, status, host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.reviewManagedTests")!({ reviewId: "c".repeat(32) });
  assert.deepEqual(opened, []);
  assert.ok(fixture.info.some((message) => /preview unavailable/i.test(message)));
});

test("review command reports an in-flight Apply without throwing from local Reject", async () => {
  const fixture = setup();
  const review: any = {
    available: async () => true,
    load: async () => { throw new Error("already applying"); },
    reject: () => { throw new Error("already applying"); }
  };
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async () => undefined }, review, status, fixture.host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.reviewManagedTests")!({ reviewId: "c".repeat(32) });
  assert.match(fixture.errors.at(-1)!, /already applying/i);
});

test("managed Apply command cannot dispatch a self-asserted preview", async () => {
  const fixture = setup();
  const reviewId = "c".repeat(32);
  const reviewDigest = "d".repeat(64);
  const caseId = `utc_${"e".repeat(32)}`;
  const generation = "a".repeat(64);
  const diff = "+FABRICATED()\n";
  const diffDigest = createHash("sha256").update(diff).digest("hex");
  const previewArtifactDigest = createHash("sha256").update(`managed-review-preview-v1\n${reviewDigest}\nc:${caseId}:${diffDigest}\n`).digest("hex");
  let dispatched = 0;
  const client: any = {
    getCapabilities: async () => ({ managedTests: true }),
    getManagedReview: async () => ({ reviewId, reviewDigest, workspaceGeneration: generation, coverageReportId: "b".repeat(32), previewArtifactDigest, cases: [{ caseId, status: "conflicted", acceptedDigest: "1".repeat(64), currentDigest: "2".repeat(64), generatedDigest: "3".repeat(64), diff }] }),
    listManagedTests: async () => ({ workspaceGeneration: generation, coverageReportId: "b".repeat(32), items: [] }),
    applyManagedReview: async () => { dispatched++; return { reviewId, reviewDigest, applied: true }; }
  };
  const review = new ManagedTestReviewController({ readContext: () => ({ trust: "trusted", client, projectId: "core", workspaceGeneration: generation, coverageReportId: "b".repeat(32) }) });
  await review.load(reviewId);
  review.markDisplayed(reviewDigest);
  review.choose(caseId, "use-generated");
  const status: CommandStatus = { trustState: "trusted", isActive: () => true, refreshTrust: () => "trusted", projectService() {} };
  registerManagedTestCommands({ subscriptions: [] }, { startManaged: async () => undefined }, review, status, fixture.host, { appendLine() {}, dispose() {} });
  await fixture.handlers.get("unitTestIde.applyManagedReview")!();
  assert.equal(dispatched, 0);
  assert.match(fixture.errors.at(-1)!, /review every managed-test change/i);
});
