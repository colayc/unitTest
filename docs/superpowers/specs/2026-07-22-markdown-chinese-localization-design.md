# Markdown 文档中文化设计

**日期：** 2026-07-22

**状态：** 已批准

## 背景

当前分支跟踪 7 个 Markdown 文档，共约 3037 行。其中产品设计文档已经以中文为主，但 README、ADR、Superpowers 实施计划和安全设计规格仍包含大量英文叙述。项目面向中文开发与评审环境，需要将这些叙述统一为自然、准确的中文，同时不能破坏代码示例、命令、协议标识或自动化任务解析。

## 目标

- 将所有已跟踪 Markdown 文档中的叙述性内容统一为中文。
- 保持英文技术名词的原英文写法，例如 TypeScript、Go、Code-OSS、Windows、Linux、Named Pipe、DACL、SID、JSON Schema 和 E2E。
- 保持文档的技术含义、需求强度、任务顺序和验收条件不变。
- 保持代码块、命令、路径、文件名、参数、JSON 字段、错误码和版本号不变。
- 保持 Superpowers 计划中的自动化解析标记可用。

## 非目标

- 不重命名 Markdown 文件或目录。
- 不修改生产代码、测试代码、配置或依赖。
- 不改变产品架构、功能范围、安全策略或实施计划。
- 不把文档改成中英双语版本。
- 不翻译英文技术名词、产品名、工具名、协议名和 API 标识。

## 范围

需要中文化的现有文件：

1. `2026-07-18-code-oss-cpp-unit-test-ide-design.md`
2. `README.md`
3. `docs/decisions/0001-local-ipc-and-protocol-v1.md`
4. `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
5. `docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`
6. `docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md`
7. `docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md`

本设计文档及后续实施计划从创建时即使用中文，因此也满足“所有 Markdown 文档中文化”的要求。

## 方案比较

### 方案 1：语义中文化（采用）

按段落含义重写为自然中文，保留原有标题层级、列表、表格、代码和技术标识。该方案兼顾可读性与技术准确性，适合较长的产品设计和实施计划。

### 方案 2：逐句直译

逐句保持英文语序，修改过程更机械，但中文表达生硬，容易让需求强度和条件关系变得难以理解。

### 方案 3：中英双语

同时保留英文原文和中文译文，便于对照，但会使 3000 多行文档接近翻倍，不符合当前统一中文文档的目标。

## 翻译规则

### 需要翻译

- 文档标题、章节标题和小节标题中的叙述性文字。
- 段落、列表项、表格表头及单元格中的说明文字。
- 提示、注意事项、目标、架构、约束、验收条件和测试说明。
- 实施计划中的 `Run`、`Expected`、`Files`、`Interfaces` 等叙述标签。
- 代码块之外的普通英文句子。

### 必须保持原样

- 所有 fenced code block 的内容，包括代码、Shell 命令、PowerShell 命令、JSON、输出样例和 Git 命令。
- 所有 inline code span 的内容。
- 文件名、相对路径、URL、分支名和 Git commit SHA。
- CLI 参数、环境变量、JSON 字段、Go/TypeScript 标识符和函数签名。
- 错误码、协议版本、语义版本号和工具链版本号。
- 英文技术名词、产品名、工具名、库名、协议名和缩写。

### 自动化敏感标记

Superpowers 的任务提取脚本依赖 `Task N` 标题格式。因此计划标题继续使用 `### Task N：中文描述`，不能把 `Task` 改成中文。清单步骤继续使用 `Step N` 标记，skill 名称（如 `superpowers:test-driven-development`）及状态值（如 `DONE`、`BLOCKED`）保持英文。

## 实施顺序

1. 在临时校验目录中记录每个现有 Markdown 文件的 fenced code block 内容和顺序。
2. 先中文化短文档：README、ADR 和安全设计规格。
3. 中文化产品设计与路线图。
4. 分任务中文化两份较长的实施计划，保持 `Task`、`Step`、代码块和验收命令不变。
5. 运行结构和内容校验，修复遗漏或格式问题。
6. 提交文档修改并推送到现有 Draft PR 分支。

## 验证

- 中文化前后逐文件比较 fenced code block 的数量、顺序和内容哈希，必须完全一致。
- 检查所有 Markdown 围栏成对闭合。
- 检查标题层级、列表结构和表格分隔行仍然有效。
- 检查 `Task N` 和 `Step N` 标记数量不变。
- 检查文件路径、URL、CLI 参数、错误码及版本号没有被翻译或改写。
- 扫描残留英文叙述；英文技术名词和自动化标记列入允许范围。
- 运行 `pnpm test:workspace`，确认 README 和文档契约测试仍然通过。
- 运行 `git diff --check`，确认没有空白字符或 Markdown 格式问题。
- 最终审查确认仅 Markdown 文件发生变化，技术语义与原文一致。

## 风险与控制

| 风险 | 控制措施 |
|---|---|
| 翻译代码或命令导致示例失效 | 对 fenced code block 和 inline code 做原样校验 |
| 翻译 `Task` 标记导致任务提取失败 | 保留 `Task N` 和 `Step N` 英文格式并比较数量 |
| 长文档遗漏英文叙述 | 分文件扫描并人工复核残留英文段落 |
| 中文表述改变需求强度 | 保留“必须”“不得”“应”“可以”等约束级别并做语义审查 |
| 重命名文件导致引用失效 | 不修改任何文件名或路径 |
