# Unified Maintainable Test Cases Implementation Plan

**Status:** 2026-10-09 按用户要求暂停，归入第二版。Task 1 已完成本地实现与独立审查（53924e42、575208a5）；Task 2 部分代码保留，尚未提交和完成审查；Task 3–15 未实施。不得仅凭下方原执行说明恢复，须先取得新的明确请求。当前仅整理 Windows protected tool digest reuse 设计文档，不启动其实现。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有离线 C/C++ 链路上贯通统一可维护用例、手动向导、本地 JSON 输入和一键执行／文件函数覆盖率。

**Architecture:** 服务持久化稳定用例身份及不可变定义版本；三个输入入口共享类型校验、受管源码维护、进程监督与覆盖率采集。手动预期及已知断言 FAIL 使用独立准入凭证，不削弱自动补覆盖率门槛。Protocol 1.7 增量提供新入口，旧版 payload 和行为保持兼容。

**Tech Stack:** Go 1.26、现有 modernc SQLite、TypeScript 6、Node 24、pnpm 11.4.0、JSON Schema／现有 quicktype、Code-OSS 扩展、Unity、CppUTest、现有 CMake／CTest／覆盖率工具包。无新下载依赖。

## Global Constraints

- 设计依据：`docs/superpowers/specs/2026-10-09-unified-maintainable-test-cases-design.md`，用户已确认书面版本。
- 数据交换采用本地文件导入／导出，不提供网络上传／下载，不新增内网或云端服务器。
- 单文件最多 8 MiB，单批最多 1000 条；既有更严格的单值、类型、渲染和协议上限仍生效。
- 每项目最多一个活动会话，服务同时最多四个会话；每个会话最多暂存 8 MiB，总暂存不超过 32 MiB。
- 单消息（含包装后）不超过 256 KiB 或协商 wire 上限中的较小值。
- 自创建起最多存活 10 分钟，到期清除暂存并拒绝旧会话提交；这不是后续编译／运行任务的执行预算。
- 生成的测试仍是可维护、可实际编译执行的 C/C++ 源码；无 Mock/Stub；不自动把观察输出当作正确答案。
- 草稿、已接受源码、真实执行结果分别管理；源码修改不被重新生成覆盖。
- 手动／导入不要求覆盖率正增量，已知断言 FAIL 必须明确确认；崩溃、超时、构建错误和清理不确定性不适用该确认。
- 行覆盖率不等于真正语句覆盖率，分支覆盖率不等于 MC/DC；本批不新增或改名这两项指标。
- 不新增 CSV／Excel／ZIP 适配器；完整报告、静态测试、项目环境管理、边界／随机与新类构造能力保留独立后续批次。
- 不授权推送、PR、合并、Release、签名、服务器安装或自动触发 hosted workflow。

---

## 0. 工作区、前置工作及验证约定

当前工作区是 `C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`，不是 `C:/codex_project/unitTest`。编写时 HEAD 为 `7efb8712`，分支为 `codex/phase10-production-generation`。执行入口先使用 using-git-worktrees 的现有 linked-worktree 检查；不另建或切换 checkout，不丢弃现有改动。

以下八个未完成源码路径不属于本计划的编辑范围：四个 Windows CMake fixture、`tools/service-probe/src/native-test-generation.ts`、`native-test-generation.test.ts`、`test-generation-e2e.test.ts`、`apps/code-oss-extension/test/coverage-first-report-native.test.mjs`。现有 W4／性能验收尚未闭合，独立性能书面设计仍需单独复核；本计划不替它们批准实施。最终新 native 验收前先收紧这些前置工作，不能用旧／超时证据宣称新功能通过。

所有 Run 命令在执行阶段才运行；本计划编写阶段没有运行测试。Go 命令从 `apps/test-service` 执行，JS 命令从仓库根执行。现有 runtime 路径可临时加入当前进程 PATH，不全局修改环境：

```powershell
$env:PATH = 'D:\Program Files\Go\bin;C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin;C:\Users\DELL\AppData\Local\Temp\utide-pnpm-11.4.0;' + $env:PATH
```

每个任务先新增明确行为断言并取得 RED，再最小实现并取得 GREEN。编译失败须归因于本任务缺失接口，不能把环境错误当行为 RED；不以筛选到零项或 SKIP 算通过。提交只 add 本任务路径，不用 `git add .`。代码步骤的契约／算法片段是规定实现方向，不是允许用固定假返回替代真实链路。

## 1. 文件职责与批次

| 批次 | 任务 | 独立交付 |
| --- | --- | --- |
| A | 1–4 | 统一定义、持久化、严格 JSON、受限传输；新写能力保持未广告 |
| B | 5–8 | 可信函数绑定、用户预期渲染、真实 FAIL 准入、维护／发布恢复 |
| C | 9–11 | Protocol 1.7 schema、服务路由、客户端及旧协议兼容 |
| D | 12–14 | 真实执行／覆盖率协调、向导／本地文件界面、生产接线 |
| E | 15 | 新路径 native 与扩展宿主验收、证据和操作文档 |

新领域代码放在 `internal/casedesign`，JSON／会话放在 `internal/caseexchange`。SQLite 代码仍放在 `internal/taskstore`，使用独立 `CaseDesignStore`，不修改旧 `Store.Get/List` 语义。用户断言扩展在 `testgenassert`；源码渲染在 `testgenrender`；准入及发布在 `testgenvalidate/testgenpublish`。新 glue 放在独立 `runtime/production_case_*.go` 和 `session/case_design_routes.go`，不把新模块全部塞入大文件。

## Task 1: 闭集定义、稳定身份与共同类型校验

**Files:**
- Create: `apps/test-service/internal/casedesign/model.go`, `identity.go`, `definition.go`, `definition_test.go`.
- Create: `apps/test-service/internal/casedesigntest/fixture.go`（供后续测试使用的固定类型化数据，不是产品 Mock）.
- Create: `apps/test-service/internal/testgenassert/typed_values.go`, `typed_values_test.go`.
- Read: `managedtest/identity.go`, `testgensolver/domain.go`, `testgenassert/assertions.go`.

**Interfaces — 本任务定义后续共用名称:**

```go
type Binding struct {
    ProjectID, SourceRelativePath, FileID, FunctionID, SymbolID string
    SourceDigest, WorkspaceGeneration, ToolchainID, TargetID string
    ProgramDigest, BuildConfigDigest, Framework string
}
type Expectation struct {
    Target testgenassert.TargetKind
    Name string
    Value testgensolver.Value
    Rule testgenassert.ComparisonRule
    Tolerance string
    Provenance string // "user" 或服务验证后的 "proof"；不是导入方可选权限
}
type Definition struct {
    SchemaVersion int
    CaseID, ScenarioID, Origin string
    Revision int64
    Enabled bool
    Binding Binding
    Inputs []testgensolver.Input
    Expectations []Expectation
    Digest string
}
type Record struct {
    Draft Definition
    AcceptedRevision int64
    AcceptedDigest, TestPath, BlockDigest, BindingStatus string
    PendingAcceptance bool
}
type Query struct { ProjectID, Cursor string; Limit int }
type Page struct { Items []Record; NextCursor, SnapshotDigest string }
type DraftChange struct { Definition Definition; ExpectedRevision int64 }
func Seal(Definition) (Definition, error)
func NewManual(Binding, string, []testgensolver.Input, []Expectation) (Definition, error)
// errors.New 固定内容；不可包含值或路径：
// ErrInvalid, ErrConflict, ErrUnavailable, ErrNotFound, ErrUncertain
```

输入值沿用既有 `testgensolver.Value`，不新造任意 JSON 值。`Origin` 是服务分配的创建来源，草稿编辑不允许改写；手动编辑已接受用例的动作另由服务审查决定准入方式，导入标签不决定权限。`Digest` 绑定身份、Binding、输入、预期、来源及启用意图；不含修订号、传输 cursor／时间戳／自身摘要。`Revision` 由存储分配，不信任文件中的版本号。

- [ ] **Step 1: 写身份与深拷贝的失败测试。** 在本包完整构造 binding，并覆盖输入修改、非法类型字段和伪造 CaseID。

```go
func TestCaseIdentitySurvivesInputEdit(t *testing.T) {
    b := Binding{ProjectID:"demo", SourceRelativePath:"src/math.c",
        FileID:strings.Repeat("a",32), FunctionID:strings.Repeat("b",32),
        SymbolID:strings.Repeat("c",64), SourceDigest:strings.Repeat("d",64),
        WorkspaceGeneration:strings.Repeat("e",64), ToolchainID:"clang-test",
        TargetID:strings.Repeat("f",64), ProgramDigest:strings.Repeat("1",64),
        BuildConfigDigest:strings.Repeat("2",64), Framework:"unity"}
    inputs := []testgensolver.Input{{Name:"x", Value:testgensolver.Value{Kind:testgenanalysis.TypeInteger,Integer:"0"}}}
    want := []Expectation{{Target:testgenassert.TargetReturn,
        Value:testgensolver.Value{Kind:testgenanalysis.TypeInteger,Integer:"1"},
        Rule:testgenassert.RuleEqual, Provenance:"user"}}
    first, err := NewManual(b, strings.Repeat("3",32), inputs, want)
    if err != nil { t.Fatal(err) }
    edited := first
    edited.Inputs = append([]testgensolver.Input(nil), first.Inputs...)
    edited.Inputs[0].Value.Integer = "2"
    edited, err = Seal(edited)
    if err != nil || edited.CaseID != first.CaseID || edited.Digest == first.Digest { t.Fatal("identity/content not separated") }
    if first.Inputs[0].Value.Integer != "0" { t.Fatal("caller mutation changed sealed value") }
}
```

- [ ] **Step 2: RED.** Run `go test ./internal/casedesign ./internal/testgenassert -run 'TestCaseIdentity|TestTypedValue' -count=1 -v`; 预期新接口缺失或新断言失败。
- [ ] **Step 3: 实现闭集和共享校验。** `typed_values.go` 导出原有严格规则，不更改 `Derive`：

```go
func ValidateTypedValue(v testgensolver.Value, typ testgenanalysis.Type) bool {
    return validValue(v, typ, 0)
}
func ValidateTypedRule(v testgensolver.Value, typ testgenanalysis.Type, rule ComparisonRule, tolerance string) bool {
    return validRule(Evidence{Expected:v, Rule:rule, Tolerance:tolerance}, typ)
}
// NewManual: scenario := "manual."+nonce；使用 managedtest.StableCaseID，
// 通过后 Seal；Seal 验证该 ID、闭集来源、摘要形状、深度/总量并深拷贝。
```

深拷贝不能只复制顶层 slice：递归复制 Pointee／Elements／Fields。拒绝同一 Kind 下无关字段（例如 integer 携带 String），沿用最大深度 4 和既有类型限制。测试fixture的完整函数如下，使用本计划的同名包导入；`Bound(t)` 在Task5新增，不提前引用不存在接口。

