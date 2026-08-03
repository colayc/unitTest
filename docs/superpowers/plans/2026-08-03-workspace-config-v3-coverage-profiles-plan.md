# Workspace config v3 coverage profiles Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在保持 Workspace config v1/v2 wire、loader 与 generation behavior 不变的前提下，增加 closed Workspace config v3、bounded coverage profiles、Build Profile reference validation 和 deterministic Workspace generation identity。

**Architecture:** `workspace` package 只解析、规范化和 defensive clone 用户声明的 coverage profile，不读取文件系统、不编译 glob、不发现 Build Profile。`cmake` package 在 Build Profile 已发现后验证 `baseBuildProfileId`，提供 project-aware resolver，并把 canonical coverage profile snapshot 纳入 v3 Workspace generation；`discovery` 把 unresolved reference 转换为稳定 blocking diagnostic。Schema 负责 closed structural contract，Go loader 负责 UTF-8 byte、NFC、glob grammar、duplicate 和 matcher-state semantic contract。

**Tech Stack:** JSON Schema Draft 2020-12、AJV 8.20.0、Go 1.26.5、`golang.org/x/text/unicode/norm` 0.40.0、Node.js 24.18.0、pnpm 11.4.0。

## Global Constraints

- 本计划不启动 CMake、compiler、CTest、test executable、Python、gcovr、`llvm-profdata` 或 `llvm-cov`。
- Workspace config v1/v2 Schema、fixtures、loader acceptance、canonical Workspace generation JSON shape 和 runtime behavior 不变。
- Protocol v1.0–v1.3 Schema、fixtures、generated model 和 runtime behavior 不变。
- v3 顶层和每个 coverage profile 都是 closed object；用户不能提供 command、flags、compilerArgs、environment、gcovrConfig、script、plugin、driver 或 threshold。
- `coverageProfiles` 最多 64 项；每个 `include`/`exclude` 最多 128 项；单个 glob 为 1..512 UTF-8 bytes。
- 未出现 `include` 时 canonical default 固定为 `["**"]`；显式空 `include` 失败；缺失或空 `exclude` canonicalize 为 `[]`。
- glob 必须是 NFC UTF-8 workspace-relative POSIX form；不允许 NUL/control、反斜杠、absolute/drive/UNC、URI scheme、空/`.`/`..` segment、重复 `/`、brace/class expansion、backtick 或 `$(`/`${` command substitution。
- glob meta 只允许 segment 内的 `*`、`?` 与作为完整 segment 的 `**`；`**` 不能嵌入其他 segment text。
- matcher-state cost 固定为：每个 pattern 的 accept state 为 1；literal rune、`?`、`/` 各 1；`*` 或完整 `**` 各 2。每个 profile 上限 8,192，总 config 上限 65,536。
- profile ID 与 `baseBuildProfileId` 使用现有 1..64 byte stable identifier grammar `^[A-Za-z0-9][A-Za-z0-9._-]*$`。
- `baseBuildProfileId` 的存在性只在 CMake Build Profile discovery 完成后校验；project-aware resolution 还必须验证 base profile 的 `ProjectID` 与请求 Project 相同。
- canonical profile 顺序按 `ID`、`BaseBuildProfileID` 递增；`include`/`exclude` 各按 UTF-8 bytes（Go string order）递增；NFC-equivalent duplicate 必须失败。
- 所有 public Config/profile 返回值和 canonicalization 都 defensive clone slice，不能修改 loader 或调用方输入。
- 每个 Task 严格 red-green-refactor，结束时独立提交，并由 controller 推送 GitHub `github` 与 Gitee `origin` 的 `codex/workspace-cmake-toolchains`。

## File Responsibility Map

| Path | Single responsibility |
|---|---|
| `apps/test-service/internal/workspace/workspace.schema.json` | Workspace v1/v2/v3 closed structural wire contract, including distinct include/exclude list cardinality |
| `apps/test-service/internal/workspace/testdata/coverage-*.json` | v3 valid/invalid shared structural and semantic evidence |
| `tools/workspace-smoke/workspace-config-schema.test.mjs` | AJV structural compatibility and rejection gate |
| `apps/test-service/internal/workspace/config.go` | v3 wire decode, semantic glob validation, canonicalization and defensive clone |
| `apps/test-service/internal/workspace/config_test.go` | loader compatibility, semantic limits, injection rejection and mutation isolation |
| `apps/test-service/internal/cmake/generation.go` | Build Profile reference/resolution and canonical v3 generation identity |
| `apps/test-service/internal/cmake/generation_test.go` | reference, project binding, hashing, order and no-mutation contract |
| `apps/test-service/internal/discovery/inspector.go` | convert unresolved coverage reference into stable blocking diagnostic |
| `apps/test-service/internal/discovery/inspector_test.go` | production discovery integration evidence |

---

### Task 1: Workspace config v3 closed Schema 与 fixtures

**Files:**

- Modify: `apps/test-service/internal/workspace/workspace.schema.json`
- Create: `apps/test-service/internal/workspace/testdata/coverage-v3.valid.json`
- Create: `apps/test-service/internal/workspace/testdata/coverage-command.invalid.json`
- Create: `apps/test-service/internal/workspace/testdata/coverage-path.invalid.json`
- Create: `apps/test-service/internal/workspace/testdata/coverage-duplicate.invalid.json`
- Modify: `tools/workspace-smoke/workspace-config-schema.test.mjs`

