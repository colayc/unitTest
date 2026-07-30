# Phase 4A：Protocol v1.3 与测试领域基础实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 在不执行任何真实测试的前提下，建立 Workspace config v2、Protocol v1.3、TypeScript Client API、Go 测试领域模型、稳定 ID 和 Catalog repository。

**架构：** JSON Schema 继续作为 wire shape 唯一事实来源；`testdomain` 是 Service 内部模型，不依赖 Protocol generated type；Catalog repository 复用现有 SQLite 与 ArtifactStore 边界。本计划不启动 CTest 或 framework executable。

**基础提交：** `07e01ca33f60c3c209c010b1b8195e72be687b0d`

## 全局约束

- v1.0/v1.1/v1.2 contract 不变。
- Workspace config v1 继续有效；v2 只新增受控 framework mapping。
- wire request 中不存在 executable、args、env、cwd、Shell、hook 或 result path。
- Test ID 使用 length-prefixed UTF-8 NFC tuple + SHA-256。
- Catalog 是不可变 snapshot；分页不改变 snapshot 内容。
- 本计划不添加 CTest、CppUTest、Unity 或 CMock process。

---

### Task 1：Workspace config v2 与 framework exact-name mapping

**文件：**

- 修改：`apps/test-service/internal/workspace/workspace.schema.json`
- 修改：`apps/test-service/internal/workspace/config.go`
- 修改：`apps/test-service/internal/workspace/config_test.go`
- 创建：`apps/test-service/internal/workspace/testdata/tests-v2.valid.json`
- 创建：`apps/test-service/internal/workspace/testdata/tests-command.invalid.json`
- 创建：`apps/test-service/internal/workspace/testdata/tests-duplicate.invalid.json`
- 修改：`apps/test-service/internal/cmake/generation.go`
- 修改：`apps/test-service/internal/cmake/generation_test.go`
- 修改：`tools/workspace-smoke/workspace-config-schema.test.mjs`

**接口：**

```go
type Framework string

const (
	FrameworkCppUTest Framework = "cpputest"
	FrameworkUnity    Framework = "unity"
)

type TestContainerMapping struct {
	CTestName string
	Framework Framework
}
```

- [x] **Step 1：写出 v1 compatibility、v2 valid 和 unsafe field 失败测试**

覆盖：

- v1 minimal config 仍通过；
- v2 exact CTest name + enum framework 通过；
- duplicate `ctestName` 拒绝；
- `ctestPattern`/glob 等非 exact-name 字段、空名称、超长名称拒绝；
- command/executable/args/environment/workingDirectory/hook 拒绝；
- `additionalProperties: false`；
- config canonical hash 包含 tests mapping。

- [x] **Step 2：运行聚焦测试并确认失败**

```powershell
go test ./apps/test-service/internal/workspace -run 'ConfigV2|TestContainer|Unsafe' -count=1
pnpm test:workspace
```

预期：FAIL，v2 Schema 和 model 尚不存在。

- [x] **Step 3：实现 strict v1/v2 loader**

Loader 根据顶层 `version` 选择 Schema 分支。`ctestName` 使用 UTF-8、长度和 NUL 校验，只保存 exact string；不得编译 regex。framework 使用 closed enum。

- [x] **Step 4：运行 Workspace 全套测试**

```powershell
go test ./apps/test-service/internal/workspace -count=1
pnpm test:workspace
git diff --check
```

预期：PASS。

- [x] **Step 5：提交 Workspace config v2**

```powershell
git add apps/test-service/internal/workspace apps/test-service/internal/cmake/generation.go apps/test-service/internal/cmake/generation_test.go tools/workspace-smoke
git commit -m "feat: define workspace test mappings"
```

### Task 2：Protocol v1.3 Schema、fixtures 与生成模型

**文件：**

- 创建：`packages/protocol-schema/schema/v1.3/capabilities.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/diagnostic.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/test.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/task.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/event.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/artifact.schema.json`
- 创建：`packages/protocol-schema/schema/v1.3/message.schema.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-discovery-start.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-run-start.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-catalog.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-result.valid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-run-command.invalid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-run-environment.invalid.json`
- 创建：`packages/protocol-schema/fixtures/v1.3/test-run-args.invalid.json`
- 修改：`packages/protocol-schema/test/schema.test.mjs`
- 修改：`tools/protocol-gen/generate.mjs`
- 修改：`packages/protocol-models/src/index.ts`
- 修改：`packages/protocol-models/src/generated-contract.test.ts`
- 创建：generated v1.3 TypeScript files under `packages/protocol-models/src/generated/`
- 创建：generated v1.3 Go files under `apps/test-service/internal/protocolmodel/v1_3/`

