# Phase 4：测试框架发现与执行设计

**日期：** 2026-07-30

**状态：** 已确认

**目标分支：** `codex/workspace-cmake-toolchains`

**基础提交：** `4a3fbeda0bb6b05e4e0a6c11fb59fabe61f25d20`

## 1. 背景

Phase 1 已建立版本化 JSON 协议、TypeScript Client、Go 本地 Service、per-user IPC、token handshake、能力查询和安全关闭流程。

Phase 2 已建立 Task 状态机、跨平台进程树控制、取消与超时、结构化事件顺序、断线重连与重放、SQLite 历史记录及 ArtifactStore。Task Engine 只执行 Service 内部生成的 `ProcessSpec`，Protocol 不能提交 executable、参数数组、环境变量、工作目录或 Shell。

Phase 3 已建立受信任 Workspace 绑定、CMake Presets、generated fallback、CMake File API、MSVC、clang-cl、GCC、Clang、Build Profile、Target、Build Coordinator、编译诊断和 Windows/Linux Hosted CI。

Phase 4 在这些边界上加入 CTest、CppUTest/CppUMock 和 Unity/CMock 的测试发现与执行。它不是把测试命令行暴露给客户端，而是把 CTest metadata、Framework Adapter、Test Catalog 和结构化测试选择转换成 Service 自己拥有的 `ExecutionPlan`。

## 2. 已确认的决策

1. 采用 **CTest-first 混合适配**：CTest 负责发现测试容器，Framework Adapter 负责可靠的单用例发现、精确运行和结果解析。
2. Unity 首期采用 **opt-in CMake helper + generated runner**，不对任意 Unity 项目做猜测性源码扫描。
3. 未接入 Unity helper、无法完成 case-level discovery 或不满足直接执行兼容条件时，降级为可运行的 Opaque CTest 容器。
4. 降级容器不生成虚假的 case ID，不支持单用例运行或单用例失败重跑。
5. CppUMock 由 CppUTest Adapter 处理，CMock 由 Unity Adapter 处理。
6. Phase 4 验证真实 Mock 用例的运行、失败解析和源码位置，不实现 Mock 代码生成或可视化配置；这些属于 Phase 7。
7. Framework Adapter 的选择必须来自 CMake helper metadata 或 Workspace 中按 CTest logical name 声明的枚举映射。
8. Service 不会为了猜测框架而向未知 executable 注入 `-ln`、`--list` 或其他探测参数。
9. 稳定测试 ID 不包含绝对路径、build directory、compiler 或 Build Profile，同一逻辑用例跨支持平台保持一致。
10. Catalog revision 失效后，Service 必须重新发现并按稳定 ID 绑定；已删除或重命名的测试不得按相似名称替换。
11. Task outcome 表示编排是否完成，TestRun/TestItem outcome 表示测试语义；断言失败不能仅由非零退出码推断。
12. Protocol v1.0、v1.1 和 v1.2 保持兼容；Phase 4 能力通过 Protocol v1.3 暴露。
13. 所有执行命令继续由 Service 从可信 Workspace、Build Profile、CTest snapshot 和 Adapter contract 推导。
14. Phase 4 的提交必须同步到 GitHub 与 Gitee；最终产品运行时不依赖 GitHub 或 Gitee。

## 3. 目标

- 从所选 Build Profile 的 build tree 中读取 CTest JSON 并建立测试容器。
- 通过 CppUTest Adapter 发现 group/case，运行全部、group 或精确 case。
- 通过 Unity CMake helper 和 generated runner 发现、运行精确 Unity case。
- 将 CppUMock/CMock expectation failure 规范化为测试断言失败。
- 为 project、container、group/suite 和 case 建立跨平台稳定 ID。
- 支持全部、容器、单用例、结构化过滤、重复运行和失败重跑。
- 支持实时日志、开始/完成事件、失败详情和源码位置。
- 明确区分 configuration error、build error、assertion failure、crash、timeout、malformed output 和 infrastructure error。
- 复用 Phase 2 的取消、超时、事件持久化、断线重连、恢复和 ArtifactStore。
- 在 Windows/MSVC、Windows/clang-cl、Linux/GCC 和 Linux/Clang 上运行真实框架 E2E。
- 保持 Protocol 到 `ProcessSpec` 的单向安全边界。

## 4. 非目标

- Code-OSS Testing API、测试树 UI、状态栏或源码装饰；这些属于 Phase 6 和 Phase 7。
- CppUMock 或 CMock 代码生成、Mock/Stub 可视化编辑和生成配置；这些属于 Phase 7。
- 覆盖率采集、JUnit XML、HTML 报告或报告导出；这些属于 Phase 5。
- GoogleTest、Catch2、doctest 或其他框架。
- 对任意 C/C++ 源码做启发式测试发现。
- 对未知 test executable 注入猜测性命令行参数。
- 通过 Protocol 接收 executable、Shell、hook、脚本、原始参数、任意环境变量或工作目录。
- 完整复刻 CTest scheduler 的所有 properties；不兼容的容器必须回退到 CTest 容器级运行。
- 测试进程的操作系统级沙箱。
- macOS 支持。
- 浏览器或云端执行。
- 10,000 测试项下的最终 UI 性能门禁；Phase 4 只建立后端基线，正式门禁属于 Phase 7。

## 5. 总体架构

```text
TypeScript Client / Protocol v1.3
                │
                │ 仅声明 workspace/project/profile/test ID
                ▼
Go Session / Router
                │
                ▼
         Test Coordinator
          ├─ Build Coordinator ──► Phase 3 CMake/Toolchain
          ├─ CTest Adapter ──────► CTest JSON / 容器执行
          ├─ Framework Registry
          │    ├─ CppUTest Adapter
          │    ├─ Unity Adapter
          │    └─ Opaque CTest Adapter
          ├─ Test Catalog Store
          ├─ Test Run Store
          └─ Result Normalizer
                    │
                    ▼
        Task Engine / Event Store / ArtifactStore
                    │
                    ▼
             Process Controller
```

