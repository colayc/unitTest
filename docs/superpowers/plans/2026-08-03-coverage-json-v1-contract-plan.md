# Coverage JSON v1 Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付独立于 Protocol version 的 Coverage JSON v1 closed Schema、deterministic TypeScript/Go generated models、双语言 semantic validator，以及默认本地/CI generated-drift 门禁。

**Architecture:** `packages/coverage-schema` 是 Coverage JSON wire shape 的唯一事实来源；`tools/coverage-gen` 从同一 Schema 生成 TypeScript 与 Go 类型。JSON Schema 负责 closed structural validation，`@unit-test-ide/coverage-models` 与 Go `coveragemodel/v1` 使用等价的 semantic validation 校验排序、URI、聚合关系和 `covered <= total`。

**Tech Stack:** JSON Schema Draft 2020-12、AJV 8.20.0、TypeScript 6.0.3、Node.js 24.18.0、Go 1.26.5、quicktype 24.0.0、pnpm 11.4.0。

## Global Constraints

- `schemaVersion` 固定为 `"1.0"`；Coverage JSON version 不与 Protocol version 绑定。
- canonical JSON body 不包含 run ID、artifact ID、timestamp、duration、native path、command、environment 或浮点 percentage。
- JSON integer 范围固定为 `0..9007199254740991`；超限必须失败，不能截断或转换为浮点数。
- `Metric` 只包含 `covered` 与 `total`，且 `covered <= total`。
- 顶层和 file summary 都只包含 `lines`、`branches`、`functions`。
- file URI 是 NFC UTF-8 workspace-relative canonical path：`/` 分隔，无空 segment、`.`/`..` segment、反斜杠、absolute/drive/UNC/URI scheme/query/fragment/NUL。
- `files` 按 URI 的 UTF-8 byte sequence 严格递增；`lines` 按 line number 严格递增。
- `available` 必须没有 reason；`partial` 必须至少包含一个 closed reason。
- v1.0–v1.3 Protocol Schema、generated model 和 runtime behavior 不得变化。
- 本计划不启动 CMake、compiler、CTest、test executable、Python、gcovr、`llvm-profdata` 或 `llvm-cov`。
- 所有步骤使用 red-green-refactor TDD；每个 Task 以独立绿色提交结束，并推送 GitHub 与 Gitee。

## File Responsibility Map

| 路径 | 单一职责 |
|---|---|
| `packages/coverage-schema/schema/v1/coverage.schema.json` | Coverage JSON v1 structural wire contract |
| `packages/coverage-schema/fixtures/v1/*.json` | TypeScript/Go 共用的 valid/invalid contract evidence |
| `packages/coverage-schema/test/schema.test.mjs` | Draft 2020-12 closed-schema 与 forbidden-field tests |
| `tools/coverage-gen/generate.mjs` | 从一个 Schema deterministic 生成 TypeScript/Go type |
| `packages/coverage-models/src/generated/coverage-v1.ts` | generated TypeScript wire type；禁止手改 |
| `packages/coverage-models/src/decoder.ts` | TypeScript Schema + semantic validation 与 defensive clone |
| `apps/test-service/internal/coveragemodel/v1/generated.go` | generated Go wire type；禁止手改 |
| `apps/test-service/internal/coveragemodel/v1/validate.go` | Go strict decode、semantic validation 与 clone |
| `package.json` | root generate/check scripts 与默认 verify drift gate |

---

### Task 1: Coverage JSON v1 closed Schema 与 fixtures

**Files:**

- Create: `packages/coverage-schema/package.json`
- Create: `packages/coverage-schema/schema/v1/coverage.schema.json`
- Create: `packages/coverage-schema/fixtures/v1/report.valid.json`
- Create: `packages/coverage-schema/fixtures/v1/report-native-path.invalid.json`
- Create: `packages/coverage-schema/fixtures/v1/report-float.invalid.json`
- Create: `packages/coverage-schema/fixtures/v1/report-unsafe-count.invalid.json`
- Create: `packages/coverage-schema/test/schema.test.mjs`

**Interfaces:**

- Consumes: JSON Schema Draft 2020-12；不依赖 Protocol package。
- Produces: package export `@unit-test-ide/coverage-schema/v1/coverage` 和 Schema ID `urn:unit-test-ide:coverage:v1`。
- Produces these exact wire names:

~~~ts
interface CoverageDocumentV1 {
  schemaVersion: "1.0";
  provenance: CoverageProvenanceV1;
  completeness: CoverageCompletenessV1;
  summary: CoverageSummaryV1;
  files: CoverageFileV1[];
}
interface CoverageProvenanceV1 {
  platform: "windows" | "linux";
  architecture: "x86" | "x64" | "arm64";
  compiler: { family: "gcc" | "clang" | "clang-cl"; version: string };
  driver: { name: "gcov" | "llvm-cov"; version: string };
  collector: { name: "gcovr" | "llvm-cov"; version: string };
  normalizerVersion: string;
  instrumentationFingerprint: string;
}
interface CoverageCompletenessV1 {
  outcome: "available" | "partial";
  reasons: Array<"test_crashed" | "test_timed_out" | "profile_missing_for_failed_invocation">;
}
interface CoverageSummaryV1 {
  lines: CoverageMetricV1;
  branches: CoverageMetricV1;
  functions: CoverageMetricV1;
}
interface CoverageMetricV1 { covered: number; total: number }
interface CoverageFileV1 {
  uri: string;
  sha256: string;
  summary: CoverageSummaryV1;
  lines: CoverageLineV1[];
}
interface CoverageLineV1 {
  line: number;
  count: number;
  branches: CoverageMetricV1;
}
~~~

