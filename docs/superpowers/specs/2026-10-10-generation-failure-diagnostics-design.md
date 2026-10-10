# Phase 10：生成任务安全失败诊断设计

日期：2026-10-10（Asia/Shanghai）。

状态：用户于 2026-10-10 回复“确认设计文档”，批准本文书面设计。[实施计划](../plans/2026-10-10-generation-failure-diagnostics.md)已保存，等待选择执行方式；尚未实施或运行测试。

工作区：`C:/Users/DELL/.codex/worktrees/phase10-production-generation/unitTest`。分支：`codex/phase10-production-generation`。设计基线 HEAD：`68eb72ef89f3b5adeedf34166b060964dad10bf9`；保留中的 W4／第二版修改不属于本文，不声称工作区干净。

本文是[Windows CMake 兼容计划](../plans/2026-10-08-windows-cmake-coverage-compatibility.md) W4 的诊断前置修正，继承[受管生成启动交接设计](2026-10-10-managed-generation-dispatch-design.md)的隔离、生命周期和验收边界。只补齐已有 Phase 10 自动生成失败证据，不重新开发生成器，不启动第二版十一项需求。

## 1. 已知事实与目标

最近一次受管 Windows 原生尝试使用上述提交导出的服务，自然退出 1，而非外部 1200 秒预算终止。经单独授权，仅读取保留数据库副本的生成生命周期字段，确认唯一 run 的事件是 `queued → baseline → failed`、revision 3、候选数 0、`error_code` 为空。原数据库及 WAL／SHM 三份文件哈希保持不变。

`baseline` 表示已完成检查点，下一计划阶段是 `analyzing`；不能据此证明分析器或编译器已经进入。生成任务创建到失败约 199.630 秒，早于请求的 300 秒总预算。底层失败原因仍未知，不能猜测为 LLVM、编译、权限或超时。

诊断缺失已从精确编译源码核实：[执行器](../../../apps/test-service/internal/runtime/test_generation.go)汇聚到不接收原因的 `s.fail(run)`；[协调器](../../../apps/test-service/internal/testgencoord/coordinator.go)的 `Fail` 仅终结状态；[存储层](../../../apps/test-service/internal/taskstore/test_generation.go)的检查点事务不写初始化为空的错误字段。

本轮目标：以后在现有失败收口发生时，保留可信、路径无关的“尝试阶段／失败环节／规范原因”，与失败终态原子保存，使内部只读排障能分清失败边界。目标不是修复未知的生成根因，也不保证下一次原生尝试通过。

本地证据：`.superpowers/sdd/2026-10-08-windows-cmake-coverage-compatibility/w4-dispatch-native-20261010T0243153193545Z.md` 的授权数据库诊断章节；安全生命周期 receipt `.superpowers/runtime/w4-generation-db-diagnosis-20261010/generation-lifecycle.json`，SHA-256 `C5FA8D3BC583FAF8ABBCF343447D2CB1339E24310617E11F9A6AF1F2FB4BEC5B`。这些是保留的忽略文件，不是本文提交中的发布产物。

## 2. 选定方案与接口边界

采用方案 1：复用现有 `tasks.error_code` 保存版本化的闭集诊断，本轮新诊断写入的 `error_message` 始终为空；不清洗或改写旧任务字段。复用原检查点事务，使 run、task、私有事件与诊断共同提交；不增加第二次补写。

不采用方案 2：仅输出 opt-in 安全日志。它改动较少，但丢失日志后无法从持久化任务恢复原因；本轮不同时加入日志系统。

内部职责分为三层：

- 领域定义负责闭集值、编码和解析，不依赖 runtime、协调器或存储。
- runtime 在既有失败边界确定尝试阶段和环节，并按已知错误身份分类；不把原始错误转为字符串持久化。
- 协调器／存储通过携带诊断的内部失败入口，在原有失败检查点事务内提交。保留原 `Fail`／`CheckpointGeneration` 调用接口，未携带诊断的旧调用保持原行为。

不在 `Run`、GenerationRecord、候选、请求、事件载荷中新增字段。不更改 v1.5／v1.6 schema、SDK、扩展界面或任何对外响应。诊断仅通过现有内部 task 字段或经授权的本地只读字段投影读取，不假设现有公开 run 查询已经暴露错误码，也不新增 RPC。

## 3. 数据契约与隐私

`error_code` 的唯一新格式为 `TG1:<stage>:<operation>:<reason>`，ASCII、大小写敏感、最长 128 字节，解析必须恰好得到四段，不接受空段、额外分隔符或非枚举值。三个部分均为下述静态枚举；不含 run/task ID、路径或自由文本。例如 `TG1:analyzing:resolve_target:stale_snapshot` 只是格式示例，不是本次旧失败的诊断结果。

