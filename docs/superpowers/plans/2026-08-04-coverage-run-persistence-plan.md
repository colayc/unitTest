# Coverage Run Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 SQLite migration 009 持久化 CoverageRun/CoverageReport metadata，并提供严格限定的 get/list repository、Task/CoverageRun/TestRun/queued event 原子创建，以及 Task/TestRun/CoverageRun/Report/artifact/final event 原子终态提交。

**Architecture:** `coveragedomain` 继续拥有 coverage aggregate 语义；`taskstore` 只负责 canonical codec、关系约束、CAS 与 transaction。CoverageRun 只能通过 `CreateCoverageTask` 与所属 Task/TestRun 一起创建，CoverageReport 只能通过 `task.Store.Apply` 的 coverage completion 与 terminal aggregates、已经由 ArtifactStore publish 的 metadata 一起提交，不能提供会产生 orphan 的独立 create API。SQLite 只保存 request、selection snapshot、lifecycle、summary、tool provenance 与 artifact IDs，不保存 file/line coverage payload。`coverage/runs/list` 的 cursor 同时绑定 Workspace generation、Project 和 Coverage Profile filter，拒绝跨 scope 重放。

**Tech Stack:** Go 1.26.5、`database/sql`、modernc SQLite、标准库 `encoding/json`/`encoding/base64`/`reflect`、现有 `internal/task`、`internal/testdomain` 与 `internal/coveragedomain`。

## Global Constraints

- 本计划不启动 CMake、compiler、test executable、Python、gcovr、`llvm-profdata` 或 `llvm-cov`，不读取或写入 coverage payload body，不访问网络。
- Migration 009 必须从完整 migration 001–008 前缀升级；broken/cancelled/foreign-key-invalid migration 必须回滚 schema 与数据，并把原连接的 `PRAGMA foreign_keys` 恢复为原值。
- Migration 009 只新增 `coverage_runs`、`coverage_reports` 与必要 indexes；禁止新增 `coverage_files`、`coverage_lines`、逐文件/逐行 JSON column 或逐文件/逐行 event table。
- Migration 重建 `tasks`/`task_steps` 时必须保留 migration 008 的所有 column、row、index 与 foreign-key relation；只扩展 `coverage_run` Task kind 和七个已批准 coverage step kind。
- `coverage_runs.request_json` 必须是 `Request.CanonicalJSON()` 的逐 byte 值。读取时使用 strict decoder，经 `coveragedomain.NewRequest` 重验，再重新编码并逐 byte 比较；非 canonical 或含未知字段的数据库内容视为 `task.ErrStorageUnavailable`。
- 所有 selection、summary、toolchain、completeness JSON 都必须 strict decode 并通过 domain constructor 重验；未知字段、invalid enum、unsafe count、非 canonical completeness order 或不一致 lifecycle 均视为存储损坏。
- CoverageRun 创建只能经 `CreateCoverageTask`；单一 transaction 内依次持久化 Task、CoverageRun、TestRun、steps、queued event，并用实际 journal sequence 同时更新 Task 与 CoverageRun。
- `validateTask`/`validTaskKind`/Task list 必须识别 `coverage_run`，Task kind filter 上限从 4 调整为 5；但通用 `Store.Create` 必须显式拒绝 `coverage_run`，防止绕过专用 API 创建 orphan Task。`CreateTestTask` 仍只接受 `test_run`。
- 相同 canonical request/idempotency replay 返回原 Task 且不产生 event；replay 必须核对已存在 CoverageRun/TestRun 的 request、snapshot、toolchain 与测试上下文，关系缺失或语义不同返回 `task.ErrIdempotencyConflict`。
- `CreateCoverageTask` 必须验证 Task、CoverageRun、TestRun 对同一 Task/TestRun/request/workspace/project/catalog/selection/repeat/timeout 的引用一致；任何 storage/event/relation fault 都不能留下 Task、CoverageRun、TestRun、step 或 event orphan。
- Coverage terminal completion 只能通过一个 `task.Mutation`：Task、TestRun 与 CoverageRun 都进入 terminal，CoverageReport（仅 available/partial）、artifact metadata、steps、final event 与 journal sequence 在同一 transaction 中提交。
- available/partial 必须同时提交互不相同的 `coverage-json`/`application/json`、`junit-xml`/`application/xml`、`coverage-html`/`text/html` metadata；三个 artifact 必须属于同一 Task，Report artifact 必须等于 CoverageRun 的 Coverage JSON artifact。
- unavailable/cancelled 禁止 Report、summary 与 public coverage artifact refs。Report 的 run、test run、summary、toolchain、completeness 必须与终态 CoverageRun 完全一致。
- ArtifactStore 的 fsync/validation/final publish 在本 transaction API 之前发生；本切片以传入并通过 `validArtifact` 的 metadata 表示已 publish artifact。metadata insert、link 或 report insert 失败必须回滚所有 terminal row change。
- `coverage_runs` 与 `coverage_reports` 使用 foreign key 绑定同一 Task owner；不能把别的 Task 的 TestRun 或 artifact 连接到 CoverageRun/Report。
- terminal CoverageRun/Report 不可覆盖。CAS mismatch、第二次不同 completion 或 report replacement 返回 `task.ErrConflict`，保留首次 durable state。
- `ListCoverageRuns` 必须要求合法 `WorkspaceGeneration`，limit 默认 100、最大 200，排序固定为 `created_at DESC, coverage_run_id DESC`；cursor 内编码并验证 workspace/project/profile filter 与最后一项 identity。
- Manager 继续拒绝 `KindCoverageRun` 的通用 `Start`/`ResumeQueued`；本切片只开放 persistence port，不生成 plan、不启动 process。
- v1.0–v1.3 的 Task/TestRun/recovery/list/replay 行为保持不变；已有 migration checksum 不得修改。
- 所有实现按 red-green-refactor 完成；每个 Task 完成 spec review 和 quality review后独立提交，controller 随后推送 GitHub `github` 与 Gitee `origin` 的 `codex/workspace-cmake-toolchains`。