### 5.1 Test Coordinator

Test Coordinator 是测试领域的唯一编排入口，负责：

- 校验 Workspace、Project、Build Profile 和 Catalog revision；
- 调用 Build Coordinator 执行安全的增量构建；
- 调用 CTest Adapter 生成容器；
- 选择 Framework Adapter；
- 将结构化 Test Selection 展开为不可变稳定 ID 集合；
- 生成 Service-owned `ExecutionPlan`；
- 汇总 TestRun、TestItem、Diagnostic 和 Artifact；
- 将最终结果与 Task outcome 分层持久化。

Test Coordinator 不直接解析某个框架的输出，也不自行拼接 Framework Adapter 的参数。

### 5.2 CTest Adapter

CTest Adapter 负责：

- 使用选定 Build Profile 的同一 CMake installation 解析 CTest；
- 对 multi-config build tree 传入选定 configuration；
- 执行 `ctest --show-only=json-v1`；
- 解析 logical test name、command、properties、labels 和 backtrace；
- 规范化 CTest command 和路径；
- 生成容器级 `CTestExecutionDescriptor`；
- 对 Opaque 容器通过 CTest 做精确容器选择和执行。

CTest Adapter 不解析 CppUTest/Unity assertion。

### 5.3 Framework Registry

Framework Registry 根据已验证 metadata 返回 Adapter。每个 Adapter 独立声明：

- `frameworkId` 与 `contractVersion`；
- `canDiscoverCases`；
- `canRunCase`；
- `canReportSkipped`；
- `canReportSourceLocation`；
- `canReportMockDetails`；
- 支持的 CTest properties；
- 降级原因。

添加新框架时只新增 Adapter 和 Protocol capability，不修改 CTest Adapter、Task Engine 或 Process Controller。

### 5.4 Result Normalizer

Result Normalizer 把框架特定输出转换为统一的 TestRun、TestItemResult 和 Diagnostic。它必须保留原始 evidence 引用。CppUTest stdout 只有在通过 Adapter 的完整 grammar 和 summary 一致性校验后才能成为 evidence；未经验证的日志子串或退出码不能单独决定最终状态。

## 6. Workspace 配置与 CMake helper

### 6.1 Workspace 配置版本

Workspace 配置升级到 `version: 2`，新增可选的 project-level `tests`。Loader 继续接受 Phase 3 的 `version: 1`；v1 配置没有 framework mapping，未带 helper metadata 的 CTest test 因而表现为 Opaque 容器。

```json
{
  "version": 2,
  "projects": [
    {
      "id": "core",
      "sourceDir": ".",
      "tests": {
        "containers": [
          {
            "ctestName": "core_cpputest",
            "framework": "cpputest"
          },
          {
            "ctestName": "core_unity",
            "framework": "unity"
          }
        ]
      }
    }
  ]
}
```

约束：

- `ctestName` 必须是精确 logical name，不接受 regex、glob 或路径。
- `framework` 只能是 `cpputest` 或 `unity`。
- 不允许 command、executable、args、environment、workingDirectory、shell 或 hook。
- 同一 project 中的 `ctestName` 必须唯一。
- helper metadata 与 Workspace mapping 冲突时产生 `framework_configuration_conflict`，不得静默选择。

### 6.2 CMake helper 的分发

仓库和产品 SDK 提供可复制、可版本固定的 `UnitTestIDE.cmake`。Project 必须显式 `include()`；Service 不自动修改用户的 `CMakeLists.txt`，也不在未授权时注入 top-level include。

helper 提供两类受控入口：

```cmake
unit_test_ide_add_cpputest(
  TEST   core_cpputest
  TARGET core_cpputest_target
)

unit_test_ide_add_unity_test(
  TEST         core_unity
  TARGET       core_unity_target
  TEST_SOURCES test_math.c test_buffer.c
)
```

这两个签名是 Phase 4 的固定公开入口：

- `unit_test_ide_add_cpputest` 要求 `TARGET` 是现有 CMake executable target；helper 以 `$<TARGET_FILE:...>` 注册新的 `TEST` 并追加 framework label。已存在同名 CTest test 时配置失败，已有 `add_test()` 的项目应改用 Workspace exact-name mapping。
- `unit_test_ide_add_unity_test` 要求 `TARGET` 是尚未包含其他 Unity main 的现有 executable target；helper 将 generated runner 加入 target，注册 `TEST` 并写入 framework/runner labels。
- `TEST`、`TARGET` 和 `TEST_SOURCES` 都是 CMake 结构化参数；helper 不接受 `COMMAND`、`ARGS`、`ENVIRONMENT`、`WORKING_DIRECTORY` 或 hook。
- Service 配置 Project 时只从产品 installation manifest 注入保留的 `UTIDE_UNITY_RUNNER_GENERATOR` 绝对路径，并把 generator identity 纳入 configure fingerprint。Project 在 IDE 外独立构建时可以显式提供同版本 generator；helper 必须验证 generator version，且不能搜索 `PATH` 后静默选取其他版本。

### 6.3 helper metadata

helper 只写入版本固定的 CTest labels 和由 helper 管理的 build-tree sidecar：

- `utide.framework.cpputest`；
- `utide.framework.unity`；
- `utide.runner.v1`；
- `.unit-test-ide/<encoded-ctest-name>/manifest.json`。

sidecar 路径从 build root 和编码后的 CTest logical name 推导，不能由 Protocol 指定。Service 必须验证 sidecar 的 canonical path、Schema、文件大小和 framework contract version。

### 6.4 Unity runner generator