**Interfaces:**

- Consumes: existing `$defs/cmake`、`$defs/projectV2`、`$defs/toolchains`、`$defs/identifier` without changing them.
- Produces: top-level `workspaceV3` with optional `coverageProfiles`.
- Produces exact wire fields: `id`, `baseBuildProfileId`, `include`, `exclude`.
- Structural bounds: 64 profiles, 128 globs per list, non-empty explicit `include`, empty-or-missing `exclude`, and 512 Unicode characters as Schema coarse limit; Go loader adds exact UTF-8 byte/state rules.

- [ ] **Step 1: Write the v3 fixtures**

Create `coverage-v3.valid.json` exactly:

```json
{
  "version": 3,
  "projects": [
    {
      "id": "app",
      "sourceDir": ".",
      "tests": {
        "containers": []
      }
    }
  ],
  "coverageProfiles": [
    {
      "id": "coverage-debug",
      "baseBuildProfileId": "debug-clang",
      "include": ["src/**", "include/**"],
      "exclude": ["third_party/**", "tests/**"]
    }
  ]
}
```

Create `coverage-command.invalid.json` from the valid fixture and add only this member to its profile:

```json
"command": "llvm-cov export"
```

Create `coverage-path.invalid.json` from the valid fixture and change only `include`:

```json
"include": ["../src/**"]
```

Create `coverage-duplicate.invalid.json` with two byte-identical copies of the valid profile so Draft 2020-12 `uniqueItems` rejects it:

```json
{
  "version": 3,
  "projects": [{ "id": "app", "sourceDir": "." }],
  "coverageProfiles": [
    {
      "id": "coverage-debug",
      "baseBuildProfileId": "debug-clang",
      "include": ["src/**"],
      "exclude": ["tests/**"]
    },
    {
      "id": "coverage-debug",
      "baseBuildProfileId": "debug-clang",
      "include": ["src/**"],
      "exclude": ["tests/**"]
    }
  ]
}
```

- [ ] **Step 2: Write the failing AJV contract tests**

Append these tests to `workspace-config-schema.test.mjs`:

```js
test("workspace schema accepts closed v3 coverage profiles without widening v1 or v2", async () => {
  const validate = await compileSchema();
  assert.equal(validate(await fixture("coverage-v3.valid.json")), true, JSON.stringify(validate.errors));

  for (const version of [1, 2]) {
    assert.equal(validate({
      version,
      projects: [{ id: "app", sourceDir: "." }],
      coverageProfiles: [{ id: "coverage", baseBuildProfileId: "debug" }]
    }), false, `version ${version} accepted coverageProfiles`);
  }
});

test("workspace v3 schema rejects coverage injection, unsafe path, duplicates, and structural limits", async () => {
  const validate = await compileSchema();
  for (const name of [
    "coverage-command.invalid.json",
    "coverage-path.invalid.json",
    "coverage-duplicate.invalid.json"
  ]) {
    assert.equal(validate(await fixture(name)), false, name);
  }

  const base = {
    version: 3,
    projects: [{ id: "app", sourceDir: "." }],
    coverageProfiles: [{ id: "coverage", baseBuildProfileId: "debug", include: ["src/**"] }]
  };
  const forbidden = [
    "flags", "compilerArgs", "environment", "gcovrConfig", "script",
    "plugin", "driver", "threshold"
  ];
  for (const field of forbidden) {
    const value = structuredClone(base);
    value.coverageProfiles[0][field] = field === "environment" ? { TOKEN: "secret" } : "unsafe";
    assert.equal(validate(value), false, field);
  }

  const emptyExclude = structuredClone(base);
  emptyExclude.coverageProfiles[0].exclude = [];
  assert.equal(validate(emptyExclude), true, JSON.stringify(validate.errors));
  const emptyInclude = structuredClone(base);
  emptyInclude.coverageProfiles[0].include = [];
  assert.equal(validate(emptyInclude), false, "empty include");

  assert.equal(validate({
    ...base,
    coverageProfiles: Array.from({ length: 65 }, (_, index) => ({
      id: `coverage-${index}`,
      baseBuildProfileId: "debug"
    }))
  }), false, "65 profiles");
  assert.equal(validate({
    ...base,
    coverageProfiles: [{
      id: "coverage",
      baseBuildProfileId: "debug",
      include: Array.from({ length: 129 }, (_, index) => `src/file-${index}.cpp`)
    }]
  }), false, "129 includes");
});
```

- [ ] **Step 3: Run the Schema test and verify RED**

Run:

```powershell
pnpm test:workspace
```

Expected: FAIL because `version: 3` is outside the existing `oneOf` and `coverageProfiles` has no definition.

- [ ] **Step 4: Add the closed v3 Schema definitions**

Add `workspaceV3` to the top-level `oneOf` and add these exact `$defs` entries. Reuse the existing v2 project and toolchain definitions; do not edit `workspaceV1` or `workspaceV2`.