- [ ] **Step 1: Write the failing Schema contract test**

Create `package.json`:

~~~json
{
  "name": "@unit-test-ide/coverage-schema",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": {
    "./v1/coverage": "./schema/v1/coverage.schema.json"
  },
  "scripts": {
    "test": "node --test test/schema.test.mjs"
  },
  "devDependencies": {
    "ajv": "8.20.0",
    "ajv-formats": "3.0.1"
  }
}
~~~

Create `report.valid.json`:

~~~json
{
  "schemaVersion": "1.0",
  "provenance": {
    "platform": "linux",
    "architecture": "x64",
    "compiler": { "family": "clang", "version": "22.1.0" },
    "driver": { "name": "llvm-cov", "version": "22.1.0" },
    "collector": { "name": "llvm-cov", "version": "22.1.0" },
    "normalizerVersion": "1.0.0",
    "instrumentationFingerprint": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  },
  "completeness": { "outcome": "available", "reasons": [] },
  "summary": {
    "lines": { "covered": 1, "total": 2 },
    "branches": { "covered": 1, "total": 2 },
    "functions": { "covered": 1, "total": 1 }
  },
  "files": [{
    "uri": "src/calculator.cpp",
    "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "summary": {
      "lines": { "covered": 1, "total": 2 },
      "branches": { "covered": 1, "total": 2 },
      "functions": { "covered": 1, "total": 1 }
    },
    "lines": [
      { "line": 10, "count": 1, "branches": { "covered": 1, "total": 2 } },
      { "line": 11, "count": 0, "branches": { "covered": 0, "total": 0 } }
    ]
  }]
}
~~~

Create the invalid fixtures using these exact changes:

| Fixture | Exact change |
|---|---|
| `report-native-path.invalid.json` | add top-level `"nativePath": "C:\\workspace\\src\\calculator.cpp"` |
| `report-float.invalid.json` | set `summary.lines.covered` to `0.5` |
| `report-unsafe-count.invalid.json` | set `summary.lines.total` to `9007199254740992` |

Create `schema.test.mjs`:

~~~js
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const load = async (relative) =>
  JSON.parse(await readFile(new URL(relative, import.meta.url), "utf8"));

test("Coverage JSON v1 accepts the canonical fixture and rejects structural violations", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  addFormats(ajv);
  const validate = ajv.compile(await load("../schema/v1/coverage.schema.json"));
  assert.equal(validate(await load("../fixtures/v1/report.valid.json")), true, JSON.stringify(validate.errors));
  for (const name of [
    "report-native-path.invalid.json",
    "report-float.invalid.json",
    "report-unsafe-count.invalid.json"
  ]) {
    assert.equal(validate(await load("../fixtures/v1/" + name)), false, name + " passed");
  }
});

test("Coverage JSON v1 rejects forbidden operational metadata", async () => {
  const ajv = new Ajv2020({ allErrors: true, strict: true });
  const validate = ajv.compile(await load("../schema/v1/coverage.schema.json"));
  const valid = await load("../fixtures/v1/report.valid.json");
  for (const [field, value] of Object.entries({
    runId: "11111111111111111111111111111111",
    artifactId: "22222222222222222222222222222222",
    timestamp: "2026-08-03T00:00:00Z",
    durationMs: 1,
    percentage: 50,
    command: "llvm-cov",
    environment: ["TOKEN=secret"]
  })) {
    assert.equal(validate({ ...valid, [field]: value }), false, "accepted " + field);
  }
});
~~~

- [ ] **Step 2: Run the Schema test to verify it fails**

Run:

~~~powershell
pnpm --filter @unit-test-ide/coverage-schema test
~~~

Expected: FAIL because `coverage.schema.json` does not exist; the red test is discovered through the new workspace package.

- [ ] **Step 3: Write the minimal closed Schema**

Create every object with `additionalProperties: false`. Use `0..9007199254740991` for metric/count/line integers, `^[0-9a-f]{64}$` for SHA-256/fingerprint, version strings of 1–128 bytes without NUL, `files.maxItems: 100000`, `lines.maxItems: 1000000` and `reasons.maxItems: 64` with `uniqueItems: true`.

Use this exact top-level and metric/summary composition:

~~~json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "urn:unit-test-ide:coverage:v1",
  "title": "CoverageDocumentV1",
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "provenance", "completeness", "summary", "files"],
  "properties": {
    "schemaVersion": { "const": "1.0" },
    "provenance": { "$ref": "#/$defs/provenance" },
    "completeness": { "$ref": "#/$defs/completeness" },
    "summary": { "$ref": "#/$defs/summary" },
    "files": {
      "type": "array",
      "maxItems": 100000,
      "items": { "$ref": "#/$defs/file" }
    }
  },
  "$defs": {
    "compiler": {
      "title": "CoverageCompilerV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["family", "version"],
      "properties": {
        "family": { "enum": ["gcc", "clang", "clang-cl"] },
        "version": { "type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\x00]+$" }
      }
    },
    "driver": {
      "title": "CoverageDriverV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "version"],
      "properties": {
        "name": { "enum": ["gcov", "llvm-cov"] },
        "version": { "type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\x00]+$" }
      }
    },
    "collector": {
      "title": "CoverageCollectorV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "version"],
      "properties": {
        "name": { "enum": ["gcovr", "llvm-cov"] },
        "version": { "type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\x00]+$" }
      }
    },
    "provenance": {
      "title": "CoverageProvenanceV1",
      "type": "object",
      "additionalProperties": false,
      "required": [
        "platform", "architecture", "compiler", "driver", "collector",
        "normalizerVersion", "instrumentationFingerprint"
      ],
      "properties": {
        "platform": { "enum": ["windows", "linux"] },
        "architecture": { "enum": ["x86", "x64", "arm64"] },
        "compiler": { "$ref": "#/$defs/compiler" },
        "driver": { "$ref": "#/$defs/driver" },
        "collector": { "$ref": "#/$defs/collector" },
        "normalizerVersion": {
          "type": "string", "minLength": 1, "maxLength": 128, "pattern": "^[^\\x00]+$"
        },
        "instrumentationFingerprint": { "type": "string", "pattern": "^[0-9a-f]{64}$" }
      }
    },
    "completeness": {
      "title": "CoverageCompletenessV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["outcome", "reasons"],
      "properties": {
        "outcome": { "enum": ["available", "partial"] },
        "reasons": {
          "type": "array",
          "maxItems": 64,
          "uniqueItems": true,
          "items": {
            "enum": ["test_crashed", "test_timed_out", "profile_missing_for_failed_invocation"]
          }
        }
      }
    },
    "metric": {
      "title": "CoverageMetricV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["covered", "total"],
      "properties": {
        "covered": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 },
        "total": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 }
      }
    },
    "summary": {
      "title": "CoverageSummaryV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["lines", "branches", "functions"],
      "properties": {
        "lines": { "$ref": "#/$defs/metric" },
        "branches": { "$ref": "#/$defs/metric" },
        "functions": { "$ref": "#/$defs/metric" }
      }
    },
    "line": {
      "title": "CoverageLineV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["line", "count", "branches"],
      "properties": {
        "line": { "type": "integer", "minimum": 1, "maximum": 9007199254740991 },
        "count": { "type": "integer", "minimum": 0, "maximum": 9007199254740991 },
        "branches": { "$ref": "#/$defs/metric" }
      }
    },
    "file": {
      "title": "CoverageFileV1",
      "type": "object",
      "additionalProperties": false,
      "required": ["uri", "sha256", "summary", "lines"],
      "properties": {
        "uri": {
          "type": "string",
          "minLength": 1,
          "maxLength": 4096,
          "pattern": "^(?!/)(?![A-Za-z]:)(?![A-Za-z][A-Za-z0-9+.-]*:)(?!.*[\\\\?#\\x00]).+$"
        },
        "sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
        "summary": { "$ref": "#/$defs/summary" },
        "lines": {
          "type": "array",
          "maxItems": 1000000,
          "items": { "$ref": "#/$defs/line" }
        }
      }
    }
  }
}
~~~

Do not add another `$defs` entry or free-form metadata map. Task 3 performs the full canonical URI and cross-field semantic checks that JSON Schema cannot express portably.

- [ ] **Step 4: Run the Schema test to verify it passes**

Run:

~~~powershell
pnpm --filter @unit-test-ide/coverage-schema test
~~~

Expected: PASS, two tests.

- [ ] **Step 5: Commit the structural contract**

~~~powershell
git add packages/coverage-schema
git commit -m "feat: define coverage json schema"
~~~

---

### Task 2: Deterministic TypeScript/Go model generation

**Files:**

- Create: `tools/coverage-gen/generate.mjs`
- Create: `tools/coverage-gen/generate.test.mjs`
- Create: `packages/coverage-models/package.json`
- Create: `packages/coverage-models/tsconfig.json`
- Create: `packages/coverage-models/src/index.ts`
- Create: `packages/coverage-models/src/generated/coverage-v1.ts`
- Create: `packages/coverage-models/src/generated-contract.test.ts`
- Create: `apps/test-service/internal/coveragemodel/v1/generated.go`
- Create: `apps/test-service/internal/coveragemodel/generated_contract_test.go`

**Interfaces:**

- Consumes: `urn:unit-test-ide:coverage:v1` and quicktype `24.0.0`.
- Produces: exact TypeScript names from Task 1 and Go package `coveragemodelv1` with identical JSON property names.
- Produces CLI `node tools/coverage-gen/generate.mjs` and non-mutating `--check`.

- [ ] **Step 1: Write the failing generated-contract and generator regression tests**

