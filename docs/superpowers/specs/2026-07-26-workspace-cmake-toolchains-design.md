# Phase 3：工作区、CMake 与工具链适配器设计

**日期：** 2026-07-26

**状态：** 已确认

**目标分支：** `codex/workspace-cmake-toolchains`

**基础提交：** `f5190ef9230469e913f8f66725c0c46e2936d9bf`

## 1. 背景

Phase 1 已建立版本化 JSON 协议、TypeScript 客户端、Go 本地服务、per-user IPC、token handshake、能力查询和安全关闭流程。

Phase 2 已建立任务状态机、跨平台进程树控制、取消与超时、结构化事件顺序、断线重连与重放、SQLite 历史记录及 ArtifactStore。它只允许服务内部生成的模拟任务，客户端仍不能提交 executable、参数数组、环境变量或 Shell 文本。

Phase 3 在该安全边界上加入真实 C/C++ 工作区、CMake 和编译器构建能力。目标不是简单地把命令行转交给 Service，而是把工作区配置、CMake Presets、工具链能力和构建目标转换为 Service 自己拥有的类型化 `ExecutionPlan`，再交给 Phase 2 Task Engine 执行。

## 2. 已确认的决策

1. 一个 Go Service 实例固定绑定一个受信任的 workspace root。
2. CMake Presets 优先；没有 Presets 时，由 Service 根据结构化配置生成受控 configure/build 参数。
3. Build Task 仅在首次构建或配置失效时执行 configure。
4. Phase 3 完成 Go Service、Protocol、TypeScript Client 和真实工具链 E2E，不提前制作 Code-OSS UI。
5. 产品安装包内置固定版本的 CMake，运行时不联网下载。
6. 默认使用内置 CMake，同时允许受信任的工作区配置指定其他 CMake 绝对路径。
7. 产品不内置编译器；自动检测本机 MSVC、clang-cl、GCC 和 Clang，并允许结构化手动配置。
8. 结构化诊断覆盖 CMake configure、编译器 warning/error 和 linker error。
9. 扩展现有 Task Engine，使其执行 Service 生成的类型化多步骤 `ExecutionPlan`，不另建平行 Build Manager。
10. Protocol v1.0 和 v1.1 保持兼容；Phase 3 新能力通过 Protocol v1.2 暴露。

## 3. 目标

- 将 Service 启动生命周期绑定到一个规范化的 workspace root。
- 定义严格、可版本化的工作区配置 Schema。
- 发现 `CMakePresets.json`、`CMakeUserPresets.json` 和无 Presets 的 CMake 项目。
- 解析产品内置 CMake，并支持受信任的绝对路径覆盖。
- 自动发现和验证 Windows MSVC、Windows clang-cl、Linux GCC 和 Linux Clang。
- 将 CMake 项目、Preset 和工具链组合规范化为稳定的 Build Profile。
- 使用参数数组生成 configure/build `ExecutionPlan`，不经过 Shell 执行构建。
- 将 Phase 2 Task Engine 扩展为可观测、可取消、可持久化的多步骤任务。
- 使用 CMake File API 获取 codemodel、cache、cmakeFiles、toolchains 和 targets。
- 输出统一的 CMake、compiler 和 linker diagnostics。
- 通过 TypeScript Client 和 Protocol v1.2 完成工作区扫描与真实构建。
- 在 Windows 与 Linux Hosted CI 上验证真实平台工具链。

## 4. 非目标

- Code-OSS 页面、Testing API、状态栏或工具链选择 UI。
- CTest、CppUTest、CppUMock、Unity 或 CMock 的测试发现与执行。
- `gcovr`、`llvm-profdata` 或 `llvm-cov` 覆盖率采集。
- 任意 executable、Shell、hook、脚本、原始参数或环境变量注入。
- 自动安装或联网下载编译器。
- 最终签名安装包、自动更新与回滚；这些属于 Phase 8。
- 恶意工作区代码的操作系统级沙箱。
- 浏览器直接访问本机编译器。
- macOS 工具链支持。

## 5. 总体架构

```text
TypeScript Client / Protocol v1.2
                │
                │ 已认证的本地 IPC
                ▼
Go Session / Router
                │
                ├────────► Workspace Inspector
                │              ├─ Workspace Config
                │              ├─ CMake Presets
                │              └─ CMake File API
                │
                ├────────► Toolchain Registry
                │              ├─ MSVC Adapter
                │              ├─ clang-cl Adapter
                │              ├─ GCC Adapter
                │              └─ Clang Adapter
                │
                ▼
Execution Planner ───────► 多步骤 Task Engine
                                   │
                                   ▼
                           Process Controller
                                   │
                          ┌────────┴────────┐
                          ▼                 ▼
                    内置 CMake       本机 Compiler/Linker
```

Session 负责认证、Schema 校验、协议版本和路由。Workspace Inspector 与 Toolchain Registry 负责把文件系统和工具状态转换为领域对象。Execution Planner 是唯一能够把这些对象转换为 executable、参数数组、工作目录和环境增量的组件。Protocol 和 TypeScript Client 均不能构造 `ProcessSpec`。

## 6. Workspace 绑定与信任边界

### 6.1 单实例单工作区

Service 新增必需的 `--workspace-root` 启动参数。启动时执行以下步骤：

1. 将输入转换为绝对路径。
2. 解析现有路径组件中的 symlink，以及 Windows junction/reparse point。
3. 验证根目录存在且为目录。
4. 生成不依赖路径大小写表现形式的稳定 `workspaceId`。
5. 将规范路径固定到 Service 实例，Protocol 不提供更改或注册其他根目录的方法。

后续所有 workspace 相对路径都必须在解析链接后仍位于该根目录内。仅做字符串前缀比较不构成有效的边界检查。

### 6.2 允许的 workspace 外部路径

Service 只允许访问以下 workspace 外部范围：