```json
"workspaceV3": {
  "type": "object",
  "additionalProperties": false,
  "required": ["version"],
  "properties": {
    "version": { "const": 3 },
    "cmake": { "$ref": "#/$defs/cmake" },
    "projects": {
      "type": "array",
      "maxItems": 64,
      "uniqueItems": true,
      "items": { "$ref": "#/$defs/projectV2" }
    },
    "toolchains": { "$ref": "#/$defs/toolchains" },
    "coverageProfiles": {
      "type": "array",
      "maxItems": 64,
      "uniqueItems": true,
      "items": { "$ref": "#/$defs/coverageProfile" }
    }
  }
},
"coverageProfile": {
  "type": "object",
  "additionalProperties": false,
  "required": ["id", "baseBuildProfileId"],
  "properties": {
    "id": { "$ref": "#/$defs/identifier" },
    "baseBuildProfileId": { "$ref": "#/$defs/identifier" },
    "include": { "$ref": "#/$defs/coverageIncludeGlobList" },
    "exclude": { "$ref": "#/$defs/coverageExcludeGlobList" }
  }
},
"coverageIncludeGlobList": {
  "type": "array",
  "minItems": 1,
  "maxItems": 128,
  "uniqueItems": true,
  "items": { "$ref": "#/$defs/coverageGlob" }
},
"coverageExcludeGlobList": {
  "type": "array",
  "maxItems": 128,
  "uniqueItems": true,
  "items": { "$ref": "#/$defs/coverageGlob" }
},
"coverageGlob": {
  "type": "string",
  "minLength": 1,
  "maxLength": 512,
  "pattern": "^(?!/)(?![A-Za-z]:)(?![A-Za-z][A-Za-z0-9+.-]*:)(?!.*\\\\)(?!.*(?:^|/)\\.\\.?(?:/|$))(?!.*//)(?!.*/$)(?!.*[\\[\\]{}])(?!.*[\\u0000-\\u001F\\u007F])(?!.*`)(?!.*\\$[({]).+$"
}
```

Use `minItems: 1` only for `coverageIncludeGlobList`. Missing `include` remains structurally valid and the Go loader supplies `["**"]`; missing or explicit empty `exclude` remains valid and the Go loader supplies a non-nil empty slice.

- [ ] **Step 5: Run Schema tests GREEN**

Run:

```powershell
pnpm test:workspace
git diff --check
```

Expected: workspace Schema tests PASS; existing v1/v2 fixtures stay PASS; four v3 fixtures have the expected result.

- [ ] **Step 6: Commit Task 1**

```powershell
git add apps/test-service/internal/workspace/workspace.schema.json apps/test-service/internal/workspace/testdata/coverage-v3.valid.json apps/test-service/internal/workspace/testdata/coverage-command.invalid.json apps/test-service/internal/workspace/testdata/coverage-path.invalid.json apps/test-service/internal/workspace/testdata/coverage-duplicate.invalid.json tools/workspace-smoke/workspace-config-schema.test.mjs
git commit -m "feat: define workspace coverage profile schema"
```

---

### Task 2: Go v3 loader、bounded glob 与 defensive clone

**Files:**

- Modify: `apps/test-service/internal/workspace/config.go`
- Modify: `apps/test-service/internal/workspace/config_test.go`

**Interfaces:**

- Consumes: Task 1 wire fields and fixtures.
- Produces:

```go
type CoverageProfile struct {
    ID                 string   `json:"id"`
    BaseBuildProfileID string   `json:"baseBuildProfileId"`
    Include            []string `json:"include"`
    Exclude            []string `json:"exclude"`
}

type Config struct {
    Version          int               `json:"version"`
    CMake            CMakeConfig       `json:"cmake,omitempty"`
    Projects         []ProjectConfig   `json:"projects,omitempty"`
    Toolchains       []ToolchainConfig `json:"toolchains,omitempty"`
    CoverageProfiles []CoverageProfile `json:"coverageProfiles,omitempty"`
}

func (config Config) Clone() Config
```

- Produces canonical NFC sorted `Include`/`Exclude`; missing include becomes `["**"]`.
- Does not resolve `BaseBuildProfileID`; Task 3 owns discovered Build Profile references.

- [ ] **Step 1: Write the failing v3 load and compatibility tests**

Add:

```go
func TestLoadConfigLoadsCanonicalCoverageV3WithoutChangingV1V2(t *testing.T) {
    result, err := loadConfigBytes(t, configFixture(t, "coverage-v3.valid.json"))
    if err != nil {
        t.Fatal(err)
    }
    if result.Config.Version != 3 || len(result.Config.CoverageProfiles) != 1 {
        t.Fatalf("Config = %#v", result.Config)
    }
    got := result.Config.CoverageProfiles[0]
    want := CoverageProfile{
        ID: "coverage-debug", BaseBuildProfileID: "debug-clang",
        Include: []string{"include/**", "src/**"},
        Exclude: []string{"tests/**", "third_party/**"},
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("CoverageProfile = %#v, want %#v", got, want)
    }

    for _, fixtureName := range []string{"minimal.valid.json", "tests-v2.valid.json"} {
        loaded, loadErr := loadConfigBytes(t, configFixture(t, fixtureName))
        if loadErr != nil {
            t.Fatalf("%s: %v", fixtureName, loadErr)
        }
        if len(loaded.Config.CoverageProfiles) != 0 {
            t.Fatalf("%s coverage profiles = %#v", fixtureName, loaded.Config.CoverageProfiles)
        }
    }
}

func TestLoadConfigCanonicalizesCoverageDefaultsNFCAndOrder(t *testing.T) {
    data := []byte(`{"version":3,"coverageProfiles":[` +
        `{"id":"z","baseBuildProfileId":"base","exclude":["tests/**"]},` +
        `{"id":"a","baseBuildProfileId":"base","include":["src/e\u0301.cpp","include/**"],"exclude":[]}` +
        `]}`)
    result, err := loadConfigBytes(t, data)
    if err != nil {
        t.Fatal(err)
    }
    if got := result.Config.CoverageProfiles[0].ID; got != "a" {
        t.Fatalf("first ID = %q", got)
    }
    if got := result.Config.CoverageProfiles[0].Include; !reflect.DeepEqual(got, []string{"include/**", "src/é.cpp"}) {
        t.Fatalf("NFC include = %#v", got)
    }
    if got := result.Config.CoverageProfiles[0].Exclude; got == nil || len(got) != 0 {
        t.Fatalf("empty exclude = %#v, want non-nil empty slice", got)
    }
    if got := result.Config.CoverageProfiles[1].Include; !reflect.DeepEqual(got, []string{"**"}) {
        t.Fatalf("default include = %#v", got)
    }
}

func TestLoadConfigAcceptsSupportedCoverageMetacharacters(t *testing.T) {
    result, err := loadConfigBytes(t, coverageConfigJSON(
        t,
        []string{"**", "include/?.hpp", "src/*.cpp"},
        []string{},
    ))
    if err != nil {
        t.Fatal(err)
    }
    got := result.Config.CoverageProfiles[0]
    if !reflect.DeepEqual(got.Include, []string{"**", "include/?.hpp", "src/*.cpp"}) ||
        got.Exclude == nil || len(got.Exclude) != 0 {
        t.Fatalf("CoverageProfile = %#v", got)
    }
}
```

Add `reflect` to the existing test imports.

- [ ] **Step 2: Write the failing semantic rejection and clone tests**

Add one table-driven test with these exact inputs:

```go
func TestLoadConfigRejectsUnsafeCoverageProfiles(t *testing.T) {
    cases := map[string][]byte{
        "v2 coverage field": []byte(`{"version":2,"coverageProfiles":[]}`),
        "command fixture": configFixture(t, "coverage-command.invalid.json"),
        "path fixture": configFixture(t, "coverage-path.invalid.json"),
        "duplicate fixture": configFixture(t, "coverage-duplicate.invalid.json"),
        "duplicate ID with different globs": []byte(`{"version":3,"coverageProfiles":[{"id":"coverage","baseBuildProfileId":"base","include":["src/**"]},{"id":"coverage","baseBuildProfileId":"base","include":["include/**"]}]}`),
        "missing base": []byte(`{"version":3,"coverageProfiles":[{"id":"coverage"}]}`),
        "empty include": []byte(`{"version":3,"coverageProfiles":[{"id":"coverage","baseBuildProfileId":"base","include":[]}]}`),
        "empty glob": coverageConfigJSON(t, []string{""}, nil),
        "backslash": coverageConfigJSON(t, []string{`src\\**`}, nil),
        "absolute": coverageConfigJSON(t, []string{"/src/**"}, nil),
        "drive": coverageConfigJSON(t, []string{"C:/src/**"}, nil),
        "UNC": coverageConfigJSON(t, []string{"//server/share/**"}, nil),
        "URI scheme": coverageConfigJSON(t, []string{"file:src/**"}, nil),
        "dot segment": coverageConfigJSON(t, []string{"src/./**"}, nil),
        "empty segment": coverageConfigJSON(t, []string{"src//**"}, nil),
        "trailing slash": coverageConfigJSON(t, []string{"src/"}, nil),
        "embedded globstar": coverageConfigJSON(t, []string{"src/**.cpp"}, nil),
        "class expansion": coverageConfigJSON(t, []string{"src/[ab].cpp"}, nil),
        "brace expansion": coverageConfigJSON(t, []string{"src/{a,b}.cpp"}, nil),
        "command substitution": coverageConfigJSON(t, []string{"src/$(whoami).cpp"}, nil),
        "environment substitution": coverageConfigJSON(t, []string{"src/${TOKEN}.cpp"}, nil),
        "backtick": coverageConfigJSON(t, []string{"src/`whoami`.cpp"}, nil),
        "NUL": coverageConfigJSON(t, []string{"src/\x00.cpp"}, nil),
        "control": coverageConfigJSON(t, []string{"src/\x01.cpp"}, nil),
        "NFC duplicate": coverageConfigJSON(t, []string{"src/é.cpp", "src/e\u0301.cpp"}, nil),
        "long ASCII glob": coverageConfigJSON(t, []string{"src/" + strings.Repeat("x", 509)}, nil),
        "long multibyte glob": coverageConfigJSON(t, []string{strings.Repeat("界", 171)}, nil),
    }
    for name, data := range cases {
        t.Run(name, func(t *testing.T) {
            if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
                t.Fatalf("error = %v, want ErrInvalidConfig", err)
            }
        })
    }
}

