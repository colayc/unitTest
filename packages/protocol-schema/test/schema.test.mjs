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

test("protocol 1.2 validates workspace builds and keeps v1.1 strict", async () => {
  const ajvV12 = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajvV12);
  for (const name of ["capabilities", "diagnostic", "workspace", "task", "event", "artifact"]) {
    ajvV12.addSchema(await load(`../schema/v1.2/${name}.schema.json`));
  }
  const validateV12 = ajvV12.compile(await load("../schema/v1.2/message.schema.json"));

  const ajvV11 = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajvV11);
  for (const name of ["task", "artifact", "event"]) {
    ajvV11.addSchema(await load(`../schema/v1.1/${name}.schema.json`));
  }
  const validateV11 = ajvV11.compile(await load("../schema/v1.1/message.schema.json"));

  assert.equal(validateV12({
    protocolVersion: "1.2",
    kind: "request",
    messageId: "0123456789abcdef0123456789abcdef",
    method: "handshake",
    sentAt: "2026-07-26T00:00:00Z",
    payload: {
      token: "0123456789abcdef",
      clientName: "schema-test",
      clientVersion: "0.3.0",
      supportedProtocolVersions: ["1.2", "1.1", "1.0"]
    }
  }), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(await load("../fixtures/v1.2/workspace-inspect.valid.json")), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(await load("../fixtures/v1.2/targets-list.valid.json")), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(await load("../fixtures/v1.2/cmake-build-start.valid.json")), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(await load("../fixtures/v1.2/event-diagnostic.valid.json")), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(await load("../fixtures/v1.2/cmake-build-shell.invalid.json")), false);
  assert.equal(validateV11(await load("../fixtures/v1.2/cmake-build-start.valid.json")), false);

  const targets = await load("../fixtures/v1.2/targets-list.valid.json");
  assert.equal(validateV12(targets), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12({ ...targets, method: "targets/list" }), false);
  const targetsRequest = {
    protocolVersion: "1.2",
    kind: "request",
    messageId: "66666666666666666666666666666666",
    sentAt: "2026-07-26T00:00:01Z",
    method: "cmake/targets/list",
    payload: {
      workspaceGeneration: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      projectId: "example-project",
      buildProfileId: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
    }
  };
  assert.equal(validateV12(targetsRequest), true, JSON.stringify(validateV12.errors));
  const { buildProfileId: _omittedProfile, ...targetsWithoutProfile } = targetsRequest.payload;
  assert.equal(validateV12({ ...targetsRequest, payload: targetsWithoutProfile }), false);

  const stepStarted = {
    protocolVersion: "1.2",
    kind: "event",
    messageId: "33333333333333333333333333333333",
    sentAt: "2026-07-26T00:00:01Z",
    sequence: 2,
    event: "task.step_started",
    taskId: "fedcba9876543210fedcba9876543210",
    payloadVersion: 1,
    payload: { stepId: "configure", kind: "configure", status: "running" }
  };
  const stepFinished = {
    ...stepStarted,
    sequence: 3,
    event: "task.step_finished",
    payload: { stepId: "build", kind: "build", status: "succeeded", exitCode: 0 }
  };
  assert.equal(validateV12(stepStarted), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(stepFinished), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12({ ...stepStarted, payload: { ...stepStarted.payload, executable: "cmake" } }), false);
  assert.equal(validateV11(stepStarted), false);
  assert.equal(validateV11(stepFinished), false);

  const configureRequired = {
    protocolVersion: "1.2",
    kind: "error",
    messageId: "44444444444444444444444444444444",
    requestId: "55555555555555555555555555555555",
    sentAt: "2026-07-26T00:00:01Z",
    error: { code: "CONFIGURE_REQUIRED", message: "cmake/targets/list requires a valid File API reply", retryable: false }
  };
  assert.equal(validateV12(configureRequired), true, JSON.stringify(validateV12.errors));

  for (const code of [
    "WORKSPACE_CHANGED",
    "PROJECT_NOT_FOUND",
    "BUILD_PROFILE_NOT_FOUND",
    "TARGET_NOT_FOUND"
  ]) {
    assert.equal(
      validateV12({ ...configureRequired, error: { ...configureRequired.error, code } }),
      true,
      `${code}: ${JSON.stringify(validateV12.errors)}`
    );
  }
});

test("protocol 1.2 accepts the Service journal event payloads without weakening them", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validate = ajv.compile(await load("../schema/v1.2/event.schema.json"));
  const base = {
    protocolVersion: "1.2",
    kind: "event",
    messageId: "77777777777777777777777777777777",
    sentAt: "2026-07-29T00:00:00Z",
    sequence: 1,
    taskId: "11111111111111111111111111111111",
    payloadVersion: 1
  };
  const events = [
    { event: "task.created", payload: { status: "queued" } },
    { event: "task.started", payload: { status: "running" } },
    {
      event: "task.step_started",
      payload: { stepId: "configure", kind: "configure", status: "running" }
    },
    {
      event: "task.step_started",
      payload: { stepId: "simulate", kind: "simulation", status: "running" }
    },
    {
      event: "task.output",
      payload: { stepId: "configure", stream: "stdout", text: "hello", truncated: false }
    },
    {
      event: "task.step_finished",
      payload: { stepId: "configure", kind: "configure", status: "succeeded", exitCode: 0 }
    },
    { event: "task.cancellation_requested", payload: { status: "cancelling" } },
    {
      event: "artifact.created",
      payload: { artifactId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", kind: "task-summary" }
    },
    { event: "task.finished", payload: { outcome: "succeeded" } },
    {
      event: "task.diagnostic",
      payload: { diagnostic: { severity: "warning", code: "C123", message: "warning" } }
    }
  ];
  for (const [index, event] of events.entries()) {
    assert.equal(
      validate({ ...base, sequence: index + 1, ...event }),
      true,
      `${event.event}: ${JSON.stringify(validate.errors)}`
    );
  }
  assert.equal(validate({
    ...base,
    event: "task.output",
    payload: { stepId: "configure", stream: "stdout", text: "hello", truncated: false, executable: "cmake" }
  }), false);
});

