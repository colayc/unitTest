# Phase 5：覆盖率与报告管道设计

**日期：** 2026-08-03

**状态：** 已确认

**目标分支：** `codex/workspace-cmake-toolchains`

**基础提交：** `69dd3d88d921ebf3e3ab197b6eae6c378b4816cc`

## 1. 背景

Phase 1 已建立版本化 JSON Protocol、TypeScript Client、Go 本地 Service、per-user IPC、token handshake 和协议协商。

Phase 2 已建立 Task Engine、跨平台进程树控制、取消与超时、事件重放、SQLite 历史记录和 ArtifactStore。Protocol 不能提交 executable、参数数组、环境变量、工作目录或 Shell。

Phase 3 已建立受信任 Workspace、CMake Presets、CMake File API、Build Profile、Target、Build Coordinator，以及 MSVC、clang-cl、GCC、Clang 的 Toolchain Adapter。clang-cl、Clang 和 GCC Adapter 已保存 coverage capability；Windows clang-cl 会验证同一 LLVM installation 中的 `llvm-profdata` 与 `llvm-cov`。

Phase 4 已建立 CTest、CppUTest/CppUMock、Unity/CMock、稳定 Test ID、Test Catalog、TestRun、结构化选择、失败重跑、结果解析、源码位置、Service-owned continuation 和 Protocol v1.3。

Phase 5 在这些边界上增加覆盖率构建、profile 采集、跨工具规范化和报告导出。覆盖率不是客户端可注入的额外 compiler flags，而是由 Service 根据已验证的 Build Profile、Toolchain capability 和固定 Adapter contract 生成的受控执行计划。

## 2. 已确认的决策

1. Phase 5 同时设计 Windows clang-cl/llvm-cov、Linux GCC/gcovr 和 Linux Clang/llvm-cov，不采用单平台临时模型。
2. coverage build 使用独立目录，绝不复用普通 MSVC、GCC 或 Clang build tree。
3. coverage build identity 由 workspace/project、base profile、toolchain、driver、instrumentation template 和 CMake identity 决定。
4. Linux GCC 所需的 Python runtime、gcovr 及依赖由产品固定并捆绑；产品运行时不联网，也不读取系统 Python package。
5. 采用 Service-native 双 Adapter：LLVM 与 GCC 各自解析原生中间 JSON，再统一为产品 Coverage JSON。
6. 产品 Coverage JSON 是 coverage metric、源码映射和工具 provenance 的唯一事实来源；JUnit 的 testcase outcome 只来自关联 TestRun。第三方 JSON 不直接暴露给客户端。
7. Coverage JSON 使用独立 schema version，不与 Protocol version 绑定。
8. JSON、JUnit XML 和单文件 HTML 都由产品生成，不长期保存工具原生 HTML/XML。
9. Phase 5 只负责准确采集、合并、展示和导出，不加入 coverage percentage threshold 或质量门禁。
10. Phase 5 允许受限的 workspace-relative include/exclude glob，但不接受 regex、原生绝对路径或 compiler 参数。
11. `CoverageRun` 与 `TestRun` 分离：测试断言失败不等于覆盖率基础设施失败。
12. 同一次“Run with Coverage”只创建一个顶层 Task；build、test、collect、normalize 和 report 通过 Service-owned plan/continuation 完成，不创建嵌套 Task。
13. LLVM raw profile 没有长期兼容承诺，`.profraw`、`.profdata`、`.gcda` 和第三方 JSON 只作为当前 Task 的中间数据。
14. Phase 5 通过 Protocol v1.4 暴露；v1.0–v1.3 保持兼容并隐藏 coverage-only entity。
15. Phase 5 提交继续同步到 GitHub 与 Gitee；最终产品运行时不依赖两个代码托管平台。

## 3. 目标

- 为 Windows clang-cl 和 Linux Clang 生成受控 LLVM source-based coverage build。
- 为 Linux GCC 生成受控 gcov coverage build。
- 使用实际测试选择执行 CppUTest/CppUMock 与 Unity/CMock，并采集相应 profile。
- 合并 profile，生成可复现的源码、行、分支、函数和总体 summary。
- 将不同工具输出规范化为统一、版本化、路径安全的 Coverage JSON。
- 从统一模型生成 JSON、JUnit XML 和单文件离线 HTML。
- 显示实际 compiler、coverage driver、collector 和完整版本。
- 明确区分 test failure、partial coverage、coverage infrastructure failure、cancel 和 timeout。
- 复用 Task Engine 的取消、超时、事件排序、重连、恢复和 ArtifactStore。
- 保持 Protocol 到 `ProcessSpec` 的单向安全边界。
- 为 Phase 6/7 的 Testing API、coverage tree、源码 decoration 和报告视图提供稳定接口。

