# 离线覆盖率驱动的测试用例自动生成

本文档描述首个版本的 C/C++ 单元测试自动生成功能。它是产品行为和安全边界说明，不是 hosted CI、native coverage、签名或法律审批的替代证据。

## 产品边界

生成器完全离线运行。源代码、编译数据库、AST/CFG 模型、生成的测试、路径信息和诊断不会发送到网络服务，也不依赖云端或本地 LLM。生成器由产品自带且摘要固定的 Clang 前端、已验证的 CMake/toolchain/framework 配置和服务侧的分析、求解、渲染、验证组件组成。

生成的测试不使用 Mock、Stub、CppUMock 或 CMock。C++ 目标渲染为 CppUTest，C 目标渲染为 Unity；`auto` 根据受信任 target metadata 选择框架。生成器不会修改生产源文件。

本能力不改变发布资格：正式 Windows 签名、最终第三方 license/legal 人工审批和最终发布资格仍是后续 Phase 8 release 阶段的独立门禁。Phase 10 的实现或本地证据不得被解释为发布完成。

## 可用范围与入口

Protocol v1.5 提供以下 scope：

| scope | 输入 | 典型入口 |
| --- | --- | --- |
| `symbol` | 已发现的 symbol ID | 函数/方法编辑器上下文（需要权威 picker 提供 ID） |
| `file` | workspace-relative file URI | 当前文件 |
| `target` | 已发现的 CMake target ID | target 上下文（需要权威 picker 提供 ID） |
| `workspace` | 已信任的 workspace generation | 全工作区（按 target 分批，需有效 workspace context） |
| `coverage-gap` | `coverageReportId` | coverage viewer 的缺口（由服务从报告解析具体缺口） |

扩展命令已注册：`unitTestIde.generateTests`、`generateTestsForSymbol`、`generateTestsForFile`、`generateTestsForTarget`、`generateTestsForCoverageGap`、`reviewGeneratedTests`、`acceptGeneratedTests` 和 `cancelTestGeneration`。当前 Code-OSS host 只有文件上下文可以从活动编辑器得到；`symbol`、`target` 和 `coverage-gap` 没有权威的 service-backed picker 时必须 fail-closed，不能猜测或让用户手填 ID，因此当前不是可用的一键入口。workspace 也必须带有效的受信任 workspace context。服务端方法为 `testGeneration/targets/list`、`testGeneration/start`、`testGeneration/runs/get`、`testGeneration/candidates/list` 和 `testGeneration/accept`；取消使用专用的 `testGeneration/runs/cancel`，不是旧版 `tasks/cancel`。

生成请求只包含 workspace URI/generation、已发现的目标标识、框架偏好、覆盖率目标、资源预算和幂等 key。客户端不能提交 shell 字符串、可执行文件、argv/environment、原生工作目录、任意编译/链接选项、输出路径、网络位置或 Mock/Stub 配置。

## 信任与能力门禁

生成开始前必须同时满足：

1. Code-OSS workspace 为 Trusted；不受信任时命令直接拒绝。
2. 当前服务会话和 workspace generation 与请求一致。
3. 服务明确广告 Protocol v1.5 离线 test-generation capability；缺失时 fail-closed，不降级为未经验证的生成。
4. workspace、build profile、target graph、compile database、toolchain、framework 和固定分析器 bundle 的摘要能够闭合。
5. 基线测试和覆盖率报告成功且 provenance 一致。

低于 v1.5 的会话必须在客户端本地拒绝生成 API，不能向旧服务发送未知消息。所有能力、任务、事件和制品均使用闭集版本化契约；路径只允许 workspace-relative URI/稳定 ID，日志和协议不包含主机绝对路径、环境变量、token、完整源文件或原始进程输出。

## 支持的安全子集

首版允许普通 C 函数、可安全构造的 C++ 函数/方法、整数/bool/有限浮点边界、枚举、空/非空指针、有界数组和字符串、可值初始化或已验证 fixture 构造的对象、内存中的可观察状态，以及产品控制临时目录内的文件读写。

以下情况稳定地拒绝，且不会先执行副作用代码：

- 网络、数据库、硬件、系统设置或 workspace 外部文件；
- 不可控时钟、随机源、外部进程/服务或后台守护进程；
- 依赖 Mock/Stub 才能隔离的调用；
- 无法安全构造/销毁的对象、无界递归/循环或无法证明预算上限的路径；
- 需要修改生产代码才能测试的内部实现；
- 不完整 AST、宏/compile identity 不一致、source snapshot 漂移或不受信任的 include/toolchain 路径。

典型 reason code 为 `unsupported-network`、`unsupported-database`、`unsupported-hardware`、`unsupported-system-state`、`unsupported-time-or-randomness`、`unsupported-external-process`、`unsupported-mock-dependency`、`unsupported-unbounded-control-flow`、`unsupported-unsafe-object`、`unsupported-production-edit`、`unsupported-ast`、`workspace-drift`、`toolchain-unavailable`、`baseline-failed`、`coverage-unavailable`、`no-acceptable-candidates` 和 `budget-exhausted`。未知或无法证明安全的情况一律归入稳定的 unsupported/fail-closed 结果。

## 生成、验证和覆盖率闭环

每个 run 按以下闭集阶段推进：

```text
queued -> baseline -> analyzing -> solving -> rendering
       -> validating -> minimizing -> awaiting-confirmation
       -> accepted | rejected | cancelled | failed
```

