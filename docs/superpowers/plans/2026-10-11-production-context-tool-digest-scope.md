# Production Context Tool Digest Scope Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在一次 Windows `ResolveProductionContext` 调用的五个发现位置复用受保护的工具摘要，同时保留原有发现、源码、报告、权限及目标校验。

**Architecture:** `toolhash.Scope` 增加只读 context 绑定与活动状态检查。生产解析新建并拥有一个 Scope，通过私有选择字段标记允许同步的内部索引读取借用；借用结束不关闭父 Scope。无标记索引查询依然新建并关闭自己的 Scope，非 Windows 不创建保护作用域。

**Tech Stack:** Go、现有 Windows 文件保护与 toolhash 实现、runtime 元数据测试夹具、PowerShell 7、隔离的一版源码测试副本。无新依赖。

## Global Constraints

每次解析都新建作用域，即使输入 context 曾用于另一次解析，也不能继承上一解析的摘要。

成功路径仍有五次完整工作区发现。

整个单次解析共用一个最多 128 个工具文件的容量池，内部索引不得额外再拥有 128 个。

每工具最多 512 MiB 的复用资格、已有更严格的消费者限制及现有拼写数量限制均保留。

只读绑定校验不得新增任意提取或关闭其他 context 作用域的通用接口，也不改变现有 toolhash.Begin 的“每次调用新建”语义。

没有私有解析标记时，即使 context 带有其他 toolhash 状态，也不能任意借用；独立索引仍按原规则新建作用域。

字段不匹配、绑定不匹配、已关闭或已失败的父作用域均拒绝借用，不能创建新作用域掩盖失效。

索引借用视图的 Close 只结束该视图，不调用父 Scope.Close；其结束时的失败检查必须能阻止索引成功返回。重复结束是幂等的。

原参数、过期、存储、取消及超时错误优先保留，不新增公开错误协议。

原样执行 ListTargets，保留其内部发现、CMake File API、配置指纹、当前工具链、声明和选定 SDK 相关校验。

不修改公开协议、UI、生成用例维护、发布器、覆盖率指标、Windows 选定 SDK 库策略、任务阶段去重或任何操作预算。

不恢复暂停的 V2 taskstore、静态测试管理、向导、MC/DC、数据上传下载和报告扩展。

未来实施必须先有行为 RED，再以同一明确选择器获得 GREEN；编译失败、缺失方法或测试未运行不能代替行为 RED。

成功标准是安全边界和重复哈希次数得到定点证明，而不是某个固定的耗时阈值。

---

## 批准范围和工作区

人已于 2026 年 10 月 11 日确认 [设计](../specs/2026-10-11-production-context-tool-digest-scope-design.md)。本轮仅保存实施计划及文档本地提交。任务执行仍需选择执行方式；本计划不授权 native、真实工具发现、服务构建、SQL 查询、Linux 执行、远程写入、workflow、合并、Release 或签名。

使用现有 linked worktree `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`，分支 `codex/phase10-production-generation`。设计提交为 `b9a24a6b`，产品基线为 `ea1c9d18`。不得切换到 `C:/codex_project/unitTest`、重新建 worktree、重置、清理或 stash 原有 19 项工作区状态。

原保护控制器和 `prepared.json` 位于 `.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/`。原 `Run-ABITest` 选择器仅授权旧任务，禁止扩大或调用它运行本任务。执行时单独创建本任务控制器、状态快照及隔离副本；原证据和控制器不变。

### 文件职责

| 文件 | 操作与职责 |
| --- | --- |
| `apps/test-service/internal/toolhash/scope.go` | 增加 `ValidateContext`；不改变 Digest、Close 或资格规则 |
| `apps/test-service/internal/toolhash/scope_context_test.go` | 新建：只读绑定、关闭、失败、取消与并发校验测试，使用真实 Scope 状态 |
| `apps/test-service/internal/runtime/production_context_tool_digest_scope.go` | 新建：私有标记、选择字段核对、借用视图及拥有者工厂 |
| `apps/test-service/internal/runtime/production_generation_runtime_authority.go` | 入口拥有与退出清理；其余原验证顺序不变 |
| `apps/test-service/internal/runtime/coverage_tool_digest_scope.go` | 私有标记存在时借用，否则保留原 Begin |
| `apps/test-service/internal/runtime/coverage_detail_backend.go` | 原报告和运行绑定后、工厂调用前核对标记选择字段 |
| `apps/test-service/internal/runtime/production_context_tool_digest_scope_windows_test.go` | 新建：真实权威入口和真实索引路径的生命周期、拒绝、容量与并发测试 |
| `apps/test-service/internal/runtime/production_context_tool_digest_scope_nonwindows_test.go` | 新建：非 Windows 无保护权威契约；本地只审核，不执行 Linux |

只读复用 `coverage_tool_digest_scope_windows_test.go` 的 `newDigestScopeRuntime`、`observedDigestScope`，以及 `production_generation_runtime_authority_test.go` 的 `productionRuntimeAuthorityFixture`。不修改它们。CMake、toolchain、driver、pipeline、taskstore、发布和 UI 文件均不属于本次写入范围。

## 定点执行控制

以下准备和运行命令仅供获准后的执行阶段。两个任务共用一个本任务副本，不重复制旧测试、原计时诊断或 native。没有准备完成的受控副本时禁止直接在脏工作树 `apps/test-service` 中运行 Go。

- [ ] **执行 Task 1 前建立一次新的保护快照。** 在 PowerShell 7 中读取旧控制器并运行 `Assert-ABIProtected`；记录 HEAD、原状态 19 项、原 Frozen/Bindings、旧 Owned9、全部本次 Owned 文件的存在及 SHA256。新控制器只允许文件职责表中的 8 个文件变化；旧 Owned9 在本任务中转为只读。所有路径用绝对解析及逐级 ReparsePoint 检查。旧快照不编辑。

- [ ] **复制一次已准备的一版模块。** 按旧 `scalar-abi-native-20261011T013031223Z-build.source-manifest.json` 中的 950 个模块相对路径，从旧 `prepared.json` 的 ModuleRoot 逐文件核对 SHA256 并复制到 `.superpowers/runtime/production-context-digest-<新 GUID>/module`。对源、目标和每一级祖先做旧控制器的 `Assert-ABIContained` 检查。若旧副本任一基线哈希不符则停止，不从脏 worktree 补齐。新副本禁止出现 V2 migration 022 或暂停的 casedesign 文件；之后仅按文件职责表同步本次源码覆盖，记录每次运行的覆盖哈希。

- [ ] **保存独立运行器。** 沿用旧控制器的 held `Process`、异步双流读取和 JSON 事件检查实现；新函数名为 `Run-ContextDigestTest([string]$Package,[string]$Regex,[ValidateSet('RED','GREEN')][string]$Expected)`，不得调用旧 `Run-ABITest`。下面是其必须使用的进程参数和结果闸门：

```powershell
$psi = [Diagnostics.ProcessStartInfo]::new()
$psi.FileName = 'D:/Program Files/Go/bin/go.exe'
$psi.WorkingDirectory = $ModuleRoot
$psi.UseShellExecute = $false
$psi.CreateNoWindow = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
foreach ($pair in @{GOWORK='off';GOPROXY='off';GOSUMDB='off';GOFLAGS='-mod=readonly';GOTOOLCHAIN='local'}.GetEnumerator()) {
    $psi.Environment[$pair.Key] = $pair.Value
}
$psi.Environment.Remove('UNIT_TEST_IDE_SCALAR_ABI_BUNDLE_ROOT') | Out-Null
foreach ($arg in @('test','-json','-count=1','-timeout=60s','-run',$Regex,$Package)) {
    $psi.ArgumentList.Add($arg)
}
```