Phase 4 提供产品自有、版本固定的 Go Unity runner generator，并由 CMake helper 在显式 opt-in 的项目中调用。这样不要求最终用户额外安装 Ruby 或 Ceedling。

generator 只处理 helper 中显式声明的 `TEST_SOURCES`，生成：

- Unity runner C source；
- 测试 manifest；
- 支持 list 和 exact-case run 的 `utide.runner.v1` 控制入口；
- case name、source-relative path、line 和参数化实例身份。

这与“任意源码扫描 fallback”不同：generator 是用户显式加入 build graph 的受控构建步骤，输入源文件和输出位置均由 CMake target 与 helper contract 确定。未使用 helper 的项目不会被 Service 扫描。

CMock 生成不属于该 generator。Unity fixture 可以链接项目已生成并纳入 build graph 的 CMock 源码。

## 7. Protocol v1.3

### 7.1 兼容策略

- handshake 继续选择双方支持的最高版本。
- v1.0、v1.1 和 v1.2 Schema、fixture 和 Client 行为保持不变。
- v1.3 新增测试领域 Schema 和事件。
- 旧 Client 不会收到 v1.3-only 响应。
- 新 Client 与旧 Service 协商到旧版本后，测试能力显示为不可用，不做模拟 fallback。

### 7.2 新增请求

Phase 4 复用 `tasks/start`：

- `kind: "testDiscovery"`：增量构建并发布新 Catalog；
- `kind: "testRun"`：运行结构化 Test Selection。

新增只读方法：

- `tests/catalog/get`；
- `tests/runs/get`；
- `tests/runs/list`。

现有 `tasks/get`、`tasks/list`、`tasks/cancel`、`events/subscribe`、`artifacts/list` 和 `artifacts/read` 继续复用。

### 7.3 testDiscovery payload

```json
{
  "kind": "testDiscovery",
  "projectId": "core",
  "profileId": "profile-id"
}
```

Discovery 是显式执行动作，会运行安全的增量 build 和 test executable 的受控 list 模式。`workspace/inspect` 和 `tests/catalog/get` 都不得隐式执行 Workspace 代码。

### 7.4 testRun payload

```json
{
  "kind": "testRun",
  "projectId": "core",
  "profileId": "profile-id",
  "catalogRevision": "revision",
  "selection": {
    "mode": "items",
    "itemIds": ["utid-v1-..."]
  },
  "repeatCount": 1
}
```

`selection.mode` 只能是：

- `all`；
- `containers`；
- `items`；
- `filter`；
- `failedFromRun`。

`filter` 只允许 Catalog 字段上的结构化条件：

- 精确 group/suite；
- 精确 label；
- Unicode case-fold 后的名称子串；
- 明确 include/exclude item ID。

`failedFromRun` 只接受已持久化的 `runId`，不接受客户端回传的失败名称。

### 7.5 限制

- `repeatCount` 范围为 1 到 100。
- 单次展开后的选择最多 100,000 个 case。
- Catalog page 默认 200 项，最大 1,000 项。
- 名称字段 UTF-8 长度不超过 512 bytes。
- Schema 对所有对象使用 `additionalProperties: false`。
- 所有新增执行请求必须有 executable、command、args、shell、environment、cwd、workingDirectory 和 hook 的负向 fixture。

### 7.6 Capability

`capabilities/get` 在 v1.3 增加：

- 可用 Framework Adapter；
- Adapter contract version；
- case discovery/run capability；
- Opaque fallback capability；
-最大 repeat count、selection size 和 page size；
- CTest JSON 支持状态；
- Unity helper/runner contract version。

Capability 只描述 Service 能力，不声称当前 Project 已接入某个框架。

## 8. 领域模型

### 8.1 TestFramework

```text
TestFramework
├─ id: cpputest | unity | opaque-ctest
├─ contractVersion
├─ displayName
└─ capabilities
```

CppUMock 不单独建立 runner framework；它作为 CppUTest capability。CMock 同理作为 Unity capability。

### 8.2 TestContainer

```text
TestContainer
├─ id
├─ projectId
├─ ctestLogicalName
├─ displayName
├─ framework
├─ capabilities
├─ labels
├─ sourceLocation?
├─ disabled
└─ degradedReason?
```

`sourceLocation` 首选 CTest backtrace 中的 `add_test()` 位置。它不等同于 case declaration。

### 8.3 TestItem

```text
TestItem
├─ id
├─ containerId
├─ parentId?
├─ kind: group | suite | case
├─ framework
├─ logicalName
├─ displayName
├─ labels
├─ sourceLocation?
├─ disabled
└─ parameters?
```

`parameters` 只能是用于展示和稳定身份的结构化值，不得转换成任意进程参数。

### 8.4 TestCatalog

```text
TestCatalog
├─ projectId
├─ profileId
├─ revision
├─ generatedAt
├─ containers[]
├─ items[]
├─ diagnostics[]
└─ partial: false
```

Catalog 只有在完整发现和 Schema 校验后才能原子发布。新 Catalog 构建失败时保留上一份成功 Catalog，并把失败记录在 Discovery Task；不得发布 `partial: true` 的 Catalog。

### 8.5 TestRun

```text
TestRun
├─ runId
├─ taskId
├─ projectId
├─ profileId
├─ toolchainId
├─ catalogRevision
├─ selectionSnapshot
├─ status: queued | running | completed
├─ outcome
├─ startedAt?
├─ finishedAt?
├─ summary
├─ resultRevision
└─ incomplete
```

`selectionSnapshot` 持久化实际展开的稳定 ID，不在运行中重新应用 filter。

### 8.6 TestItemResult

```text
TestItemResult
├─ itemId
├─ containerId
├─ iteration
├─ outcome
├─ durationMs?
├─ sourceLocation?
├─ failureDetails[]
├─ outputRefs[]
├─ partial
└─ reason?
```