**主要 wire model：**

```text
CapabilitiesV13
TestCatalog
TestContainer
TestItem
TestSelection
TestRun
TestItemResult
TaskSnapshotV13
TaskEventV13
```

- [ ] **Step 1：写出 v1.3 valid、negative injection 和 compatibility 测试**

至少逐字段测试：

```text
executable command args argv shell script env environment
cwd workingDirectory hook preHook postHook resultPath
```

同时验证 repeat 1..100、page 1..1000、item selection 最大 100,000、closed unions、稳定 ID pattern 和安全 integer。

- [ ] **Step 2：运行 Schema tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/protocol-schema test
```

预期：FAIL，v1.3 schemas 尚不存在。

- [ ] **Step 3：实现 v1.3 closed schemas 与 deterministic generation**

`tasks/start` v1.3 使用 closed `oneOf`，保留 simulation/cmakeBuild 并增加 testDiscovery/testRun。只读方法增加 `tests/catalog/get`、`tests/runs/get`、`tests/runs/list`。

v1.3 Test Selection 只能表达 ID 和结构化 Catalog filter。`failedFromRun` 只包含 `runId`。

- [ ] **Step 4：生成并验证 drift**

```powershell
pnpm generate:protocol
pnpm --filter @unit-test-ide/protocol-schema test
pnpm --filter @unit-test-ide/protocol-models test
pnpm check:protocol-generated
```

预期：PASS，第二次 generation 无 diff。

- [ ] **Step 5：提交 Protocol v1.3 contract**

```powershell
git add packages/protocol-schema packages/protocol-models apps/test-service/internal/protocolmodel tools/protocol-gen/generate.mjs
git commit -m "feat: define protocol v1.3 test contracts"
```

### Task 3：TypeScript Client v1.3 API 与 decoder

**文件：**

- 修改：`packages/test-client/src/envelopes.ts`
- 修改：`packages/test-client/src/connection.ts`
- 修改：`packages/test-client/src/client.ts`
- 修改：`packages/test-client/src/decoders.ts`
- 修改：`packages/test-client/src/subscription.ts`
- 修改：`packages/test-client/src/index.ts`
- 修改：`packages/test-client/src/client.test.ts`

**接口：**

```ts
discoverTests(input: TestDiscoveryInput): Promise<TaskSnapshotV13>;
runTests(input: TestRunInput): Promise<TaskSnapshotV13>;
getTestCatalog(input: CatalogGetInput): Promise<TestCatalog>;
getTestRun(runId: string): Promise<TestRun>;
listTestRuns(input: TestRunListInput): Promise<TestRunPage>;
```

- [ ] **Step 1：写出 negotiation、本地校验和 runtime decoder 失败测试**

覆盖：

- 首选 v1.3，fallback 到 v1.2/v1.1/v1.0；
- 旧 session 调用 test API 在本地拒绝且不写 wire；
- selection/repeat/page/ID 在本地先校验；
- unknown outcome、unsafe integer、invalid URI/date 拒绝；
- v1.3 test event sequence 保持；
- v1.2 subscription projection 行为不变。

- [ ] **Step 2：运行 Client tests 并确认失败**

```powershell
pnpm --filter @unit-test-ide/test-client test
```

预期：FAIL。

- [ ] **Step 3：实现 version-aware API 和 strict decoder**

Client 不构造任何 framework 参数。Filter 仅保留 generated type 中定义的字段。Decoder 对 Catalog tree reference、summary count、iteration 和 `partial` 做交叉校验。

- [ ] **Step 4：运行 TypeScript build/tests**

```powershell
pnpm --filter @unit-test-ide/protocol-models build
pnpm --filter @unit-test-ide/test-client test
```

预期：PASS。

- [ ] **Step 5：提交 TypeScript Client**

```powershell
git add packages/test-client
git commit -m "feat: add typed protocol v1.3 test APIs"
```

### Task 4：Go Test Domain、稳定 ID 与 selection

**文件：**

- 创建：`apps/test-service/internal/testdomain/model.go`
- 创建：`apps/test-service/internal/testdomain/model_test.go`
- 创建：`apps/test-service/internal/testdomain/id.go`
- 创建：`apps/test-service/internal/testdomain/id_test.go`
- 创建：`apps/test-service/internal/testdomain/selection.go`
- 创建：`apps/test-service/internal/testdomain/selection_test.go`
- 创建：`apps/test-service/internal/testdomain/errors.go`

**接口：**

```go
func ContainerID(projectID, ctestName string) (ID, error)
func GroupID(projectID, ctestName string, framework Framework, group string) (ID, error)
func CaseID(identity CaseIdentity) (ID, error)
func ResolveSelection(catalog Catalog, selection Selection, limits Limits) (SelectionSnapshot, error)
```

- [ ] **Step 1：写出 stable ID 与 selection 失败测试**

覆盖：

- Windows/Linux path 和 profile 不影响 ID；
- tuple field boundary 不碰撞；
- NFC 等价文本产生同一 ID；
- rename/framework change 产生新 case ID；
- container framework change不影响 container ID；
- duplicate identity 返回明确错误；
- filter 展开 deterministic；
- empty/oversized selection 拒绝；
- disabled item 默认排除；
- 输入数组顺序不影响 selection snapshot。

- [ ] **Step 2：运行 testdomain tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testdomain -count=1
```

