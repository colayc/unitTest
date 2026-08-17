import assert from "node:assert/strict";
import { stat } from "node:fs/promises";
import test from "node:test";
import {
  createEndpointResource,
  createToken,
  redactServiceError
} from "../src/service-resources.js";

test("service resource uses a unique Windows named pipe without a Unix directory", async () => {
  const resource = await createEndpointResource("win32");

  assert.match(resource.path, /^\\\\\.\\pipe\\unit-test-ide-[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i);
  assert.equal(resource.directory, undefined);
});

test("service resource uses an owner-only Linux directory and fits sockaddr_un", async (t) => {
  const resource = await createEndpointResource("linux");
  assert.ok(resource.directory);
  t.after(async () => {
    const { rm } = await import("node:fs/promises");
    await rm(resource.directory!, { recursive: true, force: true });
  });

  const directory = await stat(resource.directory);
  if (process.platform !== "win32") assert.equal(directory.mode & 0o777, 0o700);
  assert.ok(Buffer.byteLength(resource.path, "utf8") + 1 <= 108);
  assert.ok(resource.path.startsWith(resource.directory));
});

test("service resource creates independent 32-byte base64url tokens", () => {
  const first = createToken();
  const second = createToken();

  assert.match(first, /^[A-Za-z0-9_-]{43}$/);
  assert.match(second, /^[A-Za-z0-9_-]{43}$/);
  assert.notEqual(first, second);

  const diagnostic = redactServiceError(new Error(`authentication rejected ${first}`), [first]);
  assert.doesNotMatch(diagnostic.message, new RegExp(first));
});

test("service resource timeout diagnostics retain only the operation and duration", () => {
  const token = "secret-service-token";
  const workspaceRoot = "C:\\private\\workspace";
  const binary = "C:\\private\\bin\\unit-test-service.exe";
  const error = redactServiceError(
    new Error(`service startup readiness timed out after 25ms: ${token} ${workspaceRoot} ${binary}`),
    [token, workspaceRoot, binary]
  );

  assert.match(error.message, /service startup readiness/);
  assert.match(error.message, /25ms/);
  assert.doesNotMatch(error.message, /secret-service-token|private|unit-test-service\.exe/);
  assert.equal(error.stack, `Error: ${error.message}`);
});