创建 `tools/coverage-gen/generate.test.mjs`，覆盖 raw-byte drift（CRLF、缺少末尾 LF）以及生成失败/替换失败时的原子性与临时文件清理。

Create `packages/coverage-models/package.json`:

~~~json
{
  "name": "@unit-test-ide/coverage-models",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "exports": "./dist/index.js",
  "types": "./dist/index.d.ts",
  "scripts": {
    "build": "tsc -b tsconfig.json",
    "test": "pnpm run build && node --test dist/generated-contract.test.js"
  },
  "dependencies": {
    "@unit-test-ide/coverage-schema": "workspace:*",
    "ajv": "8.20.0",
    "ajv-formats": "3.0.1"
  },
  "devDependencies": {
    "@types/node": "24.13.3",
    "typescript": "6.0.3"
  }
}
~~~

Create `tsconfig.json`:

~~~json
{
  "extends": "../../tsconfig.base.json",
  "compilerOptions": { "rootDir": "src", "outDir": "dist", "types": ["node"] },
  "include": ["src/**/*.ts"]
}
~~~

Create `generated-contract.test.ts`:

~~~ts
import assert from "node:assert/strict";
import test from "node:test";
import type {
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageMetricV1
} from "./generated/coverage-v1.js";

test("generated Coverage JSON v1 types expose stable wire names", () => {
  const metric: CoverageMetricV1 = { covered: 1, total: 2 };
  const file: CoverageFileV1 = {
    uri: "src/calculator.cpp",
    sha256: "b".repeat(64),
    summary: { lines: metric, branches: metric, functions: { covered: 1, total: 1 } },
    lines: [{ line: 10, count: 1, branches: metric }]
  };
  const document: CoverageDocumentV1 = {
    schemaVersion: "1.0",
    provenance: {
      platform: "linux",
      architecture: "x64",
      compiler: { family: "clang", version: "22.1.0" },
      driver: { name: "llvm-cov", version: "22.1.0" },
      collector: { name: "llvm-cov", version: "22.1.0" },
      normalizerVersion: "1.0.0",
      instrumentationFingerprint: "a".repeat(64)
    },
    completeness: { outcome: "available", reasons: [] },
    summary: file.summary,
    files: [file]
  };
  assert.equal(document.files[0]?.uri, "src/calculator.cpp");
});
~~~

Create this Go compile contract:

~~~go
package coveragemodel_test

import (
	"testing"

	coveragemodelv1 "unit-test-ide.local/test-service/internal/coveragemodel/v1"
)

func TestGeneratedCoverageV1UsesStableJSONFields(t *testing.T) {
	value := coveragemodelv1.CoverageMetricV1{Covered: 1, Total: 2}
	if value.Covered != 1 || value.Total != 2 {
		t.Fatalf("generated metric = %#v", value)
	}
}
~~~

- [ ] **Step 2: Run generated-contract tests to verify they fail**

~~~powershell
pnpm --filter @unit-test-ide/coverage-models test
go test ./apps/test-service/internal/coveragemodel/... -run Generated -count=1
node --test tools/coverage-gen/generate.test.mjs
~~~

Expected: commands FAIL because generated types and generator behavior do not exist.

- [ ] **Step 3: Write the deterministic generator**

Implement these exact generation targets:

~~~js
const targets = [
  {
    language: "typescript",
    output: "packages/coverage-models/src/generated/coverage-v1.ts",
    extra: ["--just-types", "--prefer-unions"]
  },
  {
    language: "go",
    output: "apps/test-service/internal/coveragemodel/v1/generated.go",
    extra: ["--package", "coveragemodelv1", "--just-types"]
  }
];
~~~

Invoke repository quicktype through `process.execPath` with `--src-lang schema`、`--top-level CoverageDocumentV1` and the Task 1 Schema. Normalize UTF-8、LF and one final newline. Normal mode atomically replaces both outputs. `--check` generates both under one `mkdtemp` directory, byte-compares them with committed outputs, lists every drifted repository path, and removes the temporary directory in `finally`.

Export the generated TypeScript names from `src/index.ts`:

~~~ts
export type {
  CoverageCompletenessV1,
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageLineV1,
  CoverageMetricV1,
  CoverageProvenanceV1,
  CoverageSummaryV1
} from "./generated/coverage-v1.js";
~~~

- [ ] **Step 4: Generate models and verify contract tests pass**

~~~powershell
node tools/coverage-gen/generate.mjs
node --test tools/coverage-gen/generate.test.mjs
pnpm --filter @unit-test-ide/coverage-models test
go test ./apps/test-service/internal/coveragemodel/... -run Generated -count=1
node tools/coverage-gen/generate.mjs --check
~~~

Expected: PASS; `--check` makes no worktree change.

- [ ] **Step 5: Commit generated models**

~~~powershell
git add tools/coverage-gen packages/coverage-models apps/test-service/internal/coveragemodel
git commit -m "feat: generate coverage json models"
~~~

---

### Task 3: TypeScript Schema decoder 与 semantic validation

**Files:**