## 9. Discovery 数据流

1. Session 验证 Protocol、token、Workspace generation 和请求 Schema。
2. Test Coordinator 解析 Project 与 Build Profile。
3. Build Coordinator 对所选 profile 执行一次增量 default build。CMake 决定实际需要重编译的 target。
4. CTest Adapter 使用 profile 对应 build directory 和 configuration 执行 CTest JSON discovery。
5. CTest Adapter 规范化 container command、properties、labels 和 backtrace。
6. Framework Registry 按 helper metadata、Workspace mapping 的顺序选择 Adapter。
7. Adapter 先验证自己的 runner signature/contract，再进入 case discovery。
8. CppUTest Adapter 使用原生 list 模式；Unity Adapter 使用 `utide.runner.v1` list 模式。
9. Result Normalizer 生成 container、group/suite、case 和 Diagnostic。
10. Catalog Store 检查重复逻辑身份、上限和引用完整性。
11. Catalog Store 计算 revision 并通过一个事务发布 Catalog metadata 和 JSON artifact。
12. Service 发出 `test.catalog.published` 终态事件。

Framework discovery 失败只降级对应 container，不让其他 container 消失。Catalog 可以包含带 `degradedReason` 的 Opaque container，但不能包含半完成的 case 列表。

## 10. Adapter 选择与降级

### 10.1 选择顺序

1. CMake helper 的版本化 metadata；
2. Workspace v2 的 exact-name framework mapping；
3. Opaque CTest Adapter。

helper metadata 和 Workspace mapping 同时存在且一致时允许继续；不一致时产生 configuration error。

### 10.2 二次验证

framework 声明不是充分条件。Adapter 必须验证：

- CppUTest `-ln` 返回满足 grammar 的完整列表；
- Unity runner 返回正确 magic、protocol version 和完整 JSON records；
- executable identity 与当前 Catalog fingerprint 一致；
- CTest execution descriptor 满足 case-level 兼容条件。

验证失败时：

- 保留 container；
- 清除所有未提交 case；
- 设置 `canDiscoverCases=false` 和 `canRunCase=false`；
- 写入稳定的 `degradedReason`；
- 允许容器级 CTest 运行。

### 10.3 禁止猜测

Service 不根据以下信息猜测框架：

- executable 文件名；
- CMake target 名称；
- stdout 中偶然出现的框架单词；
- binary symbol 扫描；
- 任意源码正则扫描；
- 向未知 executable 追加探测参数。

## 11. CTest execution descriptor

### 11.1 容器级执行

Opaque 或降级 container 始终由 CTest 执行。Service 根据 logical name 生成锚定且正确转义的精确选择，并固定 test directory 与 configuration。客户端不能提交 CTest 参数。

容器级执行保留 CTest 对 fixture、dependency、resource lock、working directory、environment、timeout 和其他 properties 的解释。

容器级“可运行”仍受 Workspace 安全边界约束：CTest descriptor 中的 command、引用文件和 working directory 必须规范化到当前 Project、profile build root 或产品安装清单明确允许的工具路径。超出边界的容器仍可发现，但标记为 `blocked_external_command`，不能通过 Service 运行。

### 11.2 case-level 直接执行条件

只有同时满足以下条件，Framework Adapter 才能直接运行 case：

1. CTest command 的 executable 能映射到当前 CMake File API codemodel 的 executable target artifact；
2. canonical executable 位于当前受信任 Project 或 profile build root 内；
3. command 不是 Shell、script、launcher 或 wrapper；
4. 原始参数不包含 Adapter 保留的 filter/control 参数；
5. 所有影响运行语义的 CTest properties 都在 Adapter allowlist；
6. executable fingerprint 与 Catalog revision 一致；
7. Framework Adapter contract 验证成功。

Phase 4 case-level allowlist：

- `WORKING_DIRECTORY`；
- `ENVIRONMENT`；
- `ENVIRONMENT_MODIFICATION`；
- `TIMEOUT`；
- `LABELS`；
- `DISABLED`；
- `SKIP_RETURN_CODE`；
- 与所选 multi-config configuration 有关的 metadata。

遇到下列语义时默认降级为容器级执行：

- fixture setup/cleanup/required；
- test dependency；
- resource lock/resource groups；
- launcher 或 wrapper；
- `WILL_FAIL`；
- pass/fail regular expression；
- 无法安全复现的未知 property。

`RUN_SERIAL` 可以由 Test Coordinator 保守地执行为 container-exclusive lock；如果它与其他不支持 property 组合，仍然降级。

### 11.3 环境与工作目录

case-level environment 只能来自当前 CTest descriptor 和 Service-owned control variables。Service 按 CTest 语义解析 environment modification，禁止 NUL、非法 key、超长值和跨边界 control path。

Protocol 和 Workspace framework mapping 都不能提供环境变量或工作目录。

## 12. 稳定 ID 与 Catalog revision

### 12.1 ID 编码

所有稳定 ID 使用：

```text
utid-v1-<sha256-lower-hex>
```

hash 输入采用带字段名和 byte length 的 UTF-8 NFC tuple encoding，避免字符串连接歧义。

身份 tuple：

- container：`projectId + ctestLogicalName`；
- group/suite：`projectId + ctestLogicalName + framework + groupOrSuiteName`；
- case：`projectId + ctestLogicalName + framework + groupOrSuiteName + caseName + normalizedParameters`。

绝对路径、build directory、profile、toolchain、compiler、configuration、发现顺序和数组下标不得进入稳定 ID。

### 12.2 跨平台规则

