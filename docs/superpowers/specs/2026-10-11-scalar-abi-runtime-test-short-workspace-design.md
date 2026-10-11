# Phase 10 真实整数交接回归的 Windows 短工作目录补充设计

日期：2026-10-11（Asia/Shanghai）

状态：用户已确认方案 1，即仅缩短当前回归测试的临时目录。本文供书面复核，尚未实施；书面设计获确认后才编写计划补充。

本项只排除第一版 Phase 10 真实分析到求解回归的 Windows 测试目录障碍，使原整数 ABI 缺陷可以按既定门槛取得行为 RED。它补充[可信整数分析与求解设计](2026-10-10-trusted-scalar-abi-analysis-solver-design.md)及其[实施计划](../plans/2026-10-10-trusted-scalar-abi-analysis-solver.md)，不替代 Task 2 的生产集成、安全验证或 W4 验收。第二版新增需求继续暂停。

分支为 `codex/phase10-production-generation`；保存前 HEAD 为 `2d76dd77d44a6d01c71794b081a3329be07aeca5`。Task 1 已提交并审查，Task 2 仍停在真实 C++ 分析的较早进程边界；其新增回归文件尚未提交。

## 已有证据与原因判断

在保持原 runtime `TestMain`、根测试名、`cpputest` 子测试名和 `t.TempDir` 嵌套的隔离诊断中，真实工作目录为 261 个 UTF-16 码元，源码路径为 280 个。路径均为规范绝对路径，输入文件已经创建；直接运行器报告 `phase=start`、系统错误码 267、退出码 -1、输出零字节，耗时 4 ms。根测试和子测试各执行并失败一次，实际 Go exit 1，没有跳过。

此前在 probe 包较短临时目录中，相同批准工具、C++ 源码、头文件和 AST 参数调用成功，编译器退出码为 0，AST 输出为合法 JSON。源码检查表明，runtime `TestMain` 将 TEMP/TMP 根建在很深的包目录下，而 probe 包没有同样的 Windows 全局测试入口。

