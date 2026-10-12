# Phase 10：已选工具链 ID 契约修正设计

日期：2026-10-09（Asia/Shanghai）

状态：用户于 2026-10-09 回复“确认设计文档”，已确认本文书面设计；[补充实施计划](../plans/2026-10-09-selected-toolchain-identity-contract.md)已保存，等待选择执行方式。尚未实施本修正，不代表 W4、Phase 10 或打包验收完成。

工作区：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`。

分支：`codex/phase10-production-generation`。写入本文前 HEAD：`df55ef702c5d2af5edfb7e4e5da4619504e6b6ff`。

衔接：[Windows CMake 受控兼容设计](2026-10-08-windows-cmake-coverage-compatibility-design.md)、[原实施计划](../plans/2026-10-08-windows-cmake-coverage-compatibility.md)、[首份覆盖率明细契约设计](2026-10-08-coverage-detail-first-report-contract-design.md)。本文是 W4 的有界前置修正，不替代已完成 W1–W3 或另开发生成器。

## 1. 问题与证据边界

真实 Windows 原生尝试 `win32-xUUIf8` 已完成 CppUTest 普通构建、发现及基准覆盖率，随后在启动正式自动生成时失败。前两项 Windows 夹具问题——显式头文件归属和内部目标名——已有独立行为 RED/GREEN；它们不能证明整个生成链路通过。

本次确定的根因是工具链实例 ID 被误当作 SHA-256。现有自动 ID 生产器 `automaticToolchainID` 返回 `family + "-" + truncatedHex`，总长度为 64 字符，但并非 64 个十六进制字符的摘要。本次真实选中值为：

```text
clang-cl-6c3869c0ae502e45d43c7cdb0aaead95681ee493e9489b710cbd48a
```

普通覆盖率从选中实例取得 `instance.ID`，经任务和 attested coverage index 保存为 `ToolchainID`；生成解析器原样送入 `resolveProductionCompileBinding`。现有纯摘要谓词拒绝该合法实例 ID，其他三个生成/受管确认边界也有相同语义错误。

保留元数据的有界诊断以真实已选 ID 复现首个拒绝边界；它不是重建了完整存活服务上下文的验收。此前用 File API compiler digest 作为诊断输入的成功只证明该条件下的元数据解析，不证明真实实例身份通过。禁止把这类摘要替代作为修复。

原生首份报告 UI 的实际通过继续有效，但三次失败尝试均未取得候选执行、确认应用、维护/重新打开、Unity 或双框架生成 receipt。本文不提升这些结果等级。

## 2. 已选择方案与明确未支持的情形

采用方案 1：共享工具链 ID 词法修正，并要求生成显式绑定一个已选实例。

1. 工具链 ID 使用 registry 实例 ID 契约，不再使用 SHA 契约。
2. profile 必须具有非空工具链 ID，与当前 coverage index 完全相同；当前 discovery snapshot 中必须恰好有一个相同 ID 的实例。
3. 受管确认再次读取 index 时，必须与刚重新解析出的目标工具链 ID 完全相同。
4. 候选与受管确认继续通过已有正式 build authority，实际准备出的实例必须与目标相同；不能用词法合法或 registry 成员关系代替构建授权。

空 ID 的 preset profile 和空 ID 的非 preset profile 均在生成 resolver 边界拒绝。用户需选择明确绑定实例的 profile 后才能生成。现有原生 harness 已要求实例/profile 精确匹配的 generated Ninja Debug profile，不需要新增空 ID preset 支持。

不采用方案 2：新增内部契约，返回并比较 preset 之前成功配置时的权威实例。原因是现有 `Targets` 只返回目标，后续 `PreparePlan` 可以按另一个新请求实例重新配置；“新准备实例 B == index B”不能证明“先前配置实例 A == B”。本批次不得宣称已经补齐该证明，也不得自动填充空 profile ID。

拒绝仅限本生成边界；普通 preset、构建、发现和覆盖率实现保持不变，不改变 coordinator 的配置恢复、选择或重配置语义。

## 3. ID 与摘要是两种独立类型

私有谓词 `validProductionToolchainID(string) bool` 精确采用现有 registry 契约：

- 长度为 1–128 **字节**。
- 每个字符只能是 ASCII `A-Z`、`a-z`、`0-9`、`.`、`_`、`-`。
- 没有“首字符必须字母数字”的额外限制。
- 区分大小写、逐字节比较；不 trim、不转大小写、不哈希、不去掉前缀，不设置 family 前缀白名单。

workspace 手动配置的声明契约更窄：最多 64 字节、首字符为 ASCII 字母数字。它保持原样；不能把 registry 的规则套回手动配置声明。registry 合法的前导标点只说明词法合法，生成仍需满足真实选择与唯一成员授权。

纯小写 64 字符十六进制字符串也可以是合法实例 ID，但只有满足所有身份授权条件才可使用。长度或字符形状不能推导其含义。

`validProductionDigest` 保持严格的 64 个小写十六进制字符校验；所有真实 SHA 字段继续使用它。`validProductionObjectID` 的 32 个小写十六进制字符实体 ID 规则保持不变。编译快照继续将原始 ToolchainID 纳入既有 canonical identity，并计算真正的 SHA-256；不改变域、版本、schema 或序列化形式。仅更换合法 ToolchainID 时，编译快照摘要必须随之改变。

## 4. 正式解析与确认的身份约束

### 4.1 生成 resolver

在已有 generation/project/report/source 检查之后、编译元数据解析及布局/分析之前增加以下条件：

1. index ToolchainID 满足私有 ID 谓词。
2. profile ToolchainID 非空，且逐字节等于 index ToolchainID。
3. 当前 snapshot.Toolchains 中 ID 逐字节等于 index ToolchainID 的实例计数恰好为 1；零个或两个均拒绝，即使两个 descriptor 相同也不能接受。

沿用 resolver 该上下文边界的 `testgendomain.ErrStaleSnapshot`，不新增公开错误码或协议字段。保留重新解析不可变请求时的完整 equality 检查、generation/profile 新鲜性和现有权限/预算。

不能选第一个实例，不能退回同 family 的另一实例，不能从 index 补写 profile，不能从 compiler digest、PATH 或元数据猜测已选身份。

### 4.2 元数据、目标及受管记录

只将四处被误分类的 ToolchainID 校验改用私有 ID 谓词：编译绑定入口、`generationTarget.valid()`、受管 case 验证、受管 selection 的当前 index。相邻的源码、compile、CMake target、profile、test target、framework、analyzer、工作区、执行/receipt 摘要不变。

原始 ID 必须贯穿目标、编译绑定、候选渲染/执行上下文、受管记录保存/加载及确认上下文；不能在任意一层规范化成另一个合法值。

### 4.3 受管确认的第二次读取

`productionManagedSelectionAuthority.resolveBinding` 先重新解析不可变请求，随后再读取 current index。第二次读取除了现有 report/project/generation 检查和新的 ID 词法检查，还必须要求：

```text
freshIndex.ToolchainID == reResolvedTarget.toolchainID
```

两个分别合法、分别存在的不同 ID 仍必须拒绝，沿用该边界的 `errProductionManagedUnavailable`。保持既有源快照、覆盖率来源及后续确认前再次校验。

正式 build authority 仍为必需：传递原样 ID，核对 prepared instance、generation/project/profile、生产/测试目标及 CTest 映射，保留失败时释放资源。fixture preparer 回传请求 ID 只能证明单元边界，不是实际 coordinator 或空 ID preset 权威证明。

## 5. 确切源文件边界

本修正最多修改下面 5 个现有生产文件，均位于 `apps/test-service/internal/runtime/`：

| 文件 | 允许职责 |
| --- | --- |
| `production_generation_pipeline.go` | 新私有 ID 谓词；仅目标的 ToolchainID 改用它 |
| `production_generation_metadata.go` | 仅 compile-binding 的 ToolchainID 入口谓词修正 |
| `production_generation_validation.go` | 仅 managed case 的 ToolchainID 入口谓词修正 |
| `production_managed_selection_authority.go` | 当前 index 的 ID 谓词及与刚重新解析目标的精确 equality |
| `production_generation_resolver.go` | 非空 profile、index/profile equality、当前实例唯一精确成员检查 |

行为测试只修改同目录这 6 个现有测试文件：

```text
production_generation_metadata_test.go
production_generation_pipeline_test.go
production_generation_validation_test.go
production_managed_selection_authority_test.go
production_generation_resolver_test.go
production_generation_build_authority_test.go
```

不新增、修改 toolchain producer/registry、workspace schema、build coordinator、runtime preset authority API、数据库、协议或生成算法。若实际必要修改超出上述边界，先报告具体证据并取得范围决定，不自行扩大。

已有 W4 的 4 个 Windows CMake 夹具文件、3 个 TypeScript helper/测试、原生 UI 测试和执行证据继续保留，由原 W4 收口；不是这项 ID 修正的新生产文件授权。文档使用独立、精确路径提交，不顺带提交这些源改动或忽略区诊断。

## 6. 对旧 Linux 约束的唯一有界修订

用户已接受：共享生成 runtime 恢复合法实例 ID 的消费契约，会新接受符合授权条件的 Linux `clang-...` ID；这不是“Linux 行为完全不变”。本文书面确认后，仅这一平台中立的 ID/选择校验修正及其测试覆盖，取代旧设计第 10 节、旧计划 Global Constraints 和 W4 brief 中对应的“Linux 行为保持不变”要求。

Linux CMake 夹具及原集成方式、工具链生产/选择、普通构建/覆盖率、图闭包、安全规则和剩余门槛均不因此获得修改授权。Windows 配方不替换 Linux 集成。

Linux 夹具已知的显式头文件归属、内部目标命名前置问题仍未修复、未验收；本修正不包含这些文件。词法测试、Windows 原生结果及跨平台编译都不能算 Linux 原生 PASS。需要解决 Linux 前置问题时必须另按证据明确处理范围。

## 7. 最小有效行为门槛

先写覆盖根因的行为反例，记录实际 RED，再实施最小修正并记录 GREEN。只运行相关 runtime 门槛，不重跑 W1–W3、已有首份报告 UI 或相同失败的耗时原生链路来证明一个词法修正。

| 门槛 | 必须证明 |
| --- | --- |
| 合法 ID 贯穿 | 真实 retained clang-cl ID、自动 clang 形式、混合大小写/点号手动 ID 原样进入绑定、目标、渲染及受管保存/加载；只变合法 ID 会改变 compile digest |
| 词法边界 | 1/128 字节合法，0/129 非法；空白、控制字符、斜杠、反斜杠、非 ASCII 拒绝；无修剪/大小写归一化；前导标点按 registry 契约测试 |
| 真 SHA 不放宽 | ID 形状、uppercase hex、坏 hex、错误长度不能进入真实摘要字段；覆盖元数据 target/profile、目标 source/compile/workspace/framework/analyzer/test-target、managed source 及现有 artifact/receipt 约束 |
| 实例/选择授权 | 正例具有真实声明的 selected profile 和当前实例；缺失、同 family 不同 ID、重复相同 ID、profile/index 不同、非法 ID、stale generation/request 均拒绝；空 ID preset 与非 preset 均拒绝，且未调用 layout/analysis |
| 二次 index 漂移 | 重新解析目标后将 index 改为另一个单独合法且已发现的 ID，确认拒绝；不能只测空字符串漂移 |
| build authority | 原样请求/prepared ID 及有效计划绑定；nil/error/unavailable、不同实例/generation/profile/targets/CTest 映射拒绝，失败释放保留；不把 echo fixture 当成真实构建证明 |

旧 resolver 正例若没有 selected profile 或当前 toolchains，补齐真实声明的 fixture 上下文；不得删除新拒绝条件或依赖测试专用例外。表中尚未运行的门槛不能提前标记通过。

## 8. 恢复 W4 的顺序和完成定义

1. 本文书面审阅后，编写有界实施计划，包含上述 11 个路径、TDD/精确身份门槛及与原 W4 的依赖；不重派已完成 W1–W3。
2. ID 修正通过相关行为测试和独立代码审查后，恢复原 W4 现场；已有架构审查不是修正代码审查或整个分支审查。
3. 再构建并运行所需的真实 Windows CppUTest/Unity 路径：候选真实编译执行、分支覆盖率基准 `2/4` 到 `4/4` 且原覆盖不回退、确认应用、可维护源码实际修改、重新打开独立服务/客户端及正式全部测试/覆盖率复跑。
4. 新 SDK/build/CMake 生命周期的有界 race 门槛仍要完成；不重复不相关 taskstore race。关键门槛后仅运行一次所需全局收口验证；失败按实际归属处理，不盲目重跑矩阵。
5. 区分 dirty 尝试与干净提交的实际 receipt/candidateCommit；最终 W4 任务审查及整个 PR #39 分支审查之后，才使用既有授权正常推送双远端、更新现有 PR、消费 hosted 验收。不得新建重复 PR、合并、操作 master、强推、发布 Release 或启用签名。

仅本修正的单元测试通过不等于自动生成验收完成；Windows 原生通过也不等于双平台 hosted 或新安装包资格完成。独立进程清理、Windows 2022 LLVM 解包、Linux 夹具前置问题、候选打包资格及 Phase 8 签名/legal 门槛均保持各自未完成状态，不以本文关闭。

## 9. 不可放宽的共同约束

完全离线；不引入 Mock/CMock、CppUTestExt Mock 扩展、云模型、新服务端、协议版本或另一套生成算法。

保留 APP_ID 进程闭包、输入快照/身份/摘要、受管输出、来源认证、分页/分析/执行预算、原覆盖率不回退；保持已有 128 个 CMake 文件、单文件 512 KiB、总计 4 MiB、8192 条命令、65536 个参数、64 个启动计划条目及 wire/File API/Launch 预算。

用户 CMake 不自动改写；框架/vendor/license、原 helper 与 CMake/LLVM/SDK 认证锁不改。未知/动态表达式、越界/别名、输入或选择变化及不完整证据继续失败关闭，不制造 PASS/skipped，不把工具链身份错误转化成摘要伪造或编译器回退。