- `stage`：`baseline`、`analyzing`、`solving`、`rendering`、`validating`、`minimizing`、`awaiting_confirmation`、`unknown`。
- `operation`：`ledger`、`reserve_budget`、`stage_run`、`stage_context`、`transition`、`preview_set`、`authoritative_set`、`publish_plan`、`preview_diff`、`commit_budget`、`finalize_review`、`resume`、`resolve_target`、`authorize_process`、`pipeline_generate`、`validate_candidates`、`minimize_candidates`、`unknown`。
- `reason`：`timed_out`、`cancelled`、`budget_exceeded`、`budget_invalid`、`stale_snapshot`、`invalid_argument`、`storage_unavailable`、`unavailable`、`conflict`、`invalid_transition`、`invalid_preview`、`unknown`。

阶段取失败发生时已知的尝试阶段，不取最新 task 的 `active_step=failed`，也不把最后完成的检查点冒充失败阶段。无法可靠确定时用 `unknown`。恢复失败的环节为 `resume`，阶段使用 `unknown`；review finalizer 失败对应 `awaiting_confirmation`。

错误分类采用 `errors.Is`／受限内部类型，不匹配 `err.Error()` 的词语，不从编译器 stdout／stderr、命令行或源码提取内容。确定优先序为：deadline、context cancellation、budget exceeded、invalid budget、stale snapshot、invalid argument、storage unavailable、coordinator／production unavailable、conflict、unknown。结构性错误只在对应显式检查失败处分配 `invalid_transition` 或 `invalid_preview`。被包装的已知错误仍可按身份分类。

若 stage context 已明确失效，优先记录 `stage_context` 及其 context 原因；否则采用实际返回错误的安全分类。父 worker context 的取消／服务关闭仍按既有退出分支处理，不为了获得诊断强制变成 `failed`。

存储层必须再次验证闭集格式、长度及只用于 `test_generation` 的 `failed` 转换；无效诊断拒绝整次写入。未知原始错误映射到已知边界及 `reason=unknown`，不是保存原始文本后做脱敏。禁止持久化原始错误、路径、源码、命令行、工具输出、连接信息或凭据；禁止对异常对象整体 JSON 序列化。

空错误码、迁移产生的已有 `stale-snapshot` 等旧值继续可读，不重新解释为 TG1，不回填历史记录；畸形 TG1 解析失败时只报告“诊断不可用”，不回显非法原文。诊断解析是可选排障能力，不阻断历史 run 的既有读取。

## 4. 失败边界、事务与并发

执行器只给目前已经调用 `s.fail` 的分支携带诊断：账本创建、预算预留、阶段返回／context、阶段转换、preview set／权威集合／publish plan／diff 检查、预算 commit、review finalizer 和恢复失败。正常成功、拒绝、取消和关闭顺序不变，不增加工具运行或自动重试。

[生产 driver](../../../apps/test-service/internal/runtime/production_generation_driver.go)只在已有 target resolution、process authorization、pipeline generation、candidate validation／minimization 的返回边界附带内部闭集环节标记，保留原 error identity 和返回语义。执行器优先采用经过验证的环节标记，否则回退 `stage_run`。不深入改写 analyzer、solver、compiler 或 pipeline 内部失败分类；若底层统一返回 unavailable 或任意错误，仍如实记录 coarse reason／unknown，不承诺此次诊断必然精确到工具内部根因。

失败写入携带观察到的 run/task 身份和 revision，在同一事务中检查现有 CAS／last_sequence 条件、更新 `test_generation_runs` 及 task 生命周期和错误码，并插入原有私有终态事件。保持 `active_step=failed`、outcome、事件类型／载荷／序列规则；只扩展错误字段写入。

第一次成功提交的终态及诊断不可覆盖。已终结的幂等重复不新增 revision 或事件，也不补写第二个原因。若竞争执行推进了非终态 revision，过期诊断入口返回冲突，不把旧阶段原因贴到新检查点上；不得另起无 CAS 的 UPDATE。正常旧接口的并发语义不因本轮诊断扩展而全局改写。

保持 runtime 现有 `cancelPending` 和 parent context 退出防护；事务检查已提交取消／其他终态。不能承诺消除原有取消与失败竞争中的所有时序，不能让诊断路径绕过已有取消、进程所有权核验或 worker 关闭等待。

任一事务步骤失败，run／task／诊断／事件共同回滚，调用方得到原有错误；不发布未提交的终态，不声称原因已持久化。数据库本身不可用时不可能保证诊断落库，不增加无界重试、旁路文件或新日志兜底。

