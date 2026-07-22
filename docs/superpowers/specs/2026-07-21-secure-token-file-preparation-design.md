# 安全 Token 文件准备设计

**日期：** 2026-07-21
**状态：** 等待书面审查

## 背景

TypeScript 服务探针当前会先写入身份验证 token，然后在 Windows 上调用 `icacls`。这有两个问题：

1. token 在其 DACL 被限制前就已存在。
2. `icacls /grant:r` 会替换一个受托人的条目，但不能保证移除其他所有 explicit allow ACE。因此，GitHub 的 Windows runner 保留了 Local System (`SY`) ACE，而 Go 服务正确拒绝了该文件。

文档规定的安全约定保持不变：token 文件是由当前用户所有且只能由该用户访问的普通非符号链接文件。服务在读取和删除 token 前必须独立验证此约定。

## 目标

- 在写入任何秘密数据前以严格限制的权限创建 token 文件。
- 在 Windows 和 Linux 上使用统一的启动器流程，同时将特定平台的权限代码保留在 Go 内。
- 保留服务现有的所有者、权限、身份、大小和删除检查。
- 移除启动器对 `whoami`、`icacls`、PowerShell 或其他 shell 的依赖。
- 在不记录日志且不通过进程参数传递 token 的前提下 fail closed。

## 非目标

- 改变 handshake 协议或 token 格式。
- 允许 Local System、Administrators、组或其他用户访问 token 文件。
- 构建通用的文件权限命令。
- 将服务生命周期的所有权移入 Go 进程。

## 考虑过的方案

### 1. Go 负责的安全文件创建（已选择）

新增一个与服务命令互斥的模式：

```text
unit-test-service --prepare-token-file <path>
```

Go 二进制程序以平台原生的仅所有者权限创建新的空文件后退出。随后 TypeScript 在不采用创建语义的情况下打开该现有文件，并在启动普通服务模式前写入 token。

该方案将原生权限逻辑保留在 Go 中，在秘密数据存在前安全地创建文件，并避免特定 shell 的 ACL 行为。

### 2. 通过 PowerShell 替换 DACL

PowerShell 可以构造新的 ACL 并移除所有现有规则。这个方案起初更小，但会增加引号处理和主机策略依赖，并且仍需谨慎安排顺序，以避免在加固文件前写入秘密数据。

### 3. 允许 `SY` ACE

允许 Local System 会使 GitHub runner 通过，但这会削弱并重新定义现有的仅所有者约定，而不是修复 token 创建。本阶段不采纳此方案。

## 命令接口

可执行程序有两种互斥模式：

- 准备模式：仅 `--prepare-token-file <path>`。
- 服务模式：同时使用 `--endpoint <endpoint>` 和 `--token-file <path>`。

准备模式拒绝额外的位置参数、已存在的目标路径、不存在的父目录或任何创建/权限错误。它不输出 token 数据。发生部分失败时，它只删除自己创建的文件实例。

服务模式保持当前行为和错误代码。一起提供准备和服务标志是使用错误，退出码为 `2`。

## 安全创建

### Windows

Go 进程获取当前进程 token SID，并构建受保护的 security descriptor；其 DACL 包含一个 allow ACE，向该 SID 授予 full access。它将此 descriptor 传给 `CreateFile`，并使用 `CREATE_NEW`，使严格限制的 DACL 在创建空文件时原子地生效。当前用户是文件所有者。句柄会在准备模式退出前关闭。

实现使用服务启动期间相同的 semantic SID comparison 验证生成的所有者和受保护的仅所有者 DACL。任何不匹配都是错误，并触发经身份检查的清理。

### Linux

Go 进程以权限模式 `0600` 独占创建文件。它验证结果是归有效用户所有、且没有组或其他用户权限位的普通文件，然后关闭句柄。

## 启动器数据流

1. TypeScript 创建临时工作目录，并在内存中生成 token。
2. 它使用 `--prepare-token-file <path>` 执行服务二进制程序，并等待退出码 `0`。
3. 它在不采用创建语义的情况下打开现有文件并写入 token。打开操作不得替换该文件或其 ACL。
4. 它使用 `--endpoint` 和 `--token-file` 启动普通服务模式。
5. Go 服务在不跟随符号链接的情况下再次打开文件，检查文件身份和权限，最多读取 4096 bytes，并在接受连接前删除同一文件。
6. 现有 handshake、capabilities、shutdown 和清理行为保持不变。

若任何启动器步骤失败，TypeScript 会删除其临时目录，并报告不含内存中 token 的进程 stdout/stderr。

## 测试

- 跨平台 Go CLI 测试证明准备模式会创建空文件并拒绝已存在的路径。
- Windows 测试证明创建的文件归当前 SID 所有，具有只包含该 SID 的 protected DACL，且不授予 `SY` 或 `WD` 访问权限。
- Linux 测试证明权限模式 `0600` 和当前有效用户所有权。
- CLI 测试证明准备模式与服务模式互斥。
- TypeScript E2E 测试演练真实的准备命令，向现有文件写入 token，完成身份验证、读取 capabilities 并 shutdown。
- 完整的 Windows/Ubuntu Actions matrix 仍是验收门槛。

## 文档影响

本地 IPC 决策记录和 README 将说明：启动器在写入 token 前请求 Go 二进制程序创建受限 token 文件。仅所有者验证约定不变。