- Modify: `packages/coverage-models/package.json`
- Create: `packages/coverage-models/src/decoder.ts`
- Create: `packages/coverage-models/src/decoder.test.ts`
- Modify: `packages/coverage-models/src/index.ts`

**Interfaces:**

- Consumes: `CoverageDocumentV1` and `@unit-test-ide/coverage-schema/v1/coverage`.
- Produces: `decodeCoverageDocumentV1(value: unknown): CoverageDocumentV1`.
- Produces: `validateCoverageDocumentV1(value: CoverageDocumentV1): void`.
- Errors begin with `invalid Coverage JSON v1:`; no function returns a partial object.

- [ ] **Step 1: Write the failing TypeScript semantic tests**

Change the package test script to:

~~~json
"test": "pnpm run build && node --test dist/generated-contract.test.js dist/decoder.test.js"
~~~

~~~ts
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { decodeCoverageDocumentV1 } from "./decoder.js";

const fixture = async () =>
  JSON.parse(await readFile(
    new URL("../../coverage-schema/fixtures/v1/report.valid.json", import.meta.url),
    "utf8"
  ));

test("decoder returns a defensive canonical clone", async () => {
  const input = await fixture();
  const decoded = decodeCoverageDocumentV1(input);
  input.files[0].uri = "mutated.cpp";
  assert.equal(decoded.files[0]?.uri, "src/calculator.cpp");
});

test("decoder rejects cross-field and ordering violations", async () => {
  const valid = await fixture();
  const mutations = [
    (value: any) => { value.summary.lines.covered = 3; },
    (value: any) => { value.files[0].summary.lines.total = 3; },
    (value: any) => { value.files[0].lines.reverse(); },
    (value: any) => { value.files[0].uri = "../outside.cpp"; },
    (value: any) => { value.completeness = { outcome: "available", reasons: ["test_crashed"] }; }
  ];
  for (const mutate of mutations) {
    const candidate = structuredClone(valid);
    mutate(candidate);
    assert.throws(() => decodeCoverageDocumentV1(candidate), /invalid Coverage JSON v1:/);
  }
});
~~~

- [ ] **Step 2: Run TypeScript decoder tests to verify they fail**

~~~powershell
pnpm --filter @unit-test-ide/coverage-models test
~~~

Expected: FAIL because `decoder.ts` and its exports do not exist.

- [ ] **Step 3: Write the minimal TypeScript decoder**

Compile the Schema with AJV `allErrors: true`、`strict: true`. In one semantic walk enforce:

1. every metric has `covered <= total`；
2. every addition remains a safe integer；
3. URI obeys the Global Constraints and is NFC；
4. files are strict UTF-8 byte sorted；
5. line numbers are strictly increasing；
6. file line metric equals line-record count and covered records；
7. file branch metric equals the sum of line branch metrics；
8. top-level metrics equal the sum of file summaries；
9. `available` reasons is empty and `partial` reasons is non-empty；
10. file URI and line number are unique。

Load the JSON Schema through its package export so TypeScript does not require `resolveJsonModule`:

~~~ts
import { createRequire } from "node:module";
import { Ajv2020, type ValidateFunction } from "ajv/dist/2020.js";
import type {
  CoverageCompletenessV1,
  CoverageDocumentV1,
  CoverageFileV1,
  CoverageMetricV1,
  CoverageSummaryV1
} from "./generated/coverage-v1.js";

const require = createRequire(import.meta.url);
const ajv = new Ajv2020({ allErrors: true, strict: true });
const validateSchema = ajv.compile(
  require("@unit-test-ide/coverage-schema/v1/coverage")
) as ValidateFunction;
~~~

Start the public implementation exactly as follows:

~~~ts
export function decodeCoverageDocumentV1(value: unknown): CoverageDocumentV1 {
  if (!validateSchema(value)) {
    throw new Error("invalid Coverage JSON v1: " + ajv.errorsText(validateSchema.errors));
  }
  const result = structuredClone(value) as CoverageDocumentV1;
  validateCoverageDocumentV1(result);
  return result;
}

export function validateCoverageDocumentV1(value: CoverageDocumentV1): void {
  assertCompleteness(value.completeness);
  assertSummary(value.summary, "summary");
  let aggregate = emptySummary();
  let previousURI;
  for (const file of value.files) {
    assertCanonicalURI(file.uri);
    if (previousURI !== undefined &&
        Buffer.compare(Buffer.from(previousURI, "utf8"), Buffer.from(file.uri, "utf8")) >= 0) {
      fail("files are not strictly sorted by URI");
    }
    previousURI = file.uri;
    assertFile(file);
    aggregate = addSummary(aggregate, file.summary);
  }
  if (!sameSummary(aggregate, value.summary)) {
    fail("summary does not equal the sum of file summaries");
  }
}
~~~

Implement the private helpers with these exact signatures and invariants:

~~~ts
function fail(message: string): never {
  throw new Error("invalid Coverage JSON v1: " + message);
}

function assertMetric(value: CoverageMetricV1, field: string): void {
  if (!Number.isSafeInteger(value.covered) || !Number.isSafeInteger(value.total) ||
      value.covered < 0 || value.total < 0 || value.covered > value.total) {
    fail(field + " is not a valid metric");
  }
}