```go
func Definition(t testing.TB) casedesign.Definition {
    t.Helper()
    b:=casedesign.Binding{ProjectID:"demo",SourceRelativePath:"src/math.c",
        FileID:strings.Repeat("a",32),FunctionID:strings.Repeat("b",32),SymbolID:strings.Repeat("c",64),
        SourceDigest:strings.Repeat("d",64),WorkspaceGeneration:strings.Repeat("e",64),ToolchainID:"clang-test",
        TargetID:strings.Repeat("f",64),ProgramDigest:strings.Repeat("1",64),BuildConfigDigest:strings.Repeat("2",64),Framework:"unity"}
    d,err:=casedesign.NewManual(b,strings.Repeat("3",32),
        []testgensolver.Input{{Name:"x",Value:testgensolver.Value{Kind:testgenanalysis.TypeInteger,Integer:"0"}}},
        []casedesign.Expectation{{Target:testgenassert.TargetReturn,Value:testgensolver.Value{Kind:testgenanalysis.TypeInteger,Integer:"1"},Rule:testgenassert.RuleEqual,Provenance:"user"}})
    if err!=nil {t.Fatal(err)}
    return d
}
```
- [ ] **Step 4: GREEN 与原断言负向回归。** Run `go test ./internal/casedesign ./internal/testgenassert -count=1 -v`；所有新测试及旧证据拒绝测试通过。
- [ ] **Step 5: 小提交。** 显式 add 本任务创建的目录／typed_values 文件；commit `feat: define stable typed case definitions`。

## Task 2: 草稿 CAS、不可变版本与可选迁移

**Files:**
- Create: `internal/taskstore/migrations/022_case_definitions.sql`, `case_definitions.go`, `case_definitions_test.go`, `case_definitions_migration_test.go`（路径均相对 `apps/test-service`）.
- Modify: `internal/taskstore/sqlite.go`, `migrations.go` 的可选能力状态分支，不编辑 001–021 SQL。

**Interfaces:** Consumes Task 1 `Definition/Record/Query/Page/DraftChange`；Produces:

```go
type CaseDesignStore struct { store *Store }
func (*Store) CaseDesignStore() *CaseDesignStore
func (*CaseDesignStore) Ready() bool
func (*CaseDesignStore) SaveDrafts(context.Context, string, []casedesign.DraftChange) ([]casedesign.Record,error)
func (*CaseDesignStore) Get(context.Context, string, string) (casedesign.Record,error)
func (*CaseDesignStore) List(context.Context, casedesign.Query) (casedesign.Page,error)
```

第二参数 projectID 始终由可信活动项目解析；不以传输 owner 字符串代替项目校验。SaveDrafts 的 expected=0 是新建，其他值必须等于当前 revision。批次全部校验及 CAS 成功才提交；相同 digest 重放不增加版本。

- [ ] **Step 1: 写真实 SQLite 的失败测试。** 在本包复用实际 `openTestStore`，完整实现：

```go
func TestCaseDraftCASDoesNotChangeAcceptedRevision(t *testing.T) {
    s := openTestStore(t).CaseDesignStore()
    d := casedesigntest.Definition(t)
    rows, err := s.SaveDrafts(context.Background(), d.Binding.ProjectID,
        []casedesign.DraftChange{{Definition:d, ExpectedRevision:0}})
    if err != nil || len(rows)!=1 || rows[0].Draft.Revision!=1 || rows[0].AcceptedRevision!=0 { t.Fatalf("create: %v %v", rows,err) }
    d.Inputs[0].Value.Integer="2"
    d, err=casedesign.Seal(d); if err!=nil { t.Fatal(err) }
    _, err=s.SaveDrafts(context.Background(), d.Binding.ProjectID,
        []casedesign.DraftChange{{Definition:d, ExpectedRevision:9}})
    if !errors.Is(err,casedesign.ErrConflict) { t.Fatal("stale edit accepted") }
    got,err:=s.Get(context.Background(),d.Binding.ProjectID,d.CaseID)
    if err!=nil || got.Draft.Revision!=1 || got.AcceptedRevision!=0 { t.Fatal("CAS changed record") }
}
```

增加 reopen、两条中第二条冲突导致全批零提交、摘要篡改、旧迁移失败仍可读旧任务的测试；测试用真实文件 DB，不假装内存 map 验证 SQLite。
- [ ] **Step 2: RED.** Run `go test ./internal/taskstore -run 'TestCaseDraft|TestCaseDefinitionMigration' -count=1 -v`。
- [ ] **Step 3: 实现独立版本表和原子 CAS。** SQL 最小骨架：

```sql
CREATE TABLE case_definition_heads (
 case_id TEXT PRIMARY KEY, project_id TEXT NOT NULL, origin TEXT NOT NULL,
 draft_revision INTEGER NOT NULL CHECK(draft_revision>=1), accepted_revision INTEGER,
 accepted_digest TEXT, test_path TEXT, block_digest TEXT, binding_status TEXT NOT NULL
);
CREATE TABLE case_definition_versions (
 case_id TEXT NOT NULL REFERENCES case_definition_heads(case_id), revision INTEGER NOT NULL,
 definition_digest TEXT NOT NULL, definition_json TEXT NOT NULL CHECK(json_valid(definition_json)),
 PRIMARY KEY(case_id,revision)
);
CREATE TABLE case_definition_meta(singleton INTEGER PRIMARY KEY CHECK(singleton=1), cursor_key BLOB NOT NULL CHECK(length(cursor_key)=32));
INSERT INTO case_definition_meta VALUES(1,randomblob(32));
```

额外 SQL CHECK 使用 Task 1 的闭集 ID／摘要／来源／状态；按读取原始 JSON 重新 Seal 和核对列绑定，不仅检查 JSON 合法。BeginTx→读取全部 heads→逐项核对 project/origin/revision→插入版本及 CAS heads→Commit；任一错误 Rollback。022 失败／校验和异常只关闭 caseDesignAvailable，不隐藏旧数据；023 后续迁移不能越过失败的 022。List cursor 绑定项目、冻结 heads 版本摘要、排序和页大小，limit 1–32，漂移拒绝。
- [ ] **Step 4: GREEN。** Run `go test ./internal/taskstore -run 'TestCaseDraft|TestCaseDefinitionMigration|TestMigration|TestManaged' -count=1 -v`；新增测试无 SKIP，旧注册表语义不变。
- [ ] **Step 5: 小提交。** 只提交 022、case_definitions 和两个迁移状态文件；commit `feat: persist case drafts with revision CAS`。

## Task 3: 严格 JSON 定义文件与幂等导入计划

**Files:** Create `internal/caseexchange/document.go`, `strict_json.go`, `document_test.go`。

**Interfaces:** Consumes Task 1/2；Produces:

```go
type PortableCase struct {
    CaseID string `json:"caseId"`
    ScenarioID string `json:"scenarioId"`
    SourceRelativePath string `json:"sourceRelativePath"`
    FunctionID string `json:"functionId"`
    SourceDigest string `json:"sourceDigest"`
    Inputs []testgensolver.Input `json:"inputs"`
    Expectations []PortableExpectation `json:"expectations"`
    Enabled bool `json:"enabled"`
    ExportedRevision int64 `json:"exportedRevision"`
    ExportedState string `json:"exportedState"` // draft / accepted，非权限
}
type PortableExpectation struct {
    Target testgenassert.TargetKind `json:"target"`
    Name string `json:"name"`
    Value testgensolver.Value `json:"value"`
    Rule testgenassert.ComparisonRule `json:"rule"`
    Tolerance string `json:"tolerance"`
}
type Document struct { Version int `json:"version"`; ProjectID string `json:"projectId"`; Cases []PortableCase `json:"cases"` }
type ImportResolution struct { CaseID string; ExpectedRevision int64; Action string }
func ParseDocument([]byte) (Document,error)
func EncodeDocument(Document) ([]byte,error)
func PlanImport(Document,map[string]casedesign.Record,map[string]casedesign.Binding,[]ImportResolution) ([]casedesign.DraftChange,error)
```

PortableExpectation 不提供 provenance字段，转入领域模型只能是user；导出proof只导出预期值并注明供用户复核，不携带证明权限。可信Binding由Task5重建，bindings的key固定 SourceRelativePath+NUL+FunctionID。PlanImport先要求文件ProjectID与全部Binding项目相同、SourceDigest与当前相同，再派生身份/类型校验；不执行或发布，不能将文件的SourceDigest当当前源码证明。

- [ ] **Step 1: 写严格重复键和总量测试。**

```go
func TestCaseDocumentRejectsDuplicateKeys(t *testing.T) {
    raw:=[]byte(`{"version":1,"version":1,"projectId":"demo","cases":[]}`)
    if _,err:=ParseDocument(raw); err==nil { t.Fatal("duplicate key accepted") }
}
func TestCaseDocumentRejectsByteLimit(t *testing.T) {
    if _,err:=ParseDocument(bytes.Repeat([]byte(" "),8*1024*1024+1)); err==nil { t.Fatal("over limit accepted") }
}
```

在同文件完整构造文档 roundtrip、1001 条、嵌套重复键、未知字段／版本、同 ID 不同内容和已接受声明伪造案例。
- [ ] **Step 2: RED。** Run `go test ./internal/caseexchange -run 'TestCaseDocument' -count=1 -v`。
- [ ] **Step 3: 实现双层严格解析。**

```go
// ParseDocument 算法顺序：
// 1 len<=8MiB、utf8.Valid；2 walkJSONTokens 检查每层对象重复键，深度<=32；
// 3 Decoder.DisallowUnknownFields 解码闭集类型，要求第二次 Decode 为 io.EOF；
// 4 Version==1、cases<=1000、ID唯一；5 输入值闭集和总量检查。
// PlanImport：已有 ID 与当前规范化内容相同 -> 不返回 DraftChange；
// 内容不同 -> 未给resolution时ErrConflict；keep-local不写；replace-draft须
// expectedRevision等于当前revision，再返回绑定该CAS的DraftChange；
// 新 ID -> 重新派生并核对，不使用 exportedRevision 作 expected revision。
```

`walkJSONTokens(dec *json.Decoder, depth int) error` 在本文件定义：对象 push `map[string]bool`，重复 key 立即 ErrInvalid；数组逐项递归；检查闭合 delimiter；不把 token 解析当权限校验。导出按稳定 CaseID 排序，完整 freeze 原版本后编码；修改分支不得静默取文件覆盖本地版本。

幂等比较使用当前定义投影出的PortableCase（不比较ExportedRevision/ExportedState和不可移植proof权限），同时仍验证可信Binding新鲜度。已验证auto预期导出再原样导入是no-op，不把旧proof降格user；值改变必须revision-bound replace-draft/手动审查，仍不能携带证明权限。生成DraftChange时保持existing Origin，不接受文件写Origin；新导入来源由服务分配import。
- [ ] **Step 4: GREEN。** Run `go test ./internal/caseexchange -run 'TestCaseDocument' -count=1 -v`；数据库／源码未被 codec 访问。
- [ ] **Step 5: 小提交。** 提交 caseexchange 的三个文件；commit `feat: validate portable offline case documents`。

## Task 4: 受限 owner 会话、分块及一致导出快照

**Files:** Create `internal/caseexchange/transfers.go`, `transfers_test.go`。