## Exact Public Contract

在 `coveragedomain/model.go` 增加 repository paging value，不让 persistence package 向 session/runtime 泄漏自定义 page 类型：

```go
type RunPageRequest struct {
	WorkspaceGeneration string
	ProjectID           string
	CoverageProfileID   string
	Cursor              string
	Limit               int
}

type RunPage struct {
	Items      []Run
	NextCursor string
}

const (
	DefaultRunPageSize = 100
	MaxRunPageSize     = 200
)
```

在 `task/ports.go` 增加原子 creation/completion contract：

```go
type CoverageCompletion struct {
	Run      coveragedomain.Run
	Expected coveragedomain.Status
	Report   *coveragedomain.Report
}

type Mutation struct {
	// existing fields remain unchanged
	FinishCoverage *CoverageCompletion
}

type CoverageTaskStore interface {
	CreateCoverageTask(
		context.Context,
		Task,
		[]StepSnapshot,
		EventDraft,
		coveragedomain.Run,
		testdomain.TestRun,
	) (Task, []Event, error)
}

type CoverageRepository interface {
	GetCoverageRun(context.Context, string) (coveragedomain.Run, error)
	ListCoverageRuns(context.Context, coveragedomain.RunPageRequest) (coveragedomain.RunPage, error)
	GetCoverageReport(context.Context, string) (coveragedomain.Report, error)
}
```

No public `CreateCoverageRun`、`CreateCoverageReport` or `ReplaceCoverageReport` method is allowed. The compile-time assertions are:

```go
var _ task.CoverageTaskStore = (*Store)(nil)
var _ task.CoverageRepository = (*Store)(nil)
```

`task.Mutation.FinishCoverage` is valid only when `FinishRun` is also non-nil and all of these are true:

```text
Task.Kind                         = coverage_run
Task.Status                       = finished
Task.Outcome                      = CoverageTaskOutcome(Run.Outcome, Run.Reason)
CoverageCompletion.Expected       = queued | running
CoverageCompletion.Run.Status     = finished
CoverageRun.TaskID                = Task.ID
CoverageRun.TestRunID             = FinishRun.RunID
FinishRun.TaskID                  = Task.ID
FinishRun.Status                  = completed
Report present                    iff outcome = available | partial
Task final event present          = task.finished
```

## Exact SQLite 009 Contract

Migration 009 starts with `-- unit-test-ide: foreign-keys-off`, rebuilds `tasks_v9`/`task_steps_v9` from the exact migration 008 shape, and expands only these `CHECK` lists:

```sql
kind IN ('simulation','cmake_build','test_discovery','test_run','coverage_run')

step_kind IN (
  'simulation','configure','build','test-discovery','test-run',
  'coverage-configure','coverage-build','coverage-test','coverage-merge',
  'coverage-normalize','coverage-report','coverage-publish'
)
```

After rename/index recreation, migration 009 creates owner-aware indexes and these tables. Implement the SQL with the stated columns and equivalent closed `CHECK` constraints; do not weaken any ID, lifecycle or JSON constraint:

