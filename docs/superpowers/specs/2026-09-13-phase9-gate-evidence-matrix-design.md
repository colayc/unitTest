# Phase 9 Batch A：发布门禁与证据矩阵设计

**状态：** 设计已确认  
**日期：** 2026-09-13  
**范围：** Phase 9 Batch A  
**候选基线：** GitHub `master` `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`

## 1. 目标

Phase 9 Batch A 建立发布门禁的机器可读事实源、长期证据回执、离线校验器、确定性矩阵生成器与只读在线审计 workflow。它先回答“每项要求由什么验证、当前证据是什么、缺口在哪里”，再让后续 Phase 9 批次只实施真实缺口。

Batch A 不以“所有门禁已经通过”为完成条件。它以“所有权威要求已映射、每个状态都可解释且不会误报”为完成条件。最终输出同时区分：

- `catalogComplete`：门禁目录和需求覆盖是否完整；
- `releaseReady`：当前候选是否满足正式发布的全部条件。

只要存在 `MISSING`、`FAILED` 或允许延期的 Phase 8 项，`releaseReady` 必须为 `false`。

## 2. 当前基线与明确延期

当前无签名发布输入 producer run `34728889872` 与 unsigned foundation run `34731651809` 均绑定候选基线并成功。foundation 的九个 job 均成功，资格报告为 `qualified=true`，Windows 签名结果为 `not-required`。这些结果证明无签名双平台打包与安装生命周期资格，不构成生产发布资格。

本设计只允许以下三个 gate 使用 `DEFERRED`：

| Gate ID | 延期事项 | 恢复条件 |
|---|---|---|
| `P8-SIGN-WINDOWS` | 正式 Windows 签名 | 真实公开发布前，用正式证书和时间戳完成签名、干净机器验签和签名版 foundation 资格验证 |
| `P8-LEGAL-THIRD-PARTY` | 第三方 license/legal 人工审批 | 真实公开发布前，由有权负责人或合格法律审查者对精确候选制品和 notice/license 闭集作出书面审批 |
| `P8-DOCS-CLOSEOUT` | Phase 8 状态文档收口 | 前两项通过后更新 roadmap、security、README、验收证据和对应文档测试 |

任何第四个 `DEFERRED` 都是结构错误。延期不是 `PASS`，也不能让 Phase 8、Phase 9 或整个产品被标为完成。

## 3. 范围

### 3.1 本批次包含

- 枚举 Phase 1–9 权威规格中的发布、验收、安全、E2E、故障注入和性能要求；
- 为每项要求分配稳定 gate ID；
- 记录验证命令、CI workflow/job、artifact 与人工记录要求；
- 定义闭集证据回执并绑定精确候选提交；
- 离线计算 `PASS`、`MISSING`、`FAILED`、`DEFERRED`；
- 在线复核 GitHub Actions run、attempt、job 和 artifact 身份；
- 生成机器可读 JSON 与人类可读 Markdown 矩阵；
- 将校验纳入根 `pnpm verify`；
- 以独立只读 workflow 生成 Phase 9 审计 artifact。

### 3.2 本批次不包含

- 修改产品运行时、协议、Service、Code-OSS Extension 或 UI；
- 改动 `foundation.yml`、`release-inputs.yml`、签名或发布行为；
- 执行正式 Windows 签名；
- 代替第三方 license/legal 人工审批；
- 创建 tag、GitHub Release 或 Gitee 发布；
- 在 Batch A 中补齐发现的性能、故障注入或上游回归缺口；这些缺口进入后续独立批次。

## 4. 架构与文件边界

### 4.1 门禁注册表

新增 `tools/phase9/gates.json`，作为门禁定义的唯一事实源。根对象使用闭集字段：

```json
{
  "schemaVersion": 1,
  "product": "unit-test-ide",
  "repository": "colayc/unitTest",
  "allowedDeferredGateIds": [
    "P8-DOCS-CLOSEOUT",
    "P8-LEGAL-THIRD-PARTY",
    "P8-SIGN-WINDOWS"
  ],
  "sources": [],
  "gates": []
}
```

`sources` 显式列出权威规格的仓库相对路径和受覆盖章节。每个章节必须被至少一个 gate 引用。章节引用使用稳定标题文本，不使用易漂移的行号。