预期：FAIL。

- [ ] **Step 3：实现不可变 domain model**

所有 constructor 完成 validation 和 defensive copy。ID tuple 使用字段名、byte length、NFC UTF-8 和 SHA-256；不得使用 `fmt.Sprintf` 拼接 identity。

- [ ] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testdomain -count=1
go test -race ./apps/test-service/internal/testdomain -count=1
```

预期：PASS。

- [ ] **Step 5：提交 Test Domain**

```powershell
git add apps/test-service/internal/testdomain
git commit -m "feat: add stable test domain models"
```

### Task 5：Catalog persistence 与分页

**文件：**

- 创建：`apps/test-service/internal/taskstore/migrations/005_test_catalogs.sql`
- 修改：`apps/test-service/internal/taskstore/migrations.go`
- 创建：`apps/test-service/internal/taskstore/test_catalogs.go`
- 创建：`apps/test-service/internal/taskstore/test_catalogs_test.go`
- 修改：`apps/test-service/internal/taskstore/sqlite_test.go`
- 修改：`apps/test-service/internal/task/ports.go`
- 修改：`apps/test-service/internal/artifactstore/store.go`
- 修改：`apps/test-service/internal/artifactstore/store_test.go`

**接口：**

```go
type TestCatalogRepository interface {
	PublishCatalog(context.Context, testdomain.Catalog, task.Artifact) error
	GetCatalog(context.Context, string, string) (testdomain.Catalog, error)
	PageCatalog(context.Context, testdomain.CatalogPageRequest) (testdomain.CatalogPage, error)
}
```

- [ ] **Step 1：写出 migration、atomic publish 和 cursor 失败测试**

覆盖：

- migration upgrade/rollback；
- project/profile 只有一个 current Catalog；
- publish transaction 失败保留旧 Catalog；
- Artifact commit 失败不切换 metadata；
- pagination cursor 绑定 revision；
- stale cursor 被拒绝；
- 100,000 item 上限；
- restart 后读取一致；
- concurrent reader 只看到旧或新完整 Catalog。

- [ ] **Step 2：运行 Store tests 并确认失败**

```powershell
go test ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -run 'Catalog|Migration005' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 Catalog repository**

SQLite 保存 revision、project/profile、summary、artifact reference 和查询索引；完整 Catalog JSON 通过 ArtifactStore 原子提交。Publication 顺序必须避免 dangling metadata。

- [ ] **Step 4：运行 Store 全套与 race**

```powershell
go test ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
go test -race ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore -count=1
```

预期：PASS。

- [ ] **Step 5：提交 Catalog persistence**

```powershell
git add apps/test-service/internal/taskstore apps/test-service/internal/task/ports.go apps/test-service/internal/artifactstore
git commit -m "feat: persist immutable test catalogs"
```

## Phase 4A 完成检查

- [ ] Workspace v1/v2 compatibility
- [ ] Protocol v1.0–v1.3 contract tests
- [ ] execution injection negative fixtures
- [ ] deterministic generated model drift
- [ ] TypeScript Client strict tests
- [ ] stable ID cross-platform unit tests
- [ ] Catalog atomic publication/restart tests
- [ ] `pnpm verify`
- [ ] `git diff --check`
- [ ] 独立评审确认本计划没有启动 CTest/framework process
