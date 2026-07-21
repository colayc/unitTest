# Foundation Protocol and Local Test Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a cross-platform vertical slice in which a TypeScript client authenticates to a Go service over Windows Named Pipe or Linux Unix Socket, queries capabilities, and shuts the service down through a versioned JSON protocol.

**Architecture:** JSON Schema 2020-12 is the protocol source of truth. A deterministic generator produces the shared `Capabilities` model for TypeScript and Go; manually maintained envelopes contain transport semantics. The Go service owns IPC, authentication, and request dispatch, while the TypeScript package is transport-aware but independent of Code-OSS so the extension can consume it in a later phase.

**Tech Stack:** Node.js 24.18.0 LTS, pnpm 11.4.0, TypeScript 6.0.3, `@types/node` 24.13.3, Go 1.26.5, JSON Schema 2020-12, quicktype 24.0.0, Ajv 8.20.0, ajv-formats 3.0.1, and `github.com/Microsoft/go-winio` 0.6.2.

## Global Constraints

- The first release supports Windows/MSVC, Windows/clang-cl coverage, Linux/GCC, and Linux/Clang.
- The service must not depend on Electron, DOM, Code-OSS objects, or shell-command strings.
- The TypeScript boundary uses workspace URIs; platform-native paths remain inside the Go service.
- IPC is Windows Named Pipe on Windows and Unix Socket with mode `0600` on Linux.
- Every connection must authenticate before any non-handshake request.
- Protocol version `1.0` is the only accepted version in this phase.
- Messages use UTF-8 NDJSON framing and have a maximum encoded line size of 1 MiB.
- Protocol errors use stable codes separate from user-facing text.
- Generated protocol files are committed and CI fails when regeneration changes them.
- This phase does not execute workspace programs, compilers, CMake, or tests.

---

## Locked file structure

```text
.editorconfig
.gitattributes
.gitignore
.go-version
.node-version
go.work
package.json
pnpm-workspace.yaml
tsconfig.base.json
apps/test-service/
├── go.mod
├── cmd/unit-test-service/main.go
└── internal/
    ├── protocol/envelope.go
    ├── protocol/envelope_test.go
    ├── protocolmodel/generated.go
    ├── server/server.go
    ├── server/server_test.go
    ├── session/session.go
    ├── session/session_test.go
    └── transport/
        ├── listener.go
        ├── listener_unix.go
        ├── listener_windows.go
        └── listener_unix_test.go
packages/protocol-schema/
├── package.json
├── schema/v1/capabilities.schema.json
├── schema/v1/message.schema.json
├── fixtures/v1/handshake.valid.json
├── fixtures/v1/handshake-missing-token.invalid.json
└── test/schema.test.mjs
packages/protocol-models/
├── package.json
├── tsconfig.json
└── src/
    ├── generated/capabilities.ts
    ├── generated-contract.test.ts
    └── index.ts
packages/test-client/
├── package.json
├── tsconfig.json
└── src/
    ├── client.ts
    ├── client.test.ts
    ├── envelopes.ts
    └── index.ts
tools/protocol-gen/generate.mjs
tools/service-probe/
├── package.json
├── tsconfig.json
├── build-service.mjs
└── src/
    ├── endpoint.ts
    ├── probe.ts
    └── probe.test.ts
tools/workspace-smoke/workspace-smoke.test.mjs
.github/workflows/foundation.yml
docs/decisions/0001-local-ipc-and-protocol-v1.md
```

### Task 1: Bootstrap the polyglot workspace

**Files:**

- Create: `.editorconfig`
- Create: `.gitattributes`
- Create: `.gitignore`
- Create: `.go-version`
- Create: `.node-version`
- Create: `package.json`
- Create: `pnpm-workspace.yaml`
- Create: `tsconfig.base.json`
- Create: `go.work`
- Create: `apps/test-service/go.mod`
- Create: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**

- Consumes: none.
- Produces: root `pnpm test`, `pnpm build`, `pnpm generate:protocol`, and `pnpm check:protocol-generated` commands used by every later task.

- [ ] **Step 1: Write the failing workspace smoke test**

```js
// tools/workspace-smoke/workspace-smoke.test.mjs
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

test("workspace pins supported toolchains", async () => {
  assert.equal((await readFile(".node-version", "utf8")).trim(), "24.18.0");
  assert.equal((await readFile(".go-version", "utf8")).trim(), "1.26.5");
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(manifest.packageManager, "pnpm@11.4.0");
  assert.equal(manifest.engines.node, ">=24.18.0 <25");
});
```

- [ ] **Step 2: Run the smoke test and verify the missing bootstrap fails**

Run: `node --test tools/workspace-smoke/workspace-smoke.test.mjs`

Expected: FAIL with `ENOENT` for `.node-version`.

- [ ] **Step 3: Add the workspace manifests and formatting rules**

```json
{
  "name": "unit-test-ide",
  "private": true,
  "type": "module",
  "packageManager": "pnpm@11.4.0",
  "engines": {
    "node": ">=24.18.0 <25",
    "pnpm": "11.4.0"
  },
  "scripts": {
    "build": "pnpm -r build && go build ./apps/test-service/...",
    "generate:protocol": "node tools/protocol-gen/generate.mjs",
    "check:protocol-generated": "node tools/protocol-gen/generate.mjs --check",
    "test:workspace": "node --test tools/workspace-smoke/workspace-smoke.test.mjs",
    "test:go": "go test ./apps/test-service/...",
    "test": "pnpm run test:workspace && pnpm -r --if-present test && pnpm run test:go"
  },
  "devDependencies": {
    "@types/node": "24.13.3",
    "ajv": "8.20.0",
    "ajv-formats": "3.0.1",
    "quicktype": "24.0.0",
    "typescript": "6.0.3"
  }
}
```

```yaml
packages:
  - "packages/*"
  - "tools/service-probe"
```

```json
{
  "compilerOptions": {
    "composite": true,
    "declaration": true,
    "esModuleInterop": true,
    "forceConsistentCasingInFileNames": true,
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "noUncheckedIndexedAccess": true,
    "outDir": "dist",
    "rootDir": "src",
    "strict": true,
    "target": "ES2023"
  }
}
```

`.node-version`:

```text
24.18.0
```

`.go-version`:

```text
1.26.5
```

`go.work`:

```text
go 1.26.0

use ./apps/test-service
```

```go
module unit-test-ide.local/test-service

go 1.26.0

require github.com/Microsoft/go-winio v0.6.2
```

```ini
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
indent_style = space
indent_size = 2

[*.go]
indent_style = tab

[*.md]
trim_trailing_whitespace = false
```

```gitattributes
* text=auto
*.go text eol=lf
*.json text eol=lf
*.md text eol=lf
*.ts text eol=lf
*.yaml text eol=lf
```

```gitignore
build/
dist/
node_modules/
*.tsbuildinfo
*.profraw
*.profdata
*.sock
```

- [ ] **Step 4: Install dependencies and verify the workspace**