func TestConfigCloneIsolatesCoverageSlices(t *testing.T) {
    result, err := loadConfigBytes(t, configFixture(t, "coverage-v3.valid.json"))
    if err != nil {
        t.Fatal(err)
    }
    cloned := result.Config.Clone()
    cloned.CoverageProfiles[0].Include[0] = "mutated/**"
    cloned.CoverageProfiles[0].Exclude[0] = "mutated/**"
    cloned.CoverageProfiles = append(cloned.CoverageProfiles, CoverageProfile{ID: "extra"})
    if result.Config.CoverageProfiles[0].Include[0] != "include/**" ||
        result.Config.CoverageProfiles[0].Exclude[0] != "tests/**" ||
        len(result.Config.CoverageProfiles) != 1 {
        t.Fatalf("source Config mutated: %#v", result.Config)
    }
}
```

Add this exact helper:

```go
func coverageConfigJSON(t *testing.T, include, exclude []string) []byte {
    t.Helper()
    profile := map[string]any{
        "id": "coverage", "baseBuildProfileId": "base", "include": include,
    }
    if exclude != nil {
        profile["exclude"] = exclude
    }
    return mustJSON(t, map[string]any{"version": 3, "coverageProfiles": []any{profile}})
}
```

Add these exact helpers and bounds test so no duplicate, byte limit, or per-profile limit masks the intended failure:

```go
func literalCoverageGlobs(prefix string, lengths ...int) []string {
    result := make([]string, 0, len(lengths))
    for index, length := range lengths {
        head := fmt.Sprintf("%s-%02d-", prefix, index)
        result = append(result, head+strings.Repeat("x", length-len(head)))
    }
    return result
}