- Windows path casing 不影响 ID，因为 path 不进入 logical identity。
- CppUTest 使用原生 group/name。
- Unity 使用 generator manifest 中的 suite/case/parameter identity。
- display name 的本地化不影响 ID。
- test、group 或 CTest logical name 重命名会产生新 ID。
- framework mapping 改变时 case ID 改变，但 container ID 保持不变。

### 12.3 重复身份

同一 container 中出现完全相同的 case identity 时：

- 不使用 ordinal 消歧；
- 不把 source line 加入 ID 临时规避；
- Adapter discovery 标记为 malformed；
- container 降级为 Opaque；
- Diagnostic 列出冲突 identity 和 evidence。

### 12.4 Catalog revision

Catalog revision 是 profile-specific snapshot，至少包含：

- Workspace generation；
- canonical test config hash；
- CMake installation identity；
- Build Profile identity；
- CMake File API reply identity；
- canonical CTest JSON semantic hash；
- case-level executable SHA-256；
- Unity manifest hash；
- Framework Adapter contract version。

Catalog revision 可以跨 profile 不同，但其中的稳定 Test ID 必须一致。

### 12.5 stale 处理

Run 请求携带 Catalog revision。Service 在生成 `ProcessSpec` 前重新验证 fingerprint：

- revision 有效：继续；
- revision 失效：自动执行 discovery；
- 选中稳定 ID 全部仍存在：用新 revision 继续，并在 TestRun 中记录 rebinding；
- 任一选中 ID 不存在：返回 `catalog_stale` / `test_not_found`；
- 不允许名称相似匹配、ordinal fallback 或运行旧 executable。

## 13. CppUTest/CppUMock Adapter

### 13.1 case discovery

CppUTest Adapter 对已声明并验证的 executable 使用 `-ln`。根据 CppUTest contract，输出是以空白分隔的 `group.name`。Parser 必须处理：

- LF 和 CRLF；
- 多个空白字符；
- ANSI color；
- 分块 UTF-8；
- 空列表；
- 重复 group/name；
- 非法 token；
- 输出上限。

完整列表通过校验后才提交 case。

### 13.2 运行

- 全部 container：使用 Adapter 批量运行；
- 精确 group：使用 `-sg <group>`；
- 精确 case：组合 `-sg <group>` 和 `-sn <name>`；
- 多个离散 case：由 Planner 按兼容的 container batch 或独立 process 执行；
- repeat 由 Test Coordinator 控制并记录 iteration，不依赖框架的 `-r` 隐式合并结果。

Adapter 为执行追加 `-v`，使 item boundary 可以被流式 parser 验证。Service 追加的参数来自稳定 TestItem，不来自客户端文本。原始 CTest 参数出现 `-g`、`-n`、`-sg`、`-sn`、`-xg`、`-xn`、`-r`、`-v` 或等价保留参数时，case-level capability 降级。

### 13.3 结果解析

Phase 4 解析标准 CppUTest verbose/normal 输出并保留原始日志。Parser 识别：

- passed；
- `IGNORE_TEST` 对应 skipped；
- assertion failure；
- memory leak failure；
- CppUMock unexpected call、missing call 和 parameter mismatch；
- source file 与 line；
- summary；
- crash 前的完整记录。

`-ojunit` 不是 Phase 4 的最终制品接口。Phase 5 可以在不改变 TestRun 语义的前提下加入 JUnit export。

## 14. Unity/CMock Adapter

### 14.1 `utide.runner.v1`

generated runner 支持 Service-owned control 参数：

- list mode；
- exact-case run mode；
- protocol version；
- Service-owned result file。

具体 flag spelling 由 helper 与 runner contract 固定，Protocol 不暴露这些参数。

runner 在独立 result file 中写入有界 JSON Lines，stdout/stderr 保留为用户日志。每条 control record 包含：

- magic 和 protocol version；
- record type；
- suite/case/parameter identity；
- source-relative path 和 line；
- status；
- duration；
- failure message；
- 可选的 expected/actual；
- 可选的 CMock call/parameter details。

Service 只接受位于当前 Task artifact directory 内、通过 canonical path 校验的 result file。

Runner 在每个完整 control record 后 flush。发生 crash 时，Service 只接受 crash 前已完整落盘并通过 Schema 校验的 record。

### 14.2 list

list mode 不运行测试，只返回 manifest 中的 case。Service 交叉验证 runner 输出与 build-tree manifest：

- protocol version；
- case identity；
- source location；
- parameterized instance；
- executable fingerprint。

不一致时按 malformed discovery 降级。

### 14.3 exact run

exact-case run 只能引用 manifest 中存在的稳定 case identity。Runner 在自己的 generated dispatch table 中定位函数，不接受原始函数名、指针或任意参数。

### 14.4 CMock

CMock 生成代码由用户 Project 的现有 build graph 提供。Unity Adapter 只解析 expectation failure、调用次数和 parameter mismatch，不调用 CMock generator，不要求 Ceedling。

## 15. Test Selection、运行与失败重跑

### 15.1 选择展开

Test Coordinator 在 Task 启动时把 selection 解析为排序稳定的 `selectionSnapshot`。后续 Catalog 更新不会改变正在运行的选择。

排序仅用于确定性事件和结果；它不进入 Test ID。

空选择返回 `empty_test_selection`，不启动测试 process。

### 15.2 运行计划

```text
Preflight
→ Incremental Build
→ Catalog Validate/Refresh
→ Selection Expand
→ Group by Container
→ Compatibility Check
→ Build ProcessSpec
→ Run and Stream Parse
→ Persist Results
→ Publish Summary
```

Test Coordinator 可以在不同 container 之间做有界并发，同一 container 默认串行。Service-wide maximum 是受控配置，Client 不能突破 capability 上限。

### 15.3 全部、容器和 case