Run: `corepack enable`

Run: `corepack prepare pnpm@11.4.0 --activate`

Run: `pnpm install`

Run: `node --test tools/workspace-smoke/workspace-smoke.test.mjs`

Expected: one passing test and a committed `pnpm-lock.yaml`.

- [ ] **Step 5: Commit the bootstrap**

```bash
git add .editorconfig .gitattributes .gitignore .go-version .node-version go.work package.json pnpm-lock.yaml pnpm-workspace.yaml tsconfig.base.json apps/test-service/go.mod tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "build: bootstrap TypeScript and Go workspace"
```

### Task 2: Define protocol v1 and contract validation

**Files:**

- Create: `docs/decisions/0001-local-ipc-and-protocol-v1.md`
- Create: `packages/protocol-schema/package.json`
- Create: `packages/protocol-schema/schema/v1/capabilities.schema.json`
- Create: `packages/protocol-schema/schema/v1/message.schema.json`
- Create: `packages/protocol-schema/fixtures/v1/handshake.valid.json`
- Create: `packages/protocol-schema/fixtures/v1/handshake-missing-token.invalid.json`
- Create: `packages/protocol-schema/test/schema.test.mjs`

**Interfaces:**

- Consumes: root Ajv dependencies from Task 1.
- Produces: protocol version `1.0`, methods `handshake`, `capabilities/get`, and `shutdown`, plus error codes `INVALID_MESSAGE`, `UNSUPPORTED_PROTOCOL`, `AUTH_REQUIRED`, `AUTH_FAILED`, and `METHOD_NOT_FOUND`.

- [ ] **Step 1: Add valid and invalid handshake fixtures**

```json
{
  "protocolVersion": "1.0",
  "kind": "request",
  "messageId": "0123456789abcdef0123456789abcdef",
  "method": "handshake",
  "sentAt": "2026-07-21T00:00:00Z",
  "payload": {
    "token": "base64url-token",
    "clientName": "service-probe",
    "clientVersion": "0.1.0"
  }
}
```

```json
{
  "protocolVersion": "1.0",
  "kind": "request",
  "messageId": "0123456789abcdef0123456789abcdef",
  "method": "handshake",
  "sentAt": "2026-07-21T00:00:00Z",
  "payload": {
    "clientName": "service-probe",
    "clientVersion": "0.1.0"
  }
}
```

- [ ] **Step 2: Write the contract test and verify the absent schemas fail**

```js
// packages/protocol-schema/test/schema.test.mjs
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
```

Run: `node --test packages/protocol-schema/test/schema.test.mjs`

Expected: FAIL with `ENOENT` for `message.schema.json`.

- [ ] **Step 3: Add the exact protocol schemas**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1:capabilities",
  "title": "Capabilities",
  "type": "object",
  "additionalProperties": false,
  "required": ["platform", "transports", "toolchains", "frameworks", "coverageTools"],
  "properties": {
    "platform": { "type": "string", "pattern": "^(windows|linux)$" },
    "transports": { "type": "array", "items": { "type": "string", "pattern": "^(named-pipe|unix-socket)$" } },
    "toolchains": { "type": "array", "items": { "type": "string" } },
    "frameworks": { "type": "array", "items": { "type": "string" } },
    "coverageTools": { "type": "array", "items": { "type": "string" } }
  }
}
```

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:protocol:v1:message",
  "title": "ProtocolMessage",
  "oneOf": [
    { "$ref": "#/$defs/handshakeRequest" },
    { "$ref": "#/$defs/emptyRequest" },
    { "$ref": "#/$defs/response" },
    { "$ref": "#/$defs/error" }
  ],
  "$defs": {
    "baseProperties": {
      "type": "object",
      "properties": {
        "protocolVersion": { "const": "1.0" },
        "messageId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
        "sentAt": { "type": "string", "format": "date-time" }
      },
      "required": ["protocolVersion", "messageId", "sentAt"]
    },
    "handshakeRequest": {
      "allOf": [
        { "$ref": "#/$defs/baseProperties" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "request" },
            "method": { "const": "handshake" },
            "payload": {
              "type": "object",
              "additionalProperties": false,
              "required": ["token", "clientName", "clientVersion"],
              "properties": {
                "token": { "type": "string", "minLength": 16 },
                "clientName": { "type": "string", "minLength": 1 },
                "clientVersion": { "type": "string", "minLength": 1 }
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
        { "$ref": "#/$defs/baseProperties" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "request" },
            "method": { "enum": ["capabilities/get", "shutdown"] },
            "payload": { "type": "object", "maxProperties": 0 }
          },
          "required": ["kind", "method", "payload"]
        }
      ],
      "unevaluatedProperties": false
    },
    "response": {
      "allOf": [
        { "$ref": "#/$defs/baseProperties" },
        {
          "type": "object",
          "properties": {
            "kind": { "const": "response" },
            "requestId": { "type": "string", "pattern": "^[0-9a-f]{32}$" },
            "method": { "enum": ["handshake", "capabilities/get", "shutdown"] },
            "payload": { "type": "object" }
          },
          "required": ["kind", "requestId", "method", "payload"]
        }
      ],
      "unevaluatedProperties": false
    },
    "error": {
      "allOf": [
        { "$ref": "#/$defs/baseProperties" },
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
                "code": { "enum": ["INVALID_MESSAGE", "UNSUPPORTED_PROTOCOL", "AUTH_REQUIRED", "AUTH_FAILED", "METHOD_NOT_FOUND"] },
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

```json
{
  "name": "@unit-test-ide/protocol-schema",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": {
    "./v1/message": "./schema/v1/message.schema.json",
    "./v1/capabilities": "./schema/v1/capabilities.schema.json"
  },
  "scripts": {
    "test": "node --test test/schema.test.mjs"
  },
  "devDependencies": {
    "ajv": "8.20.0",
    "ajv-formats": "3.0.1"
  }
}
```

- [ ] **Step 4: Record the transport decision and run the contract test**

Write `docs/decisions/0001-local-ipc-and-protocol-v1.md` with these binding decisions: per-user Go service; Named Pipe on Windows; Unix Socket mode `0600` on Linux; NDJSON framing; 1 MiB line limit; handshake first; token supplied through a mode-restricted file and deleted after the service reads it; protocol `1.0`; error codes from the Schema; disconnect ends only that client session, not the service process.

Run: `pnpm install`

Run: `pnpm --filter @unit-test-ide/protocol-schema test`

Expected: one passing contract test.

- [ ] **Step 5: Commit the protocol contract**

```bash
git add docs/decisions/0001-local-ipc-and-protocol-v1.md packages/protocol-schema pnpm-lock.yaml
git commit -m "feat(protocol): define local IPC contract v1"
```

### Task 3: Generate TypeScript and Go capability models

**Files:**

- Create: `tools/protocol-gen/generate.mjs`
- Create: `packages/protocol-models/package.json`
- Create: `packages/protocol-models/tsconfig.json`
- Create: `packages/protocol-models/src/index.ts`
- Create: `packages/protocol-models/src/generated-contract.test.ts`
- Generate: `packages/protocol-models/src/generated/capabilities.ts`
- Generate: `apps/test-service/internal/protocolmodel/generated.go`

**Interfaces:**

- Consumes: `capabilities.schema.json` from Task 2.
- Produces: TypeScript `Capabilities` and Go `protocolmodel.Capabilities` with fields `platform`, `transports`, `toolchains`, `frameworks`, and `coverageTools`.

- [ ] **Step 1: Write the TypeScript type contract before generation**

```ts
// packages/protocol-models/src/generated-contract.test.ts
import test from "node:test";
import assert from "node:assert/strict";
import type { Capabilities } from "./index.js";

