# First-Report Coverage Detail Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 接通首份真实覆盖率报告的项目、文件、函数和非空行/分支读取，使离线生产生成入口使用正确实体 ID，并明确区分当前覆盖率与不可用的历史比较。

**Architecture:** 修正尚未正式发布的 v1.6 明细契约，nullable 比较字段贯穿 schema、Go/TS 模型、session、客户端和扩展。当前行/分支投影复用 SQLite 已验证索引，在同一只读事务中读取、验证及分页；不向原始观察图或数据库填入虚构基线。用真实 store→session 测试及正式 native IPC 验收补齐此前测试替身掩盖的边界。

**Tech Stack:** Go、SQLite/modernc、JSON Schema 2020-12、quicktype、TypeScript、Node 24、pnpm 11.4.0、Code-OSS 扩展、离线 LLVM/CMake/CppUTest/Unity。

## Global Constraints

- 依据已确认设计：`docs/superpowers/specs/2026-10-08-coverage-detail-first-report-contract-design.md`。
- 本次工作目录：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`；分支 `codex/phase10-production-generation`；设计提交 `2ff4ee14`，诊断代码基准 `b091e51bebf3d7dbc075186eda5ba65ce0836a53`。
- 覆盖率 artifact v1.0 以及 Protocol v1.0–v1.5 保持不变。无需新增协议版本或协商分支。
- `covered`、`total` 保持必填非负安全整数，并继续验证 `covered <= total`。
- `coveredDelta` 保持必填，但允许 `null`；三个指标共同遵守：没有可信比较来源三个值都为 `null`，有可信比较来源三个值都为有符号安全整数，不能混用数字和 `null`。
- nullable 字段必须出现在 JSON 中；Go nil 值序列化为 `null`，不能因 `omitempty` 而消失。TypeScript 对应必填 `number | null`。
- `line`、`count`、`branchesCovered`、`branchesTotal` 仍是必填的当前证据。`baselineCount`、`baselineBranchesCovered`、`baselineBranchesTotal` 保持必填，但允许 `null`；没有可信基线时三者全部为 `null`。
- 本次真实存储读取只输出不可比较状态，不填充不存在的基线 ID；即使有多份报告也不自动选“上一份”。
- 本次涉及的 file/function/gap/report/run ID：32 位小写十六进制；workspace generation、build profile identity、SHA-256：64 位小写十六进制。不泛化到 test item ID 或 target identity。
- 保持现有分页、HMAC cursor、报告/源摘要绑定和消息预算。不能通过扩大 wire 上限解决数据建模问题。
- 源变化、工具变化、损坏索引、报告不完整、信任撤销、未知字段和非法 cursor 的现有 fail-closed 规则继续保留。
- 生成候选仍必须与自己的真实执行基线比较，证明覆盖率提升且不回退；界面历史增量不可用不能绕过候选验证。
- Windows 普通测试的子进程清理失败、Windows 2022 的 LLVM archive listing 卡住，属于独立诊断项，不混入本设计，也不因本设计通过就宣称整套 foundation 通过。
- PR #39 暂不合并，不发布 GitHub Release，不启用签名，不修改 master。
- 沿用已有推送 GitHub/Gitee、更新 PR #39 和运行 hosted workflow 的授权；本地契约通过后才推送代码。计划/设计独立提交不推送以避免无用 CI。

---

## 文件职责与任务边界

| 任务 | 文件 | 职责 |
| --- | --- | --- |
| 1 | `tools/service-probe/src/native-test-generation.ts`、同名 `.test.ts` | 32 位实体 ID 与 64 位摘要分离，保留目标选择所有约束 |
| 2 | `packages/protocol-schema/schema/v1.6/coverage.schema.json`、`test/schema.test.mjs` | 必填 nullable 汇总比较；保留 aggregate metric 和旧协议 |
| 2 | `tools/protocol-gen/generate.mjs` 的 `rewriteCoverageV16`；`packages/protocol-models/src/generated/coverage-v1-6.ts`；`apps/test-service/internal/protocolmodel/v1_6/coverage/generated.go` | 从 schema 重生成并稳定命名；生成文件不手改 |
| 2 | `packages/protocol-models/src/coverage-v1-6-validation.ts`、`src/generated-contract.test.ts`；`apps/test-service/internal/protocol/coverage_v16.go`、新建 `coverage_v16_test.go` | 当前计数边界、nullable 增量和全 null/全数字一致性 |
| 2 | `apps/test-service/internal/session/coverage_detail_routes.go`、同名 `_test.go` | 无历史比较不再阻断完整当前报告；真实读取统一输出 null |
| 2 | 新建 `apps/test-service/internal/taskstore/coverage_detail_session_test.go`、`coverage_detail_export_test.go` | 外部测试包经仅测试编译的 fixture 桥复用真实 SQLite fixture，实际 session 序列化验证，不伪造历史来源 |
| 2 | `packages/test-client/src/decoders.ts`、`src/client.test.ts`；`apps/code-oss-extension/src/coverage-detail-tree.ts`、`test/coverage-detail-tree.test.ts` | nullable 解码和 UI 文案/筛选/零分母 |
| 3 | `apps/test-service/internal/coveragedetail/model.go` | 新的只读页面 DTO；原始 `Line`、`Index` 保持不变 |
| 3 | `apps/test-service/internal/taskstore/coverage_attested.go`；从 `coverage_details.go` 移出到新建 `coverage_detail_lines.go`；新建 `coverage_detail_lines_test.go` | 同一事务重放完整已验证图并投影分支；保留原分页 SQL 和 cursor |
| 3 | `apps/test-service/internal/taskstore/coverage_details_test.go` | 将共用 fixture 函数 ID 改为实际 canonical semantic key，更新页面期望 DTO |
| 3 | 任务 2 的 schema/模型/session/decoder 测试；`apps/code-oss-extension/test/coverage-decorations.test.ts` | nullable 历史行字段、非空行路由和真实当前装饰 |
| 4 | `tools/service-probe/src/native-test-generation.ts`、同名 `.test.ts`；`src/test-generation-e2e.test.ts` | 正式 IPC 的首次明细闭合检查；修正实体 ID fixture，不改变候选增量判定 |
| 4 | 新建 `apps/code-oss-extension/test/coverage-first-report-native.test.mjs` | 用正式 native 服务客户端驱动 tree/decorations；测试按明确命令运行，不加入普通单测隐性下载链 |
| 4 | 新建 `docs/superpowers/evidence/phase10/2026-10-08-first-report-contract.md` | 按 SHA 记录红绿、真实 native、race、hosted 结果和独立未闭合项 |

只移动 `ListCoverageLines` 及其新投影辅助函数，不重构整份 store/session 文件；不用新增 SQLite migration 或历史基线存储。

## 执行环境与公共命令

在上面的工作目录使用 PowerShell，一次执行这个环境块。不要从 Word 复制含不间断空格的命令。

```powershell
$ErrorActionPreference = 'Stop'
$NodeBin = 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin'
$GoBin = 'D:\Program Files\Go\bin'
$PnpmBin = 'C:\Users\DELL\AppData\Local\Temp\utide-pnpm-11.4.0'
$env:PATH = "$PnpmBin;$NodeBin;$GoBin;$env:PATH"
$env:CMAKE = 'C:\codex_project\unitTest\.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin\cmake.exe'
node --version
pnpm --version
go version
git status --short
```

期望 Node `v24.19.0`、pnpm `11.4.0`；不安装另一套依赖或切换 master。每条 native 命令执行后立即检查 `$LASTEXITCODE`：PowerShell 的 `Stop` 不会自动把原生命令非零退出转成异常。

各任务开始使用 test-driven-development，完成使用 verification-before-completion。只有实测退出 0 才写“通过”；未执行、不具备工具、取消和失败分别记录。

### Task 1: 修正生产目标实体 ID 契约

**Files:** 修改 `tools/service-probe/src/native-test-generation.ts` 的 `selectNativeGenerationTarget`；测试 `tools/service-probe/src/native-test-generation.test.ts`。

**Interfaces:**

- 保持 `selectNativeGenerationTarget(files: CoverageFilePageLike, functions: CoverageFunctionPageLike, relativePath: string): { fileId: string; functionId: string }`。
- `hex64` 仍供 `buildNativeGenerationWorkspaceConfig` 的 build profile 校验；新增私有 `hex32` 仅校验 file/function。

- [ ] **Step 1: 增加真实 ID 红测与反例。** 现有正例不再调用 64 位 `digest`；下面正例直接采用失败 artifact 的 ID。

```typescript
test("coverage target accepts real 32-character entity IDs", () => {
  const fileId = "8c41ec4cd1ae1c444276f6fed5c16bab";
  const functionId = "c9096d1efd8a87e00f4cfba79e459610";
  assert.deepEqual(selectNativeGenerationTarget(
    { items: [{ fileId, relativePath: "src/classifier.cpp", status: "current" }] },
    { items: [{ fileId, functionId, qualifiedName: "_Z8classifyi", status: "current" }] },
    "src/classifier.cpp",
  ), { fileId, functionId });
});
```

对 file/function 分别测试 31/33/64 位、非 hex、含大写、错误父 file；保留 stale、文件不精确、重复文件/函数、第二个函数、任一页面有 nextCursor、空 qualifiedName 的拒绝。build profile 的 32 位输入必须仍失败。

- [ ] **Step 2: 运行定向红测。**

```powershell
node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json --pretty false
node --test --test-name-pattern='coverage target' tools/service-probe/dist/native-test-generation.test.js
```

期望真实 32 位正例失败于 `native test-generation coverage target is unavailable`，而不是编译/环境错误。

- [ ] **Step 3: 最小修复。**

```typescript
const hex32 = /^[0-9a-f]{32}$/u;
// In selectNativeGenerationTarget, preserve every pre-existing selection guard.
if (file === undefined || fn === undefined ||
    !hex32.test(file.fileId) || !hex32.test(fn.functionId)) {
  throw new Error("native test-generation coverage target is unavailable");
}
```

- [ ] **Step 4: 重跑编译与整个选择器测试文件。**

```powershell
node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json --pretty false
node --test tools/service-probe/dist/native-test-generation.test.js
git diff --check
```

期望退出 0；不能新增 `32|64` 宽松表达式。提交前审查函数父子绑定及分页闭合仍存在。

- [ ] **Step 5: 本地提交。**

```powershell
git add tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts
git commit -m "fix: validate native generation coverage entity IDs"
```

### Task 2: 首份报告汇总的必填 nullable 比较贯通

**Files:** 文件职责表任务 2 的所有文件；同步因生成 Go pointer 类型而不能编译的既有 coverage metric 测试赋值，只修改 v1.6 明细赋值，不改 aggregate 或候选验证模型。

**Interfaces:**

- `CoverageMetricV16.coveredDelta: number | null`；Go `CoveredDelta *int64`、JSON tag `json:"coveredDelta"`，无 `omitempty`。
- `validateCoverageMetricV16(metric: CoverageMetricV16): boolean`、`validateCoverageSummaryV16(summary: CoverageSummaryV16): boolean` 和 Go 同名 PascalCase 函数保持公开签名。
- `detailSummary(s coveragedomain.Summary) coveragev16.CoverageSummaryV16` 仅投影当前 covered/total，三个 pointer 都 nil；更新本文件所有调用。
- `MetricLabel` 的 `percent: number | null`、`coveredDelta: number | null`；其他 tree context、分页、导航 API 保持不变。

- [ ] **Step 1: 写 schema 与 validator 红测。** 在 schema test 中用现有 `compileV16` 和 `$defs.detailMetric` 编译器测试：`coveredDelta:null` 有效、缺字段无效、字符串/小数/unsafe integer 无效、未知字段无效。Go 增加完整零值/null 序列化测试；TS 增加全 null、全数字、混合状态和 covered>total 测试。

```typescript
const metric = { covered: 2, total: 4, coveredDelta: null };
assert.equal(validateCoverageMetricV16(metric), true);
assert.equal(validateCoverageSummaryV16({
  functions: metric, lines: metric, branches: metric,
}), true);
assert.equal(validateCoverageSummaryV16({
  functions: metric, lines: { ...metric, coveredDelta: 0 }, branches: metric,
}), false);
```

```go
func TestCoverageDetailNullDeltaIsRequiredJSON(t *testing.T) {
    metric := coveragev16.CoverageMetricV16{Covered: 2, Total: 4}
    data, err := json.Marshal(metric)
    if err != nil { t.Fatal(err) }
    var wire map[string]json.RawMessage
    if err := json.Unmarshal(data, &wire); err != nil { t.Fatal(err) }
    if string(wire["coveredDelta"]) != "null" { t.Fatalf("wire=%s", data) }
    if !protocol.ValidCoverageMetricV16(metric) { t.Fatal("null rejected") }
}
```

- [ ] **Step 2: 运行 schema 红测，再修改 schema 并生成模型。** 第一条预期 nullable 被旧 schema 拒绝；生成后 Go/TS 类型会驱动消费端编译修改。

```powershell
node --test --test-name-pattern='protocol 1.6' packages/protocol-schema/test/schema.test.mjs
```

只改 `$defs.detailMetric.properties.coveredDelta`，保留 required 和 additionalProperties：

```json
"coveredDelta": { "anyOf": [{ "$ref": "#/$defs/signedCount" }, { "type": "null" }] }
```

```powershell
node tools/protocol-gen/generate.mjs
```

`rewriteCoverageV16` 当前以 quicktype 的 Purple/Fluffy 名称稳定导出名；如 nullable 导致自动名称改变，按 actual generated source 精确更新该映射和断言，不全局猜测替换。检查 Go nullable 字段的 required tag；如生成器带 `omitempty`，在此 template 的 Go 分支定点移除该 tag 并断言，不能手改 generated.go。两种输出仍是 `CoverageMetricV16` 与 `CoverageSummaryV16`。

- [ ] **Step 3: 更新 Go/TS validator 与 client decoder。** 可空值只有字面 null 有效；缺字段/undefined 仍失败。summary 的三个 comparison availability 必须相同。

```typescript
export function validateCoverageMetricV16(metric: CoverageMetricV16): boolean {
  return Number.isSafeInteger(metric.covered) && metric.covered >= 0 &&
    Number.isSafeInteger(metric.total) && metric.total >= 0 &&
    (metric.coveredDelta === null || Number.isSafeInteger(metric.coveredDelta)) &&
    metric.covered <= metric.total;
}
export function validateCoverageSummaryV16(summary: CoverageSummaryV16): boolean {
  const values = [summary.functions, summary.lines, summary.branches];
  return values.every(validateCoverageMetricV16) &&
    values.every((value) => (value.coveredDelta === null) === (values[0]!.coveredDelta === null));
}
```

```go
func ValidCoverageMetricV16(metric coveragev16.CoverageMetricV16) bool {
    return validNonnegativeSafeInteger(metric.Covered) &&
        validNonnegativeSafeInteger(metric.Total) && metric.Covered <= metric.Total &&
        (metric.CoveredDelta == nil || validSafeInteger(*metric.CoveredDelta))
}
func ValidCoverageSummaryV16(summary coveragev16.CoverageSummaryV16) bool {
    return ValidCoverageMetricV16(summary.Functions) &&
        ValidCoverageMetricV16(summary.Lines) && ValidCoverageMetricV16(summary.Branches) &&
        (summary.Functions.CoveredDelta == nil) == (summary.Lines.CoveredDelta == nil) &&
        (summary.Functions.CoveredDelta == nil) == (summary.Branches.CoveredDelta == nil)
}
```

`detailMetric` 用 `wire.coveredDelta === null ? null : safeInteger(...)`；`detailSummary` 返回前调用共享 `validateCoverageSummaryV16`，失败抛出 `detail comparison state is inconsistent`。通过 `ProtocolClient` 的真实 response validation/decode 路径测试所有三种 summary-bearing response；不只测 helper。

- [ ] **Step 4: 写并复现真实 SQLite→session 红测。** 新建 `taskstore/coverage_detail_session_test.go`，package `taskstore_test`。2026-10-08 用户确认调整：原定同包测试会形成 `taskstore → session → build → taskstore` 循环依赖；仅调整测试布局，不修改生产依赖或产品接口。

新建 `coverage_detail_export_test.go`，package `taskstore`，仅在测试编译时导出两个薄包装：`OpenCoverageDetailTestStore(t *testing.T) *Store` 调用现有 `openTestStore(t)`；`AttestedCoverageDetailTestFixture(t *testing.T, store *Store, number int) coveragedetail.Index` 调用现有 `attestedDetailFixture(t,store,number)`。两个包装都调用 `t.Helper()`；不得新增生产测试 hook、复制 fixture 生命周期或添加可变全局状态。外部测试通过 `taskstore.OpenCoverageDetailTestStore(t)` 和 `taskstore.AttestedCoverageDetailTestFixture(t,store,7401)` 复用真实 fixture。fixture 经真实 `CreateCoverageTask`/`Apply`/`PutCoverageDetail` 持久化，返回 canonical 32 位 IDs；不插入假的 baseline。Task 3 的内部 store 测试仍直接使用现有私有 fixture，仅扩展这个外部通信测试时使用测试桥。

测试桥只替代与读路由无关的 readiness：

```go
type firstReportDetailReader struct{ *taskstore.Store }
func (b firstReportDetailReader) CoverageDetailsReady() bool { return b.CoverageDetailReady() }
type firstReportCoverageReader struct {
    session.CoverageBackend
    store *taskstore.Store
}
func (b firstReportCoverageReader) GetCoverageReport(ctx context.Context, id string) (coveragedomain.Report, error) {
    return b.store.GetCoverageReport(ctx, id)
}
func (b firstReportCoverageReader) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) {
    return b.store.GetCoverageRun(ctx, id)
}
type firstReportReadiness struct {
    session.ManagedGenerationBackend
    session.ManagedStartResolverBackend
    session.ManagedStartBackend
    session.ManagedRunReadBackend
}
func (firstReportReadiness) TestGenerationReady() bool { return true }
func (firstReportReadiness) ManagedTestsReady() bool { return true }
```

用 `session.NewWithManagedDetails(token, "linux", "test-pipe", nil, firstReportCoverageReader{store:store}, readiness, firstReportDetailReader{store}, readiness)` 建立 session。只允许 handshake 与 coverage detail 读方法；这些测试不调用 readiness 内嵌接口的方法，不把这个桥当作生产生成成功证据。使用 `net.Pipe`、`server.ServeConnection` 和 `json.Encoder/Decoder` 发送真正 wire 请求，连接设置 5 秒 deadline，cleanup 关闭两端；请求使用 `protocol.Version16`、32 位 messageId、固定有效 UTC sentAt 和 `json.Marshal(payload)`。

首先 handshake token `0123456789abcdef`、clientName `first-report-test`、clientVersion `0.1.0`、supportedProtocolVersions `["1.6"]`。然后调用 project/get、files/list、functions/list：必须成功且 summary 当前值与 index 一致、三个 coveredDelta 属性都存在且为 null。第一轮旧路由预期返回 SERVICE_UNHEALTHY，证明测试达到了故障边界。

再写入第二份 `taskstore.AttestedCoverageDetailTestFixture(t,store,7402)` 并读取，仍必须全 null；不能由报告数量推导基线。保留 noBaseline 单测，但将断言改为上述成功语义；另加一个仅带字符串 BaselineReportID 的替身也输出 null，证明内存字符串不能成为来源。

- [ ] **Step 5: 解耦 session 汇总读取。**

```go
func detailSummary(s coveragedomain.Summary) coveragev16.CoverageSummaryV16 {
    return coveragev16.CoverageSummaryV16{
        Functions: coveragev16.CoverageMetricV16{Covered:s.Functions.Covered, Total:s.Functions.Total},
        Lines: coveragev16.CoverageMetricV16{Covered:s.Lines.Covered, Total:s.Lines.Total},
        Branches: coveragev16.CoverageMetricV16{Covered:s.Branches.Covered, Total:s.Branches.Total},
    }
}
```

`detailSummaryProject` 移除仅依赖 BaselineReportID 的门禁；保留 backend error、stale/current、reasons、summary 数值验证和 report/run/project/generation 绑定。`validDetailProjection` 不以 null 绕过原有 current count 校验；store 的 canonical delta JSON 检查仍保留。无 history 不添加 `baseline_unavailable` reason 到 current report（客户端 current 要求空 reasons）。所有真实页面投影统一调用当前 summary mapper，不根据 fake baseline 字符串输出数字。

- [ ] **Step 6: UI 红测与实现。** 在 tree 测试构造 current、全 null summary，refresh→children(project)→children(file) 必须可展开。total=0 的分支显示“不适用”；coveredDelta=null 显示“不可比较”，不出现 `+0`、`null%`、`NaN` 或 `Infinity`。先完整加载 functions 再筛 regressed，未知 delta 不当回退；现有未加载子页面保留 file 可导航行为，不误标记下降。

```typescript
const percent = value.total === 0 ? null : Math.round(100 * value.covered / value.total);
const comparison = value.coveredDelta === null ? "不可比较" :
  `${value.coveredDelta >= 0 ? "+" : ""}${value.coveredDelta}`;
