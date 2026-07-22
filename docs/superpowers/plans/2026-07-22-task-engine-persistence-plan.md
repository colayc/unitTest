# Phase 2 任务引擎、进程控制与持久化实施计划

> **面向智能体工作者：** 必须使用以下子技能之一：superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，按任务逐项实施本计划。步骤采用复选框（`- [ ]`）语法进行跟踪。

**目标：** 构建一个跨平台 Phase 2 垂直切片，使客户端能够启动、观察、重连恢复、取消并持久化受控模拟任务，同时可靠终止 Windows/Linux 完整进程树。

**架构：** Go `TaskManager` 是运行中任务和状态迁移的唯一写入者；SQLite 以事务保存任务快照、追加事件、进程租约和制品元数据，`EventBroker` 只广播已经提交的事件。平台 `ProcessRunner` 通过受启动屏障保护的内部 Process Host 管理进程树，TypeScript 客户端使用协议 `1.1` 请求任务并按全局 `sequence` 重放事件，同时保持 `1.0` 客户端兼容。

**技术栈：** Node.js 24.18.0、pnpm 11.4.0、TypeScript 6.0.3、`@types/node` 24.13.3、Go 1.26.5、JSON Schema 2020-12、quicktype 24.0.0、Ajv 8.20.0、ajv-formats 3.0.1、`modernc.org/sqlite` 1.54.0、`modernc.org/libc` 1.74.1、`golang.org/x/sys` 0.46.0，以及 `github.com/Microsoft/go-winio` 0.6.2。

**依赖锁定依据：** Go 官方包索引将 `modernc.org/sqlite` 1.54.0 标为 2026-07-15 发布的当前版本；该版本的上游 `go.mod` 固定 `modernc.org/libc` 1.74.1 和 `golang.org/x/sys` 0.46.0，因此本项目同步固定这些版本，避免脆弱的 transitive ABI 组合。参见 [pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite) 与 [上游 go.mod](https://gitlab.com/cznic/sqlite/-/raw/v1.54.0/go.mod)。

## 全局约束

- Phase 2 只执行服务内置的模拟场景；协议不得接受程序路径、Shell 字符串、任意参数、任意环境变量或任意工作目录。
- Windows 使用 Job Object，Linux 使用 Process Group/Session；取消、超时、主进程退出和服务关闭均不得遗留后代进程。
- 同一服务实例支持断线重连和至少一次事件重放；客户端按全局单调递增的 `sequence` 去重。
- 服务重启不恢复原进程；所有非终止任务转换为 `finished/interrupted`。
- 任务生命周期状态仅为 `queued`、`running`、`cancelling`、`finished`。
- 终止结果仅为 `succeeded`、`command_failed`、`cancelled`、`timed_out`、`interrupted`、`infrastructure_failed`；Phase 2 不产生 `test_failed`。
- SQLite 与制品目录分离；制品只能按服务生成的 ID 读取，且必须校验规范化路径、大小和 SHA-256。
- Phase 2 不删除已完成任务、已提交事件或已引用制品；启动 Cleanup 只删除临时文件和数据库未引用的孤立文件。
- 协议消息继续使用 UTF-8 NDJSON，单行编码后大小上限为 1 MiB。
- 协议 `1.1` 新增任务能力；新服务仍须为 `1.0` 客户端返回严格的 `1.0` 响应形状。
- 生成的 TypeScript/Go 协议模型必须提交，`pnpm check:protocol-generated` 必须保持干净。
- 服务继续使用 Phase 1 的 per-user Named Pipe/Unix Socket、owner-only token 文件和 handshake。
- Go 服务不得依赖 Electron、DOM、Code-OSS 对象或 Shell 命令字符串。
- 日志、Host status 和协议错误不得包含 token、完整环境变量、SQLite DSN 或内部绝对路径。
- 每个任务严格遵循 Red-Green-Refactor：先观察目标测试失败，再写最小实现，最后运行相关回归测试并提交。

---

## 锁定的文件结构

```text
apps/test-service/
├── go.mod
├── go.sum
├── cmd/unit-test-service/
│   ├── main.go
│   ├── main_test.go
│   ├── process_host_unix.go
│   ├── process_host_windows.go
│   └── process_modes_test.go
└── internal/
    ├── artifactstore/
    │   ├── store.go
    │   ├── store_test.go
    │   ├── path_unix.go
    │   └── path_windows.go
    ├── eventbroker/
    │   ├── broker.go
    │   └── broker_test.go
    ├── instance/
    │   ├── lock.go
    │   ├── lock_unix.go
    │   ├── lock_windows.go
    │   └── lock_test.go
    ├── processcontrol/
    │   ├── process.go
    │   ├── host_protocol.go
    │   ├── runner_unix.go
    │   ├── runner_unix_test.go
    │   ├── runner_windows.go
    │   └── runner_windows_test.go
    ├── processhost/
    │   ├── host.go
    │   ├── host_unix.go
    │   ├── host_windows.go
    │   └── host_test.go
    ├── protocol/
    │   ├── envelope.go
    │   └── envelope_test.go
    ├── protocolmodel/
    │   ├── artifact_generated.go
    │   ├── capabilities_v11_generated.go
    │   ├── event_generated.go
    │   ├── generated.go
    │   └── task_generated.go
    ├── runtime/
    │   ├── data_dir.go
    │   ├── data_dir_unix.go
    │   ├── data_dir_windows.go
    │   ├── runtime.go
    │   └── runtime_test.go
    ├── server/
    │   ├── server.go
    │   ├── server_test.go
    │   ├── service.go
    │   └── service_test.go
    ├── session/
    │   ├── session.go
    │   └── session_test.go
    ├── task/
    │   ├── manager.go
    │   ├── manager_test.go
    │   ├── model.go
    │   ├── ports.go
    │   ├── state.go
    │   └── state_test.go
    ├── taskfixture/
    │   ├── fixture.go
    │   └── fixture_test.go
    └── taskstore/
        ├── artifacts.go
        ├── events.go
        ├── migrations.go
        ├── migrations/001_initial.sql
        ├── recovery.go
        ├── sqlite.go
        ├── sqlite_test.go
        └── tasks.go
packages/protocol-schema/
├── fixtures/v1.1/
│   ├── event-task-started.valid.json
│   ├── handshake.valid.json
│   ├── task-start-shell.invalid.json
│   └── task-start.valid.json
├── schema/v1.1/
│   ├── artifact.schema.json
│   ├── capabilities.schema.json
│   ├── event.schema.json
│   ├── message.schema.json
│   └── task.schema.json
├── package.json
└── test/schema.test.mjs
packages/protocol-models/src/
├── generated/
│   ├── artifact.ts
│   ├── capabilities-v1-1.ts
│   ├── event.ts
│   └── task.ts
├── generated-contract.test.ts
└── index.ts
packages/test-client/src/
├── client.ts
├── client.test.ts
├── connection.ts
├── envelopes.ts
├── index.ts
└── subscription.ts
tools/protocol-gen/generate.mjs
tools/service-probe/src/
├── probe.ts
└── probe.test.ts
docs/decisions/0002-task-engine-event-journal.md
README.md
```

## Task 1：定义协议 `1.1` Schema 与生成模型

**文件：**

- 创建：`packages/protocol-schema/schema/v1.1/task.schema.json`
- 创建：`packages/protocol-schema/schema/v1.1/event.schema.json`
- 创建：`packages/protocol-schema/schema/v1.1/artifact.schema.json`
- 创建：`packages/protocol-schema/schema/v1.1/capabilities.schema.json`
- 创建：`packages/protocol-schema/schema/v1.1/message.schema.json`
- 创建：`packages/protocol-schema/fixtures/v1.1/handshake.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.1/task-start.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.1/task-start-shell.invalid.json`
- 创建：`packages/protocol-schema/fixtures/v1.1/event-task-started.valid.json`
- 修改：`packages/protocol-schema/package.json`
- 修改：`packages/protocol-schema/test/schema.test.mjs`
- 修改：`tools/protocol-gen/generate.mjs`
- 修改：`packages/protocol-models/src/generated-contract.test.ts`
- 修改：`packages/protocol-models/src/index.ts`
- 生成：`packages/protocol-models/src/generated/{task,event,artifact,capabilities-v1-1}.ts`
- 生成：`apps/test-service/internal/protocolmodel/{task,event,artifact,capabilities_v11}_generated.go`

**接口：**

- 输入：现有 `1.0` message/capabilities Schema 和确定性 quicktype 生成器。
- 产出：`TaskSnapshot`、`TaskEvent`、`ArtifactMetadata`、`CapabilitiesV11` 的 TypeScript/Go 生成类型，以及可验证 `1.1` envelope 的 Schema。

- [ ] **Step 1：编写会失败的 `1.1` Schema 契约测试**

在 `packages/protocol-schema/test/schema.test.mjs` 中保留现有测试，并加入：

```js
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
```

- [ ] **Step 2：运行测试并确认新 Schema 缺失**

运行：`pnpm --filter @unit-test-ide/protocol-schema test`

预期：FAIL，错误包含 `schema/v1.1/task.schema.json` 或 `ENOENT`。

- [ ] **Step 3：添加严格的领域 Schema**

`task.schema.json` 的完整顶层模型：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1.1:task",
  "title": "TaskSnapshot",
  "type": "object",
  "additionalProperties": false,
  "required": ["taskId", "kind", "scenario", "status", "createdAt", "lastSequence"],
  "properties": {
    "taskId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
    "kind": { "const": "simulation" },
    "scenario": { "enum": ["success", "exit-nonzero", "hang", "spawn-child", "emit-output"] },
    "status": { "enum": ["queued", "running", "cancelling", "finished"] },
    "outcome": { "enum": ["succeeded", "command_failed", "cancelled", "timed_out", "interrupted", "infrastructure_failed"] },
    "createdAt": { "type": "string", "format": "date-time" },
    "startedAt": { "type": "string", "format": "date-time" },
    "finishedAt": { "type": "string", "format": "date-time" },
    "timeoutMs": { "type": "integer", "minimum": 1, "maximum": 86400000 },
    "lastSequence": { "type": "integer", "minimum": 1 },
    "errorCode": { "type": "string" },
    "errorMessage": { "type": "string" }
  },
  "allOf": [
    {
      "if": { "properties": { "status": { "const": "finished" } }, "required": ["status"] },
      "then": { "required": ["outcome", "finishedAt"] },
      "else": { "not": { "required": ["outcome"] } }
    }
  ]
}
```

`artifact.schema.json`：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1.1:artifact",
  "title": "ArtifactMetadata",
  "type": "object",
  "additionalProperties": false,
  "required": ["artifactId", "taskId", "kind", "mimeType", "sizeBytes", "sha256", "createdAt"],
  "properties": {
    "artifactId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
    "taskId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
    "kind": { "const": "task-summary" },
    "mimeType": { "const": "application/json" },
    "sizeBytes": { "type": "integer", "minimum": 0 },
    "sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
    "createdAt": { "type": "string", "format": "date-time" }
  }
}
```

`event.schema.json`：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1.1:event",
  "title": "TaskEvent",
  "type": "object",
  "additionalProperties": false,
  "required": ["protocolVersion", "kind", "messageId", "sentAt", "sequence", "event", "taskId", "payloadVersion", "payload"],
  "properties": {
    "protocolVersion": { "const": "1.1" },
    "kind": { "const": "event" },
    "messageId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
    "sentAt": { "type": "string", "format": "date-time" },
    "sequence": { "type": "integer", "minimum": 1 },
    "event": { "enum": ["task.created", "task.started", "task.output", "task.cancellation_requested", "task.finished", "artifact.created"] },
    "taskId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
    "payloadVersion": { "const": 1 },
    "payload": { "type": "object" }
  }
}
```

`capabilities.schema.json` 使用完整闭合模型：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1.1:capabilities",
  "title": "CapabilitiesV11",
  "type": "object",
  "additionalProperties": false,
  "required": ["platform", "transports", "toolchains", "frameworks", "coverageTools", "taskExecution", "eventReplay", "sqliteHistory", "artifactRead", "processTreeControl"],
  "properties": {
    "platform": { "enum": ["windows", "linux"] },
    "transports": { "type": "array", "items": { "enum": ["named-pipe", "unix-socket"] } },
    "toolchains": { "type": "array", "items": { "type": "string" } },
    "frameworks": { "type": "array", "items": { "type": "string" } },
    "coverageTools": { "type": "array", "items": { "type": "string" } },
    "taskExecution": { "const": true },
    "eventReplay": { "const": true },
    "sqliteHistory": { "const": true },
    "artifactRead": { "const": true },
    "processTreeControl": { "enum": ["job-object", "process-group"] }
  }
}
```

