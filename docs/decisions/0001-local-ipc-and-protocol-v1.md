# ADR 0001：本地 IPC 和 protocol v1

## 状态

已接受。

## 决策

test service 作为 per-user Go service 运行。它在 Windows 上监听 Named Pipe，在 Linux 上监听 mode 为 `0600` 的 Unix Socket。message 使用 NDJSON framing，line limit 为 1 MiB。

每个 client 都必须先发送 `handshake`。其 token 通过归当前 user 所有的 file 提供。写入 token 前，launcher 会调用 `unit-test-service --prepare-token-file <path>`；Go 在 Unix 上以 mode `0600`、在 Windows 上以 protected owner-only DACL atomically 创建 empty file。随后 launcher 向现有 file 写入内容而不替换它。service 会独立拒绝 symbolic link 和 non-owner access，将 file 限制为 4 KiB，并且必须在 startup 成功前删除它所打开的同一 file。protocol version 为 `1.0`。

protocol schema 定义 error code `INVALID_MESSAGE`、`UNSUPPORTED_PROTOCOL`、`AUTH_REQUIRED`、`AUTH_FAILED` 和 `METHOD_NOT_FOUND`。支持的 request method 为 `handshake`、`capabilities/get` 和 `shutdown`。

disconnect 只会结束已断开 client 的 session；不会结束 service process。