test("generated capabilities represent an empty Windows service", () => {
  const value: Capabilities = {
    platform: "windows",
    transports: ["named-pipe"],
    toolchains: [],
    frameworks: [],
    coverageTools: []
  };
  assert.equal(value.platform, "windows");
});
```

Run: `pnpm --filter @unit-test-ide/protocol-models test`

Expected: FAIL because the package and generated model do not exist.

- [ ] **Step 2: Add the deterministic generator**

```js
// tools/protocol-gen/generate.mjs
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";

const check = process.argv.includes("--check");
const root = resolve(import.meta.dirname, "../..");
const schema = join(root, "packages/protocol-schema/schema/v1/capabilities.schema.json");
const targets = [
  {
    output: join(root, "packages/protocol-models/src/generated/capabilities.ts"),
    args: ["--lang", "typescript", "--just-types", "--top-level", "Capabilities"],
    format: null
  },
  {
    output: join(root, "apps/test-service/internal/protocolmodel/generated.go"),
    args: ["--lang", "go", "--just-types", "--package", "protocolmodel", "--top-level", "Capabilities"],
    format: "gofmt"
  }
];
const temp = await mkdtemp(join(tmpdir(), "unit-test-ide-protocol-"));
const pnpm = process.platform === "win32" ? "pnpm.cmd" : "pnpm";

try {
  for (const [index, target] of targets.entries()) {
    const output = check ? join(temp, String(index)) : target.output;
    await mkdir(dirname(output), { recursive: true });
    const result = spawnSync(pnpm, ["exec", "quicktype", "--src-lang", "schema", "--src", schema, ...target.args, "--out", output], { cwd: root, stdio: "inherit" });
    if (result.status !== 0) process.exit(result.status ?? 1);
    if (target.format === "gofmt") {
      const formatted = spawnSync("gofmt", ["-w", output], { cwd: root, stdio: "inherit" });
      if (formatted.status !== 0) process.exit(formatted.status ?? 1);
    }
    if (check) {
      const normalize = (value) => value.replaceAll("\r\n", "\n");
      if (normalize(await readFile(output, "utf8")) !== normalize(await readFile(target.output, "utf8"))) {
        throw new Error(`Generated file is stale: ${target.output}`);
      }
    }
  }
} finally {
  await rm(temp, { recursive: true, force: true });
}
```

- [ ] **Step 3: Add the TypeScript model package and generate both languages**

```json
{
  "name": "@unit-test-ide/protocol-models",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": "./dist/index.js",
  "types": "./dist/index.d.ts",
  "scripts": {
    "build": "tsc -b tsconfig.json",
    "test": "pnpm run build && node --test dist/generated-contract.test.js"
  },
  "devDependencies": {
    "@types/node": "24.13.3",
    "typescript": "6.0.3"
  }
}
```

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "dist" },
  "include": ["src/**/*.ts"]
}
```

```ts
// packages/protocol-models/src/index.ts
export type { Capabilities } from "./generated/capabilities.js";
```

Run: `pnpm install`

Run: `pnpm generate:protocol`

Run: `pnpm --filter @unit-test-ide/protocol-models test`

Run: `pnpm check:protocol-generated`

Expected: TypeScript test passes and generation check exits 0 without changing files.

- [ ] **Step 4: Verify the generated Go model compiles**

Run: `go test ./apps/test-service/internal/protocolmodel`

Expected: package compiles with no test files.

- [ ] **Step 5: Commit generated models and generator**

```bash
git add tools/protocol-gen packages/protocol-models apps/test-service/internal/protocolmodel/generated.go pnpm-lock.yaml
git commit -m "feat(protocol): generate TypeScript and Go capability models"
```

### Task 4: Implement Go envelopes and authenticated session dispatch

**Files:**

- Create: `apps/test-service/internal/protocol/envelope.go`
- Create: `apps/test-service/internal/protocol/envelope_test.go`
- Create: `apps/test-service/internal/session/session.go`
- Create: `apps/test-service/internal/session/session_test.go`

**Interfaces:**

- Consumes: `protocolmodel.Capabilities` from Task 3.
- Produces: `protocol.DecodeRequest([]byte)`, `session.New(token, platform, transport)`, `(*Session).Handle(request)`, and `(*Session).ShutdownRequested()`.

- [ ] **Step 1: Write failing codec and authentication tests**

```go
// apps/test-service/internal/session/session_test.go
package session_test

import (
	"encoding/json"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

func request(t *testing.T, method string, payload any) protocol.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil { t.Fatal(err) }
	return protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: method, Payload: raw}
}

func TestSessionRequiresHandshakeThenReturnsCapabilities(t *testing.T) {
	s := session.New("0123456789abcdef", "windows", "named-pipe")
	before := s.Handle(request(t, "capabilities/get", map[string]any{}))
	if before.Kind != "error" || before.Error.Code != "AUTH_REQUIRED" { t.Fatalf("unexpected response: %#v", before) }
	accepted := s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	if accepted.Kind != "response" { t.Fatalf("handshake failed: %#v", accepted) }
	capabilities := s.Handle(request(t, "capabilities/get", map[string]any{}))
	if capabilities.Kind != "response" { t.Fatalf("capabilities failed: %#v", capabilities) }
}

func TestSessionRejectsWrongToken(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	result := s.Handle(request(t, "handshake", map[string]string{"token": "wrong-token-value", "clientName": "test", "clientVersion": "0.1.0"}))
	if result.Error.Code != "AUTH_FAILED" { t.Fatalf("unexpected response: %#v", result) }
}
```

Run: `go test ./apps/test-service/internal/session -v`

Expected: FAIL because the protocol and session packages do not exist.

- [ ] **Step 2: Implement strict request decoding and response envelopes**

```go
// apps/test-service/internal/protocol/envelope.go
package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const Version = "1.0"

type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Kind            string          `json:"kind"`
	MessageID       string          `json:"messageId"`
	Method          string          `json:"method"`
	SentAt          string          `json:"sentAt,omitempty"`
	Payload         json.RawMessage `json:"payload"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Response struct {
	ProtocolVersion string      `json:"protocolVersion"`
	Kind            string      `json:"kind"`
	MessageID       string      `json:"messageId"`
	RequestID       string      `json:"requestId"`
	Method          string      `json:"method,omitempty"`
	SentAt          string      `json:"sentAt"`
	Payload         any         `json:"payload,omitempty"`
	Error           *ErrorBody  `json:"error,omitempty"`
}

