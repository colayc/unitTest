# Phase 5E：JUnit/HTML renderer 与 artifact terminal transaction 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 从 canonical Coverage JSON 与关联 TestRun projection 生成 deterministic JSON/JUnit/单文件 HTML，并以 durable-before-publish transaction 完成 CoverageRun/TestRun/Task。

**架构：** Go `coveragereport` 负责 JUnit、HTML envelope、artifact staging 和 validation；`packages/report-ui` 提供 build-time 固定 browser asset，不在产品运行时启动 Node。Coverage JSON 提供 metric/provenance，TestRun 提供 testcase outcome。大型 source/line 数据只进入 artifact。

**依赖：** Phase 5A domain/store、5D canonical Coverage JSON。

## 全局约束

- report body 不含随机 ID、timestamp、duration、absolute path、environment、raw command 或 Service token。
- JUnit testcase outcome 不由 coverage percentage 或 completeness 改写。
- HTML 不访问网络，不使用 CDN/font/source map/form/frame/remote image，不执行 `eval`/dynamic code。
- source 只有在 workspace boundary、digest、size 全部通过时才能嵌入；所有 content context-aware escape。
- artifact 先 close/digest/validate/publish，再提交 SQLite metadata/terminal event；publisher 只消费 durable event。
- raw/intermediate 文件不是 report artifact。

---

### Task 1：Coverage artifact kinds、staging 与 structural validators

**文件：**

- 创建：`apps/test-service/internal/artifactstore/coverage.go`
- 创建：`apps/test-service/internal/artifactstore/coverage_test.go`
- 创建：`apps/test-service/internal/coveragereport/staging.go`
- 创建：`apps/test-service/internal/coveragereport/staging_test.go`
- 创建：`apps/test-service/internal/coveragereport/validate.go`
- 创建：`apps/test-service/internal/coveragereport/validate_test.go`
- 修改：`apps/test-service/internal/taskstore/artifacts.go`
- 修改：`apps/test-service/internal/taskstore/artifacts_test.go`

- [ ] **Step 1：写出 artifact path/close/digest/validation 失败测试**

覆盖：

- kinds 仅 `coverage-json|junit-xml|coverage-html`；
- unpredictable physical name 与 stable logical kind；
- Task-owned staging/final path；
- symlink/reparse/path escape/overwrite 拒绝；
- writer close 前不能 publish；
- size/digest mismatch、empty/truncated/oversized 拒绝；
- Coverage JSON schema version/UTF-8；
- JUnit XML well-formed/no DTD/entity；
- HTML doctype/CSP/asset digest/no external URL；
- cancellation/failure cleanup only owned staging；
- raw profile kind 无法注册。

- [ ] **Step 2：运行 artifact tests 并确认失败**

```powershell
go test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragereport ./apps/test-service/internal/taskstore -run 'Coverage|Artifact|Staging|Validate' -count=1
```

- [ ] **Step 3：实现 staged report set**

`ReportSet` 要么三种 artifact 全部 publish 成功，要么不返回可提交 metadata。已物理 publish 但 DB 未引用的 orphan 由 startup cleanup 根据 owner manifest 回收。

- [ ] **Step 4：运行全套/race**

```powershell
go test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragereport ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/artifactstore ./apps/test-service/internal/coveragereport -count=1
```

- [ ] **Step 5：提交 coverage artifact contract**

```powershell
git add apps/test-service/internal/artifactstore apps/test-service/internal/coveragereport apps/test-service/internal/taskstore
git commit -m "feat: stage coverage report artifacts"
```

### Task 2：Deterministic JUnit XML renderer

**文件：**

- 创建：`apps/test-service/internal/coveragereport/junit.go`
- 创建：`apps/test-service/internal/coveragereport/junit_test.go`
- 创建：`apps/test-service/internal/coveragereport/testdata/junit-pass.golden.xml`
- 创建：`apps/test-service/internal/coveragereport/testdata/junit-failure.golden.xml`
- 创建：`apps/test-service/internal/coveragereport/testdata/junit-error-skip.golden.xml`
- 创建：`apps/test-service/internal/coveragereport/testdata/junit-escaping.golden.xml`
- 修改：`apps/test-service/internal/testrun/summary.go`
- 修改：`apps/test-service/internal/testrun/summary_test.go`

- [ ] **Step 1：写出 outcome/properties/escaping/determinism 失败测试**

覆盖：

- pass/failure/error/skip/not_run mapping；
- repeat iteration 独立 testcase 与 stable order；
- suite total/failure/error/skipped count；
- Coverage JSON SHA-256、compiler/driver/collector/version properties；
- assertion/mock/source location；
- XML special char、CDATA terminator、control char、Unicode；
- bounded name/message/diagnostic；
- no time/timestamp/run ID/artifact ID/native path；
- no external entity/DOCTYPE；
- repeated input byte-identical XML。