- `all`：运行 Catalog 中全部未 disabled container/item；
- `containers`：运行精确 container；
- `items`：运行精确 group/suite/case；
- `filter`：先在 Catalog 中匹配，再生成精确 ID snapshot；
- disabled item 不会因 `all` 或 `filter` 隐式运行。

### 15.4 repeat

每次重复都有从 1 开始的独立 `iteration`。单次 iteration 的失败不阻止后续 iteration，除非：

- 用户取消；
- Task timeout；
- infrastructure error；
- container crash 后 Planner 无法安全继续同一 container。

Summary 同时提供 iteration-level 和 aggregate 统计。

### 15.5 failedFromRun

Service 从持久化 TestRun 读取失败范围：

- assertion failure：精确 case ID；
- timeout、crash 或 malformed output 导致范围不完整：对应 container ID；
- cancelled：默认不加入；
- 明确 skipped：不加入；
- 旧 ID 已不存在：报告 stale item 并拒绝静默替换。

failed rerun 的选择也要保存为新的 `selectionSnapshot`，不能修改原 TestRun。

## 16. 结果状态与错误分类

### 16.1 Task 与测试领域分层

Task outcome 继续表示通用编排结果：

- `succeeded`；
- `command_failed`；
- `cancelled`；
- `timed_out`；
- `interrupted`；
- `infrastructure_failed`。

如果测试 process 正常完成、Adapter 成功解释并持久化了 assertion failure，Task outcome 是 `succeeded`，TestRun outcome 是 `failed`。Client 判断测试是否通过时必须读取 TestRun，不得读取 Task outcome 代替。

Build command 失败仍产生 `command_failed` Task，并产生 `blocked` TestRun。取消和 timeout 同时反映到 Task 与 TestRun。

能够被 Test Coordinator 完整捕获并持久化的 crash、unexpected exit 或 malformed framework output 也是测试领域结果：Task outcome 为 `succeeded`，TestRun outcome 为 `errored`。如果 process 无法启动、结果事务无法提交或 Service 自身发生 I/O 故障，则 Task outcome 为 `infrastructure_failed`。

### 16.2 TestRun outcome

- `passed`；
- `failed`；
- `blocked`；
- `errored`；
- `cancelled`；
- `timed_out`；
- `interrupted`。

### 16.3 TestItemResult outcome

- `passed`；
- `failed`；
- `skipped`；
- `errored`；
- `cancelled`；
- `timed_out`；
- `not_run`。

`not_run` 必须带 reason，例如 build blocked、container terminated 或 selection aborted。

### 16.4 分类表

| 场景 | TestRun | TestItem | Diagnostic category |
|---|---|---|---|
| 全部通过或明确跳过 | `passed` | `passed` / `skipped` | 无 |
| 框架或 Mock 断言失败 | `failed` | `failed` | `assertion_failure` |
| 配置或 Catalog 不可用 | `blocked` | 不创建或 `not_run` | `configuration_error` |
| 增量构建失败 | `blocked` | `not_run` | `build_error` |
| executable crash | `errored` | 当前项 `errored`，其余 `not_run` | `test_process_crash` |
| timeout | `timed_out` | 当前项 `timed_out`，其余 `not_run` | `test_timeout` |
| 用户停止 | `cancelled` | 当前项 `cancelled`，其余 `not_run` | `cancelled` |
| 输出格式错误 | `errored` | 完整记录保留，其余 `not_run` | `framework_output_invalid` |
| Service/I/O/持久化故障 | `errored` | 结果不完整 | `infrastructure_error` |

### 16.5 退出码规则

- 非零退出码且存在 Adapter 验证的 assertion records：TestRun `failed`。
- 非零退出码且没有 assertion evidence：`unexpected_exit` 或 `test_process_crash`，不能猜测为断言失败。
- 零退出码但存在验证的失败记录：保留 `failed` 并增加 `inconsistent_exit_status`。
- Mock expectation failure 是 `assertion_failure`。
- CTest `WILL_FAIL` container 不进入 case-level Adapter；其反转语义由 CTest 保留。

### 16.6 partial results

流式 Parser 只提交语法完整的 record。发生 crash、timeout 或 malformed output 时：

- 已完整提交的 item result 保留并设置 `partial=true`；
- 当前不完整 item 标记为 `errored` 或 `timed_out`；
- 未报告 item 标记为 `not_run`；
- 不得根据 summary 数字或缺失输出推断 passed。

## 17. Source Location 与 Diagnostic

统一位置模型复用 Phase 3 的 URI、line、column 规则：

- URI 使用 canonical file URI；
- line 和 column 在 Protocol 中使用已定义的基准；
- CppUTest failure location、Unity manifest location 和 CTest backtrace 分别保留 provenance；
- Workspace 内位置 `navigable=true`；
- Workspace 外位置可以显示，但 `navigable=false`；
- path traversal、非法 URI、NUL 和越界行列产生安全 Diagnostic；
- Windows drive letter、separator 和 case 按 Phase 3 规则规范化；
- ANSI sequence 不得进入 message identity 或 path。

同一 failure 可以包含多个 location，例如 assertion location、Mock expectation declaration 和 actual call location。

## 18. 事件、持久化与恢复

### 18.1 事件

Phase 4 复用单 Task 单调 `sequence`，新增：

- `test.discovery.started`；
- `test.container.discovered`；
- `test.catalog.published`；
- `test.run.started`；
- `test.container.started`；
- `test.item.started`；
- `test.output`；
- `test.item.finished`；
- `test.container.finished`；
- `test.run.finished`。

Catalog item 通过分页响应或有界 batch event 发布，不为 10,000 个静态 item 强制生成 10,000 条独立持久化事件。

### 18.2 终态顺序

`test.run.finished` 只能在以下内容原子持久化后发出：

1. 所有已知 TestItemResult；
2. TestRun summary；
3. Diagnostic references；
4. output/artifact metadata；
5. Task terminal snapshot。