test("protocol 1.2 preserves the complete task, subscription, and artifact method surface", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  for (const name of ["capabilities", "diagnostic", "workspace", "task", "event", "artifact"]) {
    ajv.addSchema(await load(`../schema/v1.2/${name}.schema.json`));
  }
  const validateV12 = ajv.compile(await load("../schema/v1.2/message.schema.json"));
  const base = {
    protocolVersion: "1.2",
    messageId: "88888888888888888888888888888888",
    sentAt: "2026-07-29T00:00:00Z"
  };
  const task = {
    taskId: "11111111111111111111111111111111",
    kind: "cmakeBuild",
    workspaceGeneration: "2".repeat(64),
    projectId: "core",
    buildProfileId: "3".repeat(64),
    targetIds: ["4".repeat(64)],
    jobs: 8,
    timeoutMs: 600000,
    status: "running",
    createdAt: "2026-07-29T00:00:00Z",
    lastSequence: 2
  };
  const artifact = {
    artifactId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    taskId: "11111111111111111111111111111111",
    kind: "task-summary",
    mimeType: "application/json",
    sizeBytes: 2,
    sha256: "0".repeat(64),
    createdAt: "2026-07-29T00:00:00Z",
    uri: "unit-test-ide://artifact/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  };
  const messages = [
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "handshake",
      payload: { negotiatedProtocolVersion: "1.2", serviceVersion: "0.3.0" }
    },
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "shutdown",
      payload: { accepted: true }
    },
    {
      ...base,
      kind: "request",
      method: "events/subscribe",
      payload: { afterSequence: 0 }
    },
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "events/subscribe",
      payload: { afterSequence: 0 }
    },
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "tasks/list",
      payload: { items: [task] }
    },
    {
      ...base,
      kind: "request",
      method: "artifacts/list",
      payload: { taskId: "11111111111111111111111111111111", limit: 10 }
    },
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "artifacts/list",
      payload: { items: [artifact] }
    },
    {
      ...base,
      kind: "request",
      method: "artifacts/read",
      payload: { artifactId: artifact.artifactId, offset: 0, length: 65536 }
    },
    {
      ...base,
      kind: "response",
      requestId: "11111111111111111111111111111111",
      method: "artifacts/read",
      payload: { data: "e30", nextOffset: 2, eof: true, sizeBytes: 2, sha256: artifact.sha256 }
    }
  ];
  for (const message of messages) {
    assert.equal(validateV12(message), true, `${message.method}: ${JSON.stringify(validateV12.errors)}`);
  }
});

test("protocol 1.2 refuses execution details and native workspace paths", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  for (const name of ["capabilities", "diagnostic", "workspace", "task", "event", "artifact"]) {
    ajv.addSchema(await load(`../schema/v1.2/${name}.schema.json`));
  }
  const validate = ajv.compile(await load("../schema/v1.2/message.schema.json"));
  const build = await load("../fixtures/v1.2/cmake-build-start.valid.json");
  const workspace = await load("../fixtures/v1.2/workspace-inspect.valid.json");

  for (const field of ["executable", "args", "env", "workingDirectory", "presetPath", "nativeToolOptions"]) {
    assert.equal(validate({ ...build, payload: { ...build.payload, [field]: "forbidden" } }), false, field);
  }
  for (const field of ["compilerPath", "binaryDirectory", "serviceDataPath"]) {
    assert.equal(validate({ ...workspace, payload: { ...workspace.payload, [field]: "forbidden" } }), false, field);
  }

  const safeToolchain = workspace.payload.toolchains[0];
  for (const field of ["cCompiler", "cxxCompiler", "environment", "sysroot", "coverageToolPath"]) {
    assert.equal(validate({
      ...workspace,
      payload: {
        ...workspace.payload,
        toolchains: [{ ...safeToolchain, [field]: "forbidden" }]
      }
    }), false, `toolchain.${field}`);
  }
});

test("protocol 1.2 generates TargetList from the isolated workspace contract", async () => {
  const workspace = await load("../schema/v1.2/workspace.schema.json");
  const message = await load("../schema/v1.2/message.schema.json");
  const generator = await readFile(new URL("../../../tools/protocol-gen/generate.mjs", import.meta.url), "utf8");

  assert.ok(workspace.$defs.targetList);
  assert.equal(message.$defs.targetList, undefined);
  assert.match(generator, /schema: "workspace\.schema\.json", definition: "targetList", top: "TargetList"/);
});

test("protocol 1.2 accepts the complete CMake build artifact surface", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validate = ajv.compile(await load("../schema/v1.2/artifact.schema.json"));
  const base = {
    artifactId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    taskId: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    sizeBytes: 0,
    sha256: "c".repeat(64),
    createdAt: "2026-07-26T00:00:00Z",
    uri: "unit-test-ide://artifact/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  };
  const artifacts = [
    { kind: "build-summary", mimeType: "application/json" },
    { kind: "execution-plan", mimeType: "application/json" },
    { kind: "diagnostics", mimeType: "application/x-ndjson" },
    { kind: "stdout", mimeType: "application/octet-stream" },
    { kind: "stderr", mimeType: "application/octet-stream" }
  ];

  for (const artifact of artifacts) {
    assert.equal(validate({ ...base, ...artifact }), true, JSON.stringify(validate.errors));
  }
  assert.equal(validate({ ...base, kind: "process-environment", mimeType: "application/json" }), false);
});
