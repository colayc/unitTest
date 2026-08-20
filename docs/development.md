# 开发指南

## 产品与仓库架构

最终产品采用 Code-OSS desktop 架构，而不是浏览器部署：

- TypeScript：Code-OSS UI、IDE 状态、Protocol Client 与用户交互。
- Go Service：本机 workspace 检查、Toolchain 发现、任务持久化、CMake 构建、测试与后续覆盖率执行。
- Named Pipe/Unix Socket：桌面进程与 Service 的 per-user 本地 IPC。
- GitHub / Gitee：仅用于源码托管、协作、Hosted CI 与开发分发；二者都不是产品运行依赖，Service 执行 coverage 时不会访问网络或代码托管平台。

当前已完成 Phase 6 首个 Vertical Slice，并已实现 Phase 6B Testing API 集成代码：独立 Code-OSS Extension 可接入真实 Go Service，Workspace Trust gate、Service lifecycle、`workspace/inspect`、Test Item tree 与 Run Profile 已形成闭环。Phase 6B 只有在 Windows/Linux CI 都实际执行真实 Service smoke 后才能标记完成；Coverage UI、source decoration 与 desktop packaging 仍不在本阶段范围内。

Coverage Report Extension 已提供受信任工作区的 `Run with Coverage`、`Refresh Coverage` 和 `Open Coverage Report` command，CoverageRun 会在每个异步边界重新校验 trust、Service session、workspace generation 和 catalog revision；HTML artifact 只能通过 protocol chunk digest 校验后进入无网络 CSP viewer。Go Service 的 trusted Runtime 现在会先持久化 CoverageRun aggregate，再交给共享 `coverageexec.Coordinator`：Windows `clang-cl` 可执行 `clang-cl → llvm-profdata → llvm-cov` 链路并完成 Coverage JSON、JUnit XML 与单文件 HTML 的原子发布。真实 Windows Named Pipe + Protocol v1.4 smoke 已接入 required-PASS CI 门禁；它验证 assertion failure 对应的 failed TestRun 仍可关联 available CoverageRun，并下载、校验、解码和打开三种报告。Linux 本批明确返回 unsupported terminal aggregate；GCC/Clang native coverage execution 是下一批工作，cross-compile 或 fake response 都不能作为 Linux native PASS。

## 固定开发环境

- Node.js 24.18.0
- pnpm 11.4.0
- Go 1.26.6
- CMake 4.3.4 bundle

依赖安装：

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
```

依赖已在本机 pnpm store 中时，可使用：

```sh
pnpm install --frozen-lockfile --offline
```

## 本地门禁

日常完整门禁：

```sh
pnpm verify
git diff --check
git diff --exit-code
```

需要定位失败时，按顺序运行：

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
```

`pnpm verify` 和产品运行不得联网。只有显式的 `pnpm prepare:cmake-bundle` 可以下载固定 CMake archive；下载后仍会进行完整摘要和布局验证。

## Code-OSS Extension 开发与验收

独立 Extension 位于 `apps/code-oss-extension`。使用固定 Node.js 与 pnpm runtime 构建：

```sh
pnpm --filter code-oss-extension build
pnpm --filter code-oss-extension test
pnpm --filter code-oss-extension test:benchmark
```

真实 Service smoke 不下载依赖，也不通过 shell 启动子进程。先使用固定 Go runtime 按现有 `service-probe` 约定构建 `unit-test-service`、`cmake-fixture` 等本地 fixture，再运行验收：

```sh
node tools/service-probe/build-service.mjs
pnpm --filter code-oss-extension test:service-smoke
```

默认 Service binary 为仓库 `build/unit-test-service`（Windows 为 `build/unit-test-service.exe`）。开发者也可通过 `UNIT_TEST_IDE_SERVICE_BINARY` 指定另一份已构建 binary。该 smoke 在当前平台执行同一 contract：trusted workspace 必须完成 `READY`、handshake、capabilities、`workspace/inspect → discoverTests → tests/catalog/get → runTests`，并把真实 passed/failed item result 投影到 TestRun；untrusted workspace 必须保持零 Service process、零 token、零 endpoint 和零 data directory；Code-OSS 信任丢失会 reload/teardown Extension Host，因此验收通过显式 `deactivate()` 验证 child 退出且旧 endpoint 不可重连，不模拟不存在的 trust-revoke callback。Windows 本地结果只作为 Named Pipe evidence，Linux Unix Socket 必须由 Linux CI 实际执行，不能用 cross-compile 代替。

