# Phase 4C：CppUTest/CppUMock Adapter 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 为已声明 CppUTest container 提供可靠 case discovery、精确运行、流式结果解析、CppUMock failure detail 和安全降级。

**架构：** CppUTest Adapter 只接收已验证 `CTestExecutionDescriptor`。Discovery 使用 `-ln`；运行使用 Service-owned `-v/-sg/-sn`；parser 以完整 grammar 和 summary 一致性作为 evidence。

**依赖：** Phase 4B。

## 全局约束

- 未经 Registry 选择的 executable 不运行 `-ln`。
- Adapter 参数只从稳定 TestItem 生成。
- 原 CTest 参数与保留参数冲突时降级。
- repeat 由 Test Coordinator 控制，不使用框架 `-r` 汇总。
- exit code 不是 assertion 的充分证据。
- parser 必须支持 chunk、CRLF、ANSI、UTF-8 和有界输出。

---

### Task 1：CppUTest `-ln` discovery parser

**文件：**

- 创建：`apps/test-service/internal/testframework/cpputest/adapter.go`
- 创建：`apps/test-service/internal/testframework/cpputest/discovery.go`
- 创建：`apps/test-service/internal/testframework/cpputest/discovery_test.go`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/list.valid.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/list-crlf.valid.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/list-duplicate.invalid.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/list-malformed.invalid.txt`
- 修改：`.gitattributes`，固定 CRLF Golden File 的跨平台 checkout 行尾

**接口：**

```go
func ParseList(reader io.Reader, limits Limits) ([]CaseIdentity, error)
```

- [x] **Step 1：写出 list grammar 失败测试**

覆盖：

- 空白分隔 `group.name`；
- 多 group/case；
- LF/CRLF/tab；
- ANSI；
- chunk boundary 和分裂 UTF-8；
- empty list；
- 缺少 dot、空 group/name、重复 identity；
- token/document 上限；
- partial final token。

- [x] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'ParseList|Discovery' -count=1
```

预期：FAIL。

- [x] **Step 3：实现有界 tokenizer**

Parser 不使用无限长 `bufio.Scanner` 默认 token，也不根据输出中出现 `CppUTest` 单词判断成功。完整输入关闭且全部 token 合法后才返回 cases。

- [x] **Step 4：运行 unit/fuzz seed**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [x] **Step 5：提交 CppUTest discovery**

```powershell
git add apps/test-service/internal/testframework/cpputest
git commit -m "feat: discover cpputest cases"
```

### Task 2：exact group/case RunPlan 与保留参数

**文件：**

- 创建：`apps/test-service/internal/testframework/cpputest/planner.go`
- 创建：`apps/test-service/internal/testframework/cpputest/planner_test.go`
- 修改：`apps/test-service/internal/testframework/cpputest/adapter.go`
- 修改：`apps/test-service/internal/testframework/adapter.go`，补充多 invocation 和 expected case boundary

**接口：**

```go
func BuildRunPlan(descriptor ctest.ExecutionDescriptor, selection Selection) (testframework.RunPlan, error)
```

- [x] **Step 1：写出受控参数和 conflict 失败测试**

覆盖：

- all → `-v`；
- exact group → `-v -sg <group>`；
- exact case → `-v -sg <group> -sn <case>`；
- group/case 每个参数独立 argv；
- 多离散 case deterministic batch；
- `-g/-n/-sg/-sn/-xg/-xn/-r/-v` conflict 降级；
- shell metacharacter 只作为 literal argv；
- client filter text 不进入 argv；
- executable pin 与 working directory 来自 descriptor。

- [x] **Step 2：运行 planner tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'RunPlan|Reserved' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 immutable RunPlan**

Adapter 返回 framework-neutral RunPlan，不直接启动 process。每个 plan 记录 selection item ID 与预期 case boundary，供结果 parser 交叉验证。

> 实施修正：多个离散 case 各生成一个独立 invocation，并按稳定逻辑身份排序，避免
> CppUTest 多 `-sg/-sn` 组合产生 group/name 交叉选择。Adapter registration 与
> Registry 集成测试仍在 Task 5 完成。