func DecodeRequest(line []byte) (Request, error) {
	var value Request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil { return Request{}, err }
	if err := decoder.Decode(&struct{}{}); err != io.EOF { return Request{}, errors.New("multiple JSON values") }
	if value.Kind != "request" || value.MessageID == "" || value.Method == "" { return Request{}, errors.New("missing request fields") }
	return value, nil
}

func Success(request Request, payload any) Response {
	return Response{ProtocolVersion: Version, Kind: "response", MessageID: newID(), RequestID: request.MessageID, Method: request.Method, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

func Failure(request Request, code, message string, retryable bool) Response {
	return Response{ProtocolVersion: Version, Kind: "error", MessageID: newID(), RequestID: request.MessageID, SentAt: time.Now().UTC().Format(time.RFC3339Nano), Error: &ErrorBody{Code: code, Message: message, Retryable: retryable}}
}

func newID() string { var value [16]byte; if _, err := rand.Read(value[:]); err != nil { panic(err) }; return hex.EncodeToString(value[:]) }
```

- [ ] **Step 3: Implement the session state machine**

```go
// apps/test-service/internal/session/session.go
package session

import (
	"crypto/subtle"
	"encoding/json"
	"sync"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/protocolmodel"
)

type Session struct { token, platform, transport string; mu sync.Mutex; authenticated bool; shutdown chan struct{}; shutdownOnce sync.Once }
type handshake struct { Token string `json:"token"`; ClientName string `json:"clientName"`; ClientVersion string `json:"clientVersion"` }

func New(token, platform, transport string) *Session { return &Session{token: token, platform: platform, transport: transport, shutdown: make(chan struct{})} }
func (s *Session) ShutdownRequested() <-chan struct{} { return s.shutdown }

func (s *Session) Handle(request protocol.Request) protocol.Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.ProtocolVersion != protocol.Version { return protocol.Failure(request, "UNSUPPORTED_PROTOCOL", "protocol version is not supported", false) }
	if !s.authenticated && request.Method != "handshake" { return protocol.Failure(request, "AUTH_REQUIRED", "handshake must be completed first", false) }
	switch request.Method {
	case "handshake":
		var payload handshake
		if err := json.Unmarshal(request.Payload, &payload); err != nil { return protocol.Failure(request, "INVALID_MESSAGE", "invalid handshake payload", false) }
		if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(s.token)) != 1 { return protocol.Failure(request, "AUTH_FAILED", "authentication failed", false) }
		s.authenticated = true
		return protocol.Success(request, map[string]string{"negotiatedProtocolVersion": protocol.Version, "serviceVersion": "0.1.0"})
	case "capabilities/get":
		return protocol.Success(request, protocolmodel.Capabilities{Platform: s.platform, Transports: []string{s.transport}, Toolchains: []string{}, Frameworks: []string{}, CoverageTools: []string{}})
	case "shutdown":
		s.shutdownOnce.Do(func() { close(s.shutdown) })
		return protocol.Success(request, map[string]bool{"accepted": true})
	default:
		return protocol.Failure(request, "METHOD_NOT_FOUND", "method is not supported", false)
	}
}
```

- [ ] **Step 4: Add the remaining codec and state-machine cases**

```go
// apps/test-service/internal/protocol/envelope_test.go
package protocol_test

import (
	"testing"
	"unit-test-ide.local/test-service/internal/protocol"
)

func TestDecodeRequestRejectsUnknownField(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","payload":{},"unknown":true}`))
	if err == nil { t.Fatal("expected unknown field to fail") }
}

func TestDecodeRequestRejectsMissingMessageID(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","method":"shutdown","payload":{}}`))
	if err == nil { t.Fatal("expected missing messageId to fail") }
}

func TestDecodeRequestRejectsTrailingJSON(t *testing.T) {
	_, err := protocol.DecodeRequest([]byte(`{"protocolVersion":"1.0","kind":"request","messageId":"0123456789abcdef0123456789abcdef","method":"shutdown","payload":{}} {}`))
	if err == nil { t.Fatal("expected trailing JSON to fail") }
}
```

Append these cases to `session_test.go`:

```go
func TestSessionRejectsUnknownMethodAfterAuthentication(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	result := s.Handle(request(t, "unknown", map[string]any{}))
	if result.Error.Code != "METHOD_NOT_FOUND" { t.Fatalf("unexpected response: %#v", result) }
}

func TestShutdownClosesSignalOnce(t *testing.T) {
	s := session.New("0123456789abcdef", "linux", "unix-socket")
	_ = s.Handle(request(t, "handshake", map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"}))
	_ = s.Handle(request(t, "shutdown", map[string]any{}))
	_ = s.Handle(request(t, "shutdown", map[string]any{}))
	select { case <-s.ShutdownRequested(): default: t.Fatal("shutdown signal was not closed") }
}
```

Run: `gofmt -w apps/test-service/internal/protocol apps/test-service/internal/session`

Run: `go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session -v`

Expected: all codec and session tests pass.

- [ ] **Step 5: Commit the Go protocol session**

```bash
git add apps/test-service/internal/protocol apps/test-service/internal/session
git commit -m "feat(service): add authenticated protocol session"
```

### Task 5: Add platform IPC and the Go service executable

**Files:**

- Create: `apps/test-service/internal/transport/listener.go`
- Create: `apps/test-service/internal/transport/listener_unix.go`
- Create: `apps/test-service/internal/transport/listener_windows.go`
- Create: `apps/test-service/internal/transport/listener_unix_test.go`
- Create: `apps/test-service/internal/server/server.go`
- Create: `apps/test-service/internal/server/server_test.go`
- Create: `apps/test-service/cmd/unit-test-service/main.go`

**Interfaces:**

- Consumes: `protocol.DecodeRequest` and `session.Session` from Task 4.
- Produces: `transport.Listen(endpoint)`, `transport.PlatformName()`, `transport.TransportName()`, and executable flags `--endpoint` and `--token-file`.

- [ ] **Step 1: Write a failing connection-server test**

```go
// apps/test-service/internal/server/server_test.go
package server_test

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
)

func exchange(t *testing.T, connection net.Conn, request protocol.Request) protocol.Response {
	t.Helper()
	if err := json.NewEncoder(connection).Encode(request); err != nil { t.Fatal(err) }
	var response protocol.Response
	if err := json.NewDecoder(connection).Decode(&response); err != nil { t.Fatal(err) }
	return response
}

func TestServeConnectionHandlesHandshakeAndShutdown(t *testing.T) {
	client, service := net.Pipe()
	active := session.New("0123456789abcdef", "linux", "unix-socket")
	go server.ServeConnection(service, active)
	defer client.Close()
	handshake, _ := json.Marshal(map[string]string{"token": "0123456789abcdef", "clientName": "test", "clientVersion": "0.1.0"})
	response := exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "0123456789abcdef0123456789abcdef", Method: "handshake", Payload: handshake})
	if response.Kind != "response" { t.Fatalf("handshake failed: %#v", response) }
	shutdown := exchange(t, client, protocol.Request{ProtocolVersion: "1.0", Kind: "request", MessageID: "fedcba9876543210fedcba9876543210", Method: "shutdown", Payload: json.RawMessage(`{}`)})
	if shutdown.Kind != "response" { t.Fatalf("shutdown failed: %#v", shutdown) }
}