Windows LLVM coverage vertical slice 先准备固定 CMake bundle，再运行独立 smoke：

```powershell
pnpm prepare:cmake-bundle
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS = "clang-cl"
pnpm --filter code-oss-extension test:coverage-service-smoke
```

smoke 自行用 Go 1.26.6 构建当前仓库 Service，启动 trusted Windows Named Pipe session，完成 base build、test discovery、catalog、coverage start 与有界轮询，并通过真实 TypeScript Client 的 artifact chunk/size/SHA-256 校验读取 Coverage JSON v1、JUnit XML 和单文件 HTML。Coverage JSON 使用共享 decoder；JUnit 由逐字符 strict XML tokenizer 验证 UTF-8/XML well-formedness、合法 builtin/numeric entity、quoted/无重复 attribute、nesting、单根与 closed JUnit schema/count，DOCTYPE、ENTITY、CDATA、comment 和 XML declaration 之外的 processing instruction 一律拒绝；HTML 必须经过 Extension 的无网络 viewer adapter。

进入真实 Service 前，Windows smoke 同时安装 Node HTTP(S) guard 和一个随机唯一、仅在测试时窗作用于隔离 runner 的 Windows Firewall all-program outbound `Block/Any` rule。该 OS rule 不是 Node monkeypatch：它覆盖 Service 及其 CMake、Ninja、compiler/linker、测试进程、`llvm-profdata`、`llvm-cov` 整棵子进程树；安装后必须从 ActiveStore 复核 rule/action/direction/profile/application/address/port filter，且所有 Firewall profile 都必须启用。独立 watchdog 必须先通过 exclusive readiness marker 证明已锁定测试 Node PID；Node PID 退出后 watchdog 重复执行幂等清理，正常 teardown 还会删除并复核 PersistentStore/ActiveStore 均无 rule，Windows CI 随后的 `always()` step 再按专属 group 做兜底清理。查询权限错误不得伪装成“rule 不存在”；无法创建、审计或撤销该边界时 required CI 必须失败，不能降级为 Node-only PASS。

成功证据只在 viewer/assertion、Service shutdown、fixture 删除和 offline boundary 撤销全部成功后产生。JSON 先写入 exclusive 临时文件，flush 后从临时文件回读并验证 canonical bytes，最后一步才 atomic rename；任何 assertion、回读或 teardown fault 都不得留下最终 report。`.native-e2e/artifacts/windows/coverage-execution-report.json` 的 strict JSON 只含 platform、architecture、工具版本、run/TestRun outcome、整数 summary、三种 artifact 的 size/digest 和 duration，不含 ID 或原生路径。

证据边界是固定的：本机缺少 verified CMake/`clang-cl`/`llvm-profdata`/`llvm-cov` 能力时，未设置 required-family 的运行只能在 native execution 前产生一个 `SKIP: verified clang-cl coverage toolset is unavailable`，`pass` 计数必须为 0，且不得留下 execution report；该 SKIP 不是 Windows native PASS。Windows CI 显式要求 `clang-cl`，因此缺工具链或无法建立 OS offline boundary 必须失败。Coverage report upload 只在此前所有 job step 成功时运行，并保留 `if-no-files-found: error`；失败的 smoke 即使曾生成临时文件也不能被上传成 evidence。Linux job 不运行或上传 native coverage execution report，只保留现有 Unix Socket Service smoke；Linux GCC/Clang coverage 尚未实现。