func repeatedLength(length, count int) []int {
    result := make([]int, count)
    for index := range result {
        result[index] = length
    }
    return result
}

func TestLoadConfigRejectsCoverageBounds(t *testing.T) {
    profiles := make([]any, 65)
    for index := range profiles {
        profiles[index] = map[string]any{
            "id": fmt.Sprintf("coverage-%02d", index), "baseBuildProfileId": "base",
        }
    }
    includes := make([]string, 129)
    for index := range includes {
        includes[index] = fmt.Sprintf("src/file-%03d.cpp", index)
    }

    perProfile := literalCoverageGlobs("per", repeatedLength(511, 15)...)
    perProfile = append(perProfile, literalCoverageGlobs("tail", 510)...)
    perProfile = append(perProfile, "z") // 15*512 + 511 + 2 = 8,193 states.

    totalProfiles := make([]any, 0, 9)
    for index := 0; index < 7; index++ {
        totalProfiles = append(totalProfiles, map[string]any{
            "id": fmt.Sprintf("total-%02d", index), "baseBuildProfileId": "base",
            "include": literalCoverageGlobs(fmt.Sprintf("p%02d", index), repeatedLength(511, 16)...),
        })
    }
    totalProfiles = append(totalProfiles, map[string]any{
        "id": "total-07", "baseBuildProfileId": "base",
        "include": append(
            literalCoverageGlobs("p07", repeatedLength(511, 15)...),
            literalCoverageGlobs("p07-tail", 510)...,
        ),
    })
    totalProfiles = append(totalProfiles, map[string]any{
        "id": "total-08", "baseBuildProfileId": "base", "include": []string{"z"},
    }) // 7*8,192 + 8,191 + 2 = 65,537 states.

    cases := map[string][]byte{
        "65 profiles": mustJSON(t, map[string]any{"version": 3, "coverageProfiles": profiles}),
        "129 includes": coverageConfigJSON(t, includes, nil),
        "8193 profile states": coverageConfigJSON(t, perProfile, nil),
        "65537 total states": mustJSON(t, map[string]any{"version": 3, "coverageProfiles": totalProfiles}),
    }
    for name, data := range cases {
        t.Run(name, func(t *testing.T) {
            if _, err := loadConfigBytes(t, data); !errors.Is(err, ErrInvalidConfig) {
                t.Fatalf("error = %v, want ErrInvalidConfig", err)
            }
        })
    }
}
```

Add `fmt` to the existing test imports.

- [ ] **Step 3: Run focused tests and verify RED**

Run:

```powershell
go test ./apps/test-service/internal/workspace -run 'Coverage|ConfigV3|Clone' -count=1
```

Expected: compile FAIL because `CoverageProfile`, `Config.CoverageProfiles` and `Config.Clone` do not exist.

- [ ] **Step 4: Add v3 wire types and exact limits**

Add imports and constants:

```go
import "sort"
import "unicode"

import "golang.org/x/text/unicode/norm"

