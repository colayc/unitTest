# 已选工具链 ID 契约修正 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正将合法工具链实例 ID 误当作 SHA-256 的生成边界，并精确验证已选 profile、当前实例、报告索引及确认时的同一身份，使原 W4 能安全恢复。

**Architecture:** 工具链 ID 使用私有 registry-compatible 谓词，真实摘要校验不变；resolver 要求非空明确选择及唯一当前实例，受管确认绑定第二次读取的 index 与刚重新解析的目标。两项顺序任务各自具有行为 RED/GREEN 和独立审查；本计划完成后才切回原 W4，不重开发生成器、不新增空 ID preset 授权机制。

**Tech Stack:** Go 1.26.6、现有 runtime/toolchain/discovery/CMake/managed-test 模块与 Go 测试；恢复 W4 时沿用 CMake 4.3.4、LLVM 22.1.8、Node 24.19、pnpm 11.4、Unity 2.6.1、CppUTest 4.0。

## Global Constraints

- 唯一新增设计依据：[已确认书面设计](../specs/2026-10-09-selected-toolchain-identity-contract-design.md)，用户于 2026-10-09 回复“确认设计文档”。旧 Windows 设计/计划仅按该设计第 6 节作有界修订。
- 工作区：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`；分支：`codex/phase10-production-generation`；计划前 HEAD 为 `d31ed39931003fdf42cf86d9b19b1098e569f60b`（文档提交，不是新的通过验收源 SHA）。
- 完全离线；不引入 Mock/CMock、CppUTestExt Mock 扩展、云模型、新服务端、协议版本或另一套生成算法。
- 工具链 ID 长度为 1–128 字节，每个字符只能是 ASCII `A-Z`、`a-z`、`0-9`、`.`、`_`、`-`，没有首字符必须字母数字的额外限制。
- 区分大小写、逐字节比较；不 trim、不转大小写、不哈希、不去掉前缀，不设置 family 前缀白名单。workspace 手动 ID 的 64 字节/首字母数字契约不变。
- `validProductionDigest` 保持严格的 64 个小写十六进制字符校验；`validProductionObjectID` 的 32 个小写十六进制字符实体 ID 规则保持不变。canonical compile identity 的域、版本、schema、序列化形式不变。
- 空 ID preset 与空 ID 非 preset profile 均在生成 resolver 拒绝；普通 preset、构建、发现和覆盖率实现保持不变，不改变 coordinator 的配置恢复、选择或重配置语义。
- 保留 APP_ID 进程闭包、输入快照/身份/摘要、受管输出、来源认证、分页/分析/执行预算、原覆盖率不回退；保持已有 128 个 CMake 文件、单文件 512 KiB、总计 4 MiB、8192 条命令、65536 个参数、64 个启动计划条目及 wire/File API/Launch 预算。
- 用户 CMake 不自动改写；框架/vendor/license、原 helper 与 CMake/LLVM/SDK 认证锁不改。未知/动态表达式、越界/别名、输入或选择变化及不完整证据继续失败关闭，不制造 PASS/skipped。
- 共享 runtime ID/精确选择修正与平台中立单元测试是唯一 Linux 行为例外；Linux fixture/CMake 集成不改，其头文件/目标命名前置问题继续未验收。不能将单元/跨平台编译/Windows 结果标记为 Linux 原生 PASS。
- 生产源/测试总边界为设计中的 5 + 6 个现有 Go 文件；新需求越界时报告具体证据，不扩大范围。不得编辑 producer/registry/workspace/build coordinator/preset authority API/数据库/协议/算法。
- 一次只有一个生产实施者；控制器不代写生产修复。SDD 选项使用本计划专属 `.superpowers/sdd/2026-10-09-selected-toolchain-identity-contract/`，不得读写旧/兄弟计划的 SDD 目录；恢复旧 W4 必须由控制器明确切换回原计划及其自有现场。
- 本计划不跑原生、race、全局 verify、远端操作，不重跑 W1–W3。两项通过审查后，原 W4 负责真实双框架、维护/重新打开、必要 race/全局收口、干净源 SHA 与整个分支审查。
- 不推送、不新建重复 PR、不合并、不操作 master、不强推、不发布 Release、不启用签名。既有双远端/PR #39/hosted 授权仅由控制器在完整本地门槛与审查后使用。

---

## 现场保护、文件地图与接口

现有 linked worktree 已确认，git-dir 不同于 common-dir；不要新建 checkout、stash/reset/checkout 清理现场、安装依赖或跑完整基线。执行前读取最新 HEAD、空 index 和这 8 个受保护源文件的 SHA-256，执行后逐个比较：

```powershell
$Protected = @(
  'tools/service-probe/fixtures/test-generation/cpputest/windows/CMakeLists.txt',
  'tools/service-probe/fixtures/test-generation/cpputest/windows/tests/CMakeLists.txt',
  'tools/service-probe/fixtures/test-generation/unity/windows/CMakeLists.txt',
  'tools/service-probe/fixtures/test-generation/unity/windows/tests/CMakeLists.txt',
  'tools/service-probe/src/native-test-generation.ts',
  'tools/service-probe/src/native-test-generation.test.ts',
  'tools/service-probe/src/test-generation-e2e.test.ts',
  'apps/code-oss-extension/test/coverage-first-report-native.test.mjs'
)
git status --short
git rev-parse HEAD
git diff --cached --name-only
$Protected | ForEach-Object { Get-FileHash -LiteralPath $_ -Algorithm SHA256 }
```

`docs/superpowers/evidence/phase10/` 未提交证据和忽略区诊断/失败 workspaces 同样保留；不能顺带 stage、复制成验收、清理或绕过此前清理策略拒绝。Task 1/2 的 BASE 在开始时捕获，不能固定为本文的文档 SHA；审查覆盖各自 BASE 到实际 HEAD 的完整累计任务 diff。

以下所有源文件路径前缀为 `apps/test-service/internal/runtime/`：

| 文件 | 任务 | 单一职责 |
| --- | --- | --- |
| `production_generation_pipeline.go` | 1 | 私有 ID 词法、目标 ToolchainID 守卫 |
| `production_generation_metadata.go` | 1 | compile-binding 的 ToolchainID 守卫 |
| `production_generation_validation.go` | 1 | managed case 的 ToolchainID 守卫 |
| `production_managed_selection_authority.go` | 1→2 | 先词法修正，再添加 fresh-index/target equality |
| `production_generation_resolver.go` | 2 | 非空已选 profile、精确相等、唯一当前实例 |
| `production_generation_metadata_test.go` | 1 | 合法/非法 ID、canonical identity、target/profile SHA |
| `production_generation_pipeline_test.go` | 1 | 渲染正例、真实 SHA 边界、可选 ID fixture 输入 |
| `production_generation_validation_test.go` | 1 | 受管保存/加载、source/artifact/receipt 真 SHA |
| `production_managed_selection_authority_test.go` | 1→2 | 原样合法 ID 正例，再验证真正的二次读取漂移 |
| `production_generation_resolver_test.go` | 2 | 明确选择、当前实例、拒绝顺序及不可变请求 |
| `production_generation_build_authority_test.go` | 2 | 原样请求/prepared ID、现有失败/释放边界 |

复用接口（实施者需读取定义，不改接口）：

```go
func resolveProductionCompileBinding(root workspace.Root, sourceRelative string, targets []cmake.Target, toolchainID string) (productionCompileBinding, error)
func (target generationTarget) valid() bool
func (pipeline *productionGenerationPipeline) Generate(context.Context, generationTarget) (productionPipelineResult, error)
func validProductionManagedCases([]productionManagedCaseEvidence, testgenpublish.CandidateSet, []byte) bool
func (resolver *productionGenerationTargetResolver) ResolveManagedStart(context.Context, string, generationv16.TestGenerationStartRequestV16, testgendomain.ManagedTarget) (generationTarget, error)
func (resolver *productionGenerationTargetResolver) ResolveRequest(context.Context, testgendomain.Request) (generationTarget, error)
func (authority *productionManagedSelectionAuthority) Resolve(context.Context, string) (testgenvalidate.SelectionContext, error)
func (authority *productionBuildAuthority) ResolveProductionBuild(context.Context, generationTarget) (productionBuildSnapshot, error)
```

## Task 1: 分离实例 ID 与真实 SHA，保持原样绑定和受管证据

**Files:**
- Modify: `apps/test-service/internal/runtime/production_generation_pipeline.go`（`validProductionDigest` 附近新增私有谓词；`generationTarget.valid` 仅替换 ID 守卫）。
- Modify: `apps/test-service/internal/runtime/production_generation_metadata.go`（`resolveProductionCompileBinding` 入口）。
- Modify: `apps/test-service/internal/runtime/production_generation_validation.go`（`validProductionManagedCases` 的 ToolchainID 守卫）。
- Modify: `apps/test-service/internal/runtime/production_managed_selection_authority.go`（当前 index 的 ToolchainID 守卫；fresh equality 留 Task 2）。
- Test: `apps/test-service/internal/runtime/production_generation_metadata_test.go`。
- Test: `apps/test-service/internal/runtime/production_generation_pipeline_test.go`。
- Test: `apps/test-service/internal/runtime/production_generation_validation_test.go`。
- Test: `apps/test-service/internal/runtime/production_managed_selection_authority_test.go`。

**Interfaces:**
- Consumes: 上文 4 个既有入口和 existing compile/pipeline/validation/managed fixtures；不需要 Task 2 或真实 compiler 来证明词法边界。
- Produces: 私有 `validProductionToolchainID(string) bool`；测试 helper `productionToolchainIdentityTestIDs() []string`；`productionPipelineFixture(t *testing.T, ids ...string)` 与 `productionValidationFixture(t *testing.T, ids ...string)`（现有不带 ID 的调用仍保留默认合法 ID）。这些是 runtime/test 内部接口，无公开协议变更。

- [ ] **Step 1: 添加元数据行为测试，先不用新的生产谓词**

在 metadata test 中加 `encoding/json` import。保留原有 `productionCompileBindingFixture` 和全部安全反例；新增以下 helper 和测试。真实 clang-cl、clang、手动混合大小写 ID 是核心实际行为 RED，不能用缺失符号/编译失败替代。

```go
func productionToolchainIdentityTestIDs() []string {
    return []string{
        "clang-cl-6c3869c0ae502e45d43c7cdb0aaead95681ee493e9489b710cbd48a",
        "clang-" + strings.Repeat("a", 58), "Local.Clang_1",
        "local.clang_1", "x", strings.Repeat("x", 128), "._-",
        strings.Repeat("a", 64),
    }
}

