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