```sql
CREATE UNIQUE INDEX artifacts_identity_task
  ON artifacts(artifact_id, task_id);
CREATE UNIQUE INDEX test_runs_identity_task
  ON test_runs(run_id, task_id);

CREATE TABLE coverage_runs (
  coverage_run_id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL UNIQUE REFERENCES tasks(task_id) ON DELETE CASCADE,
  test_run_id TEXT NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL UNIQUE,
  request_json TEXT NOT NULL CHECK (json_valid(request_json)),
  workspace_generation TEXT NOT NULL,
  project_id TEXT NOT NULL,
  coverage_profile_id TEXT NOT NULL,
  catalog_revision TEXT NOT NULL,
  selection_snapshot_json TEXT NOT NULL CHECK (json_valid(selection_snapshot_json)),
  repeat_count INTEGER NOT NULL CHECK (repeat_count BETWEEN 1 AND 100),
  timeout_ms INTEGER NOT NULL CHECK (timeout_ms BETWEEN 1 AND 86400000),
  status TEXT NOT NULL CHECK (status IN ('queued','running','finished')),
  outcome TEXT CHECK (outcome IS NULL OR outcome IN ('available','partial','unavailable','cancelled')),
  reason TEXT,
  toolchain_json TEXT NOT NULL CHECK (json_valid(toolchain_json)),
  summary_json TEXT CHECK (summary_json IS NULL OR json_valid(summary_json)),
  report_id TEXT UNIQUE,
  coverage_json_artifact_id TEXT,
  junit_xml_artifact_id TEXT,
  coverage_html_artifact_id TEXT,
  created_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  last_sequence INTEGER NOT NULL DEFAULT 0 CHECK (last_sequence BETWEEN 0 AND 9007199254740991),
  UNIQUE(coverage_run_id, task_id),
  FOREIGN KEY(test_run_id, task_id) REFERENCES test_runs(run_id, task_id) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY(coverage_json_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id),
  FOREIGN KEY(junit_xml_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id),
  FOREIGN KEY(coverage_html_artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id)
);

CREATE TABLE coverage_reports (
  report_id TEXT PRIMARY KEY,
  coverage_run_id TEXT NOT NULL UNIQUE,
  task_id TEXT NOT NULL,
  test_run_id TEXT NOT NULL UNIQUE,
  schema_version TEXT NOT NULL CHECK (schema_version='1.0'),
  created_at TEXT NOT NULL,
  completeness_json TEXT NOT NULL CHECK (json_valid(completeness_json)),
  summary_json TEXT NOT NULL CHECK (json_valid(summary_json)),
  toolchain_json TEXT NOT NULL CHECK (json_valid(toolchain_json)),
  artifact_id TEXT NOT NULL UNIQUE,
  FOREIGN KEY(coverage_run_id, task_id) REFERENCES coverage_runs(coverage_run_id, task_id) ON DELETE CASCADE,
  FOREIGN KEY(test_run_id, task_id) REFERENCES test_runs(run_id, task_id),
  FOREIGN KEY(artifact_id, task_id) REFERENCES artifacts(artifact_id, task_id)
);
```

Add explicit lowercase-hex length checks for all 32/64-hex columns, closed reason/lifecycle checks, report-bearing versus non-report-bearing nullability checks, distinct public artifact ID checks, and indexes for:

```text
(workspace_generation, created_at DESC, coverage_run_id DESC)
(workspace_generation, project_id, created_at DESC, coverage_run_id DESC)
(workspace_generation, project_id, coverage_profile_id, created_at DESC, coverage_run_id DESC)
```

The application must still revalidate every row; SQL constraints are defense in depth, not the domain constructor replacement.

## File Responsibility Map

| Path | Single responsibility |
|---|---|
| `apps/test-service/internal/coveragedomain/model.go` | bounded workspace-scoped Run page request/result types |
| `apps/test-service/internal/task/ports.go` | coverage creation/repository ports and terminal completion payload |
| `apps/test-service/internal/taskstore/migrations/009_coverage_runs.sql` | Task/step vocabulary expansion, coverage metadata tables, owner-aware FKs/indexes |
| `apps/test-service/internal/taskstore/sqlite_test.go` | migration 001–008 upgrade, broken 009 rollback/FK restore, legacy replay regression |
| `apps/test-service/internal/taskstore/coverage_runs.go` | strict codecs, row validation, get/list/cursor, creation insert/replay helpers, terminal run CAS |
| `apps/test-service/internal/taskstore/coverage_runs_test.go` | persisted round trip, corrupted row rejection, scoped stable paging, terminal immutability |
| `apps/test-service/internal/taskstore/coverage_reports.go` | report codec/get/insert and run/test/artifact linkage validation |
| `apps/test-service/internal/taskstore/coverage_reports_test.go` | report round trip and wrong owner/kind/MIME/run/test/summary/provenance rejection |
| `apps/test-service/internal/taskstore/coverage_task_creation_test.go` | atomic creation, idempotent replay, conflicting aggregate, insert/event fault rollback |
| `apps/test-service/internal/taskstore/tasks.go` | coverage-aware creation relation transaction and coverage terminal integration in `Apply` |
| `apps/test-service/internal/taskstore/events.go` | reusable exact event-presence validation without changing journal ordering |
| `apps/test-service/internal/taskstore/artifacts.go` | closed coverage artifact kind/MIME/owner validator |