func TestProductionCompileBindingPreservesToolchainIdentity(t *testing.T) {
    seen := map[string]string{}
    root, target := productionCompileBindingFixture(t)
    for _, id := range productionToolchainIdentityTestIDs() {
        t.Run(id, func(t *testing.T) {
            got, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, id)
            if err != nil { t.Fatal(err) }
            identity := struct {
                Version int `json:"version"`
                ToolchainID string `json:"toolchainId"`
                TargetID string `json:"targetId"`
                TargetName string `json:"targetName"`
                Configuration string `json:"configuration"`
                Source string `json:"source"`
                Header string `json:"header"`
                Arguments []string `json:"arguments"`
            }{1, id, target.ID, target.Name, target.Configuration,
                "src/choose.c", "include/choose.h", []string{"-std=c17", "-DFEATURE=1", "-Iinclude"}}
            encoded, err := json.Marshal(identity)
            if err != nil { t.Fatal(err) }
            want := productionBytesDigest(append([]byte("production-compile-snapshot-v1\x00"), encoded...))
            if got.compileSnapshotDigest != want || !validProductionDigest(want) {
                t.Fatalf("ID was not bound verbatim: got=%s want=%s", got.compileSnapshotDigest, want)
            }
            if prior, ok := seen[want]; ok && prior != id { t.Fatalf("different IDs collide: %q %q", prior, id) }
            seen[want] = id
        })
    }
}

func TestProductionCompileBindingRejectsMalformedToolchainIdentity(t *testing.T) {
    for _, id := range []string{"", strings.Repeat("x", 129), " leading", "trailing ", "a\tb", "a\nb", "a\x00b", "a/b", "a\\b", "工具链", "aé"} {
        root, target := productionCompileBindingFixture(t)
        if _, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, id); err == nil {
            t.Fatalf("malformed ID accepted: %q", id)
        }
    }
}

