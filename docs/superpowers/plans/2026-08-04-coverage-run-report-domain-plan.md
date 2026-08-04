# Go CoverageRun/Report Domain Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立不依赖 Protocol generated model、SQLite 或 process package 的 Go `coveragedomain`，严格验证 Coverage request、CoverageRun、CoverageReport、summary、tool provenance 与 artifact 关联，并为 Task Engine 增加与 Protocol v1.4 一致的 coverage kind、step vocabulary 和 outcome mapping。

**Architecture:** `coveragedomain` 是 Service 内部 coverage aggregate 的唯一语义边界。它复用 `testdomain.Selection`/`SelectionSnapshot`，但独立拥有 request canonicalization、稳定 CoverageRun ID、run/report lifecycle、summary arithmetic 和 toolchain combination validation；它不能导入 `task`，从而保持依赖方向为 `task -> coveragedomain -> testdomain`。本切片只增加 Task vocabulary 和纯 outcome mapping，不允许 `Manager.Start` 接受 `KindCoverageRun`，也不创建真实 coverage plan；Task/CoverageRun/TestRun 的原子创建在后续 migration 009/repository Task 中接入。

**Tech Stack:** Go 1.26.5、标准库 `crypto/sha256`/`encoding/json`/`time`/`unicode/utf8`、现有 `internal/testdomain` 与 `internal/task`。

## Global Constraints

- 本计划不启动 CMake、compiler、CTest、test executable、Python、gcovr、`llvm-profdata` 或 `llvm-cov`，不访问网络，不写 SQLite migration。
- `coveragedomain` 不得导入 `protocolmodel`、`coveragemodel`、`task`、`processhost`、`processcontrol`、`workspaceconfig` 或任何 persistence package。
- Protocol v1.4 与 Coverage JSON v1 是 wire/artifact contract；本 domain 必须独立定义类型并主动验证跨字段语义，不能把 generated model 当作 domain model。
- `Request` 只允许 idempotency、Workspace generation、Project/Coverage Profile/Catalog identity、Phase 4 structured selection、repeat 和 timeout。它不得包含 executable、argv、environment、native path、driver、collector、report format 或 hook。
- 32-hex logical ID、64-hex revision/generation/digest、Project/Profile stable ID、repeat 1..100、timeout 1 ms..24 h 且 millisecond aligned、safe integer `0..9007199254740991` 与 Protocol v1.4 保持一致。
- `Request` 通过 `testdomain.NewSelection` 复用 closed selection shape/NFC/ID validation，再对 set-like ID arrays 做稳定排序；caller mutation 不能影响 validated request 或 canonical JSON。
- CoverageRun ID 使用 domain-separated SHA-256 的前 16 bytes，输入是 validated request 的 canonical JSON；同一语义 request 必须产生同一 32-hex ID，任一 semantic field 变化必须改变 ID。
- `CoverageRun.status` 只允许 `queued|running|finished`；`outcome` 只允许 `available|partial|unavailable|cancelled`。
- queued/running 禁止 outcome、reason、finished time、report ID、summary 和 public report artifact refs；finished 必须有 outcome 与 finished time。
- available/partial 必须有 summary、report ID 与全部三个 public artifacts，并禁止 run-level reason；unavailable/cancelled 必须有相应 closed reason，并禁止 summary、report ID 与 public report artifacts。
- cancelled reason 只允许 `user_cancelled|task_timed_out`；unavailable reason 只允许 `instrumentation_failed|build_failed|profile_collection_failed|merge_failed|normalization_failed|report_generation_failed|persistence_failed|service_restarted`。
- CoverageReport completeness 只允许 available + empty reasons，或 partial + 1..64 unique reasons；partial reasons 只允许 `test_crashed|test_timed_out|profile_missing_for_failed_invocation`，并按字符串排序。
- 每个 metric 都要求 `0 <= covered <= total <= 9007199254740991`；summary addition 必须在相加前检查 safe-integer overflow，不返回 partial result。
- Toolchain combination 只接受已批准矩阵：Windows clang-cl + llvm-cov/llvm-cov、Linux GCC + gcov/gcovr、Linux Clang + llvm-cov/llvm-cov。所有版本为 1..128 bytes valid UTF-8、无 NUL，instrumentation fingerprint 为 64 lowercase hex。
- `ArtifactRefs` 精确包含 `coverage-json`、`junit-xml`、`coverage-html` 三个互不相同的 32-hex artifact ID；raw/indexed profile、`.gcda` 或 third-party JSON 不得进入 domain public refs。
- Task outcome mapping 固定：available/partial → succeeded；unavailable/build_failed → command_failed；unavailable/service_restarted → interrupted；其他 unavailable → infrastructure_failed；cancelled/user_cancelled → cancelled；cancelled/task_timed_out → timed_out；非法组合 → empty outcome。
- Task step vocabulary 精确增加 `coverage-configure`、`coverage-build`、`coverage-test`、`coverage-merge`、`coverage-normalize`、`coverage-report`、`coverage-publish`，与 Protocol v1.4 一致。
- 每个 Task 严格执行 red-green-refactor，完成 spec review 与 quality review 后独立提交；controller 随后推送 GitHub `github` 与 Gitee `origin` 的 `codex/workspace-cmake-toolchains`。