**Interfaces:** Consumes Task 3；Produces:

```go
type Transfer struct { ID, ProjectID, Digest string; ByteLength, CaseCount int; ExpiresAt time.Time }
type Chunk struct { Offset int; Data []byte; EOF bool }
type transferState struct { meta Transfer; owner,mode,phase string; data []byte; next int }
type TransferManager struct { mu sync.Mutex; sessions map[string]*transferState; reserved int; now func() time.Time }
func NewTransfers(func() time.Time) *TransferManager
func (*TransferManager) BeginImport(owner,project,digest string,bytes,count int) (Transfer,error)
func (*TransferManager) Append(owner,id string,offset int,data []byte) error
func (*TransferManager) Preview(owner,id string) (Document,error)
func (*TransferManager) BeginExport(owner string,doc Document) (Transfer,error)
func (*TransferManager) Read(owner,id string,offset,length int) (Chunk,error)
func (*TransferManager) Close(owner,id string) error
func (*TransferManager) CloseOwner(owner string)
func (*TransferManager) Commit(context.Context,string,string,string,func(context.Context,Document) error) error
// owner,id,previewDigest,atomicSave；committing阶段不可继续append/read/resolve。
```

offset顺序、完整digest、owner、项目和deadline都是使用前检查，不只在Begin检查。一个活动导出也占项目槽和总量；Preview不提交草稿，Close清零暂存，export使用一次冻结Document。Commit在锁内核对owner/id/摘要/时限后标记committing，释放锁再执行有deadline的atomicSave；完成后清理且保留有界commit状态，禁止Close与写事务竞争出虚假的零写结论。taskstore.SaveDrafts在提交前核对context及deadline，过期回滚；若commit结果不确定则ErrUncertain并以幂等键查询DB，不重试产生新修订。32MiB是暂存序列化数据的总预算，解析深度/项数/值大小另有限额，不声称Go总heap仅32MiB。

- [ ] **Step 1: 写时钟与跨 owner 失败测试。**

```go
func TestCaseTransferCannotCrossOwnerOrDeadline(t *testing.T) {
    now:=time.Date(2026,10,9,0,0,0,0,time.UTC)
    m:=NewTransfers(func()time.Time{return now})
    raw:=[]byte(`{"version":1,"projectId":"demo","cases":[]}`)
    sum:=sha256.Sum256(raw)
    tr,err:=m.BeginImport("owner-a","demo",hex.EncodeToString(sum[:]),len(raw),0)
    if err!=nil { t.Fatal(err) }
    if err=m.Append("owner-b",tr.ID,0,raw); err==nil { t.Fatal("owner bypass") }
    now=now.Add(10*time.Minute)
    if err=m.Append("owner-a",tr.ID,0,raw); err==nil { t.Fatal("expired session") }
}
```

增加每项目 1／全局 4／32 MiB、摘要不符、乱序 chunk、序列重复、关闭恢复容量、不同页版本混合拒绝测试；测试 clock 不 sleep。
- [ ] **Step 2: RED。** Run `go test ./internal/caseexchange -run 'TestCaseTransfer' -count=1 -v`。
- [ ] **Step 3: 实现预算租约。**

```go
const MaxDocumentBytes=8*1024*1024
const MaxCases=1000
const MaxTransferSessions=4
const MaxAggregateBytes=32*1024*1024
const MaxFrameBytes=256*1024
const RawChunkBytes=128*1024 // base64+envelope 后仍重新验证 MaxFrameBytes
const TransferLifetime=10*time.Minute
```

锁内先清理到期会话再预留budget，cryptographic ID由服务产生；预留在Close／错误／owner shutdown释放。暂存serialized副本不能绕过账本；不持mutex跨外部文件或编译操作。EOF只在完整长度、SHA和count一致时成立。导入提交在Task14的trusted dispatcher委托Commit/SaveDrafts，不在Append写源码；Task10只提供该dispatcher的路由/owner边界。
- [ ] **Step 4: GREEN 与竞态。** Run `go test ./internal/caseexchange -count=1 -v`，再仅对本包 `go test -race ./internal/caseexchange -count=1`。
- [ ] **Step 5: 小提交。** 提交 transfers 两文件；commit `feat: bound offline case transfer sessions`。

## Task 5: 可信函数目录、类型绑定与不支持原因

**Files:** Create `internal/casedesign/bound.go`, `bound_test.go`, `internal/runtime/production_case_catalog.go`, `production_case_catalog_test.go`。

**Interfaces:** Consumes Task 1；Produces:

```go
type BoundFunction struct { Binding Binding; Program testgenanalysis.Program; Function testgenanalysis.Function }
type CatalogItem struct { Binding Binding; Name,ClassName,Reason string; Supported bool; Parameters []testgenanalysis.Parameter; ReturnType testgenanalysis.Type }
type CatalogPage struct { Items []CatalogItem; NextCursor,SnapshotDigest string }
func ValidateBound(Definition,BoundFunction) error
func ValidateDraftBound(Definition,BoundFunction) error // zero expectations may remain draft
type CatalogResolver interface {
    List(context.Context,string,casedesign.Query) (casedesign.CatalogPage,error)
    Resolve(context.Context,string,casedesign.Binding) (casedesign.BoundFunction,error)
    VerifyCurrent(context.Context,string,casedesign.Binding) error
}
```

CatalogResolver 定义在 runtime 新文件，owner 由 session 产生。采用现有 workspace／CMake FileAPI、已验证 analyzer、函数语义身份及 coverage-detail 归属逻辑；不只按函数名匹配。旧 baseline／目录尚不存在时明确受阻并指向现有基线操作，不制造假函数 ID。构造器／析构器／不安全 receiver 与未知外部调用继续标不支持。

- [ ] **Step 1: 写输入范围及源码绑定测试。** 测试使用 `package casedesign_test`，避免fixture→领域包的测试循环导入。在fixture.go新增完整Bound函数：

```go
func Bound(t testing.TB) casedesign.BoundFunction {
    d:=Definition(t)
    integer:=testgenanalysis.Type{Kind:testgenanalysis.TypeInteger,Spelling:"int",BitWidth:32,Signed:true}
    fn:=testgenanalysis.Function{SymbolID:d.Binding.SymbolID,Name:"classify",ReturnType:integer,
        Parameters:[]testgenanalysis.Parameter{{Name:"x",Type:integer}},
        Excerpt:testgenanalysis.SourceExcerpt{Digest:d.Binding.SourceDigest},
        Effect:testgenanalysis.EffectLocalMemory,Decision:testgenanalysis.Decision{Kind:testgenanalysis.DecisionSupported,Reason:testgenanalysis.ReasonNone}}
    p:=testgenanalysis.Program{Version:testgenanalysis.IRVersion,Digest:d.Binding.ProgramDigest,Functions:[]testgenanalysis.Function{fn}}
    return casedesign.BoundFunction{Binding:d.Binding,Program:p,Function:fn}
}
```

```go
func TestCaseBoundRejectsOverflowAndSourceDrift(t *testing.T) {
    d:=casedesigntest.Definition(t); b:=casedesigntest.Bound(t)
    d.Inputs[0].Value.Integer="2147483648"
    if err:=casedesign.ValidateBound(d,b); err==nil { t.Fatal("int32 overflow") }
    d=casedesigntest.Definition(t); b.Binding.SourceDigest=strings.Repeat("9",64)
    if err:=casedesign.ValidateBound(d,b); err==nil { t.Fatal("source drift") }
}
```

另测同名不同签名、32-byte FunctionID／64-byte SymbolID 混用、不支持方法仍列出、跨项目 cursor、缺少基线和响应不能泄漏原始源码。
- [ ] **Step 2: RED。** Run `go test ./internal/casedesign ./internal/runtime -run 'TestCaseBound|TestCaseCatalog' -count=1 -v`。
- [ ] **Step 3: 实现绑定与类型校验。**

```go
func ValidateBound(d Definition,b BoundFunction) error {
    if len(d.Expectations)==0 { return ErrInvalid }
    return ValidateDraftBound(d,b)
}
func ValidateDraftBound(d Definition,b BoundFunction) error {
    if d.Binding!=b.Binding || b.Function.SymbolID!=d.Binding.SymbolID || b.Program.Digest!=d.Binding.ProgramDigest || b.Function.Decision.Kind!=testgenanalysis.DecisionSupported || b.Function.Decision.Reason!=testgenanalysis.ReasonNone || len(d.Inputs)!=len(b.Function.Parameters) { return ErrInvalid }
    for i,p:=range b.Function.Parameters {
        if d.Inputs[i].Name!=p.Name || !testgenassert.ValidateTypedValue(d.Inputs[i].Value,p.Type) { return ErrInvalid }
    }
    if len(d.Expectations)==0 { return nil }
    return validateBoundExpectations(d,b.Function)
}
```

同文件定义 `validateBoundExpectations(Definition,testgenanalysis.Function) error`：return 用真实 ReturnType，output 用已支持输出参数类型，拒绝其他／重复目标，要求1–128项，并调用 ValidateTypedValue/ValidateTypedRule。无预期只可存草稿。List上限32，cursor绑定来源；Resolve／VerifyCurrent核对全部当前来源，不引入跨请求未证明缓存。
- [ ] **Step 4: GREEN。** Run 上述 focused 命令及 `go test ./internal/casedesign -count=1 -v`。
- [ ] **Step 5: 小提交。** 提交 bound/catalog/testkit；commit `feat: bind case design to trusted function metadata`。

## Task 6: 用户预期断言与稳定受管源码渲染

**Files:** Create `internal/testgenassert/user_contract.go`, `user_contract_test.go`, `internal/testgenrender/designed_managed.go`, `designed_managed_test.go`；Modify `internal/testgenrender/managed.go`, `cpputest.go`, `unity.go` 的共同渲染小接口；不更新旧黄金文件。

**Interfaces:** Consumes Tasks1/5；Produces:

```go
// testgenassert 不反向 import casedesign：
type UserExpectation struct { Target TargetKind; Name string; Value testgensolver.Value; Rule ComparisonRule; Tolerance string }
type UserContract struct { DefinitionDigest,SourceDigest,InputDigest,SymbolID string; Expectations []UserExpectation }
type UserAssertionSet struct { assertions []Assertion; contractDigest,definitionDigest,programDigest,vectorID,symbolID string }
func DeriveUser(testgenanalysis.Program,testgensolver.InputVector,UserContract) (UserAssertionSet,error)
func (UserAssertionSet) Digest() string
// testgenrender：
type DesignedCase struct { Definition casedesign.Definition; Vector testgensolver.InputVector; Assertions testgenassert.UserAssertionSet }
type DesignedManagedInput struct { Base ManagedRenderInput; Cases []DesignedCase }
func RenderDesignedManagedFile(DesignedManagedInput) (managedtest.ManagedFile,error)
```

私有 UserAssertionSet 阻止普通调用者伪造 proof；不得暴露可修改底层 slice。新入口标注 user-specified expectation，不使用 KindVerified／characterization 蒙混。现有 Derive/RenderManagedFile 自动语义、字节和黄金测试不变。

- [ ] **Step 1: 写 provenance 和 stable-marker 测试。**