- Service 自己的 data directory 和 ArtifactStore；
- 产品内置且通过 manifest 校验的 CMake；
- 已发现或在受信任配置中指定的 CMake、compiler、linker 和 build tool；
- 对应工具链声明的 SDK、include、library 和 sysroot；
- Service 自己的临时目录。

工作区文件、Preset include、source directory 和配置引用默认不得逃逸 workspace root。

### 6.3 Workspace Trust

Phase 3 没有 Code-OSS UI，因此 E2E 和开发探针必须通过显式启动条件表示工作区已受信任。Phase 6 接入 Code-OSS 后，只有 Workspace Trust 已授予时才允许启动具备构建能力的 Service。

“工作区已受信任”不意味着跳过输入校验。工作区配置仍按不可信数据处理：

- 严格 Schema；
- 拒绝未知字段；
- 限制文件大小、JSON 深度、项目数量和工具链数量；
- 不支持 command、hook、script、rawArgs 或任意 environment map；
- 自定义 executable 必须是规范绝对路径和普通可执行文件；
- 构建前重新验证引用对象。

这里禁止的是 Protocol 和 `.unit-test-ide/workspace.json` 注入原始命令。受信任项目自己的 `CMakeLists.txt` 和 CMake Preset 仍可能包含 CMake 原生的 command、environment 或 `nativeToolOptions`；它们属于工作区代码执行语义，只有授予 Workspace Trust 后才能由 CMake 解释。Service 只向 CMake 传递已经验证的 preset name，不把这些项目字段转换成客户端可控制的 `ProcessSpec`。

Service 不承诺隔离受信任工作区自身的 CMake 脚本和编译过程。对恶意构建脚本的容器或虚拟机沙箱不属于本阶段。

## 7. 工作区配置 Schema

配置文件位置固定为：

```text
<workspace>/.unit-test-ide/workspace.json
```

配置文件可选。`version: 1` 的示例形状如下：

```json
{
  "version": 1,
  "cmake": {
    "executable": "C:/Tools/CMake/bin/cmake.exe"
  },
  "projects": [
    {
      "id": "core",
      "sourceDir": ".",
      "fallback": {
        "configurations": ["Debug", "Release"],
        "preferredGenerator": "Ninja"
      }
    }
  ],
  "toolchains": [
    {
      "id": "linux-clang",
      "family": "clang",
      "cCompiler": "/usr/bin/clang",
      "cppCompiler": "/usr/bin/clang++"
    }
  ]
}
```

Schema 规则：

- `sourceDir` 必须是 workspace 内的相对目录。
- `project.id` 和手动 `toolchain.id` 在文件内唯一，并满足受限标识符格式。
- `cmake.executable`、`cCompiler` 和 `cppCompiler` 必须是绝对路径。
- MSVC 手动配置使用安装实例、toolset、host architecture 和 target architecture 等结构化字段，不接受 `.bat` 参数。
- `configuration`、generator 和 architecture 只接受已知值或经过 Adapter capability 验证的值。
- fallback 配置不接受 `-D`、native build options 或其他原始参数。
- 默认限制为 256 KiB、64 个项目和 64 个手动工具链；具体常量在实施计划中固化并测试。

如果配置文件不存在：

- workspace 根目录存在 `CMakeLists.txt` 时，生成一个 ID 为 `root` 的项目；
- 不递归把 `third_party` 或子模块中的 `CMakeLists.txt` 自动视为顶层项目；
- Monorepo 和嵌套项目必须通过 `projects` 显式声明。

配置错误作为 workspace diagnostics 返回。存在阻断性错误的项目不能生成 Build Profile。

## 8. CMake Resolver 与内置发行包

### 8.1 解析顺序

CMake Resolver 按以下顺序选择 executable：

1. 受信任工作区明确配置的绝对路径；
2. 产品安装包内置的固定版本 CMake；
3. 仅在开发和测试模式下，由 Service 启动参数注入的 CMake。

产品模式不隐式搜索 `PATH`，也不在运行时联网下载 CMake。

### 8.2 Bundle Manifest

仓库保存 CMake bundle manifest，至少包含：

- CMake version；
- platform 和 architecture；
- 官方分发文件名及来源；
- archive SHA-256；
- 安装后关键文件 SHA-256；
- license 标识和 license 文件路径。

Phase 3 建立可测试的 bundle layout、resolver 和完整性校验。普通 `pnpm verify` 不联网；CI 的独立准备步骤可以从 CMake 官方来源取得固定包并校验摘要。最终产品安装包把已校验的 CMake runtime 和 BSD 3-Clause License 一起分发，最终签名与安装器仍由 Phase 8 完成。

CMake 版本只能通过显式 manifest 变更升级，不能使用 `latest`。升级必须经过 Presets、File API、真实工具链和 diagnostics E2E。

### 8.3 自定义 CMake

自定义 CMake 必须：

- 仅在受信任工作区生效；
- 是规范化的绝对 executable；
- 通过固定参数的 `--version` probe；
- 满足产品支持的最低版本；
- 支持所请求的 Presets Schema 和 File API object version；
- 不由 Protocol 临时指定。

自定义 CMake 的路径、版本和文件身份进入 workspace generation 与 configure fingerprint。

## 9. CMake Project、Presets 与 Build Profile

### 9.1 Presets

每个声明项目可以包含：

- `CMakePresets.json`
- `CMakeUserPresets.json`

Service 支持 CMake 的 configure presets 和 build presets。Preset 的 include、inheritance、condition 和 macro expansion 由实际选定的 CMake 解释，Service 不实现另一套完整 CMake 求值器。

Phase 3 对文件边界采用更严格规则：

- 所有直接或间接 include 默认必须位于 workspace root 内；
- `CMakeUserPresets.json` 指向 workspace 外部的 include 会产生阻断性配置诊断；
- include cycle、不可读文件、超限文件或所选 CMake 不支持的 Schema version 均返回稳定错误。