func TestProductionCompileBindingKeepsTargetAndProfileDigestsStrict(t *testing.T) {
    for _, bad := range []string{"clang-" + strings.Repeat("a", 58), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
        for _, field := range []string{"target", "profile"} {
            root, target := productionCompileBindingFixture(t)
            if field == "target" { target.ID = bad } else { target.ProfileID = bad }
            if _, err := resolveProductionCompileBinding(root, "src/choose.c", []cmake.Target{target}, "Local.Clang_1"); err == nil {
                t.Fatalf("bad %s digest accepted: %q", field, bad)
            }
        }
    }
}
```

- [ ] **Step 2: 给现有 pipeline fixture 加可选原样 ID，并验证渲染与 SHA 隔离**

签名改为 `func productionPipelineFixture(t *testing.T, ids ...string) (*productionGenerationPipeline, generationTarget, *pipelineAnalyzerFixture, *pipelineOracleFixture)`；在既有 `target.request` 初始化之前插入以下完整逻辑。不要将 compile digest 改成 ID；这是单元边界 fixture，不是实际 coordinator/coverage authority。

```go
if len(ids) > 1 { t.Fatal("at most one fixture toolchain ID") }
if len(ids) == 1 { target.toolchainID = ids[0] }
```

在同一 test 文件新增以下两项，继续保留全部原有函数/file scope、预算、无 Mock 及 linkage 测试：

```go
func TestProductionPipelineAcceptsExactToolchainIdentity(t *testing.T) {
    for _, id := range productionToolchainIdentityTestIDs() {
        pipeline, target, analyzer, _ := productionPipelineFixture(t, id)
        target.managed, target.sourceRelativePath = true, "src/classify.cpp"
        target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
        result, err := pipeline.Generate(context.Background(), target)
        if err != nil { t.Fatalf("%q: %v", id, err) }
        source, ok := exactGeneratedSource(result.editSet.Files)
        if !ok || target.toolchainID != id || analyzer.calls != 1 { t.Fatalf("ID/render boundary failed for %q", id) }
        document, err := managedtest.ParseDocument(source, int64(len(source)), 4096)
        if err != nil || len(document.Blocks) == 0 { t.Fatalf("managed render failed: %v", err) }
    }
}

func TestProductionPipelineRejectsMalformedToolchainIdentityBeforeAnalysis(t *testing.T) {
    for _, id := range []string{"", strings.Repeat("x", 129), " leading", "trailing ", "a\tb", "a\nb", "a\x00b", "a/b", "a\\b", "工具链", "aé"} {
        pipeline, target, analyzer, _ := productionPipelineFixture(t, id)
        if _, err := pipeline.Generate(context.Background(), target); err == nil || analyzer.calls != 0 {
            t.Fatalf("malformed ID accepted/analyzed: %q err=%v calls=%d", id, err, analyzer.calls)
        }
    }
}

func TestProductionPipelineKeepsRealDigestsStrict(t *testing.T) {
    fields := map[string]func(*generationTarget, string){
        "workspace": func(x *generationTarget, v string) { x.workspaceGeneration = v; x.request.WorkspaceGeneration = v },
        "source": func(x *generationTarget, v string) { x.sourceDigest = v; x.request.SourceDigest = v; x.analysis.SourceDigest = v },
        "compile": func(x *generationTarget, v string) { x.compileSnapshotDigest = v; x.request.CompileSnapshotDigest = v; x.analysis.CompileSnapshotDigest = v; x.gap.CompileSnapshot = v },
        "cmake": func(x *generationTarget, v string) { x.cmakeTargetDigest = v; x.request.CMakeTargetDigest = v },
        "profile": func(x *generationTarget, v string) { x.buildProfileID = v },
        "test target": func(x *generationTarget, v string) { x.testTargetID = v },
        "framework": func(x *generationTarget, v string) { x.frameworkDigest = v; x.request.FrameworkBundleDigest = v },
        "analyzer": func(x *generationTarget, v string) { x.analyzerBundleDigest = v; x.request.AnalyzerBundleDigest = v },
    }
    for name, mutate := range fields {
        for _, bad := range []string{"clang-" + strings.Repeat("a", 58), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
            pipeline, target, analyzer, _ := productionPipelineFixture(t, "Local.Clang_1")
            mutate(&target, bad)
            if _, err := pipeline.Generate(context.Background(), target); err == nil || analyzer.calls != 0 {
                t.Fatalf("bad %s digest accepted/analyzed: %q err=%v calls=%d", name, bad, err, analyzer.calls)
            }
        }
    }
}
```

- [ ] **Step 3: 验证受管证据的原样保存/加载及真 SHA 守卫**

validation test 的 fixture 签名改为 `func productionValidationFixture(t *testing.T, ids ...string) (*productionGenerationValidation, testgendomain.Run, generationTarget, productionPipelineResult, *productionValidationAuthorityFixture, *productionGenerationArtifactFixture)`，仅将其第一处 `productionPipelineFixture(t)` 改为 `productionPipelineFixture(t, ids...)`；其他所有既有行为和返回值不改。新增 durable 测试，managed pipeline 在生成前设置 ID（不能只改已生成的 persisted record）：

```go
func TestProductionValidationPreservesDurableToolchainIdentity(t *testing.T) {
    for _, id := range productionToolchainIdentityTestIDs()[:3] {
        adapter, run, _, _, _, _ := productionValidationFixture(t, id)
        pipeline, target, _, _ := productionPipelineFixture(t, id)
        target.managed, target.sourceRelativePath = true, "src/classify.cpp"
        target.renderTarget.TestPath = "tests/generated/src/classify.cpp_test.cpp"
        result, err := pipeline.Generate(context.Background(), target)
        if err != nil { t.Fatal(err) }
        run.Request = target.request
        validated, err := adapter.Validate(context.Background(), run, target, result)
        if err != nil || len(validated.Candidates) != 1 { t.Fatalf("validate: %v", err) }
        for _, artifact := range validated.Artifacts {
            run.ArtifactDigests = append(run.ArtifactDigests, testgendomain.ArtifactRef{ID: artifact.ID, Digest: artifact.SHA256})
        }
        _, cases, receipt, digest, err := adapter.ManagedEvidence(context.Background(), run, validated.Candidates)
        if err != nil || len(cases) == 0 || !validProductionDigest(digest) || productionBytesDigest(receipt) != digest { t.Fatalf("evidence: %v", err) }
        for _, item := range cases { if item.ToolchainID != id { t.Fatalf("ID rewritten: %q", item.ToolchainID) } }
        adapter.mu.Lock()
        adapter.records = make(map[string]productionValidatedRecord)
        adapter.mu.Unlock()
        _, replayed, replayReceipt, replayDigest, err := adapter.ManagedEvidence(context.Background(), run, validated.Candidates)
        if err != nil || !reflect.DeepEqual(replayed, cases) || !bytes.Equal(receipt, replayReceipt) || digest != replayDigest { t.Fatalf("durable identity changed: %v", err) }
    }
}

func TestProductionManagedCaseKeepsSourceDigestStrict(t *testing.T) {
    _, _, set, source, target, pipeline := productionManagedFixture(t)
    target.toolchainID = "Local.Clang_1"
    cases, err := productionManagedCases(target, pipeline, source)
    if err != nil || len(cases) == 0 || !validProductionManagedCases(cases, set, source) { t.Fatalf("legal ID rejected: %v", err) }
    for _, bad := range []string{"clang-" + strings.Repeat("a", 58), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
        changed := append([]productionManagedCaseEvidence(nil), cases...)
        changed[0].SourceDigest = bad
        if validProductionManagedCases(changed, set, source) { t.Fatalf("bad managed source SHA accepted: %q", bad) }
    }
}