```go
func TestUserContractCannotActAsIndependentProof(t *testing.T) {
    b:=casedesigntest.Bound(t); d:=casedesigntest.Definition(t)
    v:=testgensolver.InputVector{ID:strings.Repeat("4",64),Inputs:d.Inputs}
    c:=testgenassert.UserContract{DefinitionDigest:d.Digest,SourceDigest:b.Function.Excerpt.Digest,InputDigest:testgenassert.InputDigest(v.Inputs),SymbolID:b.Function.SymbolID,
        Expectations:[]testgenassert.UserExpectation{{Target:testgenassert.TargetReturn,Value:d.Expectations[0].Value,Rule:testgenassert.RuleEqual}}}
    set,err:=testgenassert.DeriveUser(b.Program,v,c)
    if err!=nil || set.Digest()=="" { t.Fatal("manual contract rejected") }
    _,_,err=testgenassert.Derive(b.Program,v,testgenassert.Observation{SymbolID:c.SymbolID,CandidateID:v.ID})
    if err==nil { t.Fatal("manual contract widened proof path") }
}
```

user_contract_test.go使用 `package testgenassert_test`，否则引用casedesigntest会与casedesign→testgenassert形成循环。renderer测试直接构造DesignedCase，修改输入后标记仍同CaseID；检查旧黄金未变、用户标签及保留未受管文本。
- [ ] **Step 2: RED。** Run `go test ./internal/testgenassert ./internal/testgenrender -run 'TestUserContract|TestDesigned' -count=1 -v`。
- [ ] **Step 3: 实现独立 derive 和共同 body。**

```go
// DeriveUser 顺序：supported fn -> vector参数类型 -> 完整InputDigest ->
// SourceDigest==fn.Excerpt.Digest -> 预期目标/值/规则 -> 私有AssertionSet。
// 摘要域 "case-user-expectation-v1"；不是OracleProof；renderer再核对
// program/symbol/vector/contract，CaseID始终由Definition.ScenarioID派生。
```

复用现有 literal/comparison/AAA body。C 新设计源码使用现有 unityrunner 外部 driver 模式，避免第二个 main；CppUTest 保留可发现 TEST_GROUP/TEST。共同 helper 的 legacy 默认分支保持字节一致。增加 `UserAssertionSet.Check(programDigest,symbolID,vectorID,definitionDigest string) error` 和 `Assertions() []Assertion`（深拷贝）供 renderer 消费，均在 user_contract.go 定义。
- [ ] **Step 4: GREEN。** Run `go test ./internal/testgenassert ./internal/testgenrender ./internal/managedtest ./internal/unityrunner -count=1 -v`。
- [ ] **Step 5: 小提交。** 提交 user_contract/designed_managed/必要 renderer 接口；commit `feat: render maintained tests from user expectations`。

## Task 7: 真实 FAIL 分类与独立手动准入凭证

**Files:** Create `internal/casedesign/validation.go`, `internal/testgenvalidate/manual.go`, `manual_test.go`, `internal/runtime/production_case_validation.go`, `production_case_validation_test.go`。

**Interfaces:** Consumes Tasks5/6；Produces:

```go
type ItemResult struct { CaseID string; Revision int64; Status,ResultDigest string }
type CaseRevision struct { CaseID string; Revision int64; Digest string }
type ManualEvidence struct {
    DefinitionDigest,SnapshotDigest,PlanDigest,SelectedSourceDigest string
    ConfigureOK,BuildOK,DiscoveryComplete,RunComplete,CleanupProven bool
    Results []ItemResult
    Selected []CaseRevision
    UnexpectedRegression bool
}
type ManualReceipt struct { Domain,DefinitionDigest,SnapshotDigest,PlanDigest,SelectedSourceDigest,ResultsDigest,MAC string; KnownFail bool }
// testgenvalidate：
func IssueManualReceipt(casedesign.ManualEvidence,[]byte) (casedesign.ManualReceipt,error)
func VerifyManualReceipt(casedesign.ManualReceipt,casedesign.ManualEvidence,[]byte) error
// runtime，服务端配置，不能由IPC创建：
type CaseValidator interface {
    Validate(context.Context,string,casedesign.Record,[]testgenrender.StagedFile) (casedesign.ManualEvidence,casedesign.ManualReceipt,error)
}
```

结果闭集：passed/failed/build-error/crashed/timed-out/cancelled/incomplete。只有全部实际所选结果为 passed 或可归属 failed，且构建／发现／执行／清理完整，才可签凭证。非零 exit 不自动等于 failed。实际 CaseID 从已有 testrun.Interpreter／framework parser 取得，不从计划文件直接填“全部执行／通过”。

- [ ] **Step 1: 写 FAIL 和 timeout 区别的失败测试。**

```go
func TestManualReceiptAcceptsAssertionFailNotTimeout(t *testing.T) {
    key:=bytes.Repeat([]byte{7},32)
    e:=casedesign.ManualEvidence{DefinitionDigest:strings.Repeat("a",64),SnapshotDigest:strings.Repeat("b",64),PlanDigest:strings.Repeat("c",64),SelectedSourceDigest:strings.Repeat("d",64),ConfigureOK:true,BuildOK:true,DiscoveryComplete:true,RunComplete:true,CleanupProven:true,
        Results:[]casedesign.ItemResult{{CaseID:"utc_"+strings.Repeat("e",32),Revision:1,Status:"failed",ResultDigest:strings.Repeat("f",64)}},
        Selected:[]casedesign.CaseRevision{{CaseID:"utc_"+strings.Repeat("e",32),Revision:1,Digest:strings.Repeat("a",64)}}}
    r,err:=IssueManualReceipt(e,key)
    if err!=nil || !r.KnownFail { t.Fatal("assertion FAIL lost") }
    e.Results[0].Status="timed-out"
    if _,err=IssueManualReceipt(e,key); err==nil { t.Fatal("timeout accepted as FAIL") }
}
```

增加缺少实际 ID、重复结果、cleanup=false、已有套件新增失败、MAC／plan／source／revision 篡改、auto receipt 混用拒绝测试。
- [ ] **Step 2: RED。** Run `go test ./internal/testgenvalidate ./internal/runtime -run 'TestManualReceipt|TestCaseValidation' -count=1 -v`。
- [ ] **Step 3: 实现签发及生产 adapter。**

```go
// Issue：真实选择完全相等，按CaseID排序；ResultsDigest=SHA256(canonical results)；
// MAC=HMAC-SHA256(key,"case-manual-admission-v1\x00"+canonical payload)。
// Verify：重建全部绑定、恒时核对MAC；KnownFail来自实际failed，不来自请求bool。
```

生产adapter共用trusted stage planner/processcontrol、`testrun.PlanRun/NewInterpreter`、CppUTest／Unity parser；不调用require-all-pass的SelectedValidator后伪装成功。验证前后再次核对来源；清理未证明时ErrUncertain。保持draft和typed result，日志只写原因码。每项对应actual item result，Issue同时检查Selected与Results的身份/修订集合完全相等，绑定每个定义Digest。
- [ ] **Step 4: GREEN。** Run focused 命令及 `go test ./internal/testgenvalidate -count=1 -v`，旧自动门槛不变。
- [ ] **Step 5: 小提交。** 提交本任务文件；commit `feat: attest manual assertion failures without weakening auto gates`。

## Task 8: 三方维护、已知 FAIL 确认与发布恢复

**Files:** Create `internal/casedesign/publication.go`, `internal/taskstore/migrations/023_case_acceptances.sql`, `case_acceptances.go`, `case_acceptances_test.go`, `internal/testgenpublish/designed.go`, `designed_test.go`, `internal/runtime/production_case_publication.go`；Modify `testgenpublish/publisher.go`、`plan.go` 的共享 journal 小接口及 taskstore/migrations.go 的023可选分支。

**Interfaces:** Consumes Tasks2/6/7；Produces:

```go
type Review struct { ID,Digest,DefinitionDigest,SnapshotDigest,DiffDigest,ResultsDigest string; CaseID string; Revision int64; KnownFail bool }
type Confirm struct { ReviewID,ReviewDigest,DiffDigest,ResultsDigest string; AcceptKnownFail bool; Choices map[string]string }
type Acceptance struct { ID,ProjectID,CaseID string; Revision int64; DefinitionDigest,ReviewDigest,PublishedDigest,ResultsDigest,ReceiptMAC,TestPath,BlockDigest string }
// CaseDesignStore：
func (*CaseDesignStore) BeginAcceptance(context.Context,casedesign.Acceptance) error
func (*CaseDesignStore) MarkFileWritten(context.Context,string,string) error
func (*CaseDesignStore) CommitAcceptance(context.Context,string) error
func (*CaseDesignStore) Pending(context.Context) ([]casedesign.Acceptance,error)
// runtime：
type CasePublisher interface {
    Prepare(context.Context,string,casedesign.Record,[]testgenrender.StagedFile,casedesign.ManualEvidence,casedesign.ManualReceipt) (casedesign.Review,error)
    Apply(context.Context,string,casedesign.Confirm) (casedesign.Record,error)
    Recover(context.Context) error
}
// testgenpublish：
func ValidateDesignedConfirmation(casedesign.Review,casedesign.Confirm) error
type DesignedCandidate struct { Record casedesign.Record; Files []testgenrender.StagedFile; Evidence casedesign.ManualEvidence; Receipt casedesign.ManualReceipt; Review casedesign.Review }
type DesignedReceiptVerifier interface { CheckManual(context.Context,DesignedCandidate) error }
func (*Publisher) PlanDesigned(context.Context,DesignedCandidate,casedesign.Confirm) (Plan,error)
```

- [ ] **Step 1: 写精确 FAIL 确认和中断恢复测试。**

```go
func TestDesignedConfirmMustAcknowledgeExactKnownFail(t *testing.T) {
    r:=casedesign.Review{ID:strings.Repeat("a",32),Digest:strings.Repeat("b",64),DiffDigest:strings.Repeat("c",64),ResultsDigest:strings.Repeat("d",64),DefinitionDigest:strings.Repeat("f",64),SnapshotDigest:strings.Repeat("1",64),CaseID:"utc_"+strings.Repeat("2",32),Revision:1,KnownFail:true}
    c:=casedesign.Confirm{ReviewID:r.ID,ReviewDigest:r.Digest,DiffDigest:r.DiffDigest,ResultsDigest:r.ResultsDigest}
    if err:=ValidateDesignedConfirmation(r,c); err==nil { t.Fatal("silent FAIL acceptance") }
    c.AcceptKnownFail=true; c.ResultsDigest=strings.Repeat("e",64)
    if err:=ValidateDesignedConfirmation(r,c); err==nil { t.Fatal("different FAIL replay") }
    c.ResultsDigest=r.ResultsDigest
    if err:=ValidateDesignedConfirmation(r,c); err!=nil { t.Fatal("exact acknowledged FAIL rejected") }
}
```

再测文件已写但 DB CAS 中断、来源／draft 漂移、重复确认、手改块 zero-write、块外内容逐字节保留、恢复后 accepted pointer 与实际 source 一致。
- [ ] **Step 2: RED。** Run `go test ./internal/testgenpublish ./internal/taskstore -run 'TestDesigned|TestCaseAcceptance' -count=1 -v`。
- [ ] **Step 3: 实现持久 journal。**

