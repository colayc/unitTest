import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const load = async (path) => JSON.parse(await readFile(new URL(path, import.meta.url), "utf8"));

test("protocol v1 accepts authenticated handshake shape and rejects a missing token", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validate = ajv.compile(await load("../schema/v1/message.schema.json"));
  assert.equal(validate(await load("../fixtures/v1/handshake.valid.json")), true);
  assert.equal(validate(await load("../fixtures/v1/handshake-missing-token.invalid.json")), false);
  assert.match(JSON.stringify(validate.errors), /token/);
});

test("protocol 1.1 accepts controlled tasks and rejects shell input", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  for (const name of ["task", "artifact", "event"]) {
    ajv.addSchema(await load(`../schema/v1.1/${name}.schema.json`));
  }
  const validate = ajv.compile(await load("../schema/v1.1/message.schema.json"));
  assert.equal(validate(await load("../fixtures/v1.1/handshake.valid.json")), true);
  assert.equal(validate(await load("../fixtures/v1.1/task-start.valid.json")), true);
  assert.equal(validate(await load("../fixtures/v1.1/task-start-shell.invalid.json")), false);
  assert.equal(validate(await load("../fixtures/v1.1/event-task-started.valid.json")), true);
});

test("protocol 1.1 tasks/start rejects every execution-plan injection field", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  for (const name of ["task", "artifact", "event"]) {
    ajv.addSchema(await load(`../schema/v1.1/${name}.schema.json`));
  }
  const validate = ajv.compile(await load("../schema/v1.1/message.schema.json"));
  const request = {
    protocolVersion: "1.1",
    kind: "request",
    messageId: "0123456789abcdef0123456789abcdef",
    method: "tasks/start",
    sentAt: "2026-07-26T00:00:00Z",
    payload: {
      idempotencyKey: "fedcba9876543210fedcba9876543210",
      scenario: "success",
      timeoutMs: 1000
    }
  };
  assert.equal(validate(request), true, JSON.stringify(validate.errors));

  for (const [field, value] of Object.entries({
    kind: "cmakeBuild",
    steps: [{
      id: "configure",
      kind: "configure",
      executable: "cmake",
      args: ["-S", ".", "-B", "build"],
      workingDirectory: ".",
      env: ["BUILD_MODE=debug"]
    }],
    executable: "cmake",
    args: ["--build", "."],
    env: ["BUILD_MODE=debug"]
  })) {
    assert.equal(
      validate({ ...request, payload: { ...request.payload, [field]: value } }),
      false,
      `tasks/start accepted forbidden field ${field}`
    );
    assert.ok(
      validate.errors?.some((error) =>
        error.instancePath === "/payload"
        && error.keyword === "additionalProperties"
        && error.params.additionalProperty === field
      ),
      `tasks/start rejected ${field} for the wrong reason: ${JSON.stringify(validate.errors)}`
    );
  }
});

test("protocol 1.1 simulation snapshots and event enum remain closed to Step fields", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
  addFormats(ajv);
  const validateTask = ajv.compile(await load("../schema/v1.1/task.schema.json"));
  const validateEvent = ajv.compile(await load("../schema/v1.1/event.schema.json"));
  const snapshot = {
    taskId: "11111111111111111111111111111111",
    kind: "simulation",
    scenario: "success",
    status: "finished",
    outcome: "succeeded",
    createdAt: "2026-07-26T00:00:00Z",
    startedAt: "2026-07-26T00:00:00Z",
    finishedAt: "2026-07-26T00:00:01Z",
    timeoutMs: 1000,
    lastSequence: 6
  };
  assert.equal(validateTask(snapshot), true, JSON.stringify(validateTask.errors));
  assert.equal(validateTask({ ...snapshot, activeStep: "simulate" }), false);
  assert.equal(validateTask({ ...snapshot, steps: [] }), false);

  const event = {
    protocolVersion: "1.1",
    kind: "event",
    messageId: "22222222222222222222222222222222",
    sentAt: "2026-07-26T00:00:01Z",
    sequence: 1,
    taskId: snapshot.taskId,
    payloadVersion: 1,
    payload: {}
  };
  for (const eventName of [
    "task.created",
    "task.started",
    "task.output",
    "task.cancellation_requested",
    "artifact.created",
    "task.finished"
  ]) {
    assert.equal(validateEvent({ ...event, event: eventName }), true, `${eventName} left the v1.1 event enum`);
  }
  assert.equal(validateEvent({ ...event, event: "task.step_started" }), false);
  assert.equal(validateEvent({ ...event, event: "task.step_finished" }), false);
});

test("protocol 1.0 capabilities remain closed to 1.1 fields", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const validate = ajv.compile(await load("../schema/v1/capabilities.schema.json"));
  assert.equal(validate({
    platform: "linux",
    transports: ["unix-socket"],
    toolchains: [],
    frameworks: [],
    coverageTools: [],
    taskExecution: true
  }), false);
});