## Exact Domain Contract

```go
type Request struct {
	IdempotencyKey      string
	WorkspaceGeneration string
	ProjectID           string
	CoverageProfileID   string
	CatalogRevision     string
	Selection           testdomain.Selection
	RepeatCount         int64
	Timeout             time.Duration
}

type Run struct {
	ID                string
	TaskID            string
	TestRunID         string
	Status            Status
	Outcome           Outcome
	Reason            Reason
	Request           Request
	SelectionSnapshot testdomain.SelectionSnapshot
	Toolchain         ToolchainSnapshot
	Summary           *Summary
	ReportID          string
	Artifacts         ArtifactRefs
	CreatedAt         time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	LastSequence      int64
}

type Report struct {
	ID           string
	RunID        string
	TestRunID    string
	SchemaVersion string
	CreatedAt    time.Time
	Completeness Completeness
	Summary      Summary
	Toolchain    ToolchainSnapshot
	ArtifactID   string
}
```

Public constructors and helpers:

```go
func NewRequest(Request) (Request, error)
func (Request) Clone() Request
func (Request) CanonicalJSON() ([]byte, error)
func CoverageRunID(Request) (string, error)

func NewSummary(Summary) (Summary, error)
func AddSummary(Summary, Summary) (Summary, error)

func NewRun(Run) (Run, error)
func (Run) Clone() Run
func NewReport(Report) (Report, error)
func (Report) Clone() Report
```

`Request.CanonicalJSON` emits exactly this closed shape and encodes timeout in milliseconds:

```json
{
  "idempotencyKey": "cccccccccccccccccccccccccccccccc",
  "workspaceGeneration": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "projectId": "core",
  "coverageProfileId": "coverage-debug",
  "catalogRevision": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
  "selection": { "mode": "items", "itemIds": ["utid-v1-..."] },
  "repeatCount": 2,
  "timeoutMs": 60000
}
```

## File Responsibility Map

| Path | Single responsibility |
|---|---|
| `apps/test-service/internal/testdomain/run.go` | export existing defensive/canonical `SelectionSnapshot` constructor without changing TestRun behavior |
| `apps/test-service/internal/coveragedomain/request.go` | request validation, selection canonicalization, defensive clone, canonical JSON, stable CoverageRun ID |
| `apps/test-service/internal/coveragedomain/request_test.go` | request bounds, selection reuse, canonical determinism, mutation isolation, forbidden control-surface evidence |
| `apps/test-service/internal/coveragedomain/summary.go` | safe metric/summary validation and overflow-safe addition |
| `apps/test-service/internal/coveragedomain/summary_test.go` | arithmetic, covered<=total, negative/unsafe/overflow rejection |
| `apps/test-service/internal/coveragedomain/model.go` | closed enums, toolchain matrix, CoverageRun/Report lifecycle, completeness, artifact refs, deep clone |
| `apps/test-service/internal/coveragedomain/model_test.go` | lifecycle matrix, reason/report/artifact invariants, provenance, time monotonicity, no-native-path shape |
| `apps/test-service/internal/task/plan.go` | internal `KindCoverageRun` and seven coverage step kinds accepted by plan validation/fingerprint |
| `apps/test-service/internal/task/plan_test.go` | exact coverage step vocabulary, unknown-kind rejection and fingerprint evidence |
| `apps/test-service/internal/task/model.go` | pure CoverageRun outcome/reason → Task outcome mapping |
| `apps/test-service/internal/task/model_test.go` | exhaustive mapping table and invalid-pair fail-closed evidence |

