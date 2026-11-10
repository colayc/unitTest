当前说明（2026-11-10；仅同步文档，不执行新的构建或验收）

本入口仍只生产 Windows 资源原料，不是最终可双击的 UnitTestIDE.exe 产品包。
用例生成保持 PAUSED_RUNTIME_DISABLED；本次不恢复生成器或运行用户测试。
以下是当前本地接线和已有证据的说明，不是提交、推送、CI、生产或发布授权。

来源锁与本地验证
windows-input-source.json 已为 APPROVED，coordinate 已固定到私有输入仓库
colayc/unitTest-inputs 的提交和清单 path/size/SHA-256；实际值以该锁文件为准。
此批准仅针对输入来源，不代表当前候选、workflow、run 或产物已获批准，
也不能替代原 prepare/check、逐文件摘要和精确集合校验。
已有本地定点结果为 3/3 个顶层测试入口、14/14 个纯 PowerShell 模型子项。
本次只修改 README，复用上述未变测试依赖的结果，不重跑、不追加通过计数；
这不是对派生的全部 58 项重新测试，也不是原四组32项或完整矩阵的新结果。
本地模型不证明 GitHub 表达式解析、失败/跳过传播、输出传递、审批等待、
作业排队期间的来源稳定性或真实资源生产；这些仍需后续实际验收。

同批身份与人工审批
同仓 PR 的 verify-windows 与 verify-framework-windows 统一检出 PR head H。
W 是实际 github.workflow_sha，不要求 H 与 W 相等，两个 SHA 都必须独立核验：
实际候选 == WINDOWS_INPUTS_APPROVED_CANDIDATE_SHA；
实际 workflow == WINDOWS_INPUTS_APPROVED_WORKFLOW_SHA。
审批值必须是对应的完整40位小写 SHA；身份回执本身不是人工批准。
审批前核对同一 repository、run ID、attempt、事件、ref、H/W 和完整消费作业集合。
同一批不能通过逐作业轮换共享审批值来绕过身份不一致。
PR foundation 的允许集合为上述两个 Windows verifier；
master 手动 foundation 仅在原依赖成功后再包含 package-windows；
master 资源 producer 的允许集合为 resources。不同批次不得串用回执。
来源不足、批次不符、回执缺项或原依赖失败仍阻断，不改用通配授权或降级路由。

凭据与后续门槛
保留 windows-inputs-read 人工审批、双 SHA 校验及原凭据导入前门槛。
专用只读令牌仍只在读取步骤使用；不得放宽为仓库级同名 secret，
不得进入参数、URL、日志或资源程序环境；本次不写令牌、不改 GitHub 设置。
实际运行前仍须核对受保护环境、允许 ref、审批身份及无凭据路由隔离。
普通 PR CI 仍可能包含旧矩阵和生成相关测试；用例生成暂停不等于 CI 测试被删除。
提交、双端推送、CI 范围、合并和资源生产均需另行授权，不能从本说明推定许可。
手动资源 producer 仍须满足 workflow 位于默认分支及实际 master 完整 SHA 门槛，
不得用配方基线、本地工作区摘要或 PR head 冒充真实 producer 提交。
资源 producer 成功仍不等于最终免安装 EXE 完成，后续保留最终包组合、
隔离加载及基础功能验收；正式签名与第三方 license/legal 人工审批门槛不变。
本说明不解决 Linux 下载、PR 必需检查或 Phase 9 历史证据问题。
不创建 Release、不签名；未通过后续门槛的原料仍仅限 UNSIGNED_TEST_ONLY。

历史原文（完整保留）
以下为早期阶段原文，不作为当前状态、操作顺序或审批许可。
其中“UNAPPROVED / coordinate=null”“未创建输入仓库”“尚未运行模型”
以及 PR head/merge 混用、逐作业审批和未来授权顺序等表述仅记录当时状态；
与本页当前说明不一致时，以以上当前说明及对应固定回执为准。
历史原文中的阻断、失败、退出码缺失说明和未完成验收门槛不因此消失。