允许包仅 `./internal/toolhash` 和 `./internal/runtime`，允许根测试仅各任务列出的名称。Regex 必须首尾锚定，禁用 tags、`./...`、全包无选择器及 race 额外编译。Go 自身 60 秒；held 父进程从 Start 后最长 90 秒。超时只终止持有的该进程树，最多 15 秒确认退出、5 秒排空流；不按名字杀进程、不重跑。保存完整 JSON 流和 stderr 后检查每个选定根恰好 run 一次；GREEN 必须全部 pass、无 fail/skip、自然 Go 0、排空成功；RED 必须选定根真实 run/fail、自然非零退出，并核对对应行为断言，不能是编译失败、超时或夹具错误。任何非预期失败停止当前任务、保留证据，按 systematic-debugging 定点修正后决定新尝试，不盲目重复。

新运行器执行前后核对所有原保护状态以及本任务 Owned 覆盖；只在单文件/本任务提交闸门允许暂时的索引变化。每次修改后只重跑受影响的根选择器，最终不把已取得的 GREEN 再整批重跑。运行器源码、准备快照、SHA256、进程退出、根事件与资源状态留在本任务 ledger，不提交产品仓库。

## Task 1 只读 Scope 绑定与活动校验

**Files:**

- Modify: `apps/test-service/internal/toolhash/scope.go`，`Err` 与 `liveLocked` 之间。
- Create/Test: `apps/test-service/internal/toolhash/scope_context_test.go`。
- Read only: `scope_windows.go`、`scope_nonwindows.go`、现有 `scope_windows_test.go`。

**Interfaces:**

- Consumes: `func newScope(context.Context, scopeOps, int, int64) *Scope`；`func (*Scope) liveLocked(context.Context) error`；`func (*Scope) Err() error`；`func (*Scope) Close() error`。
- Produces: `func (*Scope) ValidateContext(context.Context) error`。只确认指定 Scope 的实际 context 绑定和活动状态，不取出任意 Scope、不计算摘要、不改变状态、不关闭句柄。

- [ ] **Step 1 写测试，再补齐可编译的旧行为适配。** 测试使用 `newScope` 建立真实公共状态，空 entries 不需要文件或平台能力。先写下面代码；补齐方法签名时仅将 `ValidateContext` 的临时体设为 `return s.Err()`，表达当前只看 Err 的不足。签名准备不是实现通过，也不计作 RED；不运行缺方法版本。随后运行测试，要求失败原因是错误绑定或已关闭仍被接受。

旧行为适配仅用于获得可编译的行为 RED，Step 3 必须替换它，不能作为最终提交保留：

```go
func (s *Scope) ValidateContext(ctx context.Context) error { return s.Err() }
```

```go
package toolhash

import (
    "context"
    "errors"
    "sync"
    "testing"
)

func TestToolHashValidateContextBindingAndLiveness(t *testing.T) {
    for _, name := range []string{"live", "foreign", "closed", "failed", "call-cancelled", "parent-cancelled", "nil-scope", "nil-context"} {
        t.Run(name, func(t *testing.T) {
            parent, cancelParent := context.WithCancel(context.Background())
            defer cancelParent()
            s := newScope(parent, scopeOps{}, MaxEntries, MaxEligibleBytes)
            defer s.Close()
            ctx := context.WithValue(parent, scopeKey{}, s)
            wantErr := name != "live"
            sentinel := errors.New("bound validation failed")
            switch name {
            case "foreign":
                other := newScope(parent, scopeOps{}, MaxEntries, MaxEligibleBytes)
                defer other.Close()
                ctx = context.WithValue(ctx, scopeKey{}, other)
            case "closed":
                if err := s.Close(); err != nil { t.Fatal(err) }
            case "failed":
                s.poison(sentinel)
            case "call-cancelled":
                var cancel context.CancelFunc
                ctx, cancel = context.WithCancel(ctx)
                cancel()
            case "parent-cancelled":
                cancelParent()
            case "nil-scope":
                s = nil
            case "nil-context":
                ctx = nil
            }
            var before Stats
            if s != nil { before = s.Stats() }
            err := s.ValidateContext(ctx)
            if (err != nil) != wantErr { t.Fatalf("%s: accepted invalid binding/lifetime: %v", name, err) }
            if name == "failed" && !errors.Is(err, sentinel) { t.Fatalf("lost sticky error: %v", err) }
            if (name == "call-cancelled" || name == "parent-cancelled") && !errors.Is(err, context.Canceled) { t.Fatalf("lost cancellation: %v", err) }
            if s != nil && s.Stats() != before { t.Fatal("read-only check changed stats") }
            if name == "foreign" && s.Err() != nil { t.Fatal("foreign check poisoned owner") }
        })
    }
}

func TestToolHashValidateContextConcurrentClose(t *testing.T) {
    ctx := context.Background()
    s := newScope(ctx, scopeOps{}, MaxEntries, MaxEligibleBytes)
    ctx = context.WithValue(ctx, scopeKey{}, s)
    var wg sync.WaitGroup
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func() { defer wg.Done(); for n := 0; n < 32; n++ { _ = s.ValidateContext(ctx) } }()
    }
    if err := s.Close(); err != nil { t.Fatal(err) }
    wg.Wait()
    if s.ValidateContext(ctx) == nil { t.Fatal("closed scope accepted after concurrent readers drained") }
    if s.Err() != nil || s.Stats().Retained != 0 { t.Fatal("ordinary close semantics changed") }
}
```

- [ ] **Step 2 定点取得行为 RED。**

```powershell
Run-ContextDigestTest './internal/toolhash' '^TestToolHashValidateContext(BindingAndLiveness|ConcurrentClose)$' RED
```

预期 foreign/closed/call-cancelled/nil-context 的旧行为接受断言，以及并发读结束后 closed 接受断言失败。nil receiver 的旧 Err 合法，不应 panic。每根都有 run/fail，缺方法或 panic 不合格。

- [ ] **Step 3 实现只读检查。** 在共同文件内用已有 `contextError`、私有 `scopeKey` 和 `liveLocked`，不修改平台 Begin 或 Err 的语义。

```go
// ValidateContext checks a particular scope without granting extraction authority.
func (s *Scope) ValidateContext(ctx context.Context) error {
    if err := contextError(ctx); err != nil { return err }
    if s == nil { return errors.New("missing tool hash scope") }
    if bound, _ := ctx.Value(scopeKey{}).(*Scope); bound != s {
        return errors.New("tool hash context binding changed")
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.liveLocked(ctx)
}
```