function addMetric(first: CoverageMetricV1, second: CoverageMetricV1): CoverageMetricV1 {
  const result = {
    covered: first.covered + second.covered,
    total: first.total + second.total
  };
  assertMetric(result, "aggregated metric");
  return result;
}

function emptySummary(): CoverageSummaryV1 {
  return {
    lines: { covered: 0, total: 0 },
    branches: { covered: 0, total: 0 },
    functions: { covered: 0, total: 0 }
  };
}

function addSummary(first: CoverageSummaryV1, second: CoverageSummaryV1): CoverageSummaryV1 {
  return {
    lines: addMetric(first.lines, second.lines),
    branches: addMetric(first.branches, second.branches),
    functions: addMetric(first.functions, second.functions)
  };
}

function sameSummary(first: CoverageSummaryV1, second: CoverageSummaryV1): boolean {
  return first.lines.covered === second.lines.covered &&
    first.lines.total === second.lines.total &&
    first.branches.covered === second.branches.covered &&
    first.branches.total === second.branches.total &&
    first.functions.covered === second.functions.covered &&
    first.functions.total === second.functions.total;
}

function assertSummary(value: CoverageSummaryV1, field: string): void {
  assertMetric(value.lines, field + ".lines");
  assertMetric(value.branches, field + ".branches");
  assertMetric(value.functions, field + ".functions");
}

function assertCompleteness(value: CoverageCompletenessV1): void {
  if (value.outcome === "available" && value.reasons.length !== 0 ||
      value.outcome === "partial" && value.reasons.length === 0) {
    fail("completeness outcome and reasons are inconsistent");
  }
}

function assertCanonicalURI(uri: string): void {
  const segments = uri.split("/");
  if (uri.length === 0 || uri !== uri.normalize("NFC") ||
      uri.startsWith("/") || uri.includes("\\") || uri.includes("?") ||
      uri.includes("#") || uri.includes("\0") || uri.includes("//") ||
      /^[A-Za-z]:/.test(uri) || /^[A-Za-z][A-Za-z0-9+.-]*:/.test(uri) ||
      segments.some((segment) => segment === "" || segment === "." || segment === "..")) {
    fail("file URI is not canonical");
  }
}

function assertFile(file: CoverageFileV1): void {
  assertSummary(file.summary, "file.summary");
  let previousLine = 0;
  let coveredLines = 0;
  let branches: CoverageMetricV1 = { covered: 0, total: 0 };
  for (const line of file.lines) {
    if (!Number.isSafeInteger(line.line) || line.line <= previousLine ||
        !Number.isSafeInteger(line.count) || line.count < 0) {
      fail("file lines are not canonical");
    }
    previousLine = line.line;
    if (line.count > 0) coveredLines++;
    assertMetric(line.branches, "line.branches");
    branches = addMetric(branches, line.branches);
  }
  const lines = { covered: coveredLines, total: file.lines.length };
  if (lines.covered !== file.summary.lines.covered ||
      lines.total !== file.summary.lines.total ||
      branches.covered !== file.summary.branches.covered ||
      branches.total !== file.summary.branches.total) {
    fail("file summary does not match line records");
  }
}
~~~

Export both public functions from `src/index.ts`.

- [ ] **Step 4: Run TypeScript tests to verify they pass**

~~~powershell
pnpm --filter @unit-test-ide/coverage-models test
~~~

Expected: generated-contract and decoder tests PASS.

- [ ] **Step 5: Commit the TypeScript validator**

~~~powershell
git add packages/coverage-models/src
git commit -m "feat: validate coverage json in typescript"
~~~

---

### Task 4: Go strict decoder 与 semantic validation

**Files:**

- Create: `apps/test-service/internal/coveragemodel/v1/validate.go`
- Create: `apps/test-service/internal/coveragemodel/v1/validate_test.go`

**Interfaces:**

- Consumes: generated `CoverageDocumentV1`.
- Produces: `Decode(data []byte) (CoverageDocumentV1, error)`、`Validate(value CoverageDocumentV1) error`、`Clone(value CoverageDocumentV1) CoverageDocumentV1`.
- Errors wrap `ErrInvalidDocument`; Go implements the same ten checks as Task 3.

- [ ] **Step 1: Write the failing Go decoder tests**

~~~go
package coveragemodelv1

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func validFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "..",
		"packages", "coverage-schema", "fixtures", "v1", "report.valid.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodeValidatesAndClonesCoverageDocument(t *testing.T) {
	value, err := Decode(validFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	clone := Clone(value)
	clone.Files[0].URI = "mutated.cpp"
	if value.Files[0].URI != "src/calculator.cpp" {
		t.Fatalf("original URI mutated: %q", value.Files[0].URI)
	}
}

func TestValidateRejectsCoveredAboveTotal(t *testing.T) {
	value, err := Decode(validFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	value.Summary.Lines.Covered = value.Summary.Lines.Total + 1
	if err := Validate(value); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("Validate() error = %v", err)
	}
}
~~~

- [ ] **Step 2: Run Go decoder tests to verify they fail**