## 4. 非目标

- coverage percentage threshold、增量覆盖率阈值或合并请求质量门禁；这些属于 Phase 7 的运行配置与 UX。
- Code-OSS coverage tree、源码 decoration、历史报告页面或报告比较；这些属于 Phase 6/7。
- MSVC `cl.exe` 原生覆盖率。Windows coverage 明确代表 clang-cl 构建。
- macOS、IAR、Keil、GoogleTest 或其他新增平台、工具链、框架。
- MC/DC、path coverage、call coverage 或 LLVM region 的跨工具统一语义。
- PGO、SanitizerCoverage、fuzzing coverage 或性能 profile。
- 从产品运行时下载 Python、gcovr、LLVM、GCC 或其他工具。
- 接受 workspace 提供的 gcovr config、Python script/plugin、collector 参数或环境变量。
- 长期保存 raw profile，或承诺第三方 raw/profile JSON 的兼容性。
- 对恶意测试二进制提供操作系统级沙箱。
- Phase 8 的最终安装包、自动更新、签名和回滚集成。

## 5. 总体架构

```text
TypeScript Client / Protocol v1.4
                │
                │ workspace/project/profile/test selection
                ▼
Go Session / Coverage Runtime
                │
                ▼
        Coverage Coordinator
         ├─ Coverage Build Planner ──► CMake / Toolchain
         ├─ Test Coordinator ────────► CTest / Framework Adapter
         ├─ LLVM Adapter ────────────► profraw / profdata / llvm-cov JSON
         ├─ GCC Adapter ─────────────► gcda / gcov / gcovr JSON
         ├─ Coverage Normalizer ─────► Coverage JSON v1
         ├─ Report Renderer ─────────► JUnit XML / single-file HTML
         └─ Coverage Store ──────────► SQLite metadata / ArtifactStore
```

### 5.1 `CoverageCoordinator`

`CoverageCoordinator` 是 coverage Task 的唯一 plan/continuation provider。它：

- 解析并验证结构化 ID；
- 绑定 Workspace generation、Project、Build Profile、Toolchain 和 Test Catalog revision；
- 校验 driver capability；
- 创建 CoverageRun 与关联 TestRun；
- 生成 coverage configure/build、测试运行、profile merge、normalize 和 report step；
- 在每个 continuation wave 进入 Task Engine 前重新经过 `ExecutionBoundary`；
- 负责 CoverageRun 的领域事件、终态和 completeness。

### 5.2 `CoverageBuildPlanner`

`CoverageBuildPlanner` 从已验证的 base Build Profile 派生 coverage build identity 和隔离目录。它只能选择固定的 instrumentation template，不能接收 raw flags。

### 5.3 Tool Adapter

`LLVMCoverageAdapter` 负责 Windows clang-cl 与 Linux Clang；`GCCCoverageAdapter` 负责 Linux GCC。两个 Adapter 输出统一的内部 `CoverageSnapshot`，但各自保留独立的工具发现、参数生成、版本校验、parser 和 Golden fixture。

### 5.4 `CoverageNormalizer`

Normalizer 只接收 Adapter 已解析的 bounded domain object，不直接读取任意第三方文件。它负责 URI、源码 digest、summary、排序、计数上限、common semantics 和 deterministic serialization。

### 5.5 `CoverageReportRenderer`

Renderer 从统一 Coverage JSON 与关联 TestRun projection 生成 JUnit XML 和 HTML。HTML 的 CSS/JavaScript 是 build-time 生成并嵌入 Go Service 的固定资源；产品运行时不需要 Node。

### 5.6 `CoverageStore`

SQLite 只保存 CoverageRun、report metadata、工具版本、summary、状态和 artifact 关联。逐文件、逐行数据保存在 ArtifactStore，不能展开为大量数据库 event。

## 6. 领域语义

### 6.1 `CoverageRun`

`CoverageRun` 与 `TestRun` 是不同 aggregate，但可以由同一 Task 拥有：

```text
CoverageRun
├─ taskId
├─ testRunId
├─ requestSnapshot
├─ buildIdentity
├─ toolchain/tool versions
├─ status
├─ outcome/completeness
├─ summary
└─ artifact IDs
```

`CoverageRun.status`：

- `queued`
- `running`
- `finished`

`CoverageRun.outcome`：