- [x] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testframework -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [x] **Step 5：提交 CppUTest planner**

```powershell
git add apps/test-service/internal/testframework/cpputest apps/test-service/internal/testframework/adapter.go
git commit -m "feat: plan exact cpputest runs"
```

### Task 3：streaming result parser 与状态 evidence

**文件：**

- 创建：`apps/test-service/internal/testframework/cpputest/parser.go`
- 创建：`apps/test-service/internal/testframework/cpputest/parser_test.go`
- 创建：`apps/test-service/internal/testframework/cpputest/grammar.go`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/pass.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/fail.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/ignored.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/crash-partial.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/malformed-summary.txt`
- 修改：`apps/test-service/internal/testframework/adapter.go`，补充流式事件、typed outcome、原始 source path 和互斥 termination
- 修改：`apps/test-service/internal/testframework/fake_test.go`
- 修改：`apps/test-service/internal/testdiscovery/builder_test.go`

**接口：**

```go
type Parser struct
func (p *Parser) Feed(stream testframework.Stream, data []byte) ([]testframework.ResultEvent, error)
func (p *Parser) Finish(result testframework.ProcessResult) (testframework.ParseResult, error)
```

> 实施修正：现有共享合同没有 `processcontrol.Termination`，且 `Write` 无法把完整
> record 的 terminal event 及时交给 Coordinator。本 Task 将共享合同收敛为
> `Feed/Finish`，使用互斥的 `ProcessTermination` 表示 exited/timeout/crash/cancel，
> 并在 Adapter 边界保留未信任的 source path；路径归一化、trusted root 校验和
> `file://` URI 构造由后续 Coordinator 完成。

- [x] **Step 1：写出 pass/fail/skip/partial 失败测试**

覆盖：

- `-v` item start；
- pass；
- IGNORE_TEST skipped；
- assertion failure；
- memory leak failure；
- source file/line；
- summary 与 observed records 一致；
- nonzero + assertion；
- nonzero 无 assertion；
- zero + assertion；
- crash partial；
- malformed/缺失 summary；
- stdout/stderr interleave；
- ANSI、CRLF、chunk 和 UTF-8。

- [x] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'Parser|Result|Summary' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 evidence-based parser**

只在完整 record 后发出 terminal item event。Summary mismatch 产生 `framework_output_invalid`；已完整结果保留 `partial=true`，其余 `not_run`。

- [x] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [x] **Step 5：提交 CppUTest result parser**

```powershell
git add apps/test-service/internal/testframework/cpputest
git commit -m "feat: parse cpputest results"
```

### Task 4：CppUMock failure details 与多位置

**文件：**

- 创建：`apps/test-service/internal/testframework/cpputest/mock.go`
- 创建：`apps/test-service/internal/testframework/cpputest/mock_test.go`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/mock-unexpected-call.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/mock-missing-call.txt`
- 创建：`apps/test-service/internal/testframework/cpputest/testdata/mock-parameter-mismatch.txt`
- 修改：`apps/test-service/internal/testframework/cpputest/parser.go`
- 修改：`apps/test-service/internal/testdomain/model.go`
- 修改：`apps/test-service/internal/testdomain/model_test.go`
- 修改：`apps/test-service/internal/testframework/adapter.go`
- 修改：`packages/protocol-schema/schema/v1.3/test.schema.json`
- 修改：`packages/protocol-schema/fixtures/v1.3/test-result.valid.json`
- 修改：`packages/protocol-schema/test/schema.test.mjs`
- 修改：Protocol v1.3 generated Go/TypeScript models 与 export/compile tests
- 修改：`packages/test-client/src/index.ts`
- 修改：`packages/test-client/src/client.test.ts`
- 修改：`apps/test-service/internal/testdomain/errors.go`

> 实施修正：已确认设计要求保留结构化 Mock detail subtype，但当前 Protocol v1.3
> `failureDetail` 缺少承载字段。项目尚未发布，本 Task 增加可选的 closed
> `TestFailureSubtypeV13` enum；不带 subtype 的既有 v1.3 消息仍有效，Service
> 产生的 Mock detail 则不会在跨进程边界丢失类型。

- [x] **Step 1：写出 CppUMock subtype/detail 失败测试**

覆盖：

- unexpected call；
- missing expected call；
- parameter mismatch；
- expected/actual 可提取时结构化；
- 无法结构化时保留 message；
- test declaration 与 actual call 多位置；官方输出未携带 expectation call site 时不伪造位置；
- 所有 mock failure 仍是 `assertion_failure`；
- memory address/ANSI 不进入 identity。

- [x] **Step 2：运行 Mock tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testdomain -run 'Mock|FailureDetail' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 best-effort detail parser**

基础 subtype、message 和首个 source location 是必需结果；expected/actual 和附加位置是可选字段。字段解析失败不能丢失原 assertion。

- [x] **Step 4：运行 CppUTest Adapter 全套**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testdomain -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [x] **Step 5：提交 CppUMock details**

```powershell
git add apps/test-service/internal/testframework apps/test-service/internal/testdomain `
  apps/test-service/internal/protocolmodel/v1_3/test packages/protocol-schema `
  packages/protocol-models packages/test-client