错误码 267 对应目录名无效；微软说明，超过 MAX_PATH 的当前目录会使 CreateProcessW 失败。[错误码说明](https://learn.microsoft.com/en-us/windows/win32/debug/system-error-codes--0-499-)，[当前目录约束](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setcurrentdirectory)。结合上述差异，证据强烈指向测试目录过长。闭合投影中的精确操作仍为 unknown，尚未执行同一真实回归的短目录对照，因此不将推断写成已观察到某个具体系统调用失败。

本地证据保留在当前执行目录 `.superpowers/sdd/2026-10-10-trusted-scalar-abi-analysis-solver/` 的 `process-boundary-diagnostic-report.md` 和 `runtime-harness-diagnostic-report.md`。后一项实际诊断只有一次 Go 启动及一次直接运行器尝试；其控制器重复尾部被一次尝试保护拦截，不构成第二次工具调用。两项均不能代替原计划要求的真实双种子 RED/GREEN。

## 方案选择与修改范围

采用方案 1：只在新增文件 `apps/test-service/internal/runtime/production_analysis_solver_native_test.go` 中，为该真实回归提供短且独立的 Windows 工作目录及必要的同文件测试。优点是局部解除测试路径障碍，不影响其他 runtime 测试；代价是需要显式管理该目录的检查和清理。

不采用方案 2：修改全局 runtime `TestMain` 或 TEMP/TMP 配置。它可能同时缩短其他测试目录，但会改变无关测试的环境、清理及隔离契约，超出本项范围。

根测试 `TestProductionAnalysisToSolverIntegerABI`、`cpputest`/`unity` 子测试、`scalarabi_integration` 编译标签及批准 bundle 的 opt-in 条件均不变。不修改全局 TestMain、TEMP/TMP/GOTMPDIR、进程 cwd、生产 probe/winprocess、Analyzer、solver、种子字节、公开头文件、CMake、bundle、数据库或第二版文件。

本补充只替换原计划 Task 2 Step 1 中 Windows 分支的 `work := t.TempDir()` 及其清理方式。Linux 继续使用原 `t.TempDir` 逻辑，不启动 Linux 真工具、不下载工具。Task 1 不重新实施或重跑。

## Windows 目录创建与释放

目录 helper 仅属于该测试文件，没有生产接口。Windows 下使用现有隔离模块的 runtime 包绝对工作目录作为父目录，在其下以短固定前缀 `.abi-` 和唯一随机后缀创建一个新的直接子目录；不经过 TestMain 设置的 TEMP/TMP 根，也不嵌入长测试名。CppUTest 与 Unity 各拥有独立目录，不能共享可变输入。

创建前由执行控制器复核隔离模块和包目录祖先无符号链接或重解析点；helper 核对父目录为规范绝对路径且就是该已验证包目录。不扫描或选择已有目录，不借用原工程工作目录。创建后立即登记测试清理，再核对新目录身份、直接父子关系和路径安全。工作目录及实际源码、头文件路径的 UTF-16 长度均不得超过 240。240 是本测试的保守上限，不是新的 Windows 系统上限；超过时以固定类别报告准备失败，不跳过、不截断、不寻找外部目录回退，也不启动编译器。

源码与头文件继续逐字节复制到该自有目录的 `src`/`include`，请求中的相对源路径、合法标准、include 参数、真实源码摘要和编译输入描述不变。不能改写函数体、清洗源码或通过手填类型绕过真实分析。批准工具路径、资源目录、空环境和已有进程树保护不变。

清理通过测试 cleanup 在正常结束、失败或取消后的测试收尾中执行。仅删除本次实际创建的目录；删除前复核其规范绝对路径、直接父子关系、目录身份和非链接条件，禁止按前缀批量删除。身份变化或无法安全清理时报告失败，不扩大删除范围、不改旧目录或历史证据。创建后发生的任何准备失败也必须经过同一清理。沿用既有运行器的进程终止和收敛机制，不保证强行终止整个 Go 进程后仍可执行 cleanup。

## 定点验证与恢复门槛

书面计划补充先给出不启动编译器的目录 helper 定点测试，覆盖短路径上限、两目录隔离、输入不变、拒绝危险或过长父目录、失败后的释放及正常释放。断言只报告闭合类别、长度、退出和清理结果，不打印临时路径、环境、原始错误、源码、AST 或编译器输出。

目录修正验证阶段随后只执行一次原真实回归选择器 `^TestProductionAnalysisToSolverIntegerABI$`，保留两个子测试和批准工具绑定。沿用每次 Analyze 最大 30 秒、AST 输出上限 16 MiB、solver 预算 5 秒/32 个候选/64 MiB/并发 1，以及原 Go 测试和外层控制器上限，不加自动重试或单独 Clang 探测。旧诊断控制器不重用或修改；原标准 `Run-ABITest` 控制器保持不变。helper 定点测试采用单独受限的本地控制器，不扩大原控制器白名单。

目录修正的局部验收要求：两个子测试均完成真实 Analyze 和正式函数绑定，到达同一 `real analyzed integers cannot be solved` 断言，实际 Go exit 1，且无跳过、工具缺失、准备/路径/超时/分析/绑定失败，cleanup 完成。这是原 ABI 缺陷的有效行为 RED，不是功能 GREEN。若出现其他较早失败，按新边界停止，不修改生产逻辑或循环重跑。

只有该双种子 RED 合格后，才按经批准的计划补充恢复原 Task 2；原有协调 RED、安全 GREEN、真实双种子 GREEN及审查提交门槛全部保留。没有完成这些门槛前，不宣称自动生成、覆盖率、Phase 10 或打包验收完成。完整 native、服务构建、hosted 和真实 Linux 验证仍需另行授权。

## 工作区保护与书面复核

本轮只新增并本地提交本文。原 19 条脏/未跟踪状态、18 个冻结文件、10 个只读绑定和现有 Task 1/Task 2 文件字节保持不变。复用当前准备好的首版 scratch，不重新初始化，不纳入暂停迁移 022；保留当前执行目录及诊断回执，不读取或改写其他执行目录的旧进度、数据库和失败状态。

本文自审检查范围、歧义、前后条件及验收边界。用户复核书面设计后，通过 writing-plans 编写最小计划补充，列明目录 helper 的确切行为、定点选择器、一次真实 RED、保护及清理证据和原 Task 2 恢复门槛；当前不进入实施，不创建新代理。

本项不授权推送 GitHub/Gitee、PR、合并、master 同步、Release、签名或 legal 审批。Phase 8 延后项和第二版暂停状态不变。