- `available`：有效报告已生成，所有正常完成的 invocation 均满足 profile 预期。
- `partial`：有效报告已生成，但 crash、timeout 或其他已记录的 test outcome 使部分 profile 无法写出。
- `unavailable`：插桩、合并、解析、规范化、报告或持久化失败。
- `cancelled`：用户取消或 Task timeout 后停止，不强制继续生成报告。

`cancelled` 使用结构化 reason 区分 `user_cancelled` 与 `task_timed_out`。Service restart 使 running run 进入 `unavailable/service_restarted`，而不是 `cancelled`。

### 6.2 Task 与 TestRun 映射

- assertion failure：Task 可 `succeeded`，TestRun 为 `failed`，CoverageRun 可 `available`。
- test crash/timeout：TestRun 保存 crash/timeout；CoverageRun 可以 `partial`，Task 在 coverage pipeline 正常完成时仍可 `succeeded`。
- 正常退出的 instrumented executable 缺少预期 profile：CoverageRun `unavailable`，Task `infrastructure_failed`。
- merge/parser/report failure：CoverageRun `unavailable`，Task `infrastructure_failed`。
- 用户取消：Task `cancelled`，CoverageRun `cancelled/user_cancelled`。
- Task timeout：Task `timed_out`，CoverageRun `cancelled/task_timed_out`。

测试退出码不能单独决定 coverage completeness；Adapter 必须结合 TestRun evidence、process outcome 和 profile manifest。

## 7. Coverage request 与配置

### 7.1 Workspace coverage profile

Workspace Schema 增加有界的 `coverageProfiles`：

```json
{
  "id": "coverage-debug",
  "baseBuildProfileId": "debug-clang",
  "include": ["src/**", "include/**"],
  "exclude": ["third_party/**", "tests/**"]
}
```

约束：

- `id` 遵循既有 stable ID 长度和字符规则；
- `baseBuildProfileId` 必须引用当前 Project 可用的 Build Profile；
- `include`/`exclude` 是 workspace-relative POSIX glob；
- glob 只支持 `*`、`?`、`**` 和字面字符，不支持 regex、brace expansion、命令替换或原生分隔符；
- 总项数、单项长度和编译后的 matcher state 都有上限；
- `..`、absolute path、URI scheme、NUL 和 path escape 被拒绝；
- 未配置 include 时默认包含 Project source root；
- coverage build root、Service data root、`.git` 和 workspace 外部文件总是排除，用户配置不能覆盖。

### 7.2 Protocol start request

`coverage/runs/start` 只包含：

- workspace/project/coverage profile ID；
- Phase 4 结构化 selection；
- repeat 与已有有界 timeout option；
- idempotency key。

Driver 根据 Toolchain family/capability 唯一确定：clang-cl/Clang 使用 `llvm-cov`，GCC 使用 `gcov`，并由固定的 gcovr processor 生成中间 JSON。Request 不能覆盖 driver、processor 或 report format；每次成功的 CoverageRun 固定生成三种报告。

## 8. Coverage build identity 与目录所有权

Coverage build identity 的 canonical input：

- Workspace root identity 与 Project ID；
- Project source directory；
- base Build Profile canonical snapshot；
- Toolchain ID、compiler identity 和 SDK/sysroot identity；
- generator、configuration、CMake installation identity；
- coverage driver；
- instrumentation template version 与 SHA-256；
- 已验证的 preset/generator inputs。

identity 不包含每次变化的源码内容，因此相同配置允许增量 build。Toolchain、profile、CMake、driver 或 template 变化会产生新目录。

coverage build directory 位于 Service-owned data root，并具有：

- 独占 lease；
- manifest-bound identity；
- workspace/project ownership；
- configure/build 前后 identity 复验；
- 与普通 build tree 不同的 path 和 lock namespace。

同一 coverage build identity 首期只允许一个 running CoverageRun。GCC 必须串行运行 test invocation；LLVM 可以使用 bounded concurrency，但每个 invocation 必须拥有唯一 profile pattern。

## 9. CMake instrumentation

Service 生成只读、带 digest 的 CMake include，并通过 `CMAKE_PROJECT_TOP_LEVEL_INCLUDES` 注入。最低支持的 CMake 行为为 3.24 及以上。

Planner 会保留 Phase 3 已验证的 preset include list，并把 Service include 置于固定顺序；不会从 Protocol 或新增 workspace 字段接收 include path。项目本身原有的 CMake code 仍按 Workspace Trust 语义执行，但不能获得 coverage Adapter 参数控制权。

include 使用 `add_compile_options()`、`add_link_options()` 和 language/frontend generator expression，仅加入 Adapter 固定选项。

### 9.1 LLVM