---

### Task 1: Immutable Coverage Request 与稳定 Run ID

**Files:**

- Modify: `apps/test-service/internal/testdomain/run.go`
- Create: `apps/test-service/internal/coveragedomain/request.go`
- Create: `apps/test-service/internal/coveragedomain/request_test.go`

- [ ] **Step 1: Write RED tests for request validation and defensive ownership**

Create a valid request fixture using `testdomain.SelectionItems` with two stable IDs in reverse lexical order. Assert `NewRequest`:

- accepts the exact Protocol bounds and rejects 0/101 repeat, sub-millisecond/zero/>24 h timeout, uppercase/short IDs, invalid Project/Profile IDs and invalid/empty selection;
- reuses `testdomain.NewSelection` behavior for duplicate IDs, invalid mode, invalid `failedFromRun` and NFC-normalized filter text;
- sorts `ContainerIDs`、`ItemIDs`、`Filter.IncludeItemIDs` and `Filter.ExcludeItemIDs` without changing the selected set;
- deep-copies every selection slice so mutating the caller input or `Clone()` cannot mutate the validated request;
- emits deterministic canonical JSON with `timeoutMs`, never Go nanoseconds, and only the eight allowed top-level keys;
- produces the same CoverageRun ID for reordered set-like IDs and a different ID when project/profile/catalog/selection/repeat/timeout changes;
- rejects a request before producing canonical JSON or ID when any invariant is invalid.

Use an allow-list walk over decoded canonical JSON rather than substring matching:

```go
allowed := map[string]bool{
	"idempotencyKey": true, "workspaceGeneration": true,
	"projectId": true, "coverageProfileId": true,
	"catalogRevision": true, "selection": true,
	"repeatCount": true, "timeoutMs": true,
}
```

Assert there are no top-level or nested keys named `executable`、`command`、`args`、`argv`、`env`、`environment`、`cwd`、`path`、`driver`、`collector`、`reportFormat` or `hook`.

- [ ] **Step 2: Run focused tests and verify RED**

```powershell
$go = '.superpowers\runtime\go1.26.5-windows-amd64\go\bin\go.exe'
& $go test ./apps/test-service/internal/coveragedomain -run 'TestRequest|TestCoverageRunID' -count=1
```

Expected: FAIL because `internal/coveragedomain` does not exist.

- [ ] **Step 3: Export the existing SelectionSnapshot validator**

Add only this public wrapper in `testdomain/run.go`; keep `NewTestRun` calling the same underlying implementation and do not change any existing validation branch:

```go
func NewSelectionSnapshot(value SelectionSnapshot) (SelectionSnapshot, error) {
	return newSelectionSnapshot(value)
}
```

- [ ] **Step 4: Implement closed request validation and canonicalization**

In `request.go`:

- define `ErrInvalidRequest` and a field-bearing `ValidationError` local to `coveragedomain`;
- validate ID shapes locally without exporting unrelated `testdomain` internals;
- call `testdomain.NewSelection`, then clone and `sort.Slice` all set-like ID arrays;
- implement a private wire struct for canonical JSON so no new exported field can silently enter the persisted identity;
- use `json.Marshal` over the private wire struct; do not build JSON by concatenating strings;
- calculate ID as `sha256("coverage-run-v1\x00" || canonicalJSON)` and return `hex(sum[:16])`.

The stable ID function must revalidate a clone rather than trust a caller-created raw struct:

```go
func CoverageRunID(value Request) (string, error) {
	request, err := NewRequest(value)
	if err != nil {
		return "", err
	}
	raw, err := request.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("coverage-run-v1\x00"), raw...))
	return hex.EncodeToString(sum[:16]), nil
}
```

- [ ] **Step 5: Run Task 1 GREEN and regression tests**

```powershell
& $go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain -run 'TestRequest|TestCoverageRunID|Selection|TestRun' -count=1
& $go test -race ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain -run 'TestRequest|TestCoverageRunID|Selection|TestRun' -count=1
git diff --check
```

