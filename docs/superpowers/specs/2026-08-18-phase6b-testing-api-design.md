# Phase 6B Code-OSS Testing API 集成设计

## 1. 目标与范围

Phase 6B 将现有 Code-OSS Extension Vertical Slice 扩展为可使用的 Testing API 集成：trusted 单根 workspace 自动发现测试，构建稳定的 Test Item tree，支持刷新、运行整个测试树/容器/单项，并把 Go Service 的 test events 和最终 run snapshot 映射到 Code-OSS `TestRun`。

本阶段不实现完整 Coverage UI、source decoration、完整 Code-OSS fork/branding、desktop packaging，也不修改 Go Service 或 Protocol Schema。现有 `tests/catalog/get`、`tasks/start`、`tests/runs/get` 和 `events/subscribe` contract 是唯一数据来源。

## 2. 架构边界

新增独立的 `TestingApiAdapter`，由它负责 Code-OSS `TestController`、Test Item tree、refresh、Run Profile 和 event subscription。`ExtensionController` 继续只负责 Workspace Trust、Service lifecycle、命令和销毁。

Adapter 通过受控的 `clientProvider()` 获取当前已认证的 `ProtocolClient`。没有 trusted client 时，refresh 和 run 均 fail-closed。adapter 不把 VS Code 类型带入 Go 或通用 `test-client` 包；Protocol Client 仍是唯一的 IPC 消费者。

Protocol catalog 的 `containerId/itemId` 是稳定 Test Item ID。adapter 保存 `projectId`、`profileId`、`catalogRevision` 和 event sequence cursor；后续 coverage UI 可以复用这些状态，但不与本阶段耦合。

## 3. Refresh 数据流

1. 检查 Trust 和当前 Service session；untrusted、multi-root 或无 session 时不调用 discovery/catalog API，并清空测试树。
2. 调用 `workspace/inspect`，按稳定排序选择第一个 `projectId` 和该项目的第一个 `buildProfileId` 作为当前默认测试配置。没有项目或 profile 时显示脱敏诊断并保持空树。
3. 为当前 workspace/profile 生成唯一 `idempotencyKey`，调用 `discoverTests({ idempotencyKey, projectId, profileId })`。
4. 从当前 event cursor 订阅事件，等待匹配的 `test.catalog.published`；若事件丢失，则使用有限退避调用 `getTestCatalog`，不得无限等待。
5. 按 `container → parent item → child item` 重建树，保留 disabled、framework、labels、source location 和 diagnostics。相同 `catalogRevision` 不做不必要的全树重建。

Catalog revision 变化时，旧 selection 不再执行；run profile 必须先刷新并取得当前 revision。

## 4. Run Profile 与结果映射

注册一个默认 `RunProfile`，支持三种 selection：

- 根节点：`all`
- container：`containers`
- item：`items`

每次运行调用：

```ts
runTests({
  idempotencyKey,
  projectId,
  profileId,
  catalogRevision,
  selection,
  repeatCount: 1
})
```

`test.container.started`、`test.item.started`、`test.item.finished` 和 `test.container.finished` 更新对应的 Code-OSS `TestRun`。`passed`、`failed`、`skipped`、`errored` 和 failure details 映射保持确定性；运行结束、断线或 event sequence gap 后调用 `getTestRun` 收敛最终状态。

## 5. 生命周期与安全

- adapter 的 `refresh`、`run` 和事件处理都重新读取 live Trust；Trust 丢失时拒绝新操作，清空树，并将未完成 TestRun 标为终止/错误。
- `deactivate()` 取消 event subscription，关闭 adapter，释放 TestController，并等待有限的运行清理时间。
- catalog revision 不匹配、未知 item、旧 run 或协议错误均 fail-closed，不自动执行旧 selection。
- 错误通过现有脱敏 presenter；不把 token、IPC endpoint、原始 executable path 或 session directory 写入 UI、日志 artifact 或测试输出。
- Windows Named Pipe 与 Linux Unix Socket 仍由既有 ServiceManager 处理，adapter 不实现平台分支。

## 6. 测试与验收

### Unit tests

- catalog 层级映射、稳定 ID、disabled item、source location、diagnostics 和空 catalog；
- root/container/item selection 和 `repeatCount: 1`；
- started/finished event、failure details、skipped/errored 和最终 run snapshot；
- revision stale、未知 selection、无 session、untrusted 和 multi-root；
- refresh/run 并发、Trust 丢失、Service stop、event sequence gap/reconnect；
- deactivate 取消订阅、终止未完成 run、释放 controller。

### Integration/CI

- Windows CI：真实 Service + Named Pipe 的 catalog refresh/run contract；
- Linux CI：真实 Service + Unix Socket 的 catalog refresh/run contract；
- host 无 `CODE_OSS_EXECUTABLE` 时必须输出明确 `SKIP`，不得伪造 PASS；
- 生成 10,000 个测试项的树构建基准，验证相同 revision 不发生不必要的全树重建，并记录可复现结果。

## 7. 后续接口

本阶段产生的 adapter 接口应允许后续接入：

- 多 project/profile 选择器；
- coverage run/profile 和 Coverage UI；
- source decoration 与 failure navigation；
- test history、artifact/result browser；
- 完整 Code-OSS fork、built-in registration 和 desktop packaging。

这些后续功能不得绕过 Workspace Trust、ServiceManager 或 Protocol Client 的现有安全边界。
