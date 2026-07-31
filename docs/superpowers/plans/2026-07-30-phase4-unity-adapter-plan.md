# Phase 4D：Unity/CMock helper、runner 与 Adapter 实施计划

> 实施时逐 Task 使用 red-green-refactor TDD；每个 Step 完成后更新 checkbox。

**目标：** 提供无 Ruby/Ceedling 运行时依赖的 opt-in Unity CMake helper、deterministic Go runner generator、`utide.runner.v1` control protocol 和 Unity/CMock Adapter。

**架构：** `unity-runner-generator` 只读取 helper 显式声明的 `TEST_SOURCES`，生成 runner C 和 manifest。Service 通过 CTest helper metadata 选择 Unity Adapter；list/exact run 的结构化记录写入 Service-owned result file，stdout/stderr 保留 Unity/CMock 原始日志。

**依赖：** Phase 4B。

## 全局约束

- 不对未 opt-in Project 扫描 Unity source。
- generator 不生成 CMock，不要求 Ruby 或 Ceedling。
- helper 不接受 COMMAND、ARGS、ENVIRONMENT、WORKING_DIRECTORY 或 hook。
- generator path 来自产品 installation manifest，并进入 configure fingerprint。
- generated output 必须 deterministic。
- runner result file 必须位于当前 Task artifact directory。
- 每条 JSON Lines record 后 flush；crash 后只接受完整 record。

---

### Task 1：Unity source grammar 与 manifest model

**文件：**

- 创建：`apps/test-service/internal/unityrunner/model.go`
- 创建：`apps/test-service/internal/unityrunner/model_test.go`
- 创建：`apps/test-service/internal/unityrunner/parser.go`
- 创建：`apps/test-service/internal/unityrunner/parser_test.go`
- 创建：`apps/test-service/internal/unityrunner/testdata/basic.c`
- 创建：`apps/test-service/internal/unityrunner/testdata/parameterized.c`
- 创建：`apps/test-service/internal/unityrunner/testdata/unsupported.c`
- 创建：`apps/test-service/internal/unityrunner/testdata/duplicate.c`

**接口：**

```go
func ParseSources(root string, sources []string, limits Limits) (Manifest, error)
```

- [x] **Step 1：写出 declared-source grammar 失败测试**

覆盖：

- standard `setUp`、`tearDown` 和 `test_*`；
- official generator 支持的 `TEST_CASE`/`TEST_RANGE` 形式；
- source-relative location 和 line；
- comments/string literal 中的伪测试忽略；
- conditional/preprocessor 边界明确拒绝；
- duplicate logical identity；
- source symlink/path escape；
- Unicode/空格路径；
- 超大 source、超长 case、过多 parameter instance；
- input order 不影响 manifest order/hash。

- [x] **Step 2：运行 parser tests 并确认失败**

```powershell
go test ./apps/test-service/internal/unityrunner -run 'ParseSources|Manifest' -count=1
```

预期：FAIL。

- [x] **Step 3：实现受控 lexer/parser**

不使用简单 `test_*` regex 扫描完整文件。Parser 只支持设计明确列出的 Unity grammar；不支持形式返回 source diagnostic，不猜测。

- [x] **Step 4：运行 unit/fuzz seed**

```powershell
go test ./apps/test-service/internal/unityrunner -count=1
```

预期：PASS。

- [x] **Step 5：提交 Unity manifest parser**

```powershell
git add apps/test-service/internal/unityrunner
git commit -m "feat: parse declared unity test sources"
```

### Task 2：deterministic runner C generator

**文件：**

- 创建：`apps/test-service/internal/unityrunner/generator.go`
- 创建：`apps/test-service/internal/unityrunner/generator_test.go`
- 创建：`apps/test-service/internal/unityrunner/template.go`
- 创建：`apps/test-service/internal/unityrunner/json_escape_test.go`
- 创建：`apps/test-service/internal/unityrunner/testdata/basic.runner.golden.c`
- 创建：`apps/test-service/internal/unityrunner/testdata/basic.manifest.golden.json`

**生成接口：**

```go
func Generate(input GenerateInput) (runnerC []byte, manifestJSON []byte, err error)
```

- [x] **Step 1：写出 deterministic C/manifest 失败测试**

覆盖：

- 同输入 byte-for-byte 相同；
- source 输入顺序不同结果相同；
- generated dispatch table 只包含 manifest case；
- exact identity 无 substring match；
- list mode 不执行 test；
- result JSON escape；
- NUL/control character 拒绝；
- case 数量/manifest 大小上限；
- generator version 写入 C 与 manifest；
- absolute source/build path 不写入输出。

- [x] **Step 2：运行 generator tests 并确认失败**

```powershell
go test ./apps/test-service/internal/unityrunner -run 'Generate|Golden|Escape' -count=1
```

预期：FAIL。

- [x] **Step 3：实现 embedded template 与 atomic output**

Generated runner：

- parse 固定 `utide.runner.v1` flags；
- `list` 输出完整 case records；
- `run` 只 dispatch exact identity；
- 调用 `UnityBegin`、目标 test、`UnityEnd`；
- 根据 Unity globals 写 passed/failed/skipped；
- 每条 result record 后 `fflush`；
- unknown/missing control flag 退出固定 code；
- stdout/stderr 不混入 control JSON。

