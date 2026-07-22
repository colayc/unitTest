# 基于 Code-OSS 的 C/C++ 单元测试 IDE 设计规格

- 日期：2026-07-18
- 状态：设计已确认，待用户复核书面规格
- 首期平台：Windows、Linux 桌面
- 后续演进：浏览器客户端、云端测试执行

## 1. 背景与目标

产品面向外部用户独立发行，以 Code-OSS 为开源 IDE 底座，提供 C/C++ 单元测试的编辑、配置、构建、运行、Mock、覆盖率和报告能力。

首期目标：

1. 发布具有自有品牌的 Windows、Linux 桌面 IDE。
2. 首个正式版本同时支持 Windows/MSVC、Linux/GCC、Linux/Clang 三个平台与工具链组合；Windows 覆盖率使用 clang-cl/llvm-cov 专用构建配置。
3. 以 CppUTest/CppUMock 为 C/C++ 主测试方案，以 Unity/CMock 为纯 C 补充方案。
4. 使用 CMake/CTest 调度构建和测试。
5. 提供测试发现、测试树、运行控制、源码定位、覆盖率和报告功能。
6. 通过独立测试执行服务隔离 IDE 界面与本地工具链。
7. 保证主要 UI 和协议未来能够复用于浏览器与云端版本。

## 2. 非目标

首期明确不包含：

- 云端账号、租户和权限系统。
- 浏览器 IDE 正式发行。
- 分布式编译和测试。
- AI 自动生成单元测试。
- 功能安全工具认证、需求追踪和 MC/DC 合规平台。
- 自建开放式公共扩展市场。
- 对 Code-OSS Workbench 进行大范围核心修改。

## 3. 技术路线决策

### 3.1 已选路线

采用“Code-OSS 薄 Fork + 内置扩展 + 独立测试执行服务”。

- Code-OSS 提供编辑器、工作区、文件树、终端、设置、扩展宿主和桌面外壳。
- 产品功能主要通过内置扩展实现。
- 编译、测试和覆盖率命令由独立服务执行。
- 桌面版连接本地服务；未来浏览器版通过相同协议连接远程服务。

### 3.2 未选路线

**官方 VS Code + 扩展**：开发成本最低，但用户依赖微软发行版，不能形成完全独立的 IDE 产品。

**重度 Fork Code-OSS**：可以获得最大 UI 控制力，但持续合并上游更新的成本和风险过高。

**首期直接云端化**：长期方向正确，但会过早引入账号、调度、隔离、存储和运维复杂度。

## 4. 开源与发行边界

Code-OSS 源码采用 MIT 许可证，可以修改和重新发行。产品不得直接沿用微软的 VS Code 名称、Logo、遥测密钥、更新服务或其他品牌资产。

产品发行要求：

- 使用自有产品名称、图标、安装程序和 URI Scheme。
- 使用自有更新服务、崩溃报告和遥测配置。
- 不默认连接 Microsoft Visual Studio Marketplace。
- 扩展来源采用白名单 Open VSX 或自建扩展仓库。
- C/C++ 语言能力优先采用可重新发行的 clangd 等开源组件。
- 每项预装第三方扩展必须单独核查许可证和再分发权。
- 建立 Code-OSS 上游版本、产品补丁和第三方许可证清单。

## 5. 总体架构

```text
Code-OSS 产品外壳
├── 工作区、编辑器、终端、设置
├── 测试树
├── 用例编辑器
├── Mock/Stub 配置
├── 编译器配置
├── 覆盖率与报告
└── 测试协议客户端
             │
             ▼
统一测试执行协议
             │
             ▼
测试执行服务
├── 项目与工具链发现
├── 构建与运行编排
├── CppUTest/CppUMock 适配
├── Unity/CMock 适配
├── CMake/CTest 适配
├── MSVC/clang-cl/GCC/Clang 适配
├── gcovr/llvm-cov 适配
└── 结果、历史和制品管理
```

## 6. 模块职责

### 6.1 Code-OSS 产品外壳

负责通用 IDE 能力与品牌发行，不承载测试领域逻辑。核心修改限制在产品配置、品牌、默认设置、更新入口和内置扩展注册；其他核心补丁必须单独记录原因、影响和上游合并策略。

### 6.2 单元测试内置扩展

负责：