Service 使用固定版本 CMake 的 preset listing 能力取得当前平台上有效的 preset name，再用有界 JSON 读取补充 display name、description 和 preset 关联。测试针对锁定 CMake 版本固定该适配层，不把人类可读输出当作跨任意版本的永久协议。

### 9.2 Build Profile

Workspace Inspector 将可构建组合规范化为 `BuildProfile`：

```text
BuildProfile
├─ profileId
├─ projectId
├─ origin: preset | generated
├─ configurePreset
├─ buildPreset
├─ toolchainId
├─ generator
├─ configuration
├─ binaryDir
└─ capabilities
```

Preset 模式：

- configure/build preset 的语义由 CMake 保留；
- Build Profile 记录组合关系和可显示元数据；
- 如果没有对应 build preset，Service 使用 configure preset 的 binary directory 生成受控的 `cmake --build` 步骤；
- Preset 自己选择 compiler 时，Service 从 configure 后的 File API 验证实际 toolchain。

Generated 模式：

- 必须选择已验证 toolchain；
- Windows MSVC 使用对应 Visual Studio generator 和 architecture；
- Linux GCC/Clang 显式选择经过检测的 generator；
- Linux 优先使用可用的 Ninja，否则使用经过验证的 Unix Makefiles；
- Windows clang-cl 仅在所需 LLVM、MSVC/Windows SDK 环境及 generator 均可用时生成 Profile；
- Service 显式传递 source、binary directory、generator、configuration 和 compiler，不依赖 CMake 的隐式默认发现；
- 不接受客户端提供的 cache arguments。

Build Profile ID 由 project、origin、Preset 或生成配置、toolchain identity 和 configuration 确定，Service 重启后保持稳定。

### 9.3 Build Directory

默认 build directory 位于：

```text
<data-dir>/workspaces/<workspace-id>/build/<profile-id>
```

它不污染源码树，并处于 Service 已控制的 ACL 和清理边界内。Preset 显式定义的 binary directory 只有在位于 workspace 或 Service build root 内时才允许使用；其他位置在 Phase 3 中拒绝。

每个 build directory 使用进程内互斥与文件锁双重保护，避免并发 Service 或并发任务同时 configure/build 同一目录。

Windows generated profile 传给 CMake 的 compiler path 统一规范化为 `/` 分隔符，避免 CMake 生成的内部脚本把 `\P` 等片段解释为转义。Native E2E 的短生命周期 Service data root 使用 repository 管理的 `.native-e2e/work`：普通 clone 位于当前 checkout，`.worktrees/<name>` managed worktree 位于主 checkout。它不使用用户 profile temp，因此 Service 仍能以不共享 delete 的句柄固定全部祖先；同时为 Visual Studio generator 的 `CMakeScratch/TryCompile` 和 MSBuild `.tlog` 保留传统 260 字符路径预算。workspace 本身仍覆盖空格与 Unicode。正式客户端同样必须把 Service data root 放在短、固定、owner-only 且祖先可固定的产品目录，不能通过放宽 owner-only/TOCTOU 验证来兼容任意 temp 路径。

## 10. Workspace Generation 与 Configure Fingerprint

`workspace/inspect` 返回 `workspaceGeneration`。它是以下规范化信息的 SHA-256：

- workspace 配置；
- project 定义；
- Presets 文件及允许的 include 图；
- CMake executable identity 和 version；
- 已验证 toolchain descriptor；
- Build Profile 定义。

哈希输入使用确定性的字段顺序、列表排序、路径规范化和 JSON 编码，不能依赖文件枚举顺序或 map 迭代顺序。

它不包含普通 `.cpp`/`.h` 源文件内容，正常源码编辑不会使客户端选择立即失效。

`configureFingerprint` 用于决定是否执行 configure，包含：

- 相关 workspace generation 子集；
- profile、generator、configuration 和 binary directory；
- CMake 与 toolchain identity；
- Presets；
- 上次 File API `cmakeFiles` 返回的 CMake input 文件状态；
- 上次 configure 的 cache 和 File API reply 状态。

以下任一情况都会执行 configure：

- build directory 或 cache 不存在；
- profile、CMake、toolchain 或 Preset 变化；
- File API query/reply 缺失、损坏或版本不支持；
- 已知 CMake input 变化；
- 上一次 configure 未成功；
- Service 无法证明现有配置仍有效。

configure 成功后，Service 原子更新 `build_configurations`。无法确定时选择重新 configure，不假设缓存有效。

## 11. CMake File API 与 Targets

Service 在 configure 前写入 client-owned File API query，申请：

- `codemodel`
- `cache`
- `cmakeFiles`
- `toolchains`

configure 成功后：

1. 验证 reply index 位于预期 build directory。
2. 验证 object kind 和支持的 major/minor version。
3. 读取 projects、configurations、targets、source/build path 和实际 toolchain。
4. 把原生 target name 映射为稳定的 `targetId`。
5. 保存用于 configure invalidation 的 CMake input 信息。

File API 路径按用途划分边界：

- reply、target、artifact、cache 和需要 snapshot 的 CMake input 必须位于 workspace、Service data root、已校验的 bundled CMake install root，或当前 verified toolchain 的 compiler/sysroot root；
- compiler path 是 toolchain identity metadata，允许位于 workspace 外，但只接受有界的规范绝对路径；解析器不打开它，也不因此授予执行或任意文件读取权限；
- CMake executable 的执行权限仍只来自 Resolver 固定的 `Installation`，File API 不能新增 executable authority。

`cmake/targets/list` 只读取最近一次成功 configure 的有效 File API reply，不隐式执行项目代码。如果尚未 configure，返回 `CONFIGURE_REQUIRED`。

客户端只能把 Service 返回的 `targetId` 交回 `tasks/start`。执行前若 configure 使 target 集合变化，Service 重新解析 target；目标已不存在时，在启动 build step 前以 `TARGET_NOT_FOUND` 结束，绝不把未经验证的 target 字符串传给 CMake。