const (
    maxCoverageProfiles          = 64
    maxCoverageGlobsPerList      = 128
    maxCoverageGlobBytes         = 512
    maxCoverageProfileStates     = 8_192
    maxCoverageConfigurationStates = 65_536
)
```

Add `CoverageProfiles` to `Config` and add this exact field to `configWire`:

```go
CoverageProfiles nonNullOptional[[]coverageProfileWire] `json:"coverageProfiles"`
```

Add the exact profile wire type:

```go
type coverageProfileWire struct {
    ID                 *string                   `json:"id"`
    BaseBuildProfileID *string                   `json:"baseBuildProfileId"`
    Include            nonNullOptional[[]string] `json:"include"`
    Exclude            nonNullOptional[[]string] `json:"exclude"`
}
```

Allow `wire.Version` 1, 2 or 3. If `wire.CoverageProfiles.Present` and version is below 3, return `ErrInvalidConfig`. Pass `wire.CoverageProfiles.Value` to `decodeCoverageProfiles` only for version 3; a missing v3 list becomes a non-nil empty canonical slice.

- [ ] **Step 5: Implement exact glob canonicalization and matcher-state accounting**

Implement these helpers with the exact decisions from Global Constraints:

```go
func canonicalCoverageGlobs(values []string, present, defaultInclude bool) ([]string, int, error)
func validateCoverageGlob(value string) (int, error)
func coverageGlobSegmentCost(segment string) (int, error)
```

`canonicalCoverageGlobs` must:

1. return `["**"]` and its calculated cost when `defaultInclude && !present`;
2. reject a present empty list only when `defaultInclude` is true; for `exclude`, return a new non-nil empty slice and zero cost;
3. reject more than 128 values;
4. call `norm.NFC.String` before validation;
5. sort with `sort.Strings`;
6. reject adjacent duplicates after sorting;
7. return a new slice and the sum of pattern costs.

`validateCoverageGlob` must use byte length and `utf8.ValidString`, reject `unicode.IsControl`, the portable path/segment and expansion forms listed in Global Constraints, then split by `/`. For every segment:

- exact `**` costs 2;
- any other occurrence of `**` fails;
- each `*` costs 2;
- each `?`, literal rune and separator costs 1;
- each pattern begins with accept-state cost 1.

- [ ] **Step 6: Decode, sort and clone coverage profiles**

Implement:

```go
func decodeCoverageProfiles(wires []coverageProfileWire) ([]CoverageProfile, error)
func (config Config) Clone() Config
```

`decodeCoverageProfiles` must validate stable IDs, duplicate profile IDs, both list bounds, 8,192 per-profile state and 65,536 total state. It must sort profiles by `ID`, then `BaseBuildProfileID`, canonicalize missing `include` to `["**"]` and missing/empty `exclude` to a non-nil empty slice, and return slices that do not alias wire slices.

`Config.Clone` must clone:

- `Projects`;
- every `Fallback.Configurations`;
- every `Tests.Containers`;
- `Toolchains`;
- `CoverageProfiles`;
- every profile `Include` and `Exclude`.

- [ ] **Step 7: Run loader tests GREEN**

Run:

```powershell
go test ./apps/test-service/internal/workspace -run 'Coverage|ConfigV3|Clone|V2' -count=1
go test ./apps/test-service/internal/workspace -count=1
pnpm test:workspace
git diff --check
```

Expected: Go Workspace tests and AJV workspace tests PASS.

- [ ] **Step 8: Commit Task 2**

```powershell
git add apps/test-service/internal/workspace/config.go apps/test-service/internal/workspace/config_test.go
git commit -m "feat: load workspace coverage profiles"
```

---

### Task 3: Build Profile reference、project-aware resolver 与 generation hash

**Files:**

- Modify: `apps/test-service/internal/cmake/generation.go`
- Modify: `apps/test-service/internal/cmake/generation_test.go`

**Interfaces:**

- Consumes: canonical `workspace.Config.CoverageProfiles` from Task 2 and discovered `[]BuildProfile`.
- Produces:

```go
var ErrInvalidCoverageProfile = errors.New("invalid coverage profile")

func ValidateCoverageProfileReferences(
    coverageProfiles []workspace.CoverageProfile,
    buildProfiles []BuildProfile,
) error

func ResolveCoverageProfile(
    coverageProfiles []workspace.CoverageProfile,
    buildProfiles []BuildProfile,
    projectID string,
    coverageProfileID string,
) (workspace.CoverageProfile, BuildProfile, error)
```

- `WorkspaceGeneration` signature stays unchanged.
- v1/v2 canonical JSON shape stays unchanged by using `json:"coverageProfiles,omitempty"`.

- [ ] **Step 1: Write failing reference and resolver tests**

Add:

```go
func TestCoverageProfileReferencesAndProjectBinding(t *testing.T) {
    coverageProfiles := []workspace.CoverageProfile{{
        ID: "coverage-debug", BaseBuildProfileID: "build-debug",
        Include: []string{"src/**"}, Exclude: []string{"tests/**"},
    }}
    buildProfiles := []BuildProfile{{ID: "build-debug", ProjectID: "app"}}
    if err := ValidateCoverageProfileReferences(coverageProfiles, buildProfiles); err != nil {
        t.Fatal(err)
    }
    for name, profiles := range map[string][]BuildProfile{
        "missing base": nil,
        "duplicate build ID": []BuildProfile{
            {ID: "build-debug", ProjectID: "app"},
            {ID: "build-debug", ProjectID: "other"},
        },
    } {
        t.Run("validate "+name, func(t *testing.T) {
            if validateErr := ValidateCoverageProfileReferences(coverageProfiles, profiles);
                !errors.Is(validateErr, ErrInvalidCoverageProfile) {
                t.Fatalf("error = %v", validateErr)
            }
        })
    }
    coverage, base, err := ResolveCoverageProfile(
        coverageProfiles, buildProfiles, "app", "coverage-debug",
    )
    if err != nil || coverage.ID != "coverage-debug" || base.ID != "build-debug" {
        t.Fatalf("coverage/base/error = %#v / %#v / %v", coverage, base, err)
    }
    coverage.Include[0] = "mutated/**"
    if coverageProfiles[0].Include[0] != "src/**" {
        t.Fatal("ResolveCoverageProfile returned an alias")
    }

    tests := []struct {
        name          string
        projectID     string
        coverageID    string
        buildProfiles []BuildProfile
    }{
        {name: "missing base", projectID: "app", coverageID: "coverage-debug"},
        {name: "wrong project", projectID: "other", coverageID: "coverage-debug", buildProfiles: buildProfiles},
        {name: "missing coverage", projectID: "app", coverageID: "unknown", buildProfiles: buildProfiles},
    }
    for _, test := range tests {
        t.Run(test.name, func(t *testing.T) {
            _, _, resolveErr := ResolveCoverageProfile(
                coverageProfiles, test.buildProfiles, test.projectID, test.coverageID,
            )
            if !errors.Is(resolveErr, ErrInvalidCoverageProfile) {
                t.Fatalf("error = %v", resolveErr)
            }
        })
    }

    duplicateCoverage := append(
        append([]workspace.CoverageProfile{}, coverageProfiles...),
        coverageProfiles[0],
    )
    if _, _, duplicateErr := ResolveCoverageProfile(
        duplicateCoverage, buildProfiles, "app", "coverage-debug",
    ); !errors.Is(duplicateErr, ErrInvalidCoverageProfile) {
        t.Fatalf("duplicate coverage error = %v", duplicateErr)
    }
}
```

- [ ] **Step 2: Write failing canonical generation tests**

Add:

```go
func TestWorkspaceGenerationCanonicalizesCoverageProfiles(t *testing.T) {
    configA := workspace.Config{Version: 3, CoverageProfiles: []workspace.CoverageProfile{
        {ID: "z", BaseBuildProfileID: "build-z", Include: []string{"src/**", "include/**"}},
        {ID: "a", BaseBuildProfileID: "build-a", Exclude: []string{"tests/**", "third_party/**"}},
    }}
    configB := workspace.Config{Version: 3, CoverageProfiles: []workspace.CoverageProfile{
        {ID: "a", BaseBuildProfileID: "build-a", Exclude: []string{"third_party/**", "tests/**"}},
        {ID: "z", BaseBuildProfileID: "build-z", Include: []string{"include/**", "src/**"}},
    }}
    first := WorkspaceGeneration(configA, Installation{}, nil, nil)
    second := WorkspaceGeneration(configB, Installation{}, nil, nil)
    if first != second {
        t.Fatalf("coverage order changed generation: %q / %q", first, second)
    }
    configB.CoverageProfiles[0].Exclude[0] = "generated/**"
    if changed := WorkspaceGeneration(configB, Installation{}, nil, nil); changed == first {
        t.Fatalf("coverage semantic change kept generation %q", first)
    }
}