`message.schema.json` 使用以下完整结构；所有请求 payload 都是闭合对象：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1.1:message",
  "title": "ProtocolMessageV11",
  "oneOf": [
    { "$ref": "#/$defs/handshakeRequest" },
    { "$ref": "#/$defs/emptyRequest" },
    { "$ref": "#/$defs/taskStartRequest" },
    { "$ref": "#/$defs/taskIDRequest" },
    { "$ref": "#/$defs/tasksListRequest" },
    { "$ref": "#/$defs/eventsSubscribeRequest" },
    { "$ref": "#/$defs/artifactsListRequest" },
    { "$ref": "#/$defs/artifactReadRequest" },
    { "$ref": "#/$defs/response" },
    { "$ref": "#/$defs/error" },
    { "$ref": "urn:unit-test-ide:protocol:v1.1:event" }
  ],
  "$defs": {
    "base": {
      "type": "object",
      "properties": {
        "protocolVersion": { "const": "1.1" },
        "messageId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
        "sentAt": { "type": "string", "format": "date-time" }
      },
      "required": ["protocolVersion", "messageId", "sentAt"]
    },
    "handshakeRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "request" },
            "method": { "const": "handshake" },
            "payload": {
              "type": "object",
              "additionalProperties": false,
              "required": ["token", "clientName", "clientVersion", "supportedProtocolVersions"],
              "properties": {
                "token": { "type": "string", "minLength": 16 },
                "clientName": { "type": "string", "minLength": 1 },
                "clientVersion": { "type": "string", "minLength": 1 },
                "supportedProtocolVersions": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "enum": ["1.1", "1.0"] } }
              }
            }
          },
          "required": ["kind", "method", "payload"]
        }
      ],
      "unevaluatedProperties": false
    },
    "emptyRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "enum": ["capabilities/get", "shutdown"] }, "payload": { "type": "object", "maxProperties": 0 } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "taskStartRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "request" },
            "method": { "const": "tasks/start" },
            "payload": {
              "type": "object",
              "additionalProperties": false,
              "required": ["idempotencyKey", "scenario", "timeoutMs"],
              "properties": {
                "idempotencyKey": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
                "scenario": { "enum": ["success", "exit-nonzero", "hang", "spawn-child", "emit-output"] },
                "timeoutMs": { "type": "integer", "minimum": 1, "maximum": 86400000 }
              }
            }
          },
          "required": ["kind", "method", "payload"]
        }
      ],
      "unevaluatedProperties": false
    },
    "taskIDRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "enum": ["tasks/get", "tasks/cancel"] }, "payload": { "type": "object", "additionalProperties": false, "required": ["taskId"], "properties": { "taskId": { "type": "string", "pattern": "^[0-9a-f]{32}$" } } } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "tasksListRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "const": "tasks/list" }, "payload": { "type": "object", "additionalProperties": false, "properties": { "cursor": { "type": "string", "minLength": 1 }, "limit": { "type": "integer", "minimum": 1, "maximum": 200 } } } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "eventsSubscribeRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "const": "events/subscribe" }, "payload": { "type": "object", "additionalProperties": false, "required": ["afterSequence"], "properties": { "afterSequence": { "type": "integer", "minimum": 0 } } } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "artifactsListRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "const": "artifacts/list" }, "payload": { "type": "object", "additionalProperties": false, "required": ["taskId"], "properties": { "taskId": { "type": "string", "pattern": "^[0-9a-f]{32}$" }, "cursor": { "type": "string", "minLength": 1 }, "limit": { "type": "integer", "minimum": 1, "maximum": 200 } } } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "artifactReadRequest": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "request" }, "method": { "const": "artifacts/read" }, "payload": { "type": "object", "additionalProperties": false, "required": ["artifactId", "offset", "length"], "properties": { "artifactId": { "type": "string", "pattern": "^[0-9a-f]{32}$" }, "offset": { "type": "integer", "minimum": 0 }, "length": { "type": "integer", "minimum": 1, "maximum": 65536 } } } }, "required": ["kind", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "response": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        { "type": "object", "properties": { "kind": { "const": "response" }, "requestId": { "type": "string", "pattern": "^[0-9a-f]{32}$" }, "method": { "enum": ["handshake", "capabilities/get", "shutdown", "tasks/start", "tasks/get", "tasks/list", "tasks/cancel", "events/subscribe", "artifacts/list", "artifacts/read"] }, "payload": { "type": "object" } }, "required": ["kind", "requestId", "method", "payload"] }
      ],
      "unevaluatedProperties": false
    },
    "error": {
      "allOf": [
        { "$ref": "#/$defs/base" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "error" },
            "requestId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
            "error": {
              "type": "object",
              "additionalProperties": false,
              "required": ["code", "message", "retryable"],
              "properties": {
                "code": { "enum": ["INVALID_MESSAGE", "UNSUPPORTED_PROTOCOL", "AUTH_REQUIRED", "AUTH_FAILED", "METHOD_NOT_FOUND", "INVALID_TASK_SPEC", "TASK_NOT_FOUND", "IDEMPOTENCY_CONFLICT", "EVENT_CURSOR_INVALID", "ARTIFACT_NOT_FOUND", "ARTIFACT_NOT_READY", "STORAGE_UNAVAILABLE", "SERVICE_UNHEALTHY", "SUBSCRIBER_TOO_SLOW", "PROTOCOL_FEATURE_UNAVAILABLE"] },
                "message": { "type": "string" },
                "retryable": { "type": "boolean" }
              }
            }
          },
          "required": ["kind", "requestId", "error"]
        }
      ],
      "unevaluatedProperties": false
    }
  }
}
```

任何请求 Schema 都不得出现 `command`、`shell`、`executable`、`args`、`environment` 或 `workingDirectory`。

- [ ] **Step 4：添加固定的有效与无效 fixtures**

`task-start.valid.json`：

```json
{
  "protocolVersion": "1.1",
  "kind": "request",
  "messageId": "0123456789abcdef0123456789abcdef",
  "method": "tasks/start",
  "sentAt": "2026-07-22T00:00:00Z",
  "payload": {
    "idempotencyKey": "fedcba9876543210fedcba9876543210",
    "scenario": "spawn-child",
    "timeoutMs": 30000
  }
}
```

`task-start-shell.invalid.json`：

```json
{
  "protocolVersion": "1.1",
  "kind": "request",
  "messageId": "0123456789abcdef0123456789abcdef",
  "method": "tasks/start",
  "sentAt": "2026-07-22T00:00:00Z",
  "payload": {
    "idempotencyKey": "fedcba9876543210fedcba9876543210",
    "scenario": "spawn-child",
    "timeoutMs": 30000,
    "shell": "rm -rf /"
  }
}
```

`handshake.valid.json`：

```json
{
  "protocolVersion": "1.1",
  "kind": "request",
  "messageId": "0123456789abcdef0123456789abcdef",
  "method": "handshake",
  "sentAt": "2026-07-22T00:00:00Z",
  "payload": {
    "token": "0123456789abcdef",
    "clientName": "schema-test",
    "clientVersion": "0.2.0",
    "supportedProtocolVersions": ["1.1", "1.0"]
  }
}
```

`event-task-started.valid.json`：

```json
{
  "protocolVersion": "1.1",
  "kind": "event",
  "messageId": "fedcba9876543210fedcba9876543210",
  "sentAt": "2026-07-22T00:00:01Z",
  "sequence": 2,
  "event": "task.started",
  "taskId": "0123456789abcdef0123456789abcdef",
  "payloadVersion": 1,
  "payload": {}
}
```

- [ ] **Step 5：扩展 package exports 和确定性生成器**

在 `packages/protocol-schema/package.json` 增加：

```json
"./v1.1/message": "./schema/v1.1/message.schema.json",
"./v1.1/capabilities": "./schema/v1.1/capabilities.schema.json",
"./v1.1/task": "./schema/v1.1/task.schema.json",
"./v1.1/event": "./schema/v1.1/event.schema.json",
"./v1.1/artifact": "./schema/v1.1/artifact.schema.json"
```

把 `tools/protocol-gen/generate.mjs` 的单一 schema 常量改为：

```js
const models = [
  { directory: "v1", schema: "capabilities.schema.json", top: "Capabilities", ts: "capabilities.ts", go: "generated.go" },
  { directory: "v1.1", schema: "capabilities.schema.json", top: "CapabilitiesV11", ts: "capabilities-v1-1.ts", go: "capabilities_v11_generated.go" },
  { directory: "v1.1", schema: "task.schema.json", top: "TaskSnapshot", ts: "task.ts", go: "task_generated.go" },
  { directory: "v1.1", schema: "event.schema.json", top: "TaskEvent", ts: "event.ts", go: "event_generated.go" },
  { directory: "v1.1", schema: "artifact.schema.json", top: "ArtifactMetadata", ts: "artifact.ts", go: "artifact_generated.go" }
];
```

让生成循环为每个 model 创建 TypeScript 和 Go 两个目标；schema 路径使用 `join(root, "packages/protocol-schema/schema", model.directory, model.schema)`。check 模式继续逐文件比较规范化内容。更新 `packages/protocol-models/src/index.ts`，精确导出五个生成类型。

- [ ] **Step 6：生成模型并验证契约**

运行：

```sh
pnpm generate:protocol
pnpm check:protocol-generated
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
```

预期：全部 PASS；第二次生成不产生 Git 差异；生成契约测试可构造一个 `finished/cancelled` 的 `TaskSnapshot`、一个 `task.finished` 的 `TaskEvent` 和一个摘要制品。

- [ ] **Step 7：提交协议 Schema 和生成模型**

```sh
git add packages/protocol-schema packages/protocol-models tools/protocol-gen apps/test-service/internal/protocolmodel
git commit -m "feat: define protocol 1.1 task contracts"
```

## Task 2：实现协议版本协商与 `1.0` 兼容

**文件：**

- 修改：`apps/test-service/internal/protocol/envelope.go`
- 修改：`apps/test-service/internal/protocol/envelope_test.go`
- 修改：`apps/test-service/internal/session/session.go`
- 修改：`apps/test-service/internal/session/session_test.go`

**接口：**

- 输入：Task 1 生成的 `protocolmodel.CapabilitiesV11`。
- 产出：`protocol.Version10`、`protocol.Version11`、显式版本的 response/error 构造器、`protocol.NewEvent`，以及 Session 的 `NegotiatedVersion()`。

- [ ] **Step 1：编写协商与降级测试**

在 `session_test.go` 增加 `requestVersion` 辅助函数和以下主测试：

```go
func TestSessionNegotiatesV11AndKeepsV10Shape(t *testing.T) {
	v11 := session.New("0123456789abcdef", "windows", "named-pipe", nil)
	accepted := v11.Handle(context.Background(), requestVersion(t, "1.1", "handshake", map[string]any{
		"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.2.0",
		"supportedProtocolVersions": []string{"1.1", "1.0"},
	}))
	if accepted.Response.ProtocolVersion != "1.1" || v11.NegotiatedVersion() != "1.1" {
		t.Fatalf("v1.1 negotiation failed: %#v", accepted.Response)
	}

	v10 := session.New("0123456789abcdef", "linux", "unix-socket", nil)
	legacy := v10.Handle(context.Background(), requestVersion(t, "1.0", "handshake", map[string]string{
		"token": "0123456789abcdef", "clientName": "legacy", "clientVersion": "0.1.0",
	}))
	if legacy.Response.ProtocolVersion != "1.0" || v10.NegotiatedVersion() != "1.0" {
		t.Fatalf("v1.0 negotiation failed: %#v", legacy.Response)
	}
}
```

同时验证：未知 envelope 版本返回 `UNSUPPORTED_PROTOCOL`；handshake 后使用不同版本返回 `UNSUPPORTED_PROTOCOL`；`1.0` 请求 Phase 2 方法返回 `PROTOCOL_FEATURE_UNAVAILABLE`；序列化的 `1.0` capabilities 中不存在新字段。

- [ ] **Step 2：运行 Go 测试并确认新 API 尚不存在**

运行：`go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session`

预期：FAIL，编译错误包含 `undefined: protocol.Version11`、`too many arguments in call to session.New` 或 `NegotiatedVersion undefined`。

- [ ] **Step 3：使 envelope 显式携带响应版本**

在 `envelope.go` 中加入并替换原有构造函数：

```go
const (
	Version10 = "1.0"
	Version11 = "1.1"
	Version   = Version10
)

func SupportedVersion(version string) bool {
	return version == Version10 || version == Version11
}

func Success(version string, request Request, payload any) Response {
	return Response{ProtocolVersion: version, Kind: "response", MessageID: newID(), RequestID: request.MessageID, Method: request.Method, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func Failure(version string, request Request, code, message string, retryable bool) Response {
	if !SupportedVersion(version) { version = Version10 }
	return Response{ProtocolVersion: version, Kind: "error", MessageID: newID(), RequestID: request.MessageID, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Error: &ErrorBody{Code: code, Message: message, Retryable: retryable}}
}

type Event struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Kind            string          `json:"kind"`
	MessageID       string          `json:"messageId"`
	SentAt          string          `json:"sentAt"`
	Sequence        int64           `json:"sequence"`
	Event           string          `json:"event"`
	TaskID          string          `json:"taskId"`
	PayloadVersion  int             `json:"payloadVersion"`
	Payload         json.RawMessage `json:"payload"`
}

func NewEvent(sequence int64, event, taskID string, at time.Time, payload json.RawMessage) Event {
	return Event{ProtocolVersion: Version11, Kind: "event", MessageID: newID(), SentAt: at.UTC().Format(time.RFC3339Nano), Sequence: sequence, Event: event, TaskID: taskID, PayloadVersion: 1, Payload: payload}
}
```

更新所有 Phase 1 调用点并保留 `Version` 别名，使旧测试辅助代码继续编译。

- [ ] **Step 4：实现严格 handshake 协商**

Session 增加 `negotiatedVersion string`。`handshake` payload 和协商函数固定为：

```go
type handshake struct {
	Token                     string   `json:"token"`
	ClientName                string   `json:"clientName"`
	ClientVersion             string   `json:"clientVersion"`
	SupportedProtocolVersions []string `json:"supportedProtocolVersions,omitempty"`
}

func negotiate(envelopeVersion string, supported []string) (string, bool) {
	if envelopeVersion == protocol.Version10 && len(supported) == 0 {
		return protocol.Version10, true
	}
	for _, candidate := range []string{protocol.Version11, protocol.Version10} {
		if candidate != envelopeVersion { continue }
		for _, offered := range supported {
			if offered == candidate { return candidate, true }
		}
	}
	return "", false
}
```

`decodeHandshake(raw, version)` 对 `1.0` 继续拒绝额外字段；对 `1.1` 要求版本列表非空。Handshake 完成后的每条请求必须与 `negotiatedVersion` 相同。

- [ ] **Step 5：按协商版本返回严格能力对象**

`1.0` 返回现有 `protocolmodel.Capabilities`。`1.1` 返回：

```go
protocolmodel.CapabilitiesV11{
	Platform: s.platform,
	Transports: []string{s.transport},
	Toolchains: []string{},
	Frameworks: []string{},
	CoverageTools: []string{},
	TaskExecution: true,
	EventReplay: true,
	SQLiteHistory: true,
	ArtifactRead: true,
	ProcessTreeControl: map[string]string{"windows": "job-object", "linux": "process-group"}[s.platform],
}
```

给 `session.New` 增加最后一个 `TaskAPI` 参数；本任务先允许传 `nil`。`1.1` Phase 2 方法在依赖为空时返回 `SERVICE_UNHEALTHY`，`1.0` 则返回 `PROTOCOL_FEATURE_UNAVAILABLE`。

- [ ] **Step 6：运行协议与 Session 回归测试**

运行：`go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session`

预期：PASS，现有身份验证、未知方法、严格 payload 和 shutdown 测试继续通过。

- [ ] **Step 7：提交版本协商**

```sh
git add apps/test-service/internal/protocol apps/test-service/internal/session
git commit -m "feat: negotiate protocol 1.1 compatibly"
```

## Task 3：实现任务领域模型与状态机

**文件：**

- 创建：`apps/test-service/internal/task/model.go`
- 创建：`apps/test-service/internal/task/state.go`
- 创建：`apps/test-service/internal/task/state_test.go`
- 创建：`apps/test-service/internal/task/ports.go`

**接口：**

- 输入：Task 1 的协议命名和设计规格中的生命周期规则。
- 产出：`task.Task`、`task.Event`、`task.Artifact`、`task.ProcessLease`、`task.Transition`、`task.Store`、`task.Publisher` 和纯函数 `task.ApplyTransition(current, transition)`。

- [ ] **Step 1：编写完整状态迁移表测试**

`state_test.go` 使用固定时间并覆盖：

```go
func TestApplyTransitionTable(t *testing.T) {
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		from task.Status
		to task.Status
		outcome task.Outcome
		wantErr bool
	}{
		{"queued starts", task.StatusQueued, task.StatusRunning, "", false},
		{"queued cancels", task.StatusQueued, task.StatusFinished, task.OutcomeCancelled, false},
		{"running cancels", task.StatusRunning, task.StatusCancelling, "", false},
		{"running succeeds", task.StatusRunning, task.StatusFinished, task.OutcomeSucceeded, false},
		{"running times out", task.StatusRunning, task.StatusFinished, task.OutcomeTimedOut, false},
		{"cancelling finishes", task.StatusCancelling, task.StatusFinished, task.OutcomeCancelled, false},
		{"finished is immutable", task.StatusFinished, task.StatusRunning, "", true},
		{"nonterminal has no outcome", task.StatusQueued, task.StatusRunning, task.OutcomeSucceeded, true},
		{"finished requires outcome", task.StatusRunning, task.StatusFinished, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := task.Task{ID: "0123456789abcdef0123456789abcdef", Status: tt.from, CreatedAt: now}
			_, err := task.ApplyTransition(current, task.Transition{From: tt.from, To: tt.to, Outcome: tt.outcome, At: now.Add(time.Second)})
			if (err != nil) != tt.wantErr { t.Fatalf("err = %v, wantErr %v", err, tt.wantErr) }
		})
	}
}
```

另加测试确保 `command_failed` 与 `infrastructure_failed` 是不同结果，并遍历所有 Outcome 断言不存在 `test_failed`。

- [ ] **Step 2：运行测试并确认 task 包缺失**

运行：`go test ./apps/test-service/internal/task`

预期：FAIL，错误包含 `no required module provides package` 或 `undefined: task.Status`。

- [ ] **Step 3：定义领域类型**

`model.go` 的公共模型固定为：

```go
package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Status string
type Outcome string
type Scenario string
type EventType string