----- 历史原文开始 -----
Windows 免安装包：独立资源重新生产入口

本入口只生产原料，不是最终可双击的 UnitTestIDE.exe 产品包。
本轮源码为静态实施，尚未运行模型、资源构建、自检或 workflow。
用例生成保持 PAUSED_RUNTIME_DISABLED；不启动生成器、不运行用户测试。
不创建 Release、不签名；输出仅为 UNSIGNED_TEST_ONLY 测试原料。

固定来源
资源配方基线：59928bac784fedff614f7d154ef227f15b191eee。
实际 producer 提交：将来独立审查、提交并合并后，以完整 master SHA 批准。
不能用配方基线或本地派生状态冒充实际 producer 来源。
Code-OSS：microsoft/vscode b1c0a14de1414fcdaa400695b4db1c0799bc3124，1.92.0。
Windows 目标：vscode-win32-x64；上游 Node 20.14.0 / Yarn 1.22.22。
仓库脚本：Node 24.18.0 / pnpm 11.4.0；冻结锁文件并禁用仓库安装脚本。
只使用 windows-2022、VS2022 x64 与 Spectre 组件，缺失即停，不自动换版本。

尚未授权执行的步骤
1. 先冻结并审查当前源码。本机不先重复跑 hosted checks。
2. 另行授权提交、双端推送、PR 和合并；这些不因保存 YAML 自动获准。
3. 派发前核对仓库 artifact retention 设置至少 7 天。
4. 明确授权一次完整资源生产，包含有限 checks、资源工具自检和运行时
   EXE 的隔离 CLI --version 查询；它不打开 IDE，但仍是运行性操作。
5. 仅在默认分支手动运行 Windows portable resource inputs，唯一输入为
   已批准的 expected_source_commit 完整 SHA；不得填写 URL、路径或跳过开关。

未来 workflow 内的固定顺序
来源/期限/VS 预检 → 仓库依赖一次准备 → 四组32项模型、两个 workflow
静态测试及两个 tar 定点反例 → 固定上游检出与核验 → 上游依赖/构建 →
隔离运行时版本查询 → CMake、coverage、testgen 资源准备与原有自检 →
清单和 ZIP 往返 → payload 上传 → origin 来源回执生成和上传。
所有阶段使用固定命令和预算，首失败即停，不自动重试或扩大矩阵。
CMake capabilities/CTest version、Python/gcovr smoke、testgen bundle check
都是生产时实际工具自检，不能称为本次已经通过的静态校验。
testgen 仅是原分发工具资源，不恢复用例生成。

载荷与回执
五个角色：app/code-oss-runtime、bundles/cmake、bundles/coverage、
bundles/testgen、licenses。licenses 从四个来源命名空间分别归集。
无业务服务、业务扩展、UnitTestIDE.exe、MSIX、Linux 包或用户数据。
payload artifact：windows-portable-resources-<runId>-<attempt>
顶层恰为 windows-resources.zip、windows-resources-manifest.json、runtime-inventory.json。
origin artifact：windows-portable-resources-origin-<runId>-<attempt>
只含 windows-resources-origin.json。它绑定 payload ID/digest，不自引用。
package-result.json、日志和 runtime-summary.json 不进入成功载荷。
失败只上传白名单命令/输出/退出记录和安全摘要，不上传资源半成品。

后续门槛
两次上传和整个 run 都必须成功，实际 artifact 生命周期须至少7天且未过期。
消费时记录两个 artifact 的 ID、名字、传输摘要、run/attempt 和实际提交，
并独立批准 policy/origin/manifest/inventory/ZIP 的新字节摘要。
不套用旧 MSIX 摘要或旧 producer/foundation 来源允许值。
当前没有新的真实 run、artifact、Approval 或下载链接。
Task 4 portable 消费接入尚未实施，基础应用来源审查门槛仍保留；
新资源成功也不能直接宣称免安装产品完成。
最终仍需 basic 应用构建、启动器组合、独立加载与基础功能验收。
正式 Windows 签名及第三方 license/legal 人工审批仍未完成。

