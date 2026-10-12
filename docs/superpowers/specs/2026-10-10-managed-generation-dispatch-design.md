# Phase 10：受管测试生成任务启动交接修正设计

日期：2026-10-10（Asia/Shanghai）。

状态：用户于 2026-10-10 回复“确认”，批准本文书面设计。[有界实施计划](../plans/2026-10-10-managed-generation-dispatch.md)已保存，等待选择执行方式；尚未实施本修正或运行测试。

工作区：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`。

分支：`codex/phase10-production-generation`。设计基线 HEAD：`f28ce3bc84662371658985051b6a65959bedc2fb`；工作区包含保留中的未提交修改，不是干净候选。

衔接：[原 Windows CMake 兼容实施计划](../plans/2026-10-08-windows-cmake-coverage-compatibility.md)的 W4，以及[有界原生诊断设计](2026-10-09-native-operation-trace-diagnostic-design.md)。本修正属于已有 Phase 10 自动生成链路的缺陷，不启动第二版需求，不重新实现生成器。

## 1. 已确定的问题与证据等级

v1.6 的 `StartManaged` 完成校验和持久化后直接返回，没有把新任务交给已有后台执行器：

- [session 路由](../../../apps/test-service/internal/session/managed_test_routes.go)调用 `StartManaged` 后返回；不承担任务调度。
- [受管启动入口](../../../apps/test-service/internal/runtime/managed_runtime_backend.go)调用 `coord.Start`，生成响应后返回；没有 `launch`。
- [协调器](../../../apps/test-service/internal/testgencoord/coordinator.go)与[存储层](../../../apps/test-service/internal/taskstore/test_generation.go)负责创建 `queued` run、任务及事件；没有工作队列执行器。
- [现有执行器](../../../apps/test-service/internal/runtime/test_generation.go)在 v1.5 启动时调用 `launch`；`ResumeAll` 的生产调用仅发生于 [runtime 打开](../../../apps/test-service/internal/runtime/runtime.go)，不是轮询调度器。[受管 run 查询](../../../apps/test-service/internal/runtime/managed_runtime_reads.go)不会启动执行。

以上调用链已经与最近一次原生尝试实际服务使用的提交导出源码核对。受管启动入口文件 SHA-256 为 `743E23EB141FB63F1AA2498F78C3AAF279468F24DBDA6B635F49CDA5E76E7650`，其工作区内容与 HEAD 相同。

最近一次尝试 `20261009T2341185072772Z` 达到 CppUTest 基准覆盖率，`startTestGeneration` 返回后出现 133 次已返回的 run 查询；最终由外部 1200 秒预算结束，结论为 `budget-expired-inconclusive`。操作追踪不包含 DTO 状态，未读取原生数据库，因此“没有启动交接”来自确切源码，不是事后声称观察到了数据库中的 `queued`。

该尝试没有生成完成、确认应用、维护/重开、Unity 或双框架验收 receipt。已有基准覆盖率不证明自动生成通过。保留的本地诊断记录为 `.superpowers/sdd/2026-10-08-windows-cmake-coverage-compatibility/w4-post-digest-native-20261009T2341185072772Z.md`；该记录被 Git 忽略，不是本文提交中的发布产物。

[现有受管启动测试](../../../apps/test-service/internal/runtime/managed_runtime_backend_test.go)的 `driver.starts` 在 `PrepareManaged` 内计数，证明请求准备而非执行阶段启动。手工构造 `awaiting_confirmation` 的 helper 直接调用 `coord.Start` 和 checkpoint，也不能替代启动交接测试。

## 2. 选定方案及未采用方案

采用方案 1：在 `StartManaged` 的 `coord.Start` 成功后、返回响应前，调用已有 `generationService.launch(current.ID)`。

复用现有 worker 注册、重复启动抑制、运行阶段预算、进程授权、取消和关闭机制。生产修改仅补齐缺失的交接，不新增队列、服务端应用或执行引擎。

不采用方案 2：独立队列扫描/调度器。它需要新的轮询、生命周期和恢复策略，超出已证实缺陷；本轮没有支持扩大设计的证据。

## 3. 启动与幂等契约

1. 保持现有解析、基准验证、请求准备、请求字段检查及持久化前二次索引/目标核验，顺序不变。
2. 必须先成功持久化，再使用返回的真实 run ID 交接；不基于猜测的 ID、暂存请求或尚未提交的任务启动。
3. 任一前置校验、准备或持久化失败，按现有错误返回，不调用 `launch`。拒绝请求不得占用本来可用的幂等键。
4. 同一活跃 run 的幂等重放仍返回相同 run/task 身份；现有 `running` 和 `cancelPending` 防护不得绕过，不能重复进入同一执行阶段。
5. 已终结或等待确认的 run 重放不得重新执行生成阶段。复用现有 `run` 对这两类状态的退出检查；允许产生立即退出的短暂 worker，不承诺新增全局去重登记或禁止任何 goroutine 创建。
6. 响应仍投影 `coord.Start` 返回的快照，允许返回 `queued`；不得为了测试断言而同步等待生成，或把后台已启动等同于响应状态必然改变。
7. RPC 调用结束不取消已交接的工作；执行生命周期继续由已有服务 worker context、阶段预算、显式取消和关闭管理。

## 4. 安全、错误与生命周期

- 保留 owner、report/source/selector、workspace generation、幂等键、目标、工具链、预算和 goals 的现有严格校验；不降低权限或“可用”门槛。
- 后台执行继续走原 `run`，使用阶段 context/预算账本和 `AuthorizeProcessLaunch`，不直接调用原生工具，也不绕过后续校验、人工确认及发布流程。
- 对已经启动的受管 run，`CancelManagedRun` 继续经过已有取消路径；服务 `Close` 继续取消已注册 worker 并等待退出，之后才能关闭 fixture store 等资源。
- 测试只验证已注册 worker 的既有取消/关闭契约，不新增并发 `StartManaged` 与 `Close` 的关闭状态机。若发现必须修改该架构才能安全完成本修正，先报告并取得新的设计决定。
- 后台失败、预算耗尽和错误映射沿用现有机制。不添加自动重试、定时扫描、超时延长或隐式服务重启。
- `CheckpointWithRecord` 失败后的静默退出是另一项静态风险，最近尝试没有记录到该错误；不与本次启动交接修正捆绑。后续若真正出现，保留实际证据并单独界定修复。

## 5. 精确文件范围与既有修改保护

本修正允许的产品代码只有：

- `apps/test-service/internal/runtime/managed_runtime_backend.go`：增加成功持久化后的已有启动器调用。

测试限定为：

- `apps/test-service/internal/runtime/managed_runtime_dispatch_test.go`：新增聚焦交接行为的测试和该文件私有的协调 fixture。
- `apps/test-service/internal/runtime/managed_runtime_backend_test.go`：仅在新增异步执行使原有受管启动用例需要确定性等待/收尾时做最小适配；不重写整套组件测试。

共享 `generationDriverFixture` 可通过测试内嵌包装复用，不修改 `test_generation_test.go`。手工 awaiting helper 仍用于组件测试，不把它改成假的后台执行证据。测试计数/观测需要同步；不得让 worker 与测试可变 fixture 数据发生未受控竞态。

不修改 session、协议、coordinator、taskstore、`test_generation.go` 或其他产品文件，不新增 migration、UI、原生探针和 SDK。若实现实际需要扩大上述范围，先暂停并说明证据，不静默纳入。

保留现有 W4 及暂停中的第二版修改，逐文件核对已冻结的 18 个文件摘要。仅精确暂存本轮文件；不能把脏 Go 模块、未审阅第二版代码或既有证据顺带提交。本文阶段只提交本文，不提交任何产品/测试代码。

## 6. 聚焦验证设计

实施阶段先添加真实失败的回归测试，再补交接调用，随后只执行新增测试与直接受影响的受管 runtime 测试。测试使用真实临时 taskstore 和已有执行器；阶段工具用可控 fixture，不调用编译器。

定点测试不得默认编译混有暂停中第二版修改的脏模块。使用明确记录的已提交基线隔离源码，加上上述精确范围的修正/测试覆盖；记录基线与覆盖文件摘要，不将其称作整个工作区的干净提交验收。不通过暂存、回滚或删除现有修改来制造干净环境。

- **真实启动**：通过 v1.6 `StartManaged` 发起 report-bound gap/function/file 请求；以 channel/latch 观察真正的 `GenerationDriver.RunStage` 入口，并在入口核对持久化 run/task 身份及请求绑定。不能以 `PrepareManaged` 次数替代。
- **持久化进展**：受控释放阶段后，通过协调器读取已提交的合法 revision/state 进展；不强求 start 响应立即非 `queued`，也不把人工 checkpoint 作为启动成功。
- **活跃幂等**：阻塞实际阶段，重放同一请求，核对相同身份且只有一次实际阶段进入；重复请求结束与 worker 收尾都使用明确同步。
- **重放已停止状态**：分别验证等待确认及终结 run 的重放无阶段重执行。等待确认状态 fixture 只用于这一状态分支，不宣称真实候选生成已完成。
- **拒绝不执行**：保留并覆盖未知/过期 selector、源码/索引变化、准备错误、字段冲突及存储失败，不出现实际 stage 入口；可重试的被拒绝请求没有预占幂等键。
- **取消与关闭**：先确认 worker 已进入受控阶段，再分别取消 run 或关闭服务；确认 context 取消、worker done 和登记资源释放，避免靠任意 sleep 猜测退出。
- **执行防护**：阶段仍持有既有 deadline，并经过进程授权回调；不改请求预算和目标。用受限测试等待上限防止挂死，等待上限不是产品超时变更。

负向计数在启动调用完成及受控 worker 收尾后核对；并发测试 fixture 的计数器/信号采用同步保护。主要启动回归必须在缺少交接的旧行为下因未进入阶段而失败，而不是编译失败、环境缺失或无关 fixture 错误；拒绝与状态防护测试可以在修正前已经通过，不冒充全部都是新行为 RED。

这些测试证明交接和资源行为，不证明真实 C/C++ 候选编译、执行、覆盖率增长或整个 Phase 10 通过。书面设计本身也不是测试结果。

## 7. 后续门槛及不承诺事项

本文获书面确认后，下一步按 `writing-plans` 保存有界实施计划，再进入用户此前选择的子代理实施/审阅流程；不重做已经完成的前置修正。

初始验证不自动运行全量测试、广泛 race、UI、Linux/Windows native、hosted matrix 或打包。原 1200 秒原生授权已经消耗，不因本文批准而续期。后续真实原生尝试需另行明确授权预算/停止规则，并绑定包含该修复的真实服务源码与二进制，不能复用旧二进制冒充新验证。

修复目标仅为清除已证实的“持久化成功却未交接”缺陷；不保证其后的生成、确认、覆盖率或维护门槛一次通过，也不承诺消除仍存在的 27–41 秒覆盖率明细耗时。

W4/Phase 10 的未通过门槛、正式签名和第三方法务审批继续保留；第二版十一项需求继续暂停。本文不授权推送、PR、合并、Release 或签名，不更新整体路线图为完成。