```sql
CREATE TABLE case_pending_acceptances(acceptance_id TEXT PRIMARY KEY,case_id TEXT NOT NULL,revision INTEGER NOT NULL,acceptance_json TEXT NOT NULL CHECK(json_valid(acceptance_json)),phase TEXT NOT NULL CHECK(phase IN('prepared','file_written')));
CREATE UNIQUE INDEX case_pending_single ON case_pending_acceptances(case_id);
CREATE TABLE case_acceptance_commits(acceptance_id TEXT PRIMARY KEY,acceptance_digest TEXT NOT NULL,acceptance_json TEXT NOT NULL CHECK(json_valid(acceptance_json)));
CREATE TABLE case_design_reviews(review_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,owner_id TEXT NOT NULL,review_digest TEXT NOT NULL,review_json TEXT NOT NULL CHECK(json_valid(review_json)),payload_digest TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN('prepared','applied','expired')));
```

Prepare使用actual accepted/current/generated三方比较，冻结选择；review通过CaseDesignStore.SaveCaseReview(ctx,owner string,review casedesign.Review,payloadDigest string) error持久化，ReadCaseReview(ctx,owner,project,reviewID string)(casedesign.Review,error)逐次核对作用域；两方法定义在case_acceptances.go。冻结payload存于已有受限journal目录并校验digest，不放任意绝对路径到DB。Apply顺序为re-attest→pending→共享atomic file mechanics→核对实际文件→file_written→CAS accepted pointer→commit。任一中断pending阻止current/readiness。Recovery只能补交已确认且源码／凭证完全匹配的接受，不自动确认草稿。PlanDesigned需要产品工厂配置的DesignedReceiptVerifier；共享publisher只抽出私有已经验证后的写入／journal机械流程，不添加公开skipValidation bool。旧Accept/PlanManaged不能消费manual receipt。新journal类型带独立policy/domain，旧journal reader继续原校验恢复。owner断开使未确认review不可提交，已开始的journal按真实状态恢复，不能用断开覆盖已接受结果。
- [ ] **Step 4: GREEN。** Run `go test ./internal/testgenpublish ./internal/taskstore ./internal/managedtest -run 'TestDesigned|TestCaseAcceptance|TestManaged|TestReconcile' -count=1 -v`。
- [ ] **Step 5: 小提交。** 显式提交本任务文件；commit `feat: publish designed cases with recoverable acceptance journals`。

## Task 9: Protocol 1.7 闭集契约与生成模型

**Files:** Create `packages/protocol-schema/schema/v1.7/message.schema.json`, `capabilities.schema.json`, `case-design.schema.json`, `packages/protocol-schema/test/case-design-v17.test.mjs`；Modify schema package.json 的 exports/test、`tools/protocol-gen/generate.mjs`、`packages/protocol-models/src/index.ts`、`generated-contract.test.ts`；Generate `packages/protocol-models/src/generated/capabilities-v1-7.ts`, `case-design-v1-7.ts` 及 `apps/test-service/internal/protocolmodel/v1_7/{capabilities,casedesign}/generated.go`。不手改生成文件。

**Interfaces:** Consumes Tasks1–8；Produces 1.7 envelope、`caseDesignV1` capability、生成的闭集 DTO。所有新方法前缀 `caseDesign/`，以下表格同时是参数与响应契约。`Record/Page/Review/Confirm/Definition` 使用 Task1/8 形状的 lowerCamel wire DTO；服务器证明/MAC、原生 argv 和未限制源码不下发。

| 方法后缀 | 请求 payload（字段均明确，不接受额外字段） | 响应 payload |
| --- | --- | --- |
| catalog/list | projectId,cursor,limit(1–32) | CatalogPage |
| definitions/list | projectId,cursor,limit(1–32) | Page |
| definitions/get | projectId,caseId | Record |
| definitions/save | projectId,caseId(编辑时),functionId,expectedRevision,idempotencyKey,inputs,expectations,enabled | Record；新建身份由服务生成 |
| reviews/prepare | projectId,caseId,revision,definitionDigest | Review（显示摘要，不带完整源码） |
| reviews/readDiff | projectId,reviewId,offset,length(1–65536) | offset,dataBase64,eof,diffDigest；冻结 diff |
| reviews/apply | projectId,confirmation:Confirm | Record |
| transfers/beginImport | projectId,digest,byteLength,caseCount | Transfer |
| transfers/append | projectId,transferId,offset,dataBase64 | offset（下一个字节位置） |
| transfers/preview | projectId,transferId,cursor,limit(1–32) | items:ImportPreviewItem[],nextCursor,previewDigest |
| transfers/commit | projectId,transferId,previewDigest,resolutions:ImportResolution[] | savedCount,skippedCount；逐项由 definitions/list 查询 |
| transfers/beginExport | projectId,state(draft/accepted) | Transfer；一致项目快照 |
| transfers/read | projectId,transferId,offset,length(1–131072) | Chunk；base64 编码 |
| transfers/close | projectId,transferId | closed:true |
| runs/start | projectId,idempotencyKey | runId,snapshotDigest,state |
| runs/get | projectId,runId,cursor,limit(1–32) | RunPage（Task12） |
| runs/cancel | projectId,runId | cancellationRequested:true；不是 cleanup 已证明 |

类型补充在 `casedesign`/`caseexchange`，此处先定义纯类型，不引入执行依赖：

```go
// ImportResolution 已在Task3/caseexchange定义；Action仅keep-local / replace-draft。
type ImportPreviewItem struct { CaseID,State,Reason string; CurrentRevision int64 } // new / identical / conflict / blocked
type RunItem struct { CaseID string; Revision int64; DefinitionDigest,Status,ResultDigest,Reason string }
type RunPage struct { RunID,SnapshotDigest,State string; Complete,CleanupProven,AllPassed bool; Items []RunItem; NextCursor,CoverageRunID,CoverageState string; EnabledCount,ExecutedCount int }
```

expectations wire只包含target/name/value/rule/tolerance；客户端不能提交proof provenance。Value是基于Kind的封闭oneOf，递归深度和总量由decoder继续限制。Review choices仅accepted/current/generated；ResultsDigest和修订绑定，不能裸布尔忽略失败。导入resolutions总数最多1000，为始终遵守帧限，固定使用分页 `transfers/resolve {projectId,transferId,previewDigest,resolutions(最多32个)}`，返回resolutionsDigest；commit请求固定为projectId,transferId,previewDigest,resolutionsDigest，绝不传整批resolution。preview cursor绑定同一当前revision快照，resolve/commit漂移拒绝。

页大小32只是项数上限，还要按实际序列化字节预算截页并返回cursor；单项记录超过min(128KiB,协商frame预算扣除包装)时拒绝写入/导入，返回显式size-limit，不发送超限结果。API保存该实现上限为capability limits，UI与JSON导入一致显示；旧per-value/renderer更严上限优先。definitions/get只含受限typed定义/接受元数据，diff单独chunk，不能将整个源码/1000项结果塞入单message。

- [ ] **Step 1: 写 schema 行为 RED。** 在新 test 文件创建 AJV2020、注册 workspace/v1.2 和全部已有1.6 schema，再注册1.7。测试本体：

```javascript
test('v1.7 manual input does not accept proof privileges', () => {
  const validate = ajv.getSchema(caseSchema.$id + '#/$defs/SaveRequest');
  const input = {projectId:'demo', functionId:'b'.repeat(32), expectedRevision:0,
    idempotencyKey:'3'.repeat(32), inputs:[], expectations:[], enabled:true,
    oracleProof:{kind:'verified'}};
  assert.equal(validate(input), false);
});
```

文件顶层完整导入 node:test、node:assert/strict、Ajv2020、ajv-formats、JSON schema；`caseSchema` 是本任务 import，`ajv` 是上述注册实例。另测有效 save、未知 method、1001项、超长 chunk、错误版 envelope、cap=false 和旧1.6有效 fixture不变；样本不能为空用例假阳性。
- [ ] **Step 2: RED。** Run `node --test packages/protocol-schema/test/case-design-v17.test.mjs`，缺失 schema/契约导致明确失败。
- [ ] **Step 3: 实现 schema 和生成入口。** 1.7 envelope version固定1.7，旧方法仅payload引用既有1.6定义，不能引用含const1.6的整个旧envelope；不要修改旧schema。新handshake单独增加offer/negotiated1.7及本地capability。旧事件沿用既有1.6服务的task-event payload/projection时，在1.7 message/connection订阅入口明确允许该旧event版本并测试，不把1.7普通request/response误送入1.5分支；新case运行暂使用有界查询，不增加假event。capabilities增量true只表示完整ready。生成器只增两个新model输入及index exports，`pnpm generate:protocol`产出；旧生成文件不得无关漂移。新请求schema全部additionalProperties=false，integer安全范围、字符串/数组上限和每条result闭集状态；frame上限继续transport执行。
- [ ] **Step 4: GREEN。** Run `pnpm generate:protocol`、`pnpm --filter @unit-test-ide/protocol-schema test`、`pnpm --filter @unit-test-ide/protocol-models test`、`go test ./internal/protocolmodel/... -count=1`（最后命令从Go目录）。将新 schema test 加入默认 test script，避免只在 focused 命令执行。
- [ ] **Step 5: 小提交。** 显式 add schema/generator/本次生成文件及两个 package scripts；commit `feat: define versioned case design protocol contracts`。

## Task 10: 服务路由、owner 隔离与失败关闭

**Files:** Create `internal/session/case_design_routes.go`, `case_design_routes_test.go`, `internal/protocol/case_design.go`, `case_design_test.go`；Modify `session/session.go`、`protocol/envelope.go`、`server/service.go`、`server/service_test.go`。

**Interfaces:** Consumes Task9 DTO；Produces session 的 `CaseDesignBackend`，由 production runtime 实现，不把数据库开放给 IPC：

```go
type CaseDesignBackend interface {
    CaseDesignReady() bool
    HandleCaseDesign(context.Context,string,string,json.RawMessage) (json.RawMessage,error)
    CloseCaseDesignOwner(string)
}
func (s *Session) EnableCaseDesign(CaseDesignBackend) // factory only; nil is unavailable
// Handle 的 owner 使用 Session 的内部会话身份，method 必须来自上表闭集；
// 不允许客户端选择 owner。后端重验证活动 workspace/project/trust。
```

旧构造函数继续返回1.6及以下；在 server 新增 `NewServiceWithCaseDesign`，参数为 `NewServiceWithManagedDetails` 原参数加 `CaseDesignBackend`，旧构造函数委托并传 nil。session 先构造旧 provider 再 EnableCaseDesign；只有全部旧 readiness 及 CaseDesignReady 为真且对方 offer1.7 时可协商1.7。新请求 decoder 使用生成 DTO +严格未知字段/重复键校验；函数专门返回 typed错误码，日志不记录输入值。