固定私有输入传递（仅静态实施，尚未验证或启用）
新来源锁 windows-input-source.json 当前为 UNAPPROVED / coordinate=null。
本轮未创建 colayc/unitTest-inputs，未核对名称可用性，未上传或配置审批环境。
本批仅 304 个 LLVM 清单文件、manifest.json/READY 两个元数据和三份
framework 原归档，共 309 文件、126143347 字节，不含 CMake 或 Linux。
原缓存只读，单次 ZIP 封装，分片至多四份且每份不超过32 MiB，整包
不超过128 MiB，清单不超过1 MiB。大小和分片、整包、逐文件 SHA-256
及精确集合均验证；拒绝越界、重解析点、链接、大小写别名或额外成员。
这不是官方原 LLVM 归档或新的 producer 成功产物，不能套用旧摘要。

新增三个模块负责 contract / pack / import，模型独立为四组32项；
原资源 producer 四组32项、两个 workflow 测试和两个 tar 反例数量不变。
新模块本轮未运行，旧模型记录不能冒充新模块验证。模型只在新独占
目录创建小文本和ZIP，不执行 clang、CMake、Go、Node 或真实生成器。
正式 producer 在原 Preflight 最后一次来源校验后才读取私有输入，
使用原7200秒总预算的余额，不增加stage、native命令或artifact。
未批准、读取/哈希/发布失败即停，不换源，不重试，不构建半成品。
两个新缓存先校验均不存在，部分发布时保存原首因和已创建对象归属；
不覆盖旧缓存、不自动回滚删除、不把部分发布标成成功。

CI 接入和凭据门槛
专用令牌仅输入私有仓库 Contents 读取，放公开主项目的
windows-inputs-read 受保护环境，不得放仓库级同名 secret。
令牌只在读取步骤出现，读取后清除，HTTP不重定向或自动重试，
不进入参数、URL、日志、Git配置或任何资源程序环境。
审批环境的 WINDOWS_INPUTS_APPROVED_CANDIDATE_SHA 和
WINDOWS_INPUTS_APPROVED_WORKFLOW_SHA 必须匹配实际候选和实际workflow。
verify-windows 的 PR checkout 可为 head SHA，而其他作业可为 merge SHA；
必须逐作业核对审批快照，不能用一个错误值批量批准或改成通配授权。
若不能证明这个绑定，就保持CI阻断，不自动重新授信。

仅托管的同仓PR/手动Windows消费选择私有路由，fork明确失败，
不以skip让必需检查变绿。原 push/master WFP 和未选择私有输入的旧路由
仍使用原准备入口，不读取新令牌；这不是私有失败后的降级。
这些非私有路由使用 windows-inputs-no-access 环境名，启用前必须另行
只读核实它不含输入令牌、不会给旧WFP路由增加未批准的保护或权限；
不能证明环境隔离及原路由兼容，就不启用CI。当前不创建该环境。
私有路由禁用其后的framework cache restore/save，避免覆盖已验字节
或自动写公共cache；原CMake、Linux及旧矩阵没有扩围。
原 prepare/check 仍执行，不用 READY 的存在代替完整校验。

后续授权顺序
先独立固定新58项源码输入，另行授权一次新四组32项定点模型。
之后另行授权一次真实快照及本地归档往返，再授权私有建仓/配置/上传。
实际固定输入仓库提交和清单 path/size/SHA-256 须由用户独立批准，
获批后才激活锁。GitHub/Gitee提交推送、合并、审批环境配置和CI运行
分别授权；推送触发的原CI范围也必须明确，不自动发布或签名。
旧52项输入、旧失败、历史退出码缺失说明、暂停分支和全部门槛保留。
本方案不解决Linux下载、PR必需检查、Phase9历史证据或最终EXE消费门槛，
不恢复用例生成，也不意味着现在已有可双击的产品包。