终态后到达的 stdout/stderr 按 Phase 2 bounded checkpoint 规则处理，不能修改已发布的 TestRun outcome。

### 18.3 SQLite

在现有 Repository 层新增：

- test catalog metadata；
- test run；
- selection snapshot；
- item result；
- failure detail；
- result-to-artifact reference。

大体积日志和完整 Catalog JSON 保存在 ArtifactStore；SQLite 保存索引、summary 和可查询字段。

### 18.4 重连

Client 使用现有 `events/subscribe` cursor 重放测试事件。重放必须保持原 event ID、sequence、timestamp 和 payload，不重新解析日志。

### 18.5 Service 重启

Service 启动恢复时：

- 运行中的 Test Task 按 Phase 2 规则变为 `interrupted`；
- 对应 TestRun 变为 `interrupted`；
- 已持久化完整 item result 保留；
- 当前和未运行 item 标记为 `not_run`，reason 为 `service_restarted`；
- 不尝试重新附着旧进程或自动续跑；
- Catalog 只有 fingerprint 仍有效时才可继续读取，否则标记 stale。

## 19. 安全边界

### 19.1 Protocol 不可表达执行细节

v1.3 的任何 request 都不能表达：

- executable 或 command；
- argv/raw args；
- Shell 或 script；
- cwd/working directory；
- environment/PATH/library path；
- CMake/CTest 额外参数；
- pre/post hook；
- result file path；
- Unity runner control flag。

这些字段必须在 Schema、generated TypeScript model、Go decode 和 router 层全部拒绝。

### 19.2 执行来源

允许生成 `ProcessSpec` 的来源只有：

- Phase 3 验证的 CMake installation；
- Phase 3 Build Profile 和 File API target；
- 当前 CTest JSON snapshot；
- 已注册 Framework Adapter；
- Service-owned artifact path；
- Service 常量和有界数值。

### 19.3 受信任 Workspace

CTest test 本质上是 Workspace 代码。Phase 4 继续遵循“一个 Service 实例绑定一个已授权 Workspace root”的 Phase 3 边界：

- inspect 和 catalog read 不执行代码；
- discovery/run 是显式执行动作；
- 未授权 Workspace 不启动 Service 执行实例；
- Phase 6 将 Code-OSS Workspace Trust 映射到该授权边界。

GitHub、Gitee 或其他网络连接不是执行前提。

### 19.4 路径

对 build root、target artifact、working directory、Unity manifest、result file 和 source location 执行：

- absolute/canonical normalization；
- symlink、junction 和 reparse point 解析；
- Workspace/build/artifact root containment；
- TOCTOU 前的 file identity 复核；
- Windows reserved path 与 alternate data stream 拒绝；
- result file 不跟随攻击者替换的 link。

### 19.5 资源限制

- Catalog 最多 100,000 个 case；
- container 最多 10,000 个；
- 单条 runner control record 最大 256 KiB；
- 单个 manifest 最大 64 MiB；
- repeat 最大 100；
- parser 和 event batch 有固定内存上限；
- stdout/stderr 继续使用 Phase 2 spill-to-artifact 和背压规则；
- 超限产生明确 Diagnostic，不截断后伪造成功结果。

## 20. 测试策略

### 20.1 Go unit tests

- CTest JSON 与 multi-config 解析；
- Windows/Linux command、URI 和 path；
- Framework Adapter 选择与冲突；
- CppUTest list/result parser；
- Unity runner JSON Lines parser；
- LF、CRLF、ANSI、UTF-8 分块和超长 record；
- 稳定 ID、NFC、重复 identity；
- Catalog revision 和 stale rebinding；
- selection/filter/repeat/failed rerun；
- CTest property allowlist 与降级；
- exit code、assertion、crash、timeout 和 malformed output 分类；
- partial result；
- event ordering 和 summary。

### 20.2 Protocol contract tests

- v1.3 所有 request/response/event 的 valid fixture；
- executable、command、args、shell、environment、cwd、hook 的 invalid fixture；
- v1.0/v1.1/v1.2 compatibility；
- handshake downgrade；
- unknown enum/additional property 拒绝；
- page/selection/repeat 上限；
- generated TypeScript 与 Go model 一致性。

### 20.3 CMake helper tests

- CppUTest label 和 target mapping；
- Unity runner/manifest 生成；
- multi-config output path；
- source path 含空格和 Unicode；
- manifest deterministic；
- exact-case dispatch；
- 重复 case 拒绝；
- 不支持宏形式的明确错误；
- helper version mismatch 降级。

### 20.4 Framework fixture

`testdata/frameworks/` 包含：

- CppUTest + CppUMock；
- Unity + CMock；
- Opaque CTest；
- CTest wrapper；
- fixture/dependency/resource lock；
- duplicate identity；
- stale executable；
- malformed list/result。

每个真实框架覆盖：

- pass；
- assertion failure；
- skipped/ignored；
- Mock expectation failure；
- crash；
- timeout；
- malformed output。

### 20.5 Integration tests

- configure/build/discover；
- 全部、container、group/suite、case；
- filter；
- repeat；
- failed rerun；
- cancel；
- timeout；
- Catalog stale；
-断线重连；
- Service 重启恢复；
- artifact spill；
- source location；
- CppUTest/Unity Opaque 降级。

### 20.6 Security regression

- Protocol execution-plan injection；
- CTest name regex metacharacters；
- executable symlink/junction escape；
- manifest/result file link swap；
- wrapper 冒充 direct executable；
- sidecar path traversal；
- environment key/value 注入；
- unknown CTest property；
- stale executable replacement；
- oversized runner output。

## 21. Hosted CI 矩阵