func TestProductionManagedCaseRejectsMalformedToolchainIdentity(t *testing.T) {
    _, _, set, source, target, pipeline := productionManagedFixture(t)
    cases, err := productionManagedCases(target, pipeline, source)
    if err != nil || len(cases) == 0 { t.Fatalf("managed cases missing: %v", err) }
    for _, id := range []string{"", strings.Repeat("x", 129), " leading", "trailing ", "a\tb", "a\nb", "a\x00b", "a/b", "a\\b", "工具链", "aé"} {
        changed := append([]productionManagedCaseEvidence(nil), cases...)
        changed[0].ToolchainID = id
        if validProductionManagedCases(changed, set, source) { t.Fatalf("malformed managed ID accepted: %q", id) }
    }
}

func TestProductionValidationKeepsArtifactAndReceiptDigestsStrict(t *testing.T) {
    adapter, run, target, pipeline, _, _ := productionValidationFixture(t, "Local.Clang_1")
    result, err := adapter.Validate(context.Background(), run, target, pipeline)
    if err != nil || len(result.Candidates) != 1 { t.Fatalf("validate: %v", err) }
    record, ok := adapter.cachedRecord(run.ID)
    if !ok || !validProductionReceipts(record.receipts) { t.Fatal("valid receipt missing") }
    for _, bad := range []string{"clang-" + strings.Repeat("a", 58), strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), strings.Repeat("a", 65)} {
        for _, field := range []string{"source artifact", "code"} {
            candidate := testgendomain.CloneCandidate(result.Candidates[0])
            if field == "source artifact" { candidate.StagedSourceArtifact.Digest = bad } else { candidate.CodeDigest = bad }
            if testgendomain.ValidateCandidate(candidate) == nil { t.Fatalf("bad %s SHA accepted: %q", field, bad) }
        }
        for _, field := range []string{"receipt", "output", "coverage"} {
            receipts := append([]testgenvalidate.StageReceipt(nil), record.receipts...)
            switch field { case "receipt": receipts[0].Digest = bad; case "output": receipts[0].OutputDigest = bad; case "coverage": receipts[0].CoverageDigest = bad }
            if validProductionReceipts(receipts) { t.Fatalf("bad %s SHA accepted: %q", field, bad) }
        }
    }
}
```

- [ ] **Step 4: 参数化现有 selection 正例，证明 ID 原样进入确认上下文**

将 `TestProductionManagedSelectionAuthorityResolvesCurrentFileAndFunctionCoverage` 替换为下列完整参数化测试，保留真实临时源文件、metric、snapshot digest 与完整 `wantCoverage` 断言，不添加 source/layout/实例豁免。

```go
func TestProductionManagedSelectionAuthorityResolvesCurrentFileAndFunctionCoverage(t *testing.T) {
    for _, id := range productionToolchainIdentityTestIDs()[:3] {
        t.Run(id, func(t *testing.T) {
            run, index, _, _, target, _ := productionManagedFixture(t)
            index.ToolchainID, target.toolchainID = id, id
            metric := coveragedomain.Summary{
                Functions: coveragedomain.Metric{Covered: 0, Total: 1},
                Lines: coveragedomain.Metric{Covered: 2, Total: 4},
                Branches: coveragedomain.Metric{Covered: 1, Total: 2},
            }
            index.Project.Summary, index.Files[0].Summary, index.Files[0].Functions[0].Summary = metric, metric, metric
            rootPath := t.TempDir()
            if err := os.MkdirAll(filepath.Join(rootPath, "src"), 0700); err != nil { t.Fatal(err) }
            if err := os.WriteFile(filepath.Join(rootPath, "src", "classify.cpp"), []byte("int classify(int value) { return value; }\n"), 0600); err != nil { t.Fatal(err) }
            root, err := workspace.OpenRoot(rootPath)
            if err != nil { t.Fatal(err) }
            authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
                root: root, store: productionManagedSelectionStoreFixture{run: run},
                resolver: productionManagedSelectionResolverFixture{target: target}, current: productionManagedIndexFixture{index: index},
            })
            if err != nil { t.Fatal(err) }
            got, err := authority.Resolve(context.Background(), run.ID)
            if err != nil { t.Fatal(err) }
            wantSource, err := testgenvalidate.WorkspaceSnapshotDigest(root.NativePath)
            if err != nil { t.Fatal(err) }
            wantCoverage := testgenvalidate.SelectedCoverage{
                Project: metric,
                Files: []testgenvalidate.SelectedFileCoverage{{ID: index.Files[0].ID, Summary: metric}},
                Functions: []testgenvalidate.SelectedFunctionCoverage{{ID: index.Files[0].Functions[0].ID, FileID: index.Files[0].ID, Summary: metric}},
            }
            if got.SnapshotDigest != run.Record.SnapshotDigest || got.ToolchainID != id || got.SourceDigest != wantSource ||
                !reflect.DeepEqual(got.Baseline, wantCoverage) || len(got.PreviousReceipt) != 0 { t.Fatalf("context=%+v want=%+v", got, wantCoverage) }
        })
    }
}
```

无需更改 run.Request/Record：ToolchainID 不是 request 新字段，原请求内容不变。该 fixture 是确认上下文边界正例；真实当前实例/profile 授权在 Task 2 测试。

- [ ] **Step 5: 运行一次 Task 1 行为 RED，完整记录退出码和失败归属**

在本计划工作区使用现有 Go runtime；环境只对当前进程生效：

```powershell
$env:PATH = 'D:/Program Files/Go/bin;' + $env:PATH
go test ./apps/test-service/internal/runtime -run '^TestProductionCompileBinding|^TestProductionPipeline(AcceptsExactToolchainIdentity|RejectsMalformedToolchainIdentityBeforeAnalysis|KeepsRealDigestsStrict)$|^TestProductionValidation(PreservesDurableToolchainIdentity|KeepsArtifactAndReceiptDigestsStrict)$|^TestProductionManagedCase|^TestProductionManagedSelectionAuthorityResolvesCurrentFileAndFunctionCoverage$' -count=1 -timeout=2m -v
if ($LASTEXITCODE -eq 0) { throw 'Expected actual ID-contract behavior RED before production correction' }
```

预期合法 prefixed/manual ID 在 compile/pipeline/managed 边界被拒绝；坏 SHA 测试可先通过。编译/环境失败不是有效行为 RED，先修测试本身；不能开始第四次完整 native 来取得 RED。

- [ ] **Step 6: 实施最小四处词法修正**

在 pipeline.go 加以下私有函数，现有 `validProductionDigest` / `validProductionObjectID` 原样保留：

```go
func validProductionToolchainID(value string) bool {
    if len(value) == 0 || len(value) > 128 { return false }
    for _, character := range value {
        if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
            character >= '0' && character <= '9' || character == '.' || character == '_' || character == '-' {
            continue
        }
        return false
    }
    return true
}
```

只替换这 4 个表达式，不能批量替换所有 digest 校验：

```go
// production_generation_metadata.go: resolveProductionCompileBinding
!validProductionToolchainID(toolchainID)
// production_generation_pipeline.go: generationTarget.valid
!validProductionToolchainID(target.toolchainID)
// production_generation_validation.go: validProductionManagedCases
!validProductionToolchainID(item.ToolchainID)
// production_managed_selection_authority.go: resolveBinding
!validProductionToolchainID(index.ToolchainID)
```

不改 canonical identity 结构、ID 生产器、任何真实 SHA guard 或 build authority。Task 1 的合法 ID 校验尚不足以准入原生验收，必须等待 Task 2。

- [ ] **Step 7: 同一门槛 GREEN，再补现有相关回归与格式检查**

仅对 Task 1 的 8 个精确 Go 文件 `gofmt -w`，再运行 Step 5 的同一命令，要求 exit 0/无跳过。随后一次运行已有 compile/pipeline/validation/selection 回归：

```powershell
go test ./apps/test-service/internal/runtime -run '^TestResolveProductionCompileBinding|^TestProductionGenerationPipeline|^TestProductionGenerationValidation|^TestProductionValidation|^TestProductionManagedCase|^TestProductionManagedSelectionAuthority' -count=1 -timeout=2m -v
if ($LASTEXITCODE -ne 0) { throw 'Task 1 regression failed' }
git diff --check
if ($LASTEXITCODE -ne 0) { throw 'Task 1 diff check failed' }
git diff --name-only
```

完整输出保存本任务 report：行为 RED/GREEN、真实摘要反例、已有 artifact corruption/reload 结果、实际退出码/时间/skips、受保护文件哈希不变、准确源范围。没有 native/race/Linux PASS 声明。

- [ ] **Step 8: 精确提交与独立任务审查**

```powershell
git add -- apps/test-service/internal/runtime/production_generation_pipeline.go apps/test-service/internal/runtime/production_generation_metadata.go apps/test-service/internal/runtime/production_generation_validation.go apps/test-service/internal/runtime/production_managed_selection_authority.go apps/test-service/internal/runtime/production_generation_metadata_test.go apps/test-service/internal/runtime/production_generation_pipeline_test.go apps/test-service/internal/runtime/production_generation_validation_test.go apps/test-service/internal/runtime/production_managed_selection_authority_test.go
git diff --cached --check
if ($LASTEXITCODE -ne 0) { throw 'Task 1 staged check failed' }
git diff --cached --name-only
git commit -m 'fix: distinguish generation toolchain IDs from digests'
if ($LASTEXITCODE -ne 0) { throw 'Task 1 commit failed' }
```

提交前确认 index **只有这 8 个路径**，无原 W4 源/证据/诊断。审查涵盖本任务 BASE→HEAD、全局约束、全部新增/现有正反例及词法与授权区分；处理具体发现后干净复审，才启动 Task 2。

## Task 2: 绑定明确已选实例，拒绝 stale 与确认时二次漂移

**Files:**
- Modify: `apps/test-service/internal/runtime/production_generation_resolver.go`（`resolveContext`，在 compile binding 前）。
- Modify: `apps/test-service/internal/runtime/production_managed_selection_authority.go`（fresh index equality）。
- Test: `apps/test-service/internal/runtime/production_generation_resolver_test.go`。
- Test: `apps/test-service/internal/runtime/production_managed_selection_authority_test.go`。
- Test: `apps/test-service/internal/runtime/production_generation_build_authority_test.go`。

**Interfaces:**
- Consumes: Task 1 `validProductionToolchainID(string) bool` 和可选 ID pipeline fixture；`discovery.Snapshot.Toolchains []toolchain.Instance`、`cmake.BuildProfile.ToolchainID string`、`productionResolverContext.index.ToolchainID string`。
- Produces: 原有 ResolveManagedStart/ResolveRequest/SelectionContext/ResolveProductionBuild 接口不变；resolver 精确选择失败返回 `testgendomain.ErrStaleSnapshot`，fresh selection 漂移返回 `errProductionManagedUnavailable`。

- [ ] **Step 1: 让既有 resolver 正例具有明确选择，加入可观测布局调用计数**

resolver test 增加 `toolchain` import（现有内部包前缀相同）。在 `productionResolverFixture` 中既有 `profile` 初始化之后、`authority` 初始化之前插入以下代码；profile 必须明确绑定，snapshot 必须有当前实例，index 使用同值，不添加类型/项目名例外：

```go
selectedID := productionToolchainIdentityTestIDs()[0]
profile.ToolchainID, profile.Origin, profile.Generator = selectedID, "generated", "Ninja"
```

其 `productionResolverContext` 的 snapshot 与 index 分别使用：

```go
snapshot: discovery.Snapshot{Generation: generation, Toolchains: []toolchain.Instance{{ID: selectedID, Family: toolchain.FamilyClangCL}}},
ToolchainID: selectedID,
```

只为单元边界声明真实的 profile/实例关系，不声称该 descriptor 被真实 registry/probe 认证。保留其已有 generation、源码、selected file/functions、真正 compile metadata 与 layout 绑定。

为 existing `productionLayoutAuthorityFixture` 加 `calls *int`；在原方法体的最前面插入 `if fixture.calls != nil { (*fixture.calls)++ }`，其余原逻辑不变。不改 value receiver/既有 type assertion。计数只用于拒绝必须发生在布局/分析前的行为观察。

- [ ] **Step 2: 明确选择正例、完整拒绝矩阵和不可变请求漂移**

在 resolver test 增加 `errors` import，添加以下测试；保留原有 changed-source/missing-linkage/layout/scope 负例：

```go
func TestProductionResolverPreservesExplicitToolchainSelection(t *testing.T) {
    for _, id := range productionToolchainIdentityTestIDs()[:4] {
        resolver, authority, input, selected := productionResolverFixture(t)
        authority.context.profile.ToolchainID = id
        authority.context.index.ToolchainID = id
        authority.context.snapshot.Toolchains[0].ID = id
        if strings.HasPrefix(id, "clang-") { authority.context.snapshot.Toolchains[0].Family = toolchain.FamilyClang }
        target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
        if err != nil || target.toolchainID != id { t.Fatalf("selected ID=%q target=%q err=%v", id, target.toolchainID, err) }
        again, err := resolver.ResolveRequest(context.Background(), target.request)
        if err != nil || !reflect.DeepEqual(again, target) { t.Fatalf("re-resolution changed: %v", err) }
    }
}