## 12. Toolchain Adapter

### 12.1 公共接口

平台 Adapter 实现统一职责：

- `Discover`
- `Probe`
- `Validate`
- `BuildEnvironment`
- `CMakeConfiguration`
- `DiagnosticParser`

规范化的 `Toolchain` 至少包含：

- `toolchainId`
- family 和 frontend
- C/C++ compiler path
- version
- target triple
- host/target architecture
- sysroot 或 SDK identity
- generator capabilities
- 后续 coverage capability 占位

Toolchain ID 根据 family、规范 executable identity、version、target triple、architecture 和 SDK identity 确定。

### 12.2 Windows MSVC

- 使用 Visual Studio 安装器附带的 `vswhere` 定位包含 C++ workload 的实例。
- `vswhere` 会把可扩展的 Visual Studio Installer property store 投影到顶层 JSON。Adapter 先执行总大小、UTF-8、NUL、重复 key 和 installation 数量边界校验，再只把 `instanceId`、`installationPath`、`installationVersion`、`isComplete`、`isLaunchable` 投影到发现模型；其他有界 metadata 不参与 path、argument、environment 或 executable 决策，Visual Studio Installer 增加字段时也不会扩大执行面。
- Visual Studio generator capability 需要安装实例内、已固定 identity 的 `MSBuild.exe` 以固定参数成功返回有界 version 输出；parser 同时支持单行 numeric 形式与 VS 2026 的 banner 加 numeric 形式，拒绝互相冲突的 major，并要求其 major 与 `vswhere installationVersion` 一致。generator 名称只由该已验证 installation major 决定。
- 使用固定模板调用安装实例自己的 `VsDevCmd.bat`，捕获特定 host/target architecture 的环境。
- `.bat` 调用是受信任 MSVC Adapter 的发现步骤，不是 Build Task 的 Shell 执行入口。
- `VsDevCmd.bat` 路径来自已验证 Visual Studio 实例；architecture、toolset 和 SDK 参数来自枚举，不能包含客户端文本。
- 捕获结果解析为 environment map，删除 token 和不允许继承的敏感变量后缓存。
- 正常 configure/build 直接启动 CMake executable，并传入已捕获环境，不通过 Shell。

### 12.3 Windows clang-cl

- 检测 LLVM 安装中的 `clang-cl`、`lld-link` 和版本。
- Ninja 先从固定的独立 CMake 安装位置发现；若该位置不存在，只允许回退到当前已验证 Visual Studio 实例随附的 `Common7/IDE/CommonExtensions/Microsoft/CMake/Ninja/ninja.exe`。回退路径必须仍位于该实例固定 identity 的安装根内，probe 前后复验 executable、Ninja 父目录和 Visual Studio 安装根，不能从用户 `PATH` 补全。
- 组合已验证的 MSVC/Windows SDK environment。
- 验证 CMake generator 是否支持该组合。
- Phase 3 完成真实构建与诊断；只记录 `llvm-profdata`/`llvm-cov` capability，不执行覆盖率。

### 12.4 Linux GCC 与 Clang

- 自动搜索 `gcc`/`g++`、`clang`/`clang++`，并允许配置绝对路径。
- 使用固定参数探测 version、target triple 和 sysroot。
- 验证 C/C++ compiler pair 属于兼容 family 和 target。
- 检测 Ninja 或 Make 等 native build tool，但不自动安装。
- 生成模式明确设置 `CMAKE_C_COMPILER` 和 `CMAKE_CXX_COMPILER`。

### 12.5 环境处理

构建环境从 Service 维护的最小基础环境开始，再合并：

1. 平台所需基础变量；
2. 经过验证的 toolchain environment；
3. CMake Preset 自己的 environment 语义。

IPC token、GitHub token、数据库信息和 Service 内部控制变量始终剔除。Preset 的 `$penv{}` 只能看到该清理后的父环境。环境内容不写入数据库、事件、日志或 `execution-plan.json`。

## 13. 多步骤 ExecutionPlan

内部模型：

```text
ExecutionPlan
├─ taskKind
├─ planVersion
├─ planFingerprint
└─ steps[]
   ├─ configure（按需）
   └─ build
```

每个 Step 包含：

- 固定枚举的 `stepId` 和 `kind`；
- executable；
- args 数组；
- working directory；
- 经过过滤的 environment overlay；
- diagnostic parser；
- 可公开的脱敏命令摘要。

Execution Planner 只能接收已经验证的 Workspace、Build Profile、Toolchain 和 Target 领域对象。进入 Process Controller 前执行第二次防御性校验：

- executable 属于当前 CMake 或 Toolchain；
- working directory 位于 workspace 或 Service data directory；
- Step 总数最多 8；每个 Step 的 `ProcessSpec.Args` 最多 256 项、`ProcessSpec.Env` 最多 256 项，脱敏 `CommandSummary.Args` 同样最多 256 项；
- 参数中不存在 NUL；
- native target 已从 target ID 安全解析；
- 环境中不存在 Service secret。

Phase 3 的计划最多包含 configure 和 build 两个进程 Step。后续 Phase 可扩展新的 Step kind，但不能通过 Protocol 直接创建 Step。

### 13.1 Task 状态与 Step 状态

Task 继续使用 Phase 2 状态：

- `queued`
- `running`
- `cancelling`
- `finished`

每个 Step 使用：

- `pending`
- `running`
- `succeeded`
- `failed`
- `skipped`

Task timeout 是整个计划的总时限。取消、超时或任一步骤失败后不启动后续步骤。

结果映射：

- 全部步骤退出码为 0：`succeeded`
- configure/build 非零退出：`command_failed`
- 用户取消：`cancelled`
- 总时限耗尽：`timed_out`
- 运行中的 Service 被中断：`interrupted`
- 进程无法启动、存储故障或内部计划错误：`infrastructure_failed`

