# C/C++ Unit Test IDE

Phase 1 提供带版本的协议、可复用的 TypeScript 客户端和本地 Go 服务框架。它不会执行工作区代码、CMake、编译器或测试。

## 前置条件

- Node.js 24.18.0
- 通过 Corepack 使用 pnpm 11.4.0
- Go 1.26.5

## 安装

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
```

## 验证

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:e2e
```

协议模型由 `packages/protocol-schema/schema/v1` 生成。生成的 TypeScript 和 Go 文件已提交；请编辑 Schema 并运行 `pnpm generate:protocol`，不要直接编辑生成的文件。

服务会监听随机的 per-user Windows Named Pipe，或权限模式为 `0600` 的 Linux Unix Socket。每个连接在使用其他方法前都必须完成 token handshake。身份验证 token 文件必须归当前用户所有，且只能由该用户访问：Unix 使用仅所有者可用的权限位，Windows 使用受保护的仅所有者 DACL。写入 token 前，启动器会运行 `unit-test-service --prepare-token-file <path>`，使 Go 二进制程序以平台原生的仅所有者权限创建空文件。服务会独立验证该文件，并在使用 token 后将其删除。
