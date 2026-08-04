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

async function compileV13Message() {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  ajv.addSchema(await load("../schema/v1.2/workspace.schema.json"));
  for (const name of ["capabilities", "diagnostic", "test", "task", "event", "artifact"]) {
    ajv.addSchema(await load(`../schema/v1.3/${name}.schema.json`));
  }
  return ajv.compile(await load("../schema/v1.3/message.schema.json"));
}

test("protocol 1.3 accepts test discovery, run, catalog, and result contracts", async () => {
  const validate = await compileV13Message();
  for (const fixture of [
    "test-discovery-start.valid.json",
    "test-run-start.valid.json",
    "test-catalog.valid.json",
    "test-result.valid.json"
  ]) {
    const message = await load(`../fixtures/v1.3/${fixture}`);
    assert.equal(validate(message), true, `${fixture}: ${JSON.stringify(validate.errors)}`);
  }
  const resultWithoutSubtype = await load("../fixtures/v1.3/test-result.valid.json");
  delete resultWithoutSubtype.payload.result.failureDetails[0].subtype;
  assert.equal(
    validate(resultWithoutSubtype),
    true,
    `v1.3 detail without subtype: ${JSON.stringify(validate.errors)}`
  );

  const capabilities = {
    protocolVersion: "1.3",
    kind: "response",
    messageId: "cccccccccccccccccccccccccccccccc",
    requestId: "dddddddddddddddddddddddddddddddd",
    method: "capabilities/get",
    sentAt: "2026-07-31T00:00:07Z",
    payload: {
      workspaceInspect: true,
      targetList: true,
      cmakeBuild: true,
      testDiscovery: true,
      testRun: true,
      frameworkAdapters: [{
        id: "cpputest",
        contractVersion: "1",
        displayName: "CppUTest",
        canDiscoverCases: true,
        canRunCase: true,
        canReportSkipped: true,
        canReportSourceLocation: true,
        canReportMockDetails: true
      }],
      opaqueCTestFallback: true,
      ctestJson: true,
      maxRepeatCount: 100,
      maxSelectionSize: 100000,
      maxCatalogPageSize: 1000,
      unityHelperContractVersion: "1",
      unityRunnerContractVersion: "utide.runner.v1"
    }
  };
  assert.equal(validate(capabilities), true, JSON.stringify(validate.errors));

  const catalogRequest = {
    protocolVersion: "1.3",
    kind: "request",
    messageId: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    method: "tests/catalog/get",
    sentAt: "2026-07-31T00:00:08Z",
    payload: {
      projectId: "core",
      profileId: "b".repeat(64),
      limit: 1000
    }
  };
  const runGetRequest = {
    ...catalogRequest,
    messageId: "ffffffffffffffffffffffffffffffff",
    method: "tests/runs/get",
    payload: { runId: "7".repeat(32) }
  };
  const runListRequest = {
    ...catalogRequest,
    messageId: "0123456789abcdef0123456789abcdef",
    method: "tests/runs/list",
    payload: { projectId: "core", profileId: "b".repeat(64), limit: 1000 }
  };
  for (const request of [catalogRequest, runGetRequest, runListRequest]) {
    assert.equal(validate(request), true, `${request.method}: ${JSON.stringify(validate.errors)}`);
  }
});