Diagnostic 数量不替代进程退出码。存在 warning 的成功构建仍是 `succeeded`；工具返回非零但解析不到 diagnostic 时仍是 `command_failed`。

### 13.2 进程与并发

每个 Step 复用 Phase 2 的 Process Controller：

- Windows Job Object；
- Linux Process Host、Process Group/Session 和 pollable control fd；
- 完整进程树取消与超时；
- stdout/stderr 有界采集；
- 服务崩溃后的残留清理。

同一 build directory 的任务串行执行。不同 profile 可以并行，但仍受 Service 全局并发上限约束。

## 14. Protocol v1.2

### 14.1 版本兼容

- v1.0 和 v1.1 Schema、fixtures、生成模型及响应形状保持不变。
- handshake 选择双方支持的最高版本。
- 未协商 v1.2 时调用 Phase 3 方法返回 `PROTOCOL_FEATURE_UNAVAILABLE`。
- Protocol Schema 仍是 TypeScript 和 Go wire model 的唯一事实来源。

### 14.2 新增方法

#### `workspace/inspect`

请求 payload 为空。响应包含：

- `workspaceId`
- `workspaceUri`
- `workspaceGeneration`
- 配置状态与 diagnostics
- projects
- Build Profiles
- Toolchains

该方法可以读取配置、运行固定的 CMake preset listing 和 compiler capability probe，但不执行项目 configure 或 build。

Toolchain 只投影 family、version、target triple、host/target architecture、已验证 generator 和 `gcov`/`llvm-cov` capability 名称；不返回 compiler path、environment、sysroot 或 coverage executable path。Build Profile 返回 `origin`、安全的 `toolchainId` 关联、generator 和 configuration。

workspace 配置无效或超过大小上限时，Runtime 忽略其中的 CMake override 与 manual toolchain，不在 pre-READY 阶段退出；Service 以安全空配置启动，并由 `workspace/inspect` 返回 `WORKSPACE_INVALID_CONFIG` 或 `WORKSPACE_CONFIG_TOO_LARGE` 阻断诊断。这样客户端可以解释和修复配置，同时无效配置不会获得执行能力。

`sourceDir` 在配置解码阶段按 workspace root 解析真实路径。Linux symlink 或 Windows junction/reparse point 指向 workspace 外部时，配置在 project inspection 与进程创建之前被拒绝，并由 `workspace/inspect` 返回 `WORKSPACE_INVALID_CONFIG`；`PROJECT_INVALID` 仅用于已通过配置解码、但项目内容本身无效的情况。

#### `cmake/targets/list`

请求包含：

- `workspaceGeneration`
- `projectId`
- `buildProfileId`

响应来自最近一次成功 configure 的 File API。如果没有有效 reply，返回 `CONFIGURE_REQUIRED`。

#### `tasks/start`

v1.2 使用严格的 discriminated union。原有 simulation 请求继续支持；新增 `cmakeBuild`：

```json
{
  "idempotencyKey": "0123456789abcdef0123456789abcdef",
  "kind": "cmakeBuild",
  "workspaceGeneration": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
  "projectId": "core",
  "buildProfileId": "windows-msvc-debug",
  "targetIds": ["unit_tests"],
  "jobs": 8,
  "timeoutMs": 600000
}
```

规则：

- `workspaceGeneration`、project、profile 和 target 必须匹配当前 Service 状态；
- target 为空时构建 CMake 默认 target；
- `jobs` 与 `timeoutMs` 具有 Schema 和 Service 双重上限；
- 请求不能包含 executable、args、env、workingDirectory、preset path 或 native tool options；
- 同一 idempotency key 与相同规范化请求返回原任务；
- 同一 idempotency key 与不同请求返回 `IDEMPOTENCY_CONFLICT`。

幂等 identity 严格由 `Kind`、语义等价的规范化 `Request` JSON、`WorkspaceGeneration` 与 `Timeout` 组成。JSON object key 顺序不影响等价性，JSON number使用精确语义比较，不经不必要的浮点转换；无效 JSON fail closed。`RequestHash` 只是 fast path实现细节，`PlanFingerprint`、runtime-only `ExecutionPlan` 与 `ExecutionBoundary` 都不属于幂等 identity。hash算法变化或旧数据库保留历史 hash时，Manager与 Store必须共用同一个语义等价判断完成 replay。

### 14.3 Task Snapshot

v1.2 `TaskSnapshot` 是联合类型：

```text
TaskSnapshot
├─ simulation
│  └─ scenario
└─ cmakeBuild
   ├─ workspaceGeneration
   ├─ projectId
   ├─ buildProfileId
   ├─ requestedTargetIds
   ├─ activeStep
   └─ steps[]
```

旧版本客户端只会收到旧版本响应，不向 `additionalProperties: false` 的 v1.0/v1.1 模型加入字段。

### 14.4 事件

保留：

- `task.created`
- `task.started`
- `task.output`
- `task.cancellation_requested`
- `task.finished`
- `artifact.created`

新增：

- `task.step_started`
- `task.step_finished`
- `task.diagnostic`

v1.2 为每种事件定义严格 payload Schema。`task.output` 包含 `stepId` 和 stream；`task.diagnostic` 包含规范化 Diagnostic。所有事件继续使用 SQLite 分配的全局 sequence，并支持 Phase 2 的重连、重放和去重。

### 14.5 Capabilities

v1.2 `capabilities/get` 报告 Service 支持的产品能力，例如：

- `taskKinds`
- `singleWorkspace`
- `cmakePresets`
- `cmakeFileApi`
- `multiStepTasks`
- `structuredDiagnostics`
- 支持的 toolchain family

本机实际安装的 Toolchain 实例由 `workspace/inspect` 返回，不放进静态 capabilities。

## 15. SQLite Migration 与恢复