const (
	StatusQueued Status = "queued"
	StatusRunning Status = "running"
	StatusCancelling Status = "cancelling"
	StatusFinished Status = "finished"
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeCommandFailed Outcome = "command_failed"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeTimedOut Outcome = "timed_out"
	OutcomeInterrupted Outcome = "interrupted"
	OutcomeInfrastructureFailed Outcome = "infrastructure_failed"
	ScenarioSuccess Scenario = "success"
	ScenarioExitNonzero Scenario = "exit-nonzero"
	ScenarioHang Scenario = "hang"
	ScenarioSpawnChild Scenario = "spawn-child"
	ScenarioEmitOutput Scenario = "emit-output"
	EventTaskCreated EventType = "task.created"
	EventTaskStarted EventType = "task.started"
	EventTaskOutput EventType = "task.output"
	EventTaskCancellationRequested EventType = "task.cancellation_requested"
	EventTaskFinished EventType = "task.finished"
	EventArtifactCreated EventType = "artifact.created"
)

type Task struct {
	ID, IdempotencyKey, RequestHash string
	Scenario Scenario
	Timeout time.Duration
	Status Status
	Outcome Outcome
	CreatedAt time.Time
	StartedAt, FinishedAt *time.Time
	LastSequence int64
	ErrorCode, ErrorMessage string
}

type EventDraft struct { TaskID string; Type EventType; At time.Time; Payload json.RawMessage }
type Event struct { Sequence int64; ID string; EventDraft }
type Artifact struct { ID, TaskID, Kind, RelativePath, MIMEType, SHA256 string; Size int64; CreatedAt time.Time }
type ProcessLease struct { TaskID string; HostPID int; HostStartIdentity string; TargetProcessGroup int; ServiceInstanceID string }
type Page[T any] struct { Items []T; NextCursor string }

func NewID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil { panic(err) }
	return hex.EncodeToString(value[:])
}

