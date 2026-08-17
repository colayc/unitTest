# Phase 4B：CTest Catalog 与 Opaque fallback 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 从受控 Build Profile 的 CTest JSON 建立可靠 Test Catalog，验证 case-level compatibility，并为不支持的测试提供安全 Opaque container 执行。

**架构：** `ctest.Adapter` 只负责 CTest metadata 和容器执行；`testframework.Registry` 只选择 Adapter；`testdiscovery.Builder` 负责 stable identity、degradation 和 revision。Framework-specific case parser 留给 Phase 4C/4D。

**依赖：** Phase 4A。

## 全局约束

- 只使用 Phase 3 验证的 CMake installation、Build Profile 和 build root。
- CTest JSON command 不是 Protocol 输入。
- 未知 framework 不运行探测参数，直接 Opaque。
- Opaque container 使用 CTest 精确选择，不直接执行 CTest command。
- case-level compatibility 使用 allowlist；未知 property 默认降级。
- Catalog 只有完整验证后才原子发布。

---

### Task 1：CTest JSON v1 model、parser 与 Golden File

**文件：**

- 创建：`apps/test-service/internal/ctest/model.go`
- 创建：`apps/test-service/internal/ctest/json.go`
- 创建：`apps/test-service/internal/ctest/json_test.go`
- 创建：`apps/test-service/internal/ctest/testdata/show-only-linux.json`
- 创建：`apps/test-service/internal/ctest/testdata/show-only-windows.json`
- 创建：`apps/test-service/internal/ctest/testdata/show-only-multiconfig.json`
- 创建：`apps/test-service/internal/ctest/testdata/show-only-malformed.json`

**接口：**

```go
func ParseShowOnlyJSON(data []byte, limits Limits) (Snapshot, error)
```

- [x] **Step 1：写出 CTest JSON parser 失败测试**

覆盖：

- tests、command、properties、labels 和 backtrace graph；
- Windows path、Unicode、空格和 CRLF；
- multi-config command；
- duplicate logical name；
- unknown JSON field 的兼容保留/忽略规则；
- unsafe integer、NUL、超长 field、超大 document；
- broken backtrace index；
- command 为空。

- [x] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/ctest -run 'Parse|ShowOnly' -count=1
```

预期：FAIL。

- [x] **Step 3：实现有界 streaming decode 与 canonical model**

使用 `json.Decoder` 和明确上限，不对整个不可信 JSON 构造 `map[string]any`。保留 CTest logical string，不执行 regex normalization。

- [x] **Step 4：运行 unit/fuzz seed tests**

```powershell
go test ./apps/test-service/internal/ctest -count=1
go test ./apps/test-service/internal/ctest -run Fuzz -count=1
```

预期：PASS。

- [x] **Step 5：提交 CTest JSON parser**

```powershell
git add apps/test-service/internal/ctest
git commit -m "feat: parse ctest json catalogs"
```

### Task 2：CTest command normalization 与 execution descriptor

**文件：**

- 创建：`apps/test-service/internal/ctest/descriptor.go`
- 创建：`apps/test-service/internal/ctest/descriptor_test.go`
- 创建：`apps/test-service/internal/ctest/properties.go`
- 创建：`apps/test-service/internal/ctest/properties_test.go`
- 创建：`apps/test-service/internal/ctest/path_linux_test.go`
- 创建：`apps/test-service/internal/ctest/path_windows_test.go`
- 修改：`apps/test-service/internal/cmake/fileapi.go`
- 修改：`apps/test-service/internal/cmake/fileapi_test.go`

**接口：**

```go
func BuildDescriptor(
	container RawTest,
	profile cmake.BuildProfile,
	targets []cmake.Target,
) (ExecutionDescriptor, error)
```

> 实施修正：`ctest` 不依赖 `build`，避免 Task 5 组装 `build → testdiscovery → ctest`
> 时形成 import cycle。File API `Target` 携带 configuration 与 Project root metadata，
> `cmake` 提供 target artifact 的安全快照/复核；runtime `ExecutionBoundary` 仍由 Task 5
> 在生成 Service-owned plan 时建立。

- [x] **Step 1：写出 direct compatibility 与降级失败测试**

覆盖：

- command artifact 映射 File API executable target；
- Project/build root containment；
- wrapper/script/launcher 降级；
- reserved argument conflict hook；
- WORKING_DIRECTORY/ENVIRONMENT/ENVIRONMENT_MODIFICATION/TIMEOUT；
- DISABLED/SKIP_RETURN_CODE；
- fixture/dependency/resource lock/WILL_FAIL/regex property 降级；
- unknown behavior property 降级；
- RUN_SERIAL exclusive 标记；
- symlink/junction/reparse escape；
- executable identity 在校验后替换。

- [x] **Step 2：运行 descriptor tests 并确认失败**

```powershell
go test ./apps/test-service/internal/ctest ./apps/test-service/internal/cmake -run 'Descriptor|Property|CTestTarget' -count=1
```

预期：FAIL。

- [x] **Step 3：实现显式 property classifier**

Property classifier 返回：

```go
type Compatibility struct {
	CaseLevel bool
	Reasons   []Reason
	RunSerial bool
}
```

任何未知 behavior property 都使 `CaseLevel=false`。Metadata-only property 可以保留，不影响 compatibility。

- [x] **Step 4：运行 unit/race 与平台路径测试**

```powershell
go test ./apps/test-service/internal/ctest ./apps/test-service/internal/cmake -count=1
go test -race ./apps/test-service/internal/ctest -count=1
```

预期：PASS。

- [x] **Step 5：提交 CTest descriptor**

```powershell
git add apps/test-service/internal/ctest apps/test-service/internal/cmake
git commit -m "feat: validate ctest execution descriptors"
```

### Task 3：Framework Registry 与 Adapter contract

**文件：**

- 创建：`apps/test-service/internal/testframework/adapter.go`
- 创建：`apps/test-service/internal/testframework/registry.go`
- 创建：`apps/test-service/internal/testframework/registry_test.go`
- 创建：`apps/test-service/internal/testframework/capability.go`
- 创建：`apps/test-service/internal/testframework/fake_test.go`

**接口：**

```go
type Adapter interface {
	Framework() testdomain.Framework
	ContractVersion() string
	Verify(context.Context, ctest.ExecutionDescriptor) (Capabilities, error)
	Discover(context.Context, ctest.ExecutionDescriptor) (DiscoveryResult, error)
	PlanRun(context.Context, RunInput) (RunPlan, error)
	NewParser(ParseInput) (ResultParser, error)
}
```

- [x] **Step 1：写出 metadata priority、conflict 和 unknown fallback 测试**

覆盖：

- helper metadata 优先；
- helper + mapping 一致；
- helper + mapping 冲突；
- mapping exact-name；
- unknown/no metadata → Opaque；
- Adapter verification failure → Opaque + stable reason；
- Registry duplicate framework/version 拒绝；
- Registry 不执行 unknown binary。

- [x] **Step 2：运行 Registry tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework -count=1
```