新增版本化 migration：

- 将 `tasks.kind` 从仅允许 `simulation` 扩展为 `simulation | cmake_build`；
- `scenario` 对 `cmake_build` 允许为空；
- 为旧 simulation 行回填规范化 `request_json`；
- 新增 `workspace_generation`、`plan_fingerprint` 和 `active_step`；
- 新增 `task_steps`；
- 新增 `build_configurations`。

由于现有 `tasks.kind` 使用 CHECK constraint，migration 使用事务化的新表、数据复制、索引重建和表交换方式修改约束。迁移失败必须回滚到原 Schema，不能留下半迁移数据库。

已发布的 `002_multistep_tasks.sql` 及其 checksum保持不变。下一号 migration把由 v1 schema迁移来的 simulation `request_json` 规范化为同时包含行内 `scenario` 与 `timeout_ms` 的 `{"scenario":"success","timeoutMs":30000}` 形状，并保留历史 `request_hash`；当前语义 fallback负责兼容旧 hash。

`task_steps` 记录：

- task ID 和 step ordinal；
- step ID/kind；
- 状态、时间；
- exit code；
- 稳定 error code。

`build_configurations` 记录：

- workspace/project/profile identity；
- configure fingerprint；
- build directory；
- CMake/File API identity；
- 最近成功 configure 时间。

数据库不保存完整 `ExecutionPlan` environment。持久化内容仅包括规范化请求、计划 fingerprint、脱敏 Step 元数据和状态。

### 15.1 启动恢复

- Phase 2 simulation 的非终止任务继续按原语义恢复为 `interrupted`。
- `cmakeBuild` 的 `running` 或 `cancelling` 任务恢复为 `interrupted`，不尝试重新附着原进程。
- 尚未开始执行的 `cmakeBuild/queued` 可以从 `request_json` 重新规划，但必须重新验证 workspace generation、profile、toolchain 和 target。
- queued task 的 generation 已变化或引用对象失效时，以 `interrupted` 结束，并记录 `WORKSPACE_CHANGED` 或对应稳定错误。
- 已完成任务、事件和 artifacts 保持可查询与重放。

### 15.2 cleanup属于 completion

Task/Step completion的最终所有权规则以[Close-before-terminalization 所有权设计](./2026-07-28-close-before-terminalization-design.md)为准。任何持有 non-nil `ManagedProcess` 的执行路径都先把 Process result或 Start/Prepare cleanup cause暂存在 runtime-only pending completion中；只有 Process `Close`成功后，才能提交 Step/Task completion、terminal Artifact/events或`DeleteLease=true`。

intermediate Step顺序固定为 `result -> Close -> StepSucceeded/DeleteLease -> next Step`；final Step顺序固定为 `result -> Close -> terminal Step/Task/Artifact/events/DeleteLease`。normal `Close` failure保留 nonterminal Task、durable lease与 active owner，只允许显式 `Shutdown`在同进程 retry。若 Service在 result、`Close`或 completion transaction窗口退出，restart recovery通过仍存在的 lease取得 cleanup ownership，并把 runtime-only result保守恢复为 `interrupted`。

该规则不新增 Protocol状态、SQLite migration或 Process host接口；v1.1 output payload、event sequence与 replay投影保持不变。

## 16. 结构化 Diagnostics

统一模型：

```text
Diagnostic
├─ diagnosticId
├─ taskId
├─ stepId
├─ source: cmake | compiler | linker
├─ toolchainId
├─ severity: error | warning | note | info
├─ code
├─ message
├─ fileUri
├─ range
├─ related[]
└─ external
```

Protocol 中的 line/column 使用零起始、结束位置不包含在范围内。解析器保存工具的原始一基坐标语义，再统一转换并测试边界。

### 16.1 解析器范围

- CMake configure 单行和多行 error/warning；
- MSVC `Cxxxx` error/warning；
- MSVC linker `LNKxxxx`；
- clang-cl 的 MSVC 风格或 Clang 风格输出；其中 MSVC-style location 允许 MSVC 的 `Cnnnn` code，也允许 clang-cl 只输出 `error:`/`warning:` 而没有 numeric code；无 numeric code 时规范化为非空的 `COMPILER_ERROR`/`COMPILER_WARNING`/`COMPILER_NOTE`，满足 Protocol 与 ArtifactStore 的非空 code 不变量；
- GCC/Clang `file:line:column`；
- GNU ld、LLD、lld-link、link.exe 常见 linker error；
- GCC/Clang build Step 使用同一个 `FamilyGNU` 流式 parser：先匹配 compiler diagnostic；未匹配时只回落到受限的 GNU ld/LLD/`collect2` linker pattern，从而覆盖 Ubuntu GCC 把 `undefined reference` 绑定到 source-location 行的输出，同时不把普通 output 提升为 Diagnostic；
- template instantiation、included-from 和 note chain。

### 16.2 流式处理

- stdout/stderr 原始字节先写入有界日志，再进入解析器；
- 每个 stream 独立保持多行诊断状态；
- 无效 UTF-8 使用替换字符，但原始日志仍保留原始字节；
- 单行、单条诊断、related 数量和总诊断数量均有限制；
- Go regexp 使用线性时间实现，并配合输入上限；
- 解析失败不会改变 Task outcome；
- 无法识别的内容仍通过 `task.output` 和日志提供。

Service 能安全控制输出格式时关闭颜色并请求列信息；对于 Preset 控制的项目，不擅自追加可能改变项目语义的 compiler flags。

workspace 文件返回规范 URI。工具链/SDK 外部文件只有在规范路径位于已验证外部根目录时才返回可导航 URI，并设置 `external: true`；其他未知绝对路径只保留在脱敏 message 或原始本地日志中。

## 17. Artifacts、日志与脱敏

`cmakeBuild` 任务生成：

- `stdout.log`
- `stderr.log`
- `execution-plan.json`
- `diagnostics.jsonl`
- `build-summary.json`

