import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import test from "node:test";
import { EXTENSION_ACTIVATION_MARKER } from "../dist/src/extension.js";
import { waitForActivation } from "./extension-host-smoke-support.mjs";

test("console activation output cannot replace the durable host marker", async () => {
  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const activation = waitForActivation(
    child,
    join(tmpdir(), `unit-test-ide-no-marker-${process.pid}-${Date.now()}`, "activation.marker"),
    (message, output) => new Error(`${message}; process-output=${output}`),
    { timeoutMs: 1_000, pollIntervalMs: 5 }
  );

  child.stdout.write(`${EXTENSION_ACTIVATION_MARKER}\n`);
  setImmediate(() => child.emit("exit", 0, null));

  await assert.rejects(
    activation,
    /Code-OSS exited before activation marker.*UNIT_TEST_IDE_EXTENSION_ACTIVATED/s
  );
});