Generator 返回 bytes；CLI 负责 temp + fsync + atomic replace。

- [x] **Step 4：运行 Golden 和 race**

```powershell
go test ./apps/test-service/internal/unityrunner -count=1
go test -race ./apps/test-service/internal/unityrunner -count=1
```

预期：PASS，第二次 generation 无 diff。

- [x] **Step 5：提交 Unity generator**

```powershell
git add apps/test-service/internal/unityrunner
git commit -m "feat: generate deterministic unity runners"
```

### Task 3：generator CLI 与产品 installation identity

**文件：**

- 创建：`apps/test-service/cmd/unity-runner-generator/main.go`
- 创建：`apps/test-service/cmd/unity-runner-generator/main_test.go`
- 修改：`apps/test-service/internal/cmake/manifest.go`
- 修改：`apps/test-service/internal/cmake/manifest_test.go`
- 修改：`apps/test-service/internal/cmake/fingerprint.go`
- 修改：`apps/test-service/internal/cmake/fingerprint_test.go`
- 修改：`apps/test-service/internal/build/planner.go`
- 修改：`apps/test-service/internal/build/planner_test.go`

**CLI：**

```text
unity-runner-generator --version=json-v1
unity-runner-generator generate
  --workspace-root <validated>
  --build-root <validated>
  --manifest <derived>
  --runner <derived>
  --source <validated-repeatable>
```

- [ ] **Step 1：写出 CLI/path/fingerprint 失败测试**

覆盖：

- unknown flag 拒绝；
- output/source escape 拒绝；
- symlink/junction swap；
- output no-overwrite/atomic replace；
- generator executable identity 来自 product manifest；
- configure fingerprint 包含 generator identity；
- Preset/generated configure 都只注入保留 cache variable；
- Protocol 不能指定 generator path。

- [ ] **Step 2：运行 CLI/CMake tests 并确认失败**

```powershell
go test ./apps/test-service/cmd/unity-runner-generator ./apps/test-service/internal/cmake ./apps/test-service/internal/build -run 'UnityRunner|GeneratorIdentity' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 CLI 与 manifest binding**

CLI 使用与 Service 相同的 canonical root 类型。Product manifest 中 generator entry 固定 relative path、version、SHA-256 和 platform；不从 PATH 搜索。

- [ ] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/cmd/unity-runner-generator ./apps/test-service/internal/cmake ./apps/test-service/internal/build -count=1
go test -race ./apps/test-service/internal/unityrunner -count=1
```

预期：PASS。

- [ ] **Step 5：提交 generator CLI**

```powershell
git add apps/test-service/cmd/unity-runner-generator apps/test-service/internal/cmake apps/test-service/internal/build
git commit -m "feat: bind unity generator to product manifest"
```

### Task 4：`sdk/cmake/UnitTestIDE.cmake`

**文件：**

- 创建：`sdk/cmake/UnitTestIDE.cmake`
- 创建：`sdk/cmake/README.md`
- 创建：`testdata/frameworks/helper-smoke/CMakeLists.txt`
- 创建：`testdata/frameworks/helper-smoke/cpputest_main.cpp`
- 创建：`testdata/frameworks/helper-smoke/unity_tests.c`
- 创建：`tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs`
- 修改：`package.json`

**公开入口：**

```cmake
unit_test_ide_add_cpputest(TEST <name> TARGET <executable-target>)
unit_test_ide_add_unity_test(
  TEST <name>
  TARGET <executable-target>
  TEST_SOURCES <source>...
)
```

- [ ] **Step 1：写出 CMake helper smoke 失败测试**

覆盖：

- CppUTest add_test + labels；
- Unity generated source + manifest + labels；
- duplicate test name；
- wrong/non-executable target；
- Unity target 已有 main；
- missing/wrong generator version；
- source escape；
- multi-config output；
- helper 拒绝未解析 keyword 和 unsafe keyword；
- configure 两次 output deterministic。

- [ ] **Step 2：运行 smoke 并确认失败**

```powershell
pnpm test:workspace
```

预期：FAIL，helper 尚不存在。

- [ ] **Step 3：实现 strict CMake functions**

使用 `cmake_parse_arguments` 后检查 `UNPARSED_ARGUMENTS`。CTest labels 固定为：

```text
utide.framework.cpputest
utide.framework.unity
utide.runner.v1
```

helper 不设置 Project environment/working directory，不接受 raw command。

- [ ] **Step 4：运行 smoke 与 diff check**

```powershell
pnpm test:workspace
git diff --check
```

预期：PASS。

- [ ] **Step 5：提交 CMake helper**

```powershell
git add sdk/cmake testdata/frameworks/helper-smoke tools/workspace-smoke package.json
git commit -m "feat: add opt-in test cmake helper"
```

### Task 5：`utide.runner.v1` control parser 与 Unity Adapter

**文件：**

