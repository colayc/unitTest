import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { PassThrough } from "node:stream";
import test from "node:test";
import { EXTENSION_ACTIVATION_MARKER } from "../dist/src/extension.js";
import { resolveExtensionUnderTest, waitForActivation } from "./extension-host-smoke-support.mjs";

test("extension under test defaults to the source extension for blank input", () => {
  const repositoryRoot = resolve("repository-root");
  const expected = join(repositoryRoot, "apps", "code-oss-extension");
  for (const configuredPath of [undefined, "", "   "]) {
    assert.equal(resolveExtensionUnderTest(repositoryRoot, configuredPath), expected);
  }
});

test("extension under test preserves an explicit staged path containing spaces", () => {
  const repositoryRoot = resolve("repository-root");
  const stagedExtension = join(tmpdir(), "release stage with spaces", "app", "extensions", "unit-test-ide");
  assert.equal(resolveExtensionUnderTest(repositoryRoot, stagedExtension), resolve(stagedExtension));
});

test("activation polling tolerates a marker file before its contents are complete", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "unit-test-ide-marker-write-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const markerPath = join(directory, "activation.marker");
  await writeFile(markerPath, "", "utf8");

  const child = new EventEmitter();
  child.stdout = new PassThrough();
  child.stderr = new PassThrough();
  const activation = waitForActivation(
    child,
    markerPath,
    (message) => new Error(message),
    { timeoutMs: 1_000, pollIntervalMs: 5 }
  );
  const completeMarker = new Promise((resolveWrite, rejectWrite) => {
    setTimeout(() => {
      writeFile(markerPath, `${EXTENSION_ACTIVATION_MARKER}\n`, "utf8").then(resolveWrite, rejectWrite);
    }, 20);
  });

  try {
    await activation;
  } finally {
    await completeMarker;
  }
});

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