- [ ] **Step 4 格式化并用同一选择器取得 GREEN。** 仅格式化 Task 1 两个文件，同步到本任务副本，运行 Step 2 命令但 Expected 为 GREEN。追加一次现有容量/消费者限制的窄回归，不需要真实探测：

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w apps/test-service/internal/toolhash/scope.go apps/test-service/internal/toolhash/scope_context_test.go
Run-ContextDigestTest './internal/toolhash' '^TestToolHashValidateContext(BindingAndLiveness|ConcurrentClose)$' GREEN
Run-ContextDigestTest './internal/toolhash' '^TestToolHash(OptionalMissAndCapacity|ConsumerLimitRemainsStrict)$' GREEN
```

- [ ] **Step 5 独立审查和本地提交。** 审查只读不 poison、不关 Scope、closed 与 Err 区分、nil 和取消优先级、锁内无外部探测。选择子代理执行时分别进行规格审查与代码审查；选择 inline 时由主代理在此检查点完成等效逐项审查，不主动派代理。审查不重跑已有 GREEN。明确 staged 仅下列两个路径，提交后保护快照全部相同：

```powershell
git add -- apps/test-service/internal/toolhash/scope.go apps/test-service/internal/toolhash/scope_context_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m "fix(toolhash): validate bound scope lifetime without mutation"
```

## Task 2 解析拥有者与索引同步借用

**Files:**

- Create: `apps/test-service/internal/runtime/production_context_tool_digest_scope.go`。
- Modify: `production_generation_runtime_authority.go`、`coverage_tool_digest_scope.go`、`coverage_detail_backend.go`，均位于上述 runtime 目录。
- Create/Test: `production_context_tool_digest_scope_windows_test.go`、`production_context_tool_digest_scope_nonwindows_test.go`，均位于上述 runtime 目录。
- Read only: 现有索引 guard 测试、authority 测试和 `production_generation_driver.go`/其测试。

**Interfaces:**

- Consumes: `func (*toolhash.Scope) ValidateContext(context.Context) error`，Task 1 提供；`toolhash.Begin`、`Digest`、`Evidence.Verify`/`Reject`、`Stats` 保持不变。
- Consumes: 现有 `coverageToolScope interface { Err() error; Close() error }` 和 `coverageToolScopeFactory func(context.Context) (context.Context, coverageToolScope, error)`。
- Produces: `type productionToolScopeFactory func(context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error)`；`func beginProductionToolScope(context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error)`。
- Produces: `func validateProductionToolScopeQuery(context.Context, coveragedetail.CurrentIndexQuery) error`；`func borrowProductionToolScope(context.Context) (coverageToolScope, bool, error)`。
- Produces: `func (runtimeProductionResolverAuthority) resolveProductionContext(context.Context, string, string, string, productionToolScopeFactory) (productionResolverContext, error)`，公开方法仍为原签名并只转发默认工厂。
- Produces: runtime 私有 `productionToolScopeKey`、`productionToolScopeBinding{scope *toolhash.Scope; query coveragedetail.CurrentIndexQuery}` 和 `borrowedProductionToolScope`。不导出标记，不持久化，不增加任意 Scope getter。

- [ ] **Step 1 加入真实入口的行为 RED 夹具和根测试。** Windows 测试文件采用 `//go:build windows`。需要导入 context、errors、fmt、crypto/sha256、encoding/hex、io、os、path/filepath、reflect、sync、testing、time 及 build/cmake/coveragedetail/coveragedomain/discovery/task/toolhash；各后续步骤合入时保留实际使用的 imports。夹具只控制存储/发现描述符与目标列表，不模拟摘要、文件身份或 Windows guard；不使用真实 runner。

```go
type productionDigestStore struct {
    *sourceBoundDetailStore
    graphErr error
    reads int
}
func (s *productionDigestStore) ReadValidatedCoverageIndex(ctx context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
    s.reads++
    if s.graphErr != nil { return coveragedetail.Index{}, s.graphErr }
    return s.sourceBoundDetailStore.ReadValidatedCoverageIndex(ctx, q)
}
type productionDigestCoordinator struct {
    *digestScopeCoordinator
    snapshot discovery.Snapshot
    after func(context.Context, int, discovery.Snapshot) (discovery.Snapshot, error)
}
func (c *productionDigestCoordinator) Inspect(ctx context.Context) (discovery.Snapshot, error) {
    if _, err := c.digestScopeCoordinator.Inspect(ctx); err != nil { return discovery.Snapshot{}, err }
    snapshot := c.snapshot
    if c.after != nil { return c.after(ctx, len(c.results), snapshot) }
    return snapshot, nil
}
type productionDigestReader struct {
    r *Runtime
    targets []cmake.Target
    listErr error
    afterIndex func(context.Context) error
}
func (p *productionDigestReader) InspectWorkspace(ctx context.Context) (discovery.Snapshot, error) { return p.r.InspectWorkspace(ctx) }
func (p *productionDigestReader) GetCoverageReport(ctx context.Context, id string) (coveragedomain.Report, error) { return p.r.store.GetCoverageReport(ctx, id) }
func (p *productionDigestReader) GetCoverageRun(ctx context.Context, id string) (coveragedomain.Run, error) { return p.r.store.GetCoverageRun(ctx, id) }
func (p *productionDigestReader) ListTargets(ctx context.Context, q build.TargetsRequest) ([]cmake.Target, error) {
    if p.listErr != nil { return nil, p.listErr }
    if _, err := p.r.InspectWorkspace(ctx); err != nil { return nil, err }
    return cmake.CloneTargets(p.targets), nil
}
func (p *productionDigestReader) ReadCurrentCoverageIndex(ctx context.Context, q coveragedetail.CurrentIndexQuery) (coveragedetail.Index, error) {
    index, err := p.r.ReadCurrentCoverageIndex(ctx, q)
    if err == nil && p.afterIndex != nil { err = p.afterIndex(ctx) }
    if err != nil { return coveragedetail.Index{}, err }
    return index, nil
}
func newProductionDigestFixture(t *testing.T) (*productionDigestReader, *productionDigestStore, *productionDigestCoordinator, coveragedetail.CurrentIndexQuery) {
    t.Helper()
    r, s, c, q := newDigestScopeRuntime(t)
    original := productionRuntimeAuthorityFixture()
    snapshot := original.snapshots[0]
    snapshot.Generation = q.WorkspaceGeneration
    s.run.ID = s.report.RunID
    s.run.ReportID = q.ReportID
    s.run.Outcome = coveragedomain.OutcomeAvailable
    s.run.Request.CoverageProfileID = "coverage"
    s.report.SchemaVersion = coveragedomain.SchemaVersion10
    stored := &productionDigestStore{sourceBoundDetailStore: s}
    coordinator := &productionDigestCoordinator{digestScopeCoordinator: c, snapshot: snapshot}
    r.store, r.coordinator = stored, coordinator
    return &productionDigestReader{r: r, targets: original.targets}, stored, coordinator, q
}
func TestProductionContextToolScopeFiveDiscoveries(t *testing.T) {
    p, s, c, q := newProductionDigestFixture(t)
    resolved, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID)
    if err != nil { t.Fatalf("five-site protected resolution failed: %v", err) }
    if len(c.results) != 5 || s.reads != 1 || len(resolved.index.Files) != 2 { t.Fatalf("discoveries=%d graph-reads=%d", len(c.results), s.reads) }
    for n, result := range c.results {
        if result.Reused != (n != 0) || result.Evidence != c.results[0].Evidence { t.Fatalf("site %d did not share one retained digest", n+1) }
    }
    if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil { t.Fatal("owner guard leaked", err) }
    f, err := os.Open(c.path)
    if err != nil { t.Fatal(err) }
    _, verifyErr := c.results[0].Evidence.Verify(c.contexts[0], c.path, f, 1024)
    if err := f.Close(); err != nil { t.Fatal(err) }
    if verifyErr == nil { t.Fatal("returned owner left evidence reusable") }
    if err := os.Remove(c.path); err != nil { t.Fatal("delete guard leaked", err) }
}
```

此夹具通过真实 `runtimeProductionResolverAuthority` 和真实 `Runtime.ReadCurrentCoverageIndex` 连接五处。`ListTargets` 夹具只提供其内部发现位置；实际 CMake File API/工具链校验的保持由未改源码与现有契约审查支撑，不将此夹具称为完整生产工具发现或 native 验收。

- [ ] **Step 2 取得真实行为 RED。**

```powershell
Run-ContextDigestTest './internal/runtime' '^TestProductionContextToolScopeFiveDiscoveries$' RED
```

预期原权威第一次发现没有当前解析保护，真实 Digest 不能给出 evidence，报 `five-site protected resolution failed: missing current-query scope`。缺 imports/方法、索引源绑定夹具错误不计 RED。