- [ ] **Step 6: Self-review Task 1**

- Compare every request field and bound against `packages/protocol-schema/schema/v1.4/coverage.schema.json`.
- Confirm `rg -n "TODO|TBD|placeholder|panic\(\"not implemented" apps/test-service/internal/coveragedomain apps/test-service/internal/testdomain/run.go` returns no new placeholder.
- Confirm the package imports no forbidden layer and canonical JSON contains no runtime/tool control surface.

- [ ] **Step 7: Commit Task 1**

```powershell
git add apps/test-service/internal/coveragedomain/request.go apps/test-service/internal/coveragedomain/request_test.go apps/test-service/internal/testdomain/run.go
git commit -m "feat: validate coverage run requests"
```

---

### Task 2: CoverageRun、CoverageReport、Summary 与 provenance

**Files:**

- Create: `apps/test-service/internal/coveragedomain/summary.go`
- Create: `apps/test-service/internal/coveragedomain/summary_test.go`
- Create: `apps/test-service/internal/coveragedomain/model.go`
- Create: `apps/test-service/internal/coveragedomain/model_test.go`

- [ ] **Step 1: Write RED tests for metric and summary arithmetic**

Test `NewSummary` and `AddSummary` with:

- zero summary and exact safe-integer maximum accepted;
- negative covered/total, `covered > total` and `total > 9007199254740991` rejected;
- independent lines/branches/functions validation;
- valid addition returns component-wise totals;
- covered or total overflow rejects the whole addition and returns the zero value;
- inputs remain unchanged.

Exact value types:

```go
type Metric struct {
	Covered int64 `json:"covered"`
	Total   int64 `json:"total"`
}

type Summary struct {
	Lines     Metric `json:"lines"`
	Branches  Metric `json:"branches"`
	Functions Metric `json:"functions"`
}
```

- [ ] **Step 2: Write RED lifecycle and report tests**

Define table-driven cases that prove:

- all closed enums reject unknown values;
- queued accepts only `CreatedAt`, request/snapshot/toolchain/identity and sequence; running additionally requires `StartedAt`; finished requires outcome and `FinishedAt`;
- lifecycle time is monotonic and constructor stores UTC values through defensive pointer copies;
- available/partial require non-nil valid summary, 32-hex `ReportID`, and three valid distinct artifact refs; they reject run-level reason;
- unavailable requires one of the eight unavailable reasons and has no report/summary/public refs;
- cancelled requires only user-cancelled or task-timed-out and has no report/summary/public refs;
- completed raw input with the wrong deterministic `Run.ID` is rejected;
- `SelectionSnapshot` goes through `testdomain.NewSelectionSnapshot` and is deeply cloned;
- `LastSequence` rejects negative and unsafe values;
- supported platform/compiler/driver/collector matrix passes and every cross-combination mismatch fails;
- version length, UTF-8, NUL and fingerprint validation fail closed;
- completeness reasons are unique, closed, sorted and defensively copied;
- CoverageReport requires schema `1.0`, valid linked IDs/time/completeness/summary/toolchain and coverage-json artifact ID;
- mutating input, returned `Run.Clone()` or `Report.Clone()` cannot mutate the original;
- recursive JSON field-name inspection finds no native path, command, argv, environment, raw profile or third-party document field.

Use these exact enum families:

```go
const (
	StatusQueued Status = "queued"
	StatusRunning Status = "running"
	StatusFinished Status = "finished"

	OutcomeAvailable Outcome = "available"
	OutcomePartial Outcome = "partial"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeCancelled Outcome = "cancelled"
)
```

The reason constants must spell exactly as Protocol v1.4; completeness reasons are a separate `CompletenessReason` type so run-level reason cannot be accidentally assigned to report completeness.

- [ ] **Step 3: Run focused tests and verify RED**

```powershell
& $go test ./apps/test-service/internal/coveragedomain -run 'TestSummary|TestRun|TestReport|TestCompleteness|TestToolchain' -count=1
```

Expected: FAIL because summary/model types and constructors do not exist.

- [ ] **Step 4: Implement safe summary operations**

Set:

```go
const MaxSafeInteger int64 = 9_007_199_254_740_991
```

Validate each metric before addition. For each component, check `left <= MaxSafeInteger-right` before adding both covered and total; return `ErrInvalidSummary` on overflow. Do not saturate counts and do not return a partially added summary.

