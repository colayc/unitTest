# 开发指南

## 产品与仓库架构

最终产品采用 Code-OSS desktop 架构，而不是浏览器部署：

- TypeScript：Code-OSS UI、IDE 状态、Protocol Client 与用户交互。
- Go Service：本机 workspace 检查、Toolchain 发现、任务持久化、CMake 构建、测试与后续覆盖率执行。
- Named Pipe/Unix Socket：桌面进程与 Service 的 per-user 本地 IPC。
- GitHub：源码托管、PR、Hosted CI 和发布准备；不在产品运行链路内。

当前已完成 Phase 6 首个 Vertical Slice：独立 Code-OSS Extension 可接入真实 Go Service，Workspace Trust gate、Service lifecycle 与 `workspace/inspect` 已形成闭环；Testing API UI 仍属于后续子阶段。

## 固定开发环境

- Node.js 24.18.0
- pnpm 11.4.0
- Go 1.26.5
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
```

真实 Service smoke 不下载依赖，也不通过 shell 启动子进程。先使用固定 Go runtime 按现有 `service-probe` 约定构建 `unit-test-service`、`cmake-fixture` 等本地 fixture，再运行验收：

```sh
node tools/service-probe/build-service.mjs
pnpm --filter code-oss-extension test:service-smoke
```

默认 Service binary 为仓库 `build/unit-test-service`（Windows 为 `build/unit-test-service.exe`）。开发者也可通过 `UNIT_TEST_IDE_SERVICE_BINARY` 指定另一份已构建 binary。该 smoke 在当前平台执行同一 contract：trusted workspace 必须完成 `READY`、handshake、capabilities 与 `workspace/inspect`；untrusted workspace 必须保持零 Service process、零 token、零 endpoint 和零 data directory；Code-OSS 信任丢失会 reload/teardown Extension Host，因此验收通过显式 `deactivate()` 验证 child 退出且旧 endpoint 不可重连，不模拟不存在的 trust-revoke callback。Windows 本地结果只作为 Named Pipe evidence，Linux Unix Socket 必须由 Linux CI 实际执行，不能用 cross-compile 代替。

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

脚本通过 `--extensionDevelopmentPath` 与 `--extensionDevelopmentKind=workspace` 启动隔离的 Extension Development Host，等待生产 `activate()` 成功完成后输出的固定 `UNIT_TEST_IDE_EXTENSION_ACTIVATED` marker，然后终止并等待 host process 退出。marker 不会在 activation reject 时输出。未配置 `CODE_OSS_EXECUTABLE` 时只输出 `SKIP: CODE_OSS_EXECUTABLE is not configured` 并以 0 退出；该结果不是 PASS，也不能作为 Extension Host activation evidence。

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