每个 gate 使用闭集字段：

```json
{
  "id": "P9-PERF-DISCOVERY-10000",
  "phase": 9,
  "category": "performance",
  "title": "10,000 item discovery baseline",
  "requirementRefs": [
    {
      "source": "docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md",
      "section": "Phase 9：完整矩阵、性能与发布资格确认"
    }
  ],
  "disposition": "required",
  "verification": {
    "commands": [],
    "workflowPath": ".github/workflows/phase9-gates.yml",
    "jobs": [],
    "artifacts": []
  }
}
```

`commands` 只用于显示和审计映射。任何校验器或 workflow 都不得把注册表中的字符串传给 shell。CI 只执行 workflow 中静态声明的命令。

延期 gate 额外包含闭集 `deferment`：

```json
{
  "reason": "deferred until a real public release",
  "resumeCondition": "must pass before creating a production release"
}
```

`disposition=deferred` 仅对三个固定 ID 合法。固定列表同时由 Schema 后校验逻辑和单元测试锁定，不能只修改数据扩大范围。

### 4.2 证据基线与回执

新增：

```text
docs/superpowers/evidence/phase9/baseline.json
docs/superpowers/evidence/phase9/receipts/*.json
```

`baseline.json` 指定当前被评估的精确候选提交、启用的回执 ID，以及闭集 `evaluationMode`：

- `historical`：只陈述该历史候选提交已经取得的证据；允许在 Batch A 实现提交上重放这些结论，但无条件保持 `releaseReady=false`；
- `candidate`：用于判断当前发布候选；当前提交必须与候选提交完全相同，或仅包含 `docs/superpowers/evidence/phase9/` 下的证据记录变更。

它不包含秘密、机器路径或大型 artifact。Batch A 的初始基线使用 `historical`，因为现有 producer/foundation 回执绑定的是 Batch A 实现之前的 `1e20a5ceee370e9823f9fe05f40980f893cd9ed3`。

自动化证据回执使用以下闭集结构：

```json
{
  "schemaVersion": 1,
  "receiptId": "github-actions-34731651809-1",
  "candidateCommit": "1e20a5ceee370e9823f9fe05f40980f893cd9ed3",
  "observedAt": "2026-09-13T02:19:28.000Z",
  "gateIds": [],
  "evidence": {
    "kind": "github-actions",
    "repository": "colayc/unitTest",
    "workflowPath": ".github/workflows/foundation.yml",
    "runId": "34731651809",
    "runAttempt": 1,
    "event": "workflow_dispatch",
    "headSha": "1e20a5ceee370e9823f9fe05f40980f893cd9ed3",
    "conclusion": "success",
    "jobs": [],
    "artifacts": []
  }
}
```

每个 job 精确记录 `name` 与 `conclusion`。每个 artifact 精确记录 canonical decimal `id`、`name`、去掉 `sha256:` 前缀后的 64 位小写 `digest`，以及捕获时的 `expired=false`。

人工审批证据使用独立 `kind=manual-approval`。它必须绑定仓库内审批文档的相对路径、SHA-256、审批时间、审批角色与闭集 decision；不能只写自由文本“已批准”。当前 legal gate 仍是延期，因此 Batch A 不生成虚假的人工审批回执。

### 4.3 候选提交与回执提交

回执永远绑定执行测试的 `candidateCommit`，而不是后来添加回执的提交。回执提交必须是候选提交的后代。

对于候选之后的变更：

- `historical` 模式可保留原候选的历史 `PASS`，但必须清楚标记为历史结论，并无条件保持 `releaseReady=false`；
- `candidate` 模式下，仅修改 `docs/superpowers/evidence/phase9/` 下的回执、基线和生成矩阵时，证据仍可用于原候选；
- `candidate` 模式下，产品源码、既有测试、producer、foundation、Phase 9 实现或其他 workflow 发生变化时，旧证据不能成为当前候选的 `PASS`；
- Batch A 自身的 schema、validator、renderer 和只读审计 workflow 由其自己的 CI 验证，不能反向改写已经记录的历史产品结论。