- [ ] **Step 5: Implement immutable lifecycle models**

Implement `Status`、`Outcome`、`Reason`、`CompletenessReason`、`Platform`、`Architecture`、`CompilerFamily`、`DriverName` and `CollectorName` as closed string enums. Implement these exact supporting records:

```go
type CompilerSnapshot struct { Family CompilerFamily; Version string }
type DriverSnapshot struct { Name DriverName; Version string }
type CollectorSnapshot struct { Name CollectorName; Version string }

type ToolchainSnapshot struct {
	Platform                   Platform
	Architecture               Architecture
	Compiler                   CompilerSnapshot
	Driver                     DriverSnapshot
	Collector                  CollectorSnapshot
	NormalizerVersion          string
	InstrumentationFingerprint string
}

type ArtifactRefs struct {
	CoverageJSONID string
	JUnitXMLID     string
	CoverageHTMLID string
}

type Completeness struct {
	Outcome Outcome
	Reasons []CompletenessReason
}
```

`NewRun` must call `NewRequest` and `CoverageRunID`, validate `testdomain.NewSelectionSnapshot`, validate `ToolchainSnapshot`, clone all slices/pointers, and then apply lifecycle invariants. `NewReport` performs its own complete validation rather than assuming it was built from a validated Run. Empty artifact refs are valid only for non-report-bearing lifecycle states; report-bearing runs require all three refs and uniqueness.

- [ ] **Step 6: Run Task 2 GREEN and race tests**

```powershell
& $go test ./apps/test-service/internal/coveragedomain -count=1
& $go test -race ./apps/test-service/internal/coveragedomain -count=1
git diff --check
```

- [ ] **Step 7: Self-review Task 2**

- Compare all outcome/reason/completeness/provenance enums with Protocol v1.4 and Coverage JSON v1.
- Verify available/partial and unavailable/cancelled are mutually exclusive on report/summary/artifact ownership.
- Verify no constructor retains caller-owned slices or time pointers.
- Run `go list -deps ./apps/test-service/internal/coveragedomain` and confirm no forbidden project layer is present.
- Scan for placeholders and accidental native/tool execution fields.

- [ ] **Step 8: Commit Task 2**

```powershell
git add apps/test-service/internal/coveragedomain/model.go apps/test-service/internal/coveragedomain/model_test.go apps/test-service/internal/coveragedomain/summary.go apps/test-service/internal/coveragedomain/summary_test.go
git commit -m "feat: model coverage runs and reports"
```

---

### Task 3: Task Engine coverage vocabulary 与 outcome mapping

**Files:**

- Modify: `apps/test-service/internal/task/plan.go`
- Modify: `apps/test-service/internal/task/plan_test.go`
- Modify: `apps/test-service/internal/task/model.go`
- Create: `apps/test-service/internal/task/model_test.go`

- [ ] **Step 1: Write RED tests for exact coverage kinds and plan fingerprints**

Extend `plan_test.go` with a table containing exactly:

```go
[]task.StepKind{
	task.StepCoverageConfigure,
	task.StepCoverageBuild,
	task.StepCoverageTest,
	task.StepCoverageMerge,
	task.StepCoverageNormalize,
	task.StepCoverageReport,
	task.StepCoveragePublish,
}
```

For every kind, construct a one-step service-owned plan with a valid fake boundary and assert `ValidatePlan` succeeds. Then mutate only the step kind and assert `FingerprintPlan` changes. Assert near-miss values such as `coverage-collect`、`coverage-export` and `coverage-file` remain invalid.

Also assert `task.KindCoverageRun == "coverage_run"`; do not add it to `validateStartRequest` or `resumableQueuedKind` in this Task.

- [ ] **Step 2: Write RED exhaustive Task outcome mapping tests**

Create `model_test.go` and exhaustively assert:

```text
available + empty reason                 -> succeeded
partial + empty reason                   -> succeeded
unavailable + build_failed               -> command_failed
unavailable + service_restarted           -> interrupted
unavailable + every other allowed reason -> infrastructure_failed
cancelled + user_cancelled                -> cancelled
cancelled + task_timed_out                -> timed_out
```