`execution-plan.json` 只包含：

- plan/step identity；
- CMake、toolchain 和 profile identity；
- executable 的脱敏显示名；
- 脱敏后的参数摘要；
- 时间和结果。

它不包含环境值、token、完整 MSVC environment 或未批准的外部路径。

ArtifactStore 从仅验证 simulation summary 扩展为按 task kind 注册 artifact schema。旧 simulation artifact 的读取和摘要校验保持兼容。

## 18. 错误模型

新增稳定请求或规划错误：

- `WORKSPACE_CONFIG_INVALID`
- `WORKSPACE_CHANGED`
- `PATH_OUTSIDE_WORKSPACE`
- `CMAKE_UNAVAILABLE`
- `CMAKE_VERSION_UNSUPPORTED`
- `CMAKE_PRESET_INVALID`
- `CONFIGURE_REQUIRED`
- `TOOLCHAIN_NOT_FOUND`
- `TOOLCHAIN_UNSUPPORTED`
- `BUILD_TOOL_NOT_FOUND`
- `TARGET_NOT_FOUND`
- `EXECUTION_PLAN_INVALID`

错误码与本地化显示文本分离。

分类规则：

- 请求引用 stale generation、未知 profile 或未知 target 时，在创建 Task 前返回请求错误；
- Task 创建后发生的 compiler/CMake 非零退出为 `command_failed`；
- executable 消失、启动失败、存储失败或内部 plan invariant 失败为 `infrastructure_failed`；
- queued task 在重启恢复时发现 generation 变化，终止为 `interrupted` 并保存稳定错误码；
- Diagnostic parser 失败只影响结构化诊断完整性，不把成功构建改为失败。

## 19. 测试策略

### 19.1 单元测试

- Workspace Config Schema、大小、深度和数量限制。
- Windows/Linux path containment。
- symlink、junction 和 reparse point 越界。
- Native E2E 验证 `sourceDir` symlink 越界在配置层返回 `WORKSPACE_INVALID_CONFIG`，且不会进入 project inspection 或创建进程。
- CMake Resolver、bundle manifest 和摘要验证。
- Preset include 图、cycle、unsupported version 和外部 include。
- Workspace Generation 与 Configure Fingerprint。
- Build Profile 稳定 ID。
- MSVC、clang-cl、GCC 和 Clang discovery/probe。
- MSVC 固定环境捕获模板和敏感变量清理。
- 跨平台 E2E 的首次 `workspace/inspect` 使用独立的 cold-discovery 外层预算：Linux 为 30 秒，Windows 为 120 秒。Windows 预算覆盖全量 Go race 后的 hosted runner 高负载，以及 MSVC/clang-cl 多 adapter 依次组合固定 probe 的最坏路径；它只约束测试客户端等待时间，每个 production probe 自身的固定参数、输出上限和 5 秒命令预算保持不变。
- Windows production discovery smoke test 只在宿主机同时提供固定 Visual Studio metadata 与可验证 compiler/generator 时执行完整断言；generator 不可用，或底层 production runner 明确返回 `probe.ErrTimeout` 表示任一 5 秒固定 probe 预算在当前宿主负载下耗尽时，跳过该宿主机能力测试。其他 identity、格式、输出和环境错误仍失败。CI 随后的 Native E2E 仍通过 `UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=msvc,clang-cl` 强制验收项目支持矩阵，不能由 smoke test 跳过替代。
- 普通 CMake E2E 在首次 Start 因 optimistic-concurrency 返回 `WORKSPACE_CHANGED` 时，重新执行 `workspace/inspect`、重新选择 project/profile，并以新的 idempotency key 有界重试一次；拒绝发生在 Task 创建前，不会产生重复 Task。刷新后再次 stale 或基线建立后的 generation 漂移仍作为失败。
- Native E2E 在 Service recovery 场景开始前重新执行 `workspace/inspect`，用最新 generation/profile 完成基线构建并解析 slow target；重启后同时验证持久 Task 收敛为 `interrupted`，以及未变更 workspace 的 generation 保持稳定。
- Native E2E 的 compiler/linker/configure diagnostic fixture 各自使用独立 Workspace 与 Service，因此会重新执行完整 toolchain discovery。若当前 family 已在主矩阵中确认存在，但某个新隔离 Service 的首次 inspect 未生成该 family 的 profile，则只允许在同一未变更 Workspace 上有界重试一次；第二次仍缺失时必须带稳定 diagnostic code 失败，不能跳过 required family。
- Native E2E 等待 terminal event 时使用有界 liveness heartbeat：持续保留同一个 pending subscription read，查询 durable Task 状态；连接失活时由客户端执行 reconnect/replay，durable Task 已 terminal 但事件尚未到达时最多触发一次 terminal replay。验收仍要求收到连续 sequence 的 `task.finished`，不能用轮询快照替代事件契约。
- ExecutionPlan 的 executable、cwd、NUL、environment key/secret 校验，以及每 Step `ProcessSpec.Args <= 256`、`ProcessSpec.Env <= 256`、`CommandSummary.Args <= 256` 的精确边界。
- configure/build Step 状态、取消、超时和失败短路。
- SQLite migration、旧 simulation `scenario + timeoutMs` 回填、旧 hash语义 replay、checksum/unknown-version防护与 queued 恢复。
- CMake/compiler/linker golden diagnostic parser。

### 19.2 Protocol Contract

- v1.0/v1.1 fixtures 与生成文件完全不变。
- v1.2 workspace、targets、Task union、Step 和 Diagnostic fixtures。
- 包含 executable、raw args、env、Shell 或原生路径注入的请求全部无效。
- Go 与 TypeScript 生成模型一致。
- TypeScript Client 对响应和事件进行运行时校验。
- 未协商 v1.2 时客户端在本地拒绝 Phase 3 方法。
- 新事件继续满足 sequence、重放和去重规则。