- [ ] **Step 1: 写能力门槛纯函数 RED 和 socket owner 测试。** 新增私有 `canOfferCaseDesign(details,managed,design bool) bool`，完整表驱动：

```go
func TestCaseDesignCapabilityNeedsWholeComposition(t *testing.T) {
    for mask:=0;mask<8;mask++ {
        got:=canOfferCaseDesign(mask&1!=0,mask&2!=0,mask&4!=0)
        if got!=(mask==7) { t.Fatalf("mask=%d advertised=%v",mask,got) }
    }
}
```

socket 测试使用既有 server service_test 的 net.Pipe/exchange fixture，两个鉴权连接：A beginImport 后 B append/read/close 全拒绝；A断开清理会话；未鉴权、旧版、untrusted、错误项目和额外字段全零写。测试 adapter 只做 protocol unit fixture，不替代 native 产品执行证据。
- [ ] **Step 2: RED。** Run `go test ./internal/protocol ./internal/session ./internal/server -run 'TestCaseDesign' -count=1 -v`。
- [ ] **Step 3: 实现闭集 dispatcher 与 owner 生命周期。** 新增 Version17/SupportedVersion/limits 的明确分支；不提升全局 scanner 或旧版最大帧。Ready仅可协商，不是每次请求免校验；路由每次检查当前 readiness、trust、project、generation。连接终止调用 CloseCaseDesignOwner，释放 transfer/review 资源并请求任务取消；不能将取消请求当作cleanup完成。response 序列化后再次校验大小，不允许一次返回1000条定义或完整 diff。旧 detail/managed 路由在1.7执行等价1.6处理；旧1.0–1.6会话永远不获得新路由。

```go
func canOfferCaseDesign(details,managed,design bool) bool { return details && managed && design }
```
- [ ] **Step 4: GREEN。** Run focused 命令，再 `go test ./internal/protocol ./internal/session ./internal/server -count=1`；检查新增测试实际被执行。
- [ ] **Step 5: 小提交。** 仅提交新 decoder/routes、必要旧文件与测试；commit `feat: gate case design routes on trusted ready sessions`。

## Task 11: 客户端 DTO、分块与旧服务兼容

**Files:** Create `packages/test-client/src/case-design.ts`, `case-design.test.ts`；Modify `client.ts`, `connection.ts`, `envelopes.ts`, `schema-registry.ts`, `index.ts`, `package.json` test script。

**Interfaces:** Consumes Task9/10；Produces `CaseDesignClient`：

```typescript
export type CaseDesignMethod =
  'catalog/list' | 'definitions/list' | 'definitions/get' | 'definitions/save' |
  'reviews/prepare' | 'reviews/readDiff' | 'reviews/apply' |
  'transfers/beginImport' | 'transfers/append' | 'transfers/preview' |
  'transfers/resolve' | 'transfers/commit' | 'transfers/beginExport' |
  'transfers/read' | 'transfers/close' | 'runs/start' | 'runs/get' | 'runs/cancel';
export type CaseDesignTransport = (method:CaseDesignMethod, payload:unknown) => Promise<unknown>;
export class CaseDesignClient {
  constructor(ready:()=>boolean, transport:CaseDesignTransport);
  call(method:CaseDesignMethod,payload:unknown):Promise<unknown>;
}
export function supportsCaseDesign(version:string,capability:boolean):boolean;
```

公开 typed overload 的 request/response 对照 Task9 生成的 DTO；unknown 只在底层边界，call内部 schema校验+decoder才返回给 typed封装。TestClient 公开 `caseDesign: CaseDesignClient`，ready读取协商version==1.7且caseDesignV1==true；旧服务 new command明确 unavailable，不会退化调用旧 start 自动生成。

- [ ] **Step 1: 写零发送兼容 RED。**

```typescript
test('old service never receives manual design writes',async()=>{
  let sends=0;
  const client=new CaseDesignClient(()=>false,async()=>{sends++;return {};});
  await assert.rejects(client.call('runs/start',{projectId:'demo',idempotencyKey:'a'.repeat(32)}));
  assert.equal(sends,0);
  assert.equal(supportsCaseDesign('1.6',true),false);
});
```

另测1.7下旧 detailedcoverage/managed接口确实发1.7 envelope而使用既有payload；分页cursor漂移、base64字节精确、关闭后缓存失效、超大 response拒绝。测试纯传输 fixture，不执行编译器。
- [ ] **Step 2: RED。** Run `node node_modules/typescript/bin/tsc -b packages/test-client/tsconfig.json --pretty false`，再 `node --test packages/test-client/dist/case-design.test.js`。
- [ ] **Step 3: 实现边界与版本分支。** 在 schema-registry 注册1.7+复用的1.6refs；connection 的 validators/rank/limit/isProtocolVersion 显式支持1.7。逐处审计 client.ts 中 `version==='1.6'`、硬编码 request('1.6')、capabilities decoder：details/managed在1.7走同等1.6payload而使用实际协商envelope；不得把1.7当legacy。call先 ready+请求schema后发送，响应按 method选择schema再转不可变DTO。transfer chunk选min(128KiB,实际wire预算扣除包装并考虑base64)，逐次await；close放finally，取消或到期停止追加与commit。

```typescript
export function supportsCaseDesign(version:string,capability:boolean):boolean {
  return version === '1.7' && capability === true;
}
```
- [ ] **Step 4: GREEN。** Run上述 build+focused，随后 `pnpm --filter @unit-test-ide/test-client test`；将新测试加入默认 script。保留旧客户端fixture版本矩阵，覆盖1.0–1.6降级和1.7新旧混合调用。
- [ ] **Step 5: 小提交。** 仅提交 test-client 本任务路径；commit `feat: expose bounded case design client methods`。

## Task 12: 已接受快照、真实过滤执行与失败运行覆盖率

**Files:** Create `internal/casedesign/run.go`, `run_test.go`, `internal/taskstore/migrations/024_case_runs.sql`, `case_runs.go`, `case_runs_test.go`, `internal/runtime/production_case_run.go`, `production_case_run_test.go`；Modify `taskstore/migrations.go` 的024可选状态；只在需要时修改既有 `testrun`/`unityrunner`/`testframework` adapter 的新 manifest接线，不另写 parser/runner。

**Interfaces:** Consumes accepted定义、Task9 RunPage、现有 testrun；Produces:

```go
type RunSnapshot struct { ProjectID,Digest string; Items []RunItem }
type RunSummary struct { Complete,CleanupProven,AllPassed bool; Enabled,Executable,Executed,Passed,Failed,Blocked,Disabled int }
func SummarizeRun([]RunItem,bool) RunSummary // bool is cleanup proven
func (*CaseDesignStore) GetAccepted(context.Context,string,string) (casedesign.Definition,error)
func (*CaseDesignStore) SaveRunSnapshot(context.Context,string,casedesign.RunSnapshot) error
func (*CaseDesignStore) AppendCaseResult(context.Context,string,casedesign.RunItem) error
func (*CaseDesignStore) FinishCaseRun(context.Context,string,casedesign.RunPage) error
func (*CaseDesignStore) ReadCaseRun(context.Context,string,string,string,int) (casedesign.RunPage,error)
type CaseRunner interface {
    Start(context.Context,string,string,string) (casedesign.RunPage,error) // owner, project, idempotency key
    Read(context.Context,string,string,string,string,int) (casedesign.RunPage,error)
    Cancel(context.Context,string,string,string) error
}
```

GetAccepted 只读取 accepted_revision 指向的不可变版本，不使用 Draft 的输入替代。RunItem.Status：disabled/blocked/queued/running/passed/failed/build-error/crashed/timed-out/cancelled/incomplete。添加 RunPage.ExecutableCount/PassedCount/FailedCount/BlockedCount/DisabledCount，Task9 schema同步；所有统计基于冻结快照，不根据事后改动重新计算。

- [ ] **Step 1: 写 blocked/空选择/关闭用例 RED。**

```go
func TestCaseRunCannotHideBlockedEnabledCase(t *testing.T) {
    items:=[]RunItem{{CaseID:"a",Revision:1,Status:"passed"},{CaseID:"b",Revision:1,Status:"blocked"},{CaseID:"c",Revision:1,Status:"disabled"}}
    got:=SummarizeRun(items,true)
    if got.AllPassed || got.Complete || got.Enabled!=2 || got.Executed!=1 || got.Disabled!=1 { t.Fatalf("%+v",got) }
    if SummarizeRun(nil,true).AllPassed { t.Fatal("empty run passed") }
}
```

另测未发布 revision=2 时仍运行 accepted=1、禁用用例不出现在真实框架 argv/Unity选择清单、同名不同函数不混淆、程序非零exit与实际failed区分、部分结果不补全、服务restart保留历史且旧运行非current、已知FAIL coverage带failed标识。
- [ ] **Step 2: RED。** Run `go test ./internal/casedesign ./internal/taskstore ./internal/runtime -run 'TestCaseRun' -count=1 -v`。
- [ ] **Step 3: 实现快照和既有 framework适配。**

```sql
CREATE TABLE case_runs(run_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,snapshot_digest TEXT NOT NULL,snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),state TEXT NOT NULL,summary_json TEXT);
CREATE TABLE case_run_items(run_id TEXT NOT NULL REFERENCES case_runs(run_id),case_id TEXT NOT NULL,revision INTEGER NOT NULL,definition_digest TEXT NOT NULL,result_json TEXT NOT NULL CHECK(json_valid(result_json)),PRIMARY KEY(run_id,case_id));
```

Start在一致读事务冻结所有项目用例，接受版本/启用意图/Binding有效性/源码三方状态都进入摘要。草稿-only且启用、stale、conflict、pending acceptance作为blocked；有未发布编辑但旧接受版本仍安全可用时，显式展示并执行旧接受版本，不能暗中执行草稿。编译使用Task6受管源码和现有trusted planner；CppUTest.SelectionCases使用已有 `-sg/-sn` adapter，Unity使用现有runner case_identity协议。Interpreter比较实际expected/parsed IDs，不从计划制造ExecutedCount；run总量/并发沿用既有framework/planner预算，超限显式拒绝，不按导入单批1000上限截断整个项目。结果页受Task9帧限。coverage绑定同一实际来源、accepted revisions、toolchain/run ID。

已知断言FAIL不阻止完整coverage采集；有效 collector绑定实际失败run，CoverageState=complete而AllPassed=false。crash/timeout/partial collection不得用旧成功coverage替换；返回incomplete/stale并保留历史。SummarizeRun：Complete仅在非disabled每项都有可归属终态passed/failed且cleanup=true；AllPassed额外要求Enabled>0、Failed=0、Blocked=0、Executed==Enabled。pending取消不变终态passed；cleanup未证实终态incomplete。并发结果追加/取消使用DB CAS，不覆盖已记录实际结果。
- [ ] **Step 4: GREEN。** Run focused 命令，再 `go test ./internal/testrun ./internal/testframework/... ./internal/unityrunner -count=1`；这些是 adapter/unit证据，不称为native通过。
- [ ] **Step 5: 小提交。** 显式提交024/run实现及必要adapter接线；commit `feat: execute accepted enabled cases with truthful coverage results`。

## Task 13: 类型化向导、本地文件入口与一键执行界面

