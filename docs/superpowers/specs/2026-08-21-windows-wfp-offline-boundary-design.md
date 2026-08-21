# Windows WFP Offline Boundary 设计

## 1. 背景与目标

Phase8 Task9 的 Windows offline coverage smoke 目前依赖 PowerShell 持久防火墙规则、marker 文件和 guardian 进程扫描。多轮复核表明，这种组合在状态目录损坏、进程命令行解析、普通 PowerShell 并发存在等边界条件下仍可能提前清理或误判 guardian 状态。

本设计将 offline boundary 改为 Windows 原生 Windows Filtering Platform（WFP）动态会话：在 native coverage 执行前建立仅存在于 guardian 进程生命周期内的动态阻断策略；guardian 退出时由 WFP 自动删除动态对象。目标是保证：

- preflight 未确认 verified clang-cl/llvm-profdata/llvm-cov 前，不创建网络边界、不启动 Service；
- required CI 在缺少 WFP 管理权限或无法完成审计时明确失败；local 非 required 场景可在 preflight 阶段精确 SKIP；
- boundary 只阻断测试进程树向外发起的 TCP/UDP 连接，不依赖可写 marker 作为安全依据；
- guardian 崩溃、被杀或 owner 消失时不遗留持久规则、注册表状态或可被后续运行误认的文件；
- Linux 继续明确报告 unsupported，不伪造 Windows evidence。

## 2. 范围与非目标

### 范围

1. 新增 Go `offlineboundary` Windows 实现及独立 guardian 进程。
2. 使用 WFP dynamic session 和 ALE outbound-connect filters 覆盖 IPv4/IPv6。
3. 通过受控本地 IPC 完成 owner 身份、ready、release 和退出确认。
4. TypeScript smoke adapter、CI workflow、报告和 legacy cleanup 迁移。
5. Windows privileged integration、unprivileged fail-closed control、Node vertical smoke 与 Linux unsupported 测试。

### 非目标

- 不修改产品运行时对 GitHub/Gitee 的依赖；产品部署不需要连接 GitHub。
- 不在本设计中实现 Linux native coverage；Linux 仅保留显式 unsupported 分支。
- 不允许通过降低过滤范围、绕过 WFP 或静默忽略权限错误来“修复”失败。
- 不把 HTTP(S) guard 当成唯一隔离措施；它只作为补充的应用层检查。

## 3. 信任模型与权限

可信输入只有：

- 由服务进程创建并传给 guardian 的 owner PID 与 process creation time；
- guardian 自己持有的 WFP engine/session handle；
- 由 guardian 进程创建的匿名/受控 IPC 通道；
- preflight 产生的、闭集且无路径的 toolchain identity/digest 结果。

下列内容不再作为安全依据：state directory、marker 文件、PID 文件、持久防火墙规则、注册表项、目录名或可写 JSON。

本方案选择“WFP 管理权限必需”模型：

- required CI：无法打开 WFP engine、创建动态 session、安装或审计 filters 时 FAIL；
- local/non-required：若在任何 session/filter 创建前确认 toolchain 不可用，可以 SKIP；一旦 preflight 已通过而 boundary 失败，必须 FAIL；
- 不提供非管理员 PowerShell/HTTP 降级路径。