- compile/link：`-fprofile-instr-generate` 的 clang/clang-cl 对应形式；
- compile：`-fcoverage-mapping`；
- Windows clang-cl 使用 Adapter 验证过的 MSVC frontend 参数形式；
- 不从 PATH 补全 profile runtime 或 LLVM tool。

### 9.2 GCC

- compile/link：`--coverage`；
- compile：`-fprofile-abs-path`；
- 使用已验证 Toolchain 中的 `gcov`；
- 不允许用户选择其他 gcov executable。

### 9.3 Post-configure/build verification

configure 后使用 CMake File API 检查 coverage target 的 C/C++ compile group 和 link fragment 是否含有预期 instrumentation contract。build 后再次校验 executable fingerprint，并验证 binary/profile mapping capability。

项目通过 target option 显式撤销 instrumentation、编译器忽略选项、runtime 缺失或目标没有有效 coverage mapping 时，必须在运行测试前以 configuration/build diagnostic 失败，不能静默生成空报告。

Phase 5 不覆盖 base profile 的 optimization level。报告记录 configuration；GCC optimized build 产生可见 warning，因为 GCC source mapping 可能受 optimization 影响。

## 10. LLVM coverage pipeline

### 10.1 工具约束

clang-cl/Clang、`llvm-profdata`、`llvm-cov` 必须：

- 来自同一已验证 LLVM installation；
- 满足 Toolchain Adapter 的版本兼容检查；
- 在执行前后保持相同 file identity 与 SHA-256；
- 位于允许的 installation root；
- 通过 `ExecutionBoundary` 固定。

### 10.2 Profile allocation

每个 test invocation 使用 Service-owned `LLVM_PROFILE_FILE`：

```text
<run-data>/<invocation-id>/<iteration>-%p-%m.profraw
```

`invocation-id` 来自内部 stable ID 映射，不含用户文本。目录在启动 test process 前创建并固定 ownership。用户 environment 不能覆盖 `LLVM_PROFILE_FILE`。

### 10.3 Merge 与 export

测试 wave 完成后：

1. 按 canonical path 枚举当前 run manifest 中的 `.profraw`；
2. 拒绝 symlink/reparse point、未知 owner、重复 identity 和越界文件；
3. 使用 `llvm-profdata merge -sparse` 生成当前 run 的 `.profdata`；
4. 按 stable ID 排序所有经过 fingerprint 校验的 instrumented binary，对首个 binary 执行 `llvm-cov export`，其余 binary 通过固定的 additional object 参数加入；
5. 在 bounded parser 中转换为内部 LLVM domain object；
6. 删除 raw/indexed profile 和第三方 export JSON。

同一 CoverageRun 可以包含多个 CTest container/binary，但每个 binary 都必须属于当前 build manifest、通过 `ExecutionBoundary` 固定并进入 export object set。raw profile 与任一 instrumented binary 不匹配、LLVM JSON major version 不受支持或 mapping 无法读取时，CoverageRun `unavailable`。

## 11. GCC/gcovr coverage pipeline

### 11.1 工具约束

GCC 与 `gcov` 必须来自同一 Toolchain identity。Python、gcovr 和 Python dependency 必须来自一个 product-owned coverage bundle。

bundle manifest 至少包含：

- platform/architecture；
- Python、gcovr 和 dependency 精确版本；
- 每个 executable/archive/module 的 SHA-256；
- build provenance；
- license/NOTICE 关联；
- bundle schema version。

具体版本由 repository lock 和 manifest 固定，而不是写死在 Protocol 或 Workspace Schema 中。任何版本升级都必须更新 lock、checksum、Golden output 和兼容测试。

### 11.2 Python isolation

- 不调用系统 `python`、`python3`、`pip` 或 PATH command；
- Windows 使用 product-owned embedded distribution 和受限 `._pth`；
- Linux 使用 product-owned、按支持的 ABI baseline 构建并校验的 runtime bundle；
- 启动使用 isolated mode，忽略 `PYTHON*` environment、current directory 和 user site-packages；
- module search path 只包含 manifest 中的 standard library 和 gcovr application archive；
- 入口是产品固定 runner，不接受 `-c`、`-m`、任意 script path、plugin 或 config file；
- runner 的输入/输出路径由 Service 创建，并位于当前 Task-owned root。

bundle 下载只允许发生在开发/CI bootstrap；产品运行时只消费已安装并通过 manifest 校验的文件。Phase 8 再把 bundle 纳入签名安装包和更新/回滚。

### 11.3 Collection

GCC coverage test invocation 首期串行执行，并对 coverage build identity 持有独占 lease：