const ratio = percent === null ? "不适用" : `${percent}%`;
// label fragment: `${key} ${value.covered}/${value.total} (${ratio}, ${comparison})`
// regressed predicate: item.coveredDelta !== null && item.coveredDelta < 0
```

- [ ] **Step 7: 定向绿测与类型一致性。**

```powershell
node tools/protocol-gen/generate.mjs --check
node --test packages/protocol-schema/test/schema.test.mjs
node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json apps/code-oss-extension/tsconfig.json --pretty false
node --test packages/protocol-models/dist/generated-contract.test.js packages/test-client/dist/client.test.js apps/code-oss-extension/dist/test/coverage-detail-tree.test.js
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session
go test ./apps/test-service/internal/taskstore -run 'TestCoverageDetailFirstReport|TestCoverageDetailSecondReport' -count=1
git diff --check
```

对新测试使用名称 `TestCoverageDetailFirstReportThroughSession`、`TestCoverageDetailSecondReportHasNoInferredBaseline`。期望全退出 0，旧协议 fixtures 通过，生成器 check 无漂移。当前行页面尚未修复，不能称完整链路已通过。

- [ ] **Step 8: 本地提交任务 2 范围。**

```powershell
git add packages/protocol-schema/schema/v1.6/coverage.schema.json packages/protocol-schema/test/schema.test.mjs tools/protocol-gen/generate.mjs packages/protocol-models/src/generated/coverage-v1-6.ts apps/test-service/internal/protocolmodel/v1_6/coverage/generated.go packages/protocol-models/src/coverage-v1-6-validation.ts packages/protocol-models/src/generated-contract.test.ts apps/test-service/internal/protocol/coverage_v16.go apps/test-service/internal/protocol/coverage_v16_test.go apps/test-service/internal/session/coverage_detail_routes.go apps/test-service/internal/session/coverage_detail_routes_test.go apps/test-service/internal/taskstore/coverage_detail_session_test.go packages/test-client/src/decoders.ts packages/test-client/src/client.test.ts apps/code-oss-extension/src/coverage-detail-tree.ts apps/code-oss-extension/test/coverage-detail-tree.test.ts
git diff --cached --check
git commit -m "fix: expose first-report coverage with unavailable comparison"
```

若其他既有 v1.6 测试需要 pointer literal，同一提交显式加入这些已核对文件，不使用 `git add -A`。
同一提交显式加入仅测试桥 `apps/test-service/internal/taskstore/coverage_detail_export_test.go` 及本计划已确认的布局调整，保留决策记录。

### Task 3: 从同一已验证快照投影真实当前行/分支

**Files:** 文件职责表任务 3 的文件；Task 2 的真实 store→session 测试扩展为非空 file/function line 请求。

**Interfaces:**

- 新增 `coveragedetail.LineDetail struct { Line, Count, BranchesCovered, BranchesTotal int64 }`；`LinePage.Items []LineDetail`。
- 原始 `coveragedetail.Line`、`File.Lines`、`Function.Lines`、`Index` 与持久化格式不变。
- 新增私有 `readValidatedCoverageIndexSnapshot(ctx context.Context, tx *sql.Tx, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index,error)`；从 `ReadValidatedCoverageIndex` 提取原有完整验证主体，helper 不 Begin/Commit/Rollback。
- 新增私有 `projectCurrentLines(index coveragedetail.Index, fileID, functionID string) (map[int64]coveragedetail.LineDetail,error)`；仅接受已验证 current 图。
- 公开 `Store.ListCoverageLines(ctx context.Context,q coveragedetail.LineQuery) (coveragedetail.LinePage,error)` 签名保持不变。
- wire 的三个历史字段 Go `*int64` 无 omitempty，TS 必填 `number|null`。

- [ ] **Step 1: 写真实 store 行/分支红测。** `coverage_detail_lines_test.go` 使用 `attestedDetailFixture`，其 lines 1–8 命中 1、9–10 命中 0；line 1 四条臂覆盖 3，其他行已验证无分支。

```go
func TestCoverageDetailLinesProjectRealBranchArms(t *testing.T) {
    s := openTestStore(t)
    index := attestedDetailFixture(t, s, 7501)
    q := coveragedetail.LineQuery{ReportID:index.ReportID,
        WorkspaceGeneration:index.WorkspaceGeneration, FileID:index.Files[0].ID, Limit:2}
    page, err := s.ListCoverageLines(context.Background(), q)
    if err != nil { t.Fatal(err) }
    want := coveragedetail.LineDetail{Line:1, Count:1, BranchesCovered:3, BranchesTotal:4}
    if len(page.Items)!=2 || page.Items[0]!=want || page.NextCursor=="" {
        t.Fatalf("page=%#v",page)
    }
}
```

先以现有 DTO 可编译的当前行为断言“应存在分支字段”或完整 wire JSON 来取得红测；引入 DTO 后再用以上类型化断言。新增 file/function 分页遍历、asc/desc、covered/uncovered、limit 1/1000、错误 parent 和跨 filter/parent/report cursor。累计 line covered/total 和 branch covered/total 必须等于对应 file/function summary。为空的第二个文件返回空 slice，不输出 null items。

- [ ] **Step 2: 为 current graph 损坏写拒绝测试。** 在真实 SQLite 上分别 DELETE 一条 branch、改 branch count 导致 summary 不一致、将 function_id 改到另一文件、改 source manifest/toolchain、让 branch owner 重复或 branch 无可归属 line。调用公开 ListCoverageLines 必须非 nil error，不能降为零臂。保持原 attestation oversized-row 和全图数量 bound 测试。

```go
if _, err := s.db.Exec(`DELETE FROM coverage_detail_branches WHERE report_id=? AND ordinal=3`, index.ReportID); err != nil {
    t.Fatal(err)
}
if _, err := s.ListCoverageLines(context.Background(), q); !errors.Is(err, task.ErrStorageUnavailable) {
    t.Fatalf("missing branch was zero-filled: %v", err)
}
```

- [ ] **Step 3: 提取同事务验证 helper 与页面 DTO。** 将 `ReadValidatedCoverageIndex` 的 report/run/testRun/header/manifest/rows/graph 检查移入上述 helper；公开 wrapper 保留原参数、readiness、BeginReadOnly、defer Rollback、Commit。`ListCoverageLines` 继续用 `detailRead` 开启一个事务，使用 header 的 project/workspace 构造 CurrentIndexQuery，并在同一 tx 调 helper；不从 ListCoverageLines 调会新开事务的公开 ReadValidatedCoverageIndex。

```go
type LineDetail struct {
    Line, Count, BranchesCovered, BranchesTotal int64
}
type LinePage struct {
    Items []LineDetail
    NextCursor string
}
// Inside ListCoverageLines, after detailRead and before scanning the page:
index, err := readValidatedCoverageIndexSnapshot(ctx, tx, coveragedetail.CurrentIndexQuery{
    ProjectID:h.project, ReportID:q.ReportID, WorkspaceGeneration:q.WorkspaceGeneration,
})
if err != nil { return coveragedetail.LinePage{}, err }
projected, err := projectCurrentLines(index, q.FileID, q.FunctionID)
if err != nil { return coveragedetail.LinePage{}, err }
```

`coverage_details_test.go` 共用 fixture 的 function semantic key 改为 `fmt.Sprintf("linkage:%d:%s:signature:",len(linkage),linkage)`，避免旧 fixture 的非 canonical ID 被新增真实验证正确拒绝。不要为了保留坏 fixture 放宽 production validator。

- [ ] **Step 4: 实现 current 分支投影并保留分页。** `projectCurrentLines` 查唯一 file 和可选其子 function；当前 lines 建立 map。函数页面仅用该函数 Branches；文件页面按 `coveragedetail/index.go` 已有 `(line,column,ordinal)` key 汇合，拒绝不同函数的归属冲突，同一规范臂不重复计数。count>0 才 covered，不能从 line.Count 推断。

```go
type currentBranchKey struct { line, column, ordinal int64 }
// validated arms is map[currentBranchKey]int64 after ownership/dedup checks.
for key, count := range arms {
    line, ok := projected[key.line]
    if !ok { return nil, storageError("branch has no attributed line", nil) }
    line.BranchesTotal++
    if count > 0 { line.BranchesCovered++ }
    projected[key.line] = line
}
```

完整投影校验 lines/branches covered,total 与选中父 summary 一致，然后执行保留的 line,count SQL keyset/filter/limit+1。每个 scan 原始 Line 在 projected map 中必须存在且 count 相同；append 对应 LineDetail。`detailScope`、cursor key/sort/filter/parent/limit 保持原值，返回页数量预算不变。验证与扫描必须共享 tx；所有失败都不 commit。

- [ ] **Step 5: nullable 历史行 schema、模型与 decoder 红绿。** schema 的三个 baseline count 字段使用 `anyOf:[safeCount,null]`，所有 required 当前/历史字段保留；重新运行生成器并确保 pointer tag。decoder 当前 line>=1、count>=0、0<=branchesCovered<=branchesTotal，历史三值只有全部 null 或全部非负安全整数可接受；数字历史还验证 covered<=total。

```typescript
const baselineCount = line.baselineCount === null ? null : safeInteger(line.baselineCount, "baseline line count");
const baselineBranchesCovered = line.baselineBranchesCovered === null ? null : safeInteger(line.baselineBranchesCovered, "baseline branches covered");
const baselineBranchesTotal = line.baselineBranchesTotal === null ? null : safeInteger(line.baselineBranchesTotal, "baseline branches total");
if ((baselineCount === null) !== (baselineBranchesCovered === null) ||
    (baselineCount === null) !== (baselineBranchesTotal === null)) {
  throw new Error("line comparison state is inconsistent");
}
```

测试 null 正例、数字全零有效、缺任一字段、混合 null/数字、负数、unsafe integer、未知字段、重复行拒绝。当前不存在历史来源时 session 必须输出 null，不能用 decoder 对零值来源作猜测。

- [ ] **Step 6: 接通非空行 session 并写装饰回归。** 删除“所有非空页拒绝”占位行为，逐项校验 LineDetail 当前计数后序列化；所有历史 pointer nil，保持 report/generation/parent/cursor 绑定。lines 路由和 summary 路由同样保留有效 current project 门禁，损坏/incomplete 图不能作为精确行页暴露。

```go
out.Items = append(out.Items, coveragev16.CoverageLineDetailV16{
    Line:line.Line, Count:line.Count,
    BranchesCovered:line.BranchesCovered, BranchesTotal:line.BranchesTotal,
    BaselineCount:nil, BaselineBranchesCovered:nil, BaselineBranchesTotal:nil,
})
```

修改既有“line evidence silently zero-filled”反例为真正不一致 LineDetail，例如 covered>total；增加非空有效页成功及 JSON null 必填断言。扩展真实 store→session test 同时请求 fileId 与 functionId 的独立页面，验证 line1 的 3/4 和历史 null。`lineDecorations` 新增上述 null 历史输入仍用当前 3/4、命中 1 标 covered；line9 命中 0 标 uncovered，换 report/source/generation 后清空。若 decorations 本身无历史依赖，不做无用源码改动。

- [ ] **Step 7: 运行任务 3 定向绿测。**

```powershell
node tools/protocol-gen/generate.mjs --check
node --test packages/protocol-schema/test/schema.test.mjs
go test ./apps/test-service/internal/taskstore -run 'TestCoverageDetail|TestValidatedCoverageIndex' -count=1
go test ./apps/test-service/internal/session ./apps/test-service/internal/protocol ./apps/test-service/internal/runtime -run 'Test.*CoverageDetail|TestDetailSourceBinding|TestCoverageDetailNull' -count=1
node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json apps/code-oss-extension/tsconfig.json --pretty false
node --test packages/protocol-models/dist/generated-contract.test.js packages/test-client/dist/client.test.js apps/code-oss-extension/dist/test/coverage-detail-tree.test.js apps/code-oss-extension/dist/test/coverage-decorations.test.js
git diff --check
```

期望所有退出 0，真实非空页不再 SERVICE_UNHEALTHY；损坏图仍拒绝。检查 moved ListCoverageLines 只有一份定义，所有 `LinePage{Items:...}` 消费点改为新页面 DTO，原始索引 line 不改。

- [ ] **Step 8: 本地提交任务 3 变更。**

```powershell
git add apps/test-service/internal/coveragedetail/model.go apps/test-service/internal/taskstore/coverage_attested.go apps/test-service/internal/taskstore/coverage_details.go apps/test-service/internal/taskstore/coverage_detail_lines.go apps/test-service/internal/taskstore/coverage_detail_lines_test.go apps/test-service/internal/taskstore/coverage_details_test.go apps/test-service/internal/taskstore/coverage_detail_session_test.go apps/test-service/internal/session/coverage_detail_routes.go apps/test-service/internal/session/coverage_detail_routes_test.go packages/protocol-schema/schema/v1.6/coverage.schema.json packages/protocol-schema/test/schema.test.mjs packages/protocol-models/src/generated/coverage-v1-6.ts packages/protocol-models/src/generated-contract.test.ts apps/test-service/internal/protocolmodel/v1_6/coverage/generated.go packages/test-client/src/decoders.ts packages/test-client/src/client.test.ts apps/code-oss-extension/test/coverage-decorations.test.ts
git diff --cached --check
git commit -m "fix: project current coverage lines and branch evidence"
```

### Task 4: 正式 IPC→客户端→UI→离线生成验收收口

2026-10-08 续接：任务 1–3 已实现并通过对应审查；任务 4 的原生 Windows 验收暴露闭合 CMake 兼容缺口，现有未提交 helper/UI 工作保留。用户已确认[新增设计](../specs/2026-10-08-windows-cmake-coverage-compatibility-design.md)，先执行[新增计划](2026-10-08-windows-cmake-coverage-compatibility.md) W1–W3，再恢复本任务；不要重做任务 1–3。W3 将提交两个重叠 TS 文件的累计改动，本任务提交按届时剩余路径精确暂存。

**Files:** 文件职责表任务 4 的文件。已有 `tools/service-probe/src/test-generation-e2e.ts` 的 `coverageImproved` 用 baseline/final 当前 covered,total 比较；不要用 null UI delta 代替它。

**Interfaces:**

- 新增导出 `readNativeCoverageLineEvidence(client: Pick<ProtocolClient,"listCoverageLines">, binding: NativeCoverageRefreshBinding): Promise<{ file: readonly CoverageLineDetailV16[]; function: readonly CoverageLineDetailV16[] }>`，使用现有 ProtocolClient 类型和 report/generation/file/function binding。
- 新增导出 `withNativeFirstCoverageReport<T>(root:string, platform:NativePlatform, framework:Framework, inspect:(client:ProtocolClient,binding:NativeCoverageRefreshBinding)=>Promise<T>): Promise<T>`，把现有 runFramework 的 workspace/正式服务/首份 runCoverageCycle setup 和 dispose 抽成此 scoped helper。helper 自身只 setup、调用 inspect、返回其结果并 dispose，不创建第二份基线报告，不分析/接受候选；实际生成 flow 的 callback 复用同一个 setup 生命周期继续 executeNativeManagedGeneration，返回原 receipt。
- 新 UI native 测试沿用既有 native `.mjs` 方式，直接运行 `apps/code-oss-extension/test/coverage-first-report-native.test.mjs`，从明确的相对 dist 路径导入已编译 helper 与 UI。无需修改 tsconfig、扩展运行时依赖或普通 test script。

- [ ] **Step 1: 先补定向验收红测，不开 hosted。** 修正 `test-generation-e2e.test.ts` 中 report/run/file/function 的 64 位 fixture 为 `value.repeat(32).slice(0,32)`，workspace/review/source digest 仍64。增加 history 全 null 的首次和刷新 summary 输入仍根据当前值证明改善；不覆盖已有无增量/回退拒绝反例。对 `readNativeCoverageLineEvidence` 增加 fake 协议边界负例：binding 漂移、重复行、空页伪装成功、循环 cursor、页超额、baseline 字段混合。

```typescript
const entityId = (value: string) => value.repeat(32).slice(0, 32);
const summary = {
  functions: { covered: 1, total: 2, coveredDelta: null },
  lines: { covered: 2, total: 4, coveredDelta: null },
  branches: { covered: 1, total: 3, coveredDelta: null },
};
const refreshedSummary = {
  functions: { covered: 2, total: 2, coveredDelta: null },
  lines: { covered: 3, total: 4, coveredDelta: null },
  branches: { covered: 2, total: 3, coveredDelta: null },
};
```

这些是现有测试文件顶部的真实替换，所有 current covered/total 保留原值，只将 history delta 改为 null。当前 fixture 的改善由 1/3→2/3 等当前值证明，不将它误称 native classifier 2/4→4/4。以下定向 helper 红测使用新导出的函数，不连接真实服务：

```typescript
test("first-report line evidence keeps real hits with null history", async () => {
  const binding = { workspaceGeneration: digest("a"), coverageReportId: entityId("b"),
    fileId: entityId("c"), functionId: entityId("d") };
  const item = { line:3, count:1, branchesCovered:1, branchesTotal:2,
    baselineCount:null, baselineBranchesCovered:null, baselineBranchesTotal:null };
  const client = { async listCoverageLines() {
    return { coverageReportId:binding.coverageReportId,
      workspaceGeneration:binding.workspaceGeneration, items:[item] };
  } };
  const result = await readNativeCoverageLineEvidence(client, binding);
  assert.deepEqual(result.file, [item]);
  assert.deepEqual(result.function, [item]);
  await assert.rejects(readNativeCoverageLineEvidence({ async listCoverageLines() {
    return { coverageReportId:entityId("e"),
      workspaceGeneration:binding.workspaceGeneration, items:[item] };
  } }, binding), /identity|binding/u);
});
```

- [ ] **Step 2: 实现有界真实明细闭合读取。** `readNativeCoverageLineEvidence` 分别用 fileId 与 functionId 走正式 listCoverageLines；固定每页200，不超过已有1000能力值。保持 cursor seen Set、report/generation 校验，累积上限使用已有 attested point bound 2,000,000，不提高服务 wire 预算；两种页均要求非空、line 唯一、当前值安全且三个历史字段全 null。helper 返回所有当前 line DTO；在已有 project/file/function summary 的 `runCoverageCycle` 内累计 covered/total 与各自父 summary 验证一致（初始 classifier 单函数 fixture 正好 file=function），不能把通用多函数文件断言为永远相等。

`runCoverageCycle` 选好真实 target 后调用此 helper，正式 native 首次覆盖即读明细；WORKSPACE_CHANGED 仍只有原有一次有界重试，其他契约错误不重试。保留服务数据/失败 artifact，不能直接从 SQLite 读取来替代 IPC。

- [ ] **Step 3: 实现真实客户端驱动 UI 测试。** 新测试调用上述 scoped helper，使用真实 client 创建 CoverageDetailTree，绑定首次报告，验证 refresh、project→file→function 可展开、计数 5/5 和分支2/4、null 增量显示“不可比较”。调用真实 listCoverageLines 的 function 页面，把响应传给 `lineDecorations`，确认行4与5显示1/2 branches；无伪造 baseline、无+0。用 runtime source/trust 单测验证失效；不要在 native 生成生命周期中修改源文件导致候选验收被另一个 UI 负例破坏。

```javascript
import { resolve } from "node:path";
import assert from "node:assert/strict";
import test from "node:test";
import { withNativeFirstCoverageReport, readNativeCoverageLineEvidence } from "../../../tools/service-probe/dist/native-test-generation.js";
import { CoverageDetailTree } from "../dist/src/coverage-detail-tree.js";
import { lineDecorations } from "../dist/src/coverage-decorations.js";