- [ ] **Step 3 加入私有绑定和借用视图。** `production_context_tool_digest_scope.go` 的实现如下。普通 Close 不关闭父 Scope，也不延长原 deadline；同步索引借用无后台任务和引用计数。

```go
package runtime

import (
    "context"
    "errors"
    "sync"
    "sync/atomic"
    "unit-test-ide.local/test-service/internal/coveragedetail"
    "unit-test-ide.local/test-service/internal/task"
    "unit-test-ide.local/test-service/internal/toolhash"
)

type productionToolScopeFactory func(context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error)
type productionToolScopeKey struct{}
type productionToolScopeBinding struct {
    scope *toolhash.Scope
    query coveragedetail.CurrentIndexQuery
}
func beginProductionToolScope(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
    scoped, scope, err := toolhash.Begin(ctx)
    if scope == nil { return scoped, nil, nil, err }
    return scoped, scope, scope, err
}
func validateProductionToolScopeQuery(ctx context.Context, q coveragedetail.CurrentIndexQuery) error {
    if ctx == nil { return task.ErrInvalidArgument }
    raw := ctx.Value(productionToolScopeKey{})
    if raw == nil { return nil }
    if err := ctx.Err(); err != nil { return err }
    binding, ok := raw.(*productionToolScopeBinding)
    if !ok || binding == nil || binding.scope == nil { return task.ErrStorageUnavailable }
    if binding.query.ProjectID != q.ProjectID || binding.query.WorkspaceGeneration != q.WorkspaceGeneration || binding.query.ReportID != q.ReportID {
        return task.ErrInvalidArgument
    }
    if binding.scope.ValidateContext(ctx) != nil { return task.ErrStorageUnavailable }
    return nil
}
func borrowProductionToolScope(ctx context.Context) (coverageToolScope, bool, error) {
    if ctx == nil { return nil, false, task.ErrInvalidArgument }
    raw := ctx.Value(productionToolScopeKey{})
    if raw == nil { return nil, false, nil }
    if err := ctx.Err(); err != nil { return nil, true, err }
    binding, ok := raw.(*productionToolScopeBinding)
    if !ok || binding == nil || binding.scope == nil { return nil, true, task.ErrStorageUnavailable }
    if binding.scope.ValidateContext(ctx) != nil { return nil, true, task.ErrStorageUnavailable }
    return &borrowedProductionToolScope{ctx: ctx, scope: binding.scope}, true, nil
}
type borrowedProductionToolScope struct {
    ctx context.Context
    scope *toolhash.Scope
    closed atomic.Bool
    once sync.Once
    closeErr error
}
func (b *borrowedProductionToolScope) Err() error {
    if b.closed.Load() { return errors.New("tool hash borrow ended") }
    return b.scope.ValidateContext(b.ctx)
}
func (b *borrowedProductionToolScope) Close() error {
    b.once.Do(func() {
        b.closeErr = b.scope.ValidateContext(b.ctx)
        b.closed.Store(true)
    })
    return b.closeErr
}
```

在 `beginCoverageToolScope` 的原 Begin 前插入下列分支，原无标记代码一字不省；保持工厂签名，测试专用工厂不能替代正式路径的 marker 校验：

```go
if borrowed, present, err := borrowProductionToolScope(ctx); present || err != nil {
    return ctx, borrowed, err
}
```

在 `readCurrentCoverageIndex` 中既有 report/run project/generation 拒绝之后、`factory == nil` 之前插入下列代码，保留后面的两次发现、全部 graph/source 校验和既有清理：

```go
if err := validateProductionToolScopeQuery(ctx, q); err != nil {
    return coveragedetail.Index{}, err
}
```

- [ ] **Step 4 给权威入口接入拥有者并清理所有返回。** 公开方法只转发到内部带工厂方法；后者命名返回 `resolved productionResolverContext, err error`。保留原入口参数拒绝；原主体从第一次 Inspect 起全部原样移动到内部方法，不做目标缓存或发现去重。

```go
func (authority runtimeProductionResolverAuthority) ResolveProductionContext(ctx context.Context, projectID, generation, reportID string) (productionResolverContext, error) {
    return authority.resolveProductionContext(ctx, projectID, generation, reportID, beginProductionToolScope)
}
```

内部方法将原方法签名替换为下面的命名返回签名，并保留原花括号内的入口检查及全部主体：

```go
func (authority runtimeProductionResolverAuthority) resolveProductionContext(ctx context.Context, projectID, generation, reportID string, factory productionToolScopeFactory) (resolved productionResolverContext, err error)
```

在原入口参数检查通过后插入以下代码，之后继续原主体。原 `snapshot, err :=` 和 `current, err :=` 仍合法，返回仍按原值类型构造；toolhash import 位于私有工厂定义的 helper 文件中，authority 文件不需要新增该 import，不在主体重复创建 Scope。

```go
if factory == nil { return productionResolverContext{}, task.ErrStorageUnavailable }
scoped, scope, owner, beginErr := factory(ctx)
defer func() {
    // Register immediately: even a malformed factory result must release its owner.
    var finishErr error
    if scoped != nil { finishErr = scoped.Err() }
    if finishErr == nil && scope != nil { finishErr = scope.ValidateContext(scoped) }
    if finishErr == nil && owner != nil { finishErr = owner.Err() }
    var closeErr error
    if owner != nil { closeErr = owner.Close() } else if scope != nil { closeErr = scope.Close() }
    if err == nil && finishErr != nil {
        if ctx.Err() != nil { err = ctx.Err() } else { err = task.ErrStorageUnavailable }
    }
    if err == nil && closeErr != nil {
        if ctx.Err() != nil { err = ctx.Err() } else { err = task.ErrStorageUnavailable }
    }
    if err != nil { resolved = productionResolverContext{} }
}()
if beginErr != nil || scoped == nil || (scope == nil) != (owner == nil) {
    if ctx.Err() != nil { return productionResolverContext{}, ctx.Err() }
    return productionResolverContext{}, task.ErrStorageUnavailable
}
ctx = scoped
if scope != nil {
    if scope.ValidateContext(ctx) != nil { return productionResolverContext{}, task.ErrStorageUnavailable }
    ctx = context.WithValue(ctx, productionToolScopeKey{}, &productionToolScopeBinding{
        scope: scope,
        query: coveragedetail.CurrentIndexQuery{ProjectID: projectID, WorkspaceGeneration: generation, ReportID: reportID},
    })
}
```

工厂 seam 仅限包内测试；默认 Begin 每次真实新建，不从输入标记读取 Scope。非 Windows 默认 `(ctx,nil,nil,nil)`，没有 marker 和额外保护 owner。若原操作已失败，defer 保留其主错误；若原操作成功但 ctx/Scope/Close 失败，返回空结果。原 context deadline 不变。

