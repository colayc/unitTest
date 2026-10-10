# Phase 10：原生生成样例与固定分析器契约最小修复设计

日期：2026-10-10（Asia/Shanghai）

状态：用户已同意保存本项最小修复设计；本文待书面复核，尚未实施。

工作分支：`codex/phase10-production-generation`。设计前代码基准：`f57b81ebab12534fe4fb9a07c141211bb09b429e`。

本项属于第一版 Phase 10 / Windows CMake 兼容 W4 的验收样例修正，不是第二版功能开发。第二版需求及其暂存代码继续暂停。第一版受控兼容范围和正式验收门槛继承[Windows CMake 覆盖率兼容设计](2026-10-08-windows-cmake-coverage-compatibility-design.md)及其[实施计划](../plans/2026-10-08-windows-cmake-coverage-compatibility.md)，不替代它们。

## 1. 证据、已知缺陷与未确定事项

最近一次、且仅一次已授权 Windows native 运行绑定上述提交构建的服务，于 2026-10-10T06:17:44.3270311Z 自行退出 1，用时 896.1178002 秒；并非触发外部 1200 秒上限。安全任务诊断为 `TG1:analyzing:pipeline_generate:unknown`，失败修订为 3，候选数为 0。历史为 queued → baseline → failed；诊断中的 analyzing 是尝试的下一阶段，不是已经提交的状态。

现有证据没有候选成功执行、确认应用、维护复跑、Unity 执行或最终双框架报告。诊断只定位到生成流水线错误；静态调用链进一步指向固定分析器的未分类返回，尚未证明真实运行首先失败于哪个内部检查。

另有可直接验证的样例契约冲突：

- [CppUTest 样例源码](../../../tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp)含 `#include "classifier.hpp"`。它与本次实际暂存的源码逐字节相同，SHA-256 为 `CD67C3D1DDF2A62E7D46DA309CE938EE6680D54E2CC3F31DC0DCFEB8C871147A`。
- [Unity 样例源码](../../../tools/service-probe/fixtures/test-generation/unity/src/classifier.c)含 `#include "classifier.h"`，SHA-256 为 `9567B3383192447E9F685DDC4B3223619AFA3B26A1B376D0DA92D9A6FD5D8DBF`。本次没有执行 Unity。
- [固定分析器](../../../apps/test-service/internal/testgenanalysis/analyzer.go)的 `hasSourceDirective` 在调用 Clang runner 前拒绝任何 `#`、`%:`、`??=`。这是首版现有安全边界，不是本次新加限制。
- [现有分析器测试](../../../apps/test-service/internal/testgenanalysis/analyzer_test.go)中的 `TestAnalyzeRejectsIncludedHeaderAndForeignASTExcerpt` 明确要求含 include 的源码被拒绝且 runner 调用数为 0；本轮只阅读，没有重跑。

因此，这两份样例不能在现有受支持子集内完成分析，必须修正。该结论不证明本次运行的所有前置检查已通过，也不承诺此次修正能解决剩余所有生成失败。

## 2. 方案比较与选择

采用方案 A：仅调整仓库拥有的两份验收样例，使待分析函数自身不含源码指令；保留公开头文件、真实框架测试和现有生产校验。改动小、验证可离线定点执行，但不扩展用户工程的 include 支持范围。

方案 B：让分析器支持认证后的头文件闭包。它能覆盖更多工程，但需要设计头文件依赖图、源码/头文件身份绑定、AST 来源及摘录归属、系统头文件与宏的边界，属于另一项生产能力设计，本次不采用。

不采用运行时删去指令、生成清洗后的分析副本或夹具路径白名单。这些办法会造成分析输入与用户源码/编译快照不一致，或引入仅测试环境可通过的产品行为。

用户已同意“修正样例、快速定点回归、保留安全限制”的最小范围。本文供书面复核，不将这项范围同意视为实施或 native 重跑授权。

## 3. 改动范围与样例契约

后续实施仅允许以下三处源码/测试改动：

1. `tools/service-probe/fixtures/test-generation/cpputest/src/classifier.cpp`：移除自身头文件 include 及紧邻的开头空行。
2. `tools/service-probe/fixtures/test-generation/unity/src/classifier.c`：作同样处理。
3. 新增 `apps/test-service/internal/testgenanalysis/native_seed_contract_test.go`：为上述真实样例建立固定分析器的快速契约回归。

函数体逐字节保留；不改变参数、返回类型、C++ `noexcept`、`value < 0` / `value == 0` 两个判断及 -1/0/1 返回值。移除开头指令后的首个非空内容就是现有函数定义，不新增解释性注释、宏、包装函数或额外声明。

公开头文件继续保留在 `include/classifier.hpp` 和 `include/classifier.h`，声明与实现签名不变。两份框架测试及后续生成用例仍通过公开头文件调用函数。移除实现文件的 include 不等于移除 API 头文件，也不等于让测试绕过声明。

所有 CMake 文件、头文件、测试入口、框架配方、编译参数、正式生成流水线与暂存 helper 均保持字节不变。Windows File API 中现有头文件目标归属和 `classifier_tests`/`classifier-tests` 契约不变。不编辑冻结的三个 TypeScript 文件来承载本次回归。

共享样例源码同时用于 Linux 和 Windows；两平台会读取新的样例字节。Linux 生产逻辑、CMake 集成及执行算法不变，不因样例变化自动运行 Linux native。

## 4. 输入与安全边界