- 通过 Testing API 发布测试树与测试状态。
- 显示工具链、框架和项目配置。
- 提供测试运行、停止、重新运行和过滤命令。
- 显示实时日志、失败信息和源码位置。
- 提供 Mock/Stub 配置入口。
- 展示覆盖率和历史报告。
- 将所有执行请求转换成测试协议消息。

扩展不得直接拼接 Shell 命令，也不得直接管理编译器进程。

### 6.3 测试协议客户端

负责连接建立、握手、协议版本协商、任务提交、事件流、取消、超时、断线重连和制品下载。桌面首期优先使用 stdio、Windows Named Pipe 或 Linux Unix Socket；未来远程实现使用 TLS 保护的 HTTPS/WebSocket 或等价传输。

### 6.4 测试执行服务

负责工作区扫描、工具链发现、构建计划、进程管理、结果解析、覆盖率采集、历史记录和制品生成。服务不依赖 Electron、DOM 或 Code-OSS 内部对象。

服务实现语言不属于本产品设计的外部约束；无论采用 Node、Go、Rust 或其他技术，都必须满足本规格的协议、平台、进程控制、安装体积和可测试性要求。

### 6.5 适配器层

每个测试框架、构建工具、编译器和覆盖率工具使用独立适配器。适配器通过统一接口输出：

- 能力与版本信息。
- 配置校验结果。
- 构建和运行计划。
- 结构化诊断信息。
- 测试发现与测试结果。
- 覆盖率和制品描述。

新增 IAR、Keil 等工具链时不得修改 UI 领域模型。

### 6.6 数据与制品层

- 可共享项目配置使用 JSON 或 YAML 并纳入版本控制。
- 本地执行历史使用 SQLite。
- 日志、JUnit XML、HTML 报告、覆盖率文件和二进制结果作为制品保存。
- 历史数据库与制品目录分离。
- 数据库升级必须版本化、可迁移、可回滚。

## 7. 协议与数据模型

协议采用版本化 JSON 消息模型，传输层可替换。核心对象包括：

- `Workspace`：工作区 URI、项目列表和信任状态。
- `Toolchain`：编译器、架构、版本和环境信息。
- `TestFramework`：CppUTest、Unity 及其能力。
- `TestItem`：稳定测试 ID、层级、标签和源码位置。
- `RunRequest`：范围、构建配置、环境、超时和覆盖率选项。
- `Task`：任务 ID、状态、阶段、进度和时间戳。
- `Diagnostic`：严重级别、消息、URI 和源码位置。
- `TestResult`：通过、失败、跳过、中断和基础设施错误。
- `Artifact`：制品 ID、类型、摘要、大小和获取方式。

约束：

- 使用工作区 URI，不传递 UI 对象。
- 平台原生路径只在测试服务内部解析。
- 每个任务、测试和制品具有稳定 ID。
- 错误码与用户显示文本分离。
- 新协议至少兼容前一个已发布客户端协议版本。
- 未来字段预留用户、租户、执行节点和远程工作区标识，但首期不实现云端权限逻辑。

## 8. 主要数据流

1. 用户打开工作区。
2. 内置扩展把工作区 URI 与信任状态传给测试服务。
3. 测试服务发现项目、工具链、测试框架和现有构建配置。
4. 用户选择测试范围并提交运行请求。
5. 服务执行预检，生成构建和运行计划。
6. 服务启动 CMake、编译器和测试进程，持续发送结构化事件。
7. 扩展实时更新测试树、日志和诊断。
8. 服务解析 CppUTest/Unity 结果并采集覆盖率。
9. 服务原子写入历史记录并生成制品。
10. UI 更新最终结果、源码标记、覆盖率和报告入口。

## 9. 首期功能范围

### 9.1 平台与项目

- Windows、Linux 桌面安装包。
- 自有品牌、配置、更新地址和扩展源。
- CMake 项目识别和工作区配置。
- 首个正式版本同时支持 Windows/MSVC、Linux/GCC、Linux/Clang。
- Windows 自动发现 MSVC 与 LLVM/clang-cl；Linux 自动发现 GCC 与 Clang，并允许手动配置。
- Windows 使用相互隔离的 MSVC 正式构建配置和 clang-cl 覆盖率构建配置，两者不得复用构建目录。

### 9.2 测试能力

- CppUTest/CppUMock 主方案。
- Unity/CMock 纯 C 补充方案。
- 测试发现、分组、过滤、单个运行和全部运行。
- 运行、停止、重复运行和失败重跑。
- 实时日志、失败详情和源码跳转。
- 基础 Mock/Stub 配置入口。