~~~powershell
go test ./apps/test-service/internal/coveragemodel/... -run 'Decode|Validate' -count=1
~~~

Expected: FAIL because the decoder API does not exist.

- [ ] **Step 3: Write the minimal Go decoder**

Use `json.Decoder.DisallowUnknownFields()`, reject a second JSON value, call `Validate`, and return `Clone`. Use `golang.org/x/text/unicode/norm` for NFC and `bytes.Compare` for UTF-8 ordering.

~~~go
var ErrInvalidDocument = errors.New("invalid Coverage JSON v1")

func Decode(data []byte) (CoverageDocumentV1, error) {
	var value CoverageDocumentV1
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return CoverageDocumentV1{}, fmt.Errorf("%w: decode: %v", ErrInvalidDocument, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return CoverageDocumentV1{}, fmt.Errorf("%w: trailing JSON value", ErrInvalidDocument)
	}
	if err := Validate(value); err != nil {
		return CoverageDocumentV1{}, err
	}
	return Clone(value), nil
}

func addSafe(first, second int64) (int64, error) {
	if first < 0 || second < 0 ||
		first > 9_007_199_254_740_991-second {
		return 0, ErrInvalidDocument
	}
	return first + second, nil
}
~~~

Implement `Validate` as one bounded walk. The generated enum fields are string-backed; compare their string form against these closed sets:

~~~go
func Validate(value CoverageDocumentV1) error {
	if value.SchemaVersion != "1.0" || !validProvenance(value.Provenance) ||
		!validCompleteness(value.Completeness) ||
		len(value.Files) > 100_000 {
		return invalid("document header")
	}
	if err := validateSummary(value.Summary); err != nil {
		return err
	}
	aggregate := CoverageSummaryV1{}
	var previousURI string
	for index, file := range value.Files {
		if !canonicalURI(file.URI) || !lowerHex(file.SHA256, 64) ||
			index > 0 && bytes.Compare([]byte(previousURI), []byte(file.URI)) >= 0 ||
			len(file.Lines) > 1_000_000 {
			return invalid("file identity or ordering")
		}
		previousURI = file.URI
		if err := validateSummary(file.Summary); err != nil {
			return err
		}
		var previousLine int64
		var coveredLines int64
		branches := CoverageMetricV1{}
		for _, line := range file.Lines {
			if line.Line < 1 || line.Line > 9_007_199_254_740_991 ||
				line.Line <= previousLine || line.Count < 0 ||
				line.Count > 9_007_199_254_740_991 {
				return invalid("line identity or count")
			}
			previousLine = line.Line
			if line.Count > 0 {
				coveredLines++
			}
			var err error
			branches, err = addMetric(branches, line.Branches)
			if err != nil {
				return err
			}
		}
		if file.Summary.Lines.Covered != coveredLines ||
			file.Summary.Lines.Total != int64(len(file.Lines)) ||
			file.Summary.Branches != branches {
			return invalid("file summary")
		}
		var err error
		aggregate, err = addSummary(aggregate, file.Summary)
		if err != nil {
			return err
		}
	}
	if aggregate != value.Summary {
		return invalid("document summary")
	}
	return nil
}
~~~

Create the helpers named above with these exact rules:

- `validProvenance` accepts platform `windows|linux`、architecture `x86|x64|arm64`、compiler `gcc|clang|clang-cl`、driver `gcov|llvm-cov` and collector `gcovr|llvm-cov`；all four version fields contain 1–128 valid UTF-8 bytes without NUL；fingerprint is lowercase 64 hex.
- `validCompleteness` accepts at most 64 unique reasons from `test_crashed|test_timed_out|profile_missing_for_failed_invocation`；`available` requires zero reasons and `partial` requires at least one.
- `validateSummary` calls `validateMetric` for lines/branches/functions；`validateMetric` enforces `0 <= covered <= total <= 9007199254740991`.
- `addMetric` uses `addSafe` on covered and total, then `validateMetric`；`addSummary` applies it to all three metrics.
- `canonicalURI` requires valid UTF-8 NFC via `norm.NFC.IsNormalString`, 1–4096 bytes, and rejects every path form listed in Global Constraints.
- `lowerHex` compares every byte against lowercase `0-9a-f`; it does not call a case-folding function.
- `invalid(message)` returns `fmt.Errorf("%w: %s", ErrInvalidDocument, message)`.

Implement `Clone` by assigning the struct and allocating each mutable slice:

~~~go
func Clone(value CoverageDocumentV1) CoverageDocumentV1 {
	result := value
	result.Completeness.Reasons = slices.Clone(value.Completeness.Reasons)
	result.Files = make([]CoverageFileV1, len(value.Files))
	for index, file := range value.Files {
		result.Files[index] = file
		result.Files[index].Lines = slices.Clone(file.Lines)
	}
	return result
}
~~~

Import the Go standard-library `slices` package; do not weaken `Clone` to reflection or JSON round-trip.

- [ ] **Step 4: Run Go and cross-language tests to verify they pass**

~~~powershell
go test ./apps/test-service/internal/coveragemodel/... -count=1
go test -race ./apps/test-service/internal/coveragemodel/... -count=1
pnpm --filter @unit-test-ide/coverage-models test
~~~