正式路径保持“真实样例字节 → 现有暂存流程 → 服务拥有的源码/编译快照 → 既有固定分析器 → 既有生成/验证/发布路径”。不增加另一个分析入口，不导出 `hasSourceDirective` 为产品 API。

源码变更必须产生新的来源摘要、工作区代次及后续报告身份。旧覆盖率报告、原生任务 ID、失败数据库和历史验收回执不能重标成新样例证据。不回写历史 TG1，也不假设源码行号、符号位置或函数 ID 仍与旧样例相同。

保持原有来源/权限/身份/摘要、固定 bundle、参数闭合语法、`-nostdinc`/`-nostdinc++`、取消和预算校验。源码指令、越界 include、外来 AST 摘录仍拒绝；不允许 PATH 回退、下载工具、直接复用历史权限或放开宏。

本次不修改分析器、TG1 编码、错误分类、协议、数据库 schema、日志输出、资源预算或超时。任何后续不同失败均按实际新证据另行处理，不用本设计扩大修复范围。

## 5. 快速回归设计与 RED/GREEN 证据

在现有 `testgenanalysis` 包内新增独立测试文件，直接读取仓库中的两份真实样例，不复制硬编码“正确函数”代替真实输入。测试从稳定的测试源码位置定位仓库根；样例不存在、定位失败或包内路径不符时直接失败，不跳过、不回退到其他工程。

核心回归 `TestNativeGenerationSeedMatchesFixedAnalyzer` 按 cpputest/unity 建立子测试：

- 确认实际样例的现有函数签名和判断/返回结构仍在；`hasSourceDirective` 是辅助断言，必须在真正的分析器调用和 runner 计数核对后检查，不能提前中止主要 RED 路径。
- 将实际字节复制到测试自有临时工作区，计算它们的真实摘要；分别使用 C++17/C11 的已有合法参数及现有请求字段。
- 沿用已有 `fixtureAnalysis`、`fixtureBundle` 和 `fixtureRunner` 测试能力，调用真正的 `Analyzer.Analyze`。runner 只计数并立即返回预设测试错误；不启动 Clang，不返回伪造成功 AST 或覆盖率报告。
- 修正前两份真实样例均应因源码指令在 runner 前被拒绝，形成可编译、可执行的行为 RED；不能用缺 API、缺样例或编译失败充当 RED。
- 修正后两份样例应通过同一生产前置校验，runner 调用数恰为 1。该 GREEN 只证明达到已验证请求的 runner 边界，不宣称完成 Clang 分析、生成候选或实际测试执行。

在同一新增文件加入 `TestNativeGenerationSeedDirectiveReintroductionRejected`：对真实样例的临时副本重新加入其 own-header include，以及分别使用 `%:` 和 `??=` 的指令前缀；每例重新计算输入摘要。要求真实分析器返回错误、runner 调用数为 0。仅修改测试临时副本，不修改仓库原文件。普通 `#include` 与两种替代标记必须均被拒绝。

实施验收同时选取已有 `TestAnalyzeRejectsIncludedHeaderAndForeignASTExcerpt` 和 `TestAnalyzeSuppressesImplicitHostHeaders`，验证 include/外来摘录及隐式系统头文件安全边界未变。保留 header 内容和上述周边文件的前后字节/哈希比较。

使用从提交导出的首版源码和明确列出的本项改动进行离线、精确包/测试名验证，不编译带暂停迁移 022 的脏模块。单次 Go 定点测试最多 60 秒；不运行全量、race、UI、hosted、工具下载、真实 CMake/Clang 或 native。记录实际 RED/GREEN 命令、退出码、失败断言和测试身份；本设计保存阶段不执行这些命令。

## 6. 成功标准与明确不承诺的内容

本项修复完成的局部门槛是：两份种子源码均满足固定分析器的前置契约；旧样例行为 RED 和新样例 GREEN 有真实证据；重新引入指令仍被拒绝；函数体/公开签名和所有约定保护文件未变；无产品 API、分析算法或安全豁免变更。

原生验收继续使用原来的实际目标：双框架首份行覆盖率 5/5、分支基线 2/4，生成并验证后分支 4/4；有真实文件/函数明细、用户确认应用和维护后全部测试复跑。这些仍是未来正式验收目标，不是本项快速回归的结果。行号变化不能通过硬编码旧报告修正。

本项不承诺消除本次 896 秒耗时，不承诺 unknown 的所有内部原因已确定，也不承诺自动生成、Phase 10、打包资格或上线全部完成。任何 native、hosted 或重新打包均是后续独立门槛。

## 7. 所有权、审批与下一步

当前仅保存并本地提交这份设计文档。既有冻结 18 个文件、19 条脏/未跟踪状态和原始证据均保持不变；本次不改源码、添加测试实现、创建实施代理或重跑任何验收。

书面设计获用户确认后，下一步仅调用 writing-plans 生成本项实施计划，明确三文件范围、RED/GREEN、保护检查、提交范围及与原 W4 的衔接；尚不自动实施。

实施及定点回归需要后续执行选择。再次运行 Windows native 必须在局部门槛通过后取得新的、一次性、有界授权，不能复用已消耗的旧许可，也不能自动延长 1200 秒/15 秒的运行和确认边界。

本设计不授权 GitHub/Gitee 推送、PR、合并、master 同步、Release、签名或 legal 审批；Phase 8 延后项和第二版功能仍保持原状态。