func TestServeConnectionRejectsOversizedLine(t *testing.T) {
	client, service := net.Pipe()
	go server.ServeConnection(service, session.New("0123456789abcdef", "linux", "unix-socket"))
	defer client.Close()
	go func() { _, _ = client.Write(append(bytes.Repeat([]byte("x"), server.MaxMessageBytes+1), '\n')) }()
	var response protocol.Response
	if err := json.NewDecoder(client).Decode(&response); err != nil { t.Fatal(err) }
	if response.Error == nil || response.Error.Code != "INVALID_MESSAGE" { t.Fatalf("unexpected response: %#v", response) }
}
```

Run: `go test ./apps/test-service/internal/server -v`

Expected: FAIL because `server.ServeConnection` does not exist.

- [ ] **Step 2: Implement IPC listener variants**

```go
// apps/test-service/internal/transport/listener.go
package transport

import "net"

func Listen(endpoint string) (net.Listener, error) { return listen(endpoint) }
```

```go
// apps/test-service/internal/transport/listener_windows.go
//go:build windows

package transport

import (
	"fmt"
	"net"
	"os/user"
	"github.com/Microsoft/go-winio"
)

func listen(endpoint string) (net.Listener, error) {
	current, err := user.Current()
	if err != nil { return nil, err }
	sddl := fmt.Sprintf("D:P(A;;GA;;;%s)", current.Uid)
	return winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: sddl})
}
func PlatformName() string { return "windows" }
func TransportName() string { return "named-pipe" }
```

```go
// apps/test-service/internal/transport/listener_unix.go
//go:build !windows

package transport

import (
	"net"
	"os"
)

func listen(endpoint string) (net.Listener, error) {
	_ = os.Remove(endpoint)
	listener, err := net.Listen("unix", endpoint)
	if err != nil { return nil, err }
	if err := os.Chmod(endpoint, 0o600); err != nil { listener.Close(); return nil, err }
	return listener, nil
}
func PlatformName() string { return "linux" }
func TransportName() string { return "unix-socket" }
```

```go
// apps/test-service/internal/transport/listener_unix_test.go
//go:build !windows

package transport_test

import (
	"os"
	"path/filepath"
	"testing"

	"unit-test-ide.local/test-service/internal/transport"
)

func TestUnixSocketIsOwnerOnly(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "service.sock")
	listener, err := transport.Listen(endpoint)
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	info, err := os.Stat(endpoint)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0o600 { t.Fatalf("mode = %o", info.Mode().Perm()) }
}
```

- [ ] **Step 3: Implement bounded NDJSON serving**

```go
// apps/test-service/internal/server/server.go
package server

import (
	"bufio"
	"encoding/json"
	"net"

	"unit-test-ide.local/test-service/internal/protocol"
	"unit-test-ide.local/test-service/internal/session"
)

const MaxMessageBytes = 1024 * 1024

func ServeConnection(connection net.Conn, active *session.Session) {
	defer connection.Close()
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 64*1024), MaxMessageBytes)
	encoder := json.NewEncoder(connection)
	for scanner.Scan() {
		request, err := protocol.DecodeRequest(scanner.Bytes())
		if err != nil {
			_ = encoder.Encode(protocol.Failure(protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message is invalid", false))
			return
		}
		if err := encoder.Encode(active.Handle(request)); err != nil { return }
		select { case <-active.ShutdownRequested(): return; default: }
	}
	if scanner.Err() != nil {
		_ = encoder.Encode(protocol.Failure(protocol.Request{MessageID: "00000000000000000000000000000000"}, "INVALID_MESSAGE", "message exceeds the 1 MiB limit", false))
	}
}
```

- [ ] **Step 4: Implement service startup, token consumption, and graceful exit**

```go
// apps/test-service/cmd/unit-test-service/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"unit-test-ide.local/test-service/internal/server"
	"unit-test-ide.local/test-service/internal/session"
	"unit-test-ide.local/test-service/internal/transport"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("unit-test-service", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "local IPC endpoint")
	tokenFile := flags.String("token-file", "", "authentication token file")
	if err := flags.Parse(args); err != nil { return 2 }
	if *endpoint == "" || *tokenFile == "" { fmt.Fprintln(stderr, "--endpoint and --token-file are required"); return 2 }
	rawToken, err := os.ReadFile(*tokenFile)
	if err != nil { fmt.Fprintln(stderr, err); return 1 }
	token := strings.TrimSpace(string(rawToken))
	if len(token) < 16 { fmt.Fprintln(stderr, "authentication token must contain at least 16 characters"); return 1 }
	if err := os.Remove(*tokenFile); err != nil { fmt.Fprintln(stderr, err); return 1 }
	listener, err := transport.Listen(*endpoint)
	if err != nil { fmt.Fprintln(stderr, err); return 1 }
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	go func() { select { case <-ctx.Done(): case <-shutdown: }; _ = listener.Close() }()
	fmt.Fprintf(stdout, "READY %s\n", *endpoint)

	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			select { case <-ctx.Done(): return 0; case <-shutdown: return 0; default: fmt.Fprintln(stderr, acceptErr); return 1 }
		}
		active := session.New(token, transport.PlatformName(), transport.TransportName())
		go func() {
			server.ServeConnection(connection, active)
			select { case <-active.ShutdownRequested(): shutdownOnce.Do(func() { close(shutdown) }); default: }
		}()
	}
}
```

Run: `go mod tidy`

Run: `gofmt -w apps/test-service`

Run: `go test ./apps/test-service/...`

Run: `go build ./apps/test-service/cmd/unit-test-service`

Expected: all Go tests pass and the service binary builds.

- [ ] **Step 5: Commit the IPC service**

```bash
git add apps/test-service
git commit -m "feat(service): serve protocol over local IPC"
```

### Task 6: Build the reusable TypeScript protocol client

**Files:**

- Create: `packages/test-client/package.json`
- Create: `packages/test-client/tsconfig.json`
- Create: `packages/test-client/src/envelopes.ts`
- Create: `packages/test-client/src/client.ts`
- Create: `packages/test-client/src/client.test.ts`
- Create: `packages/test-client/src/index.ts`

**Interfaces:**

- Consumes: `Capabilities` from `@unit-test-ide/protocol-models` and protocol Schema from `@unit-test-ide/protocol-schema`.
- Produces: `ProtocolClient.connect(endpoint)`, test-only `ProtocolClient.attach(stream)`, `handshake(token, clientName, clientVersion)`, `getCapabilities()`, `shutdown()`, and `close()`.

- [ ] **Step 1: Write failing client tests with an in-memory duplex pair**

```ts
// packages/test-client/src/client.test.ts
import assert from "node:assert/strict";
import { Duplex, PassThrough } from "node:stream";
import { createInterface } from "node:readline";
import test from "node:test";
import { MAX_MESSAGE_BYTES, ProtocolClient } from "./client.js";
import { ProtocolError } from "./envelopes.js";