矩阵始终显示 `evaluationMode`、`candidateCommit` 与生成矩阵的仓库提交，防止把“历史测试对象”“当前发布候选”和“记录证据的提交”混为一谈。只有 `candidate` 模式且全部门禁为 `PASS` 时，`releaseReady` 才可能为 `true`。

### 4.4 离线校验与渲染

新增：

```text
tools/phase9/gates.schema.json
tools/phase9/validate.mjs
tools/phase9/render.mjs
tools/phase9/validate.test.mjs
tools/phase9/audit.mjs
tools/phase9/audit.test.mjs
```

`validate.mjs` 完全离线运行，负责：

- 限制输入字节数后再解析 JSON；
- 检查闭集 Schema 与精确语义；
- 验证 ID、digest、commit、UTC ISO 和安全相对路径；
- 检测重复 gate、receipt、source section 与冲突证据；
- 检查每个 source section 至少映射一个 gate；
- 检查恰好三个固定延期 gate；
- 将没有有效回执的 required gate 计算为 `MISSING`；
- 将失败回执或本地不一致计算为 `FAILED`；
- 输出确定性、按 gate ID 排序的矩阵模型。

`render.mjs` 从校验后的模型生成：

```text
docs/superpowers/evidence/phase9/gate-matrix.json
docs/superpowers/evidence/phase9/gate-matrix.md
```

生成内容不包含当前机器路径、用户名、随机数或渲染时钟。`--check` 模式逐字节检查已提交结果，发现手工修改或漂移时返回 `PHASE9_MATRIX_DRIFT`。

`audit.mjs` 不自行联网。在线 workflow 先对回执中的 canonical decimal ID 作离线校验，再通过固定 GitHub API 路径分别取得 run、job 与 artifact JSON 快照，最后把三类快照交给 `audit.mjs`。原始 GitHub API JSON 是有大小上限的临时输入；审计器只提取受信字段形成闭集内部对象，额外 API 字段不能影响身份判断。单元测试因此可以使用相同接口和完全离线的合成快照，不需要模拟另一套审计逻辑。

### 4.5 在线证据审计

新增 `.github/workflows/phase9-gates.yml`，在 `pull_request`、`master` push 和手工 dispatch 上执行。权限固定为：

```yaml
permissions:
  actions: read
  contents: read
```

workflow 不引用 repository、environment 或 organization secret，不接触签名材料，也不接受仓库名、workflow、run ID 或 shell 命令作为 dispatch 输入。

在线审计只读取 `baseline.json` 与已提交回执，固定查询 `colayc/unitTest`。对 `github-actions` 回执重新核验：

- repository、workflow path、event、head SHA；
- run ID、run attempt、status 和 conclusion；
- 每个要求的 job 名与 conclusion；
- artifact ID、name、digest 和所属 run；
- 捕获时 `expired=false` 的记录是否完整。

审计上传机器可读 JSON 与人类可读 Markdown artifact。失败时允许上传结构有效的诊断报告，但 workflow 必须失败，报告不得包含部分 `PASS` 汇总结论。

## 5. 状态计算

每个 gate 的派生状态只能是：

| 状态 | 条件 |
|---|---|
| `PASS` | disposition 为 required，回执闭集有效、绑定候选，并记录了捕获时成功的在线复核；每次在线 audit 还必须与回执完全一致 |
| `MISSING` | required gate 没有完整证据，或没有满足其 job/artifact 要求 |
| `FAILED` | 测试结论失败、身份漂移、证据冲突、候选不匹配或在线复核失败 |
| `DEFERRED` | gate ID 是三个固定延期项之一，并包含固定格式的原因和恢复条件 |

总结果包含：

```json
{
  "catalogComplete": true,
  "releaseReady": false,
  "counts": {
    "pass": 0,
    "missing": 0,
    "failed": 0,
    "deferred": 3
  }
}
```

`catalogComplete=true` 只表示所有权威章节和门禁已被建模。`releaseReady=true` 必须同时满足 `evaluationMode=candidate`、`missing=0`、`failed=0`、`deferred=0`，因此当前 Batch A 的 `historical` 基线不可能把产品标为可正式发布。