- [ ] **Step 5 同一入口选择器获得 GREEN。** 格式化本任务实际改动文件，同步受控副本，运行 Step 2 的原选择器，Expected 为 GREEN。五处顺序必须仍为 outer first、ListTargets 内部、index before、index after、outer last；首次真实 SHA256 一次，随后同一 evidence 四次复用；返回后真实写入和删除可行，旧 evidence 拒绝。

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w apps/test-service/internal/runtime/production_context_tool_digest_scope.go apps/test-service/internal/runtime/production_generation_runtime_authority.go apps/test-service/internal/runtime/coverage_tool_digest_scope.go apps/test-service/internal/runtime/coverage_detail_backend.go apps/test-service/internal/runtime/production_context_tool_digest_scope_windows_test.go
Run-ContextDigestTest './internal/runtime' '^TestProductionContextToolScopeFiveDiscoveries$' GREEN
```

- [ ] **Step 6 加入生命周期与独立查询测试。** 新根 `TestProductionContextToolScopeLifetime` 采用以下 body。工厂只用于记录真实 Scope 的 Close 次数，不模拟 digest。借用视图的直接关闭行为另用 `TestProductionContextToolScopeBorrowView`。

```go
func TestProductionContextToolScopeLifetime(t *testing.T) {
    p, _, c, q := newProductionDigestFixture(t)
    deadline := time.Now().Add(30 * time.Second)
    input, cancel := context.WithDeadline(context.Background(), deadline)
    defer cancel()
    var owners []*observedDigestScope
    factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
        scoped, scope, err := toolhash.Begin(ctx)
        observed := &observedDigestScope{Scope: scope}
        owners = append(owners, observed)
        return scoped, scope, observed, err
    }
    p.afterIndex = func(ctx context.Context) error {
        binding := ctx.Value(productionToolScopeKey{}).(*productionToolScopeBinding)
        if err := binding.scope.ValidateContext(ctx); err != nil { return err }
        if owners[len(owners)-1].closed != 0 { return errors.New("borrow closed parent") }
        if got, ok := ctx.Deadline(); !ok || !got.Equal(deadline) { return errors.New("deadline changed") }
        if os.WriteFile(c.path, []byte("must be locked"), 0600) == nil || os.Remove(c.path) == nil { return errors.New("parent released before last discovery") }
        return nil
    }
    authority := runtimeProductionResolverAuthority{reader: p}
    for n := 0; n < 2; n++ {
        // The second input deliberately carries the first, now-closed internal marker.
        if n == 1 { input = c.contexts[0] }
        if _, err := authority.resolveProductionContext(input, q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory); err != nil { t.Fatal(err) }
        owner := owners[n]
        if owner.closed != 1 || owner.stats.FullHashCalls != 1 || owner.stats.Retained != 1 || owner.Scope.Stats().Retained != 0 { t.Fatalf("owner lifecycle: %+v", owner) }
        if c.results[5*n].Reused { t.Fatal("next resolution inherited old digest") }
    }
    if c.results[0].Evidence == c.results[5].Evidence { t.Fatal("cross-resolution evidence reuse") }
    naked, unrelated, err := toolhash.Begin(context.Background())
    if err != nil { t.Fatal(err) }
    defer unrelated.Close()
    before := len(c.results)
    if _, err := p.r.ReadCurrentCoverageIndex(naked, q); err != nil { t.Fatal(err) }
    if len(c.results) != before+2 || c.results[before].Reused || !c.results[before+1].Reused || unrelated.Stats().Retained != 0 { t.Fatal("standalone query borrowed arbitrary input scope") }
    if err := os.WriteFile(c.path, []byte("standalone released"), 0600); err != nil { t.Fatal(err) }
}
func TestProductionContextToolScopeBorrowView(t *testing.T) {
    p, _, c, q := newProductionDigestFixture(t)
    ctx, scope, err := toolhash.Begin(context.Background())
    if err != nil { t.Fatal(err) }
    defer scope.Close()
    ctx = context.WithValue(ctx, productionToolScopeKey{}, &productionToolScopeBinding{scope: scope, query: q})
    if _, err := p.r.InspectWorkspace(ctx); err != nil { t.Fatal(err) }
    view, present, err := borrowProductionToolScope(ctx)
    if !present || err != nil || view.Err() != nil { t.Fatal("live borrow rejected", err) }
    if view.Close() != nil || view.Close() != nil || scope.ValidateContext(ctx) != nil { t.Fatal("borrow close changed parent") }
    if view.Err() == nil { t.Fatal("ended view remains usable") }
    if _, err := p.r.InspectWorkspace(ctx); err != nil { t.Fatal(err) }
    if !c.results[1].Reused { t.Fatal("parent lost digest after borrow") }
    poisoned, _, err := borrowProductionToolScope(ctx)
    if err != nil { t.Fatal(err) }
    sentinel := errors.New("sticky after borrow")
    c.results[0].Evidence.Reject(sentinel)
    if !errors.Is(poisoned.Close(), sentinel) || !errors.Is(poisoned.Close(), sentinel) { t.Fatal("borrow cleanup swallowed sticky failure") }
}
```

- [ ] **Step 7 加入借用拒绝根。** 每例保留原 scopes 的 defer Close；新 scope 不能掩盖旧 marker 的错误。以下表分别覆盖三项选择字段、外来实际绑定、正常关闭（Err 仍 nil）、粘性失败、取消和错误标记类型。

```go
func TestProductionContextToolScopeBorrowValidation(t *testing.T) {
    for _, name := range []string{"project", "generation", "report", "foreign", "closed", "failed", "cancelled", "nil-marker", "wrong-marker-type"} {
        t.Run(name, func(t *testing.T) {
            p, _, c, q := newProductionDigestFixture(t)
            ctx, scope, err := toolhash.Begin(context.Background())
            if err != nil { t.Fatal(err) }
            defer scope.Close()
            if _, err := p.r.InspectWorkspace(ctx); err != nil { t.Fatal(err) }
            binding := &productionToolScopeBinding{scope: scope, query: q}
            ctx = context.WithValue(ctx, productionToolScopeKey{}, binding)
            want := task.ErrStorageUnavailable
            switch name {
            case "project": binding.query.ProjectID = "other"; want = task.ErrInvalidArgument
            case "generation": binding.query.WorkspaceGeneration = "other"; want = task.ErrInvalidArgument
            case "report": binding.query.ReportID = "other"; want = task.ErrInvalidArgument
            case "foreign":
                var other *toolhash.Scope
                ctx, other, err = toolhash.Begin(ctx)
                if err != nil { t.Fatal(err) }
                defer other.Close()
            case "closed": if err := scope.Close(); err != nil { t.Fatal(err) }
            case "failed": c.results[0].Evidence.Reject(errors.New("sticky bound failure"))
            case "cancelled":
                var cancel context.CancelFunc
                ctx, cancel = context.WithCancel(ctx); cancel(); want = context.Canceled
            case "nil-marker": ctx = context.WithValue(ctx, productionToolScopeKey{}, (*productionToolScopeBinding)(nil))
            case "wrong-marker-type": ctx = context.WithValue(ctx, productionToolScopeKey{}, "untrusted value")
            }
            index, got := p.r.ReadCurrentCoverageIndex(ctx, q)
            if !errors.Is(got, want) || !reflect.DeepEqual(index, coveragedetail.Index{}) { t.Fatalf("accepted invalid borrow: index=%+v err=%v want=%v", index, got, want) }
            if len(c.results) != 1 { t.Fatal("invalid borrow opened fresh scope or inspected again") }
        })
    }
}
```

- [ ] **Step 8 加入保持新鲜度与清理根。** 使用表驱动注入，错误只在真实五个调用位置或原元数据边界注入，不替代 source validator。每例返回零解析，factory 捕获的真实 owner 只 Close 一次、Retained 为零、实际工具可写可删。补充取消、Close 错误、原错误优先和 swallowed failure；源文件仍可修改以证明不被工具锁保护。

```go
func TestProductionContextToolScopeFreshnessAndCleanup(t *testing.T) {
    names := []string{"first-generation", "index-before-generation", "index-after-generation", "last-generation", "last-project", "last-profile", "last-coverage-profile", "report-binding", "run-binding", "graph", "source", "untrusted", "list", "close", "primary-plus-close", "cancel", "swallowed"}
    for at := 1; at <= 5; at++ { names = append(names, fmt.Sprintf("inspect-%d", at)) }
    for _, name := range names {
        t.Run(name, func(t *testing.T) {
            p, s, c, q := newProductionDigestFixture(t)
            ctx, cancel := context.WithCancel(context.Background())
            defer cancel()
            primary := errors.New("controlled primary error")
            var observed *observedDigestScope
            factory := func(ctx context.Context) (context.Context, *toolhash.Scope, coverageToolScope, error) {
                scoped, scope, err := toolhash.Begin(ctx)
                observed = &observedDigestScope{Scope: scope}
                if name == "close" || name == "primary-plus-close" { observed.closeErr = errors.New("controlled close error") }
                return scoped, scope, observed, err
            }
            want := coveragedetail.ErrStale
            switch name {
            case "report-binding": s.report.ID = "wrong"
            case "run-binding": s.run.ReportID = "wrong"
            case "graph": s.graphErr = task.ErrStorageUnavailable; want = task.ErrStorageUnavailable
            case "source":
                if err := os.WriteFile(filepath.Join(p.r.workspaceRoot.NativePath, "other.c"), []byte("changed"), 0600); err != nil { t.Fatal(err) }
            case "untrusted": p.r.trustedWorkspace = false; want = build.ErrWorkspaceTrustRequired
            case "list", "primary-plus-close": p.listErr = primary; want = primary
            case "close", "swallowed": want = task.ErrStorageUnavailable
            case "cancel": want = context.Canceled
            }
            c.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
                if name == fmt.Sprintf("inspect-%d", call) { return discovery.Snapshot{}, primary }
                if name == "first-generation" && call == 1 || name == "index-before-generation" && call == 3 || name == "index-after-generation" && call == 4 || name == "last-generation" && call == 5 { snapshot.Generation = "changed" }
                if name == "last-project" && call == 5 {
                    snapshot.Projects = append(snapshot.Projects[:0:0], snapshot.Projects...)
                    snapshot.Projects[0].SourceDir = "changed"
                }
                if name == "last-profile" && call == 5 {
                    snapshot.Profiles = append([]cmake.BuildProfile(nil), snapshot.Profiles...)
                    snapshot.Profiles[0].BinaryDir = "changed"
                }
                if name == "last-coverage-profile" && call == 5 {
                    snapshot.CoverageProfiles = append(snapshot.CoverageProfiles[:0:0], snapshot.CoverageProfiles...)
                    snapshot.CoverageProfiles[0].BaseBuildProfileID = "changed"
                }
                if name == "cancel" && call == 5 { cancel() }
                if name == "swallowed" && call == 5 { c.results[0].Evidence.Reject(primary) }
                return snapshot, nil
            }
            if len(name) >= 8 && name[:8] == "inspect-" { want = primary }
            resolved, err := (runtimeProductionResolverAuthority{reader: p}).resolveProductionContext(ctx, q.ProjectID, q.WorkspaceGeneration, q.ReportID, factory)
            if !errors.Is(err, want) || !reflect.DeepEqual(resolved, productionResolverContext{}) { t.Fatalf("accepted failure: result=%+v err=%v want=%v", resolved, err, want) }
            if observed == nil || observed.closed != 1 || observed.Scope.Stats().Retained != 0 { t.Fatal("owner not released exactly once") }
            if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil { t.Fatal("guard leaked", err) }
            if err := os.Remove(c.path); err != nil { t.Fatal("delete guard leaked", err) }
        })
    }
}
```

`untrusted` 在权威第一次调用真实 Runtime.InspectWorkspace 时由现有 `build.ErrWorkspaceTrustRequired` 拒绝；独立索引的 detailReader 仍用现有 `task.ErrStorageUnavailable`。不新增错误或改变优先级。通过旧 `TestRuntimeToolDigestScopeIndexBindingRejectsReportRunProject`、`SourceDrift`、`RetainsBothFreshnessChecks`、`CancellationAndCleanupError` 的一次窄回归补足 index 内部绑定与两处新鲜度错误；不是省略这些校验。

- [ ] **Step 8a 验证拒绝入口不创建 owner 和工厂异常仍清理。** 新根对参数拒绝统计工厂调用必须为零；对 Begin 返回错误/缺 owner/缺 scoped ctx 预先建立真实 guard，然后检查返回空结果并释放。另一子例在最终发现主动 panic，原 panic 继续向外传播而 owner 仍关闭；不在生产代码 recover。

```go
func TestProductionContextToolScopeEntryAndFactory(t *testing.T) {
    for _, name := range []string{"reader", "context", "project", "generation", "report"} {
        t.Run("entry-"+name, func(t *testing.T) {
            p, _, _, q := newProductionDigestFixture(t)
            authority := runtimeProductionResolverAuthority{reader:p}
            ctx := context.Background()
            switch name {
            case "reader": authority.reader=nil
            case "context": ctx=nil
            case "project": q.ProjectID=""
            case "generation": q.WorkspaceGeneration="invalid"
            case "report": q.ReportID="invalid"
            }
            calls:=0
            factory:=func(ctx context.Context)(context.Context,*toolhash.Scope,coverageToolScope,error){ calls++; return beginProductionToolScope(ctx) }
            result,err:=authority.resolveProductionContext(ctx,q.ProjectID,q.WorkspaceGeneration,q.ReportID,factory)
            if !errors.Is(err,task.ErrInvalidArgument) || !reflect.DeepEqual(result,productionResolverContext{}) || calls!=0 { t.Fatal("invalid entry created owner",err) }
        })
    }
    for _, name := range []string{"begin-error", "missing-owner", "missing-context", "panic"} {
        t.Run(name, func(t *testing.T) {
            p, _, c, q := newProductionDigestFixture(t)
            var observed *observedDigestScope
            var scope *toolhash.Scope
            factory:=func(ctx context.Context)(context.Context,*toolhash.Scope,coverageToolScope,error){
                scoped,s,err:=toolhash.Begin(ctx)
                if err!=nil { return scoped,nil,nil,err }
                scope=s
                observed=&observedDigestScope{Scope:s}
                if name!="panic" {
                    if _,err:=p.r.InspectWorkspace(scoped); err!=nil { return scoped,s,observed,err }
                }
                switch name {
                case "begin-error": return scoped,s,observed,errors.New("controlled begin error")
                case "missing-owner": return scoped,s,nil,nil
                case "missing-context": return nil,s,observed,nil
                }
                return scoped,s,observed,nil
            }
            authority:=runtimeProductionResolverAuthority{reader:p}
            if name=="panic" {
                sentinel:=errors.New("controlled panic")
                c.after=func(ctx context.Context,call int,snapshot discovery.Snapshot)(discovery.Snapshot,error){ if call==5 { panic(sentinel) }; return snapshot,nil }
                func(){ defer func(){ if recover()!=sentinel { t.Error("panic was replaced or swallowed") } }(); _,_=authority.resolveProductionContext(context.Background(),q.ProjectID,q.WorkspaceGeneration,q.ReportID,factory) }()
            } else {
                result,err:=authority.resolveProductionContext(context.Background(),q.ProjectID,q.WorkspaceGeneration,q.ReportID,factory)
                if !errors.Is(err,task.ErrStorageUnavailable) || !reflect.DeepEqual(result,productionResolverContext{}) { t.Fatal("malformed factory accepted",err) }
            }
            if scope==nil || scope.Stats().Retained!=0 { t.Fatal("partial owner leaked") }
            if name!="missing-owner" && observed.closed!=1 { t.Fatal("owner close count changed") }
            if err:=os.WriteFile(c.path,[]byte("released"),0600); err!=nil { t.Fatal(err) }
        })
    }
}
```

- [ ] **Step 9 验证共享容量和真实回退。** 新根 `TestProductionContextToolScopeCapacity` 创建 130 个小工具，第一发现保留前 65 个，index before 保留其余，最后检查共享池。主夹具工具本身占 1，最终 Retained/FullHashCalls 恰为 128；容量未入场工具通过实际 `io.Copy` 全量哈希，而不是返回虚构摘要。严格 consumer 最大值和保护不可用回退沿用 Task 1 已取得的现有 GREEN，不重复执行它们。

```go
func TestProductionContextToolScopeCapacity(t *testing.T) {
    p, _, c, q := newProductionDigestFixture(t)
    dir := t.TempDir()
    paths := make([]string, 130)
    for n := range paths {
        paths[n] = filepath.Join(dir, fmt.Sprintf("tool-%03d.exe", n))
        if err := os.WriteFile(paths[n], []byte("capacity tool"), 0600); err != nil { t.Fatal(err) }
    }
    fallback := 0
    var parent *toolhash.Scope
    c.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
        parent = ctx.Value(productionToolScopeKey{}).(*productionToolScopeBinding).scope
        first, last := 0, 0
        if call == 1 { first, last = 0, 65 }
        if call == 3 { first, last = 65, 130 }
        for _, path := range paths[first:last] {
            f, err := os.Open(path)
            if err != nil { return discovery.Snapshot{}, err }
            result, protected, err := toolhash.Digest(ctx, path, f, 1024)
            if err == nil && !protected {
                hash := sha256.New()
                _, err = io.Copy(hash, f)
                if err == nil && hex.EncodeToString(hash.Sum(nil)) != fmt.Sprintf("%x", sha256.Sum256([]byte("capacity tool"))) { err = errors.New("incorrect fallback hash") }
                fallback++
            }
            closeErr := f.Close()
            if err != nil { return discovery.Snapshot{}, err }
            if closeErr != nil { return discovery.Snapshot{}, closeErr }
            if protected && result.Evidence == nil { return discovery.Snapshot{}, errors.New("missing retained evidence") }
        }
        if call == 5 {
            stats := parent.Stats()
            if stats.Retained != 128 || stats.FullHashCalls != 128 || fallback != 3 { return discovery.Snapshot{}, fmt.Errorf("shared cap mismatch: %+v fallback=%d", stats, fallback) }
        }
        return snapshot, nil
    }
    if _, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(context.Background(), q.ProjectID, q.WorkspaceGeneration, q.ReportID); err != nil { t.Fatal(err) }
    if parent.Stats().Retained != 0 { t.Fatal("capacity guards leaked") }
    for _, path := range paths { if err := os.Remove(path); err != nil { t.Fatal(err) } }
}
```

- [ ] **Step 10 验证同作用域并发、两个解析隔离与取消收尾。** 新根如下；所有 worker 用独立读句柄，没有新增生产后台借用。对同一解析的消费者保留实际 Digest/Evidence；两个解析使用同一输入 ctx、同一工具路径但不同 Scope，一个解析的 sticky failure 不污染另一个。

```go
func TestProductionContextToolScopeConcurrentConsumers(t *testing.T) {
    p, _, c, q := newProductionDigestFixture(t)
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    c.after = func(scoped context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
        if call != 5 { return snapshot, nil }
        var wg sync.WaitGroup
        failures := make(chan error, 8)
        for n := 0; n < 8; n++ {
            wg.Add(1)
            go func() {
                defer wg.Done()
                f, err := os.Open(c.path)
                if err != nil { failures <- err; return }
                result, protected, err := toolhash.Digest(scoped, c.path, f, 1024)
                closeErr := f.Close()
                if err == nil && (!protected || !result.Reused || result.Evidence != c.results[0].Evidence) { err = errors.New("concurrent consumer lost parent evidence") }
                failures <- errors.Join(err, closeErr)
            }()
        }
        done := make(chan struct{})
        go func() { wg.Wait(); close(done) }()
        select { case <-done: case <-time.After(2*time.Second): return discovery.Snapshot{}, errors.New("consumer drain timeout") }
        close(failures)
        for err := range failures { if err != nil { return discovery.Snapshot{}, err } }
        cancel()
        return snapshot, nil
    }
    result, err := (runtimeProductionResolverAuthority{reader: p}).ResolveProductionContext(ctx, q.ProjectID, q.WorkspaceGeneration, q.ReportID)
    if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, productionResolverContext{}) { t.Fatal("cancellation returned success", err) }
    if err := os.WriteFile(c.path, []byte("released"), 0600); err != nil { t.Fatal(err) }
}
func TestProductionContextToolScopeConcurrentResolutions(t *testing.T) {
    p1, _, c1, q1 := newProductionDigestFixture(t)
    p2, _, c2, q2 := newProductionDigestFixture(t)
    c2.path = c1.path
    input := context.Background()
    arrived := make(chan struct{}, 2)
    release := make(chan struct{})
    firstDone := make(chan struct{})
    type outcome struct { result productionResolverContext; err error }
    first, second := make(chan outcome, 1), make(chan outcome, 1)
    barrier := func() error {
        arrived <- struct{}{}
        select { case <-release: return nil; case <-time.After(2*time.Second): return errors.New("resolution barrier timeout") }
    }
    c1.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
        if call == 1 { if err := barrier(); err != nil { return discovery.Snapshot{}, err } }
        if call == 5 { c1.results[0].Evidence.Reject(errors.New("first resolution poisoned")) }
        return snapshot, nil
    }
    c2.after = func(ctx context.Context, call int, snapshot discovery.Snapshot) (discovery.Snapshot, error) {
        if call == 1 { if err := barrier(); err != nil { return discovery.Snapshot{}, err } }
        if call == 5 {
            select { case <-firstDone: case <-time.After(2*time.Second): return discovery.Snapshot{}, errors.New("first owner did not drain") }
            if os.WriteFile(c2.path, []byte("must remain protected"), 0600) == nil { return discovery.Snapshot{}, errors.New("other owner released my guard") }
        }
        return snapshot, nil
    }
    go func() { r, err := (runtimeProductionResolverAuthority{reader:p1}).ResolveProductionContext(input,q1.ProjectID,q1.WorkspaceGeneration,q1.ReportID); first <- outcome{r,err}; close(firstDone) }()
    go func() { r, err := (runtimeProductionResolverAuthority{reader:p2}).ResolveProductionContext(input,q2.ProjectID,q2.WorkspaceGeneration,q2.ReportID); second <- outcome{r,err} }()
    for n:=0; n<2; n++ { select { case <-arrived: case <-time.After(2*time.Second): t.Fatal("owners failed to reach barrier") } }
    close(release)
    var a, b outcome
    select { case a=<-first: case <-time.After(2*time.Second): t.Fatal("first did not finish") }
    select { case b=<-second: case <-time.After(2*time.Second): t.Fatal("second did not finish") }
    if !errors.Is(a.err, task.ErrStorageUnavailable) || !reflect.DeepEqual(a.result,productionResolverContext{}) || b.err!=nil { t.Fatalf("cross-owner contamination: %v %v",a.err,b.err) }
    if c1.results[0].Evidence==c2.results[0].Evidence || c1.results[0].Reused || c2.results[0].Reused { t.Fatal("concurrent resolutions shared evidence") }
    if err:=os.WriteFile(c1.path,[]byte("released"),0600); err!=nil { t.Fatal(err) }
}
```

这两个根证明复用后的并发消费者与解析隔离。另以现有 `TestToolHashCloseDuringHash`、`ConcurrentDiscardAndClose`、`PriorGuardCloseFailure` 的一次窄 GREEN 覆盖取消/关闭与真实流式哈希重叠的原机制；不把“先排空再取消”说成 in-flight 取消证明，不引入外部 probes 持锁等待。

- [ ] **Step 11 加入非 Windows 契约，保留原路径。** 新文件使用 `//go:build !windows`，导入 context/testing 及 discovery；不修改 platform Begin，也不在当前 Windows 中交叉编译或执行 Linux。

