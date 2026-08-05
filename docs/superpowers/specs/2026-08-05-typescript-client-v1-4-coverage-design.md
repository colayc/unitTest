# TypeScript Client Protocol v1.4 Coverage API 设计

**日期：** 2026-08-05

**状态：** 已确认

**所属阶段：** Phase 5A Task 6

**上位设计：** `2026-08-03-coverage-report-pipeline-design.md`

**实施索引：** `2026-08-03-phase5-coverage-implementation-index.md`

## 1. 目标

在现有 `@unit-test-ide/test-client` 的显式 Protocol v1.0–v1.3 分支上增加完整的 Protocol v1.4 支持，并公开四个 Coverage API：

```ts
startCoverage(input: CoverageRunInput): Promise<CoverageRun>;
getCoverageRun(coverageRunId: string): Promise<CoverageRun>;
listCoverageRuns(input?: CoverageRunListInput): Promise<CoverageRunPage>;
getCoverageReport(reportId: string): Promise<CoverageReport>;
```

Client 必须优先协商 v1.4，在旧 Session 中本地拒绝 Coverage API，并对所有 v1.4 request、response、event 执行与旧版本同等级别的 runtime validation、semantic validation 和 defensive clone。

## 2. 非目标

- 不实现 Go Service 的 Coverage handler、Runtime 或 Session dispatch；这些属于 Phase 5F。
- 不读取或解析大型 Coverage JSON artifact body；该内容由 `@unit-test-ide/coverage-models` 独立验证。
- 不增加 executable、raw args、environment、working directory、collector option 或 report template 输入。
- 不重构现有 Client 为通用 schema registry，也不改变 v1.0–v1.3 的 public behavior。
- 不实现 Coverage UI、报告比较、threshold 或源码 decoration。

## 3. 方案选择

采用显式版本扩展方案：保留现有 `if`/`switch` 版本边界，为 v1.4 平行注册 schema、选择 validator、转换 payload 和解码 public model。

未采用通用 schema registry，因为它会把本任务扩大为 Client 基础设施重构，并增加旧版本兼容风险。未采用 generated type 直接断言，因为 TypeScript type 在 runtime 不提供安全边界，无法满足 closed schema、safe integer、date、URI、digest、cross-field semantics 和 defensive clone 要求。

## 4. Public API 与类型

`CoverageRunInput` 精确对应 Protocol v1.4 `coverageRunStartRequest`，只允许：

- `idempotencyKey`；
- `workspaceGeneration`；
- `projectId`；
- `coverageProfileId`；
- `catalogRevision`；
- 结构化 `selection`；
- `repeatCount`；
- `timeoutMs`。

`CoverageRunListInput` 只允许可选的 `projectId`、`coverageProfileId`、`cursor` 和 `limit`。ID、generation、cursor、selection、repeat 与 timeout 的长度、字符集和数值范围完全由 Protocol v1.4 schema 决定，不在 Client 创建更宽松的第二套规则。

返回值使用 `@unit-test-ide/protocol-models` 的 `CoverageRun`、`CoverageRunPage` 与 `CoverageReport`。Client public barrel 同时导出两个 input type 和三个返回 model type。

现有 public union 必须扩展至 v1.4：

- `ProtocolVersion`；
- `ProtocolTaskSnapshot`；
- `ProtocolArtifactMetadata`；
- `ProtocolTaskEvent`；
- capabilities 返回 union。

这保证 Client 一旦协商到 v1.4，现有 Task、Artifact、Test 与 Event API 仍能使用 v1.4 envelope 和 model，不会因为新增最高版本而破坏旧功能。

## 5. 版本协商与方法路由

首轮 handshake 按以下顺序声明支持：

```text
1.4 → 1.3 → 1.2 → 1.1 → 1.0
```

legacy fallback 继续沿用现有“只对 `UNSUPPORTED_PROTOCOL` 降级”的策略，并依次尝试较低 envelope version。其他 handshake error 不触发降级。

四个 Coverage method 固定为：

- `coverage/runs/start`；
- `coverage/runs/get`；
- `coverage/runs/list`；
- `coverage/reports/get`。

只有 negotiated version 为 `1.4` 时才能调用这些方法。v1.0–v1.3 Session 必须在 request serialization 和 `Connection.write` 之前返回稳定的 local version error；测试必须证明 wire write count 保持不变。

现有方法在 v1.4 Session 中继续使用相同 method name，但选择 v1.4 payload validator、response validator 与 decoder。v1.3 及更旧 Session 的选择逻辑保持原样。

## 6. Validation 与 decoder 分层

### 6.1 Envelope validation

`connection.ts` 注册 Protocol v1.4 message schema 所需的完整 dependency set，并为 `1.4` 编译 closed whole-message validator。未知 version、错误 method/payload 组合、额外字段或错误 event shape 继续使 connection fail closed。

Handshake 的特殊兼容逻辑只允许已提供版本集合中的 negotiated response，不放宽普通 response/event 的 version equality。