const repositoryRoot = resolve(import.meta.dirname, "../../..");
const platform = process.platform;
assert.ok(platform === "win32" || platform === "linux");
test("first native report drives coverage tree and decorations", async () => {
  await withNativeFirstCoverageReport(repositoryRoot, platform, "cpputest", async (client, binding) => {
  const tree = new CoverageDetailTree(() => ({
    client, workspaceGeneration: binding.workspaceGeneration,
    projectId: "classifier", reportId: binding.coverageReportId,
  }));
  assert.equal(await tree.refresh(), true);
  const roots = await tree.children();
  assert.match(roots[0].label, /不可比较/u);
  const files = await tree.children(roots[0]);
  const functions = await tree.children(files.find((file) => file.id === binding.fileId));
  assert.equal(functions.some((fn) => fn.id === binding.functionId), true);
  const evidence = await readNativeCoverageLineEvidence(client, binding);
  assert.equal(lineDecorations(evidence.function, "current", 200).length, 5);
  });
});
```

新测试在 Linux 明确选 `linux`；不可用 bundle 应报告环境阻塞，不静默 skip 或写 PASS。普通单测不执行这个 native 文件；显式验收命令执行它。

- [ ] **Step 4: 定向本地验证，确认完整 native 工具可用后执行一次闭环。**

```powershell
node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json apps/code-oss-extension/tsconfig.json --pretty false
node --test tools/service-probe/dist/native-test-generation.test.js tools/service-probe/dist/test-generation-e2e.test.js
pnpm --filter @unit-test-ide/service-probe test:e2e
node --test apps/code-oss-extension/test/coverage-first-report-native.test.mjs
$env:GITHUB_SHA = (git rev-parse HEAD).Trim()
$env:UT_DEBUG_PROCESS_HOST_FAILURES = '1'
pnpm --filter @unit-test-ide/service-probe test:e2e:native:test-generation -- --platform win32
```

正式生成仍要求 compile/test/coverage、用户确认的 managed review apply、维护文件/record 身份、接受后刷新真实覆盖率提升且不回退。预期 CppUTest 和 Unity 两者完成；如 compiler/framework prerequisite 缺失，先记录准确环境阻塞，不能把 WSL 无工具说成 Linux 通过。

不要把提交前 native 产物的 GITHUB_SHA 当成尚未提交工作区的代码身份；记录为本地 dirty working-tree 验证及 diff摘要，本任务 Step 5 所述最终 clean SHA 验证才是候选证据。失败时保存 stdout/stderr、哪个阶段和 store，继续该具体根因诊断，不重跑不相关 full matrix。

- [ ] **Step 5: 一次必要回归、review、提交和候选 SHA 验证。** 对 store 同快照改动执行定向 race：

```powershell
go test -race -timeout=20m ./apps/test-service/internal/taskstore -run 'TestCoverageDetail|TestValidatedCoverageIndex' -count=1
go test ./apps/test-service/internal/runtime ./apps/test-service/internal/session ./apps/test-service/internal/protocol
node tools/protocol-gen/generate.mjs --check
git diff --check
```

若跨层改动 review 判定需要 full verify，在所有定向边界通过后最多执行一次 `pnpm verify`，记录整条退出码。Windows processcontrol 原错误出现时保存实际 unwrap 后原因，不把20次本地通过当成 CI 根因已修；Windows2022 tar 停滞单独跟踪，不通过放宽 archive 检查关闭。

按 requesting-code-review 对本任务与前面跨层改动审查：必填 null、all-or-none、实体ID、完整图和snapshot、cursor/预算、UI语义、旧协议、候选执行 gate。若用户选子代理执行，遵循其 spec/code 双审；inline 模式不自行派子代理。问题关闭后精确 stage任务4文件，写 evidence 的验证时间、命令、退出码、当前diff和未闭合项，再提交：

```powershell
git add tools/service-probe/src/native-test-generation.ts tools/service-probe/src/native-test-generation.test.ts tools/service-probe/src/test-generation-e2e.test.ts apps/code-oss-extension/test/coverage-first-report-native.test.mjs docs/superpowers/evidence/phase10/2026-10-08-first-report-contract.md
git diff --cached --check
git commit -m "test: close first-report native generation and coverage UI contract"
git status --short
$env:GITHUB_SHA = (git rev-parse HEAD).Trim()
pnpm --filter @unit-test-ide/service-probe test:e2e:native:test-generation -- --platform win32
```

这是最终 clean 候选 SHA 的一次 native 验证，不重跑所有未受影响测试。验收成功记录绑定此 SHA，不在产物写入后假称该文件已经包含在相同提交中；证据文档可后续单独提交注明被验证 SHA。

- [ ] **Step 6: 依据已有授权推送并只消费新 workflow。** 本地关键契约和能执行的平台 native gate 成功后，先核对 fast-forward关系，再普通推送当前功能分支到两个 remote，更新已有 PR #39，不新建重复 PR：

```powershell
git push github HEAD:refs/heads/codex/phase10-production-generation
git push origin HEAD:refs/heads/codex/phase10-production-generation
gh pr view 39 --repo colayc/unitTest --json state,headRefName,headRefOid,url
gh run list --repo colayc/unitTest --branch codex/phase10-production-generation --limit 8 --json databaseId,headSha,status,conclusion,workflowName
```

只跟踪 headSha 等于推送候选的新运行；push已经触发时不重复dispatch。如果需要人工dispatch，先读现有workflow inputs按实际 schema触发。Linux verify 至少必须证明新的正式首次读取与生成不再卡原边界；Windows 普通清理失败和Windows2022 archive卡住分别按独立 issue记录。只读检查没变不宣告blocked；不为“全绿”删除安全gate或skipnative。成功与失败都汇报准确job/run链接；无merge/Release/sign/master操作。

## 自审覆盖与完成判定

| 已确认设计要求 | 对应任务/证据 |
| --- | --- |
| 32位实体与64位摘要分离 | Task 1 正反例；Task 4 fixture 修正和正式IPC |
| 首份/多份但无来源 summary 全null且current | Task 2 real SQLite→session、client、tree |
| 必填 nullable、三指标一致、旧协议不变 | Task 2 schema/generated/Go+TS validators/client；Task 3 baseline triplet |
| 同快照真实行/分支、完整性和归属 | Task 3 attested helper、SQL corruption、file/function分页聚合 |
| cursor/父子/report/source/tool/trust仍闭合 | Task 1、Task 3负例及runtime回归；Task 4实际IPC |
| UI不可比较、零分母不适用、当前装饰 | Task 2 tree单测；Task 3 decorations；Task 4真实client驱动 |
| 候选真实compile/test/改善/no-regression/维护/确认 | Task 4既有生产执行闭环及无增量反例 |
| 本地与hosted证据、SHA、Windows独立阻塞 | Task 4 evidence及新headSha工作流 |

实施完成必须四个任务均有实际验证结果；不能只打勾而没有命令退出码。未执行的Linux本地native、独立Windows问题和历史比较后续功能必须明确保留，不能宣称Phase10整体完成。最后报告真实报告可读性、生成闭环结果、候选SHA和剩余阻塞；PR#39仍不合并。