---

### Task 1: Migration 009 与 persistence port skeleton

**Files:**

- Modify: `apps/test-service/internal/coveragedomain/model.go`
- Modify: `apps/test-service/internal/task/ports.go`
- Create: `apps/test-service/internal/taskstore/migrations/009_coverage_runs.sql`
- Modify: `apps/test-service/internal/taskstore/sqlite_test.go`

- [ ] **Step 1: Write RED migration upgrade/rollback tests**

Add `TestMigration009UpgradesCoverageSchemaAndPreservesRelations` that:

- loads migrations and asserts the last entry is exactly version 9 without changing checksums 1–8;
- applies migrations 1–8 to a configured database;
- persists a legacy Task, steps, event, artifact, TestRun and process lease before 009;
- applies 009, reloads every legacy relation, runs `PRAGMA foreign_key_check`, and asserts `PRAGMA foreign_keys=1`;
- 通过 direct SQL 创建一个 `coverage_run` Task 与全部七种 coverage step，证明 migration constraint 接受新 vocabulary；本 Task 尚不让通用 `Store.Create` 创建 coverage Task；
- inspects `sqlite_master`/`pragma_table_info` and proves only `coverage_runs`/`coverage_reports` hold coverage metadata and there is no file/line payload table or column.

Add `TestMigration009FailureRollsBackAndRestoresForeignKeys` by cloning migration 009 and appending an invalid statement. Assert:

- `applyMigration` returns an error matching `task.ErrStorageUnavailable`;
- schema migration count remains 8;
- pre-009 Task/step/event/artifact/TestRun/lease rows still load;
- no `coverage_runs`/`coverage_reports` table or v9 replacement table survives;
- `PRAGMA foreign_keys` is restored to 1 and `PRAGMA foreign_key_check` is empty.

- [ ] **Step 2: Run migration tests and verify RED**

```powershell
$go = '.superpowers\runtime\go1.26.5-windows-amd64\go\bin\go.exe'
$env:GOCACHE = Join-Path (Get-Location) '.superpowers\cache\task5-migration'
& $go test ./apps/test-service/internal/taskstore -run 'Migration009' -count=1
```

Expected: FAIL because migration 009 and coverage persistence types do not exist.

- [ ] **Step 3: Add bounded paging and persistence ports**

Add the exact `RunPageRequest`/`RunPage` types and 100/200 constants above. Add `CoverageCompletion` and the two task ports above. Importing `coveragedomain` from `task` is permitted because dependency direction already is `task -> coveragedomain -> testdomain`; do not add a reverse import.

Keep `Store` itself unchanged except for the existing `Apply(context.Context, Mutation)` carrying the new optional completion. Do not enable Manager coverage start/resume.

- [ ] **Step 4: Implement migration 009**

Copy migration 008's current Task and step shape exactly into v9 tables, add only the new accepted literals, then create the exact coverage tables/indexes described above. All Task relations must continue referencing the renamed final `tasks` table after commit.

Use SQL `CHECK` branches mirroring `coveragedomain.NewRun`:

```text
queued   -> no start/finish/outcome/reason/report/summary/artifacts
running  -> started only, no terminal/report fields
finished available|partial -> finish + summary + report + all artifacts, no reason
finished unavailable|cancelled -> finish + closed-family reason, no report fields
```