| 平台 | 工具链 | 框架与验证 |
|---|---|---|
| Windows | MSVC | CppUTest/CppUMock、Unity/CMock、multi-config、取消、超时、crash |
| Windows | clang-cl | CppUTest/CppUMock、Unity/CMock、路径与结果一致性 |
| Linux | GCC | CppUTest/CppUMock、Unity/CMock、取消、超时、crash |
| Linux | Clang | CppUTest/CppUMock、Unity/CMock、sanitizer 异常分类 |

CI 继续运行：

- Protocol compatibility；
- generated model drift；
- TypeScript tests；
- Go tests；
- lint/typecheck；
- 完整 `pnpm verify`；
- Windows/Linux Hosted E2E；
- 双平台报告门禁。

CppUTest、Unity 和 CMock fixture 依赖使用固定 upstream revision 与 SHA-256。下载只发生在开发/CI bootstrap，经过缓存和 checksum 验证；产品运行时没有下载框架的代码路径。

## 22. 验收标准

Phase 4 完成必须同时满足：

1. 四种工具链矩阵全部通过。
2. CppUTest/CppUMock 与 Unity/CMock 都能发现并运行真实测试。
3. 两个框架都支持全部、单个、过滤、重复和失败重跑。
4. Opaque fallback 能运行容器，且不会声称支持单用例。
5. 同一逻辑 case 在 Windows/MSVC、Windows/clang-cl、Linux/GCC 和 Linux/Clang 下拥有相同稳定 ID。
6. pass、assertion failure、skip、Mock failure、crash、timeout、cancel 和 malformed output 分类准确。
7. 断言失败不由退出码单独推断。
8. build error、test failure 与 infrastructure error 可由结构化字段明确区分。
9. source location 在工作区内可规范化，越界路径不可导航。
10. Catalog stale 时不会运行旧 executable 或错误 case。
11. 断线重连和 Service 重启后结果符合持久化与 interrupted 规则。
12. Protocol v1.0、v1.1、v1.2 compatibility 保持全绿。
13. v1.3 execution injection negative tests 全绿。
14. 10,000 项 synthetic Catalog 建立可复现的后端 benchmark，不设置依赖机器速度的易波动绝对门槛。
15. 完整 `pnpm verify` 与 Windows/Linux Hosted E2E 全绿。
16. 同一 commit 推送 GitHub 与 Gitee。

## 23. 风险与缓解

| 风险 | 缓解 |
|---|---|
| CTest command 包含 wrapper 或复杂 properties | case-level compatibility allowlist；不兼容时由 CTest 做容器级运行 |
| CppUTest 人类可读输出随版本变化 | 固定 fixture 版本、Golden File、分块 parser、contract capability 和 malformed 降级 |
| Unity 任意项目缺少可靠 list/filter | opt-in CMake helper、generated runner、versioned control protocol |
| helper 造成额外构建依赖 | 使用产品自有 Go generator，不要求 Ruby/Ceedling；helper 必须显式 opt-in |
| Mock failure 与普通 assertion 混淆 | 统一 assertion category，同时保留结构化 mock detail subtype |
| 非零退出码误报 test failure | assertion 必须有 Adapter evidence；否则归类 crash/unexpected exit |
| Catalog 与 executable 不一致 | executable SHA-256、manifest hash、adapter version 和 run 前 fingerprint 复核 |
| 跨平台 ID 漂移 | 只使用逻辑身份；禁止 path/profile/compiler/order 进入 ID；四平台契约测试 |
| 大量测试导致内存或事件爆炸 | 100,000 item 上限、分页、batch event、Catalog artifact 与 SQLite metadata 分离 |
| 测试 Service 被命令注入 | v1.3 negative fixture、Service-owned Planner、exact-name mapping、无 raw args/env/cwd |
| CTest property 语义被 direct run 丢失 | 明确 allowlist；未知或复杂 property 自动降级 |
| 框架 upstream 下载影响最终产品 | 下载仅限开发/CI；固定 revision/checksum；产品运行时无网络依赖 |

## 24. 后续阶段接口

Phase 5 可以复用：

- TestRun 与 TestItemResult；
- Framework Adapter 的稳定选择与 execution plan；
- profile/toolchain metadata；
- source location；
- ArtifactStore；
- iteration 与 summary。

Phase 5 在此基础上增加 clang-cl/llvm-cov、GCC/gcovr、Clang/llvm-cov、JUnit XML 和 HTML，不改变 assertion/error 语义。

Phase 6 将：

- Code-OSS Workspace Trust 映射到 Service execution authorization；
- Test Catalog 投射到 Testing API；
- 把 Protocol v1.3 event 映射为 Testing API run event。

Phase 7 将：

- 增加 Test UX、过滤器编辑、失败详情、历史记录和报告视图；
- 增加 CppUMock/CMock Mock/Stub 配置和生成工作流；
- 对 10,000 测试项设置正式 UI 响应门禁。

新框架 Adapter 必须继续遵循 CTest-first、稳定 ID、evidence-based result 和 Service-owned `ProcessSpec`。

## 25. 参考资料

- [CTest command-line reference](https://cmake.org/cmake/help/latest/manual/ctest.1.html)
- [CMake Presets](https://cmake.org/cmake/help/latest/manual/cmake-presets.7.html)
- [CppUTest manual](https://cpputest.github.io/manual.html)
- [CppUMock manual](https://cpputest.github.io/mocking_manual.html)
- [Unity repository](https://github.com/ThrowTheSwitch/Unity)
- [Unity Getting Started Guide](https://github.com/ThrowTheSwitch/Unity/blob/master/docs/UnityGettingStartedGuide.md)
- [Unity test runner generator](https://github.com/ThrowTheSwitch/Unity/blob/master/auto/generate_test_runner.rb)
- [CMock repository](https://github.com/ThrowTheSwitch/CMock)