```go
type nonWindowsProductionScopeReader struct {
    *productionRuntimeContextFixture
    expected context.Context
    seen int
    bad bool
}
func (p *nonWindowsProductionScopeReader) InspectWorkspace(ctx context.Context) (discovery.Snapshot,error) {
    p.seen++
    if ctx!=p.expected || ctx.Value(productionToolScopeKey{})!=nil { p.bad=true }
    return p.productionRuntimeContextFixture.InspectWorkspace(ctx)
}
func TestProductionContextToolScopeNonWindows(t *testing.T) {
    input:=context.Background()
    ctx, scope, owner, err:=beginProductionToolScope(input)
    if err!=nil || ctx!=input || scope!=nil || owner!=nil { t.Fatal("non-Windows acquired protected owner") }
    fixture:=productionRuntimeAuthorityFixture()
    reader:=&nonWindowsProductionScopeReader{productionRuntimeContextFixture:fixture,expected:input}
    if _,err:=(runtimeProductionResolverAuthority{reader:reader}).ResolveProductionContext(input,"core",fixture.run.Request.WorkspaceGeneration,fixture.report.ID); err!=nil { t.Fatal(err) }
    if reader.bad || reader.seen!=2 { t.Fatal("non-Windows gained marker or lost outer inspections") }
}
```