### 19.3 Adapter Integration

使用固定 fixture executable 和 golden output 测试：

- capability probe；
- Preset listing；
- File API reply；
- configure invalidation；
- compiler/linker 输出；
- 路径包含空格与 Unicode；
- 大输出、无效 UTF-8 和截断。

Golden 文件将绝对路径规范化为占位符，不依赖 GitHub Runner 工作目录。

### 19.4 Native E2E Matrix

- Windows + MSVC；
- Windows + clang-cl；
- Linux + GCC；
- Linux + Clang。

样例项目覆盖：

- 有 CMake Presets；
- 无 Presets 的 generated fallback；
- 首次 configure/build；
- 第二次无变化时跳过 configure；
- 修改 CMake input 后重新 configure；
- warning、compiler error 和 linker error；
- 默认 target 和指定 target；
- 空格与 Unicode 路径；
- configure/build 期间取消和超时；
- Service 重启恢复；
- workspace/path escape 拒绝。

Windows/MSVC、Linux/GCC 和 Linux/Clang 是 Phase 3 必过矩阵。Windows clang-cl 同样执行真实构建，以验证 Phase 5 `llvm-cov` 所需的工具链边界；覆盖率命令本身不在本阶段运行。

### 19.5 CI

Windows 与 Ubuntu Hosted CI 均运行：

- `pnpm check:protocol-generated`
- `pnpm build`
- `pnpm test`
- Go normal tests
- Go race tests
- native E2E

CI 记录实际 compiler、generator 和 CMake version 作为构建制品，但 golden diagnostics 不固定 Hosted Runner 经常变化的绝对安装路径。

产品运行不依赖 GitHub。GitHub 只用于源码托管、PR、CI 和发布准备。

## 20. 验收场景

1. 以显式 trusted-workspace 条件启动绑定样例 workspace 的 Service。
2. TypeScript Client 协商 Protocol v1.2 并调用 `workspace/inspect`。
3. Service 返回 projects、Build Profiles、Toolchains 和 workspace generation。
4. 客户端启动一个不带 target 的 `cmakeBuild`。
5. Service 生成 configure/build ExecutionPlan，完成 CMake configure 和默认构建。
6. 事件流包含 Step、原始 output 和结构化 diagnostics。
7. 客户端读取 File API targets，并启动指定 target 的第二次构建。
8. Service 证明配置有效并跳过 configure。
9. 修改 CMake input 后再次构建，Service 自动重新 configure。
10. 编译和链接失败样例产生 `command_failed` 及基准 Diagnostic。
11. 取消运行中的构建后完整进程树退出，无遗留 compiler、linker 或 build tool。
12. 客户端断线重连后通过 sequence 恢复全部事件。
13. v1.0/v1.1 客户端仍按原协议运行。
14. 整个流程不接受 Shell 或客户端 executable。
15. migration-1 数据库升级后，相同 v1.1 simulation请求返回原 Task且不新增 event；scenario或 timeout变化返回 `IDEMPOTENCY_CONFLICT`。
16. CMake replay只在 Kind、semantic Request、WorkspaceGeneration与 Timeout全部相同时成立；仅 PlanFingerprint变化不冲突。

## 21. 完成门禁

- Windows/MSVC 样例项目通过 Service configure/build。
- Linux/GCC 与 Linux/Clang 样例项目通过 Service configure/build。
- Windows clang-cl Adapter 和真实构建通过。
- Preset 与 generated fallback 均通过。
- configure invalidation 与跳过逻辑通过。
- CMake、compiler 和 linker golden diagnostics 通过。
- Protocol v1.0/v1.1 回归测试通过。
- 多步骤取消、超时、重连和恢复测试通过。
- ExecutionPlan三类参数/环境 collection均验证 256合法、257拒绝。
- migration后 simulation与当前 simulation/CMake行在 hash变化时仍按 semantic identity replay。
- Windows 与 Ubuntu Hosted CI 全绿。
- 完整 `pnpm verify` 全绿。
- 工作树无生成差异和未提交文件。
- 独立代码评审确认不存在 Protocol 到 `ProcessSpec` 的越权入口。

## 22. 后续阶段接口

Phase 4 可以在 `ExecutionPlan` 中加入 `discoverTests` 和 `runTests` Step，并复用本阶段的 Workspace、Build Profile、Toolchain、Target、Task、事件与 Diagnostic。

Phase 5 可以读取 clang-cl/Clang/GCC Adapter 已记录的 coverage capability，加入 coverage instrumentation、`llvm-profdata`、`llvm-cov` 和 `gcovr` Step。

Phase 6 负责把 Service 生命周期绑定到 Code-OSS Workspace Trust，并把 Build Profile、Target 和 Diagnostic 投射到 UI。

Phase 8 负责把本阶段的 CMake bundle manifest 纳入签名安装包、更新、回滚和第三方许可清单。

## 23. 官方参考

- [CMake Presets](https://cmake.org/cmake/help/latest/manual/cmake-presets.7.html)
- [CMake File API](https://cmake.org/cmake/help/latest/manual/cmake-file-api.7.html)
- [CMake Downloads](https://cmake.org/download/)
- [CMake Licensing](https://cmake.org/licensing/)
- [Microsoft vswhere](https://github.com/microsoft/vswhere/wiki/Installing)
- [Microsoft C++ Build Tools command-line environment](https://learn.microsoft.com/en-us/cpp/build/building-on-the-command-line?view=msvc-170)
- [MSVC compiler diagnostic options](https://learn.microsoft.com/en-us/cpp/build/reference/diagnostics-compiler-diagnostic-options?view=msvc-170)
- [GCC diagnostic formatting options](https://gcc.gnu.org/onlinedocs/gcc/Diagnostic-Message-Formatting-Options.html)
- [Clang Compiler User’s Manual](https://clang.llvm.org/docs/UsersManual.html)