1. 运行前删除 manifest 管理范围内的旧 `.gcda`；
2. 保留 build 产生的 `.gcno`；
3. 运行全部选中测试，累计当前 run 的 `.gcda`；
4. 校验 `.gcno/.gcda` 位于 build root、不是 link/reparse point，且 compiler identity 匹配；
5. 使用固定 gcovr runner 生成第三方 JSON；
6. bounded parser 转换为内部 GCC domain object；
7. 删除 `.gcda` 和第三方 JSON。

测试进程 crash/timeout 可能没有刷新全部 counter，此时可以生成 `partial`。正常退出但所有预期 instrumented object 都缺少 data 时视为 infrastructure failure。

## 12. Coverage JSON v1

Coverage JSON 是 canonical、deterministic、UTF-8 JSON。它不包含 run ID、artifact ID、时间戳、duration 或 native path；这些属于 SQLite/artifact metadata。

顶层结构：

```text
CoverageDocument
├─ schemaVersion: "1.0"
├─ provenance
│  ├─ platform / architecture
│  ├─ compiler family / version
│  ├─ driver / collector / normalizer version
│  └─ instrumentation fingerprint
├─ completeness
│  ├─ outcome
│  └─ bounded reason codes
├─ summary
└─ files[]
```

`summary` 与 file summary 使用：

```text
Metric { covered: uint64-safe-integer, total: uint64-safe-integer }
lines / branches / functions: Metric
```

`files[]`：

- workspace-relative canonical URI；
- source SHA-256；
- lines/branches/functions summary；
- 按 line number 排序的 line record；
- 每行 execution count；
- 每行 branch `{covered,total}`。

所有 JSON integer 必须小于或等于 JavaScript `Number.MAX_SAFE_INTEGER`。超限输入以 `COVERAGE_COUNT_OUT_OF_RANGE` 失败，不能截断或转为浮点数。

公共模型不暴露 LLVM region、template instantiation、MC/DC、GCC call 或 path coverage。`toolDetails` 只能包含经过 schema 版本化、大小受限且不会改变公共 summary 的 provenance/diagnostic 字段。

percentage 由 consumer 从整数计算；canonical JSON 不保存浮点 percentage，避免跨语言舍入差异。

## 13. 路径规范化与源码绑定

Normalizer 将第三方 native path 解析到当前 Workspace root：

- Windows 处理 drive letter、UNC、separator、case-insensitive identity 和 reparse point；
- Linux 处理 absolute path、separator、case-sensitive identity 和 symlink；
- source path 必须落在 Workspace root 内，并不能落在 coverage build、Service data 或 `.git` root；
- 先按真实文件 identity 校验，再转换为 workspace-relative URI；
- URI 使用 `/`，按 Unicode code point 的 canonical byte sequence 排序；
- 同一物理文件的多个 spelling 合并；不同物理文件不能因大小写或 path cleanup 错误合并。

include/exclude glob 只作用于已经通过边界校验的 relative URI，不能把 workspace 外文件重新纳入。

每个 file record 保存生成报告时的 source SHA-256。当前文件 digest 不一致时：

- 历史 JSON/HTML 仍可查看；
- Protocol 标记 source mapping 为 stale；
- Phase 7 不得把历史 line decoration 投射到当前编辑器。

## 14. Report renderer

### 14.1 Coverage JSON artifact

规范 Coverage JSON 直接作为长期 artifact 保存。Artifact metadata 记录 run/report ID、创建时间、size、SHA-256 和 content type，但这些非确定字段不写入 JSON body。

### 14.2 JUnit XML

JUnit XML 从关联 TestRun projection 与 Coverage JSON provenance 生成：

- testcase outcome、failure/error/skip 与 Phase 4 语义一致；
- Coverage JSON SHA-256、compiler、driver 和 version 写入 bounded `<properties>`；
- 不使用 coverage percentage 改写 testcase outcome；
- 所有 name/message/location 必须 XML escape、移除非法 control character 并应用长度上限；
- 为 deterministic fixture 保持可复现，XML 不写 timestamp、run ID、absolute path 或 execution duration。

### 14.3 单文件 HTML

HTML：

- 内嵌固定、带版本的 CSS/JavaScript 和 canonical report data；
- 不加载 CDN、font、image、source map 或远程 URL；
- 使用固定 CSP，禁止网络、frame、form 和任意动态 code generation；
- 对 source、test name、assertion 和 diagnostic 做 context-appropriate escaping；
- 只嵌入边界内、digest 匹配、单文件与总大小未超限的 source text；
- 显示 compiler、driver、tool version、completeness、test result 与 coverage summary；
- 不包含 token、environment、native path、Service data path 或 raw command。

