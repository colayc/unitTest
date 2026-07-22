# Secure Token File Preparation 设计

**日期：** 2026-07-21
**状态：** 等待书面 review

## 背景

TypeScript service probe 当前会先写入 authentication token，然后在 Windows 上调用 `icacls`。这有两个问题：

1. token 在其 DACL 被限制前就已存在。
2. `icacls /grant:r` 会替换一个 trustee 的 entry，但不能保证移除其他所有 explicit allow ACE。因此，GitHub 的 Windows runner 保留了 Local System (`SY`) ACE，而 Go service 正确拒绝了该 file。

已记录的 security contract 保持不变：token file 是由当前 user 所有且只能由该 user 访问的 regular、non-symlink file。service 在读取和删除 token 前必须独立验证此 contract。

## 目标

- 在写入任何 secret 前以 restrictive permission 创建 token file。
- 在 Windows 和 Linux 上使用一个 launcher flow，同时将 platform-specific permission code 保留在 Go 内。
- 保留 service 现有的 owner、permission、identity、size 和 deletion check。
- 移除 launcher 对 `whoami`、`icacls`、PowerShell 或其他 shell 的依赖。
- 在不记录 log 且不通过 process argument 传递 token 的前提下 fail closed。

## 非目标

- 改变 handshake protocol 或 token format。
- 允许 Local System、Administrators、group 或 other-user 访问 token file。
- 构建 general-purpose file-permission command。
- 将 service lifecycle ownership 移入 Go process。

## 考虑过的方案

### 1. Go-owned secure file creation（已选择）

Add a mutually exclusive service command mode:

```text
unit-test-service --prepare-token-file <path>
```

Go binary 以 platform-native owner-only permission 创建新的 empty file 后退出。随后 TypeScript 在不采用 create semantic 的情况下打开该 existing file，并在启动 normal service mode 前写入 token。

该方案将 native permission logic 保留在 Go 中，在 secret 存在前安全地创建 file，并避免 shell-specific ACL behavior。

### 2. 通过 PowerShell 替换 DACL

PowerShell 可以构造新的 ACL 并移除所有 existing rule。这个方案起初更小，但会增加 quoting 和 host-policy dependency，并且仍需谨慎安排顺序，以避免在 hardening file 前写入 secret。

### 3. 允许 `SY` ACE

允许 Local System 会使 GitHub runner 通过，但这会削弱并重新定义现有 owner-only contract，而不是修复 token creation。本 phase 不采纳此方案。

## Command Interface

executable 有两种 mutually exclusive mode：

- Preparation mode：仅 `--prepare-token-file <path>`。
- Service mode：同时使用 `--endpoint <endpoint>` 和 `--token-file <path>`。

Preparation mode 拒绝 additional positional argument、existing destination、missing parent directory 或任何 creation/permission error。它不输出 token data。发生 partial failure 时，它只删除自己创建的 file instance。

Service mode 保持当前 behavior 和 error code。一起提供 preparation 和 service flag 是 usage error，exit code 为 `2`。

## 安全创建

### Windows

Go process 获取当前 process token SID，并构建 protected security descriptor；其 DACL 包含一个 allow ACE，向该 SID 授予 full access。它将此 descriptor 传给 `CreateFile`，并使用 `CREATE_NEW`，使 restrictive DACL 在创建 empty file 时 atomically 生效。当前 user 是 file owner。handle 会在 preparation mode 退出前关闭。

implementation 使用 service startup 期间相同的 semantic SID comparison 验证 resulting owner 和 protected owner-only DACL。任何 mismatch 都是 error，并触发 identity-checked cleanup。

### Linux

Go process 以 mode `0600` exclusive 创建 file。它验证结果是归 effective user 所有、且没有 group 或 other permission bit 的 regular file，然后关闭 handle。

## Launcher Data Flow

1. TypeScript 创建 temporary working directory，并在 memory 中生成 token。
2. 它使用 `--prepare-token-file <path>` 执行 service binary，并等待 exit code `0`。
3. 它在不采用 create semantic 的情况下打开 existing file 并写入 token。open operation 不得替换该 file 或其 ACL。
4. 它使用 `--endpoint` 和 `--token-file` 启动 normal service mode。
5. Go service 在不跟随 symlink 的情况下 reopen，检查 file identity 和 permission，最多读取 4096 bytes，并在接受 connection 前删除同一 file。
6. 现有 handshake、capabilities、shutdown 和 cleanup behavior 保持不变。

若任何 launcher step 失败，TypeScript 会删除其 temporary directory，并报告不含 in-memory token 的 process stdout/stderr。

## 测试

- cross-platform Go CLI test 证明 preparation mode 会创建 empty file 并拒绝 existing path。
- Windows test 证明创建的 file 归当前 SID 所有，具有只包含该 SID 的 protected DACL，且不授予 `SY` 或 `WD` access。
- Linux test 证明 mode `0600` 和当前 effective-user ownership。
- CLI test 证明 preparation mode 与 service mode mutually exclusive。
- TypeScript E2E test 演练真实的 preparation command，向 existing file 写入 token，完成 authentication、读取 capabilities 并 shutdown。
- 完整的 Windows/Ubuntu Actions matrix 仍是 acceptance gate。

## Documentation Impact

local IPC decision record 和 README 将说明：launcher 在写入 token 前请求 Go binary 创建 restricted token file。owner-only validation contract 不变。