- 创建：`apps/test-service/internal/testframework/unity/adapter.go`
- 创建：`apps/test-service/internal/testframework/unity/adapter_test.go`
- 创建：`apps/test-service/internal/testframework/unity/protocol.go`
- 创建：`apps/test-service/internal/testframework/unity/protocol_test.go`
- 创建：`apps/test-service/internal/testframework/unity/planner.go`
- 创建：`apps/test-service/internal/testframework/unity/planner_test.go`
- 创建：`apps/test-service/internal/testframework/unity/testdata/list.jsonl`
- 创建：`apps/test-service/internal/testframework/unity/testdata/fail.jsonl`
- 创建：`apps/test-service/internal/testframework/unity/testdata/crash-partial.jsonl`
- 创建：`apps/test-service/internal/testframework/unity/testdata/malformed.jsonl`
- 修改：`apps/test-service/internal/testframework/registry.go`
- 修改：`apps/test-service/internal/testframework/registry_test.go`

- [ ] **Step 1：写出 protocol/list/exact-run 失败测试**

覆盖：

- magic/version；
- list 与 manifest cross-check；
- exact identity；
- result path Service-owned；
- record 256 KiB 上限；
- complete flush record；
- partial final JSON 丢弃；
- pass/fail/skip；
- zero/nonzero inconsistency；
- crash partial；
- source location；
- runner/executable/manifest fingerprint mismatch；
- reserved control arg conflict。

- [ ] **Step 2：运行 Unity Adapter tests 并确认失败**

```powershell
go test ./apps/test-service/internal/testframework/unity ./apps/test-service/internal/testframework -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 Adapter 与 Service-owned RunPlan**

Control argv 只由 Adapter 常量、manifest case identity、contract version 和 ArtifactStore 分配的 result path组成。Client/Workspace mapping 不能提供 flag 或 path。

- [ ] **Step 4：运行 unit/race**

```powershell
go test ./apps/test-service/internal/testframework/unity ./apps/test-service/internal/testframework ./apps/test-service/internal/unityrunner -count=1
go test -race ./apps/test-service/internal/testframework/unity ./apps/test-service/internal/unityrunner -count=1
```

预期：PASS。

- [ ] **Step 5：提交 Unity Adapter**

```powershell
git add apps/test-service/internal/testframework/unity apps/test-service/internal/testframework/registry.go apps/test-service/internal/testframework/registry_test.go
git commit -m "feat: integrate unity runner protocol"
```

### Task 6：CMock failure detail 与 generated C compile integration

**文件：**

- 创建：`apps/test-service/internal/testframework/unity/output.go`
- 创建：`apps/test-service/internal/testframework/unity/output_test.go`
- 创建：`apps/test-service/internal/testframework/unity/testdata/cmock-fail.txt`
- 创建：`apps/test-service/internal/unityrunner/compile_test.go`
- 修改：`apps/test-service/internal/testframework/unity/adapter.go`
- 修改：`apps/test-service/internal/testdomain/model.go`
- 修改：`apps/test-service/internal/testdomain/model_test.go`

- [ ] **Step 1：写出 CMock detail 和 generated C compile 失败测试**

覆盖：

- generated runner 可用当前平台 C compiler + minimal Unity stub 编译；
- list 不执行 test；
- exact dispatch；
- JSON escape；
- CMock expectation failure subtype/message/location；
- expected/actual 可选；
- stdout malformed 不覆盖 control-file status；
- control-file failure 与 stdout assertion 不一致时 `framework_output_invalid`。

- [ ] **Step 2：运行 integration tests 并确认失败**

```powershell
go test ./apps/test-service/internal/unityrunner ./apps/test-service/internal/testframework/unity ./apps/test-service/internal/testdomain -run 'Compile|CMock|ControlConsistency' -count=1
```

预期：FAIL。

- [ ] **Step 3：实现 output enrichment**

Control file 是 status 事实来源；stdout parser 只补充 failure detail/source。无法结构化 CMock detail 时保留原 message，不改变 assertion。

- [ ] **Step 4：运行 Unity 全套与完整 Go tests**

```powershell
go test ./apps/test-service/internal/unityrunner ./apps/test-service/internal/testframework/unity ./apps/test-service/internal/testdomain -count=1
go test -race ./apps/test-service/internal/unityrunner ./apps/test-service/internal/testframework/unity -count=1
pnpm test:go
```

预期：PASS。

- [ ] **Step 5：提交 Unity/CMock integration**

```powershell
git add apps/test-service/internal/unityrunner apps/test-service/internal/testframework/unity apps/test-service/internal/testdomain
git commit -m "feat: normalize unity and cmock results"
```

## Phase 4D 完成检查

- [ ] declared-source parser 不扫描未 opt-in Project
- [ ] deterministic runner/manifest Golden File
- [ ] helper strict keyword/security tests
- [ ] generated C Windows/Linux compile tests
- [ ] list/exact-run protocol tests
- [ ] pass/fail/skip/crash/partial tests
- [ ] CMock detail tests
- [ ] 无 Ruby/Ceedling runtime dependency
- [ ] Go unit/race、workspace smoke、`pnpm verify`
- [ ] 独立评审确认 result path 和 control argv 只能由 Service 生成
