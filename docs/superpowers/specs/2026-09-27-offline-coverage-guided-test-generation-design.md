# Phase 10：离线覆盖率驱动的 C/C++ 测试用例自动生成设计

- 日期：2026-09-27
- 状态：设计已确认
- 目标版本：首个正式发布版本
- 目标平台：Windows x64、Linux x64
- 目标框架：C++ 使用 CppUTest，C 使用 Unity

## 1. 背景与决策

项目当前已经具备 C/C++ 工作区识别、CMake 构建、测试发现与执行、覆盖率采集、Code-OSS 测试界面和发布资格门禁。首个正式版本还必须加入一套由项目自行开发的测试用例自动生成能力，其主要价值不是生成模板，而是提高函数、行和分支覆盖率。

该能力必须在正式 Windows 签名、最终第三方 license/legal 审批和最终发布资格验证之前完成。签名和人工审批绑定具体源码与制品；如果先完成这些工作再加入生成器，就必须重新签名、重新审批并重新执行发布验收。

本设计采用以下已确认决策：

1. 完全离线，不发送源码或中间数据到外部服务。
2. 不使用云端或本地大模型。
3. 不接入第三方测试生成产品。
4. 使用固定版本的 Clang 前端理解 C/C++ 语法、类型和控制流，输入生成、断言分类、候选筛选和覆盖率反馈算法由本项目自行开发。
5. 生成器不生成、不使用 Mock 或 Stub。
6. 生成器不自动修改生产源码。
7. 只有包含有效断言、能够编译、能够通过并带来覆盖率增量的候选才可成为正式测试。
8. 所有正式文件修改都必须在用户预览并确认后发生。

## 2. 目标

首版必须提供以下用户能力：

- 从函数、类、当前文件、CMake 目标、整个工作区或覆盖率缺口启动生成。
- 自动运行现有测试并建立函数、行、分支覆盖率基线。
- 分析未覆盖代码路径，生成能够到达目标路径的具体输入。
- 为 C++ 生成 CppUTest 测试，为 C 生成 Unity 测试。
- 为每个正式候选生成有来源说明的有效断言。
- 在隔离临时工作区自动编译、执行和重新采集覆盖率。
- 只保留通过验证并确实增加覆盖率的候选。
- 按覆盖贡献去重和最小化测试集合。
- 展示生成前后覆盖率、候选代码、断言来源、不能生成的原因和拟修改文件。
- 用户确认后原子写入测试文件和必要的 CMake 配置。
- 写入后支持一键执行全部测试并刷新覆盖率。

## 3. 明确非目标

首版不承诺：

- 为任意 C/C++ 程序自动生成测试。
- 达到 100% 覆盖率。
- 生成或使用 Mock/Stub。
- 自动修改生产源码以建立测试接缝。
- 测试网络、数据库、硬件、系统配置或真实用户文件。
- 支持不可控时间、随机源或外部服务。
- 提供 MC/DC、无限路径探索或完整通用符号执行。
- 自动证明业务逻辑正确。
- 把没有有效断言的执行探测计为正式测试。
- 把 Windows `cl.exe` 原生覆盖率作为首版能力；Windows MSVC 项目通过专用 clang-cl/LLVM coverage profile 采集覆盖率。

## 4. Phase 10 分批边界

### 4.1 Phase 10A：覆盖率基础闭合

测试生成器必须建立在可验证的覆盖率输入上。Phase 10A 在生成器生产实现之前完成：

- 实现 Linux Clang/LLVM 原生覆盖率执行。
- 保留并重新闭合 Windows clang-cl/LLVM 原生覆盖率证据。
- 保留 Linux GCC/gcovr 原生覆盖率能力。
- 让 CppUTest 和 Unity 的函数、行、分支数据进入同一个版本化 canonical coverage model。
- 闭合覆盖率故障映射、报告生成和对应 Phase 9 evidence receipt。
- 明确普通 Windows MSVC 构建与专用 clang-cl coverage profile 的对应关系。
- 将新增 Clang analyzer/runtime 纳入固定 manifest、摘要校验、第三方 license inventory 和发布 staging；不得依赖用户 PATH 中的可变工具。