test("protocol 1.3 rejects every execution-plan injection field at every test request boundary", async () => {
  const validate = await compileV13Message();
  const discovery = await load("../fixtures/v1.3/test-discovery-start.valid.json");
  const run = await load("../fixtures/v1.3/test-run-start.valid.json");
  const forbidden = {
    executable: "ctest",
    command: "ctest --output-on-failure",
    args: ["--output-on-failure"],
    argv: ["ctest"],
    shell: true,
    script: "ctest",
    env: { PATH: "/tmp/attacker" },
    environment: { PATH: "/tmp/attacker" },
    cwd: "/tmp",
    workingDirectory: "/tmp",
    hook: "before",
    preHook: "before",
    postHook: "after",
    resultPath: "/tmp/result.json"
  };

  for (const [field, value] of Object.entries(forbidden)) {
    for (const request of [discovery, run]) {
      assert.equal(
        validate({ ...request, payload: { ...request.payload, [field]: value } }),
        false,
        `${request.payload.kind} accepted payload.${field}`
      );
    }
    assert.equal(
      validate({
        ...run,
        payload: {
          ...run.payload,
          selection: { ...run.payload.selection, [field]: value }
        }
      }),
      false,
      `testRun accepted selection.${field}`
    );
  }

  for (const fixture of [
    "test-run-command.invalid.json",
    "test-run-environment.invalid.json",
    "test-run-args.invalid.json"
  ]) {
    assert.equal(validate(await load(`../fixtures/v1.3/${fixture}`)), false, fixture);
  }
});

test("protocol 1.3 enforces closed selections, stable IDs, safe integers, and bounded pages", async () => {
  const validate = await compileV13Message();
  const run = await load("../fixtures/v1.3/test-run-start.valid.json");
  const result = await load("../fixtures/v1.3/test-result.valid.json");
  const catalogRequest = {
    protocolVersion: "1.3",
    kind: "request",
    messageId: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
    method: "tests/catalog/get",
    sentAt: "2026-07-31T00:00:08Z",
    payload: { projectId: "core", profileId: "b".repeat(64) }
  };

  for (const repeatCount of [0, 101, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(validate({
      ...run,
      payload: { ...run.payload, repeatCount }
    }), false, `repeatCount=${repeatCount}`);
  }
  for (const limit of [0, 1001, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(validate({
      ...catalogRequest,
      payload: { ...catalogRequest.payload, limit }
    }), false, `limit=${limit}`);
  }

  const invalidSelections = [
    { mode: "items", itemIds: ["not-a-stable-id"] },
    { mode: "items", itemIds: [run.payload.selection.itemIds[0]], containerIds: [] },
    { mode: "failedFromRun", runId: "7".repeat(32), itemIds: [] },
    { mode: "filter", filter: {} },
    { mode: "unknown" }
  ];
  for (const selection of invalidSelections) {
    assert.equal(validate({
      ...run,
      payload: { ...run.payload, selection }
    }), false, JSON.stringify(selection));
  }

  const tooManyItemIds = Array.from(
    { length: 100001 },
    (_, index) => `utid-v1-${index.toString(16).padStart(64, "0")}`
  );
  const selectionAjv = new Ajv2020({ allErrors: false, strict: true });
  addFormats(selectionAjv);
  selectionAjv.addSchema(await load("../schema/v1.3/diagnostic.schema.json"));
  selectionAjv.addSchema(await load("../schema/v1.3/test.schema.json"));
  const validateSelection = selectionAjv.compile({
    "$ref": "urn:unit-test-ide:protocol:v1.3:test#/$defs/testSelection"
  });
  assert.equal(
    validateSelection({ mode: "items", itemIds: tooManyItemIds }),
    false,
    "selection accepted more than 100,000 items"
  );

  assert.equal(validate({
    ...result,
    payload: {
      ...result.payload,
      result: { ...result.payload.result, outcome: "unknown" }
    }
  }), false, "result accepted unknown outcome");
  assert.equal(validate({
    ...result,
    payload: {
      ...result.payload,
      result: {
        ...result.payload.result,
        failureDetails: [{
          ...result.payload.result.failureDetails[0],
          subtype: "shell_command"
        }]
      }
    }
  }), false, "result accepted unknown failure subtype");
  assert.equal(validate({
    ...result,
    payload: {
      ...result.payload,
      result: {
        ...result.payload.result,
        failureDetails: [{
          ...result.payload.result.failureDetails[0],
          category: "unexpected_exit"
        }]
      }
    }
  }), false, "result accepted mock subtype outside assertion_failure");
  assert.equal(validate({
    ...result,
    payload: {
      ...result.payload,
      result: { ...result.payload.result, iteration: Number.MAX_SAFE_INTEGER + 1 }
    }
  }), false, "result accepted unsafe integer");
  assert.equal(validate({
    ...result,
    payload: {
      ...result.payload,
      result: {
        ...result.payload.result,
        sourceLocation: { ...result.payload.result.sourceLocation, uri: "not a uri" }
      }
    }
  }), false, "result accepted invalid URI");
  assert.equal(validate({
    ...result,
    sentAt: "not a date"
  }), false, "result accepted invalid date-time");
});

test("protocol 1.3 preserves v1.0-v1.2 contracts without backporting test messages", async () => {
  const validateV13 = await compileV13Message();
  const ajvV12 = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajvV12);
  for (const name of ["capabilities", "diagnostic", "workspace", "task", "event", "artifact"]) {
    ajvV12.addSchema(await load(`../schema/v1.2/${name}.schema.json`));
  }
  const validateV12 = ajvV12.compile(await load("../schema/v1.2/message.schema.json"));

  const v12Build = await load("../fixtures/v1.2/cmake-build-start.valid.json");
  const v13Run = await load("../fixtures/v1.3/test-run-start.valid.json");
  assert.equal(validateV12(v12Build), true, JSON.stringify(validateV12.errors));
  assert.equal(validateV12(v13Run), false);
  assert.equal(validateV13(v12Build), false);

  const ajvV1 = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajvV1);
  const v1 = ajvV1.compile(await load("../schema/v1/message.schema.json"));
  assert.equal(v1(await load("../fixtures/v1/handshake.valid.json")), true);
});