`CheckpointWithRecord` 出错后原本静默返回的支路、coord.Get／NextStage 出错退出和其他未进入 `s.fail` 的路径不在本轮终结行为修复范围。发现其中新缺陷时单独取证，不借诊断补强更改调度／恢复状态机。检查点对 minimized case／preview 的既有安全约束也不得为了强行保存诊断而放宽；若旧约束已拒绝某种失败终结，携带诊断也须拒绝并保留原记录，不能承诺该边界能够落库。

## 5. 文件范围与既有工作保护

允许的产品文件限定如下（新文件名称在实施计划中按本契约落实）：

- 新增 `apps/test-service/internal/testgendomain/failure.go`：闭集诊断类型和编解码，独立于已有候选诊断类型。
- 新增 `apps/test-service/internal/runtime/test_generation_failure.go`：错误身份分类及内部安全环节包装，不包含执行逻辑。
- `apps/test-service/internal/runtime/test_generation.go`：仅给既有失败收口传递上述诊断。
- `apps/test-service/internal/runtime/production_generation_driver.go`：仅标记现有返回边界。
- `apps/test-service/internal/testgencoord/coordinator.go`：新增内部带诊断失败入口，保留旧接口。
- `apps/test-service/internal/taskstore/test_generation.go`：复用原检查点事务，增加经验证的诊断参数路径，不复制整套事务。

测试允许上述四层各自新增私有的定点测试文件，并对直接受影响的已有测试做最小适配；具体文件清单须在实施计划中列明。禁止修改 migration、sqlite.go、migrations.go、协议／生成模型、UI、native probe、fixture CMake、第三方工具或已冻结第二版文件。

当前 W4／暂停第二版修改逐文件保存，核对既有 18 项冻结摘要；不 reset、stash、删除、回滚或顺带暂存。本设计阶段只提交本文；忽略目录的原 W4 进度记账不作为候选源码提交。

## 6. 聚焦验证契约

实施时先建立行为 RED，再实施并验证 GREEN。主要回归通过可控真实 worker 在 baseline 后触发阶段失败，从实际任务读取新 TG1 诊断；旧行为应因错误码为空而失败，不能用人工 checkpoint 或编译错误冒充。

- 编解码／分类：完整闭集、拒绝越界、畸形／超长值、固定优先序、已知 wrapped errors、unknown fallback。异常文本中放入假的凭据／路径／源码 marker，证明未进入错误码、error_message 或私有事件；原始错误的 `Error()` 不得被诊断提取调用。
- 失败边界：目标解析、授权、pipeline 返回及执行器各现有 fail 分支，核对尝试阶段与环节，不把 `baseline` 最后检查点当成 `analyzing` 已完成。context 原因与普通错误分别验证。允许合法终结的 fixture 验证落库；被既有 record／preview 安全约束拒绝的状态验证不发生写入，不能为了测试绿色削弱约束。
- 原子性：使用临时 taskstore；成功读取 run、task 和原有私有事件；在事务中后段注入可恢复的故障，确认所有写入回滚，诊断无独立补写。
- 终态／并发：重复失败、已成功／已拒绝／已取消终态、两个竞争终结者及过期非终态 revision；只允许一个合法终态事务，已有原因和事件不被覆盖。
- 生命周期／兼容：channel／latch 确认 worker 已进入后再取消或关闭；不靠任意 sleep。临时数据库 close／reopen 后只读获取诊断；旧空错误码和迁移旧值保持可读。不启动真实服务进程或原生工具。
- 验证隔离：使用记录清楚的已提交服务基线加上述精确 owned overlays，不默认编译混有暂停第二版代码的脏模块；记录源码摘要。只跑新增及直接受影响的定点测试，不自动跑全量、广泛 race、UI、Linux／Windows native、hosted 或打包。

这些测试证明安全证据留存和兼容行为，不证明 C/C++ 用例生成、编译、执行、覆盖率增长、维护重开或两框架原生验收。全流程 W4 gate 仍须后续新授权且源代码／二进制绑定的真实运行。

## 7. 后续门槛与不承诺事项

本文方向已获确认；保存并完成内联自查后，必须请用户审阅书面设计。只有书面设计获确认，才进入 writing-plans；本轮不实施、不运行测试、不启动新实施计划工作区或子代理。

不修改旧失败证据，不追溯补出旧原因，不延长任何超时，不自动续用已消耗的 native 授权，不承诺消除 27 秒以上的明细查询耗时。未知底层失败、W4／Phase 10 正式验收、干净候选／hosted／打包、Phase 8 签名与第三方法务审批保持未通过或待办状态；不更新路线图为完成。

本文不授权推送 GitHub／Gitee、PR、合并、Release 或签名。第二版需求继续暂停。