Unknown outcome, available/partial with a reason, unavailable/cancelled with an empty or wrong-family reason must return the empty Task outcome.

- [ ] **Step 3: Run focused tests and verify RED**

```powershell
& $go test ./apps/test-service/internal/task -run 'Coverage|Plan' -count=1
```

Expected: FAIL because coverage task/step constants and mapping do not exist.

- [ ] **Step 4: Implement vocabulary without enabling execution**

Add to `plan.go`:

```go
const KindCoverageRun Kind = "coverage_run"

const (
	StepCoverageConfigure StepKind = "coverage-configure"
	StepCoverageBuild     StepKind = "coverage-build"
	StepCoverageTest      StepKind = "coverage-test"
	StepCoverageMerge     StepKind = "coverage-merge"
	StepCoverageNormalize StepKind = "coverage-normalize"
	StepCoverageReport    StepKind = "coverage-report"
	StepCoveragePublish   StepKind = "coverage-publish"
)
```

Add all seven to `validStepKind`; `FingerprintPlan` and plan cloning stay kind-agnostic and require no special branch. Do not add a public request payload, coordinator, process launch, Manager registry entry, artifact projection or persistence mutation.

- [ ] **Step 5: Implement fail-closed outcome mapping**

In `model.go`, import `coveragedomain` and add:

```go
func CoverageTaskOutcome(
	outcome coveragedomain.Outcome,
	reason coveragedomain.Reason,
) Outcome
```

Use an explicit nested switch. Do not infer from strings, test process exit codes, summary percentages or artifact presence. This dependency direction is permitted only because `coveragedomain` never imports `task`.

- [ ] **Step 6: Run Task 3 GREEN and regression/race tests**

```powershell
& $go test ./apps/test-service/internal/task ./apps/test-service/internal/coveragedomain -run 'Coverage|Plan' -count=1
& $go test ./apps/test-service/internal/task ./apps/test-service/internal/coveragedomain -count=1
& $go test -race ./apps/test-service/internal/task ./apps/test-service/internal/coveragedomain -count=1
git diff --check
```

- [ ] **Step 7: Self-review Task 3**

- Confirm Task mapping covers every allowed run-level reason exactly once.
- Confirm Task Engine does not yet accept/start/resume `KindCoverageRun`; atomic aggregate creation remains deferred to migration 009 Task.
- Confirm v1.0-v1.3 behavior and existing fingerprint golden digest remain unchanged.
- Scan for placeholder branches and stringly-typed fallback mappings.

- [ ] **Step 8: Commit Task 3**

```powershell
git add apps/test-service/internal/task/plan.go apps/test-service/internal/task/plan_test.go apps/test-service/internal/task/model.go apps/test-service/internal/task/model_test.go
git commit -m "feat: add coverage task vocabulary"
```

## Final Verification

- [ ] Run focused domain and Task suites with pinned Go:

```powershell
$go = '.superpowers\runtime\go1.26.5-windows-amd64\go\bin\go.exe'
& $go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain ./apps/test-service/internal/task -count=1
& $go test -race ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain ./apps/test-service/internal/task -count=1
```

- [ ] Run repository-level generated-contract and formatting gates:

```powershell
$env:COREPACK_HOME = (Join-Path (Get-Location) '.superpowers\runtime\corepack')
$env:Path = (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64') + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:coverage-generated
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:protocol-generated
git diff --check
```

- [ ] Review package dependency direction:

```powershell
& $go list -deps ./apps/test-service/internal/coveragedomain
git grep -n -E 'TODO|TBD|placeholder|not implemented' -- apps/test-service/internal/coveragedomain apps/test-service/internal/task/model.go apps/test-service/internal/task/plan.go
```

- [ ] Perform final spec review against:

  - `docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md` Sections 6、7、15、16、17、20；
  - `packages/protocol-schema/schema/v1.4/coverage.schema.json`；
  - `packages/coverage-schema/schema/v1/coverage.schema.json`；
  - `docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md` Task 4。

- [ ] Confirm worktree contains no unexpected generated file, database, report, cache or native path fixture.
- [ ] Push every reviewed commit to both remotes and verify both remote branch SHAs equal local `HEAD`.
- [ ] Inspect the resulting GitHub workflow run; compare any failure with the slice base and report whether the slice introduced a new failure.