func TestProductionResolverRejectsUnboundOrAmbiguousSelectionBeforeLayout(t *testing.T) {
    tests := map[string]func(*productionResolverContext){
        "missing instance": func(c *productionResolverContext) { c.snapshot.Toolchains = nil },
        "same family different ID": func(c *productionResolverContext) { c.snapshot.Toolchains[0].ID = "Other.Clang_1" },
        "duplicate identical instance": func(c *productionResolverContext) { c.snapshot.Toolchains = append(c.snapshot.Toolchains, c.snapshot.Toolchains[0]) },
        "profile mismatch": func(c *productionResolverContext) { c.profile.ToolchainID = "Other.Clang_1" },
        "empty generated profile": func(c *productionResolverContext) { c.profile.ToolchainID = ""; c.profile.Origin = "generated" },
        "empty preset profile": func(c *productionResolverContext) { c.profile.ToolchainID = ""; c.profile.Origin = "preset"; c.profile.ConfigurePreset = "debug" },
        "malformed selected ID": func(c *productionResolverContext) { c.profile.ToolchainID = "bad/id"; c.index.ToolchainID = "bad/id"; c.snapshot.Toolchains[0].ID = "bad/id" },
        "stale index generation": func(c *productionResolverContext) { c.index.WorkspaceGeneration = strings.Repeat("f", 64) },
        "stale profile project": func(c *productionResolverContext) { c.profile.ProjectID = "other" },
    }
    for name, mutate := range tests {
        t.Run(name, func(t *testing.T) {
            resolver, authority, input, selected := productionResolverFixture(t)
            calls := 0
            layout := resolver.config.layout.(productionLayoutAuthorityFixture)
            layout.calls = &calls
            resolver.config.layout = layout
            mutate(&authority.context)
            _, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
            if !errors.Is(err, testgendomain.ErrStaleSnapshot) || calls != 0 { t.Fatalf("selection reached layout: err=%v calls=%d", err, calls) }
        })
    }
}