git commit -m "feat: normalize cppumock failures"
```

### Task 5：deterministic fake executable integration 与 Registry

**文件：**

- 创建：`apps/test-service/cmd/test-framework-fixture/main.go`
- 创建：`apps/test-service/cmd/test-framework-fixture/main_test.go`
- 验证：`apps/test-service/internal/testframework/registry.go`
- 验证：`apps/test-service/internal/testframework/registry_test.go`
- 修改：`apps/test-service/internal/testframework/adapter.go`
- 修改：`apps/test-service/internal/testframework/cpputest/adapter.go`
- 创建：`apps/test-service/internal/testframework/cpputest/framework.go`
- 创建：`apps/test-service/internal/testframework/cpputest/environment.go`
- 创建：`apps/test-service/internal/testframework/cpputest/integration_test.go`
- 创建：`apps/test-service/internal/testframework/cpputest/fixture_process_test.go`
- 修改：`tools/service-probe/build-service.mjs`

> 实施修正：通用 `RunInput` 原先只有 case 列表，无法无歧义表达“全部 / group /
> 精确 cases”。本 Task 增加 closed framework-neutral run mode；CppUTest Adapter
> 不根据 case 数量猜测 mode，group 名只从已展开的稳定 `RunItem` 派生，不新增
> Client 自由文本入口。
>
> Registry 已提供显式 Adapter 注入，不在父 package 导入 `cpputest` 子 package，
> 避免形成循环依赖；concrete registration 由集成测试和后续 Runtime composition
> 验证。

- [x] **Step 1：写出 list/run/crash/timeout integration 失败测试**

Fixture 只实现固定 CppUTest-like scenarios，不启动外部命令。测试验证 Registry → Verify → Discover → Plan → Parser 的完整内部切片。

- [x] **Step 2：运行 integration tests 并确认失败**

```powershell
go test ./apps/test-service/cmd/test-framework-fixture ./apps/test-service/internal/testframework/... -run 'Fixture|Integration' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 fixture 与 Adapter registration**

Fixture 未知参数退出 2；scenario 来自构建时固定表，不接受 command/env/cwd。

- [x] **Step 4：运行 Adapter 全套和完整 Go tests**

```powershell
go test ./apps/test-service/internal/testframework/... ./apps/test-service/cmd/test-framework-fixture -count=1
go test -race ./apps/test-service/internal/testframework/... -count=1
pnpm test:go
```

预期：PASS。

- [x] **Step 5：提交 CppUTest integration**

```powershell
git add apps/test-service/internal/testframework apps/test-service/cmd/test-framework-fixture tools/service-probe/build-service.mjs
git commit -m "feat: integrate cpputest adapter"
```

## Phase 4C 完成检查

- [x] `-ln` valid/malformed/chunk tests
- [x] exact group/case argv tests
- [x] pass/fail/skip/crash/timeout evidence tests
- [x] CppUMock subtype/detail/source tests
- [x] partial result 不伪造 pass
- [x] unknown executable 不探测
- [x] Go unit/race tests
- [x] `pnpm verify`
- [x] 独立评审确认 Client 文本不能进入 CppUTest argv