在 Code-OSS Testing view 中，Refresh 与默认 `Run Tests` profile 只在 trusted 单根 workspace 且当前 Service session 为 `running` 时调用协议；untrusted、multi-root、无 session 或 Service stopping 状态均 fail-closed，不会发起 discovery/run。Refresh 固定先执行 `workspace/inspect`，再选择排序后的首个 project/profile，启动 discovery 并读取 catalog。`projectId + profileId + catalogRevision` 相同的 refresh 不替换已有 Test Item object；revision 变化才原子替换树，旧 revision 的 selection 不得启动 run。

10,000 item 基准可单独复现：

```sh
pnpm --filter code-oss-extension test:benchmark
```

基准输出仅记录 Node runtime、platform/architecture、item count、catalog revision、首次建树 elapsed time 与同 revision replacement count，不输出 token、endpoint 或本机路径。它验证 10,000 个 item 均进入树，且相同 revision 的 replacement count 为 0；elapsed time 是环境观测值，不是跨机器硬编码阈值。

需要分别定位 trusted 与 untrusted 验收时，先完成 Extension build，再运行：

```sh
node --test --test-name-pattern "trusted real service|host deactivation" apps/code-oss-extension/dist/test/service-smoke.test.js
node --test --test-name-pattern "untrusted real-service" apps/code-oss-extension/dist/test/service-smoke.test.js
```

Extension Host smoke 需要本机已有 Code-OSS 或 Code-OSS compatible executable。PowerShell 示例：

```powershell
$env:CODE_OSS_EXECUTABLE = "C:\path\to\code-oss.exe"
pnpm --filter code-oss-extension test:host
```

脚本通过 `--extensionDevelopmentPath` 与 `--extensionDevelopmentKind=workspace` 启动隔离的 Extension Development Host，等待生产 `activate()` 成功完成后输出的固定 `UNIT_TEST_IDE_EXTENSION_ACTIVATED` marker，然后终止并等待 host process 退出。marker 不会在 activation reject 时输出。未配置 `CODE_OSS_EXECUTABLE` 时只输出 `SKIP: CODE_OSS_EXECUTABLE is not configured` 并以 0 退出；该结果不是 PASS，也不能作为 Extension Host activation evidence。Hosted CI 的 Windows 与 Linux job 都构建相同 Service/fixture 并运行 `test:service-smoke` 与 `test:benchmark`；只有两个平台 job 的真实 smoke 均通过，才能形成 Phase 6B 跨平台 runtime evidence。`test:host` 在没有 CI-provided `CODE_OSS_EXECUTABLE` 时仍必须诚实 SKIP。

## Native 开发

先准备 bundle，再运行 native E2E：

```sh
pnpm prepare:cmake-bundle
pnpm test:e2e:native
```

Windows 需要可验证的 Visual Studio/MSVC 与 Windows SDK；clang-cl 场景还需要同一 MSVC 环境可用的 LLVM。Linux 需要 GCC 或 Clang。生成的 Build Profile 优先使用经过验证的 Ninja，Linux 缺少 Ninja 时可回退到 Unix Makefiles。

自定义 CMake 仅面向开发和受信任 workspace。它必须是绝对 executable，经过固定 probe 和文件身份验证；Protocol Client 不能提供该路径。

跨平台命令、required-family 策略和报告格式见 [native E2E](native-e2e.md)，bundle 布局见 [CMake bundle](cmake-bundle.md)。

## 版本化 Protocol

修改 `packages/protocol-schema/schema` 后运行：

```sh
pnpm generate:protocol
pnpm check:protocol-generated
```

不要手工修改生成的 TypeScript 或 Go model。新增能力必须保持 Protocol v1.0/v1.1 compatibility gate，并避免把 executable、raw args、environment 或 cwd 暴露为 request 字段。

当前 Go Service 在 trusted Runtime 构建成功后会协商 Protocol v1.4，并同时保留 workspace、test、task、event 的既有 projection；coverage provider 只在 trusted workspace 暴露。untrusted 或 provider 不可用时仍安全回退到 legacy protocol。`coverage/runs/start` 严格 persist-first，然后 resume canonical persisted Task；Windows `clang-cl` run 可以进入完成态并生成 report，Linux GCC/Clang run 当前会明确终态化为 unavailable，而不会启动 compiler、collector 或伪造 report。
