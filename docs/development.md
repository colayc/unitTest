# 开发指南

## 产品与仓库架构

最终产品采用 Code-OSS desktop 架构，而不是浏览器部署：

- TypeScript：Code-OSS UI、IDE 状态、Protocol Client 与用户交互。
- Go Service：本机 workspace 检查、Toolchain 发现、任务持久化、CMake 构建、测试与后续覆盖率执行。
- Named Pipe/Unix Socket：桌面进程与 Service 的 per-user 本地 IPC。
- GitHub：源码托管、PR、Hosted CI 和发布准备；不在产品运行链路内。

当前 Phase 3D 已实现 Service 与 native CMake 构建链路，Code-OSS UI 将在后续阶段接入。

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