仓库内生成矩阵表达最后一次已提交回执的记录状态；在线 workflow 产生的审计 artifact 是当次 CI 的权威复核结果。若在线结果与仓库矩阵不一致，workflow 失败并把对应 gate 输出为 `FAILED`，不能继续使用仓库中的历史 `PASS` 作为当次发布判断。

## 6. Artifact 过期与历史证据

GitHub Actions 大型 artifact 当前只保留一天。回执必须在 artifact 未过期时捕获并在线验证其 ID、name、digest、run attempt 与内容结构结果。

后续 artifact 变为 `expired=true` 时：

- 历史回执仍能证明曾观察到的测试事实；
- 矩阵必须显示 `artifactAvailability=expired`；
- 不因正常过期把历史测试事实自动改写为失败；
- 过期制品绝不能用于正式发布、签名或重新资格验证；
- 任何正式发布候选都必须运行新的 producer 和 foundation，产生可下载的新制品。

历史保留依赖 GitHub API 仍返回同一 artifact ID、name、digest 和 `expired=true`。若 API 不再返回该身份，在线审计无法复核，必须输出 `FAILED`；不得从日志文本或手工复制的摘要重建 `PASS`。如果当前 producer/foundation artifact 在 Batch A 实施前已经过期或消失，则现有 run 只能列为历史坐标，必须运行新的 producer/foundation 后再生成满足本设计的回执。

大型 MSIX、AppImage、Code-OSS runtime 和工具包不提交进 Git。仓库只保存小型、脱敏的证据回执、资格摘要和矩阵。

## 7. 需求覆盖方法

Batch A 从整体 roadmap 的“源设计规格覆盖索引”开始，逐份检查 Phase 1–9 权威规格中的：

- 验收标准、完成标准和成功条件；
- 安全边界与明确拒绝条件；
- 单元、契约、集成、E2E 和 native platform 要求；
- 故障注入、恢复、取消、超时和清理要求；
- 性能、规模和硬件基线要求；
- 发布、签名、许可、升级和回滚门禁。

每个受覆盖章节进入 `sources`，每项独立、可判定要求进入一个 gate。一个 gate 可以引用多个来源，但不同成功条件不能因为共享命令而合并成无法单独判断的“大门禁”。

初次矩阵允许出现 `MISSING`。遗漏要求不允许。Batch A 的评审重点是“目录完整且状态诚实”，不是通过修改状态隐藏缺口。

## 8. 失败处理与安全边界

稳定错误码为：

```text
PHASE9_GATE_SCHEMA_INVALID
PHASE9_GATE_MISSING
PHASE9_EVIDENCE_CONFLICT
PHASE9_EVIDENCE_UNTRUSTED
PHASE9_DEFERRED_NOT_ALLOWED
PHASE9_MATRIX_DRIFT
```

所有仓库控制的 JSON 输入采用大小上限、严格 UTF-8、重复 key 拒绝和闭集字段检查。临时 GitHub API 快照采用大小上限与严格 UTF-8，随后只抽取允许的受信字段进入闭集校验；未知原始 API 字段被忽略且不能覆盖受信字段。路径拒绝绝对路径、反斜杠、冒号、控制字符、空段、`.`、`..` 与规范化别名。错误消息只输出 gate/receipt ID 与稳定原因，不输出 API token、环境、用户名、runner root、PFX 或其他秘密。

注册表中的命令、路径、workflow 和 artifact 名永远被当作数据。在线 auditor 使用固定 API 路径构造规则，run/job/artifact ID 先通过规范十进制校验，不能注入 URL、shell 或 GitHub expression。

未知状态、额外字段、API 限流、网络错误、run 消失、attempt 改变、workflow 不匹配和 artifact digest 漂移全部 fail-closed。失败不会生成可被后续消费者误读为成功的资格文件。

## 9. 测试策略

### 9.1 单元与合成集成测试

`tools/phase9/validate.test.mjs` 与 `tools/phase9/audit.test.mjs` 使用临时目录和离线 GitHub API fixture 覆盖：