- [ ] **Step 5: Run Task 1 GREEN and regression tests**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'Migration009|Migration|Replay' -count=1
& $go test ./apps/test-service/internal/task ./apps/test-service/internal/coveragedomain -run 'Coverage|ManagerRejectsCoverage' -count=1
git diff --check
```

- [ ] **Step 6: Self-review Task 1**

- Diff migration 009 Task/step columns against migration 008 and verify no column/default/index was dropped.
- Confirm all coverage IDs/revisions/lifecycle states have SQL checks and all owner relations use FKs.
- Confirm no existing migration changed with `git diff -- apps/test-service/internal/taskstore/migrations/001_initial.sql .../008_batch_process_leases.sql`.
- Confirm `rg -n 'coverage_(files|lines)|file.*json|line.*json' apps/test-service/internal/taskstore/migrations/009_coverage_runs.sql` has no payload table/column.
- Confirm Manager's coverage rejection tests still pass.

- [ ] **Step 7: Commit Task 1**

```powershell
git add apps/test-service/internal/coveragedomain/model.go apps/test-service/internal/task/ports.go apps/test-service/internal/taskstore/migrations/009_coverage_runs.sql apps/test-service/internal/taskstore/sqlite_test.go
git commit -m "feat: add coverage persistence schema"
```

---

### Task 2: Strict CoverageRun repository 与 workspace-scoped paging

**Files:**

- Create: `apps/test-service/internal/taskstore/coverage_runs.go`
- Create: `apps/test-service/internal/taskstore/coverage_runs_test.go`

- [ ] **Step 1: Write RED codec/get/list tests**

Create fixtures for Windows clang-cl/LLVM, Linux GCC/gcovr and Linux Clang/LLVM. Test:

- strict round trip of queued, running, available, partial, unavailable and cancelled rows through private insert/test helpers plus `GetCoverageRun`;
- request JSON stored byte-for-byte equal to `Request.CanonicalJSON()`;
- selection snapshot, toolchain, summary and all UTC timestamps round trip without aliasing;
- `task.ErrInvalidArgument` for nil store/context, malformed run ID, invalid page scope/filter/cursor/limit;
- `task.ErrNotFound` for a valid missing run ID;
- `task.ErrStorageUnavailable` for injected non-canonical request JSON, unknown JSON field, invalid toolchain, corrupt summary, lifecycle mismatch or bad time;
- ordering by `created_at DESC, coverage_run_id DESC`, exact limit 1/200 bounds and stable traversal with equal timestamps;
- default limit 100;
- cursor rejection when Workspace generation, Project ID or Coverage Profile ID differs from the request that created it;
- mandatory Workspace generation prevents rows from another workspace from appearing even if project/profile match;
- returned runs/pages are defensive copies.

Use an opaque cursor payload with exact fields:

```go
type coverageRunCursor struct {
	WorkspaceGeneration string    `json:"workspaceGeneration"`
	ProjectID           string    `json:"projectId"`
	CoverageProfileID   string    `json:"coverageProfileId"`
	CreatedAt           time.Time `json:"createdAt"`
	CoverageRunID       string    `json:"coverageRunId"`
}
```

- [ ] **Step 2: Run repository tests and verify RED**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'CoverageRun.*(RoundTrip|List|Cursor|Corrupt|Argument)' -count=1
```

Expected: FAIL because `coverage_runs.go` and repository methods do not exist.

- [ ] **Step 3: Implement closed JSON codecs**

Define private camelCase wire structs for request, selection, toolchain, summary and artifact refs. For request decode:

1. `decodeStrictJSON` into the private eight-field request wire;
2. map milliseconds to `time.Duration` with overflow/bounds checked before multiplication;
3. call `coveragedomain.NewRequest`;
4. call `CanonicalJSON` and require `bytes.Equal(canonical, stored)`.

Encode request only through `Run.Request.CanonicalJSON()`. Encode other values only after `coveragedomain.NewRun`; on scan reconstruct the full Run and call `coveragedomain.NewRun` again. Never silently normalize a corrupt persisted row into a valid row.

- [ ] **Step 4: Implement get/list and stable cursor**

`GetCoverageRun` validates 32 lowercase hex input, maps `sql.ErrNoRows` to `task.ErrNotFound`, wraps DB/codec/domain corruption with `task.ErrStorageUnavailable`, and returns the validated clone.

`ListCoverageRuns`:

- validates required 64-hex Workspace generation and optional stable Project/Profile IDs;
- applies default 100 and max 200;
- binds all filters in cursor and query;
- fetches `limit+1`, encodes the last returned `(createdAt, coverageRunId)`, and never loads report/artifact bodies;
- validates every returned row before exposing any page.