- [ ] **Step 12 运行新增根和必要的窄回归一次。** 新根在编写后首先针对各具体拒绝条件检查断言；如果某个断言暴露遗漏，保留该根真实行为 RED，按同一选择器修正为 GREEN。已经取得的 FiveDiscoveries GREEN 不重复混入全量选择器。

```powershell
& 'D:/Program Files/Go/bin/gofmt.exe' -w apps/test-service/internal/runtime/production_context_tool_digest_scope_windows_test.go apps/test-service/internal/runtime/production_context_tool_digest_scope_nonwindows_test.go
Run-ContextDigestTest './internal/runtime' '^TestProductionContextToolScope(Lifetime|BorrowView|BorrowValidation|FreshnessAndCleanup|EntryAndFactory|Capacity|ConcurrentConsumers|ConcurrentResolutions)$' GREEN
Run-ContextDigestTest './internal/runtime' '^TestRuntimeToolDigestScope(IndexBindingRejectsReportRunProject|SourceDrift|RetainsBothFreshnessChecks|CancellationAndCleanupError|SwallowedConsumerFailure)$' GREEN
Run-ContextDigestTest './internal/runtime' '^TestProductionGenerationDriver(ResolvesAndRunsFixedStages|FailsClosedBeforeAnyStageProcess)$' GREEN
Run-ContextDigestTest './internal/toolhash' '^TestToolHash(CloseDuringHash|ConcurrentDiscardAndClose|PriorGuardCloseFailure)$' GREEN
```