- [ ] **Step 2：运行 JUnit tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/testrun -run 'JUnit|Summary' -count=1
```

- [ ] **Step 3：实现 streaming XML encoder**

Renderer 接收 immutable TestRun report projection 与 Coverage provenance，不读取 SQLite/native file。写入前 stable sort；invalid XML codepoint 使用 replacement/diagnostic contract，不能产生 malformed XML。

- [ ] **Step 4：运行 Golden/race**

```powershell
go test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/coveragereport -count=1
```

- [ ] **Step 5：提交 JUnit renderer**

```powershell
git add apps/test-service/internal/coveragereport apps/test-service/internal/testrun
git commit -m "feat: render deterministic junit reports"
```

### Task 3：`packages/report-ui` deterministic static asset

**文件：**

- 创建：`packages/report-ui/package.json`
- 创建：`packages/report-ui/tsconfig.json`
- 创建：`packages/report-ui/tsconfig.browser.json`
- 创建：`packages/report-ui/src/model.ts`
- 创建：`packages/report-ui/src/format.ts`
- 创建：`packages/report-ui/src/render.ts`
- 创建：`packages/report-ui/src/browser.ts`
- 创建：`packages/report-ui/src/index.ts`
- 创建：`packages/report-ui/src/report-ui.test.ts`
- 创建：`packages/report-ui/styles/report.css`
- 创建：`packages/report-ui/build.mjs`
- 创建：`packages/report-ui/build.test.mjs`
- 创建：`packages/report-ui/dist/report-ui.js`
- 创建：`packages/report-ui/dist/report-ui.css`
- 创建：`packages/report-ui/dist/manifest.json`
- 修改：`package.json`

- [ ] **Step 1：写出 model/render/build determinism 失败测试**

覆盖：

- strict Coverage JSON v1 model；
- integer percentage formatting/zero total；
- available/partial badge；
- compiler/driver/version；
- file/line/branch tree；
- test outcome projection；
- HTML text/attribute escaping；
- no `innerHTML` with untrusted value；
- asset no network/eval/Function/dynamic import/source map；
- CSS/JS fixed order、LF、SHA-256 manifest；
- second build no diff。

- [ ] **Step 2：运行 package tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/report-ui test
```

- [ ] **Step 3：实现 browser-compatible report core**

Core formatting/model 不依赖 Code-OSS API。Browser entry 只读取 document 中 fixed element/data；测试用纯 function/DOM fixture。Build 使用 repository-owned Node script + TypeScript `outFile`，不引入 runtime CDN 或 unpinned bundler。`packages/*` 已由现有 workspace glob 覆盖，不修改 `pnpm-workspace.yaml`；root `build`/`test` 通过 recursive workspace scripts 自动纳入该 package。

- [ ] **Step 4：验证 deterministic asset**

```powershell
pnpm --filter @unit-test-ide/report-ui build
pnpm --filter @unit-test-ide/report-ui test
pnpm build
git diff --exit-code -- packages/report-ui/dist
```

- [ ] **Step 5：提交 report-ui asset**

```powershell
git add package.json packages/report-ui
git commit -m "feat: build reusable coverage report ui"
```

### Task 4：Single-file HTML envelope、CSP 与 source snapshot

**文件：**

- 创建：`apps/test-service/internal/coveragereport/assets.go`
- 创建：`apps/test-service/internal/coveragereport/assets_test.go`
- 创建：`apps/test-service/internal/coveragereport/html.go`
- 创建：`apps/test-service/internal/coveragereport/html_test.go`
- 创建：`apps/test-service/internal/coveragereport/source.go`
- 创建：`apps/test-service/internal/coveragereport/source_test.go`
- 创建：`apps/test-service/internal/coveragereport/testdata/report.golden.html`
- 修改：`apps/test-service/internal/coveragereport/validate.go`
- 修改：`apps/test-service/internal/coveragereport/validate_test.go`

- [ ] **Step 1：写出 CSP/source/escaping/size 失败测试**

覆盖：

- `go:embed` asset digest 与 TS manifest 一致；
- CSP exact hash，禁止 default/network/frame/form/object/base/eval；
- JSON data `<`/`</script>`/Unicode line separator safe encoding；
- source boundary/digest/regular-file/size；
- stale source 不嵌入；
- binary/invalid UTF-8/oversized source 降级 metadata-only；
- assertion/test/source malicious HTML/URL 不执行；
- no native path/token/env/command；
- fixed doctype/meta/order/newline；
- repeated input byte-identical HTML。