- [ ] **Step 5: Run Task 2 GREEN/race and self-review**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'CoverageRun' -count=1
& $go test -race ./apps/test-service/internal/taskstore -run 'CoverageRun' -count=1
git diff --check
```

- Compare private request wire keys and bounds to `coveragedomain.Request.CanonicalJSON` and Protocol v1.4.
- Verify no decode path uses plain `json.Unmarshal` without unknown-field rejection.
- Verify cursor filter comparisons occur before SQL execution.
- Verify the file contains no independent public CoverageRun create method.

- [ ] **Step 6: Commit Task 2**

```powershell
git add apps/test-service/internal/taskstore/coverage_runs.go apps/test-service/internal/taskstore/coverage_runs_test.go
git commit -m "feat: persist coverage run history"
```

---

### Task 3: Task/CoverageRun/TestRun/queued event 原子创建

**Files:**

- Modify: `apps/test-service/internal/taskstore/tasks.go`
- Create: `apps/test-service/internal/taskstore/coverage_task_creation_test.go`

- [ ] **Step 1: Write RED validation and atomicity tests**

Create a valid queued fixture whose Task request bytes are exactly `CoverageRun.Request.CanonicalJSON()`. Test successful `CreateCoverageTask` persists:

- one `coverage_run` Task with all supplied coverage steps;
- one queued CoverageRun and one queued TestRun owned by that Task;
- one `task.created` event;
- identical non-zero `LastSequence` on Task and CoverageRun;
- canonical request bytes, request/workspace/project/catalog/selection/repeat/timeout alignment.

Add table-driven invalid alignment cases for Task kind/idempotency/workspace/request/timeout, CoverageRun ID/task/test run/status/sequence, TestRun task/project/catalog/selection/iterations/status, event owner/type and invalid/duplicate coverage steps. Every case must return `task.ErrInvalidArgument` before a transaction leaves state.

- [ ] **Step 2: Write RED idempotency tests**

Test replay with regenerated Task/TestRun ownership IDs and creation timestamps but the same canonical request and the same resolved snapshot/toolchain/test context. It must return the original Task, no events and no new rows.

Then mutate each non-generated semantic component independently:

- canonical request;
- CoverageRun selection snapshot;
- CoverageRun toolchain;
- TestRun Project/Profile/Toolchain/Catalog/selection/iterations.

Each mutation must return `task.ErrIdempotencyConflict` and preserve exactly one aggregate set. Delete or corrupt the existing linked CoverageRun/TestRun through direct test SQL and prove replay fails closed instead of returning a bare Task.

- [ ] **Step 3: Write RED fault-injection tests**

Use SQLite abort triggers separately on `coverage_runs`, `test_runs`, `task_steps` and `task_events`. For every injected failure assert:

- returned error matches `task.ErrStorageUnavailable`;
- no Task, CoverageRun, TestRun, step or event row for the request survives;
- global event watermark does not advance;
- foreign-key check remains empty.

- [ ] **Step 4: Run atomic creation tests and verify RED**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'CreateCoverageTask|CoverageCreation' -count=1
```

Expected: FAIL because `CreateCoverageTask` and relation-aware create transaction do not exist.

- [ ] **Step 5: Implement relation-aware creation**

Refactor the private `createTask` path to accept optional validated creation relations without changing `Create` or `CreateTestTask` behavior. `CreateCoverageTask` must:

1. reject nil store/context and validate Task/steps/event;
2. validate CoverageRun via `coveragedomain.NewRun` and TestRun via `testdomain.NewTestRun`;
3. require both aggregates queued and TestRun results empty;
4. require the exact alignment matrix in Step 1;
5. begin one transaction;
6. insert Task, CoverageRun, TestRun, steps and queued event;
7. update both `tasks.last_sequence` and `coverage_runs.last_sequence` from the inserted event;
8. commit and return a Task with cloned steps.

Update Task validation/list support deliberately:

- `validateTask` treats `coverage_run` like other workspace-owned kinds and requires a valid Workspace generation with no simulation scenario;
- `validTaskKind` accepts it and `List` allows all five distinct Task kinds;
- `Store.Create` rejects `coverage_run` before calling the shared private insertion path, so only `CreateCoverageTask` may create it;
- add regression tests proving generic create rejection has zero rows/events and Task list can filter persisted coverage Tasks.

On the Task idempotency unique conflict, load the existing Task plus its linked CoverageRun/TestRun inside the same transaction. Compare request-derived semantics while normalizing only regenerated owner IDs and creation timestamps. Never accept missing relation rows, changed snapshot/toolchain/test context or a non-queued existing aggregate.