func TestCanonicalGenerationOmitsCoverageFieldForV1V2(t *testing.T) {
    for _, version := range []int{1, 2} {
        encoded, err := json.Marshal(canonicalGenerationConfig(workspace.Config{
            Version: version,
            CoverageProfiles: []workspace.CoverageProfile{{
                ID: "must-be-ignored", BaseBuildProfileID: "base", Include: []string{"**"},
            }},
        }))
        if err != nil {
            t.Fatal(err)
        }
        if bytes.Contains(encoded, []byte("coverageProfiles")) {
            t.Fatalf("v%d canonical config widened: %s", version, encoded)
        }
    }
}
```

Extend `TestWorkspaceGenerationDoesNotMutateInputs` with one coverage profile and cloned include/exclude slices.

- [ ] **Step 3: Run focused CMake tests and verify RED**

Run:

```powershell
go test ./apps/test-service/internal/cmake -run 'Coverage|Generation' -count=1
```

Expected: compile FAIL because the validation/resolution interfaces do not exist and coverage profiles are absent from `generationConfig`.

- [ ] **Step 4: Implement reference validation and project-aware resolution**

Add `errors` and `fmt` imports, the sentinel, and both functions. Requirements:

- build profile IDs must be unique for resolution; duplicate discovered IDs fail;
- every coverage `BaseBuildProfileID` must exist exactly once;
- requested coverage ID must exist exactly once;
- resolved base `ProjectID` must equal the supplied `projectID`;
- returned coverage profile clones `Include` and `Exclude`;
- errors wrap `ErrInvalidCoverageProfile` and never include native paths or secrets.

- [ ] **Step 5: Add coverage profiles to canonical generation JSON**

Add:

```go
type generationConfig struct {
    Version          int                         `json:"version"`
    CMake            generationCMakeConfig       `json:"cmake"`
    Projects         []generationProject         `json:"projects"`
    Toolchains       []generationToolchain       `json:"toolchains"`
    CoverageProfiles []generationCoverageProfile `json:"coverageProfiles,omitempty"`
}