func TestProductionResolverRejectsChangedValidSelectedIDForExistingRequest(t *testing.T) {
    resolver, authority, input, selected := productionResolverFixture(t)
    target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
    if err != nil { t.Fatal(err) }
    next := "Other.Clang_1"
    authority.context.profile.ToolchainID = next
    authority.context.index.ToolchainID = next
    authority.context.snapshot.Toolchains[0].ID = next
    if _, err := resolver.ResolveRequest(context.Background(), target.request); !errors.Is(err, testgendomain.ErrStaleSnapshot) { t.Fatalf("stale immutable request accepted: %v", err) }
}
```

- [ ] **Step 3: 用正式 resolver 加独立第二次 index 读取复现漂移**

selection authority test 增加 `strings`、`errors` imports；无需新增 production API。以下测试让 resolver 证明 A/B 都单独属于当前 discovery，成功解析 A 后再读取 B；它不是旧的仅空字符串反例，也不是回传 ID 的 resolver fixture：

```go
func TestProductionManagedSelectionRejectsSecondReadIdentityDrift(t *testing.T) {
    resolver, contextAuthority, input, selected := productionResolverFixture(t)
    secondID := "Other.Clang_1"
    contextAuthority.context.snapshot.Toolchains = append(contextAuthority.context.snapshot.Toolchains, toolchain.Instance{ID: secondID, Family: toolchain.FamilyClangCL})
    target, err := resolver.ResolveManagedStart(context.Background(), strings.Repeat("9", 64), input, selected)
    if err != nil { t.Fatal(err) }
    run, _, _, _, _, _ := productionManagedFixture(t)
    run.Request = target.request
    run.Record = testgendomain.NewGenerationRecord(run.Request)
    run.Record.MinimizedCaseIDs = []string{run.ID}
    if err := testgendomain.ValidateRun(run); err != nil { t.Fatalf("invalid declared run fixture: %v", err) }
    current := contextAuthority.context.index
    current.Files = append([]coveragedetail.File(nil), current.Files...)
    for i := range current.Files { current.Files[i].Functions = append([]coveragedetail.Function(nil), current.Files[i].Functions...) }
    metric := coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 2}, Lines: coveragedomain.Metric{Total: 2}}
    current.Project.Summary, current.Files[0].Summary = metric, metric
    for i := range current.Files[0].Functions {
        current.Files[0].Functions[i].Summary = coveragedomain.Summary{Functions: coveragedomain.Metric{Total: 1}, Lines: coveragedomain.Metric{Total: 1}}
    }
    authority, err := newProductionManagedSelectionAuthority(productionManagedSelectionAuthorityConfig{
        root: resolver.config.root, store: productionManagedSelectionStoreFixture{run: run},
        resolver: resolver, current: productionManagedIndexFixture{index: current},
    })
    if err != nil { t.Fatal(err) }
    got, err := authority.Resolve(context.Background(), run.ID)
    if err != nil || got.ToolchainID != target.toolchainID { t.Fatalf("matching second read failed: %v", err) }
    current.ToolchainID = secondID
    authority.config.current = productionManagedIndexFixture{index: current}
    _, err = authority.Resolve(context.Background(), run.ID)
    if !errors.Is(err, errProductionManagedUnavailable) { t.Fatalf("different valid second-read ID accepted: %v", err) }
}
```

此 test 还需 `toolchain` import。独立复制 Files/Functions 保证第二次读取的 metrics 不污染 resolver 原 index；先匹配成功再漂移，不能将 fixture 本身失败当作拒绝证明。

- [ ] **Step 4: 延续实际 build authority 的精确请求及释放单元边界**

将现有 build authority 成功测试替换为下列完整参数化代码，保留 snapshot、request.ToolchainID、targets/jobs/timeout/idempotency 与 `prepared.releases == 1` 断言：

```go
func TestProductionBuildAuthorityResolvesExactCurrentBuild(t *testing.T) {
    for _, id := range productionToolchainIdentityTestIDs()[:3] {
        t.Run(id, func(t *testing.T) {
            _, target, _, _ := productionPipelineFixture(t, id)
            prepared := &productionPreparedBuildFixture{
                generation: target.workspaceGeneration,
                project: workspace.ProjectConfig{ID: target.projectID, Tests: workspace.ProjectTestsConfig{Containers: []workspace.TestContainerMapping{{CTestName: target.ctestName, Framework: workspace.FrameworkCppUTest}}}},
                profile: cmake.BuildProfile{ID: target.buildProfileID, ProjectID: target.projectID},
                toolchain: toolchain.Instance{ID: id},
                targets: []cmake.Target{
                    {ID: target.cmakeTargetDigest, Name: target.renderTarget.ProductionTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
                    {ID: target.testTargetID, Name: target.renderTarget.TestTarget, ProjectID: target.projectID, ProfileID: target.buildProfileID},
                },
            }
            preparer := &productionBuildPreparerFixture{prepared: prepared}
            authority, err := newProductionBuildAuthority(preparer)
            if err != nil { t.Fatal(err) }
            snapshot, err := authority.ResolveProductionBuild(context.Background(), target)
            if err != nil { t.Fatal(err) }
            if snapshot.generation != target.workspaceGeneration || snapshot.project.ID != target.projectID || snapshot.profile.ID != target.buildProfileID ||
                snapshot.toolchain.ID != id || len(snapshot.targets) != 2 || prepared.releases != 1 { t.Fatalf("snapshot=%+v releases=%d", snapshot, prepared.releases) }
            request := preparer.request
            if request.WorkspaceGeneration != target.workspaceGeneration || request.ProjectID != target.projectID || request.BuildProfileID != target.buildProfileID ||
                request.ToolchainID != id || !reflect.DeepEqual(request.TargetIDs, []string{target.cmakeTargetDigest, target.testTargetID}) ||
                request.Jobs != target.concurrency || request.Timeout != target.wallTime || len(request.IdempotencyKey) != 32 { t.Fatalf("request=%+v", request) }
        })
    }
}
```

现有失败表保留 workspace/toolchain/test-target/CTest 四例，追加下面 3 个具体 mutators；ToolchainID drift 使用合法原样字符串，不能仅使用非法 ID：

```go
{name: "profile", mutate: func(value *productionPreparedBuildFixture) { value.profile.ID = strings.Repeat("b", 64) }},
{name: "profile project", mutate: func(value *productionPreparedBuildFixture) { value.profile.ProjectID = "other" }},
{name: "production target", mutate: func(value *productionPreparedBuildFixture) { value.targets[0].Name = "other_core" }},
```

将其 `toolchain` mutator 的赋值改为 `value.toolchain.ID = "Other.Clang_1"`，其他逻辑及 releases=1 不改。测试 preparer fixture 加 `err error` 字段，return 改为 `return fixture.prepared, fixture.err`；添加 `errors` 和 `task` imports，测试 nil/unavailable/error，但不改变生产 build authority 或额外发明 preset configured-selection 证明：

```go
func TestProductionBuildAuthorityRejectsUnavailablePreparation(t *testing.T) {
    _, target, _, _ := productionPipelineFixture(t, "Local.Clang_1")
    if _, err := newProductionBuildAuthority(nil); err == nil { t.Fatal("nil preparer accepted") }
    var unavailable productionBuildAuthority
    if _, err := unavailable.ResolveProductionBuild(context.Background(), target); err == nil { t.Fatal("unavailable authority accepted") }
    for _, preparer := range []*productionBuildPreparerFixture{
        {}, {err: task.ErrStorageUnavailable}, {err: errors.New("prepare failed")},
    } {
        authority, err := newProductionBuildAuthority(preparer)
        if err != nil { t.Fatal(err) }
        if _, err := authority.ResolveProductionBuild(context.Background(), target); err == nil { t.Fatal("missing/failed preparation accepted") }
        if preparer.request.ToolchainID != target.toolchainID { t.Fatal("prepare request ID changed") }
    }
}
```

prepared=nil/error 没有获得可释放计划；已获得计划但 lineage 错误时原表必须断言释放一次。此处不以“preparer 同时返回非空计划和 error”引入设计外生命周期修复。

- [ ] **Step 5: 运行 Task 2 真实行为 RED**

```powershell
go test ./apps/test-service/internal/runtime -run '^TestProductionResolver|^TestProductionManagedSelectionRejectsSecondReadIdentityDrift$|^TestProductionBuildAuthority' -count=1 -timeout=2m -v
if ($LASTEXITCODE -eq 0) { throw 'Expected missing selection/equality behavior RED before Task 2 correction' }
```

预期 Task 1 后合法 ID 正例通过；缺失/重复/profile 不一致与空 profile 会被旧 resolver 接受或错误地进入 layout；二次合法 ID 漂移会被旧 selection 接受。严格断言与完整日志必须证明这些行为失败，不能把 test syntax/fixture setup failure记作 RED。

- [ ] **Step 6: 仅增加 resolver 守卫及 fresh equality**

在 resolver.resolveContext 的现有首个条件中，用下列子条件取代仅 `index.ToolchainID == ""` 的条件，其余已有条件和 `ErrStaleSnapshot` 返回保留：

```go
!validProductionToolchainID(contextValue.index.ToolchainID) ||
contextValue.profile.ToolchainID == "" ||
contextValue.profile.ToolchainID != contextValue.index.ToolchainID ||
```

在该条件返回之后、`resolveProductionCompileBinding` 之前新增：

```go
matches := 0
for _, instance := range contextValue.snapshot.Toolchains {
    if instance.ID == contextValue.index.ToolchainID { matches++ }
}
if matches != 1 { return generationTarget{}, testgendomain.ErrStaleSnapshot }
```

在 selection.resolveBinding 的第二次 index 检查追加以下子条件；保留 Task 1 ID 谓词和已有 project/report/generation 条件及错误映射：

```go
index.ToolchainID != target.toolchainID
```

不改 ordinary build/coverage/coordinator、不修改 stale 请求 equality、不归一化 ID、不猜测/填充空选择。正例为 explicit selected profile；不宣称空 ID preset 之前配置的 A 与新准备 B 有权威相等。

- [ ] **Step 7: GREEN、一次 runtime 包回归与保护核对**

仅 `gofmt -w` Task 2 的 5 个精确 Go 文件；运行 Step 5 同一命令，要求真实 GREEN/no skips，然后一次完整 runtime 包回归（不是整个 service/full verify）：

```powershell
go test ./apps/test-service/internal/runtime -count=1 -timeout=5m
if ($LASTEXITCODE -ne 0) { throw 'Runtime regression failed' }
git diff --check
if ($LASTEXITCODE -ne 0) { throw 'Task 2 diff check failed' }
git diff --name-only
```

保留旧 malformed/source/snapshot/linkage/预算/artifact/receipt/执行授权拒绝测试。记录真正 RED/GREEN 与旧正例补齐 explicit profile/discovery 的依据，核对 8 个受保护源哈希和证据不变。不得自行运行 native/race/global/remote。

- [ ] **Step 8: 精确提交、独立审查并交还 W4**

```powershell
git add -- apps/test-service/internal/runtime/production_generation_resolver.go apps/test-service/internal/runtime/production_managed_selection_authority.go apps/test-service/internal/runtime/production_generation_resolver_test.go apps/test-service/internal/runtime/production_managed_selection_authority_test.go apps/test-service/internal/runtime/production_generation_build_authority_test.go
git diff --cached --check
if ($LASTEXITCODE -ne 0) { throw 'Task 2 staged check failed' }
git diff --cached --name-only
git commit -m 'fix: bind generation to one explicitly selected toolchain'
if ($LASTEXITCODE -ne 0) { throw 'Task 2 commit failed' }
```

提交前确认只有这 5 个路径。独立审查覆盖本任务 BASE→HEAD 和与 Task 1 的完整接口；不能以之前 architecture-review 代替实现审查。存在不能验证项时由控制器明确归类：必要已运行行为证据、原 W4 留待真实执行或需进一步范围决定，不能默认放行。

两任务和必要最终修正批次审查均通过后，控制器记录修正源 SHA、所有未完成 W4 门槛与 protected hashes。结束本计划后**明确切回原 Windows CMake 计划**，原 W4 实施者才解除冻结；本修正的代理不可继续读写旧 SDD/擅自接管 W4。

## 原 W4 恢复交接：不是本计划第三个实施任务

以下只规定后继依赖和完成口径，不能在 Task 1/2 中消费。原 W4 已有的首份报告 UI 1/1、helper 52/52、两个 Windows CMake prerequisite RED/GREEN 保留，不重复为本 ID 修正证明。

1. 使用既有 prepared CMake/coverage/testgen/framework bundle，不更新 manifest/锁或重新选择“最新”工具链。构建当前 Go 服务及实际 probe；先一次 dirty native 验证 ID 修正能进入真正候选链路。失败保留独立尝试/日志，先诊断根因，不自动第四/第五轮盲跑。
2. 实际 CppUTest、Unity 均需要候选真实编译执行、分支覆盖率 `2/4 → 4/4` 且不回退、确认应用、用户源码实际维护、fresh service/client reopen 和正式 all-suite 覆盖率复跑。必须查看两份 receipt 及维护四阶段 source SHA/服务和报告身份，非静态fixture或 SQLite 捷径。
3. 新 build/CMake/SDK 生命周期只跑一条有界 race 门槛；原 W4 对独立清理/权限前置负责。示例精确范围为现有测试（若函数更名必须先查真实名字，不能跑零测试）：

```powershell
$env:PATH = 'C:/Program Files (x86)/mingw64/bin;D:/Program Files/Go/bin;' + $env:PATH
$env:CGO_ENABLED = '1'
$env:CC = 'gcc'
go test -race ./apps/test-service/internal/build ./apps/test-service/internal/cmake -run '^TestWindowsSDK|^TestConfigureFingerprintWindowsSDK|^TestCoordinatorPresetTargetsRecoverSelectedIdentityWithoutSDK$|^TestConfigureFingerprintWindowsSDKRootIdentity$|^TestFileAPILinkFragments|^TestFileAPILinkFragmentsDoNotAuthorizeSDKSourceRoots$' -count=1 -timeout=3m -v
if ($LASTEXITCODE -ne 0) { throw 'New SDK lifecycle race gate failed' }
```

要求 lease/root/directory/file aliases 及 reconstructed-coordinator preset 门槛实际执行且无 silent skip/Log+return；不额外单独重跑 unchanged taskstore race。后继规定的一次 `pnpm verify` 自身包含全局 Go race；必须如实记录该固有范围/重叠，不将其伪称为只跑 SDK。

4. 关键 native/race 通过后一次 `pnpm verify`；仅按真实错误修复必要范围，不修改测试/预算规避。原 W4 精确提交源码后，再以干净源 SHA 跑 CLI receipt identity 门槛：

```powershell
$env:GITHUB_SHA = (git rev-parse HEAD).Trim()
$env:UT_DEBUG_PROCESS_HOST_FAILURES = '1'
pnpm --filter @unit-test-ide/service-probe test:e2e:native:test-generation -- --platform win32
if ($LASTEXITCODE -ne 0) { throw 'Clean-source dual-framework native acceptance failed' }
```

这是后继原 W4 的 clean receipt gate，不是立即可执行指令。当前 helper 读取 GITHUB_SHA 必须是 40 字符小写 hex；dirty 运行赋值不能把该 SHA 变成实际被测试的干净源。将 tested source SHA、随后 evidence commit SHA、PR head/event/checkout/合成 merge SHA 分开记录。

5. 原 W4 BASE 仍为 `df55ef702c5d2af5edfb7e4e5da4619504e6b6ff`，任务审查覆盖其累计源码/文档/证据，而非仅 ID 修正。最终整个分支审查重新计算真实 merge-base，覆盖完整 PR #39；最高能力审查角色只读，不能重复 native/global gate。
6. 全部本地必要门槛/审查后才正常推送 GitHub/Gitee feature branch 并更新 PR #39。现有 pull_request synchronize 应触发 hosted；不手动 dispatch packaging 当作 matrix 替代，不合并/签名/发布。Linux 前置缺口、进程清理、Windows 2022 LLVM 解包、打包资格与 Phase 8 legal/sign 仍按实际证据处理，不能以本两项通过关闭。

## 实施计划自检与证据口径

- 设计第 1–3/4.2：Task 1 覆盖合法实例 ID、原样 canonical binding 与所有真 SHA/实体类型隔离。
- 设计第 4.1/4.3：Task 2 覆盖明确 profile、唯一 discovery、空 profile、不可变请求和第二次读取精确 equality；build prepared-ID 单元边界不宣称 preset 旧配置证明。
- 设计第 5–6/9：文件地图和 Global Constraints 精确限制 5 + 6 路径、Linux 例外、预算、安全及非发布边界。
- 设计第 7：两任务均有具体测试实现、同一 RED/GREEN 命令、已存在 helper 类型、精准提交和审查；未运行结果不填 PASS。
- 设计第 8：原 W4 交接列出双框架真实 receipt、维护/重开、race/global、干净源 SHA 与任务/全分支/hosted 的顺序，不重派 W1–W3 或提前推送。
- 本文是实施计划，不是已经应用的 Go 代码；其命令尚未运行。计划书写/链接/精确文档提交核对不能替代以上任一行为验收。

## 执行方式选择

1. 子代理驱动（推荐）：本计划两个任务各一位新的实施者，顺序执行并在任务间审查；控制器保留原 W4 现场，修正完成后明确切回原计划恢复 W4。
2. 当前会话直接实施：使用 executing-plans 逐任务实施及审查检查点，不并发修改源文件；相同测试、范围和 W4 交接门槛不减。

用户选择后开始实施；不因本文提交启动测试、代理或远端 workflow。