无法安全嵌入的 source 只显示相对 URI、digest 和 coverage counts，不使整个报告失败。

## 15. Protocol v1.4

### 15.1 Methods

- `coverage/runs/start`
- `coverage/runs/get`
- `coverage/runs/list`
- `coverage/reports/get`

`coverage/runs/list` 使用 bounded page size 与稳定 cursor。`coverage/reports/get` 只返回 metadata、summary、completeness 和 artifact ID，不内联大型 files/lines。

### 15.2 Events

- `coverage.run.started`
- `coverage.build.finished`
- `coverage.collection.started`
- `coverage.report.available`
- `coverage.run.finished`

事件写入同一 Task journal，使用单调 sequence。不得为每个 file/line 生成 journal event。

### 15.3 Artifacts

- `coverage-json`
- `junit-xml`
- `coverage-html`
- 必要的 bounded diagnostics/stdout/stderr

raw profile、indexed profile、`.gcda` 和第三方 JSON 不是公开 artifact kind。

### 15.4 Compatibility

Protocol v1.0–v1.3：

- 不提供 coverage methods/capability；
- 不返回 CoverageRun/Report；
- 隐藏 coverage-only Task 和关联的 coverage-owned TestRun；
- 不投影 coverage domain event；
- 其他 event 保留原 sequence，客户端必须继续允许安全跳号；
- 既有 build/test request、response 和 generated model 不改变。

## 16. 持久化与事务边界

SQLite migration 增加 `coverage_runs` 与 `coverage_reports`。大对象仍在 ArtifactStore。

创建事务：

1. 验证 idempotency、Workspace generation、request snapshot 和 Catalog reference；
2. 创建 Task；
3. 创建 CoverageRun；
4. 创建关联 TestRun；
5. 创建 queued event；
6. 单一 transaction commit。

终态事务前，renderer 将 artifact 写入 Task-owned staging，并完成：

- fsync/close；
- size/digest；
- JSON/XML/HTML structural validation；
- 对生成的 metadata 执行 Service secret/native-path scan；合法的 workspace source payload 单独经过边界、digest、size 和 escaping 校验；
- final ArtifactStore publish。

随后一个 SQLite transaction 提交：

- TestRun terminal state；
- CoverageRun terminal state；
- CoverageReport summary/provenance；
- artifact metadata；
- Task snapshot；
- final domain event。

publisher 只在 durable commit 后工作。publish failure 不能回滚已提交终态，也不能产生第二个 owner。

## 17. Idempotency、取消与恢复

### 17.1 Idempotency

相同 workspace scope 与 idempotency key：

- canonical request 相同：返回原 CoverageRun/Task；
- canonical request 不同：返回 `CONFLICT`；
- 不创建第二套 build/profile/report。

### 17.2 Cancel/timeout

取消或 Task timeout：

- 复用 Phase 2 process tree control；
- 不再启动 merge/normalize/report step；
- 关闭 sink/process 后提交 terminal state；
- 清理 current-run profile 和 staging；
- 已 durable 的 TestItemResult 保留。

### 17.3 Service restart

- queued CoverageRun：重新解析结构化 request，重新验证 Workspace generation、Project/Profile、Toolchain identity、coverage bundle manifest、Catalog revision 和 runtime-only boundary；验证成功后 resume。
- running CoverageRun：Task `interrupted`，CoverageRun `unavailable/service_restarted`；不复用 raw profile 或半成品 report。
- terminal CoverageRun：保持不变，可通过 list/get/artifact replay。
- startup cleanup 删除无 active owner 的 staging、profraw、profdata、gcda 和第三方 JSON；不能删除已发布 artifact。

## 18. ExecutionBoundary 与安全

Coverage execution 必须继续满足 Phase 2–4 的安全模型：

- Protocol 不创建 `ProcessSpec`；
- Coordinator/Adapter 只输出固定 executable 与参数数组；
- 不执行 Shell；
- 所有 working directory 位于 Workspace root 或 Service-owned data root；
- executable、CMake include、bundle manifest 和 test binary 均固定 identity/digest；
- continuation 合并、全计划校验并 durable persist 后才启动下一 process；
- batch 中每个 executable/working directory 独立校验。

Coverage boundary 新增固定对象：

- CMake/CTest；
- compiler/linker；
- instrumented test executable；
- `llvm-profdata`/`llvm-cov` 或 `gcov`；
- product Python runtime；
- gcovr runner/archive；
- instrumentation template；
- report-ui embedded asset manifest。

不得执行：

- workspace Python/script/module/plugin；
- workspace gcovr config；
- PATH 中同名工具；
- 未验证的 response file；
- user-provided report template；
- artifact 目录中的 executable。