WFP 官方文档说明 dynamic session 中的对象会随 session 结束自动删除，包括进程终止场景：[Object Management](https://learn.microsoft.com/en-us/windows/win32/fwp/object-management)、[FwpmEngineOpen0](https://learn.microsoft.com/en-us/windows/win32/api/fwpmu/nf-fwpmu-fwpmengineopen0)、[FWPM_SESSION0](https://learn.microsoft.com/en-us/windows/win32/api/fwpmtypes/ns-fwpmtypes-fwpm_session0)。

## 4. 总体架构

组件关系如下：

```text
TS coverage smoke
  └─ preflight (verified LLVM identity/digest)
       └─ native-offline-guardian.exe
            ├─ WFP dynamic session
            ├─ ALE_AUTH_CONNECT_V4 filter
            ├─ ALE_AUTH_CONNECT_V6 filter
            └─ local IPC: owner/ready/release/exit
                 └─ closed APP_ID launch declaration for Service/native tools
```

### 4.1 Go API

```go
type OfflineBoundary interface {
    Start(context.Context, OwnerIdentity) (Lease, error)
}

type Lease interface {
    Ready() <-chan struct{}
    Close() error
    Wait() error
}

type OwnerIdentity struct {
    PID          uint32
    CreationTime uint64
}
```

`Start` 只接受当前服务进程计算出的 identity。guardian 在 ready 前必须重新验证 PID 与 creation time，避免 PID reuse；release 之后 `Close` 等待 guardian 退出并确认 WFP session 已关闭。所有 close/release 操作幂等，重复调用返回同一个 canonical error 或 nil。

### 4.2 WFP filters

guardian 使用 `FwpmEngineOpen0` 打开 dynamic session，并在以下层添加 block filters：

- `FWPM_LAYER_ALE_AUTH_CONNECT_V4`；
- `FWPM_LAYER_ALE_AUTH_CONNECT_V6`。

filters 的 action 为 block，provider/sublayer/filter key 为每次 session 新生成的 GUID。每个 filter 同时绑定精确 APP_ID 并排除 loopback；未注册的无关进程和本地资源不被机器级阻断。filter 创建、查询和删除均在同一 dynamic session 内完成，不写 PersistentStore，也不调用 PowerShell 防火墙 cmdlet。

WFP 没有 PID-tree condition。支持的 coverage build 因此使用 closed LaunchPlan：planner 在启动 CMake/Ninja 前有界解析主 `CMakeLists.txt`、literal `add_subdirectory` 和 literal/pre-generated `include`，收集 `add_custom_command`、`add_custom_target` 与 `add_test` 的每个 `COMMAND` executable，并要求它精确映射到已注册 APP_ID。除精确 `$<TARGET_FILE:...>` 映射外，未解析的动态变量/生成表达式、无法静态证明的 include/subdirectory、未知 executable、shell/代启动 wrapper 或解析上限溢出都在 CreateProcess 前失败；不能将任意动态 grandchildren 宣称为已覆盖。

### 4.3 Guardian IPC

guardian 由 Go 启动，继承受控的匿名 pipe 或 loopback-free named pipe。协议消息为长度受限、严格 JSON/二进制 schema：`hello`、`ready`、`release`、`error`、`bye`。消息不包含路径、token 或原始命令行；错误返回使用固定 reason code。

生命周期：

1. parent 完成 toolchain preflight 后启动 guardian；
2. guardian 验证 owner identity，打开 WFP session，创建并查询 V4/V6 filters；
3. 所有 filter audit 通过后发送 `ready`；
4. parent 收到 `ready` 才启动 Service/native coverage；
5. parent 发送 `release` 或 owner 终止时，guardian 关闭 session 并发送 `bye`；
6. parent 等待 `bye` 与 process exit，任何一步超时或审计失败均 FAIL closed。

## 5. 错误与报告契约

固定错误类别：

- `ToolchainUnavailable`：preflight 未找到 verified LLVM；local 可 SKIP，required FAIL；
- `WFPAccessDenied`：无法取得管理权限；
- `GuardianStartFailed`：进程或 IPC 建立失败；
- `FilterAuditFailed`：V4/V6 filter 缺失、额外或字段不匹配；
- `OwnerIdentityMismatch`：PID reuse、creation time 不匹配或 owner 消失；
- `GuardianTimeout`：ready/release/bye 超时；
- `SessionCloseFailed`：动态 session 未能确认关闭。

报告仅允许闭集字段：schema/version、outcome、reason、toolchain digest、guardian outcome、filter audit outcome、started/finished timestamps。不得写入绝对路径、命令行、token、环境变量、原始网络地址或防火墙内部句柄。

## 6. 兼容与迁移

- WFP guardian 成为正式实现；旧 PowerShell persistent-rule guardian 只保留一次性 legacy cleanup，用于删除历史版本遗留的已知规则组。未知规则或无法证明归属时 fail closed，不自动删除。
- 现有 `native-network-guard.ts` 的 HTTP(S) guard 继续保留，作为应用层补充；它不能替代 WFP。
- CI 的 required coverage job 必须在 Windows runner 上具备 WFP 管理权限；Linux job 只运行 parser/contract/unsupported checks。
- 旧 marker/state 文件不再创建。迁移期间发现旧 state 时先执行只读审计，再按 legacy cleanup 规则处理。

## 7. 测试策略

### Go 单元测试

- dynamic session/filter key 生成、V4/V6 closed audit；
- owner creation time 校验和 PID reuse；
- ready/release/bye 超时、重复 Close、guardian crash；
- schema 长度、字段、reason 白名单；
- 非 Windows 编译返回 `ErrUnsupported`，不执行 native side effect。

### Windows privileged integration

- 真正创建 dynamic session，查询 V4/V6 filters，验证 outbound connect 被阻断；
- guardian 正常 release、异常退出、owner 终止后确认 session/filter 自动消失；
- 不能访问 WFP 管理 API 时明确 FAIL，不伪造 SKIP；
- 同机普通 PowerShell、任意 marker/state 文件不会影响 guardian 判定。

### TypeScript vertical smoke

- preflight unavailable：0 firewall/session/process side effect，local SKIP；
- preflight verified + WFP denied：FAIL，report absent；
- verified + guardian ready：才启动 Service/native；
- guardian ready 后崩溃或 release timeout：FAIL，报告不发布；
- HTTP(S) guard、Go guardian、Service 全部关闭后才返回。

## 8. 验收标准

1. Windows privileged CI 能完成 verified clang-cl coverage native PASS，并留下无路径闭集报告。
2. Windows 无 verified LLVM 时 local smoke 恰好一个 SKIP，required control 恰好一个 FAIL；两者均不创建 WFP session、规则或 Service。
3. WFP dynamic session 关闭或 guardian 进程死亡后，Active/Persistent firewall store 均无本次运行对象。
4. guardian 对普通 PowerShell、损坏/替换目录、伪造 PID/marker 均 fail closed，不提前清理也不放行 late creator。
5. 全量 Go/Node/CI contract 测试通过；Linux 明确 unsupported；不引入 GitHub/Gitee 运行时依赖。