- [ ] **Step 6: Run Task 3 GREEN/race and regressions**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'CreateCoverageTask|CoverageCreation|CreateTestTask' -count=1
& $go test -race ./apps/test-service/internal/taskstore -run 'CreateCoverageTask|CoverageCreation|CreateTestTask' -count=1
& $go test ./apps/test-service/internal/task -run 'ManagerRejectsCoverage' -count=1
git diff --check
```

- [ ] **Step 7: Self-review Task 3**

- Trace every return after `BeginTx` and confirm deferred rollback covers it.
- Verify event sequence is written to both aggregates before commit.
- Verify replay comparison includes both linked aggregates and cannot return an orphan Task.
- Verify ordinary Task/TestTask idempotency behavior is unchanged.
- Verify no Manager/process/artifact writer is invoked.

- [ ] **Step 8: Commit Task 3**

```powershell
git add apps/test-service/internal/taskstore/tasks.go apps/test-service/internal/taskstore/coverage_task_creation_test.go
git commit -m "feat: create coverage tasks atomically"
```

---

### Task 4: Atomic terminal completion 与 CoverageReport repository

**Files:**

- Modify: `apps/test-service/internal/taskstore/tasks.go`
- Modify: `apps/test-service/internal/taskstore/events.go`
- Modify: `apps/test-service/internal/taskstore/artifacts.go`
- Modify: `apps/test-service/internal/taskstore/coverage_runs.go`
- Create: `apps/test-service/internal/taskstore/coverage_reports.go`
- Create: `apps/test-service/internal/taskstore/coverage_reports_test.go`
- Modify: `apps/test-service/internal/taskstore/coverage_runs_test.go`

- [ ] **Step 1: Write RED report linkage tests**

For available and partial runs, build valid terminal CoverageRun/Report/TestRun and three artifacts. Assert a successful coverage completion:

- updates Task/TestRun/CoverageRun terminal state;
- inserts exactly one CoverageReport;
- inserts and links all artifact metadata;
- makes `GetCoverageReport` round trip completeness reasons, summary, toolchain and UTC timestamp;
- sets Report artifact to the Coverage JSON artifact and exposes all three IDs from CoverageRun;
- sets Task and CoverageRun `LastSequence` to the same final event sequence.

Table-drive rejection for:

- wrong report/run/test run identity;
- Report summary/toolchain/completeness different from CoverageRun;
- artifact from another Task;
- missing/duplicate artifact ID or path;
- wrong `coverage-json`/`junit-xml`/`coverage-html` kind or MIME pair;
- Report artifact not equal to CoverageRun Coverage JSON ID;
- available/partial without Report or any one public artifact;
- unavailable/cancelled with Report/summary/public artifacts;
- Task outcome not equal to `task.CoverageTaskOutcome`;
- missing `task.finished` event;
- coverage completion without terminal TestRun, and terminal TestRun for a coverage Task without coverage completion.

- [ ] **Step 2: Write RED terminal immutability and fault tests**

After a successful completion, retry an identical and then a changed completion with expected queued/running status. Both must not create a second Report or change durable rows; the changed attempt must return `task.ErrConflict`.

Inject SQLite abort triggers separately on:

- `task_events` final insert;
- `artifacts` metadata insert;
- `test_run_artifacts` link insert;
- `test_runs` terminal update;
- `coverage_reports` insert;
- `coverage_runs` terminal update.

For each fault, assert the Task/TestRun/CoverageRun remain at their pre-terminal state, no report/artifact/link row survives, event watermark is unchanged and foreign-key check is empty.

Also cover unavailable/cancelled completion without a Report/artifact trio and prove the terminal reason remains immutable.

- [ ] **Step 3: Run completion/report tests and verify RED**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'Coverage(Report|Completion|Terminal|Artifact)' -count=1
```

Expected: FAIL because report repository and coverage completion integration do not exist.

- [ ] **Step 4: Implement closed coverage artifact validation**

In `artifacts.go`, add a private validator that first calls `validateRunArtifacts`, then indexes artifacts by ID and requires these exact pairs for report-bearing runs:

```text
CoverageJSONID -> coverage-json  / application/json
JUnitXMLID     -> junit-xml      / application/xml
CoverageHTMLID -> coverage-html / text/html
```

Require distinct IDs/paths and the CoverageRun Task owner. Extra bounded diagnostics/stdout/stderr artifacts may remain in the same mutation, but no extra artifact may masquerade as a public coverage kind.

- [ ] **Step 5: Implement report codec and GetCoverageReport**

Encode/decode private camelCase completeness/summary/toolchain wire structs. On read call `coveragedomain.NewReport`; map missing valid ID to `task.ErrNotFound` and any DB/codec/domain corruption to `task.ErrStorageUnavailable`.

The private `insertCoverageReport` receives the validated terminal Run and Task owner, requires all cross-aggregate equality, then inserts only after artifact metadata exists in the same transaction. There is no upsert or replace branch.

- [ ] **Step 6: Integrate coverage completion into `Store.Apply`**

Before opening the transaction, validate the complete matrix in **Exact Public Contract**. Preserve the existing TestRun-only branch. For coverage completion:

1. CAS-update Task using `Mutation.Expected`;
2. apply step/event/lease changes;
3. insert all artifact metadata;
4. finish and link the terminal TestRun with `finishRunTx(..., insertArtifacts=false)`;
5. insert CoverageReport when required;
6. CAS-update CoverageRun from `CoverageCompletion.Expected` to finished;
7. calculate the durable final sequence and update both Task and CoverageRun;
8. commit once.