- 最小合法 registry、baseline、receipt 与矩阵；
- 第四个延期 gate 被拒绝；
- 缺失回执产生 `MISSING`；
- 失败 conclusion 和身份漂移产生 `FAILED`；
- run、attempt、commit、job、artifact ID/digest 任一不匹配；
- 重复 JSON key、额外字段、大小超限和非法路径；
- 重复 gate/receipt、一个 gate 的冲突回执；
- `historical` 基线可保留历史绑定但永远不能 release-ready；
- `candidate` 基线后只有 evidence 变更时允许保留候选绑定；
- `candidate` 基线后产品、Phase 9 实现或既有 workflow 变化时旧证据失效；
- artifact 过期显示不可下载但保留历史结果；
- renderer 字节级确定性与 `--check` 漂移失败；
- 错误输出不泄露路径、环境或 secret fixture。

### 9.2 Workflow 静态契约测试

扩展 `tools/workspace-smoke/workspace-smoke.test.mjs`，验证：

- 新 workflow 只读权限；
- 不引用 `secrets.*`；
- 不接受动态 repository、workflow、run ID 或命令输入；
- action 引用固定到审核过的完整提交 SHA；
- 离线校验先于在线 API 访问；
- 审计失败不会产生成功资格结论；
- artifact 上传有显式路径、名称、缺失失败与有限保留期。

### 9.3 根验证

根 `package.json` 新增聚焦脚本并纳入现有验证链：

```text
test:phase9-gates
check:phase9-gates
```

验收命令：

```powershell
node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
pnpm test:workspace
pnpm verify
```

这些命令不得访问网络。只有 `.github/workflows/phase9-gates.yml` 的在线审计 job 可以读取 GitHub Actions API。

## 10. 数据流

```text
roadmap + authoritative specs
        │
        ▼
tools/phase9/gates.json
        │
        ├── baseline.json
        └── receipts/*.json
                 │
                 ▼
       offline validate + render
                 │
       ┌─────────┴─────────┐
       ▼                   ▼
gate-matrix.json    gate-matrix.md
       │                   │
       └─────────┬─────────┘
                 ▼
       read-only GitHub API audit
                 │
                 ▼
        Phase 9 audit artifact
```

## 11. Batch A 实施顺序

1. 先以失败测试定义 Schema、三个延期 gate 和状态计算；
2. 实现 registry/baseline/receipt 的闭集解析与语义校验；
3. 实现确定性 renderer 与 drift check；
4. 建立权威 source section 清单和初始 gate 目录；
5. 为现有 producer/foundation 成功运行生成脱敏回执；
6. 实现离线 GitHub API fixture auditor；
7. 新增只读 `phase9-gates.yml` 并加入静态契约测试；
8. 运行聚焦测试和完整 `pnpm verify`；
9. 在候选提交上运行在线审计并保存 artifact 身份；
10. 评审初次矩阵，将 `MISSING` 按独立领域拆分为后续 Phase 9 批次。

## 12. Batch A 验收标准

Batch A 只有同时满足以下条件才完成：

1. 所有权威设计规格中的验收、安全、E2E、故障注入和性能章节都进入 source coverage；
2. 每个独立要求映射到稳定 gate ID、静态验证入口和证据策略；
3. 恰好三个 Phase 8 gate 为 `DEFERRED`；
4. 现有 producer/foundation 证据在仍可复核时形成闭集回执；若已过期或消失，则由绑定同一候选或新候选的全新成功运行替代；
5. 无证据项目显示为 `MISSING`，失败或漂移显示为 `FAILED`；
6. JSON 和 Markdown 矩阵由同一验证模型确定性生成；
7. 新 workflow 只读、无 secret、无动态命令，并上传审计报告；
8. 聚焦测试、workspace smoke 和完整 `pnpm verify` 通过；
9. `catalogComplete=true` 与 `releaseReady=false` 同时得到正确表达；
10. 没有修改产品运行时、producer、foundation、签名、tag 或 Release 行为。

## 13. 后续批次边界

Batch A 结束后，根据初次矩阵中的真实 `MISSING` 分组创建独立设计与实施计划。预期领域包括完整测试矩阵、故障注入、硬件性能基线和 Code-OSS 上游合并回归。每个领域独立评审、实现和生成证据，不在 Batch A 内预设其全部实现细节。

Phase 8 的三个延期项可与这些批次并行保持打开，但在它们全部通过并完成文档收口前，任何 Phase 9 报告都必须维持 `releaseReady=false`，且不得创建生产 Release。