async function compileV14Coverage() {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  ajv.addSchema(await load("../schema/v1.4/diagnostic.schema.json"));
  ajv.addSchema(await load("../schema/v1.4/test.schema.json"));
  ajv.addSchema(await load("../schema/v1.4/coverage.schema.json"));
  return {
    start: ajv.compile({ "$ref": "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRunStartRequest" }),
    run: ajv.compile({ "$ref": "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageRun" }),
    report: ajv.compile({ "$ref": "urn:unit-test-ide:protocol:v1.4:coverage#/$defs/coverageReport" })
  };
}

test("protocol 1.4 accepts standalone coverage start, run, and report payloads", async () => {
  const validate = await compileV14Coverage();
  const start = await load("../fixtures/v1.4/coverage-run-start.valid.json");
  const run = await load("../fixtures/v1.4/coverage-run.valid.json");
  const report = await load("../fixtures/v1.4/coverage-report.valid.json");
  assert.equal(validate.start(start.payload), true, JSON.stringify(validate.start.errors));
  assert.equal(validate.run(run.payload), true, JSON.stringify(validate.run.errors));
  assert.equal(validate.report(report.payload), true, JSON.stringify(validate.report.errors));
});

test("protocol 1.4 rejects execution-plan injection fields in coverage starts and selections", async () => {
  const { start: validate } = await compileV14Coverage();
  const request = await load("../fixtures/v1.4/coverage-run-start.valid.json");
  const forbidden = {
    executable: "llvm-cov",
    command: "llvm-cov export",
    args: ["export"],
    argv: ["llvm-cov"],
    flags: ["--format=json"],
    shell: true,
    script: "collect",
    env: { PATH: "C:/attacker" },
    environment: { PATH: "C:/attacker" },
    cwd: "C:/tmp",
    workingDirectory: "C:/tmp",
    hook: "before",
    resultPath: "C:/tmp/result.json",
    driver: "llvm-cov",
    collector: "gcovr",
    gcovrConfig: "attacker.cfg",
    python: "python",
    module: "gcovr",
    plugin: "attacker",
    template: "custom",
    threshold: 80
  };

  for (const [field, value] of Object.entries(forbidden)) {
    assert.equal(validate({ ...request.payload, [field]: value }), false, `payload.${field}`);
    assert.equal(
      validate({ ...request.payload, selection: { ...request.payload.selection, [field]: value } }),
      false,
      `selection.${field}`
    );
  }
  for (const fixture of [
    "coverage-run-command.invalid.json",
    "coverage-run-environment.invalid.json",
    "coverage-run-driver.invalid.json"
  ]) {
    assert.equal(validate((await load(`../fixtures/v1.4/${fixture}`)).payload), false, fixture);
  }
});

