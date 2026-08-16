import assert from "node:assert/strict";
import test from "node:test";
import { canStartService, evaluateWorkspace, TrustGate } from "../src/trust-gate.js";

test("no workspace and multi-root workspaces are not executable", () => {
  assert.equal(evaluateWorkspace({ folderCount: 0, isTrusted: false }), "no-workspace");
  assert.equal(evaluateWorkspace({ folderCount: 2, isTrusted: true }), "blocked-multi-root");
  assert.equal(canStartService("blocked-multi-root"), false);
});

test("untrusted single-root workspace never becomes executable", () => {
  assert.equal(evaluateWorkspace({ folderCount: 1, isTrusted: false }), "blocked-untrusted");
  assert.equal(canStartService("blocked-untrusted"), false);
});

test("trusted single-root workspace can start the service", () => {
  assert.equal(evaluateWorkspace({ folderCount: 1, isTrusted: true }), "trusted");
  assert.equal(canStartService("trusted"), true);
});

test("trust transition emits trusted only for one trusted folder", () => {
  const gate = new TrustGate();
  assert.equal(gate.update({ folderCount: 1, isTrusted: false }), "blocked-untrusted");
  assert.equal(gate.update({ folderCount: 1, isTrusted: true }), "trusted");
  assert.equal(gate.update({ folderCount: 0, isTrusted: false }), "no-workspace");
});

test("trust gate transitions from trusted when trust is revoked", () => {
  const gate = new TrustGate();
  gate.update({ folderCount: 1, isTrusted: true });
  assert.equal(gate.update({ folderCount: 1, isTrusted: false }), "blocked-untrusted");
});

test("trust gate transitions from trusted when workspace becomes multi-root", () => {
  const gate = new TrustGate();
  gate.update({ folderCount: 1, isTrusted: true });
  assert.equal(gate.update({ folderCount: 2, isTrusted: true }), "blocked-multi-root");
});

test("trust gate cannot produce the service-only failed state", () => {
  for (const snapshot of [
    { folderCount: 0, isTrusted: false },
    { folderCount: 1, isTrusted: false },
    { folderCount: 1, isTrusted: true },
    { folderCount: 2, isTrusted: true }
  ]) {
    assert.notEqual(evaluateWorkspace(snapshot), "failed");
  }
});

test("trust gate listeners run once per state transition", () => {
  const gate = new TrustGate();
  const transitions: string[] = [];
  gate.onTransition((state) => transitions.push(state));

  gate.update({ folderCount: 1, isTrusted: false });
  gate.update({ folderCount: 1, isTrusted: false });
  gate.update({ folderCount: 1, isTrusted: true });
  gate.update({ folderCount: 1, isTrusted: true });

  assert.deepEqual(transitions, ["blocked-untrusted", "trusted"]);
});