Phase 10A 完成后，生成器只消费 canonical coverage model，不直接理解 llvm-cov 或 gcovr 的私有输出。

### 4.2 Phase 10B：源码分析与生成引擎

Phase 10B 引入固定 Clang 分析器、稳定的项目自有中间模型、有界条件求解、输入生成、断言分类和两种测试框架的渲染器。

### 4.3 Phase 10C：候选验证闭环

Phase 10C 在隔离目录中完成候选的配置、编译、执行、覆盖率比较、去重、最小化和故障处理。候选在此阶段不能修改正式工作区。

### 4.4 Phase 10D：Code-OSS 一键工作流与最终验收

Phase 10D 提供范围选择、预算设置、进度、取消、差异预览、选择性接受、一键重跑和覆盖率结果界面，并执行完整 Windows/Linux、CppUTest/Unity 验收矩阵。

## 5. 总体架构

```text
Code-OSS 生成界面
        |
        v
Protocol v1.5 测试生成合同
        |
        v
Go Service 测试生成协调器
        |-- 固定 Clang 源码分析器
        |-- 自研目标选择与有界条件求解器
        |-- 自研输入与断言生成器
        |-- CppUTest / Unity 渲染器
        `-- 隔离编译、执行与覆盖率验证器
        |
        v
候选测试、覆盖率增量与有界诊断
        |
        v
用户预览并确认
        |
        v
原子写入测试文件和 CMake 配置
```

组件必须保持单一职责：

- Code-OSS 扩展只负责用户交互、进度展示、差异预览和确认，不执行编译器或测试进程。
- Protocol v1.5 只传输闭集的范围、预算、目标和结果引用，不传输任意命令、可执行文件、环境变量或工作目录。
- Go Service 协调任务生命周期、恢复、取消、资源预算和 artifact ownership。
- Clang 分析器只把受信任工作区及固定编译描述转换为稳定中间模型，不生成测试。
- 生成引擎只消费稳定中间模型和 canonical coverage gaps，不直接读取 UI 状态或原始工具输出。
- 框架渲染器把 framework-neutral candidate 转换为 CppUTest 或 Unity 源码。
- 验证器只在产品控制的隔离根目录中配置、编译、执行和采集覆盖率。
- 文件发布器只在用户确认后执行带摘要检查的原子写入。

## 6. Protocol v1.5 与任务模型

Protocol v1.5 在不放宽 v1.0-v1.4 合同的前提下新增测试生成能力。旧客户端与旧 Service 仍按协商版本工作；低于 v1.5 的会话必须在本地拒绝生成 API，不能发送未知请求。

生成请求只允许包含：

- workspace URI 和既有 workspace generation identity；
- 受控 scope kind：symbol、file、target、workspace 或 coverage-gap；
- 已发现的 symbol ID、file URI、target ID 或 coverage location ID；
- framework preference：auto、cpputest 或 unity；
- 函数、行、分支覆盖率目标；
- wall-clock、candidate count、memory 和 concurrency 预算；
- 是否允许产生特征测试候选；
- 生成请求的幂等 key。

请求不得包含：

- Shell 字符串；
- executable、argv 或 environment；
- 原生工作目录；
- 任意编译选项或链接选项；
- 任意输出路径；
- 任意网络位置；
- Mock/Stub 配置。

Service 从已验证的 workspace、build profile、toolchain 和 product manifest 中解析所有执行细节。

生成任务状态为闭集：

```text
queued -> baseline -> analyzing -> solving -> rendering
       -> validating -> minimizing -> awaiting-confirmation
       -> accepted | rejected | cancelled | failed