The CoverageRun update must match ID, Task ID, TestRun ID and expected status. `status='finished'` is never a valid expected value. If any affected-row count is not one, return `task.ErrConflict` and let the transaction roll back.

Use a small event helper in `events.go` to require the exact existing `task.EventTaskFinished` type; do not add per-file events or accept unknown coverage event strings in this persistence Task.

- [ ] **Step 7: Run Task 4 GREEN/race and full taskstore tests**

```powershell
& $go test ./apps/test-service/internal/taskstore -run 'Coverage(Report|Completion|Terminal|Artifact)' -count=1
& $go test -race ./apps/test-service/internal/taskstore -run 'Coverage(Report|Completion|Terminal|Artifact)' -count=1
& $go test ./apps/test-service/internal/taskstore -count=1
& $go test -race ./apps/test-service/internal/taskstore -count=1
git diff --check
```

- [ ] **Step 8: Self-review Task 4**

- Trace artifact publication boundary: file publish is caller-owned and precedes mutation; all DB metadata/link/report rows are one transaction.
- Confirm Report cannot reference a different run, TestRun, Task or artifact owner and its payload metadata equals terminal Run.
- Confirm coverage terminal flow always includes terminal TestRun and exact Task outcome mapping.
- Confirm all CAS and unique/FK errors preserve the first terminal state.
- Confirm no public create/replace report method and no file/line storage/event path exists.
- Confirm existing TestRun-only `Store.Apply` and `FinishRun` behavior remains covered.

- [ ] **Step 9: Commit Task 4**

```powershell
git add apps/test-service/internal/taskstore/tasks.go apps/test-service/internal/taskstore/events.go apps/test-service/internal/taskstore/artifacts.go apps/test-service/internal/taskstore/coverage_runs.go apps/test-service/internal/taskstore/coverage_runs_test.go apps/test-service/internal/taskstore/coverage_reports.go apps/test-service/internal/taskstore/coverage_reports_test.go
git commit -m "feat: persist coverage run metadata"
```

## Final Verification

- [ ] Run focused domain/task/taskstore suites with a worktree-local cache:

```powershell
$go = '.superpowers\runtime\go1.26.5-windows-amd64\go\bin\go.exe'
$env:GOCACHE = Join-Path (Get-Location) '.superpowers\cache\task5-final'
& $go test ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
& $go test -race ./apps/test-service/internal/coveragedomain ./apps/test-service/internal/testdomain ./apps/test-service/internal/task ./apps/test-service/internal/taskstore -count=1
```

- [ ] Run repository contract/drift gates with the pinned Node runtime:

```powershell
$env:COREPACK_HOME = Join-Path (Get-Location) '.superpowers\runtime\corepack'
$env:Path = (Join-Path (Get-Location) '.superpowers\runtime\node-v24.18.0-win-x64') + ';' + $env:Path
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:coverage-generated
& '.superpowers\runtime\node-v24.18.0-win-x64\corepack.cmd' pnpm check:protocol-generated
git diff --check
```

- [ ] Run architecture/placeholder/storage-shape gates:

```powershell
& $go list -deps ./apps/test-service/internal/taskstore
git grep -n -E 'TODO|TBD|placeholder|not implemented' -- apps/test-service/internal/coveragedomain apps/test-service/internal/task/ports.go apps/test-service/internal/taskstore
rg -n 'coverage_(files|lines)|file.*coverage.*json|line.*coverage.*json' apps/test-service/internal/taskstore
```

- [ ] Confirm only `taskstore` imports SQLite and `coveragedomain` still imports neither `task` nor persistence/protocol/process packages.
- [ ] Run `git status --short` and remove only the worktree-local `.superpowers/cache/task5-*` directories after resolving their absolute paths inside the worktree; do not delete user files.
- [ ] Perform final spec review against:

  - `docs/superpowers/specs/2026-08-03-coverage-report-pipeline-design.md` Sections 5.6、6、15、16、17、22；
  - `packages/protocol-schema/schema/v1.4/coverage.schema.json`；
  - `packages/protocol-schema/schema/v1.4/message.schema.json` coverage methods/list payload；
  - `packages/protocol-schema/schema/v1.4/artifact.schema.json`；
  - `docs/superpowers/plans/2026-08-03-phase5-coverage-contract-domain-plan.md` Task 5。

- [ ] Perform final quality review over all plan commits and fix all findings in one final fix wave before re-review.
- [ ] Push every reviewed commit to GitHub `github` and Gitee `origin`, then verify both remote branch SHAs equal local `HEAD`.
- [ ] Inspect the new GitHub workflow run and compare its exact failing-test set with base run `30873347506`; report separately whether this slice introduced a new failure.