func ValidScenario(value Scenario) bool {
	switch value {
	case ScenarioSuccess, ScenarioExitNonzero, ScenarioHang, ScenarioSpawnChild, ScenarioEmitOutput:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4：实现纯状态机**

`state.go` 定义 `Transition` 并完整校验允许边：

```go
package task

import (
	"errors"
	"fmt"
	"time"
)

type Transition struct { From, To Status; Outcome Outcome; At time.Time; ErrorCode, ErrorMessage string }

func ApplyTransition(current Task, change Transition) (Task, error) {
	if current.Status != change.From { return Task{}, fmt.Errorf("state conflict: have %s, expected %s", current.Status, change.From) }
	allowed := map[Status]map[Status]bool{
		StatusQueued: {StatusRunning: true, StatusFinished: true},
		StatusRunning: {StatusCancelling: true, StatusFinished: true},
		StatusCancelling: {StatusFinished: true},
		StatusFinished: {},
	}
	if !allowed[change.From][change.To] { return Task{}, fmt.Errorf("invalid transition %s -> %s", change.From, change.To) }
	if change.To == StatusFinished && !validOutcome(change.Outcome) { return Task{}, errors.New("finished task requires valid outcome") }
	if change.To != StatusFinished && change.Outcome != "" { return Task{}, errors.New("nonterminal task cannot have outcome") }
	next := current
	next.Status, next.Outcome = change.To, change.Outcome
	next.ErrorCode, next.ErrorMessage = change.ErrorCode, change.ErrorMessage
	if change.To == StatusRunning { at := change.At; next.StartedAt = &at }
	if change.To == StatusFinished { at := change.At; next.FinishedAt = &at }
	return next, nil
}

func validOutcome(value Outcome) bool {
	switch value {
	case OutcomeSucceeded, OutcomeCommandFailed, OutcomeCancelled, OutcomeTimedOut, OutcomeInterrupted, OutcomeInfrastructureFailed:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 5：锁定存储和事件端口**

`ports.go` 使用以下签名，后续任务不得自行重命名：

```go
package task

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("state conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrStorageUnavailable = errors.New("storage unavailable")
)

type Mutation struct {
	Task Task
	Expected Status
	Events []EventDraft
	PutLease *ProcessLease
	DeleteLease bool
	Artifacts []Artifact
}

type Store interface {
	Create(context.Context, Task, EventDraft) (Task, []Event, error)
	FindByIdempotencyKey(context.Context, string) (Task, error)
	Get(context.Context, string) (Task, error)
	List(context.Context, string, int) (Page[Task], error)
	Apply(context.Context, Mutation) (Task, []Event, error)
	AppendEvent(context.Context, string, EventDraft) (Event, error)
	UpdateLease(context.Context, ProcessLease) error
	Watermark(context.Context) (int64, error)
	EventsAfter(context.Context, int64, int64, int) ([]Event, error)
	ListArtifacts(context.Context, string, string, int) (Page[Artifact], error)
	GetArtifact(context.Context, string) (Artifact, error)
	ActiveLeases(context.Context) ([]ProcessLease, error)
	RecoverInterrupted(context.Context, time.Time) ([]Event, error)
	ReferencedArtifactPaths(context.Context) (map[string]struct{}, error)
	Close() error
}

type Publisher interface { Publish(Event) }
```

- [ ] **Step 6：运行状态机测试与格式检查**

运行：

```sh
gofmt -w apps/test-service/internal/task
go test ./apps/test-service/internal/task
go vet ./apps/test-service/internal/task
```

预期：全部 PASS。

- [ ] **Step 7：提交领域模型**

```sh
git add apps/test-service/internal/task
git commit -m "feat: add task lifecycle model"
```

## Task 4：实现 SQLite 事务存储与启动恢复

**文件：**

- 修改：`apps/test-service/go.mod`
- 修改：`apps/test-service/go.sum`
- 创建：`apps/test-service/internal/taskstore/migrations/001_initial.sql`
- 创建：`apps/test-service/internal/taskstore/migrations.go`
- 创建：`apps/test-service/internal/taskstore/sqlite.go`
- 创建：`apps/test-service/internal/taskstore/tasks.go`
- 创建：`apps/test-service/internal/taskstore/events.go`
- 创建：`apps/test-service/internal/taskstore/artifacts.go`
- 创建：`apps/test-service/internal/taskstore/recovery.go`
- 创建：`apps/test-service/internal/taskstore/sqlite_test.go`

**接口：**

- 输入：Task 3 的 `task.Store` 接口。
- 产出：`taskstore.Open(path string) (*Store, error)`，完整实现 `task.Store`；每次状态变化、事件、租约和制品元数据在单个 SQLite 事务中提交。

- [ ] **Step 1：编写事务、幂等和恢复失败测试**

在 `sqlite_test.go` 创建临时数据库并至少实现以下主断言：

```go
func TestStoreCommitsSnapshotAndEventAtomically(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	created, events, err := store.Create(ctx, task.Task{
		ID: id(1), IdempotencyKey: id(2), RequestHash: strings.Repeat("a", 64),
		Scenario: task.ScenarioHang, Timeout: 30 * time.Second,
		Status: task.StatusQueued, CreatedAt: now,
	}, task.EventDraft{TaskID: id(1), Type: "task.created", At: now, Payload: json.RawMessage(`{}`)})
	if err != nil { t.Fatal(err) }
	if created.LastSequence != events[0].Sequence || events[0].Sequence != 1 { t.Fatalf("created = %#v, events = %#v", created, events) }

	running, started, err := store.Apply(ctx, task.Mutation{
		Task: mustTransition(t, created, task.Transition{From: task.StatusQueued, To: task.StatusRunning, At: now.Add(time.Second)}),
		Expected: task.StatusQueued,
		Events: []task.EventDraft{{TaskID: id(1), Type: "task.started", At: now.Add(time.Second), Payload: json.RawMessage(`{}`)}},
		PutLease: &task.ProcessLease{TaskID: id(1), HostPID: 42, HostStartIdentity: "100", ServiceInstanceID: id(3)},
	})
	if err != nil { t.Fatal(err) }
	if running.Status != task.StatusRunning || started[0].Sequence != 2 || running.LastSequence != 2 { t.Fatalf("running = %#v, events = %#v", running, started) }
}
```

再增加测试：相同幂等键同 hash 返回原任务、不同 hash 返回 `task.ErrIdempotencyConflict`；错误 expected status 回滚事件；分页游标稳定；`EventsAfter` 严格按 sequence；`RecoverInterrupted` 为所有活动任务追加 `task.finished` 并删除租约；重复迁移不改变数据库。

- [ ] **Step 2：运行测试并确认存储包缺失**

运行：`go test ./apps/test-service/internal/taskstore`

预期：FAIL，错误包含 `no required module provides package` 或 `undefined: taskstore.Open`。

- [ ] **Step 3：锁定无 CGO SQLite 依赖**

运行：

```sh
cd apps/test-service
go get modernc.org/sqlite@v1.54.0 modernc.org/libc@v1.74.1 golang.org/x/sys@v0.46.0
go mod tidy
cd ../..
```

预期：`go.mod` 直接固定 `modernc.org/sqlite v1.54.0`、`modernc.org/libc v1.74.1` 和 `golang.org/x/sys v0.46.0`；不出现 CGO 驱动。

- [ ] **Step 4：添加初始数据库迁移**

`001_initial.sql` 使用以下完整表和索引：

```sql
CREATE TABLE tasks (
  task_id TEXT PRIMARY KEY,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_hash TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind = 'simulation'),
  scenario TEXT NOT NULL,
  timeout_ms INTEGER NOT NULL CHECK (timeout_ms BETWEEN 1 AND 86400000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','cancelling','finished')),
  outcome TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  CHECK ((status = 'finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL) OR
         (status <> 'finished' AND outcome IS NULL AND finished_at IS NULL))
);

CREATE TABLE task_events (
  sequence INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL UNIQUE,
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  event_type TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  payload_version INTEGER NOT NULL DEFAULT 1,
  payload_json TEXT NOT NULL CHECK (json_valid(payload_json))
);

CREATE INDEX task_events_task_sequence ON task_events(task_id, sequence);
CREATE INDEX tasks_history_order ON tasks(created_at DESC, task_id DESC);

CREATE TABLE artifacts (
  artifact_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL REFERENCES tasks(task_id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  relative_path TEXT NOT NULL UNIQUE,
  mime_type TEXT NOT NULL,
  size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
  created_at TEXT NOT NULL,
  complete INTEGER NOT NULL CHECK (complete = 1)
);

CREATE INDEX artifacts_task_order ON artifacts(task_id, created_at, artifact_id);

CREATE TABLE process_leases (
  task_id TEXT PRIMARY KEY REFERENCES tasks(task_id) ON DELETE CASCADE,
  host_pid INTEGER NOT NULL,
  host_start_identity TEXT NOT NULL,
  target_process_group INTEGER NOT NULL DEFAULT 0,
  service_instance_id TEXT NOT NULL
);
```

`migrations.go` 通过 `//go:embed migrations/*.sql` 读取文件；`schema_migrations(version INTEGER PRIMARY KEY, sha256 TEXT NOT NULL, applied_at TEXT NOT NULL)` 由 bootstrap 创建。每个 migration 的 SQL 与对应 schema_migrations 行必须在同一事务中提交，任何语句失败都完整回滚。已应用版本的摘要不一致时返回错误，不允许静默继续。

- [ ] **Step 5：实现安全打开和事务辅助函数**

`sqlite.go` 的入口固定为：

```go
package taskstore

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

type Store struct { db *sql.DB; newID func() string }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil { return nil, fmt.Errorf("open task history: %w", err) }
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=FULL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil { db.Close(); return nil, fmt.Errorf("configure task history: %w", err) }
	}
	store := &Store{db: db, newID: task.NewID}
	if err := store.migrate(context.Background()); err != nil { db.Close(); return nil, err }
	return store, nil
}

func (s *Store) Close() error { return s.db.Close() }
```

所有公开存储错误都用 `%w` 包装 `task.ErrStorageUnavailable`；未找到、状态冲突和幂等冲突映射为 Task 3 的稳定 sentinel errors。

- [ ] **Step 6：实现创建、迁移和事件查询事务**

`Create` 和 `Apply` 都使用 `BeginTx`。`UpdateLease` 只允许更新已有活动任务的同一 `task_id` 租约，并在找不到租约时返回 `task.ErrConflict`。`Apply` 的核心顺序固定为：

```go
func (s *Store) Apply(ctx context.Context, mutation task.Mutation) (task.Task, []task.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return task.Task{}, nil, storageError(err) }
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE tasks SET status=?, outcome=?, started_at=?, finished_at=?, error_code=?, error_message=? WHERE task_id=? AND status=?`,
		string(mutation.Task.Status), nullableOutcome(mutation.Task), nullableTime(mutation.Task.StartedAt), nullableTime(mutation.Task.FinishedAt), mutation.Task.ErrorCode, mutation.Task.ErrorMessage, mutation.Task.ID, string(mutation.Expected))
	if err != nil { return task.Task{}, nil, storageError(err) }
	affected, err := result.RowsAffected()
	if err != nil { return task.Task{}, nil, storageError(err) }
	if affected != 1 { return task.Task{}, nil, task.ErrConflict }
	events, err := insertEvents(ctx, tx, mutation.Events, s.newID)
	if err != nil { return task.Task{}, nil, err }
	if mutation.PutLease != nil { if err := upsertLease(ctx, tx, *mutation.PutLease); err != nil { return task.Task{}, nil, err } }
	if mutation.DeleteLease { if _, err := tx.ExecContext(ctx, `DELETE FROM process_leases WHERE task_id=?`, mutation.Task.ID); err != nil { return task.Task{}, nil, storageError(err) } }
	for _, artifact := range mutation.Artifacts { if err := insertArtifact(ctx, tx, artifact); err != nil { return task.Task{}, nil, err } }
	last := mutation.Task.LastSequence
	if len(events) > 0 { last = events[len(events)-1].Sequence }
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_sequence=? WHERE task_id=?`, last, mutation.Task.ID); err != nil { return task.Task{}, nil, storageError(err) }
	mutation.Task.LastSequence = last
	if err := tx.Commit(); err != nil { return task.Task{}, nil, storageError(err) }
	return mutation.Task, events, nil
}

func nullableOutcome(value task.Task) any {
	if value.Status != task.StatusFinished { return nil }
	return string(value.Outcome)
}

func nullableTime(value *time.Time) any {
	if value == nil { return nil }
	return value.UTC().Format(time.RFC3339Nano)
}
```

`EventsAfter(ctx, after, through, limit)` 必须执行 `WHERE sequence > ? AND sequence <= ? ORDER BY sequence LIMIT ?`。列表游标是 Base64URL 编码的 `createdAt + "\n" + taskID`，页大小限制为 `1..200`。

- [ ] **Step 7：实现幂等创建和恢复**

`Create` 遇到唯一键冲突时读取原任务并比较 `request_hash`：相同则返回原任务且不追加事件，不同则返回 `task.ErrIdempotencyConflict`。

`RecoverInterrupted(ctx, at)` 在一个事务中选择 `queued/running/cancelling` 任务，逐个更新为 `finished/interrupted`、插入 `task.finished`、更新 `last_sequence`，最后删除对应租约并按 sequence 返回新事件。若事务中任何一步失败，所有任务保持原状态。

- [ ] **Step 8：运行存储测试和竞态检测**

运行：

```sh
gofmt -w apps/test-service/internal/taskstore
go test ./apps/test-service/internal/taskstore
go test -race ./apps/test-service/internal/taskstore
```

预期：全部 PASS；临时目录中生成的数据库通过 `PRAGMA integrity_check` 返回 `ok`。

- [ ] **Step 9：提交 SQLite 存储**

```sh
git add apps/test-service/go.mod apps/test-service/go.sum apps/test-service/internal/taskstore
git commit -m "feat: persist task history in sqlite"
```

## Task 5：实现原子制品存储与安全分块读取

**文件：**

- 创建：`apps/test-service/internal/artifactstore/store.go`
- 创建：`apps/test-service/internal/artifactstore/store_test.go`
- 创建：`apps/test-service/internal/artifactstore/path_unix.go`
- 创建：`apps/test-service/internal/artifactstore/path_windows.go`

**接口：**

- 输入：Task 3 的 `task.Artifact`；Task 4 的 `ReferencedArtifactPaths`。
- 产出：`artifactstore.New(root)`、`CommitJSON`、`ReadChunk` 和 `Cleanup`。文件落盘与数据库事务分开，数据库失败留下的无主文件由启动清理删除。

- [ ] **Step 1：编写原子提交、摘要、越界和分块测试**

`store_test.go` 至少包含：

```go
func TestCommitJSONAndReadChunk(t *testing.T) {
	root := t.TempDir()
	store, err := artifactstore.New(root)
	if err != nil { t.Fatal(err) }
	artifact, err := store.CommitJSON(context.Background(), id(1), id(2), time.Unix(0, 0).UTC(), map[string]string{"outcome": "cancelled"})
	if err != nil { t.Fatal(err) }
	if artifact.Kind != "task-summary" || artifact.MIMEType != "application/json" || len(artifact.SHA256) != 64 { t.Fatalf("artifact = %#v", artifact) }
	first, next, eof, err := store.ReadChunk(context.Background(), artifact, 0, 8)
	if err != nil || len(first) != 8 || next != 8 || eof { t.Fatalf("chunk = %q, next = %d, eof = %v, err = %v", first, next, eof, err) }
	rest, _, eof, err := store.ReadChunk(context.Background(), artifact, next, 64*1024)
	if err != nil || !eof { t.Fatalf("rest = %q, eof = %v, err = %v", rest, eof, err) }
}
```

另加测试：负 offset、超过 64 KiB 的请求长度、伪造 `../` 相对路径、绝对路径、临时文件清理、无主完成文件清理，以及平台支持时的 symlink/reparse point 拒绝。

- [ ] **Step 2：运行测试并确认包缺失**

运行：`go test ./apps/test-service/internal/artifactstore`

预期：FAIL，错误包含 `undefined: artifactstore.New`。

- [ ] **Step 3：实现确定性 JSON 和同目录原子重命名**

`store.go` 的公共接口和提交顺序：

```go
const MaxReadChunk = 64 * 1024

type Store struct { root string }

func New(root string) (*Store, error) {
	absolute, err := filepath.Abs(root)
	if err != nil { return nil, err }
	if err := os.MkdirAll(absolute, 0o700); err != nil { return nil, err }
	return &Store{root: absolute}, nil
}

func (s *Store) CommitJSON(ctx context.Context, taskID, artifactID string, at time.Time, value any) (task.Artifact, error) {
	data, err := json.Marshal(value)
	if err != nil { return task.Artifact{}, err }
	data = append(data, '\n')
	relative := filepath.Join("tasks", taskID, artifactID+".json")
	target, err := s.safeTarget(relative, true)
	if err != nil { return task.Artifact{}, err }
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil { return task.Artifact{}, err }
	temporary, err := os.CreateTemp(filepath.Dir(target), ".artifact-*.tmp")
	if err != nil { return task.Artifact{}, err }
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil { temporary.Close(); return task.Artifact{}, err }
	if _, err := temporary.Write(data); err != nil { temporary.Close(); return task.Artifact{}, err }
	if err := temporary.Sync(); err != nil { temporary.Close(); return task.Artifact{}, err }
	if err := temporary.Close(); err != nil { return task.Artifact{}, err }
	if err := os.Rename(temporaryName, target); err != nil { return task.Artifact{}, err }
	if err := syncDirectory(filepath.Dir(target)); err != nil { return task.Artifact{}, err }
	sum := sha256.Sum256(data)
	return task.Artifact{ID: artifactID, TaskID: taskID, Kind: "task-summary", RelativePath: relative, MIMEType: "application/json", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), CreatedAt: at}, nil
}
```

JSON 摘要结构只使用字符串、整数和固定字段顺序；不得包含绝对路径或环境变量。

- [ ] **Step 4：实现安全路径检查和分块读取**

`safeTarget(relative, allowMissingLeaf)` 必须：拒绝空值、绝对路径和 `..`；用 `filepath.Rel` 确认最终路径仍在 root；逐段打开或 `Lstat`，拒绝 Unix symlink 和 Windows reparse point。

`ReadChunk` 固定行为：

```go
func (s *Store) ReadChunk(ctx context.Context, artifact task.Artifact, offset int64, length int) ([]byte, int64, bool, error) {
	if offset < 0 || length < 1 || length > MaxReadChunk { return nil, 0, false, ErrInvalidRange }
	path, err := s.safeTarget(artifact.RelativePath, false)
	if err != nil { return nil, 0, false, err }
	file, err := openNoFollow(path)
	if err != nil { return nil, 0, false, err }
	defer file.Close()
	info, err := file.Stat()
	if err != nil { return nil, 0, false, err }
	if info.Size() != artifact.Size { return nil, 0, false, ErrArtifactChanged }
	buffer := make([]byte, length)
	n, err := file.ReadAt(buffer, offset)
	if err != nil && !errors.Is(err, io.EOF) { return nil, 0, false, err }
	next := offset + int64(n)
	return buffer[:n], next, next == artifact.Size, nil
}
```

Unix `openNoFollow` 使用 `unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)`；Windows 使用 `CreateFile` 加 `FILE_FLAG_OPEN_REPARSE_POINT`，检查 `FILE_ATTRIBUTE_REPARSE_POINT` 后再转换为 `*os.File`。

- [ ] **Step 5：实现启动清理**

`Cleanup(ctx, referenced)` 只遍历制品 root：删除 `.artifact-*.tmp`；删除不在 `referenced` 集合中的普通完成文件；遇到 symlink/reparse point 或未知文件类型时返回错误并保持目标不变。目录采用自底向上的空目录清理，不能跟随链接。

- [ ] **Step 6：运行平台测试**

运行：

```sh
gofmt -w apps/test-service/internal/artifactstore
go test ./apps/test-service/internal/artifactstore
go test -race ./apps/test-service/internal/artifactstore
```

预期：当前平台全部 PASS；Windows 测试覆盖 reparse point 检测，Linux CI 覆盖 `O_NOFOLLOW`。

- [ ] **Step 7：提交制品存储**

```sh
git add apps/test-service/internal/artifactstore
git commit -m "feat: store task artifacts atomically"
```

## Task 6：实现无缺口事件重放与慢订阅隔离

**文件：**

- 创建：`apps/test-service/internal/eventbroker/broker.go`
- 创建：`apps/test-service/internal/eventbroker/broker_test.go`

**接口：**

- 输入：Task 3 的 `task.Event`；Task 4 Store 的 `Watermark` 和 `EventsAfter`。
- 产出：`eventbroker.New(source, queueSize, pageSize)`、`Publish(event)`、`Subscribe(ctx, afterSequence)` 和 `Subscription{Events, Errors, Close}`。

- [ ] **Step 1：编写重放/实时边界与慢订阅测试**

用可阻塞 fake source 精确制造“读取水位后、重放完成前提交事件”的竞态：

```go
func TestSubscribeBridgesReplayAndLiveWithoutGap(t *testing.T) {
	source := newFakeSource(events(1, 2))
	broker := eventbroker.New(source, 8, 2)
	subscription, err := broker.Subscribe(context.Background(), 0)
	if err != nil { t.Fatal(err) }
	source.waitForReplayStart(t)
	source.append(event(3))
	broker.Publish(event(3))
	source.releaseReplay()
	if got := readSequences(t, subscription.Events, 3); !reflect.DeepEqual(got, []int64{1, 2, 3}) { t.Fatalf("sequences = %v", got) }
}
```

另加测试：`afterSequence` 等于水位只接收实时事件；负游标或大于当前水位的游标返回 `ErrInvalidCursor`；重放与缓冲重复 sequence 只发送一次；非递增 `Publish` 被忽略并报告内部错误；容量为 1 的慢订阅收到 `ErrSubscriberTooSlow` 并关闭，但快速订阅继续接收。

- [ ] **Step 2：运行测试并确认 broker 缺失**

运行：`go test ./apps/test-service/internal/eventbroker`

预期：FAIL，错误包含 `undefined: eventbroker.New`。

- [ ] **Step 3：实现订阅模型与有界队列**

`broker.go` 锁定接口：

```go
type Source interface {
	Watermark(context.Context) (int64, error)
	EventsAfter(context.Context, int64, int64, int) ([]task.Event, error)
}

var (
	ErrSubscriberTooSlow = errors.New("subscriber too slow")
	ErrInvalidCursor = errors.New("invalid event cursor")
)

type Subscription struct {
	Events <-chan task.Event
	Errors <-chan error
	close func()
}

func (s *Subscription) Close() { s.close() }

type subscriber struct {
	id uint64
	after int64
	buffer []task.Event
	live bool
	out chan task.Event
	errs chan error
	cancel context.CancelFunc
}
```

`Broker.Publish` 在锁内只复制到每个 subscriber 的内存缓冲或非阻塞 channel，不执行数据库或网络 I/O。队列满时移除该 subscriber，非阻塞发送 `ErrSubscriberTooSlow`，关闭其 channel。

`New` 对 `queueSize < 1` 或 `pageSize < 1` 返回错误；Runtime 固定使用 `queueSize=256`、`pageSize=200`。

- [ ] **Step 4：实现水位重放切换**

`Subscribe` 先拒绝负游标；注册 buffering subscriber 后读取 watermark，如果 `afterSequence > watermark` 则移除 subscriber 并返回 `ErrInvalidCursor`。随后分页读取 `(after, watermark]`；发送未见过的 sequence；锁定 subscriber，丢弃 buffer 中 `<= watermark` 的重复项并按 sequence 发送剩余项；设置 `live=true`。所有发送都受 context 和有界队列约束。

核心去重条件：

```go
if event.Sequence <= subscriber.after { continue }
subscriber.after = event.Sequence
select {
case subscriber.out <- event:
case <-ctx.Done(): return
default: broker.dropLocked(subscriber, ErrSubscriberTooSlow); return
}
```

- [ ] **Step 5：运行竞态测试**

运行：

```sh
gofmt -w apps/test-service/internal/eventbroker
go test ./apps/test-service/internal/eventbroker
go test -race ./apps/test-service/internal/eventbroker
```

预期：全部 PASS，无 data race。

- [ ] **Step 6：提交事件 Broker**

```sh
git add apps/test-service/internal/eventbroker
git commit -m "feat: replay committed task events"
```

## Task 7：建立 Process Host 协议与受控模拟进程

**文件：**

- 创建：`apps/test-service/internal/processcontrol/process.go`
- 创建：`apps/test-service/internal/processcontrol/host_protocol.go`
- 创建：`apps/test-service/internal/processhost/host.go`
- 创建：`apps/test-service/internal/processhost/host_test.go`
- 创建：`apps/test-service/internal/taskfixture/fixture.go`
- 创建：`apps/test-service/internal/taskfixture/fixture_test.go`
- 创建：`apps/test-service/cmd/unit-test-service/process_modes_test.go`
- 修改：`apps/test-service/cmd/unit-test-service/main.go`
- 修改：`apps/test-service/cmd/unit-test-service/main_test.go`

**接口：**

- 输入：服务自身可执行文件路径和 Task 3 的 `task.ProcessLease`。
- 产出：稳定的 `processcontrol.Runner`/`Process` 接口、可注入平台实现的 Process Host 状态机、内部 NDJSON Host 控制协议，以及三个互斥内部 CLI 模式。Task 8/9 只需注入各自 Platform/Runner。

- [ ] **Step 1：编写 fixture 与模式隔离测试**

`fixture_test.go` 验证成功、非零和确定性输出：

```go
func TestFixtureScenarios(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := taskfixture.Run(context.Background(), task.ScenarioEmitOutput, &stdout, &stderr); code != 0 { t.Fatalf("code = %d", code) }
	if stdout.String() != "fixture stdout\n" || stderr.String() != "fixture stderr\n" { t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String()) }
	stdout.Reset(); stderr.Reset()
	if code := taskfixture.Run(context.Background(), task.ScenarioExitNonzero, &stdout, &stderr); code != 17 { t.Fatalf("code = %d", code) }
}
```

`process_modes_test.go` 验证 `--process-host` 或 `--task-fixture` 不能与 `--endpoint`、`--token-file`、`--prepare-token-file` 混用；未知场景返回 CLI code 2；Process Host 不读取 token 文件、不创建 IPC。

- [ ] **Step 2：运行测试并确认内部模式不存在**

运行：`go test ./apps/test-service/internal/taskfixture ./apps/test-service/cmd/unit-test-service`

预期：FAIL，错误包含 `package taskfixture is not in std` 或未知 flag。

- [ ] **Step 3：锁定 ProcessRunner 接口和 Host 帧**

`processcontrol/process.go`：

```go
package processcontrol

import (
	"context"
	"time"

	"unit-test-ide.local/test-service/internal/task"
)

type Stream string
const ( StreamStdout Stream = "stdout"; StreamStderr Stream = "stderr" )

type Spec struct { Executable string; Args []string; Dir string; Env []string }
type Output struct { Stream Stream; Data []byte }
type Result struct { ExitCode int; Err error }

type Process interface {
	Lease() task.ProcessLease
	Start(context.Context) error
	Output() <-chan Output
	Done() <-chan Result
	Terminate(context.Context, time.Duration) error
	Close() error
}

type Runner interface {
	Prepare(context.Context, Spec, string, string) (Process, error)
	Cleanup(context.Context, task.ProcessLease, time.Duration) error
}
```

`Prepare` 的最后两个字符串分别是 `taskID` 和 `serviceInstanceID`。它只创建被阻塞的 Process Host；`Start` 才通过匿名控制管道发送目标 `Spec`。

`host_protocol.go`：

```go
type HostCommand struct { Kind string `json:"kind"`; Spec *Spec `json:"spec,omitempty"` }
type HostStatus struct { Kind string `json:"kind"`; PID int `json:"pid,omitempty"`; ProcessGroup int `json:"processGroup,omitempty"`; ExitCode int `json:"exitCode,omitempty"`; ErrorCode string `json:"errorCode,omitempty"`; Message string `json:"message,omitempty"` }

func StartCommand(spec Spec) HostCommand { return HostCommand{Kind: "start", Spec: &spec} }
func StopCommand() HostCommand { return HostCommand{Kind: "stop"} }
```

控制帧限制为 64 KiB；Host 只接受一次 `start`，随后只接受一次 `stop` 或 EOF。

- [ ] **Step 4：实现固定模拟场景**

`taskfixture.Run` 必须使用 switch 穷举全部场景：

```go
func Run(ctx context.Context, scenario task.Scenario, stdout, stderr io.Writer) int {
	switch scenario {
	case task.ScenarioSuccess:
		return 0
	case task.ScenarioExitNonzero:
		fmt.Fprintln(stderr, "fixture exits with code 17")
		return 17
	case task.ScenarioEmitOutput:
		fmt.Fprintln(stdout, "fixture stdout")
		fmt.Fprintln(stderr, "fixture stderr")
		return 0
	case task.ScenarioHang:
		<-ctx.Done()
		return 0
	case task.ScenarioSpawnChild:
		return runChildFixture(ctx, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "unknown fixture scenario")
		return 2
	}
}
```

`runChildFixture` 只能通过 `os.Executable()` 启动同一二进制的 `--task-fixture-child` 模式：

```go
func runChildFixture(ctx context.Context, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil { fmt.Fprintln(stderr, "fixture executable is unavailable"); return 1 }
	child := exec.CommandContext(ctx, executable, "--task-fixture-child")
	child.Stdout, child.Stderr = stdout, stderr
	if err := child.Start(); err != nil { fmt.Fprintln(stderr, "fixture child could not start"); return 1 }
	fmt.Fprintf(stdout, "CHILD_PID=%d\n", child.Process.Pid)
	if err := child.Wait(); err != nil && ctx.Err() == nil { return 1 }
	return 0
}
```

Child mode 等待传入的 signal context，直到被终止。

- [ ] **Step 5：实现平台无关 Process Host 状态机**

`processhost.Run(ctx, platform, control, status, stdout, stderr)` 使用受限 JSON decoder 读取 `HostCommand`。先定义完整平台端口，使本任务可以用 fake Platform 独立测试和提交：

```go
type Target interface {
	PID() int
	ProcessGroup() int
	Wait() (int, error)
}

type Platform interface {
	Start(processcontrol.Spec, io.Writer, io.Writer) (Target, error)
	Terminate(Target, time.Duration) error
}

type waitResult struct { exitCode int; err error }

func writeStatus(writer io.Writer, value processcontrol.HostStatus) error {
	return json.NewEncoder(writer).Encode(value)
}

func waitOrStop(ctx context.Context, platform Platform, target Target, stop <-chan struct{}) waitResult {
	done := make(chan waitResult, 1)
	go func() { code, err := target.Wait(); done <- waitResult{exitCode: code, err: err} }()
	select {
	case result := <-done:
		if err := platform.Terminate(target, 2*time.Second); err != nil && result.err == nil { result.err = err }
		return result
	case <-stop:
		err := platform.Terminate(target, 2*time.Second)
		result := <-done
		if result.err == nil { result.err = err }
		return result
	case <-ctx.Done():
		err := platform.Terminate(target, 2*time.Second)
		result := <-done
		if result.err == nil { result.err = err }
		return result
	}
}
```

Host 主流程固定为：

```go
func Run(ctx context.Context, platform Platform, control io.Reader, status io.Writer, stdout, stderr io.Writer) int {
	commands := json.NewDecoder(io.LimitReader(control, 64*1024))
	var start processcontrol.HostCommand
	if err := commands.Decode(&start); err != nil || start.Kind != "start" || start.Spec == nil {
		writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "INVALID_HOST_COMMAND", Message: "invalid start command"})
		return 2
	}
	target, err := platform.Start(*start.Spec, stdout, stderr)
	if err != nil {
		writeStatus(status, processcontrol.HostStatus{Kind: "error", ErrorCode: "PROCESS_START_FAILED", Message: "target process could not start"})
		return 1
	}
	writeStatus(status, processcontrol.HostStatus{Kind: "started", PID: target.PID(), ProcessGroup: target.ProcessGroup()})
	stop := make(chan struct{})
	go func() { var command processcontrol.HostCommand; if commands.Decode(&command) != nil || command.Kind == "stop" { close(stop) } }()
	result := waitOrStop(ctx, platform, target, stop)
	errorCode := ""
	if result.err != nil { errorCode = "PROCESS_WAIT_FAILED" }
	writeStatus(status, processcontrol.HostStatus{Kind: "exit", ExitCode: result.exitCode, ErrorCode: errorCode})
	if result.err != nil { return 1 }
	return 0
}
```

错误 status 只包含稳定码和脱敏文本。绝对路径、参数、环境变量和 token 不得写入 status 或 stderr。

- [ ] **Step 6：接入互斥 CLI 模式**

`main.go` 增加 `--process-host`、`--task-fixture`、`--task-fixture-child`，使用现有 `flags.Visit` 检测“显式提供空值”的情况。解析后先计算唯一 mode；提供零个或多个内部 mode 与 service/preparation flags 混用都返回 code 2。

为测试匿名控制输入，把入口签名改为 `run(args []string, stdin io.Reader, stdout, stderr io.Writer) int`，`main()` 传入 `os.Stdin`；现有 CLI 测试统一传 `strings.NewReader("")`，Process Host 测试传入 NDJSON control frames。

Process Host 的 status 管道 handle/FD 由 Task 8/9 通过环境变量 `UNIT_TEST_IDE_STATUS_HANDLE` 注入；缺失或无效时 code 2。Common CLI 通过以下默认入口保持可编译，Task 8/9 的 build-tag 文件在 `init()` 中替换它。Fixture mode 只接受枚举值，不解析其他进程字段。

```go
var processHostEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "platform process host is unavailable")
	return 1
}
```

- [ ] **Step 7：运行公共包测试**

运行：

```sh
gofmt -w apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/internal/taskfixture apps/test-service/cmd/unit-test-service
go test ./apps/test-service/internal/taskfixture ./apps/test-service/internal/processhost ./apps/test-service/cmd/unit-test-service
```

预期：公共逻辑 PASS；fake Platform 覆盖 start、stop、EOF 和脱敏错误；默认 CLI 入口明确失败但不创建 IPC 或读取 token。

- [ ] **Step 8：提交 Process Host 与 fixture**

```sh
git add apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/internal/taskfixture apps/test-service/cmd/unit-test-service
git commit -m "feat: add controlled process host fixtures"
```

## Task 8：实现 Linux Process Group 控制器

**文件：**

- 创建：`apps/test-service/internal/processcontrol/runner_unix.go`
- 创建：`apps/test-service/internal/processcontrol/runner_unix_test.go`
- 创建：`apps/test-service/internal/processhost/host_unix.go`
- 创建：`apps/test-service/cmd/unit-test-service/process_host_unix.go`

**接口：**

- 输入：Task 7 的 Host 控制协议和 `Process` 接口。
- 产出：Linux `processcontrol.NewRunner(serviceExecutable)`、Host 侧 target Process Group、租约身份校验、`SIGTERM` 后升级到 `SIGKILL` 的清理流程。

- [ ] **Step 1：编写 Linux 孙进程终止集成测试**

`runner_unix_test.go` 使用 `//go:build linux`，测试构建临时 `unit-test-service`，然后：

```go
func TestRunnerTerminatesSpawnedProcessGroup(t *testing.T) {
	binary := buildService(t)
	runner := processcontrol.NewRunner(binary)
	process, err := runner.Prepare(context.Background(), processcontrol.Spec{
		Executable: binary,
		Args: []string{"--task-fixture", "spawn-child"},
	}, id(1), id(2))
	if err != nil { t.Fatal(err) }
	defer process.Close()
	if process.Lease().HostPID <= 0 || process.Lease().HostStartIdentity == "" { t.Fatalf("lease = %#v", process.Lease()) }
	if err := process.Start(context.Background()); err != nil { t.Fatal(err) }
	childPID := readChildPID(t, process.Output())
	if err := process.Terminate(context.Background(), 250*time.Millisecond); err != nil { t.Fatal(err) }
	result := <-process.Done()
	if result.Err != nil { t.Fatal(result.Err) }
	assertProcessGone(t, childPID)
}
```

另加测试：`Cleanup` 在 PID/start identity 不匹配时返回 `ErrLeaseIdentityMismatch` 且不发送信号；Host 控制管道 EOF 会结束目标；输出同时保留 stdout/stderr 标记。

- [ ] **Step 2：在 Linux 运行测试并确认 Runner 缺失**

运行：`go test ./apps/test-service/internal/processcontrol -run Linux -v`

预期：FAIL，错误包含 `undefined: processcontrol.NewRunner`。

- [ ] **Step 3：实现 Host 侧 target Process Group**

`host_unix.go` 使用 `//go:build linux`。目标进程使用：

```go
cmd := exec.Command(spec.Executable, spec.Args...)
cmd.Dir = spec.Dir
cmd.Env = append(os.Environ(), spec.Env...)
cmd.Stdout = stdout
cmd.Stderr = stderr
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
if err := cmd.Start(); err != nil { return nil, err }
return &unixTarget{cmd: cmd, pgid: cmd.Process.Pid}, nil
```

Host 本身不属于 target group。Platform `Terminate` 对 `-pgid` 发送 `SIGTERM`，等待宽限期；仍存在则发送 `SIGKILL`。Target `Wait` 把 `*exec.ExitError` 规范化为 `(exitCode, nil)`，只有等待机制本身失败才返回非 nil error。即使 target 主进程已经退出，也必须对 group 执行存在性检查，确认没有后代后才返回 exit status。

- [ ] **Step 4：实现受启动屏障保护的 Linux Runner**

`runner_unix.go` 的 `Prepare`：创建 control pipe、status pipe、stdout/stderr pipe；启动 `serviceExecutable --process-host`，设置新 Session 和父死亡信号；把 status 写端作为 FD 3 传入，并设置 `UNIT_TEST_IDE_STATUS_HANDLE=3`。Host 启动后仍阻塞在 control pipe，不会启动 target。

```go
host := exec.CommandContext(ctx, r.executable, "--process-host")
host.Stdin = controlReader
host.Stdout = stdoutWriter
host.Stderr = stderrWriter
host.ExtraFiles = []*os.File{statusWriter}
host.Env = append(os.Environ(), "UNIT_TEST_IDE_STATUS_HANDLE=3")
host.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGTERM}
if err := host.Start(); err != nil { return nil, err }
identity, err := linuxStartIdentity(host.Process.Pid)
if err != nil { host.Process.Kill(); return nil, err }
```

`Start` 编码唯一 `start` command，读取唯一 `started/error` status，并把 target PGID 写入内存 lease。`Terminate` 先编码 `stop`，等待宽限期；超时后先 kill target PGID，再 kill Host Session。所有管道在 `Close` 中只关闭一次。

- [ ] **Step 5：实现 `/proc` 身份校验和启动清理**

`linuxStartIdentity(pid)` 读取 `/proc/<pid>/stat` 的第 22 个字段。解析时必须从最后一个 `)` 后切分，避免进程名中的空格或括号破坏字段位置。

`Cleanup` 先比较当前 identity；不匹配则返回 `ErrLeaseIdentityMismatch`。匹配时向 Host PID 发送 `SIGTERM`，轮询 `/proc/<pid>` 至宽限期；仍存在则发送 `SIGKILL`。如果 lease 有非零 target PGID，同样对整个 group 执行 TERM/KILL。

- [ ] **Step 6：接入 Linux Process Host CLI**

`process_host_unix.go` 使用 `//go:build linux`，从 FD 3 创建 status writer，并注入：

```go
func init() {
	processHostEntry = func(stdin io.Reader, stdout, stderr io.Writer) int {
		statusFD, err := strconv.Atoi(os.Getenv("UNIT_TEST_IDE_STATUS_HANDLE"))
		if err != nil || statusFD != 3 { fmt.Fprintln(stderr, "invalid process host status handle"); return 2 }
		status := os.NewFile(uintptr(statusFD), "process-host-status")
		if status == nil { fmt.Fprintln(stderr, "invalid process host status handle"); return 2 }
		defer status.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return processhost.Run(ctx, processhost.NewPlatform(), stdin, status, stdout, stderr)
	}
}
```

`processhost.NewPlatform()` 由 `host_unix.go` 提供。

- [ ] **Step 7：运行 Linux 测试与竞态检测**

在 Linux 执行：

```sh
gofmt -w apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/cmd/unit-test-service
go test ./apps/test-service/internal/processcontrol ./apps/test-service/internal/processhost ./apps/test-service/cmd/unit-test-service
go test -race ./apps/test-service/internal/processcontrol ./apps/test-service/internal/processhost
```

预期：全部 PASS；孙进程 PID 在取消后不存在。

- [ ] **Step 8：提交 Linux 控制器**

```sh
git add apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/cmd/unit-test-service
git commit -m "feat: control linux process groups"
```

## Task 9：实现 Windows Job Object 控制器

**文件：**

- 创建：`apps/test-service/internal/processcontrol/runner_windows.go`
- 创建：`apps/test-service/internal/processcontrol/runner_windows_test.go`
- 创建：`apps/test-service/internal/processhost/host_windows.go`
- 创建：`apps/test-service/cmd/unit-test-service/process_host_windows.go`

**接口：**

- 输入：Task 7 的 Host 控制协议和 `Process` 接口。
- 产出：Windows `processcontrol.NewRunner(serviceExecutable)`，其 Host 在执行任何目标代码前已加入带 `KILL_ON_JOB_CLOSE` 的专属 Job Object。

- [ ] **Step 1：编写 Windows Job Object 孙进程测试**

`runner_windows_test.go` 使用 `//go:build windows` 并包含独立测试：

```go
func TestJobObjectTerminatesHostAndGrandchild(t *testing.T) {
	binary := buildService(t)
	runner := processcontrol.NewRunner(binary)
	process, err := runner.Prepare(context.Background(), processcontrol.Spec{Executable: binary, Args: []string{"--task-fixture", "spawn-child"}}, id(1), id(2))
	if err != nil { t.Fatal(err) }
	defer process.Close()
	hostPID := process.Lease().HostPID
	if err := process.Start(context.Background()); err != nil { t.Fatal(err) }
	childPID := readChildPID(t, process.Output())
	if err := process.Terminate(context.Background(), 250*time.Millisecond); err != nil { t.Fatal(err) }
	<-process.Done()
	assertWindowsProcessGone(t, hostPID)
	assertWindowsProcessGone(t, childPID)
}

func assertWindowsProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) { return }
		if err == nil { windows.CloseHandle(handle) }
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d is still present", pid)
}
```

再通过注入 native API fake，使 Job 创建或分配失败，断言 `Prepare` 返回错误且 control pipe 没有 start command。

- [ ] **Step 2：运行 Windows 测试并确认实现缺失**

运行：`go test ./apps/test-service/internal/processcontrol -run JobObject -v`

预期：FAIL，错误包含 `undefined: processcontrol.NewRunner` 或测试报告 Host 未受 Job 保护。

- [ ] **Step 3：创建 protected Job Object**

`runner_windows.go` 的 Job 初始化必须设置：

```go
job, err := windows.CreateJobObject(nil, nil)
if err != nil { return nil, err }
limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
if err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
	windows.CloseHandle(job)
	return nil, err
}
```

Job handle 只能由对应 `Process.Close` 关闭；关闭即终止仍在 Job 中的 Host 和后代。

- [ ] **Step 4：用原生 `CreateProcess` 暂停启动 Host**

创建 control/status/stdout/stderr 匿名管道，并使用 `STARTUPINFOEX` 的 `PROC_THREAD_ATTRIBUTE_HANDLE_LIST` 只继承明确列出的 child handles。命令行仅为服务二进制和 `--process-host`，使用 `windows.ComposeCommandLine`。

```go
flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
if err := windows.CreateProcess(nil, commandLine, nil, nil, true, flags, environment, nil, &startup.StartupInfo, &processInfo); err != nil { return nil, err }
if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
	windows.TerminateProcess(processInfo.Process, 1)
	closeProcessInformation(processInfo)
	windows.CloseHandle(job)
	return nil, err
}
if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
	windows.TerminateJobObject(job, 1)
	closeProcessInformation(processInfo)
	windows.CloseHandle(job)
	return nil, err
}
```

只有成功分配 Job 后才能恢复 Host 主线程。不得在 Assign 失败时降级到普通 `os/exec`。

- [ ] **Step 5：实现 Windows Host 与租约身份**

Host 侧为目标创建第二个带 `KILL_ON_JOB_CLOSE` 的嵌套 Job Object，并再次使用 suspended `CreateProcess`：目标加入内层 Job 后才能恢复。这样目标主进程自然退出时，Host 可以终止/关闭内层 Job、清理仍存活的后代，再写 exit status；外层 Job 仍负责服务崩溃或取消时同时终止 Host 和目标树。Target `Wait` 把普通非零退出规范化为 `(exitCode, nil)`。`windowsStartIdentity` 用 `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)` 和 `GetProcessTimes` 返回 creation time 的十进制值。

`Start`、status 解析和输出读取复用公共代码；`Terminate` 首先发送 `stop`，宽限期后调用 `TerminateJobObject(job, 1)`。`Cleanup` 校验 Host creation time；旧服务异常退出时 Job handle 已关闭，正常结果应为进程不存在；若匹配的 Host 仍存在，则调用 `TerminateProcess` 并确认退出。

- [ ] **Step 6：接入 Windows Process Host CLI**

Windows Runner 把可继承 status handle 的十进制值写入 `UNIT_TEST_IDE_STATUS_HANDLE`。`process_host_windows.go` 使用 `//go:build windows`，在 `init()` 中替换 `processHostEntry`：解析十进制 `windows.Handle`，转换为 `os.NewFile`，创建响应 `os.Interrupt`/`SIGTERM` 的 context，然后调用 `processhost.Run(ctx, processhost.NewPlatform(), stdin, status, stdout, stderr)`；缺失、零值或无法转换的 handle 返回 code 2。

- [ ] **Step 7：运行 Windows 测试并交叉编译 Linux 包**

在 Windows 执行：

```powershell
gofmt -w apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/cmd/unit-test-service
go test ./apps/test-service/internal/processcontrol ./apps/test-service/internal/processhost ./apps/test-service/cmd/unit-test-service
$env:GOOS='linux'; $env:GOARCH='amd64'; go test -c -o $env:TEMP/processcontrol-linux.test ./apps/test-service/internal/processcontrol
Remove-Item Env:GOOS,Env:GOARCH
```

预期：Windows 测试全部 PASS，Linux test binary 成功生成；GitHub Linux job 在 Task 14 执行真实 Linux 测试。

- [ ] **Step 8：提交 Windows 控制器**

```sh
git add apps/test-service/internal/processcontrol apps/test-service/internal/processhost apps/test-service/cmd/unit-test-service
git commit -m "feat: control windows process jobs"
```

## Task 10：实现 TaskManager 串行任务引擎

**文件：**

- 创建：`apps/test-service/internal/task/manager.go`
- 创建：`apps/test-service/internal/task/manager_test.go`
- 修改：`apps/test-service/internal/task/ports.go`

**接口：**

- 输入：Task 4 Store、Task 5 ArtifactStore、Task 6 Publisher、Task 8/9 ProcessRunner。
- 产出：`task.NewManager(config)`、`Start`、`Get`、`List`、`Cancel`、`Shutdown` 和 `Healthy`；所有改变运行状态的事件通过一个命令队列排序。Artifact 查询由 Runtime 直接组合 Store 与 ArtifactStore。

- [ ] **Step 1：编写启动、幂等与取消测试**

使用内存 fake Store、fake Process 和 recording Publisher：

```go
func TestManagerStartsAndCancelsOneTask(t *testing.T) {
	fixture := newManagerFixture(t)
	started, err := fixture.manager.Start(context.Background(), task.StartRequest{
		IdempotencyKey: id(1), Scenario: task.ScenarioSpawnChild, Timeout: 30 * time.Second,
	})
	if err != nil { t.Fatal(err) }
	if started.Status != task.StatusRunning { t.Fatalf("task = %#v", started) }
	if got := fixture.publishedTypes(); !reflect.DeepEqual(got, []task.EventType{"task.created", "task.started"}) { t.Fatalf("events = %v", got) }

	cancelled, err := fixture.manager.Cancel(context.Background(), started.ID)
	if err != nil { t.Fatal(err) }
	if cancelled.Status != task.StatusCancelling { t.Fatalf("task = %#v", cancelled) }
	fixture.process.complete(task.ProcessResult{ExitCode: 1})
	finished := fixture.awaitTask(t, started.ID, task.StatusFinished)
	if finished.Outcome != task.OutcomeCancelled { t.Fatalf("task = %#v", finished) }
}
```

另加测试：相同幂等键同请求返回同一 task ID 且只启动一次；相同键不同参数返回 `task.ErrIdempotencyConflict`；取消终止任务不追加事件；timeout 先于 cancel 时结果为 `timed_out`，cancel 先于 timeout 时为 `cancelled`。

- [ ] **Step 2：编写输出、制品和存储故障测试**

至少验证：stdout/stderr 被按到达顺序持久化为 `task.output`；单块最多 16 KiB；25 ms 合并窗口把事件速率限制在每秒最多 40 次 flush；单任务最多 4 MiB，超限只产生一次截断事件；自然 exit 0 为 `succeeded`、exit 17 为 `command_failed`；Process 错误为 `infrastructure_failed`；完成事务同时包含摘要 Artifact、`artifact.created` 和 `task.finished`。

故障测试在 `AppendEvent` 或 `Apply` 注入 `task.ErrStorageUnavailable`，断言 Process 被终止、`Healthy()` 为 false，后续 `Start` 返回 `task.ErrStorageUnavailable`。

- [ ] **Step 3：运行测试并确认 Manager 不存在**

运行：`go test ./apps/test-service/internal/task -run Manager -v`

预期：FAIL，错误包含 `undefined: task.NewManager`。

- [ ] **Step 4：定义 Manager 依赖与公共 API**

在 `ports.go` 增加：

```go
type ProcessFactory interface { Prepare(context.Context, ProcessSpec, string, string) (ManagedProcess, error) }
type ProcessSpec struct { Executable string; Args, Env []string; Dir string }
type ProcessOutput struct { Stream string; Data []byte }
type ProcessResult struct { ExitCode int; Err error }
type ManagedProcess interface {
	Lease() ProcessLease
	Start(context.Context) error
	Output() <-chan ProcessOutput
	Done() <-chan ProcessResult
	Terminate(context.Context, time.Duration) error
	Close() error
}
type ArtifactWriter interface { CommitJSON(context.Context, string, string, time.Time, any) (Artifact, error) }
type Clock interface { Now() time.Time; After(time.Duration) <-chan time.Time }
type IDGenerator func() string

type RealClock struct{}
func (RealClock) Now() time.Time { return time.Now().UTC() }
func (RealClock) After(delay time.Duration) <-chan time.Time { return time.After(delay) }
```

在 `manager.go` 定义：

```go
type StartRequest struct { IdempotencyKey string; Scenario Scenario; Timeout time.Duration }
type ManagerConfig struct {
	Store Store
	Publisher Publisher
	Processes ProcessFactory
	Artifacts ArtifactWriter
	Clock Clock
	NewID IDGenerator
	ServiceExecutable string
	ServiceInstanceID string
	TerminationGrace time.Duration
	OutputFlushInterval time.Duration
	CommandQueue int
}
```

`processcontrol.Runner` 通过一个小 adapter 实现 `task.ProcessFactory`，避免 task 包反向导入平台包。

`NewManager` 对空依赖返回错误；`TerminationGrace` 默认 2 秒、`OutputFlushInterval` 默认 25 毫秒、`CommandQueue` 默认 256。测试必须显式注入 fake Clock，不能依赖真实 sleep。

- [ ] **Step 5：实现单一命令循环和幂等启动**

Manager 构造函数启动唯一 `loop()` goroutine。`Start/Get/List/Cancel/Shutdown` 各自把 typed command 写入 `commands` 并等待单次 response channel；Store 查询也在命令循环中执行，保证状态观察顺序一致。

请求 hash 固定为规范化 JSON 的 SHA-256：

```go
canonical, _ := json.Marshal(struct {
	Scenario Scenario `json:"scenario"`
	TimeoutMS int64 `json:"timeoutMs"`
}{request.Scenario, request.Timeout.Milliseconds()})
sum := sha256.Sum256(canonical)
requestHash := hex.EncodeToString(sum[:])
```

启动流程必须按顺序执行：Store `Create(queued)`；Publish committed `task.created`；Process `Prepare`；Store `Apply(running + initial lease + task.started)`；Publish；Process `Start`；Store `UpdateLease(process.Lease())`；安装 timeout；启动 output/wait watcher。任何中间失败都关闭/终止 Process，并通过同一循环完成基础设施失败状态。

- [ ] **Step 6：实现输出上限和存储失败熔断**

每个 output watcher 只把数据复制后发送回命令队列。循环按接收顺序缓冲 stdout/stderr：累计 16 KiB 时立即 flush，否则第一次写入时用 `Clock.After(25*time.Millisecond)` 安排一次 flush，因此每个活动任务最多每秒 40 次定时 flush。Flush 时转换为合法 UTF-8并持久化：

```go
text := strings.ToValidUTF8(string(chunk), "\uFFFD")
payload, _ := json.Marshal(map[string]any{"stream": output.Stream, "text": text, "truncated": false})
event, err := m.store.AppendEvent(ctx, taskID, EventDraft{TaskID: taskID, Type: "task.output", At: m.clock.Now(), Payload: payload})
if err != nil { m.failStorage(active, err); return }
m.publisher.Publish(event)
```

累计达到 4 MiB 后丢弃后续字节，只提交一次 `{"stream":"combined","text":"","truncated":true}`。`failStorage` 设置 unhealthy、拒绝新任务并调用 `Terminate`；不能把故障标记为 `command_failed`。

- [ ] **Step 7：实现确定性终止原因和摘要事务**

活动任务保存第一个 `terminationCause`。Cancel 和 timeout 只有在 cause 为空时写入：Cancel 先提交 `cancelling + task.cancellation_requested`，timeout 保持 `running` 但把最终 cause 固定为 `timed_out`；二者都异步调用同一 `Terminate`。

Process 完成时按以下优先级选择 outcome：已记录 cause；Process error；exit code 0；非零 exit code。生成摘要：

```go
summary := struct {
	TaskID string `json:"taskId"`
	Scenario Scenario `json:"scenario"`
	Outcome Outcome `json:"outcome"`
	FinishedAt string `json:"finishedAt"`
}{active.task.ID, active.task.Scenario, outcome, finishedAt.Format(time.RFC3339Nano)}
```

先用 ArtifactStore 原子写文件，再通过一次 Store `Apply` 写入 `finished` snapshot、删除 lease、插入 Artifact metadata，并依次追加 `artifact.created`、`task.finished`。提交成功后才按 sequence Publish 两个事件。最后关闭 Process 并从 active map 删除。

- [ ] **Step 8：实现关闭语义**

`Shutdown(ctx)` 原子拒绝新任务，把所有活动任务的 cause 设为 `interrupted`，终止进程并等待它们完成持久化；context 到期时返回错误但仍保持 unhealthy/closing。服务 shutdown 不能把活动任务记为 `cancelled`。

- [ ] **Step 9：运行 Manager 单元测试和竞态检测**

运行：

```sh
gofmt -w apps/test-service/internal/task
go test ./apps/test-service/internal/task
go test -race ./apps/test-service/internal/task
```

预期：全部 PASS；竞态测试无 data race；事件 sequence 严格递增。

- [ ] **Step 10：提交任务引擎**

```sh
git add apps/test-service/internal/task
git commit -m "feat: orchestrate persistent task execution"
```

## Task 11：把任务 API 与事件流接入 Session/Server

**文件：**

- 修改：`apps/test-service/internal/session/session.go`
- 修改：`apps/test-service/internal/session/session_test.go`
- 修改：`apps/test-service/internal/server/server.go`
- 修改：`apps/test-service/internal/server/server_test.go`
- 修改：`apps/test-service/internal/server/service.go`
- 修改：`apps/test-service/internal/server/service_test.go`

**接口：**

- 输入：TaskManager、EventBroker、ArtifactStore 和生成协议类型。
- 产出：严格解码七个 Phase 2 方法的 `session.Backend` 路由，以及能够在同一 NDJSON 连接上安全交错 response/event 的单写入者 Server。

- [ ] **Step 1：编写 Session 路由与错误映射测试**

用 fake Backend 认证 `1.1` Session 后逐一调用七个方法，断言 payload 映射。核心启动测试：

```go
func TestSessionRoutesControlledTaskStart(t *testing.T) {
	backend := &fakeBackend{startResult: task.Task{ID: id(1), Scenario: task.ScenarioHang, Status: task.StatusRunning, CreatedAt: fixedTime, LastSequence: 2}}
	s := authenticatedV11(t, backend)
	result := s.Handle(context.Background(), requestVersion(t, "1.1", "tasks/start", map[string]any{
		"idempotencyKey": id(2), "scenario": "hang", "timeoutMs": 30000,
	}))
	if result.Response.Kind != "response" || backend.startRequest.Scenario != task.ScenarioHang { t.Fatalf("result=%#v request=%#v", result, backend.startRequest) }
}
```

表驱动测试把 `task.ErrNotFound`、`task.ErrIdempotencyConflict`、`task.ErrStorageUnavailable`、无效范围和 `eventbroker.ErrSubscriberTooSlow` 映射为设计中的稳定错误码与 retryable 值。额外字段、负 offset、limit 超过 200、timeout 超过一天都返回 `INVALID_MESSAGE`。

- [ ] **Step 2：编写 response/event 交错与写入串行测试**

`server_test.go` 使用 `net.Pipe` 和 fake Subscription：先发送 `events/subscribe`，断言第一条消息一定是 subscribe response；随后发送 event 3，同时发起 `tasks/get`，断言两个 JSON envelope 都完整且可独立解码。包装 connection 的 `Write`，若并发进入则测试失败。

再测试：订阅错误关闭连接；客户端断开调用 `Subscription.Close`；订阅存在时不设置两分钟 read idle deadline；未认证连接仍受原 handshake deadline 限制。

- [ ] **Step 3：运行测试并确认路由尚不存在**

运行：`go test ./apps/test-service/internal/session ./apps/test-service/internal/server`

预期：FAIL，错误包含 `undefined: session.Backend` 或 Phase 2 方法返回 `SERVICE_UNHEALTHY`。

- [ ] **Step 4：定义 Backend 和 HandleResult**

`session.go` 增加：

```go
type ArtifactChunk struct { Data []byte; NextOffset int64; EOF bool; Metadata task.Artifact }

type Backend interface {
	Start(context.Context, task.StartRequest) (task.Task, error)
	Get(context.Context, string) (task.Task, error)
	List(context.Context, string, int) (task.Page[task.Task], error)
	Cancel(context.Context, string) (task.Task, error)
	Subscribe(context.Context, int64) (*eventbroker.Subscription, error)
	ListArtifacts(context.Context, string, string, int) (task.Page[task.Artifact], error)
	ReadArtifact(context.Context, string, int64, int) (ArtifactChunk, error)
}

type HandleResult struct {
	Response protocol.Response
	Subscription *eventbroker.Subscription
}
```

`session.New(token, platform, transport, backend)` 保存共享 Backend，但认证状态仍属于单个连接。

- [ ] **Step 5：实现严格 payload 解码和模型投影**

为每个方法定义私有 payload struct，并统一使用：

```go
func decodeStrict[T any](raw json.RawMessage) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil { return value, err }
	if err := decoder.Decode(&struct{}{}); err != io.EOF { return value, errors.New("multiple JSON values") }
	return value, nil
}
```

`toProtocolTask` 和 `toProtocolArtifact` 是唯一领域→生成模型转换函数。可选时间仅在非 nil 时设置；`outcome` 仅对 finished 任务设置。`artifacts/read` 把 bytes 编码为 Base64URL 字符串，并返回 `data`、`nextOffset`、`eof`、`sizeBytes`、`sha256`。

- [ ] **Step 6：实现单写入者连接循环**

`server.go` 为每条连接创建 `outbound chan any` 和 writer goroutine；只有 writer 调用 `encoder.Encode` 和 `SetWriteDeadline`。Read loop 解码请求并调用 Session，然后按顺序：先把 response 放入 outbound；再启动 subscription forwarder。

```go
outbound <- result.Response
if result.Subscription != nil {
	activeSubscription = result.Subscription
	_ = connection.SetReadDeadline(time.Time{})
	go forwardSubscription(connectionContext, result.Subscription, outbound, closeConnection)
}
```

`forwardSubscription` 把 `task.Event` 投影为 `protocol.Event`，保持 Store sequence 和 occurred time；若 Errors channel 返回值，则尽力把关联 subscribe request 的 retryable error 放入 outbound，随后关闭连接。连接退出时 cancel context、关闭 subscription、关闭 outbound 并等待 writer。

- [ ] **Step 7：让 Service 共享 Backend**

`server.NewService` 增加 `session.Backend` 参数。每个 accepted connection 创建独立 Session，但传入同一个 Backend；连接关闭不调用 TaskManager cancel。Service 只拥有 listener/connection：shutdown 时停止 accept、关闭连接并等待 handlers；它不关闭共享 Backend。Task 13 的 composition root 在 `Serve` 返回后单独关闭 Runtime。

- [ ] **Step 8：运行 Session/Server 回归与竞态测试**

运行：

```sh
gofmt -w apps/test-service/internal/session apps/test-service/internal/server
go test ./apps/test-service/internal/session ./apps/test-service/internal/server
go test -race ./apps/test-service/internal/session ./apps/test-service/internal/server
```

预期：全部 PASS；Phase 1 大小限制、deadline 和 connection limit 测试继续通过；并发写探针没有触发。

- [ ] **Step 9：提交服务路由和事件流**

```sh
git add apps/test-service/internal/session apps/test-service/internal/server
git commit -m "feat: stream task events over sessions"
```

## Task 12：扩展 TypeScript 客户端的任务、事件与重连 API

**文件：**

- 创建：`packages/test-client/src/connection.ts`
- 创建：`packages/test-client/src/subscription.ts`
- 修改：`packages/test-client/src/envelopes.ts`
- 修改：`packages/test-client/src/client.ts`
- 修改：`packages/test-client/src/client.test.ts`
- 修改：`packages/test-client/src/index.ts`

**接口：**

- 输入：Task 1 的 `1.1` Schema/生成模型和 Task 11 的 NDJSON 行为。
- 产出：兼容 `1.0` 的 `ProtocolClient`，公开 `startTask/getTask/listTasks/cancelTask/subscribeEvents/listArtifacts/readArtifact/reconnect`；`EventSubscription` 实现 `AsyncIterable<TaskEvent>` 并记录 `lastSequence`。

- [ ] **Step 1：编写 `1.1` handshake 降级和事件交错测试**

保留现有 Duplex pair 测试，再加入：

```ts
test("client falls back to an exact 1.0 handshake", async () => {
  const { client, requests } = scriptedClient((request) => {
    if (request.protocolVersion === "1.1") return error(request, "UNSUPPORTED_PROTOCOL", false, "1.0");
    return response(request, { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }, "1.0");
  });
  const negotiated = await client.handshake("0123456789abcdef", "test", "0.2.0");
  assert.equal(negotiated.negotiatedProtocolVersion, "1.0");
  assert.deepEqual(requests.map(({ protocolVersion }) => protocolVersion), ["1.1", "1.0"]);
  assert.equal("supportedProtocolVersions" in requests[1].payload, false);
});

test("client routes interleaved responses and deduplicates events", async () => {
  const fixture = await authenticatedV11Client();
  const subscription = await fixture.client.subscribeEvents(0);
  fixture.server.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
  fixture.server.write(`${JSON.stringify(response(fixture.lastRequest("tasks/get"), runningTask()))}\n`);
  fixture.server.write(`${JSON.stringify(taskEvent(1, "task.created"))}\n`);
  fixture.server.write(`${JSON.stringify(taskEvent(2, "task.started"))}\n`);
  const events = await take(subscription, 2);
  assert.deepEqual(events.map(({ sequence }) => sequence), [1, 2]);
  assert.equal(subscription.lastSequence, 2);
});
```

另加测试：协议行大于 1 MiB 仍关闭；断开拒绝 pending request；`reconnect()` 使用缓存凭据重新 handshake 并从最后 sequence 订阅；无 connector 的 `attach()` 调用 reconnect 返回明确错误；artifact 分块按 offset 拼接并验证 SHA-256；错误 digest 拒绝结果。

- [ ] **Step 2：运行客户端测试并确认 API 缺失**

运行：`pnpm --filter @unit-test-ide/test-client test`

预期：FAIL，TypeScript 报告 `subscribeEvents`、`startTask` 或 `reconnect` 不存在。

- [ ] **Step 3：扩展 envelope 类型和双版本校验器**

`envelopes.ts` 定义：

```ts
export type ProtocolVersion = "1.0" | "1.1";
export type Method = "handshake" | "capabilities/get" | "shutdown" | "tasks/start" | "tasks/get" | "tasks/list" | "tasks/cancel" | "events/subscribe" | "artifacts/list" | "artifacts/read";
export interface RequestEnvelope { protocolVersion: ProtocolVersion; kind: "request"; messageId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ResponseEnvelope { protocolVersion: ProtocolVersion; kind: "response"; messageId: string; requestId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ErrorEnvelope { protocolVersion: ProtocolVersion; kind: "error"; messageId: string; requestId: string; sentAt: string; error: { code: string; message: string; retryable: boolean }; }
export type IncomingEnvelope = ResponseEnvelope | ErrorEnvelope | TaskEvent;
```

`connection.ts` 创建两个 Ajv validator：现有 `@unit-test-ide/protocol-schema/v1/message` 与新增 `v1.1/message`。编译 `1.1` message 前先 `addSchema` task/event/artifact Schema。根据解析对象的 `protocolVersion` 选择 validator，未知版本立即关闭。

- [ ] **Step 4：抽取单写入连接和事件分派**

把现有 buffer、最大行、pending map 和 `#onLine` 移入 `Connection`。公开：

```ts
export class Connection {
  request(version: ProtocolVersion, method: Method, payload: Record<string, unknown>): Promise<Record<string, unknown>>;
  onEvent(listener: (event: TaskEvent) => void): () => void;
  onClose(listener: (error: Error) => void): () => void;
  close(): void;
}
```

Response/error 仍按 `requestId` 分派；event 不读取 `requestId`，而是同步复制 listener 集合后调用。任何 Schema 错误、JSON 错误或消息过大都只触发一次 close 并拒绝全部 pending。

- [ ] **Step 5：实现 EventSubscription**

`subscription.ts` 使用内部等待者队列实现 AsyncIterator：

```ts
export class EventSubscription implements AsyncIterable<TaskEvent> {
  #queue: TaskEvent[] = [];
  #waiters: Array<(value: IteratorResult<TaskEvent>) => void> = [];
  #closed = false;
  lastSequence: number;

  constructor(afterSequence: number) { this.lastSequence = afterSequence; }
  push(event: TaskEvent): void {
    if (this.#closed || event.sequence <= this.lastSequence) return;
    this.lastSequence = event.sequence;
    const waiter = this.#waiters.shift();
    if (waiter) waiter({ value: event, done: false }); else this.#queue.push(event);
  }
  close(): void { this.#closed = true; for (const waiter of this.#waiters.splice(0)) waiter({ value: undefined, done: true }); }
  [Symbol.asyncIterator](): AsyncIterator<TaskEvent> { return { next: () => this.next() }; }
  next(): Promise<IteratorResult<TaskEvent>> {
    const event = this.#queue.shift();
    if (event) return Promise.resolve({ value: event, done: false });
    if (this.#closed) return Promise.resolve({ value: undefined, done: true });
    return new Promise((resolve) => this.#waiters.push(resolve));
  }
}
```

连接错误关闭 iterator；重连成功时复用同一个 subscription 对象，不重置 sequence。

- [ ] **Step 6：实现 handshake 缓存与显式 reconnect**

`ProtocolClient.connect(endpoint)` 保存 connector；`attach(stream)` 不保存。`handshake` 首先发 `1.1` payload：

```ts
{ token, clientName, clientVersion, supportedProtocolVersions: ["1.1", "1.0"] }
```

若收到 `ProtocolError` 且 code 为 `UNSUPPORTED_PROTOCOL`，在同一连接上发送完全旧式的 `1.0` payload。成功后缓存凭据和协商版本。

`reconnect()` 关闭旧 Connection、调用 connector、重新 handshake；若存在 active subscription，则在安装 event listener 后调用 `events/subscribe`，payload 使用 `subscription.lastSequence`。重连只由调用方显式触发，不加入无限自动重试。

- [ ] **Step 7：实现 typed task 与制品方法**

所有 Phase 2 API 在协商版本不是 `1.1` 时抛出 `ProtocolError("PROTOCOL_FEATURE_UNAVAILABLE", "protocol 1.1 was not negotiated", false)`。方法签名：

```ts
startTask(input: { idempotencyKey: string; scenario: SimulationScenario; timeoutMs: number }): Promise<TaskSnapshot>;
getTask(taskId: string): Promise<TaskSnapshot>;
listTasks(input?: { cursor?: string; limit?: number }): Promise<{ items: TaskSnapshot[]; nextCursor?: string }>;
cancelTask(taskId: string): Promise<TaskSnapshot>;
subscribeEvents(afterSequence: number): Promise<EventSubscription>;
listArtifacts(taskId: string, input?: { cursor?: string; limit?: number }): Promise<{ items: ArtifactMetadata[]; nextCursor?: string }>;
readArtifact(artifactId: string): Promise<Uint8Array>;
```

每个 response 使用对应 Schema validator。`readArtifact` 每次请求最多 64 KiB，验证 `nextOffset` 严格前进、最终长度等于 `sizeBytes`，并使用 `createHash("sha256")` 比较摘要。

- [ ] **Step 8：更新公共导出并运行测试**

`index.ts` 导出 `ProtocolClient`、`ProtocolError`、`EventSubscription` 和所有公开输入类型。运行：

```sh
pnpm --filter @unit-test-ide/test-client test
pnpm build
```

预期：全部 PASS；现有五个 Phase 1 客户端测试继续通过。

- [ ] **Step 9：提交 TypeScript 客户端**

```sh
git add packages/test-client
git commit -m "feat: add reconnecting task client"
```

## Task 13：组装安全数据目录、单实例锁与启动恢复

**文件：**

- 创建：`apps/test-service/internal/instance/lock.go`
- 创建：`apps/test-service/internal/instance/lock_unix.go`
- 创建：`apps/test-service/internal/instance/lock_windows.go`
- 创建：`apps/test-service/internal/instance/lock_test.go`
- 创建：`apps/test-service/internal/runtime/data_dir.go`
- 创建：`apps/test-service/internal/runtime/data_dir_unix.go`
- 创建：`apps/test-service/internal/runtime/data_dir_windows.go`
- 创建：`apps/test-service/internal/runtime/runtime.go`
- 创建：`apps/test-service/internal/runtime/runtime_test.go`
- 修改：`apps/test-service/cmd/unit-test-service/main.go`
- 修改：`apps/test-service/cmd/unit-test-service/main_test.go`
- 修改：`apps/test-service/internal/server/service.go`

**接口：**

- 输入：Task 4–12 的 Store、ArtifactStore、Broker、Runner、Manager、Session Backend 和 Server。
- 产出：`runtime.Open(config) (*Runtime, error)`；恢复在 IPC 创建前完成；`Runtime` 实现 `session.Backend` 并提供有序 `Shutdown`。

- [ ] **Step 1：编写启动顺序和恢复测试**

`runtime_test.go` 使用 fake Runner 和真实临时 SQLite/ArtifactStore：预置一个 `running` task、匹配 lease、一个 `.artifact-*.tmp` 和一个无主完成文件，然后调用 `runtime.Open`。断言顺序记录严格为：

```go
[]string{"validate-data-dir", "lock-instance", "open-sqlite", "cleanup-process", "recover-interrupted", "cleanup-artifacts", "start-manager"}
```

断言任务为 `finished/interrupted`、租约删除、两类无主文件删除、恢复事件可通过 `Subscribe(0)` 重放。另加测试：identity mismatch 不杀当前进程但仍把旧任务标为 interrupted；第二个 Runtime 无法取得同一锁；目录权限不安全时 IPC listener factory 从未调用。

- [ ] **Step 2：运行测试并确认 Runtime 不存在**

运行：`go test ./apps/test-service/internal/runtime ./apps/test-service/internal/instance`

预期：FAIL，错误包含 `undefined: runtime.Open` 或缺少 package。

- [ ] **Step 3：实现 owner-only 数据目录**

`data_dir.go` 定义固定布局：

```go
type Layout struct { Root, Database, Artifacts, Lock string }

func PrepareDataDir(root string) (Layout, error) {
	absolute, err := filepath.Abs(root)
	if err != nil { return Layout{}, err }
	if err := prepareOwnerOnlyDirectory(absolute); err != nil { return Layout{}, err }
	return Layout{
		Root: absolute,
		Database: filepath.Join(absolute, "history.sqlite3"),
		Artifacts: filepath.Join(absolute, "artifacts"),
		Lock: filepath.Join(absolute, "service.lock"),
	}, nil
}
```

Unix：目录必须为当前 EUID 所有、类型为真实目录、mode 不宽于 `0700`，逐段拒绝 symlink。Windows：使用平台原生 API 创建受保护的仅当前 owner DACL；重新打开目录 handle，验证 owner 与当前 token SID 相同，DACL 不继承且每条 allow ACE 只授予 owner。不得调用 `icacls /grant:r`。

- [ ] **Step 4：实现跨平台单实例锁**

`instance.Lock(path)` 返回 `io.Closer`。Unix 使用 `O_CREATE|O_RDWR|O_CLOEXEC|O_NOFOLLOW` 加 `flock(LOCK_EX|LOCK_NB)`，文件 mode `0600`。Windows 使用 `CreateFile` 打开 owner-only lock file，sharing mode 为 0，并用 `LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY)`。已锁定统一返回 `instance.ErrAlreadyRunning`；关闭时解锁并关闭 handle，但保留普通 lock 文件。

- [ ] **Step 5：实现 Runtime 恢复顺序**

`runtime.go` 的配置和入口：

```go
type Config struct {
	DataDir string
	ServiceExecutable string
	Platform string
	Clock task.Clock
	NewID task.IDGenerator
	TerminationGrace time.Duration
}

type Runtime struct {
	store *taskstore.Store
	artifacts *artifactstore.Store
	broker *eventbroker.Broker
	manager *task.Manager
	runner processcontrol.Runner
	lock io.Closer
	closeOnce sync.Once
}
```

`Open` 必须：PrepareDataDir；Lock；taskstore.Open；artifactstore.New；读取 `ActiveLeases` 并逐个 `runner.Cleanup`；调用 `RecoverInterrupted`；读取 referenced paths 并 Cleanup；创建 Broker；Publish 恢复事件不是必需的，因为订阅从 SQLite 重放；最后创建 Manager。任何错误都按逆序关闭已创建资源。

- [ ] **Step 6：实现 Backend adapter**

Runtime 的 `Start/Get/List/Cancel/ListArtifacts` 委托 Manager/Store；`Subscribe` 委托 Broker；`ReadArtifact` 先从 Store 按 ID 取 metadata，再调用 ArtifactStore `ReadChunk`。对外不返回 relative path 或绝对路径。

Process adapter 把 `task.ProcessSpec/ManagedProcess` 与 `processcontrol.Spec/Process` 一一转换，复制 output bytes，不能用类型断言绕过接口。

- [ ] **Step 7：实现有序 Shutdown**

`Runtime.Shutdown(ctx)` 通过 `sync.Once` 执行：Manager 停止新任务并以 interrupted 终止活动任务；关闭 Broker 订阅；关闭 Store；释放实例锁。若 Manager 超时，仍继续关闭连接/数据库并返回组合错误。`Runtime.Close` 使用 10 秒默认 context。

- [ ] **Step 8：修改 CLI，使恢复先于 IPC**

Service mode 新增必需 `--data-dir`。`main.go` 的顺序调整为：消费 token；定位自身可执行文件；`runtime.Open`；然后 `transport.Listen`；构造 Server；输出 READY；Serve；最后 Runtime Shutdown。关键代码顺序：

```go
active, err := serviceruntime.Open(serviceruntime.Config{DataDir: *dataDir, ServiceExecutable: executable, Platform: transport.PlatformName(), Clock: task.RealClock{}, NewID: task.NewID, TerminationGrace: 2 * time.Second})
if err != nil { fmt.Fprintln(stderr, err); return 1 }
defer active.Close()
listener, err := transport.Listen(*endpoint)
if err != nil { fmt.Fprintln(stderr, err); return 1 }
service := server.NewService(listener, token, transport.PlatformName(), transport.TransportName(), active, server.ServiceConfig{MaxConnections: 64})
```

`READY` 只能在 Runtime recovery、listener 和 Service 全部成功后输出。更新 CLI 测试：缺少 data-dir 返回 code 2；内部模式仍不要求 data-dir；无效/不安全目录不产生 READY。

- [ ] **Step 9：运行 Runtime、CLI 和完整 Go 测试**

运行：

```sh
gofmt -w apps/test-service/internal/instance apps/test-service/internal/runtime apps/test-service/cmd/unit-test-service apps/test-service/internal/server
go test ./apps/test-service/internal/instance ./apps/test-service/internal/runtime ./apps/test-service/cmd/unit-test-service ./apps/test-service/internal/server
go test ./apps/test-service/...
go test -race ./apps/test-service/...
```

预期：全部 PASS；当前平台没有残留 service/fixture 进程或临时 artifact。

- [ ] **Step 10：提交 Runtime 组装**

```sh
git add apps/test-service/internal/instance apps/test-service/internal/runtime apps/test-service/cmd/unit-test-service apps/test-service/internal/server
git commit -m "feat: recover persistent task runtime"
```

## Task 14：完成跨平台 E2E、CI、ADR 与文档门禁

**文件：**

- 修改：`tools/service-probe/src/probe.ts`
- 修改：`tools/service-probe/src/probe.test.ts`
- 修改：`tools/service-probe/build-service.mjs`
- 修改：`package.json`
- 修改：`.github/workflows/foundation.yml`
- 创建：`docs/decisions/0002-task-engine-event-journal.md`
- 修改：`README.md`

**接口：**

- 输入：完整 Phase 2 Runtime 和 TypeScript 客户端。
- 产出：Windows/Linux 都执行的端到端验收；ADR 记录混合任务/事件架构；README 说明 Phase 2 能力、数据目录和完整验证命令。

- [ ] **Step 1：编写取消、重连、制品与重启 E2E**

把 probe 的 service lifecycle 抽成 `startService(binary, directory)`，每次启动创建新 token/token file 和 endpoint，但复用 `dataDir`。新增测试流程：

```ts
test("task survives reconnect, cancels its tree, persists history and artifact", async () => {
  const fixture = await startTaskService(binary);
  try {
    const client = fixture.client;
    const subscription = await client.subscribeEvents(0);
    const running = await client.startTask({ idempotencyKey: randomBytes(16).toString("hex"), scenario: "spawn-child", timeoutMs: 30_000 });
    const childPID = await waitForChildPID(subscription, running.taskId);
    const beforeReconnect = subscription.lastSequence;
    await client.reconnect();
    assert.equal(subscription.lastSequence >= beforeReconnect, true);
    await client.cancelTask(running.taskId);
    const finished = await waitForFinished(subscription, running.taskId);
    assert.equal(finished.outcome, "cancelled");
    await assertProcessGone(childPID);
    const artifacts = await client.listArtifacts(running.taskId);
    assert.equal(artifacts.items.length, 1);
    const summary = JSON.parse(new TextDecoder().decode(await client.readArtifact(artifacts.items[0].artifactId)));
    assert.equal(summary.outcome, "cancelled");
    await fixture.stopGracefully();
    const restarted = await fixture.restart();
    assert.equal((await restarted.client.getTask(running.taskId)).outcome, "cancelled");
  } finally {
    await fixture.dispose();
  }
});
```

第二个 E2E 启动 `hang` 任务后强制结束 service 进程，重新启动同一 dataDir，断言任务为 `finished/interrupted`，历史事件包含且只包含一个恢复产生的 `task.finished`，没有 `test_failed` 字符串。

- [ ] **Step 2：运行 E2E 并确认 Phase 2 尚未连通**

运行：`pnpm test:e2e`

预期：FAIL，首个缺失点是 `--data-dir`、`subscribeEvents`、`tasks/start` 或任务能力字段。

- [ ] **Step 3：实现可重启 Probe fixture**

`startService` 必须：在启动前调用现有 `prepareTokenFile`；传递 `--endpoint`、`--token-file`、`--data-dir`；等待 READY；认证 `1.1`；保存 stderr；暴露 `stopGracefully`、`kill`、`restart`、`dispose`。所有等待使用有名称的 5–10 秒 timeout，失败消息包含已脱敏 stdout/stderr，但绝不包含 token。

`assertProcessGone(pid)` 使用 `process.kill(pid, 0)` 轮询；只有 `ESRCH` 视为已退出。Windows 对已退出 PID 的平台差异必须由测试辅助函数规范化，不能调用 `taskkill` 或 Shell。

- [ ] **Step 4：增加根验证命令与 CI race 门禁**

根 `package.json` 增加：

```json
"test:go:race": "go test -race ./apps/test-service/...",
"verify": "pnpm check:protocol-generated && pnpm build && pnpm test && pnpm test:go:race && pnpm test:e2e"
```

`.github/workflows/foundation.yml` 保留 Windows/Ubuntu matrix 和固定 Node/pnpm/Go 版本，把零散验证步骤替换为 `pnpm verify`，设置 job `timeout-minutes: 30`，最后继续运行 `git diff --exit-code`。不得把平台进程树测试标记为 skip。

- [ ] **Step 5：记录架构决策**

`docs/decisions/0002-task-engine-event-journal.md` 使用“状态、上下文、决策、后果”结构，明确记录：

- TaskManager 是运行状态唯一写入者。
- SQLite 保存快照与追加事件，但不是调度队列。
- 事件是至少一次交付，客户端按 sequence 去重。
- 同实例重连重放，重启后 interrupted。
- Windows Job Object 与 Linux Process Group/Host 的失败关闭行为。
- 选择 `modernc.org/sqlite` 1.54.0 和固定 `modernc.org/libc` 1.74.1，以避免 CGO 并保持两平台构建一致。
- 未选择完整 Event Sourcing 或 SQLite 中心调度器的理由。

- [ ] **Step 6：更新 README**

README 首段改为 Phase 2，说明当前只执行受控模拟任务，不执行工作区/CMake/编译器。新增：

- `--data-dir` 目录结构：`history.sqlite3`、`artifacts/`、`service.lock`。
- 协议 `1.0`/`1.1` 兼容方式。
- 任务状态/结果分类，特别说明没有 `test_failed`。
- Windows Job Object、Linux Process Group 与服务重启恢复语义。
- 本地完整验证命令 `pnpm verify`。

- [ ] **Step 7：运行完整本地门禁**

使用仓库固定版本运行：

```sh
pnpm install --frozen-lockfile
pnpm verify
git diff --check
git status --short
```

预期：所有单元、契约、竞态、集成和当前平台 E2E 测试 PASS；`git diff --check` 无输出；提交前 `git status --short` 只显示本任务预期文件。

- [ ] **Step 8：提交 E2E 与文档**

```sh
git add package.json pnpm-lock.yaml .github/workflows/foundation.yml tools/service-probe docs/decisions/0002-task-engine-event-journal.md README.md
git commit -m "test: verify phase 2 task lifecycle"
```

- [ ] **Step 9：推送叠加分支、创建 Draft PR 并验证 GitHub Actions**

运行：

```sh
git push -u github codex/task-engine-persistence
pr_url="$(gh pr create --base codex/foundation-protocol-service --head codex/task-engine-persistence --draft --title 'Phase 2: task engine and persistence' --body 'Stacked on Phase 1. Adds the cross-platform simulated-task engine, event replay, SQLite history, artifacts, and process-tree cancellation.')"
gh pr checks "$pr_url" --watch --fail-fast
```

预期：创建一个以 Phase 1 分支为 base 的 Draft PR；Windows 和 Ubuntu job 都成功；两者都执行进程树终止和完整 `pnpm verify`，没有 skipped platform test。Phase 1 合并后，把该 PR 的 base 改为 `master` 并确认差异只包含 Phase 2。

---

## 最终验收清单

- [ ] `1.0` 客户端仍能 handshake、查询旧能力和关闭服务。
- [ ] `1.1` 客户端只能提交五种受控模拟场景，不能提交 Shell 或任意命令。
- [ ] `queued/running/cancelling/finished` 状态机和六种 outcome 全部由测试覆盖。
- [ ] 任务快照、事件、租约和 Artifact metadata 在 SQLite 中事务一致。
- [ ] EventBroker 从持久化水位无缺口切换到实时事件，慢订阅者不阻塞任务。
- [ ] Windows Job Object 和 Linux Process Group 均能终止孙进程。
- [ ] cancel/timeout 的首个终止原因稳定，进程退出码不能覆盖它。
- [ ] 客户端重连后按 sequence 去重并恢复遗漏事件。
- [ ] 摘要 Artifact 通过 ID 分块读取并完成 size/SHA-256 校验。
- [ ] 服务重启先清理 lease/临时文件，再把活动任务记为 interrupted，最后开放 IPC。
- [ ] owner-only 数据目录和单实例锁在 Windows/Linux 均经过平台测试。
- [ ] `pnpm verify`、`git diff --check`、Windows CI 和 Ubuntu CI 全部通过。
- [ ] 工作树干净，生成文件已提交，Phase 2 没有 CMake、工具链、测试框架、覆盖率或 Code-OSS UI 实现。