### 9.3 覆盖率与报告

- Windows 使用 clang-cl 插桩并通过 llvm-profdata/llvm-cov 生成覆盖率；该结果代表 clang-cl 构建，不声明为 `cl.exe` 原生覆盖率。
- Linux/GCC 使用 gcovr 生成覆盖率。
- Linux/Clang 使用 llvm-cov 生成覆盖率。
- clang-cl、llvm-profdata 与 llvm-cov 必须来自同一 LLVM 工具链版本；原始 `.profraw` 只作为短期中间制品，不作为长期兼容格式保存。
- 覆盖率树、源码着色和汇总。
- JUnit XML 与 HTML 报告导出。
- 本地历史结果和制品查看。

## 10. 错误处理与恢复

错误必须分成四类：

1. **配置错误**：缺少编译器、CMake、框架或配置；任务不启动并提供修复入口。
2. **构建失败**：解析 MSVC/GCC/Clang 诊断并支持源码跳转。
3. **测试失败**：显示期望值、实际值、用例、源码位置和相关日志。
4. **基础设施失败**：服务崩溃、通信断开、超时、磁盘不足或进程启动失败；不得记为测试失败。

恢复要求：

- 取消和超时必须终止完整子进程树。
- 服务崩溃后客户端自动重启服务。
- 进行中的任务标记为中断，不能误报成功或失败。
- 配置和历史数据采用原子写入。
- 临时文件和孤儿进程在重启时清理。
- 客户端断线重连后可以查询仍在执行的任务状态。

## 11. 安全设计

- 未信任工作区禁止自动执行构建脚本和测试程序。
- 进程启动使用参数数组，不拼接 Shell 字符串。
- 工具链路径进行存在性、类型、权限和允许范围校验。
- 本地服务仅接受当前用户连接，并使用临时令牌握手。
- Named Pipe 与 Unix Socket 限制为当前用户访问。
- 日志遮蔽令牌、密码和敏感环境变量。
- 安装包、更新包和测试服务二进制必须签名或校验摘要。
- Open VSX 默认启用白名单；第三方扩展不能读取测试服务凭据。
- 测试服务对制品路径进行规范化，防止越界写入。
- 云端版本必须在另行设计中补充身份、租户隔离、执行沙箱和网络策略。

## 12. 浏览器与云端演进

首期桌面 UI 按 Web Extension 兼容原则开发：

- UI 只使用 URI，不保存 Windows 盘符或 Linux 绝对路径。
- Webview 通过消息通道与扩展通信，不直接依赖 `localhost`。
- UI 扩展与 Workspace 扩展职责分离。
- 协议对象不包含 Electron、Node 或 DOM 专有对象。
- 本地服务接口与未来远程服务接口保持一致。

未来形态：

```text
桌面：Code-OSS Desktop → 本地测试服务
浏览器：Code-OSS Web → HTTPS/WebSocket → 云端测试服务 → 隔离执行节点
```

云端账号、权限、调度和沙箱属于后续独立设计，不进入首期实现。

## 13. 测试策略

### 13.1 单元测试

覆盖协议模型、状态机、结果解析器、路径转换、工具链发现和所有适配器。使用固定输入验证不同工具输出，避免多数单元测试依赖真实编译器。

### 13.2 契约测试

- JSON Schema 和错误码版本化。
- 客户端、服务分别运行协议兼容性测试。
- 状态、诊断和制品使用 Golden File 验证。
- Windows 路径、Linux 路径和远程 URI 分别测试。
- 取消、超时、断线、重连和服务重启必须覆盖。

### 13.3 集成测试

| 平台 | 工具链 | 验证内容 |
|---|---|---|
| Windows | MSVC | C/C++ 编译、CppUTest、Unity、构建诊断、进程取消 |
| Windows | clang-cl/LLVM | 覆盖率构建、CppUTest、Unity、llvm-profdata、llvm-cov、源码路径映射 |
| Linux | GCC | C/C++ 编译、CppUTest、Unity、gcovr |
| Linux | Clang | C/C++ 编译、CppUTest、Unity、llvm-cov、ASan、UBSan |

每个适配器包含成功、构建失败、测试失败、崩溃和超时示例工程。

### 13.4 端到端测试