```

`awaiting-confirmation` 之前不允许写入正式工作区。确认请求绑定 task ID、candidate set digest、workspace generation、source snapshot digest 和每个拟修改文件的 expected-before digest。任一绑定变化都使确认失败并要求重新生成。

## 7. 固定 Clang 分析器与稳定中间模型

### 7.1 编译输入来源

分析器只使用受信任 CMake profile 产生的 compile database 和项目固定的 Clang frontend。不得采用 PATH 中任意 clang，不得接受客户端给出的编译参数。

分析开始前必须验证：

- compile database 位于产品拥有的 build artifact root；
- 每个 source URI 仍位于 workspace root；
- compiler family、target triple、language mode 和宏摘要与 workspace generation 一致；
- include path 不逃逸受信任 SDK/toolchain roots；
- 分析器、manifest 和配置摘要匹配产品锁定身份。

### 7.2 中间模型

分析器输出版本化、闭集、大小受限且顺序稳定的中间模型，至少包含：

- translation unit、symbol 和 source span 的稳定 ID；
- 公开可调用函数、方法和构造要求；
- 参数、返回值、枚举、结构体、指针、数组和字符串约束；
- 基本块、条件、switch、提前返回、错误返回和有界循环边；
- 读写的公开状态和允许观察的后置状态；
- 调用依赖及其安全分类；
- 与 canonical coverage function/line/branch identity 的映射。

中间模型不包含完整源文件内容、主机绝对路径、环境变量、token 或未经界定的 Clang 内部对象。

## 8. 安全可生成子集

首版允许：

- 普通 C 函数；
- 可公开调用且可安全构造的 C++ 函数和方法；
- bool、整数、有限浮点边界、枚举、指针空/非空状态；
- 有明确上限的数组和字符串；
- 可值初始化或使用已验证 fixture 构造的结构体和对象；
- 内存内可观察状态变化；
- 产品控制临时目录内的文件读写。

首版默认拒绝：

- 网络、数据库、硬件和系统设置访问；
- 真实用户文件或 workspace 外可写路径；
- 不可控时钟、随机源、进程间外部服务或后台守护进程；
- 需要 Mock/Stub 才能隔离的依赖；
- 无法安全构造或销毁的对象；
- 无界递归、无界循环或无法证明预算上限的路径；
- 需要修改生产代码才能测试的内部实现；
- 解析不完整、宏展开不一致或 source/compile identity 漂移的目标。

安全分类为 fail-closed。分析器不确定时必须返回稳定的 unsupported reason，不能尝试执行后再把副作用当作诊断。

## 9. 覆盖率驱动生成算法

### 9.1 基线

每次生成必须先使用相同 workspace generation、build profile、framework 和 coverage profile 运行现有测试。基线失败、coverage report 不完整或 provenance 不匹配时，生成任务失败，不能继续产生候选。

### 9.2 目标排序

生成引擎按以下顺序处理缺口：

1. 完全未调用的安全函数；
2. 未覆盖的错误返回和提前返回分支；
3. 未覆盖的条件分支；
4. 未覆盖的普通代码行；
5. 已覆盖但可由更小测试集合保留的重复路径。

排序同时考虑预计求解成本、构造安全性和候选可能覆盖的缺口数量。排序必须稳定，相同输入不能因 map iteration、线程完成顺序或主机路径不同而改变结果。

### 9.3 有界条件求解

首版自研求解器支持：

- bool 条件和否定；
- 整数等于、不等于、大小比较和闭区间；
- 常量加减和受溢出规则约束的边界值；
- enum equality 和 switch case；
- null/non-null；
- 有界字符串/数组长度；
- 有界 conjunction/disjunction；
- 循环的零次、一次和用户预算内的边界次数。

复杂指针别名、任意浮点约束、非线性算术、跨线程状态和无界路径返回明确 unsupported，不调用隐藏的通用求解服务。

### 9.4 输入集合

输入生成至少覆盖：

- 零值、单位值和符号相反值；
- 类型最小值、最大值及相邻值；
- 分支常量及其前后边界；
- 每个 enum member 和非法 enum 候选仅在语言/调用合同允许时；
- null 与可安全分配的 non-null storage；
- 空字符串、单元素字符串和分支长度边界；
- 空数组、单元素数组和受控长度边界；
- 错误码、成功码及源码明确处理的返回范围。

所有输入必须能序列化为 framework-neutral candidate，并在渲染前重新验证类型和大小。

## 10. 断言模型

没有有效断言的候选不得进入正式候选集合。

### 10.1 验证型测试

验证型断言可以来自：

- 明确常量返回路径；
- 类型与枚举合同；
- 源码可证明的错误结果；
- 公开、可观察的后置状态变化；
- 已存在且被用户接受的测试合同；
- 明确的 workspace 测试配置约束。

报告必须记录断言类别和来源位置，但不得泄露主机路径。

### 10.2 特征测试候选

当结果只能由当前稳定执行观察得到时，可以在用户允许后形成特征测试候选。该候选：

- 必须包含明确断言；
- 必须标记为 characterization，而不是 verified behavior；
- 必须展示观察值及其来源；
- 必须提示可能固化现有缺陷；
- 只有用户逐项或批量确认后才能写入。

### 10.3 拒绝条件

以下候选必须拒绝：

- 没有断言；
- 只断言进程未崩溃；
- 恒真或不读取被测结果的断言；
- 断言由随机、时间、主机路径或执行顺序决定；
- 断言仅复述输入而不观察被测行为；
- 依赖 workspace 外部可变状态。

## 11. 框架渲染

框架渲染器消费相同的 candidate model：

- C++ 只生成 CppUTest 测试，不生成 CppUMock。
- C 只生成 Unity 测试，不生成 CMock。
- framework=auto 根据受信任 target language 和现有测试目标选择框架。
- 测试名称由稳定 symbol ID、scenario kind 和短摘要组成，并满足框架命名限制。
- include、fixture、setup/teardown 和 target registration 只能来自固定模板与已验证 workspace metadata。
- 生成文件使用仓库规定的编码、换行和格式化规则。
- 同一候选重复渲染必须产生字节相同的结果。

## 12. 隔离验证闭环

### 12.1 隔离目录

验证器在产品拥有的临时根目录创建源快照、候选测试、生成 CMake overlay、build tree 和 coverage artifacts。正式 workspace 在验证期间只读。

验证器必须拒绝 symlink/junction、路径逃逸、hard-link identity ambiguity、case alias 和 workspace generation drift。子进程只能使用 product manifest 允许的 CMake、compiler、test runner 和 coverage tools。

### 12.2 候选门禁

每个候选依次通过：

1. candidate model schema；
2. 测试源码静态检查；
3. configure；
4. compile/link；
5. 单候选测试执行；
6. 全部既有测试回归；
7. coverage collection；
8. coverage provenance validation；
9. assertion quality validation；
10. coverage delta validation。

只有全部通过且至少新增一个目标函数、行或分支覆盖的候选才进入保留集合。覆盖率下降、既有测试失败或结果不稳定时拒绝候选。

### 12.3 去重与最小化

生成器按覆盖集合、输入语义和断言语义去重，再使用稳定的 greedy set-cover 选择尽量少的候选覆盖尽量多的新缺口。最小化不能删除独立验证错误路径或用户锁定的候选。

## 13. 停止条件和资源预算

用户设置函数、行、分支覆盖率目标以及 wall-clock、candidate count、memory 和 concurrency 上限。任务在任一条件满足时停止：

- 三类覆盖率达到目标；
- 达到 wall-clock 或 candidate count 上限；
- 达到内存或并发安全上限；
- 连续有界轮次没有覆盖率增量；
- 剩余缺口全部为 unsafe、unsupported 或 unsolved；
- 用户取消；
- workspace/source/toolchain identity 漂移。

停止必须保留已经完整验证的候选和明确的未完成原因。预算耗尽不是成功达到目标，也不能被报告为生成完成。

## 14. Code-OSS 用户体验

### 14.1 启动入口

提供统一命令“生成测试并提高覆盖率”，并出现在：

- 函数或类的编辑器上下文；
- 当前文件；
- CMake target；
- workspace；
- coverage viewer 的未覆盖 function/line/branch。

默认从用户当前选择的小范围开始。workspace 模式按 target 分批执行，并始终遵守全局预算。

### 14.2 进度

界面展示闭集阶段：

```text
基线覆盖率 -> 源码分析 -> 路径求解 -> 候选渲染
-> 编译 -> 执行 -> 覆盖率比较 -> 去重 -> 等待确认
```

用户可以随时取消。取消必须有界终止生成器和整个子进程树，完成临时目录清理，并保持正式 workspace 字节不变。

### 14.3 结果与确认

结果面板包含：

- 函数、行、分支覆盖率前后对比；
- 每个候选的类型、断言来源和独立覆盖贡献；
- 验证型与特征型候选的明确区分；
- 未生成缺口及安全、求解或预算原因；
- 新增测试文件和 CMake patch；
- 接受、拒绝、逐项选择和重新生成操作。

接受操作绑定候选摘要和 workspace snapshot。用户确认后发生任何源码或目标文件变化都必须使接受失败，而不是重新套用旧 patch。

## 15. 文件所有权与原子发布

- 首次生成默认创建独立测试文件，不向生产源文件插入代码。
- 每个 generated case 具有稳定 ID，避免重复生成同一场景。
- 生成器记录自己创建的文件与 case identity，但不把整个用户文件声明为永久可覆盖。
- 用户编辑过的文件不能静默覆盖；后续生成以 patch 展示。
- 测试代码和 CMake patch 分开展示，但在一次确认交易中原子提交。
- 发布前验证每个目标的 expected-before digest。
- 任一写入、flush、rename 或最终回读失败时恢复全部原文件并删除新文件。
- 发布完成后重新读取并验证文件摘要，再允许触发“一键执行全部测试”。

## 16. 错误处理

- 基线构建、测试或 coverage 失败：整个生成任务失败，不产生正式候选。
- 单个目标不可分析：记录稳定 unsupported reason，继续其他目标。
- 单个候选编译或执行失败：拒绝该候选，继续预算内其他候选。
- analyzer、compiler 或 test process 崩溃：终止对应进程树并输出脱敏、有界诊断。
- 超时：区分 analyzer、solver、configure、compile、test 和 coverage stage，不输出原始路径或命令行。
- coverage 无增量或下降：拒绝候选。
- 所有候选均被拒绝：结果为 no-acceptable-candidates，不显示 success。
- 工具链或框架缺失：返回精确 unavailable，不自动降级为无覆盖率生成。
- workspace drift：使所有未确认候选失效。
- 取消或 Service 重启：利用现有 durable task/event 语义恢复到可解释终态，不自动发布文件。

## 17. 安全与隐私

- 复用 Workspace Trust、每用户 IPC/token、artifact ownership、路径 containment 和日志脱敏能力。
- 生成、分析和验证全过程禁止网络访问。
- Windows 需要执行编译/测试/coverage 子进程时复用现有 WFP fail-closed boundary。
- Linux 在准备完固定输入后进入现有无网络 namespace boundary。
- Clang frontend、analyzer helper 及其运行时文件必须使用固定版本、逐文件摘要、闭集 inventory 和许可材料，并进入现有供应链与 license audit 门禁。
- 报告只包含 workspace-relative URI、稳定 ID、coverage summary、reason enum、duration 和受限诊断。
- 不持久化完整源码、AST dump、环境变量、token、主机绝对路径或任意进程输出。
- 临时源码快照和中间模型在任务终态后按 artifact policy 清理。

## 18. 测试策略

### 18.1 单元测试

- AST/CFG 中间模型规范化和稳定排序；
- source/compile identity 验证；
- 安全子集分类；
- 分支条件提取和有界求解；
- 边界输入生成；
- 断言分类与无效断言拒绝；
- CppUTest/Unity 渲染；
- 稳定 case ID、去重和 set-cover 最小化；
- budget、停止条件和取消状态机；
- patch digest 与原子发布回滚。

### 18.2 协议与契约测试

- Protocol v1.5 schema 和 generated model drift；
- v1.0-v1.4 严格兼容；
- 所有 request/response/event 的闭集、大小和语义约束；
- 对 executable、argv、environment、working directory、任意 output path 和 Mock 配置的注入拒绝；
- task 恢复、取消、重连和 artifact read 的完整语义。

### 18.3 真实集成矩阵

至少覆盖：

| 平台 | 普通构建 | 覆盖率构建 | C++ | C |
|---|---|---|---|---|
| Windows x64 | MSVC | clang-cl/LLVM | CppUTest | Unity |
| Windows x64 | clang-cl | clang-cl/LLVM | CppUTest | Unity |
| Linux x64 | GCC | GCC/gcovr | CppUTest | Unity |
| Linux x64 | Clang | Clang/LLVM | CppUTest | Unity |

每个组合通过真实 CMake、编译器、测试框架、Service transport 和 coverage backend。cross-compile、fake profile 或 parser-only fixture 不能替代 native PASS。

### 18.4 端到端与故障注入

端到端覆盖 function、file、target、workspace 和 coverage-gap 五类入口，以及基线、生成、预览、选择性接受、写入和一键重跑。

故障注入至少覆盖：

- analyzer 崩溃或输出畸形；
- solver 预算耗尽；
- configure/compile/test/coverage 超时；
- 测试崩溃或挂起；
- coverage artifact 缺失、替换或摘要不匹配；
- 用户取消和 Service 重启；
- 生成期间 source drift；
- symlink/junction/hard-link/path escape；
- 磁盘空间不足；
- 原子发布中任一步骤失败。

### 18.5 断言质量验证

参考 fixture 包含受控 mutation：返回值改变、边界比较改变、错误分支反转和公开状态更新丢失。生成测试必须杀死与其声明断言类别对应的 mutation。只提高覆盖率但不能识别受控行为变异的候选不能通过质量门禁。

## 19. 验收标准

Phase 10 只有在以下条件全部满足时完成：

1. Linux Clang/LLVM、Linux GCC/gcovr 和 Windows clang-cl/LLVM coverage 输入全部具有真实 native evidence。
2. CppUTest 和 Unity 在四个普通构建/coverage 组合中完成真实生成、编译、执行和覆盖率增量验证。
3. 每个被接受候选包含有效断言、通过测试并带来可归因覆盖增量。
4. 没有 Mock/Stub、网络访问或生产源码修改。
5. 相同输入产生稳定 case ID、顺序、测试代码和报告。
6. 取消、故障和拒绝操作保持正式 workspace 不变。
7. 已编辑文件不会被静默覆盖，原子发布故障能够完整回滚。
8. 函数、行、分支目标和所有资源预算被严格执行。
9. Code-OSS 中可以完成选择范围、启动、取消、预览、确认、执行和查看覆盖率的完整旅程，无需终端命令。
10. mutation fixture 证明生成断言不只是覆盖率探测。
11. Phase 9 gate evidence catalog 更新为新候选提交的真实结果；任何 MISSING 或 FAILED 仍阻止 release readiness。
12. 新增 Clang/analyzer 组件的摘要、inventory、第三方 notice 和自动 license audit 全部通过。
13. Phase 10 完成后重新执行完整 Phase 9 regression，再进入正式 Windows 签名、最终 license/legal 审批和正式发布资格验证。

## 20. 设计阶段本地验证说明

本设计在基于 GitHub `master` 提交 `70f243e34b62cd8eb4c2bc54574b013712b41cfd` 的隔离工作树中编写。Node 24 与 pnpm 11.4.0 依赖安装成功；设计前基线的 workspace smoke 在 Visual Studio CMake 3.31.6 下为 37/37 通过。后续 service-probe 为 191 通过、1 失败、2 跳过；唯一失败来自宿主缺少仓库固定的 CMake 4.3.4，使用 3.31.6 时固定版本探测失败。固定 CMake bundle 的本地准备在有界等待内未完成并已安全终止。

该记录只描述设计工作树的本地环境，不是 Phase 10 功能验收结果，也不能替代 CI、native coverage 或发布证据。