- [ ] **Step 2：运行 HTML tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragereport -run 'HTML|CSP|Asset|Source' -count=1
```

- [ ] **Step 3：实现 self-contained HTML writer**

HTML data 与 source snapshot 通过 safe JSON encoder 嵌入 non-executable data element；固定 JS asset 是唯一 executable script，CSP 使用 manifest hash。无法嵌入单个 source 不使整个 report unavailable。

- [ ] **Step 4：运行 lint/structural/browser smoke**

```powershell
pnpm --filter @unit-test-ide/report-ui test
go test ./apps/test-service/internal/coveragereport -count=1
go test -race ./apps/test-service/internal/coveragereport -count=1
git diff --check
```

- [ ] **Step 5：提交 HTML renderer**

```powershell
git add apps/test-service/internal/coveragereport
git commit -m "feat: render offline coverage html"
```

### Task 5：CoverageReport service 与 all-or-nothing report set

**文件：**

- 创建：`apps/test-service/internal/coveragereport/service.go`
- 创建：`apps/test-service/internal/coveragereport/service_test.go`
- 创建：`apps/test-service/internal/coveragereport/fault_test.go`
- 修改：`apps/test-service/internal/artifactstore/coverage.go`
- 修改：`apps/test-service/internal/artifactstore/coverage_test.go`

- [ ] **Step 1：写出 JSON→JUnit→HTML stage/publish 失败测试**

逐 fault point 覆盖 open/write/close/fsync/digest/schema/XML/CSP/source/publish；失败时不返回 partial metadata，cleanup ownership 唯一；已发布 orphan 标记可回收；success 返回 immutable三 artifact refs 与 summary/provenance。

- [ ] **Step 2：运行 service tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/artifactstore -run 'Service|ReportSet|Fault|Coverage' -count=1
```

- [ ] **Step 3：实现 bounded report pipeline**

Service 先验证 canonical Coverage JSON，再生成 JUnit/HTML；不重新计算 coverage metric。所有 writer 使用 context cancellation 与 bytes limit。

- [ ] **Step 4：运行 report/artifact race**

```powershell
go test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/artifactstore -count=1
go test -race ./apps/test-service/internal/coveragereport ./apps/test-service/internal/artifactstore -count=1
```

- [ ] **Step 5：提交 report service**

```powershell
git add apps/test-service/internal/coveragereport apps/test-service/internal/artifactstore
git commit -m "feat: generate coverage report sets"
```

### Task 6：Terminal transaction、events 与 publisher ownership

**文件：**

- 创建：`apps/test-service/internal/coveragerun/events.go`
- 创建：`apps/test-service/internal/coveragerun/events_test.go`
- 创建：`apps/test-service/internal/taskstore/coverage_completion.go`
- 创建：`apps/test-service/internal/taskstore/coverage_completion_test.go`
- 修改：`apps/test-service/internal/task/manager_completion.go`
- 修改：`apps/test-service/internal/task/manager_artifacts.go`
- 修改：`apps/test-service/internal/task/manager_cause_test.go`
- 修改：`apps/test-service/internal/taskstore/events.go`
- 修改：`apps/test-service/internal/taskstore/test_runs.go`
- 修改：`apps/test-service/internal/taskstore/coverage_runs.go`
- 修改：`apps/test-service/internal/taskstore/coverage_reports.go`

- [ ] **Step 1：写出 terminal ordering/rollback/publish 失败测试**

覆盖：

- item results durable before report；
- report artifact published before metadata；
- TestRun/CoverageRun/Report/Task snapshot/final event 单 transaction；
- assertion failed TestRun + available CoverageRun + succeeded Task；
- partial semantics；
- unavailable infrastructure mapping；
- commit failure 不发布 finished event；
- publisher failure 不改 durable terminal、不产生 second owner；
- duplicate completion idempotent/conflict；
- late output bounded；
- cancellation 不发布 report.available。

- [ ] **Step 2：运行 completion tests 并确认失败**

```powershell
go test ./apps/test-service/internal/coveragerun ./apps/test-service/internal/taskstore ./apps/test-service/internal/task -run 'Coverage|Terminal|Publisher|Completion|Late' -count=1
```

- [ ] **Step 3：实现 durable-before-publish completion**

Task Manager 接受 runtime-only coverage finalizer；finalizer 只返回 domain events/artifact metadata，所有 store mutation 仍由 Task Manager owner 执行。

- [ ] **Step 4：运行 Phase 5E 完整门禁**

```powershell
pnpm --filter @unit-test-ide/report-ui test
pnpm check:coverage-generated
pnpm check:protocol-generated
go test ./apps/test-service/internal/coveragereport ./apps/test-service/internal/coveragerun ./apps/test-service/internal/artifactstore ./apps/test-service/internal/taskstore ./apps/test-service/internal/task ./apps/test-service/internal/testrun -count=1
go test -race ./apps/test-service/internal/coveragereport ./apps/test-service/internal/coveragerun ./apps/test-service/internal/artifactstore ./apps/test-service/internal/task -count=1
pnpm verify
git diff --check
```

- [ ] **Step 5：提交 terminal transaction**

```powershell
git add apps/test-service/internal/coveragerun apps/test-service/internal/taskstore apps/test-service/internal/task
git commit -m "feat: complete coverage reports atomically"
```

## Phase 5E 完成检查

- [ ] coverage artifact staging/validation
- [ ] deterministic JUnit XML
- [ ] reusable deterministic report-ui asset
- [ ] CSP-bound single-file HTML
- [ ] source digest/stale behavior
- [ ] all-or-nothing report set
- [ ] terminal transaction 与 publisher ownership
- [ ] 完整 Phase 5E 门禁与独立 XSS/artifact review