预期：FAIL。

- [x] **Step 3：实现小型稳定 Adapter port**

接口不暴露 `processcontrol.Process`，只返回受控 `RunPlan`/Parser。Registry 不依赖 Protocol generated model。

- [x] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework -count=1
go test -race ./apps/test-service/internal/testframework -count=1
```

预期：PASS。

- [x] **Step 5：提交 Framework Registry**

```powershell
git add apps/test-service/internal/testframework
git commit -m "feat: add test framework adapter registry"
```

### Task 4：Catalog Builder、revision 与 degradation

**文件：**

- 创建：`apps/test-service/internal/testdiscovery/builder.go`
- 创建：`apps/test-service/internal/testdiscovery/builder_test.go`
- 创建：`apps/test-service/internal/testdiscovery/revision.go`
- 创建：`apps/test-service/internal/testdiscovery/revision_test.go`
- 创建：`apps/test-service/internal/testdiscovery/diagnostics.go`

**接口：**

```go
func (b *Builder) Build(context.Context, BuildInput) (testdomain.Catalog, error)
func ValidateRevision(context.Context, testdomain.Catalog, FingerprintSource) (RevisionStatus, error)
```

> 实施补充：`testframework.DiscoveredItem` 使用显式 `ParentKind + ParentLogicalName`
> 表达 tree edge，避免 group/suite 同名时猜测父节点。Revision fingerprint 不包含 mtime，
> 只接受 semantic identity 与文件 SHA-256。

- [x] **Step 1：写出 Catalog 构建与 stale 失败测试**

覆盖：

- Project → container → group/suite → case 引用；
- container ID 不含 framework；
- case ID 跨 profile/toolchain 一致；
- 一个 Adapter malformed 只降级该 container；
- duplicate case identity 降级；
- Catalog item 上限；
- CTest semantic、config、File API、executable SHA、manifest、Adapter version 进入 revision；
- mtime 相同但 executable content 改变时 stale；
- stale rebind 全部 ID 存在/部分缺失。

- [x] **Step 2：运行 Builder tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testdiscovery -count=1
```

预期：FAIL。

- [x] **Step 3：实现 deterministic Builder**

Container 按 logical name 排序，tree item 按 logical identity 排序。排序不进入 ID。Adapter 返回 partial/malformed 时丢弃该 container 的全部 case。