type generationCoverageProfile struct {
    ID                 string   `json:"id"`
    BaseBuildProfileID string   `json:"baseBuildProfileId"`
    Include            []string `json:"include"`
    Exclude            []string `json:"exclude"`
}
```

Implement `canonicalCoverageProfiles` to deep-copy into non-nil `Include`/`Exclude` slices and sort both glob lists, then sort profiles by `ID` and `BaseBuildProfileID`. `canonicalGenerationConfig` calls it only when `config.Version == 3`; it ignores programmatically supplied coverage profiles for v1/v2 so their canonical JSON shape and generation behavior stay byte-for-byte unchanged. Do not reuse the caller's slice even when already sorted.

- [ ] **Step 6: Run CMake tests GREEN**

Run:

```powershell
go test ./apps/test-service/internal/cmake -run 'Coverage|Generation' -count=1
go test ./apps/test-service/internal/cmake -count=1
git diff --check
```

Expected: reference/resolver/hash tests and all CMake unit tests PASS.

- [ ] **Step 7: Commit Task 3**

```powershell
git add apps/test-service/internal/cmake/generation.go apps/test-service/internal/cmake/generation_test.go
git commit -m "feat: bind coverage profiles to cmake generation"
```

---

### Task 4: Discovery integration 与完整 Workspace gate

**Files:**

- Modify: `apps/test-service/internal/discovery/inspector.go`
- Modify: `apps/test-service/internal/discovery/inspector_test.go`

**Interfaces:**

- Consumes: `cmake.ValidateCoverageProfileReferences` from Task 3 after preset/generated profiles are collected and sorted.
- Produces stable blocking diagnostic code `COVERAGE_PROFILE_INVALID` with sanitized message `Coverage profile base build profile is unavailable`.
- Does not expose coverage profiles through Protocol; Phase 5A Task 3 owns Protocol v1.4 wire additions.

- [ ] **Step 1: Write the failing Inspector reference test**

Add a test following `TestInspectorUsesPresetPriorityAndGeneratedProfilesUseCMakeConstructor` setup:

```go
func TestInspectorReportsUnavailableCoverageBaseProfile(t *testing.T) {
    root := openProjectRoot(t, ".")
    config := workspace.Config{
        Version: 3,
        Projects: []workspace.ProjectConfig{{ID: "root", SourceDir: "."}},
        CoverageProfiles: []workspace.CoverageProfile{{
            ID: "coverage", BaseBuildProfileID: "missing-build", Include: []string{"**"},
        }},
    }
    inspector := newTestInspector(t, root, fakeToolchainDiscovery{
        instances: []toolchain.Instance{testToolchain("gcc", toolchain.FamilyGCC, "Ninja")},
    }, inspectorDependencies{
        loadConfig: func(workspace.Root) (workspace.LoadResult, error) {
            return workspace.LoadResult{Config: config}, nil
        },
        resolve: successfulResolve(testInstallation()),
        discoverPresets: func(
            context.Context, probe.Runner, cmake.Installation,
            workspace.Root, workspace.ProjectConfig,
        ) (cmake.PresetDiscovery, error) {
            return cmake.PresetDiscovery{
                Profiles: []cmake.BuildProfile{{
                    ID: "available-build", ProjectID: "root", Origin: "preset",
                }},
                Inputs: []string{"CMakePresets.json"}, InputGeneration: "coverage-profile-input",
            }, nil
        },
    })
    snapshot, err := inspector.Inspect(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if !strings.Contains(diagnosticsText(snapshot), "COVERAGE_PROFILE_INVALID") {
        t.Fatalf("diagnostics = %#v", snapshot.Diagnostics)
    }
}
```

Add a sibling valid-reference subtest with `BaseBuildProfileID: "available-build"` and assert that diagnostic code is absent.

- [ ] **Step 2: Run the Inspector test and verify RED**

Run:

```powershell
go test ./apps/test-service/internal/discovery -run 'CoverageBaseProfile' -count=1
```

Expected: FAIL because Inspector does not call the Task 3 validator.

- [ ] **Step 3: Integrate reference validation after profile discovery**

After `sortProfiles(profiles)` and before `boundDiagnostics`, add:

```go
if err := cmake.ValidateCoverageProfileReferences(loaded.Config.CoverageProfiles, profiles); err != nil {
    diagnostics = append(diagnostics, inspectorDiagnostic(
        "workspace",
        "error",
        "COVERAGE_PROFILE_INVALID",
        "Coverage profile base build profile is unavailable",
        "",
    ))
}
```

Do not append the underlying error text; it may contain user-controlled identifiers and is not needed by the public diagnostic.

- [ ] **Step 4: Run the complete slice gate once**

Run exactly once after focused GREEN:

```powershell
go test ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake ./apps/test-service/internal/discovery -count=1
go test -race ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake ./apps/test-service/internal/discovery -count=1
pnpm test:workspace
pnpm check:coverage-generated
git diff --check
```

Expected: all targeted Go/Node tests PASS and generated Coverage models stay clean. If unrelated historical tests fail, record exact test names and compare them with the baseline CI run; do not modify unrelated packages in this Task.

- [ ] **Step 5: Verify forbidden runtime-tool scope**

Run:

```powershell
git diff --name-only HEAD~4..HEAD
git diff HEAD~4..HEAD -- apps/test-service/internal/workspace apps/test-service/internal/cmake apps/test-service/internal/discovery tools/workspace-smoke | rg -n "exec\.Command|probe\.Runner|llvm-cov|llvm-profdata|gcovr|CTest|Start-Process|child_process"
```

Expected: changed paths match this plan; the scan has no new runtime-tool invocation. Existing test fixture strings are acceptable only when unchanged.

- [ ] **Step 6: Commit Task 4**

```powershell
git add apps/test-service/internal/discovery/inspector.go apps/test-service/internal/discovery/inspector_test.go
git commit -m "feat: validate discovered coverage profiles"
```

## Completion Gate

After all four Tasks have passed their task-scoped reviews:

```powershell
go test ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake ./apps/test-service/internal/discovery -count=1
go test -race ./apps/test-service/internal/workspace ./apps/test-service/internal/cmake ./apps/test-service/internal/discovery -count=1
pnpm test:workspace
pnpm test:coverage-gen
pnpm check:coverage-generated
git diff --check
```

The final whole-branch review must confirm:

- v1/v2 schema and canonical generation JSON are unchanged;
- v3 closed structural and semantic rules agree between AJV and Go wherever Schema can express them;
- all glob limits use UTF-8 bytes and exact matcher-state accounting;
- missing include canonicalizes only once to `["**"]`;
- Build Profile references are validated after discovery and project-aware resolution cannot cross projects;
- no caller-owned slices are mutated;
- no Protocol v1.0–v1.3 or coverage runtime tool path changed.