**Files:** Create `apps/code-oss-extension/src/case-design-controller.ts`, `case-design-view.ts`, `case-design-files.ts`, `case-design-commands.ts`, `test/case-design.test.mjs`；Modify `src/protocol-client.ts`, `contracts.ts`, `extension.ts`, `extension-entry.cts`, `package.json` commands/test。沿用现有覆盖率树和来源导航，不新建另一覆盖率数据库。

**Interfaces:** Consumes Task11 client typedDTO及既有 ExtensionHost；Produces:

```typescript
export interface CaseDesignFileHost {
  chooseImport():Promise<string|undefined>;
  chooseExport():Promise<string|undefined>;
  readBounded(path:string,maxBytes:number):Promise<Uint8Array>;
  writeExact(path:string,data:Uint8Array):Promise<void>;
}
export function assertCaseFileSize(size:number):void;
export function escapeCaseText(value:string):string;
export class CaseDesignController {
  constructor(client:CaseDesignClient,files:CaseDesignFileHost);
  importProject(projectId:string):Promise<void>;
  exportProject(projectId:string,state:'draft'|'accepted'):Promise<void>;
}
export function registerCaseDesignCommands(context:CommandContext,manager:CommandManager,clientProvider:ClientProvider,output:OutputChannelLike,status:CommandStatus,host:ExtensionHost):void;
```

CommandContext/CommandManager/ClientProvider/OutputChannelLike/CommandStatus来自现有commands.ts，ExtensionHost来自extension.ts（仅type import），生产VSCode context已有结构兼容。按既有registerCommands参数接线到当前活动服务/信任状态，不能从host假造client。命令固定 `unitTestIde.designTestCase`、`unitTestIde.importTestCases`、`unitTestIde.exportTestCases`、`unitTestIde.runMaintainedTests`，使用manifestcontributes/register绑定；与现有自动入口并列，不重命名或重复开发自动生成。controller用Task11.caseDesign.call typed overload，不创建本地假结果替代service。

- [ ] **Step 1: 写超限零读取和文本转义 RED。**

```javascript
test('manual form escapes text and rejects oversized files',()=>{
  assert.throws(()=>assertCaseFileSize(8*1024*1024+1));
  assert.equal(escapeCaseText('<script>"&'), '&lt;script&gt;&quot;&amp;');
});
```

test 顶层从 `../dist/src/case-design-files.js` 导入assertCaseFileSize、`../dist/src/case-design-view.js`导入escapeCaseText。另通过既有 ExtensionHost unit fixture断言 untrusted/无cap/空workspace零发送；没有定义可执行用例时不显示PASS；draft保存不调用reviews/apply；旧revision确认拒绝；文件取消不创建transfer，超限文件在read前拒绝；diff/knownFAIL确认相绑定，不用一次通用“确定”复用所有结果。
- [ ] **Step 2: RED。** Run `node node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --pretty false`，再 `node --test apps/code-oss-extension/test/case-design.test.mjs`。
- [ ] **Step 3: 实现真实入口和向导。** 函数/类列表先消费catalog/list并分页，unsupported展示原因禁用保存。控件按 Type 渲染 boolean/integer/float/enum/string/array/record/已允许owned pointer；整数使用字符串，避免JS数值精度丢失；重复submit/CAS旧revision拒绝。typed输入+明确预期输出保存draft，随后prepare显示真实构建/结果/diff，独立knownFAILcheckbox才允许apply。没有预期时保持draft，不冒充通过。手改代码divergence显示keep-current/冲突，禁止无依据反向填表。

webview 使用固定产品脚本、nonce/CSP（default-src none；无外部连接），禁止HTML插入输入和eval/任意expression；消息严格closed action校验并经host trust复核。本地文件选取仅可读用户实际选择的文件：lstat/打开fd/fstat前后限制8MiB，拒绝目录及不可安全确定的链接/越界增长，读取过程中仍限长。writeExact使用同目录临时文件+安全rename，已有文件必须VSCode明确覆盖确认，不自动选择路径；失败清理临时文件。导入preview分页并支持keep-local/replace-draft解析，resolve分块后commit最终摘要；finallyclose。导出冻结且完整收到/digest校验后才写文件。

文件选择器限定本地file URI，路径由host保存，不接受webview传入任意path/URL；不触发远程workspace文件下载。case-design-view.ts固定脚本响应向导消息，action schema和data同样只允许typed值。

一键run捕获runId并有界查询runs/get，显示requested/executable/executed/pass/fail/blocked/disabled及cleanup状态；取消只显示requested直到服务终态。文件/函数coverage导航传当前CoverageRunID到现有tree；failed/incomplete/stale标签不可隐藏。package test script加入新文件，默认测试能发现它。
```typescript
export function assertCaseFileSize(size:number):void {
  if (!Number.isSafeInteger(size) || size < 0 || size > 8*1024*1024) throw new Error('CASE_FILE_SIZE');
}
export function escapeCaseText(value:string):string {
  return value.replaceAll('&','&amp;').replaceAll('<','&lt;').replaceAll('>','&gt;').replaceAll('"','&quot;').replaceAll("'",'&#39;');
}
```

- [ ] **Step 4: GREEN。** Run上述build/focused再 `pnpm --filter code-oss-extension test`；只在本批整合后跑这一次全扩展suite，禁止每个UI文字改动重复native矩阵。
- [ ] **Step 5: 小提交。** 显式提交extension本任务文件；commit `feat: add offline typed case wizard and maintained run controls`。

## Task 14: 生产接线、自动入口复用与完整 readiness

**Files:** Create `internal/runtime/case_design.go`, `production_case_design.go`, `production_case_design_test.go`, `production_case_generation_adapter.go`, `production_case_generation_adapter_test.go`；Modify `runtime/runtime.go`、`production_generation_runtime_composition.go`、`cmd/unit-test-service/main.go` 中产品工厂/服务构造接线，不修改冻结的Windows旧native helper。

**Interfaces:** Consumes全部前述组件；Produces实现Task10 CaseDesignBackend的产品service：

```go
type CaseDesignConfig struct {
    Store *taskstore.CaseDesignStore
    Catalog CatalogResolver
    Validator CaseValidator
    Publisher CasePublisher
    Runner CaseRunner
    Transfers *caseexchange.TransferManager
}
func NewProductionCaseDesignConfig(*taskstore.Store,*Runtime) (CaseDesignConfig,error)
func NewCaseDesignService(CaseDesignConfig) (session.CaseDesignBackend,error)
func (s *CaseDesignService) BindAutomatic(context.Context,string,string,string) error
// owner, existing runID, existing candidateID；从旧服务受信候选/接受存储读取，不从IPC获取proof。
```

CaseDesignService是本任务新增struct，内部持有config和owner作用域状态，不自建SQLite或编译器。config工厂配置独立manual HMAC key/持久review目录及旧publisher共享写mechanics，在恢复完成前ready=false。不要用测试fake provider/flag装成产品已ready。

- [ ] **Step 1: 写缺组件失败关闭 RED。**

```go
func TestCaseDesignCompositionRejectsMissingAuthorities(t *testing.T) {
    if _,err:=NewCaseDesignService(CaseDesignConfig{});err==nil { t.Fatal("empty composition advertised") }
}
```

在实际production composition tests验证022/023/024不可用、pendingjournal、missingbundles、untrusted、missingvalidator/runner都不advertise。自动候选适配测试验证既有CaseID保持、原auto receipts/positivegain门槛保留、手动proof权限不能借BindAutomatic制造，direct legacy测试源保持可读且不自动覆盖。
- [ ] **Step 2: RED。** Run `go test ./internal/runtime -run 'TestCaseDesignComposition|TestCaseAutomaticAdapter' -count=1 -v`。
- [ ] **Step 3: 完成实际dispatch与工厂。** HandleCaseDesign按Task9method解码后委托Catalog/Store/Validator/Publisher/Runner/Transfers；每次在运行中的trusted项目重新绑定，definition/save从functionID查当前catalog重建Binding，不接受wire的ProgramDigest等权限字段。transfer/preview生成有摘要的一致当前revision快照；resolve保存选择而不写draft；commit在owner锁定的提交阶段最终复查deadline/digest/当前revision，原子SaveDrafts；拒绝过期提交。commit开始前close/cancel则零写，开始后取消不能称零写，返回可查询提交状态，恢复时以DB事务结果为准。导出分别读取draft或GetAccepted，而不是用Draft伪装accepted。

save/import先对全部记录Resolve→ValidateDraftBound再调用原子SaveDrafts，缺预期可保留draft但不能prepare/publish/run。已接受auto用例的明确用户编辑可以产生用户预期revision，Origin仍automatic并显示authoring动作；未接受auto候选禁止用导入/手动policy接管其身份以绕过旧准入。新manual必须新scenario。manual验证时已有accepted knownFAIL与当前baseline绑定，不能将新增/不明回归当作可保留的目标断言FAIL。

```go
type CaseDesignService struct { config CaseDesignConfig; mu sync.Mutex; owners map[string]bool }
func NewCaseDesignService(c CaseDesignConfig) (session.CaseDesignBackend,error) {
    if c.Store==nil || !c.Store.Ready() || c.Catalog==nil || c.Validator==nil || c.Publisher==nil || c.Runner==nil || c.Transfers==nil {
        return nil,casedesign.ErrUnavailable
    }
    return &CaseDesignService{config:c,owners:make(map[string]bool)},nil
}
```

该构造代码不是ready=true的证据；CaseDesignReady重新核对factory-owned runtime当前来源/工具/readiness和恢复状态。owners表注册的是authenticated/trusted session；Handle必须先原生workspace解析，不仅查此bool。CaseRunner.Read允许同一授权项目读取completed历史运行，Cancel仅实际活动任务owner可请求。

BindAutomatic通过服务端候选/旧proof验证映射统一定义：沿用现有scenario/CaseID；只在旧自动全部准入成功后同步accepted元数据，不降格manual。同步需receipt-bound持久journal：在023接受journal增加policy=auto/manual闭集，auto恢复仅接受原auto receipt，manual仍走独立MAC；旧accepted registry为权威，发生同步未完成时新case功能关闭但旧数据仍可读。不以并行DB无journal双写代替恢复机制。无法安全结构化的legacy测试显示legacy/manual-divergence，只读维护现有源码，不猜测inputs。

Session owner断开取消transfer/review并请求run取消；Stop等待既有processcontrol有界清理证据，无证据将run记incomplete，阻止current qualification。main创建factory/通过NewServiceWithCaseDesign接线；只有完整composition+实际bundles+恢复成功的runtime才能caseDesignReady=true。不新增网络接口或部署服务器。
- [ ] **Step 4: GREEN。** Runfocused及 `go test ./internal/runtime ./internal/session ./internal/server ./internal/taskstore -count=1`，然后Task9schema/generated/Task11client/Task13extension的默认测试各一次，完成跨层兼容收口。保存真实命令、退出码和执行项数；重复无变化不再重跑。
- [ ] **Step 5: 小提交。** 显式提交工厂/glue/恢复增量；commit `feat: wire case design into the offline production service`。

