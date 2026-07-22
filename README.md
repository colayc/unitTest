# C/C++ Unit Test IDE

Phase 1 提供带版本的 protocol、可复用的 TypeScript client 和本地 Go service skeleton。它不会执行 workspace code、CMake、compiler 或 test。

## 前置条件

- Node.js 24.18.0
- pnpm 11.4.0 through Corepack
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

Protocol model 由 `packages/protocol-schema/schema/v1` 生成。生成的 TypeScript 和 Go file 已提交；请编辑 Schema 并运行 `pnpm generate:protocol`，不要直接编辑生成的 file。

service 会监听随机的 per-user Windows Named Pipe，或 mode 为 `0600` 的 Linux Unix Socket。每个 connection 在使用其他 method 前都必须完成 token handshake。authentication token file 必须归当前 user 所有，且只能由该 user 访问：Unix 使用 owner-only mode bit，Windows 使用 protected owner-only DACL。写入 token 前，launcher 会运行 `unit-test-service --prepare-token-file <path>`，使 Go binary 以 platform-native owner-only permission 创建 empty file。service 会独立验证该 file，并在使用 token 后将其删除。
