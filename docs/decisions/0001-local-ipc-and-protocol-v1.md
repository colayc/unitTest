# ADR 0001：本地 IPC 和协议 v1

## 状态

已接受。

## 决策

测试服务作为 per-user Go 服务运行。它在 Windows 上监听 Named Pipe，在 Linux 上监听权限模式为 `0600` 的 Unix Socket。消息使用 NDJSON framing，每行上限为 1 MiB。

每个客户端都必须先发送 `handshake`。其 token 通过归当前用户所有的文件提供。写入 token 前，启动器会调用 `unit-test-service --prepare-token-file <path>`；Go 在 Unix 上以权限模式 `0600`、在 Windows 上以受保护的仅所有者 DACL 原子地创建空文件。随后启动器向现有文件写入内容而不替换它。服务会独立拒绝符号链接和非所有者访问，将文件大小限制为 4 KiB，并且必须在启动成功前删除它所打开的同一文件。协议版本为 `1.0`。

协议 schema 定义错误代码 `INVALID_MESSAGE`、`UNSUPPORTED_PROTOCOL`、`AUTH_REQUIRED`、`AUTH_FAILED` 和 `METHOD_NOT_FOUND`。支持的请求方法为 `handshake`、`capabilities/get` 和 `shutdown`。

连接断开只会结束已断开客户端的会话；不会结束服务进程。