## Task 15: 新路径真实验收、维护操作和能力边界证据

**Files:** Create `tools/service-probe/src/native-case-design.ts`, `native-case-design.test.ts`, `tools/service-probe/fixtures/case-design/unity/CMakeLists.txt`, `unity/src/classify.c`, `unity/tests/CMakeLists.txt`, `unity/tests/baseline.c`, `tools/service-probe/fixtures/case-design/cpputest/CMakeLists.txt`, `cpputest/src/classify.cpp`, `cpputest/tests/CMakeLists.txt`, `cpputest/tests/baseline.cpp`（unity/cpputest缩写前缀均指本句完整case-design目录）；Create `apps/code-oss-extension/test/case-design-native.test.mjs`、`docs/testing/unified-maintainable-test-cases.md`；证据文件在实际验收后按UTC生成于 `docs/superpowers/evidence/phase10/`，文件名带真实attempt ID，不预填虚假token。Modify service-probe package.json以加入纯unit test/新native命令，现有路线图 `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md` 只更新有证据状态，不新造重复路线图。旧八冻结路径不在此范围。

**Interfaces:** Consumes真实built service、实际批准离线bundles、既有probe/guards；Produces结构化profile证据和用户操作文档。新probe定义 `runNativeCaseDesign(profile,deadline,signal)`，profile闭集windows-unity/windows-cpputest/linux-unity/linux-cpputest，deadline绝对时间，不循环重启延长预算。report字段绑定40位sourceSHA、dirty状态、OS/framework、bundle/toolchain摘要、runID、实际case revisions/result counts、coverage provenance、cleanup证据和status。证据值来自实际产品，不填预计结果。

- [ ] **Step 1: 写report约束和丢失证据 RED。** 在native-case-design.test.ts中定义并测试 `requireNativeCaseDesignEvidence(report)`，report为本任务私有 `NativeCaseDesignEvidence`：

```typescript
test('missing native results is not a passing profile',()=>{
  const report:NativeCaseDesignEvidence={sourceSHA:'a'.repeat(40),dirty:false,
    profile:'windows-unity',status:'passed',executed:0,coverageComplete:false,cleanupProven:false};
  assert.throws(()=>requireNativeCaseDesignEvidence(report));
});
```

类型除样本字段再允许optional证据字段bundleDigest、runId、items、coverageRunId、cleanupReceipt；validator对passed要求全部存在且互相绑定，不能缺省填充。另测dirty/timeout/wrongSHA/blocked不能qualified。纯report测试不启动native。
- [ ] **Step 2: RED。** Run `node node_modules/typescript/bin/tsc -b tools/service-probe/tsconfig.json --pretty false` 和 `node --test tools/service-probe/dist/native-case-design.test.js`。
- [ ] **Step 3: 创建隔离fixture和实际探针。** 两fixture均包含真实 `int classify(int x){return x>0?1:0;}` 和独立已有passing baseline；Unity复用Task12真实driver，CppUTest复用现有framework主入口，使用批准bundle，不FetchContent/download。startService显式offer1.7，先验证caseDesignV1。每profile完成：catalog有真实函数ID→向导等价DTO手动input1/want1→prepare/apply→一键真实PASS+当前函数/文件coverage→JSONexport/import幂等→编辑保持ID增加revision→input0/want1实际FAIL→精确已知FAIL确认保留→source手改拒绝覆盖→disable实际少执行→service restart accepted和history一致。另验证blocked用例使batchincomplete、缺失collector不假用历史PASS；扩展宿主用实际command/展示逻辑而非纯controller假结果。

```typescript
export type NativeCaseDesignEvidence = {
  sourceSHA:string; dirty:boolean;
  profile:'windows-unity'|'windows-cpputest'|'linux-unity'|'linux-cpputest';
  status:'passed'|'failed'|'blocked'|'timed-out'|'incomplete';
  executed:number; coverageComplete:boolean; cleanupProven:boolean;
  bundleDigest?:string; toolchainId?:string; toolchainDigest?:string; sourceDigest?:string;
  runId?:string; items?:ReadonlyArray<{caseId:string;revision:number;status:string}>;
  coverageRunId?:string; cleanupReceipt?:string;
};
export function requireNativeCaseDesignEvidence(r:NativeCaseDesignEvidence):void {
  if (r.status!=='passed' || r.dirty || !/^[a-f0-9]{40}$/.test(r.sourceSHA) ||
      !r.coverageComplete || !r.cleanupProven || r.executed<1 ||
      !r.bundleDigest || !/^[a-f0-9]{64}$/.test(r.bundleDigest) ||
      !r.toolchainId || !r.toolchainDigest || !/^[a-f0-9]{64}$/.test(r.toolchainDigest) ||
      !r.sourceDigest || !/^[a-f0-9]{64}$/.test(r.sourceDigest) || !r.runId ||
      !r.coverageRunId || !r.cleanupReceipt || !r.items || r.items.length!==r.executed ||
      r.items.some(i=>!/^utc_[a-f0-9]{32}$/.test(i.caseId) || i.revision<1)) throw new Error('NATIVE_CASE_DESIGN_INCOMPLETE');
}
```

此结构是单run最小证据；最终profile报告汇总上述维护场景的多个run，并逐项核对source/toolchain/receipt归属及预期PASS/已知FAIL。schema validator只验证形状，真实出处仍由服务/collector/cleanup receipt检查，不能仅凭字符串宣布qualified。

profile名称仅表示OS/framework，实际编译器/覆盖率后端仍必须在report中单独绑定。fixture按当前批准工具链配置，不以一个clang profile证明GCC/MSVC也通过；要声明完整已有工具链支持时，每个声明配置都取得这条新用例路径证据，缺项记录open/blocked，不增加虚假矩阵green。

native-case-design.ts的CLI必须用import.meta.url与实际argv[1]匹配的main guard；测试import其validator不运行service/编译器。扩展case-design-native.test.mjs不加入普通unit脚本，单独由明确native gate启动；纯report test则加入默认unit脚本。

采用已有 `native-network-guard.ts`/offline边界；Windows需要真实WFP隔离及进程树注册，HTTP guard不足以宣称离线。Linux采用既有批准native离线隔离；没有管理员/工具包/边界即blocked，不能skip为PASS。进程创建登记PID+出生时间、父子关系、deadline；单profile固定预算在运行前声明（默认不超过20分钟），每<=60秒可返回进度，无结果不延长。超时一次后收集有界证据并停止；cleanup未证实不重启同profile。
- [ ] **Step 4: GREEN与分层最终gate。** 先focusedreport+已批准profiles真实probe（从根：`node tools/service-probe/dist/native-case-design.js --profile <上述四个闭集之一>`；实现CLI严格校验该选项）。只有旧W4/性能前置工作已经另行收口并获执行所需批准后才启动这些native，不以本计划批准性能设计。先选一个Windows实际可用framework烟测，成功再执行另一framework和Linux实际profile；不在修复每条UI/schema后跑四平台矩阵。完成时所有声明支持profiles都有新鲜真实证据，扩展宿主测试使用实际bundled Code-OSS/service。无条件fulfilled不能标完整支持或release-ready。最终独立代码审查及 verification-before-completion 必须核对spec逐项证据；仅当人选择子代理模式时使用审查代理，inline模式由控制器自审并明确独立审查尚缺时不能声称该gate关闭。
- [ ] **Step 5: 小提交与文档收口。** 保存native evidence前检查无token、绝对个人路径/输入敏感日志；只提交新fixture/probe/UI宿主测试/已验证文档。操作文档逐步说明新四命令、draft与accepted、用户预期、knownFAIL、启用/禁用、JSON大小/版本/冲突、current与historicalcoverage，全部PowerShell片段自带必要变量/失败立即throw，禁止错误后无条件PASS。commit `test: qualify maintained cases on real offline frameworks`；本地完成不隐含推送/PR/合并/Release/签名。

## 2. 需求—任务—验收映射与后续保留

| 已确认要求 | 本批落实/证据 | 未在本批实现的保留项 |
| --- | --- | --- |
| C/C++动态测试 | Tasks5–8/12/15，Unity与CppUTest真实case结果 | 更广泛不安全类型/构造能力不宣称支持 |
| 每函数/类解析、边界/随机输入 | Task5完整支持/unsupported清单；Task14既有自动入口接入 | 新边界/种子随机策略及新类构造profiles，后续独立设计 |
| 图形向导及数据输入 | Tasks3/4/9–11/13/15，typed图形+JSON | CSV/Excel/ZIP不含；未建网络服务器 |
| 自动组驱动/执行/收集 | Tasks6/7/12/14/15，复用真实frameworkrunner | 无Mock/Stub；测试unit fixture不作为native证据 |
| 语句/分支/MC/DC专业视图 | Tasks12/13展示真实现有function/line/branch与来源 | 真正statement和MC/DC尚未实现，不能用行/分支改名 |
| 驱动/输入/三类coverage记录文档 | Tasks3/13仅本地定义JSON，Task15维护操作文档 | 完整运行证据/驱动/输入/statement/MC/DC报告导出后续独立批次 |
| 测试项目管理 | 现有活动项目绑定/Task5/10/14隔离 | 完整项目CRUD/目录管理后续 |
| 静态测试任务管理 | 不影响现有动态链路 | 静态规则/工具/任务后续 |
| 动态环境配置/任务管理 | Task12既有可信工具环境/任务执行 | 完整环境配置/任务编排UI后续 |
| 测试数据管理/流通 | Tasks1–4/8/12 durable版本/接受/运行/JSON | 完整跨项目数据管理后续，当前不自动remap |
| 数据上传下载/报告下载 | Tasks3/4/13用户选择的本地文件导入导出 | 依用户离线选择，不实现网络上传下载；完整报告后续 |

验收闭环只对本批声明完成：稳定身份与修订、受管维护保护、手动预期/knownFAIL独立准入、本地JSON有界流通、真实启用case执行和真实文件/函数coverage。不得据此称全部11项、Phase8签名/legal或完整项目已完成。

## 3. 执行依赖、自审及交接

依赖顺序 A→B→C→D→E；Task9可先定义Task12纯DTO，但不能提前宣称runs ready；Task13只能使用完整production service进行最终验收。准备和capability advertisement分离：Task10/11 unit通过不是产品入口ready，Task14完整组合才开放。每一步修改前确认当前接口与本计划一致，若有实质差异停在相关batch修订计划，不持续堆叠不安全fallback。

最终自审checklist（计划编写阶段只查文档与现有接口，不执行上述测试）：检查每任务新符号有定义、无循环依赖、new/modify路径区分、wire与DB限额一致、旧语义保留、测试脚本实际包含新项、所有spec要求有映射；检查不存在假PASS/观察即oracle/跳过验证bool；原八冻结源码和旧证据不纳入文档提交。

计划确认后执行选择：**1. 子代理分任务实施（推荐）**：按subagent-driven-development逐任务实施/规格与质量审查，有依赖任务串行；**2. 当前会话直接实施**：按superpowers:executing-plans逐批执行和记录。选择只授权本地实施/必要验证，不授权任何远端变更。当前文档编写轮尚未启动实施、测试、native或代理。