- [x] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testdiscovery -count=1
go test -race ./apps/test-service/internal/testdiscovery -count=1
```

预期：PASS。

- [x] **Step 5：提交 Catalog Builder**

```powershell
git add apps/test-service/internal/testdiscovery
git commit -m "feat: build versioned test catalogs"
```

### Task 5：CTest Runner、Opaque exact selection 与 Catalog publication

**文件：**

- 创建：`apps/test-service/internal/ctest/runner.go`
- 创建：`apps/test-service/internal/ctest/runner_test.go`
- 创建：`apps/test-service/internal/ctest/semantic.go`
- 创建：`apps/test-service/internal/ctest/semantic_test.go`
- 修改：`apps/test-service/internal/ctest/descriptor.go`
- 修改：`apps/test-service/internal/ctest/model.go`
- 修改：`apps/test-service/internal/ctest/json.go`
- 创建：`apps/test-service/internal/testdiscovery/service.go`
- 创建：`apps/test-service/internal/testdiscovery/service_test.go`
- 修改：`apps/test-service/internal/cmake/installation.go`
- 修改：`apps/test-service/internal/cmake/resolver.go`
- 修改：`apps/test-service/internal/cmake/resolver_test.go`
- 修改：`apps/test-service/internal/cmake/manifest.go`
- 修改：`apps/test-service/internal/cmake/manifest_test.go`
- 修改：`apps/test-service/internal/cmake/generation.go`
- 修改：`apps/test-service/internal/cmake/generation_test.go`
- 修改：`apps/test-service/internal/build/boundary.go`
- 修改：`apps/test-service/internal/build/planner_test.go`
- 修改：`apps/test-service/internal/task/plan.go`
- 修改：`apps/test-service/internal/task/plan_test.go`
- 修改：`tools/cmake-bundle/manifest.json`
- 修改：`tools/cmake-bundle/prepare.mjs`
- 修改：`tools/cmake-bundle/prepare.test.mjs`
- 修改：`apps/test-service/cmd/cmake-fixture/main.go`
- 修改：`apps/test-service/cmd/cmake-fixture/main_test.go`
- 修改：`tools/service-probe/build-service.mjs`

**接口：**

```go
func (r *Runner) ShowOnlyPlan(profile cmake.BuildProfile) (task.ExecutionStep, error)
func (r *Runner) OpaqueRunPlan(descriptor ExecutionDescriptor, timeout time.Duration) (task.ExecutionStep, error)
func (s *Service) DiscoverAfterBuild(context.Context, DiscoveryInput) (testdomain.Catalog, error)
```

> 实施修正：Phase 4B 只建立 Service-owned CTest step 与同步 discovery/publication
> boundary，不把它附加到现有 CMake Build Task。动态 continuation、Test Task kind 和
> Protocol projection 仍由既定 Phase 4E Task 3/4 接入。Build execution boundary 在本
> Task 同时固定并验证配套 `cmake`/`ctest`，为后续 continuation 做准备。

- [x] **Step 1：写出 Service-owned CTest plan 失败测试**

覆盖：

- CMake installation identity；
- bundled `cmake` 与 `ctest` 来自同一 installation/version；
- `--show-only=json-v1` 与 multi-config `-C`；
- exact logical name regex metacharacters；
- Client 无法传额外 CTest args；
- Opaque wrapper 由 CTest 执行而非 direct process；
- external command 标记 blocked；
- show-only failure 不替换旧 Catalog；
- Catalog artifact 成功后才发布 metadata/event。

- [x] **Step 2：运行 Runner/Service tests 并确认失败**

```powershell
go test ./apps/test-service/internal/ctest ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/build -run 'Runner|Opaque|DiscoverAfterBuild' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 CTest plan 与 publication boundary**

`Runner` 使用 CMake installation 中配套 `ctest`，不搜索 PATH。CMake bundle manifest 同时固定 `cmake` 与 `ctest` relative path、version 和文件 identity；自定义 CMake installation 也必须解析同目录配套 `ctest` 并验证版本一致。CTest logical name 经过 CTest regex 语义的锚定转义；测试覆盖所有 metacharacter。

- [x] **Step 4：运行 Go 全套与 race**

```powershell
go test ./apps/test-service/internal/ctest ./apps/test-service/internal/testframework ./apps/test-service/internal/testdiscovery ./apps/test-service/internal/cmake ./apps/test-service/internal/build ./apps/test-service/internal/taskstore -count=1
go test -race ./apps/test-service/internal/ctest ./apps/test-service/internal/testdiscovery -count=1
pnpm test:cmake-bundle
```

预期：PASS。

- [x] **Step 5：提交 CTest Catalog integration**

```powershell
git add apps/test-service/internal/ctest apps/test-service/internal/testframework apps/test-service/internal/testdiscovery apps/test-service/internal/cmake apps/test-service/internal/build apps/test-service/internal/task apps/test-service/internal/taskstore tools/cmake-bundle
git commit -m "feat: discover ctest containers safely"
```

## Phase 4B 完成检查

- [x] CTest JSON Windows/Linux/multi-config Golden File
- [x] property allowlist/degradation tests
- [x] unknown framework never probed
- [x] Opaque exact CTest execution
- [x] stable Catalog revision/stale tests
- [x] Catalog atomic publication
- [x] Go unit/race tests
- [x] `pnpm verify`
- [x] 独立安全边界审查确认 Protocol 未进入 CTest command/args