### 6.2 Payload validation

`client.ts` 为 v1.4 注册 capabilities、workspace、test、coverage、task、artifact 等 payload schema，并为四个 Coverage request/response 准备精确 validator。Outbound input 在写 wire 前验证；Inbound payload 在转为 public model 前验证。

Client 不手写比 schema 更宽的 object spread。调用者传入的 extra field、execution-plan field、unsafe integer 或错误 ID 必须被 local validation 拒绝。

### 6.3 Semantic decoder

`decoders.ts` 在 schema validation 后完成 TypeScript wire value 到 public model 的转换，并额外检查 schema 无法表达或需要 runtime representation 的边界：

- RFC 3339 date-time 必须能构造有效 `Date`；
- 所有计数、sequence、limit 和 byte value 必须是 `Number.isSafeInteger`；
- CoverageRun status/outcome/reason/report lifecycle 必须一致；
- Coverage summary 的 covered/total 关系以及 line、branch、function counters 必须安全且一致；
- tool provenance、completeness 和 artifact ID 必须保持 closed contract；
- v1.4 Artifact metadata 的 URI 与 SHA-256 digest 必须继续通过既有严格 decoder；
- page items 与 nested selection/summary/provenance 必须返回 defensive deep clone。

未知 outcome、reason、completeness 或 union kind 一律拒绝，不提供 string fallback。

## 7. 数据流

`startCoverage` 的正常路径：

1. 确认 Session 已完成 handshake 且 negotiated version 为 v1.4；
2. 使用 Coverage start request schema 验证 input；
3. 发送 `coverage/runs/start` v1.4 request；
4. `Connection` 验证完整 response envelope；
5. Client 验证 CoverageRun payload；
6. decoder 转换日期与 nested value，并返回 defensive clone。

三个查询 API 使用同一流程。`listCoverageRuns` 额外验证 page bound 和 cursor；`getCoverageReport` 只返回 metadata、summary、provenance 与 `artifactId`。后续如需 Coverage JSON 内容，调用者通过既有 Artifact API 读取 bytes，再交给 `@unit-test-ide/coverage-models` decoder。

## 8. Error handling

- Outbound input 错误：返回 local validation error，不写 wire，不关闭健康 connection。
- Coverage API version 错误：返回稳定的 unsupported-version local error，不写 wire。
- Server error envelope：继续转换为现有 `ProtocolError`，保留稳定 error code。
- Inbound schema/semantic 错误：按现有协议违规策略使 connection fail closed，并拒绝 pending request。
- Date、safe integer 或 nested semantic 错误：不得返回部分解码 object。
- Reconnect：重新协商最高共同版本；现有 subscription cursor 与 downgrade 安全规则保持不变。

## 9. Compatibility

v1.4 是 additive wire version，不修改 v1.0–v1.3 schema。Client 的旧版本 regression tests 必须继续证明：

- v1.3 Test API 的 method、payload、decoder 与 event behavior 不变；
- v1.2 Workspace/CMake API 不变；
- v1.1 Task/Artifact/Event API 不变；
- v1.0 handshake、capabilities 和 shutdown 不变；
- legacy service fallback 仍只对 `UNSUPPORTED_PROTOCOL` 生效。

## 10. 测试设计

测试沿用 `packages/test-client/src/client.test.ts` 的 in-memory duplex fixture，并按 red-green-refactor 实施：

1. handshake 首选 v1.4，并正确 fallback 到 v1.3/v1.2/v1.1/v1.0；
2. 四个 Coverage API 发送精确 method 与 closed payload，并解码有效 response；
3. v1.3 及更旧 Session 调用每个 Coverage API 均本地拒绝且零 wire write；
4. extra field、execution-plan injection、错误 ID/selection/repeat/timeout/cursor/limit 在本地拒绝；
5. unknown outcome/reason/completeness、unsafe summary/sequence、错误 lifecycle 在 Coverage response 侧拒绝，invalid URI/digest/date 在相应 v1.4 Artifact/Coverage response 侧拒绝；
6. CoverageRun、page 与 report decoder 返回 defensive deep clone；
7. v1.4 Task/Artifact/Test/Event 与 capabilities 通过现有 public API；
8. v1.3 Test API 与全部旧版本 negotiation regression 保持通过；
9. generated Coverage/Protocol drift、TypeScript build 和完整 Client tests 保持干净。

## 11. 完成标准

- `@unit-test-ide/test-client` public surface 完整支持 Protocol v1.4 与四个 Coverage API；
- Client 不接受任何 operational execution input；
- old Session local rejection 可证明不写 wire；
- v1.4 response/event 经过 whole-message、payload 和 semantic 三层验证；
- nested public model 无 wire object alias；
- v1.0–v1.3 compatibility tests 全部通过；
- `pnpm check:coverage-generated`、`pnpm check:protocol-generated`、Client build/test、适用完整门禁与 diff review 通过；
- 开发提交推送 GitHub 与 Gitee 同名分支。