自动验证打开 CMake 工程、工具链发现、测试发现、运行与停止、失败跳转、覆盖率查看、报告导出、历史恢复和未信任工作区保护。

## 14. 发布门禁

- 单元、契约、集成和关键端到端测试全部通过。
- Windows/MSVC 正式构建与 Windows/clang-cl 覆盖率构建必须分别通过，且使用独立构建目录。
- Windows、Linux 安装包完成签名或摘要校验。
- 不存在未处置的高危依赖漏洞。
- 依赖和预装扩展许可证完成审查。
- Code-OSS 上游合并后执行完整回归。
- 建立 10,000 个测试用例的发现、过滤和树展示性能基线。
- 性能退化必须有明确批准与发布说明。
- 保留可回滚安装包、协议版本和数据库迁移方案。
- IDE 外壳、内置扩展和测试服务使用独立版本号；发布清单锁定兼容组合。

## 15. 成功标准

首期视为成功需要同时满足：

1. Windows/MSVC、Linux/GCC、Linux/Clang 的核心测试流程在首个正式版本中稳定运行。
2. 同一工作区配置和测试源代码可以跨 Windows、Linux 使用。
3. CppUTest 与 Unity 测试能够发现、运行、停止并准确显示结果。
4. 构建错误、测试失败和基础设施错误能够被明确区分。
5. Windows/clang-cl、Linux/GCC 和 Linux/Clang 的覆盖率可以在源码和汇总视图中查看并导出；产品界面明确显示实际覆盖率工具链。
6. 未信任工作区不会自动执行代码。
7. Code-OSS 核心改动保持最小并可持续合并上游。
8. 测试服务可以脱离 IDE 独立运行和测试。
9. UI、协议和数据模型不阻碍后续浏览器与云端扩展。

## 16. 主要风险与缓解

| 风险 | 缓解措施 |
|---|---|
| Code-OSS 上游更新造成冲突 | 薄 Fork、补丁清单、定期小步升级和自动回归 |
| 第三方扩展不可再分发 | 白名单、逐项许可证审查、优先开源替代 |
| 多编译器输出差异 | 独立适配器、Golden File 和真实工具链矩阵 |
| 测试服务被工作区命令滥用 | Workspace Trust、参数化执行、路径校验和用户权限隔离 |
| 本地与云端协议分叉 | 版本化协议、传输层分离和契约测试 |
| 大量测试导致 UI 卡顿 | 分页/增量事件、虚拟化树、性能基线和压力测试 |
| CppUTest 不能满足现代 C++ 场景 | 保留框架适配层，后续可增加 GoogleTest 适配器 |
| Windows 正式构建与覆盖率构建使用不同编译器导致行为差异 | 使用相同源码、依赖版本和测试范围；分别执行 MSVC 功能测试与 clang-cl 覆盖率测试，并在界面和报告中标明工具链 |
| LLVM 原始覆盖率数据与工具版本不兼容 | 锁定 clang-cl、llvm-profdata、llvm-cov 版本组合，运行后立即转换为版本化 JSON/HTML 报告，原始 profile 仅短期保留 |
| 云端需求提前侵入首期 | 仅预留协议字段，不实现首期非目标能力 |

## 17. 官方参考资料

- [Code-OSS GitHub 仓库](https://github.com/microsoft/vscode)
- [Code-OSS MIT License](https://github.com/microsoft/vscode/blob/main/LICENSE.txt)
- [Code-OSS 与 Visual Studio Code 差异](https://github.com/microsoft/vscode/wiki/Differences-between-the-repository-and-Visual-Studio-Code)
- [VS Code Testing API](https://code.visualstudio.com/api/extension-guides/testing)
- [VS Code Extension Host](https://code.visualstudio.com/api/advanced-topics/extension-host)
- [VS Code Remote Extension Architecture](https://code.visualstudio.com/api/advanced-topics/remote-extensions)
- [Open VSX Registry FAQ](https://www.eclipse.org/legal/open-vsx-registry-faq/)
- [CppUTest 用户手册](https://cpputest.github.io/)
- [Unity Test](https://github.com/ThrowTheSwitch/unity)
- [Clang Compiler User's Manual：clang-cl](https://clang.llvm.org/docs/UsersManual.html#clang-cl)
- [LLVM Source-based Code Coverage](https://clang.llvm.org/docs/SourceBasedCodeCoverage.html)
- [gcovr User Guide](https://gcovr.com/en/stable/guide.html)