## 19. Parser 与资源上限

第三方 JSON、Coverage JSON 和报告生成均设置明确上限：

- input bytes；
- JSON depth；
- file/function/line/branch record count；
- 单字符串 bytes；
- source embed 单文件/总 bytes；
- diagnostic count；
- stdout/stderr bytes；
- profile file count；
- report artifact bytes；
- safe integer count。

Parser 使用 streaming 或 bounded decoder，禁止先把无界 third-party document 完整展开为多个内存副本。超限使用稳定 error code，不返回部分解析对象。

## 20. 错误分类

| 场景 | Task outcome | TestRun | CoverageRun |
|---|---|---|---|
| assertion failure，报告成功 | succeeded | failed | available |
| test crash/timeout，剩余报告有效 | succeeded | errored/partial items | partial |
| configure/build failure | command_failed | not started/not_run | unavailable |
| 正常退出但缺少 profile | infrastructure_failed | preserved | unavailable |
| tool version/digest mismatch | infrastructure_failed | not started/preserved | unavailable |
| merge/export/parser/report failure | infrastructure_failed | preserved | unavailable |
| user cancel | cancelled | cancelled/not_run | cancelled/user_cancelled |
| Task timeout | timed_out | timeout/not_run | cancelled/task_timed_out |
| Service restart during run | interrupted | preserved/not_run | unavailable/service_restarted |

错误 message 对客户端稳定且脱敏；native path、command、environment 和 Python traceback 只进入受限 diagnostic artifact，并在写入前清洗。

## 21. Determinism

- file 与 line 按 canonical key 排序；
- map 在序列化前转换为稳定数组；
- JSON 使用固定字段顺序、UTF-8、LF 和结尾 newline；
- JUnit testcase 按 stable Test ID/iteration 排序；
- HTML 使用固定 embedded asset digest；
- canonical report body 不包含时间、duration、random ID 或 absolute path；
- percentage 不进入 canonical body；
- fixture source、test outcome 和 coverage count 相同时，JSON、JUnit、HTML digest 相同。

用户程序自身的非确定执行可能导致 coverage count 变化；产品只保证相同规范输入产生相同输出，不伪造相同 digest。

## 22. 测试策略

### 22.1 Schema 与 contract

- Protocol v1.4 request/response/event/artifact；
- Coverage JSON v1 valid/invalid fixture；
- generated TypeScript/Go model drift；
- v1.0–v1.3 compatibility；
- execution injection negative fixture。

### 22.2 Unit tests

- coverage build identity 与目录隔离；
- CMake instrumentation template；
- LLVM/GCC 参数数组；
- tool/bundle version 与 identity mismatch；
- include/exclude glob；
- Windows/Linux path normalization；
- parser normal/empty/malformed/oversized/overflow；
- common summary 与 line/branch aggregation；
- deterministic JSON/JUnit/HTML；
- XML/HTML escaping、CSP、外部 URL 拒绝；
- Service secret/native-path metadata redaction；
- transaction、idempotency、cancel 和 recovery。

### 22.3 Golden fixtures

LLVM 与 gcovr 分别包含：

- C 与 C++；
- CppUTest/CppUMock；
- Unity/CMock；
- pass/assertion/mock failure；
- branch；
- template/inline/macro；
- Unicode/space path；
- empty/uninstrumented binary；
- unsupported JSON major；
- malformed/truncated/over-limit input。

### 22.4 真实 E2E 矩阵

| 平台 | Toolchain | Collector | Framework |
|---|---|---|---|
| Windows | clang-cl | llvm-profdata/llvm-cov | CppUTest、Unity |
| Linux | GCC | gcov + bundled gcovr | CppUTest、Unity |
| Linux | Clang | llvm-profdata/llvm-cov | CppUTest、Unity |

每个矩阵验证：

- configure/build/inspect/discover/run/collect/report；
- assertion failure 仍生成 available report；
- crash/timeout 产生 partial；
- cancel 不继续 report process；
- missing profile；
- mismatched tool version；
- replaced/stale executable；
- Service restart；
- JSON/JUnit/HTML artifact read；
- no-network runtime guard；
- deterministic fixture digest。

### 22.5 Scale 与 fault injection

- 100,000 行 coverage；
- 10,000 TestItemResult；
- 大量 source files 与 profile files；
- bounded memory/page/event behavior；
- configure/build/test/merge/export/parser/renderer/store/publisher 每个 failure point；
- 不使用依赖 Hosted Runner 速度的脆弱绝对时限。

## 23. 实施切片

### Slice A：domain、Protocol 与 persistence

