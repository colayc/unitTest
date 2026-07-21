import assert from "node:assert/strict";
import { join, resolve } from "node:path";
import test from "node:test";
import { runProbe } from "./probe.js";

test("probe authenticates, reads capabilities, and shuts the service down", async () => {
  const root = resolve(import.meta.dirname, "../../..");
  const binary = join(root, "build", process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
  const capabilities = await runProbe(binary);
  assert.equal(capabilities.platform, process.platform === "win32" ? "windows" : "linux");
  assert.deepEqual(capabilities.transports, [process.platform === "win32" ? "named-pipe" : "unix-socket"]);
});