最后两项夹具与小文件 tests 不运行生产编译器或外部探测。driver `resolver.calls==6`、`analyzer.calls==1` 保持，证明各阶段仍先解析，再用已有生成缓存。若实际已有测试出现需 native 的分支，停止该选择器，不设置环境变量启用它。

- [ ] **Step 13 规格审查、代码审查、本地提交与停止。** 核对下面覆盖矩阵、错误映射、closed/sticky 拒绝、128 共享池、每个失败位置释放、deadline 与空结果。检查本任务外所有源码哈希和原状态 19 项不变；不得以 GREEN 推断原 300 秒请求或 W4 完整 native 已完成。只提交 Task 2 六个路径：

```powershell
git add -- apps/test-service/internal/runtime/production_context_tool_digest_scope.go apps/test-service/internal/runtime/production_generation_runtime_authority.go apps/test-service/internal/runtime/coverage_tool_digest_scope.go apps/test-service/internal/runtime/coverage_detail_backend.go apps/test-service/internal/runtime/production_context_tool_digest_scope_windows_test.go apps/test-service/internal/runtime/production_context_tool_digest_scope_nonwindows_test.go
git diff --cached --check
git diff --cached --name-only
git commit -m "fix(runtime): share protected tool digests within one context resolution"
```

提交后记录两任务提交、测试选择器、行为 RED/GREEN、实际 Scope stats、关闭计数、保护快照及审查结论。保留隔离副本和 ledger；不使用通用收尾流程删除/归档含现有修改和证据的 worktree。后续真实解析计时与新的完整 native 验收分别另行取得有界授权，不自动触发。

## 规格覆盖矩阵

| 设计要求 | 实施与验证 |
| --- | --- |
| 指定 Scope 的实际绑定、closed/failed/取消只读检查 | Task 1 两根；ValidateContext 使用实际私有 key 与 liveLocked |
| 每解析新建、同输入 ctx 和旧 marker 不继承 | Task 2 Step 4；Lifetime；ConcurrentResolutions |
| 内部索引借用、不关父、重复 Close 和结束错误 | Step 3；BorrowView；Lifetime；FreshnessAndCleanup |
| 无 marker 独立索引仍独立拥有 | beginCoverage 原分支保留；Lifetime；原索引测试 |
| marker 选择字段、实际绑定、closed/failed/cancel 不回退 | BorrowValidation 三字段及五种失效表项 |
| 五次发现与 graph/source 原顺序次数 | FiveDiscoveries；FreshnessAndCleanup；原双发现/源绑定测试 |
| 首末和索引两处 generation、末次三种配置、报告/运行/索引拒绝 | FreshnessAndCleanup；原 IndexBinding 与 RetainsBothFreshnessChecks |
| CMake File API/toolchain/权限/SDK 非缓存 | 相关消费者文件只读保护，原校验代码逐项审查；非完整 native 声明 |
| 128 共享池、512 MiB、stricter cap/optional fallback | Capacity；Task 1 现有 OptionalMissAndCapacity/ConsumerLimit；常量与资格实现不变 |
| path/object/consumer/guard 身份核对 | Digest/Evidence/平台实现不变；真实 guard 和旧证据失效验证；既有身份测试证据继承不重复 |
| 错误、取消、Close、swallowed failure 为空结果并释放，入口/工厂/panic 清理 | FreshnessAndCleanup；BorrowView；EntryAndFactory；原 guard 清理和 Inspector swallowing tests |
| 同 Scope 并发、两个解析隔离与收尾 | ConcurrentConsumers/ConcurrentResolutions；原流式 CloseDuringHash 窄回归 |
| Linux 原逻辑、无 marker/no reuse authority | 非 Windows 新根及现有原根源码审查；本轮不执行 Linux |
| 不重设 deadline、不跳过缓存前解析 | Lifetime；driver 两根；driver/pipeline 哈希只读保护 |
| V2 暂停、旧 19 项和证据保留、不扩大验证 | 独立快照、仅 8 个允许源码路径、每次运行/提交前后保护 |

## 计划审阅与执行选择

- [x] 主代理核对全部设计段落与上述矩阵，无缺失要求。
- [x] 主代理扫描未定义符号、方法签名、临时旧行为适配的替换位置、选择器是否覆盖真实根、占位措辞和承诺边界；已修正信任错误类别、关闭时取消优先级及缺 owner 的清理，并保存。不派代理做计划审查，不运行 Go 来验证计划内代码。
- [ ] 文档提交只包含本计划；设计文档与产品源码不修改，记录人确认和计划 SHA256 到已有 main-owned ledger。
- [ ] 交由人选择：1 子代理逐任务实施并两阶段审查；2 当前会话 inline 按检查点实施。选择前不开始 Task 1。