- Coverage JSON Schema；
- Protocol v1.4；
- generated models；
- CoverageRun/Report repository；
- migration、artifact kind、transaction contract。

### Slice B：offline coverage bundle

- Python/gcovr lock、manifest、checksum、license；
- Windows/Linux bundle preparation；
- isolated runner；
- no-network 与 injection tests。

### Slice C：coverage build

- coverage profile Schema；
- build identity/lease；
- CMake include template；
- LLVM/GCC instrumentation；
- File API/post-build verification。

### Slice D：collection 与 normalization

- LLVM profile allocation/merge/export/parser；
- GCC cleanup/gcovr/parser；
- path normalization；
- Coverage JSON canonical writer。

### Slice E：reports 与 artifact pipeline

- JUnit renderer；
- report-ui static asset；
- single-file HTML；
- artifact staging/validation/terminal transaction。

### Slice F：runtime、recovery 与 E2E

- CoverageCoordinator；
- Session/TypeScript Client；
- compatibility projection；
- queued/running recovery；
- Windows/Linux matrix 与完整门禁。

每个 Slice 先写失败测试，再实现，再运行聚焦测试、全套 test 和适用的 race/E2E，并以独立提交结束。

## 24. 验收标准

Phase 5 完成必须同时满足：

1. Windows clang-cl/llvm-cov、Linux GCC/gcovr 和 Linux Clang/llvm-cov 均生成准确报告。
2. CppUTest/CppUMock 与 Unity/CMock 都能通过真实 coverage build 执行。
3. source、line、branch、function 和 overall summary 可复现。
4. 报告明确显示实际 compiler、driver、collector 和完整版本。
5. 普通 build 与 coverage build 目录、cache、target object 和 lock namespace 完全隔离。
6. assertion failure、partial coverage 和 infrastructure failure 分类准确。
7. raw profile 与第三方 JSON 不作为长期 artifact。
8. JSON/HTML 中的 coverage metric 与 provenance 都来自统一 Coverage JSON；JUnit testcase outcome 来自关联 TestRun；三种报告分别通过适用的 schema、escaping 和 CSP 校验。
9. product runtime 不联网，不调用系统 Python，不读取 user site-packages。
10. Protocol 无法构造 coverage `ProcessSpec` 或选择任意 coverage executable。
11. v1.0–v1.3 compatibility 全绿。
12. cancel、timeout、reconnect 和 restart recovery 满足本规格。
13. 完整 `pnpm verify`、Windows/Linux Hosted E2E、bundle checksum/license gate 全绿。
14. 独立安全评审确认 coverage continuation、Python runner、parser 和 renderer 边界。
15. 同一最终提交推送 GitHub 与 Gitee。

## 25. 后续阶段接口

Phase 6 可以：

- 将 `coverage/runs/start` 绑定到 Code-OSS “Run with Coverage”；
- 管理 Service/bundle 生命周期；
- 在 Workspace Trust 关闭时禁止 coverage execution；
- 下载 Coverage JSON artifact。

Phase 7 可以：

- 使用 Coverage JSON 建立 coverage tree、源码 decoration 和历史报告；
- 增加 threshold 与 quality gate 配置；
- 增加报告比较和过滤 UX；
- 复用 report-ui embedded asset。

Phase 8 可以：

- 把 Python/gcovr、LLVM capability 与 report-ui asset 纳入签名安装包；
- 管理 bundle update/rollback；
- 生成完整第三方 NOTICE/SBOM。

Phase 9 可以：

- 建立正式性能基线；
- 运行完整平台、故障注入和发布资格矩阵。

## 26. 官方参考

- [LLVM Source-based Code Coverage](https://clang.llvm.org/docs/SourceBasedCodeCoverage.html)
- [Clang Compiler User’s Manual](https://clang.llvm.org/docs/UsersManual.html)
- [GCC Instrumentation Options](https://gcc.gnu.org/onlinedocs/gcc/Instrumentation-Options.html)
- [gcovr User Guide](https://gcovr.com/en/stable/guide.html)
- [gcovr Command Line Reference](https://www.gcovr.com/en/stable/manpage.html)
- [CMake `CMAKE_PROJECT_TOP_LEVEL_INCLUDES`](https://cmake.org/cmake/help/latest/variable/CMAKE_PROJECT_TOP_LEVEL_INCLUDES.html)
- [CMake `add_compile_options`](https://cmake.org/cmake/help/latest/command/add_compile_options.html)
- [Python command line and environment](https://docs.python.org/3/using/cmdline.html)
- [Python embedded distribution](https://docs.python.org/3/using/windows.html)