function pair(): [Duplex, Duplex] {
  const leftToRight = new PassThrough();
  const rightToLeft = new PassThrough();
  const create = (incoming: PassThrough, outgoing: PassThrough) => {
    const value = new Duplex({
      read() {},
      write(chunk, encoding, callback) { outgoing.write(chunk, encoding, callback); },
      final(callback) { outgoing.end(); callback(); }
    });
    incoming.on("data", (chunk) => value.push(chunk));
    incoming.on("end", () => value.push(null));
    return value;
  };
  return [create(rightToLeft, leftToRight), create(leftToRight, rightToLeft)];
}

function response(request: Record<string, unknown>, payload: Record<string, unknown>) {
  return { protocolVersion: "1.0", kind: "response", messageId: "fedcba9876543210fedcba9876543210", requestId: request.messageId, method: request.method, sentAt: "2026-07-21T00:00:00Z", payload };
}

test("client performs handshake, capabilities, and shutdown in order", async () => {
  const [clientStream, serverStream] = pair();
  const methods: string[] = [];
  createInterface({ input: serverStream }).on("line", (line) => {
    const request = JSON.parse(line);
    methods.push(request.method);
    const payload = request.method === "handshake"
      ? { negotiatedProtocolVersion: "1.0", serviceVersion: "0.1.0" }
      : request.method === "capabilities/get"
        ? { platform: "windows", transports: ["named-pipe"], toolchains: [], frameworks: [], coverageTools: [] }
        : { accepted: true };
    serverStream.write(`${JSON.stringify(response(request, payload))}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await client.handshake("0123456789abcdef", "test", "0.1.0");
  assert.equal((await client.getCapabilities()).platform, "windows");
  await client.shutdown();
  assert.deepEqual(methods, ["handshake", "capabilities/get", "shutdown"]);
  client.close();
});

test("client exposes stable server error codes", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", (line) => {
    const request = JSON.parse(line);
    serverStream.write(`${JSON.stringify({ protocolVersion: "1.0", kind: "error", messageId: "fedcba9876543210fedcba9876543210", requestId: request.messageId, sentAt: "2026-07-21T00:00:00Z", error: { code: "AUTH_FAILED", message: "authentication failed", retryable: false } })}\n`);
  });
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("wrong-token-value", "test", "0.1.0"), (error: unknown) => error instanceof ProtocolError && error.code === "AUTH_FAILED");
  client.close();
});

test("client rejects lines larger than 1 MiB", async () => {
  const [clientStream, serverStream] = pair();
  createInterface({ input: serverStream }).once("line", () => serverStream.write(`${"x".repeat(MAX_MESSAGE_BYTES + 1)}\n`));
  const client = ProtocolClient.attach(clientStream);
  await assert.rejects(() => client.handshake("0123456789abcdef", "test", "0.1.0"), /1 MiB/);
  client.close();
});
```

Run: `pnpm --filter @unit-test-ide/test-client test`

Expected: FAIL because the package does not exist.

- [ ] **Step 2: Define TypeScript envelopes and errors**

```ts
// packages/test-client/src/envelopes.ts
export type Method = "handshake" | "capabilities/get" | "shutdown";
export interface RequestEnvelope { protocolVersion: "1.0"; kind: "request"; messageId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ResponseEnvelope { protocolVersion: "1.0"; kind: "response"; messageId: string; requestId: string; method: Method; sentAt: string; payload: Record<string, unknown>; }
export interface ErrorEnvelope { protocolVersion: "1.0"; kind: "error"; messageId: string; requestId: string; sentAt: string; error: { code: string; message: string; retryable: boolean }; }
export type IncomingEnvelope = ResponseEnvelope | ErrorEnvelope;
export class ProtocolError extends Error { constructor(readonly code: string, message: string, readonly retryable: boolean) { super(message); } }
```

- [ ] **Step 3: Implement bounded NDJSON correlation and public methods**

```ts
// packages/test-client/src/client.ts
import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { createRequire } from "node:module";
import net from "node:net";
import type { Duplex } from "node:stream";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import type { Capabilities } from "@unit-test-ide/protocol-models";
import type { ErrorEnvelope, IncomingEnvelope, Method, RequestEnvelope, ResponseEnvelope } from "./envelopes.js";
import { ProtocolError } from "./envelopes.js";

export const MAX_MESSAGE_BYTES = 1024 * 1024;
const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);
const validateMessage = ajv.compile(require("@unit-test-ide/protocol-schema/v1/message"));
const validateCapabilities = ajv.compile(require("@unit-test-ide/protocol-schema/v1/capabilities"));
type Pending = { method: Method; resolve: (payload: Record<string, unknown>) => void; reject: (error: Error) => void };

export class ProtocolClient {
  static attach(stream: Duplex): ProtocolClient { return new ProtocolClient(stream); }
  static async connect(endpoint: string): Promise<ProtocolClient> {
    const socket = net.createConnection(endpoint);
    await once(socket, "connect");
    return new ProtocolClient(socket);
  }

  readonly #pending = new Map<string, Pending>();
  #buffer = Buffer.alloc(0);
  #authenticated = false;
  #closed = false;

  private constructor(private readonly stream: Duplex) {
    stream.on("data", (chunk: Buffer | string) => this.#onData(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)));
    stream.on("error", (error) => this.#failAll(error));
    stream.on("close", () => this.#failAll(new Error("service connection closed")));
  }

  async handshake(token: string, clientName: string, clientVersion: string): Promise<{ negotiatedProtocolVersion: "1.0"; serviceVersion: string }> {
    const payload = await this.#request("handshake", { token, clientName, clientVersion });
    if (payload.negotiatedProtocolVersion !== "1.0" || typeof payload.serviceVersion !== "string") throw new Error("invalid handshake response");
    this.#authenticated = true;
    return payload as { negotiatedProtocolVersion: "1.0"; serviceVersion: string };
  }

  async getCapabilities(): Promise<Capabilities> {
    this.#requireAuthentication();
    const payload = await this.#request("capabilities/get", {});
    if (!validateCapabilities(payload)) throw new Error(`invalid capabilities response: ${ajv.errorsText(validateCapabilities.errors)}`);
    return payload as Capabilities;
  }

  async shutdown(): Promise<void> { this.#requireAuthentication(); await this.#request("shutdown", {}); }
  close(): void { if (!this.#closed) { this.#closed = true; this.stream.destroy(); } }

  #requireAuthentication(): void { if (!this.#authenticated) throw new Error("handshake has not completed"); }

  #request(method: Method, payload: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (this.#closed) return Promise.reject(new Error("service connection is closed"));
    const messageId = randomUUID().replaceAll("-", "");
    const request: RequestEnvelope = { protocolVersion: "1.0", kind: "request", messageId, method, sentAt: new Date().toISOString(), payload };
    return new Promise((resolve, reject) => {
      this.#pending.set(messageId, { method, resolve, reject });
      this.stream.write(`${JSON.stringify(request)}\n`, (error) => { if (error) { this.#pending.delete(messageId); reject(error); } });
    });
  }

  #onData(chunk: Buffer): void {
    this.#buffer = Buffer.concat([this.#buffer, chunk]);
    for (;;) {
      const newline = this.#buffer.indexOf(0x0a);
      if (newline < 0) break;
      const line = this.#buffer.subarray(0, newline);
      this.#buffer = this.#buffer.subarray(newline + 1);
      if (line.byteLength > MAX_MESSAGE_BYTES) { this.#failAll(new Error("protocol line exceeds the 1 MiB limit")); this.close(); return; }
      this.#onLine(line.toString("utf8"));
    }
    if (this.#buffer.byteLength > MAX_MESSAGE_BYTES) { this.#failAll(new Error("protocol line exceeds the 1 MiB limit")); this.close(); }
  }

  #onLine(line: string): void {
    let value: unknown;
    try { value = JSON.parse(line); } catch { this.#failAll(new Error("service returned invalid JSON")); this.close(); return; }
    if (!validateMessage(value)) { this.#failAll(new Error(`service returned invalid protocol message: ${ajv.errorsText(validateMessage.errors)}`)); this.close(); return; }
    const message = value as IncomingEnvelope;
    const pending = this.#pending.get(message.requestId);
    if (!pending) return;
    this.#pending.delete(message.requestId);
    if (message.kind === "error") {
      const failure = message as ErrorEnvelope;
      pending.reject(new ProtocolError(failure.error.code, failure.error.message, failure.error.retryable));
      return;
    }
    const response = message as ResponseEnvelope;
    if (response.method !== pending.method) { pending.reject(new Error("response method does not match request")); return; }
    pending.resolve(response.payload);
  }

  #failAll(error: Error): void {
    for (const pending of this.#pending.values()) pending.reject(error);
    this.#pending.clear();
  }
}
```

- [ ] **Step 4: Add package configuration and run unit tests**

```json
{
  "name": "@unit-test-ide/test-client",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": "./dist/index.js",
  "types": "./dist/index.d.ts",
  "dependencies": {
    "@unit-test-ide/protocol-models": "workspace:*",
    "@unit-test-ide/protocol-schema": "workspace:*",
    "ajv": "8.20.0",
    "ajv-formats": "3.0.1"
  },
  "devDependencies": {
    "@types/node": "24.13.3",
    "typescript": "6.0.3"
  },
  "scripts": {
    "build": "tsc -b tsconfig.json",
    "test": "pnpm run build && node --test dist/client.test.js"
  }
}
```

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "dist" },
  "references": [{ "path": "../protocol-models" }],
  "include": ["src/**/*.ts"]
}
```

```ts
// packages/test-client/src/index.ts
export { ProtocolClient } from "./client.js";
export { ProtocolError } from "./envelopes.js";
```

Run: `pnpm install`

Run: `pnpm --filter @unit-test-ide/test-client test`

Expected: all client unit tests pass.

- [ ] **Step 5: Commit the reusable client**

```bash
git add packages/test-client pnpm-lock.yaml
git commit -m "feat(client): add authenticated local service client"
```

### Task 7: Prove the Windows/Linux end-to-end vertical slice

**Files:**

- Create: `tools/service-probe/package.json`
- Create: `tools/service-probe/tsconfig.json`
- Create: `tools/service-probe/build-service.mjs`
- Create: `tools/service-probe/src/endpoint.ts`
- Create: `tools/service-probe/src/probe.ts`
- Create: `tools/service-probe/src/probe.test.ts`
- Modify: `package.json`

**Interfaces:**

- Consumes: Go service executable and `ProtocolClient`.
- Produces: `pnpm test:e2e` and a diagnostic command that prints the service `Capabilities` JSON.

- [ ] **Step 1: Write the failing end-to-end test**

```ts
// tools/service-probe/src/probe.test.ts
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
```

Run: `pnpm --filter @unit-test-ide/service-probe test:e2e`

Expected: FAIL because the probe package does not exist.

- [ ] **Step 2: Implement endpoint and service lifecycle helpers**

```ts
// tools/service-probe/src/endpoint.ts
import { randomUUID } from "node:crypto";
import { join } from "node:path";

export function endpoint(tempDirectory: string): string {
  const id = randomUUID();
  return process.platform === "win32"
    ? `\\\\.\\pipe\\unit-test-ide-${id}`
    : join(tempDirectory, `unit-test-ide-${id}.sock`);
}
```

```ts
// tools/service-probe/src/probe.ts
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { access, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { createInterface } from "node:readline";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import type { Capabilities } from "@unit-test-ide/protocol-models";
import { ProtocolClient } from "@unit-test-ide/test-client";
import { endpoint } from "./endpoint.js";

function within<T>(promise: Promise<T>, milliseconds: number, label: string): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out after ${milliseconds}ms`)), milliseconds);
    promise.then((value) => { clearTimeout(timer); resolve(value); }, (error) => { clearTimeout(timer); reject(error); });
  });
}

function ready(child: ChildProcessWithoutNullStreams, expectedEndpoint: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const lines = createInterface({ input: child.stdout });
    lines.on("line", (line) => {
      if (line === `READY ${expectedEndpoint}`) { lines.close(); resolve(); }
    });
    child.once("error", reject);
    child.once("exit", (code) => { if (code !== null) reject(new Error(`service exited before READY with code ${code}`)); });
  });
}

export async function runProbe(serviceBinary: string): Promise<Capabilities> {
  const directory = await mkdtemp(join(tmpdir(), "unit-test-ide-probe-"));
  const token = randomBytes(32).toString("base64url");
  const tokenFile = join(directory, "token");
  const serviceEndpoint = endpoint(directory);
  let child: ChildProcessWithoutNullStreams | undefined;
  let client: ProtocolClient | undefined;
  let stdout = "";
  let stderr = "";
  try {
    await writeFile(tokenFile, token, { mode: 0o600 });
    child = spawn(serviceBinary, ["--endpoint", serviceEndpoint, "--token-file", tokenFile], { windowsHide: true });
    child.stdout.on("data", (chunk) => { stdout += String(chunk); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); });
    const exit = once(child, "exit") as Promise<[number | null, NodeJS.Signals | null]>;
    await within(ready(child, serviceEndpoint), 5000, "service startup");
    try {
      await access(tokenFile);
      throw new Error("service did not delete the token file after reading it");
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error;
    }
    client = await within(ProtocolClient.connect(serviceEndpoint), 5000, "service connection");
    await client.handshake(token, "service-probe", "0.1.0");
    const capabilities = await client.getCapabilities();
    await client.shutdown();
    const [code] = await within(exit, 5000, "service shutdown");
    if (code !== 0) throw new Error(`service exited with code ${code}; stdout=${stdout}; stderr=${stderr}`);
    return capabilities;
  } catch (error) {
    throw new Error(`${error instanceof Error ? error.message : String(error)}; stdout=${stdout}; stderr=${stderr}`);
  } finally {
    client?.close();
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill();
      await within(once(child, "exit").then(() => undefined), 1000, "forced service shutdown").catch(() => undefined);
    }
    await rm(directory, { recursive: true, force: true });
  }
}

const entry = process.argv[1];
if (entry && import.meta.url === pathToFileURL(entry).href) {
  const binary = process.argv[2];
  if (!binary) throw new Error("service binary path is required");
  console.log(JSON.stringify(await runProbe(binary)));
}
```

- [ ] **Step 3: Add cross-platform build and test scripts**

```js
// tools/service-probe/build-service.mjs
import { spawnSync } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
const build = join(root, "build");
await mkdir(build, { recursive: true });
const output = join(build, process.platform === "win32" ? "unit-test-service.exe" : "unit-test-service");
const result = spawnSync("go", ["build", "-o", output, "./apps/test-service/cmd/unit-test-service"], { cwd: root, stdio: "inherit" });
if (result.status !== 0) process.exit(result.status ?? 1);
```

```json
{
  "name": "@unit-test-ide/service-probe",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "dependencies": {
    "@unit-test-ide/protocol-models": "workspace:*",
    "@unit-test-ide/test-client": "workspace:*"
  },
  "devDependencies": {
    "@types/node": "24.13.3",
    "typescript": "6.0.3"
  },
  "scripts": {
    "build": "tsc -b tsconfig.json",
    "test:e2e": "node build-service.mjs && pnpm run build && node --test dist/probe.test.js"
  }
}
```

```json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "dist" },
  "references": [
    { "path": "../../packages/protocol-models" },
    { "path": "../../packages/test-client" }
  ],
  "include": ["src/**/*.ts"]
}
```

Add root script: `"test:e2e": "pnpm --filter @unit-test-ide/service-probe test:e2e"`.

- [ ] **Step 4: Run the complete local gate**

Run: `pnpm install`

Run: `pnpm check:protocol-generated`

Run: `pnpm build`

Run: `pnpm test`

Run: `pnpm test:e2e`

Expected: generated files are clean; all TypeScript and Go tests pass; the probe reports the current OS and expected IPC transport.

- [ ] **Step 5: Commit the vertical slice**

```bash
git add package.json pnpm-lock.yaml tools/service-probe
git commit -m "test: prove local service vertical slice"
```

### Task 8: Add CI gates and developer handoff documentation

**Files:**

- Create: `.github/workflows/foundation.yml`
- Modify: `README.md`

**Interfaces:**

- Consumes: all commands delivered by Tasks 1-7.
- Produces: required Windows/Linux CI verification and reproducible local setup instructions.

- [ ] **Step 1: Add the failing documentation check**

Append this test to `tools/workspace-smoke/workspace-smoke.test.mjs`:

```js
test("README contains the complete local verification gate", async () => {
  const readme = await readFile("README.md", "utf8");
  for (const command of ["pnpm check:protocol-generated", "pnpm test", "pnpm test:e2e"]) {
    assert.match(readme, new RegExp(command.replaceAll(" ", "\\s+")));
  }
});
```

Run: `pnpm test:workspace`

Expected: FAIL because the current README does not contain the commands.

- [ ] **Step 2: Write the developer README**

Use this complete Phase 1 README content:

````markdown
# C/C++ Unit Test IDE

Phase 1 provides the versioned protocol, reusable TypeScript client, and local Go service skeleton. It does not execute workspace code, CMake, compilers, or tests.

## Prerequisites

- Node.js 24.18.0
- pnpm 11.4.0 through Corepack
- Go 1.26.5

## Setup

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
```

## Verification

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:e2e
```

Protocol models are generated from `packages/protocol-schema/schema/v1`. Generated TypeScript and Go files are committed; edit the Schema and run `pnpm generate:protocol` instead of editing generated files.

The service listens on a random per-user Windows Named Pipe or a Linux Unix Socket with mode `0600`. Every connection must complete the token handshake before using another method.
````

- [ ] **Step 3: Add the Windows/Linux CI matrix**

```yaml
# .github/workflows/foundation.yml
name: foundation

on:
  pull_request:
  push:
    branches: [master]

jobs:
  verify:
    strategy:
      fail-fast: false
      matrix:
        os: [windows-latest, ubuntu-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v6
      - uses: pnpm/action-setup@v4
        with:
          version: 11.4.0
          run_install: false
      - uses: actions/setup-node@v6
        with:
          node-version: 24.18.0
          cache: pnpm
      - uses: actions/setup-go@v6
        with:
          go-version: 1.26.5
          cache-dependency-path: apps/test-service/go.sum
      - run: pnpm install --frozen-lockfile
      - run: pnpm check:protocol-generated
      - run: pnpm build
      - run: pnpm test
      - run: pnpm test:e2e
      - run: git diff --exit-code
```

- [ ] **Step 4: Run final Phase 1 verification**

Run: `pnpm install --frozen-lockfile`

Run: `pnpm check:protocol-generated`

Run: `pnpm build`

Run: `pnpm test`

Run: `pnpm test:e2e`

Run: `git diff --check`

Expected: every command exits 0; end-to-end output identifies Windows Named Pipe on Windows and Unix Socket on Linux; no generated drift or whitespace errors remain.

- [ ] **Step 5: Commit CI and documentation**

```bash
git add .github/workflows/foundation.yml README.md tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "ci: verify protocol foundation on Windows and Linux"
```

## Phase 1 completion evidence

Before declaring this plan complete, attach or retain:

- `pnpm check:protocol-generated` output.
- TypeScript and Go unit-test summaries.
- Windows end-to-end probe output showing `named-pipe`.
- Linux end-to-end probe output showing `unix-socket`.
- `git diff --exit-code` result after regeneration.
- The commit hashes for all eight tasks.

## Version references

- [Node.js release schedule](https://nodejs.org/en/about/previous-releases)
- [Go release history](https://go.dev/doc/devel/release)
- [TypeScript releases](https://github.com/microsoft/TypeScript/releases)
- [pnpm releases](https://github.com/pnpm/pnpm/releases)
- [quicktype JSON Schema generation](https://www.npmjs.com/package/quicktype)
- [Ajv JSON Schema validator](https://github.com/ajv-validator/ajv)
- [Microsoft go-winio](https://github.com/microsoft/go-winio)