Expected: all commands PASS.

- [ ] **Step 5: Commit the Go validator**

~~~powershell
git add apps/test-service/internal/coveragemodel
git commit -m "feat: validate coverage json in go"
~~~

---

### Task 5: Root scripts、generated drift 与 slice gate

**Files:**

- Modify: `package.json:7-23`
- Modify: `pnpm-lock.yaml`
- Modify: `docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md:22-119`

**Interfaces:**

- Consumes: generator and validators from Tasks 1–4.
- Produces: `pnpm generate:coverage`、`pnpm check:coverage-generated`、`pnpm test:coverage-gen`; root `test` 必跑 generator regression，且 default `verify` ordering 保持 Protocol drift → Coverage drift → build → tests → Go race → service E2E.

- [ ] **Step 1: Write the failing root-script assertion**

Add to `generated-contract.test.ts`:

~~~ts
import { readFile } from "node:fs/promises";

test("root scripts gate Coverage generation drift and regressions", async () => {
  const root = JSON.parse(await readFile(new URL("../../../package.json", import.meta.url), "utf8"));
  assert.equal(root.scripts["generate:coverage"], "node tools/coverage-gen/generate.mjs");
  assert.equal(root.scripts["check:coverage-generated"], "node tools/coverage-gen/generate.mjs --check");
  assert.equal(root.scripts["test:coverage-gen"], "node --test tools/coverage-gen/generate.test.mjs");
  assert.equal(root.scripts.test,
    "pnpm run test:coverage-gen && pnpm run test:cmake-bundle && pnpm run test:workspace && pnpm -r --if-present test && pnpm run test:go");
  assert.equal(root.scripts.verify,
    "pnpm check:protocol-generated && pnpm check:coverage-generated && pnpm build && pnpm test && pnpm test:go:race && pnpm test:e2e");
});
~~~

- [ ] **Step 2: Run the root-script assertion to verify it fails**

~~~powershell
pnpm --filter @unit-test-ide/coverage-models test
~~~

Expected: FAIL because the root scripts do not exist.

- [ ] **Step 3: Add root scripts and lockfile importer entries**

Add:

~~~json
{
  "generate:coverage": "node tools/coverage-gen/generate.mjs",
  "check:coverage-generated": "node tools/coverage-gen/generate.mjs --check",
  "test:coverage-gen": "node --test tools/coverage-gen/generate.test.mjs",
  "test": "pnpm run test:coverage-gen && pnpm run test:cmake-bundle && pnpm run test:workspace && pnpm -r --if-present test && pnpm run test:go",
  "verify": "pnpm check:protocol-generated && pnpm check:coverage-generated && pnpm build && pnpm test && pnpm test:go:race && pnpm test:e2e"
}
~~~

Do not add `prepare`、`postinstall` or networked generation hooks. Lockfile 只新增两个 Coverage workspace importer，既有 `packages:` resolution records 必须保持 byte-identical；使用仓库固定 pnpm 的 frozen/offline 只读流程验证，不再运行会重写 resolution metadata 的 lockfile-only 命令。同时在 Phase 5A Task 1 下直接添加详细计划链接。

- [ ] **Step 4: Run the complete Coverage contract gate**

~~~powershell
pnpm generate:coverage
pnpm check:coverage-generated
pnpm test:coverage-gen
pnpm --filter @unit-test-ide/coverage-schema test
pnpm --filter @unit-test-ide/coverage-models test
go test ./apps/test-service/internal/coveragemodel/... -count=1
go test -race ./apps/test-service/internal/coveragemodel/... -count=1
pnpm check:protocol-generated
pnpm build
git diff --check
~~~

Expected: all commands PASS. A second generation leaves both committed generated files byte-identical.

- [ ] **Step 5: Review security and determinism boundaries**

Verify:

1. no Protocol v1.0–v1.3 file changed；
2. no Coverage JSON field carries command/environment/native path；
3. second generation is byte-identical；
4. TypeScript and Go agree on fixtures；
5. no runtime/install hook generates code or accesses the network。

Run `git diff --check` and `git status --short`; only files declared by this plan may appear.

- [ ] **Step 6: Commit and push root integration**

~~~powershell
git add package.json pnpm-lock.yaml packages/coverage-schema packages/coverage-models tools/coverage-gen apps/test-service/internal/coveragemodel docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md docs/superpowers/plans/2026-08-03-coverage-json-v1-contract-plan.md
git commit -m "feat: complete coverage json contract"
git push github codex/workspace-cmake-toolchains
git push origin codex/workspace-cmake-toolchains
~~~

## Completion Gate

- [ ] Coverage JSON v1 closed structural Schema and fixtures pass
- [ ] deterministic TypeScript/Go generation and `--check` pass
- [ ] TypeScript/Go semantic validators agree
- [ ] safe integer、ordering、NFC URI and summary invariants pass
- [ ] root `verify` includes non-mutating Coverage drift check
- [ ] existing Protocol generated drift check remains green
- [ ] GitHub and Gitee point to the same green commit