服务先运行相同 workspace/toolchain/framework/coverage profile 的现有测试建立基线，再分析安全子集、求解有界输入、渲染候选，并在服务拥有的临时根目录逐个验证：schema、静态检查、configure、compile/link、单候选执行、既有测试回归、coverage collection/provenance、断言质量和 coverage delta。候选必须通过全部步骤，至少增加函数、行或分支中的一种覆盖率，且不得造成既有测试或任一覆盖率指标回归。

保留候选必须有有效断言：

- `verified`：断言来源于可证明的返回值、错误结果、类型/枚举合同、公开后置状态或已接受的测试合同，并记录脱敏来源位置。
- `characterization`：只在结果依赖稳定执行观察时使用，必须明确显示 observed value、标记为 characterization，并在接受前得到单独或批量用户确认。它可能固化现有缺陷，不能冒充 verified behavior。

无断言、只断言“不崩溃”、恒真断言、只复述输入、依赖随机/时间/主机路径/执行顺序，或没有覆盖率增量的候选均拒绝，不得进入 retained set。

## 预览、确认和原子接受

结果面板显示基线/最终函数、行、分支覆盖率及 delta；每个候选的 kind、断言来源、独立覆盖贡献、诊断、资源使用、拒绝原因和 exact generated-test/CMake diff。生成期间以及 `awaiting-confirmation` 之前，正式 workspace 保持只读。

Protocol v1.5 的 Accept wire request 严格只有 `runId`、`candidateId`、`confirmationDigest` 和 `confirmCharacterization` 四个字段。task/candidate-set、workspace generation、source snapshot、每个目标文件的 expected-before digest、exact diff、候选代码和覆盖率/断言证据均属于服务端持久化的权威 run/preview 状态，不由客户端重复提交。服务在写入前重新读取并验证这些状态；任一摘要、workspace generation 或文件内容变化都会返回 stale/conflict，要求重新生成，绝不套用旧 patch。

唯一允许写入的路径是服务验证过的 generated test 文件及其所需测试目标 CMake 条目。测试源和 CMake patch 在一次确认交易中原子提交；写入、flush、rename 或回读失败时完整回滚。重复接受相同摘要是幂等的；不同 preimage 产生冲突而不覆盖用户内容。

## 取消、重启和恢复

客户端通过 Protocol v1.5 的 `testGeneration/runs/cancel` 关闭 generation run，按拥有关系终止 analyzer/solver/compiler/test/coverage 的完整进程树，等待 stage/process-owner close attestation 后才进入 `cancelled`，清理服务临时根目录并保持正式 workspace 字节不变。服务重启后只恢复有持久摘要和 owner identity 的阶段：已完成阶段可重放，不重新接管无 owner 的外部进程；摘要或身份漂移会终止为 stale/failed，未确认候选不会自动发布。

进度读取是 owner-scoped、分页/有界 replay；其他 session 不能读取、取消或接受本次 run。任何取消、崩溃、超时、候选失败或覆盖率下降都必须可解释地显示，而不是报告为成功。

## 预算与停止条件

用户可设置函数/行/分支目标以及 wall-clock、candidate count、memory 和 concurrency 上限。服务另行限制 source/AST 大小、进程时长、运行时长、输出字节、事件数、制品数和并发数。run 在达到覆盖率目标、任一预算、连续有界轮次无增量、剩余缺口全为 unsafe/unsupported/unsolved 或用户取消时停止。

预算耗尽不等于成功；结果必须保留已完整验证的候选和明确的未完成原因。测试生成不会为了“提高覆盖率”放宽安全边界、使用网络、注入 Mock，或将 assertionless probe 当成测试。

## 离线准备与一键操作

首次部署/升级时，在允许联网的准备环境下载并锁定产品审查过的 Clang/analyzer bundle、逐文件 SHA-256、版本、资源目录、完整 inventory 和 license notice；进入运行环境后只执行离线 `check:testgen-bundle`/等价校验，不访问网络、不从 `PATH` 或用户命令回退。bundle 缺失、替换、额外文件、摘要/许可证不匹配时能力不可用。

在提供有效上下文（当前 host 可直接解析文件，或未来接入权威 symbol/target/coverage-gap picker）后，用户可在 Code-OSS 中执行“生成测试并提高覆盖率”：启动、查看阶段进度、等待预览、逐项/批量确认、原子接受，然后一键运行全部测试并查看覆盖率。缺少权威 picker 时相关命令保持 fail-closed，而不是伪造 selection。整个可用旅程不需要终端命令；接受后的首次运行仍使用既有测试发现、执行和 coverage report 管道。

## 证据与发布状态

Phase 10 的 gate/evidence 只能引用真实的本地测试结果或 immutable hosted/native receipt。local-static fixture、cross-compile、parser-only 测试不能伪装成四工具链 native PASS；未实际运行的 Linux Clang、Windows clang-cl、bundle/license、mutation、性能或 Code-OSS native 证据必须保持 `MISSING`/未闭合。

在 Phase 10 尚未完成全部必需行，或仍存在任何 MISSING/FAILED 时，gate matrix 必须保持 `releaseReady=false`。即使 Phase 10 功能验收闭合，以下三项仍明确延后：正式 Windows 签名、第三方 license/legal 人工审批、最终发布资格/文档收口。它们必须在具体合并源码与制品上重新取证，不能由本地生成器文档替代。

相关设计与契约：

- `docs/superpowers/specs/2026-09-27-offline-coverage-guided-test-generation-design.md`
- `packages/protocol-schema/schema/v1.5/test-generation.schema.json`
- `docs/superpowers/plans/2026-09-27-offline-coverage-guided-test-generation.md`