test("protocol 1.4 enforces coverage ranges, outcomes, completeness, and metadata boundaries", async () => {
  const validate = await compileV14Coverage();
  const start = await load("../fixtures/v1.4/coverage-run-start.valid.json");
  const run = await load("../fixtures/v1.4/coverage-run.valid.json");
  const report = await load("../fixtures/v1.4/coverage-report.valid.json");
  const { reportId, ...runWithoutReport } = run.payload;
  const { taskId, ...runWithoutTaskId } = run.payload;

  for (const repeatCount of [0, 101, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(validate.start({ ...start.payload, repeatCount }), false, `repeatCount=${repeatCount}`);
  }
  for (const timeoutMs of [0, 86400001, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(validate.start({ ...start.payload, timeoutMs }), false, `timeoutMs=${timeoutMs}`);
  }
  for (const selection of [{ mode: "unknown" }, { mode: "filter", filter: {} }]) {
    assert.equal(validate.start({ ...start.payload, selection }), false, JSON.stringify(selection));
  }
  assert.equal(
    validate.report({ ...report.payload, summary: { ...report.payload.summary, lines: { covered: Number.MAX_SAFE_INTEGER + 1, total: 12 } } }),
    false,
    "unsafe summary count"
  );
  assert.equal(validate.run({ ...run.payload, outcome: "unknown" }), false, "unknown outcome");
  assert.equal(validate.run(runWithoutTaskId), false, "coverage run requires taskId");
  assert.equal(validate.run({ ...runWithoutReport, outcome: "unavailable", reason: "unknown" }), false, "unknown reason");
  assert.equal(validate.run({ ...run.payload, outcome: "available", reason: "build_failed" }), false, "available run reason");
  assert.equal(validate.run({ ...runWithoutReport, outcome: "unavailable" }), false, "unavailable needs reason");
  assert.equal(validate.run({ ...runWithoutReport, outcome: "cancelled", reason: "build_failed" }), false, "cancel reason pair");
  assert.equal(
    validate.report({ ...report.payload, completeness: { outcome: "partial", reasons: [] } }),
    false,
    "partial completeness needs reasons"
  );
  assert.equal(
    validate.report({ ...report.payload, completeness: { outcome: "available", reasons: ["test_crashed"] } }),
    false,
    "available completeness forbids reasons"
  );
  for (const field of ["files", "lines"]) {
    assert.equal(validate.report({ ...report.payload, [field]: [] }), false, `report.${field}`);
  }
  for (const field of ["compiler", "driver", "collector"]) {
    assert.equal(
      validate.report({
        ...report.payload,
        toolProvenance: {
          ...report.payload.toolProvenance,
          [field]: { ...report.payload.toolProvenance[field], version: "\u0000" }
        }
      }),
      false,
      `${field}.version rejects NUL`
    );
  }
  assert.equal(
    validate.report({
      ...report.payload,
      toolProvenance: { ...report.payload.toolProvenance, normalizerVersion: "\u0000" }
    }),
    false,
    "normalizerVersion rejects NUL"
  );
});

async function compileV14Message() {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  ajv.addSchema(await load("../schema/v1.2/workspace.schema.json"));
  for (const name of ["capabilities", "diagnostic", "test", "coverage", "task", "event", "artifact"]) {
    ajv.addSchema(await load(`../schema/v1.4/${name}.schema.json`));
  }
  return ajv.compile(await load("../schema/v1.4/message.schema.json"));
}

test("protocol 1.4 exposes coverage messages as closed whole-message contracts", async () => {
  const validate = await compileV14Message();
  const [start, run, report] = await Promise.all([
    load("../fixtures/v1.4/coverage-run-start.valid.json"),
    load("../fixtures/v1.4/coverage-run.valid.json"),
    load("../fixtures/v1.4/coverage-report.valid.json")
  ]);
  for (const fixture of [start, run, report]) {
    assert.equal(validate(fixture), true, JSON.stringify(validate.errors));
  }
  for (const fixture of [
    "coverage-run-command.invalid.json",
    "coverage-run-driver.invalid.json",
    "coverage-run-environment.invalid.json"
  ]) {
    assert.equal(validate(await load(`../fixtures/v1.4/${fixture}`)), false, fixture);
  }
});

test("protocol 1.4 coverage request boundaries and pages are closed and bounded", async () => {
  const validate = await compileV14Message();
  const start = await load("../fixtures/v1.4/coverage-run-start.valid.json");
  const run = await load("../fixtures/v1.4/coverage-run.valid.json");
  const base = {
    protocolVersion: "1.4",
    kind: "request",
    messageId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    sentAt: "2026-08-04T00:00:00Z"
  };
  const requests = [
    start,
    { ...base, method: "coverage/runs/get", payload: { coverageRunId: "5".repeat(32) } },
    { ...base, method: "coverage/runs/list", payload: { projectId: "core", coverageProfileId: "coverage-debug", cursor: "page-1", limit: 200 } },
    { ...base, method: "coverage/reports/get", payload: { reportId: "8".repeat(32) } }
  ];
  for (const request of requests) {
    assert.equal(validate(request), true, `${request.method}: ${JSON.stringify(validate.errors)}`);
  }
  const list = requests[2];
  for (const limit of [0, 201, 1.5, Number.MAX_SAFE_INTEGER + 1]) {
    assert.equal(validate({ ...list, payload: { ...list.payload, limit } }), false, `limit=${limit}`);
  }
  for (const cursor of ["", "x".repeat(4097)]) {
    assert.equal(validate({ ...list, payload: { ...list.payload, cursor } }), false, `cursor=${cursor.length}`);
  }
  assert.equal(validate({ ...requests[1], payload: { coverageRunId: "bad" } }), false, "malformed coverageRunId");
  assert.equal(validate({ ...list, payload: { ...list.payload, extra: true } }), false, "open list request");

  const page = {
    ...run,
    method: "coverage/runs/list",
    payload: { items: Array.from({ length: 200 }, () => run.payload) }
  };
  assert.equal(validate(page), true, JSON.stringify(validate.errors));
  assert.equal(validate({ ...page, payload: { items: [...page.payload.items, run.payload] } }), false, "201 coverage runs");
});

test("protocol 1.4 capabilities and artifacts add only the public coverage surface", async () => {
  const validate = await compileV14Message();
  const base = {
    protocolVersion: "1.4", kind: "response", messageId: "a".repeat(32), requestId: "b".repeat(32),
    sentAt: "2026-08-04T00:00:00Z", method: "capabilities/get"
  };
  const payload = {
    workspaceInspect: true, targetList: true, cmakeBuild: true, testDiscovery: true, testRun: true,
    frameworkAdapters: [], opaqueCTestFallback: true, ctestJson: true, maxRepeatCount: 100,
    maxSelectionSize: 100000, maxCatalogPageSize: 1000, unityHelperContractVersion: "1",
    unityRunnerContractVersion: "utide.runner.v1", coverageRun: true, coverageReport: true,
    maxCoveragePageSize: 200, maxCoverageTimeoutMs: 86400000
  };
  assert.equal(validate({ ...base, payload }), true, JSON.stringify(validate.errors));
  const { coverageRun: _coverageRun, ...missing } = payload;
  assert.equal(validate({ ...base, payload: missing }), false, "missing coverage field");
  assert.equal(validate({ ...base, payload: { ...payload, coverageRun: false } }), false, "false coverage constant");
  assert.equal(validate({ ...base, payload: { ...payload, coverageExtra: true } }), false, "unknown capability");
  assert.equal(validate({ ...base, payload: { ...payload, maxCoveragePageSize: 201 } }), false, "coverage page size");
  assert.equal(validate({ ...base, payload: { ...payload, maxCoverageTimeoutMs: 86400001 } }), false, "coverage timeout");

  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validateArtifact = ajv.compile(await load("../schema/v1.4/artifact.schema.json"));
  const artifact = { artifactId: "a".repeat(32), taskId: "b".repeat(32), sizeBytes: 1, sha256: "c".repeat(64), createdAt: "2026-08-04T00:00:00Z", uri: "unit-test-ide://artifact/a" };
  for (const [kind, mimeType] of [["coverage-json", "application/json"], ["junit-xml", "application/xml"], ["coverage-html", "text/html"]]) {
    assert.equal(validateArtifact({ ...artifact, kind, mimeType }), true, `${kind}: ${JSON.stringify(validateArtifact.errors)}`);
  }
  for (const kind of ["raw-profile", "indexed-profile", "gcda", "third-party-json"]) {
    assert.equal(validateArtifact({ ...artifact, kind, mimeType: "application/json" }), false, kind);
  }
  assert.equal(validateArtifact({ ...artifact, kind: "coverage-html", mimeType: "application/json" }), false, "coverage artifact MIME pairing");
  assert.equal(validateArtifact({ ...artifact, kind: "coverage-json", mimeType: "text/plain" }), false, "unknown MIME");
});

test("protocol 1.4 coverage tasks and journal events preserve closed task invariants", async () => {
  const validate = await compileV14Message();
  const run = await load("../fixtures/v1.4/coverage-run.valid.json");
  const task = {
    taskId: run.payload.taskId, kind: "coverageRun", workspaceGeneration: run.payload.workspaceGeneration,
    projectId: run.payload.projectId, coverageProfileId: run.payload.coverageProfileId,
    catalogRevision: run.payload.catalogRevision, coverageRunId: run.payload.coverageRunId,
    testRunId: run.payload.testRunId, repeatCount: run.payload.repeatCount, timeoutMs: run.payload.timeoutMs,
    status: "finished", outcome: "succeeded", createdAt: run.payload.createdAt, lastSequence: 42
  };
  const response = { protocolVersion: "1.4", kind: "response", messageId: "a".repeat(32), requestId: "b".repeat(32), sentAt: "2026-08-04T00:00:04Z", method: "tasks/get", payload: task };
  for (const method of ["tasks/get", "tasks/cancel"]) assert.equal(validate({ ...response, method }), true, method);
  assert.equal(validate({ ...response, method: "tasks/list", payload: { items: [task] } }), true, "tasks/list");
  assert.equal(validate({ ...response, payload: { ...task, coverageRunId: "bad" } }), false, "malformed coverage task id");
  assert.equal(validate({ ...response, payload: { ...task, unexpected: true } }), false, "open coverage task");
  assert.equal(validate({ ...(await load("../fixtures/v1.4/coverage-run-start.valid.json")), method: "tasks/start", payload: { kind: "coverageRun" } }), false, "tasks/start coverage");

  const eventBase = { protocolVersion: "1.4", kind: "event", messageId: "c".repeat(32), sentAt: "2026-08-04T00:00:05Z", sequence: 43, taskId: task.taskId, payloadVersion: 1 };
  const summary = (await load("../fixtures/v1.4/coverage-report.valid.json")).payload.summary;
  const events = [
    ["coverage.run.started", { coverageRunId: task.coverageRunId, testRunId: task.testRunId, catalogRevision: task.catalogRevision, repeatCount: 2 }],
    ["coverage.build.finished", { coverageRunId: task.coverageRunId }],
    ["coverage.collection.started", { coverageRunId: task.coverageRunId, testRunId: task.testRunId }],
    ["coverage.report.available", { coverageRunId: task.coverageRunId, reportId: "8".repeat(32), artifactId: "9".repeat(32), completeness: { outcome: "available", reasons: [] }, summary }],
    ["coverage.run.finished", { coverageRunId: task.coverageRunId, outcome: "available", reportId: "8".repeat(32) }]
  ];
  for (const [event, payload] of events) assert.equal(validate({ ...eventBase, event, payload }), true, `${event}: ${JSON.stringify(validate.errors)}`);
  assert.equal(validate({ ...eventBase, sequence: Number.MAX_SAFE_INTEGER + 1, event: events[0][0], payload: events[0][1] }), false, "unsafe event sequence");
  for (const kind of ["coverage-configure", "coverage-build", "coverage-test", "coverage-merge", "coverage-normalize", "coverage-report", "coverage-publish"]) {
    assert.equal(validate({ ...eventBase, event: "task.step_started", payload: { stepId: "coverage", kind, status: "running" } }), true, kind);
  }
  assert.equal(validate({ ...eventBase, event: "coverage.run.finished", payload: { coverageRunId: task.coverageRunId, outcome: "available", reason: "build_failed" } }), false, "coverage finished invariant");
  assert.equal(validate({ ...eventBase, event: "coverage.file.finished", payload: {} }), false, "coverage.file event");
  assert.equal(validate({ ...eventBase, event: "coverage.line.finished", payload: {} }), false, "coverage.line event");
});

test("protocol 1.4 keeps earlier protocol validators strict and leaves tasks/start coverage-free", async () => {
  const validateV14 = await compileV14Message();
  const validateV13 = await compileV13Message();
  const v13Run = await load("../fixtures/v1.3/test-run-start.valid.json");
  const v14Start = await load("../fixtures/v1.4/coverage-run-start.valid.json");
  assert.equal(validateV13(v13Run), true, JSON.stringify(validateV13.errors));
  assert.equal(validateV13(v14Start), false, "v1.3 coverage start");
  for (const fixture of [
    "coverage-run.valid.json",
    "coverage-report.valid.json",
    "coverage-run-command.invalid.json",
    "coverage-run-driver.invalid.json",
    "coverage-run-environment.invalid.json"
  ]) {
    assert.equal(validateV13(await load(`../fixtures/v1.4/${fixture}`)), false, fixture);
  }
  assert.equal(validateV14({ ...v13Run, protocolVersion: "1.4" }), true, JSON.stringify(validateV14.errors));
  const v13Capabilities = await load("../schema/v1.3/capabilities.schema.json");
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validateCapabilities = ajv.compile(v13Capabilities);
  assert.equal(validateCapabilities({ workspaceInspect: true, targetList: true, cmakeBuild: true, testDiscovery: true, testRun: true, frameworkAdapters: [], opaqueCTestFallback: true, ctestJson: true, maxRepeatCount: 100, maxSelectionSize: 100000, maxCatalogPageSize: 1000, unityHelperContractVersion: "1", unityRunnerContractVersion: "1", coverageRun: true }), false);
  const eventAjv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(eventAjv);
  eventAjv.addSchema(await load("../schema/v1.3/diagnostic.schema.json"));
  eventAjv.addSchema(await load("../schema/v1.3/test.schema.json"));
  const validateV13Event = eventAjv.compile(await load("../schema/v1.3/event.schema.json"));
  assert.equal(validateV13Event({ protocolVersion: "1.3", kind: "event", messageId: "c".repeat(32), sentAt: "2026-08-04T00:00:05Z", sequence: 1, event: "coverage.run.started", taskId: "d".repeat(32), payloadVersion: 1, payload: {} }), false, "v1.3 coverage event");
});
