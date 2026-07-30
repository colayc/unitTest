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

**接口：**

```go
func ParseList(reader io.Reader, limits Limits) ([]CaseIdentity, error)
```

- [ ] **Step 1：写出 list grammar 失败测试**

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

- [ ] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'ParseList|Discovery' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现有界 tokenizer**

Parser 不使用无限长 `bufio.Scanner` 默认 token，也不根据输出中出现 `CppUTest` 单词判断成功。完整输入关闭且全部 token 合法后才返回 cases。

- [ ] **Step 4：运行 unit/fuzz seed**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [ ] **Step 5：提交 CppUTest discovery**

```powershell
git add apps/test-service/internal/testframework/cpputest
git commit -m "feat: discover cpputest cases"
```

### Task 2：exact group/case RunPlan 与保留参数

**文件：**

- 创建：`apps/test-service/internal/testframework/cpputest/planner.go`
- 创建：`apps/test-service/internal/testframework/cpputest/planner_test.go`
- 修改：`apps/test-service/internal/testframework/cpputest/adapter.go`
- 修改：`apps/test-service/internal/testframework/registry_test.go`

**接口：**

```go
func BuildRunPlan(descriptor ctest.ExecutionDescriptor, selection Selection) (testframework.RunPlan, error)
```

- [ ] **Step 1：写出受控参数和 conflict 失败测试**

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

- [ ] **Step 2：运行 planner tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'RunPlan|Reserved' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 immutable RunPlan**

Adapter 返回 framework-neutral RunPlan，不直接启动 process。每个 plan 记录 selection item ID 与预期 case boundary，供结果 parser 交叉验证。

- [ ] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testframework -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [ ] **Step 5：提交 CppUTest planner**

```powershell
git add apps/test-service/internal/testframework/cpputest apps/test-service/internal/testframework/registry_test.go
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

**接口：**

```go
type Parser struct
func (p *Parser) Feed(stream string, data []byte) []testframework.ResultEvent
func (p *Parser) Close(termination processcontrol.Termination) testframework.ParseResult
```

- [ ] **Step 1：写出 pass/fail/skip/partial 失败测试**

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

- [ ] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -run 'Parser|Result|Summary' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 evidence-based parser**

只在完整 record 后发出 terminal item event。Summary mismatch 产生 `framework_output_invalid`；已完整结果保留 `partial=true`，其余 `not_run`。

- [ ] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework/cpputest -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [ ] **Step 5：提交 CppUTest result parser**

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
- 修改：`apps/test-service/internal/testframework/cpputest/parser_test.go`
- 修改：`apps/test-service/internal/testdomain/model.go`
- 修改：`apps/test-service/internal/testdomain/model_test.go`

- [ ] **Step 1：写出 CppUMock subtype/detail 失败测试**

覆盖：

- unexpected call；
- missing expected call；
- parameter mismatch；
- expected/actual 可提取时结构化；
- 无法结构化时保留 message；
- expectation declaration 与 actual call 多位置；
- 所有 mock failure 仍是 `assertion_failure`；
- memory address/ANSI 不进入 identity。

- [ ] **Step 2：运行 Mock tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testdomain -run 'Mock|FailureDetail' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 best-effort detail parser**

基础 subtype、message 和首个 source location 是必需结果；expected/actual 和附加位置是可选字段。字段解析失败不能丢失原 assertion。

- [ ] **Step 4：运行 CppUTest Adapter 全套**

```powershell
go test ./apps/test-service/internal/testframework/cpputest ./apps/test-service/internal/testdomain -count=1
go test -race ./apps/test-service/internal/testframework/cpputest -count=1
```

预期：PASS。

- [ ] **Step 5：提交 CppUMock details**

```powershell
git add apps/test-service/internal/testframework/cpputest apps/test-service/internal/testdomain
git commit -m "feat: normalize cppumock failures"
```

### Task 5：deterministic fake executable integration 与 Registry

**文件：**

- 创建：`apps/test-service/cmd/test-framework-fixture/main.go`
- 创建：`apps/test-service/cmd/test-framework-fixture/main_test.go`
- 修改：`apps/test-service/internal/testframework/registry.go`
- 修改：`apps/test-service/internal/testframework/registry_test.go`
- 创建：`apps/test-service/internal/testframework/cpputest/integration_test.go`
- 修改：`tools/service-probe/build-service.mjs`

- [ ] **Step 1：写出 list/run/crash/timeout integration 失败测试**

Fixture 只实现固定 CppUTest-like scenarios，不启动外部命令。测试验证 Registry → Verify → Discover → Plan → Parser 的完整内部切片。

- [ ] **Step 2：运行 integration tests 并确认失败**

```powershell
go test ./apps/test-service/cmd/test-framework-fixture ./apps/test-service/internal/testframework/... -run 'Fixture|Integration' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 fixture 与 Adapter registration**

Fixture 未知参数退出 2；scenario 来自构建时固定表，不接受 command/env/cwd。

- [ ] **Step 4：运行 Adapter 全套和完整 Go tests**

```powershell
go test ./apps/test-service/internal/testframework/... ./apps/test-service/cmd/test-framework-fixture -count=1
go test -race ./apps/test-service/internal/testframework/... -count=1
pnpm test:go
```

预期：PASS。

- [ ] **Step 5：提交 CppUTest integration**

```powershell
git add apps/test-service/internal/testframework apps/test-service/cmd/test-framework-fixture tools/service-probe/build-service.mjs
git commit -m "feat: integrate cpputest adapter"
```

## Phase 4C 完成检查

- [ ] `-ln` valid/malformed/chunk tests
- [ ] exact group/case argv tests
- [ ] pass/fail/skip/crash/timeout evidence tests
- [ ] CppUMock subtype/detail/source tests
- [ ] partial result 不伪造 pass
- [ ] unknown executable 不探测
- [ ] Go unit/race tests
- [ ] `pnpm verify`
- [ ] 独立评审确认 Client 文本不能进入 CppUTest argv
