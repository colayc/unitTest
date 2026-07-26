# C/C++ Unit Test IDE

Phase 2 提供带版本的协议、可复用的 TypeScript 客户端和本地 Go 任务服务。当前服务只执行 `success`、`exit-nonzero`、`hang`、`spawn-child`、`emit-output` 五种受控 simulation；协议不接受程序路径、Shell 字符串、任意参数、任意环境变量或任意工作目录，也不会执行工作区代码、CMake、编译器、测试框架或覆盖率工具。本阶段没有 Code-OSS UI。

## 前置条件

- Node.js 24.18.0
- 通过 Corepack 使用 pnpm 11.4.0
- Go 1.26.5

## 安装与验证

```sh
corepack enable
corepack prepare pnpm@11.4.0 --activate
pnpm install --frozen-lockfile
pnpm verify
```

`pnpm verify` 会依次检查生成协议、构建全部包和 Go 服务、运行单元与契约测试、运行 Go race 测试，并通过真实 Named Pipe 或 Unix Socket 执行端到端测试。

需要逐项定位失败时，可按同样顺序展开完整门禁：

```sh
pnpm check:protocol-generated
pnpm build
pnpm test
pnpm test:go:race
pnpm test:e2e
```

## 协议与安全边界

协议模型由 `packages/protocol-schema/schema` 生成。生成的 TypeScript 和 Go 文件已提交；请编辑 Schema 并运行 `pnpm generate:protocol`，不要直接编辑生成文件。消息继续使用 UTF-8 NDJSON，每行编码后上限为 1 MiB。

服务会监听随机的 per-user Windows Named Pipe，或权限模式为 `0600` 的 Linux Unix Socket。每个连接在使用其他方法前都必须完成 token handshake。身份验证 token 文件必须归当前用户所有，且只能由该用户访问：Unix 使用仅所有者可用的权限位，Windows 使用受保护的仅所有者 DACL。写入 token 前，启动器运行 `unit-test-service --prepare-token-file <path>`，使 Go 二进制程序以平台原生的仅所有者权限创建空文件。服务独立验证该文件，并在使用 token 后将其删除。

协议 `1.0` 保留 Phase 1 的严格响应形状，可完成 handshake、查询旧能力和关闭服务。协议 `1.1` 新增受控任务、事件重放、持久化与制品能力。新客户端优先协商 `1.1`，仅在服务明确拒绝时回退到 `1.0`；`1.0` 连接不能调用 Phase 2 方法。

## 任务生命周期

任务状态仅为 `queued`、`running`、`cancelling`、`finished`。终止结果仅为 `succeeded`、`command_failed`、`cancelled`、`timed_out`、`interrupted`、`infrastructure_failed`；Phase 2 不产生 `test_failed`，因为本阶段尚未执行测试框架。

任务、事件和制品属于服务实例，而不属于某条客户端连接。同一实例内，断线重连会从最后应用的全局 `sequence` 重放事件；交付语义为至少一次，TypeScript 客户端按 `sequence` 去重。服务重启不会恢复或重新附着原进程，所有未终止任务都会恢复为 `finished/interrupted`，已完成快照、已提交事件和已引用制品继续保留。

Windows 使用 Job Object 终止完整进程树，Linux 使用 Process Group/Session 并在宽限期后从 `SIGTERM` 升级到 `SIGKILL`。取消、超时、主进程退出和服务关闭都不得遗留后代进程。

## 数据目录与制品

服务模式必须传入 `--endpoint`、`--token-file` 和 `--data-dir`。数据目录只允许当前用户访问，并包含：

- `history.sqlite3`：任务快照、追加事件、运行租约和 Artifact metadata。
- `artifacts/`：与 SQLite 分离的制品字节。
- `service.lock`：阻止同一数据目录被多个服务实例同时打开的锁文件。

客户端只能按服务生成的 `artifactId` 读取制品。服务会校验规范化路径、文件大小和 SHA-256，TypeScript 客户端在分块读取完成后再次校验大小与 SHA-256。启动清理只删除临时文件和数据库未引用的孤立文件，不删除已完成任务、已提交事件或已引用制品。
