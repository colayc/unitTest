# Markdown 文档中文化实施计划

> **面向 agentic workers：** 必需的 sub-skill：使用 `superpowers:subagent-driven-development`（推荐）或 `superpowers:executing-plans` 按任务实施本计划。步骤使用 checkbox（`- [ ]`）语法跟踪；最终声明完成前必须使用 `superpowers:verification-before-completion`。

**目标：** 将仓库中所有已跟踪 Markdown 文档的叙述性内容统一为自然、准确的中文，同时保持英文技术名词及所有不可翻译的技术内容不变。

**架构：** 采用“不可变内容基线 + 分批语义中文化 + 分层验证”的文档迁移方式。先用临时 Node.js 校验器记录每个 Markdown 文件的 fenced code block、inline code、URL、自动化标记及结构序列，再按短文档、产品文档和两份长实施计划分批修改。临时校验资产放在已忽略的 `.superpowers/` 目录，不进入 Git；最终提交仅包含 Markdown 文件。

**技术栈：** Markdown、Node.js、PowerShell、Git、pnpm

---

## 全局约束

以下约束适用于全部任务：

- Git 跟踪内容只修改已跟踪的 `*.md` 文件，不修改生产代码、测试代码、配置、依赖或目录结构；允许在已忽略的 `.superpowers/markdown-localization/` 中创建不提交的临时校验资产。
- 不重命名任何文件、目录、标题锚点所依赖的技术标识或链接目标。
- 叙述性内容使用自然中文，不保留整句英文原文，不制作中英双语版本。
- TypeScript、Go、Code-OSS、Windows、Linux、MSVC、GCC、Clang、clang-cl、llvm-cov、Named Pipe、DACL、SID、JSON Schema、E2E、GitHub Actions、PowerShell、Node.js、pnpm 等技术名词保持英文格式。
- fenced code block、inline code、命令、路径、文件名、URL、参数、环境变量、JSON 字段、标识符、函数签名、错误码、版本号、branch 名和 commit SHA 保持原样。
- `Task N`、`Step N`、skill 名称以及 `DONE`、`BLOCKED` 等自动化敏感标记保持英文。
- 保持原文的需求强度；`must`、`must not`、`should`、`may` 分别按上下文稳定表达为“必须”“不得”“应”“可以”，不得弱化或扩大要求。
- 每批翻译后立即运行临时校验器和针对性人工复核，不把全部风险留到最终检查。

### 当前已跟踪 Markdown 文件

1. `2026-07-18-code-oss-cpp-unit-test-ide-design.md`
2. `README.md`
3. `docs/decisions/0001-local-ipc-and-protocol-v1.md`
4. `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
5. `docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`
6. `docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md`
7. `docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md`
8. `docs/superpowers/specs/2026-07-22-markdown-chinese-localization-design.md`
9. `docs/superpowers/plans/2026-07-22-markdown-chinese-localization-plan.md`

### 每批通用验证命令

在 worktree 根目录运行：

```powershell
node .superpowers/markdown-localization/verify-markdown.mjs verify
git diff --check
git status --short
```

预期结果：

- 校验器输出 `Markdown preservation verification passed.`。
- `git diff --check` 没有输出并以状态码 0 结束。
- `git status --short` 只列出当前任务允许修改的 Markdown 文件；`.superpowers/` 下的临时文件不出现。

### 每批人工复核方法

先列出当前批次的纯英文候选行：

```powershell
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" -g "*.md"
```

逐行判断：完整英文叙述必须中文化；仅由技术名词、路径、命令、代码标识、URL、`Task N`、`Step N` 或自动化状态组成的行可以保留。不得以“技术名词较多”为由保留完整英文句子。

---

### Task 1：建立不可变内容基线并中文化短文档

**文件：**

- 创建但不提交：`.superpowers/markdown-localization/verify-markdown.mjs`
- 创建但不提交：`.superpowers/markdown-localization/baseline.json`
- 修改：`README.md`
- 修改：`docs/decisions/0001-local-ipc-and-protocol-v1.md`
- 修改：`docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md`

**接口：**

- 输入：已批准的中文化设计、当前分支的全部已跟踪 Markdown 文件。
- 产出：不可变内容基线，以及完成中文化的 README、ADR 和安全设计规格；后续任务依赖此基线做保真校验。

- [ ] **Step 1：确认临时目录已被 Git 忽略**

运行：

```powershell
git check-ignore .superpowers/probe
```

预期输出包含 `.superpowers/probe`。如果没有输出，停止执行，不得把校验资产写入未忽略目录；先检查现有 `.gitignore`，并将临时目录调整到已有的忽略路径，不能为了本任务修改 `.gitignore`。

- [ ] **Step 2：创建临时 Markdown 保真校验器**

使用 `apply_patch` 创建 `.superpowers/markdown-localization/verify-markdown.mjs`，内容如下：

````javascript
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";

const mode = process.argv[2];
const baselinePath = resolve(".superpowers/markdown-localization/baseline.json");

function trackedMarkdownFiles() {
  return execFileSync("git", ["ls-files", "--", "*.md"], { encoding: "utf8" })
    .split(/\r?\n/u)
    .filter(Boolean)
    .sort();
}

function sha256(value) {
  return createHash("sha256").update(value, "utf8").digest("hex");
}

function scanMarkdown(file) {
  const source = readFileSync(file, "utf8").replace(/\r\n/gu, "\n");
  const lines = source.split("\n");
  const proseLines = [];
  const fences = [];
  const headingLevels = [];
  const listKinds = [];
  let activeFence = null;

  for (const line of lines) {
    if (activeFence) {
      activeFence.lines.push(line);
      const close = line.match(/^\s*(`+|~+)\s*$/u);
      if (close && close[1][0] === activeFence.marker[0] && close[1].length >= activeFence.marker.length) {
        fences.push(activeFence.lines.join("\n"));
        activeFence = null;
      }
      continue;
    }

    const open = line.match(/^\s*(`{3,}|~{3,})[^\n]*$/u);
    if (open) {
      activeFence = { marker: open[1], lines: [line] };
      continue;
    }

    proseLines.push(line);
    const heading = line.match(/^(#{1,6})\s/u);
    if (heading) headingLevels.push(heading[1].length);
    if (/^\s*[-+*]\s/u.test(line)) listKinds.push("unordered");
    if (/^\s*\d+\.\s/u.test(line)) listKinds.push("ordered");
  }

  if (activeFence) throw new Error(`${file}: unclosed fenced code block`);

  const prose = proseLines.join("\n");
  const inlineCode = [...prose.matchAll(/(`+)([^`\n]*?)\1/gu)].map((match) => match[0]);
  const urls = [...source.matchAll(/https?:\/\/[^\s)>]+/gu)].map((match) => match[0]);
  const automationMarkers = [...prose.matchAll(/\b(?:Task|Step)\s+\d+\b/gu)].map((match) => match[0]);

  return {
    fenceHashes: fences.map(sha256),
    inlineCode,
    urls,
    automationMarkers,
    headingLevels,
    listKinds,
  };
}

function snapshot() {
  return Object.fromEntries(trackedMarkdownFiles().map((file) => [file, scanMarkdown(file)]));
}

if (mode === "capture") {
  mkdirSync(dirname(baselinePath), { recursive: true });
  writeFileSync(baselinePath, `${JSON.stringify(snapshot(), null, 2)}\n`, "utf8");
  console.log(`Captured Markdown baseline at ${baselinePath}`);
} else if (mode === "verify") {
  const baseline = JSON.parse(readFileSync(baselinePath, "utf8"));
  const current = snapshot();
  if (JSON.stringify(current) !== JSON.stringify(baseline)) {
    for (const file of [...new Set([...Object.keys(baseline), ...Object.keys(current)])].sort()) {
      if (JSON.stringify(baseline[file]) !== JSON.stringify(current[file])) {
        console.error(`Mismatch: ${file}`);
      }
    }
    process.exitCode = 1;
  } else {
    console.log("Markdown preservation verification passed.");
  }
} else {
  console.error("Usage: node verify-markdown.mjs <capture|verify>");
  process.exitCode = 2;
}
````

该校验器故意不比较普通叙述文本，只比较本次迁移必须保持不变的技术内容和 Markdown 结构。任何 mismatch 都必须定位并恢复原值，不得通过重新执行 `capture` 覆盖基线来绕过失败。

- [ ] **Step 3：捕获基线并证明校验器可用**

运行：

```powershell
node .superpowers/markdown-localization/verify-markdown.mjs capture
node .superpowers/markdown-localization/verify-markdown.mjs verify
```

预期依次输出：

```text
Captured Markdown baseline at C:\codex_project\unitTest\.worktrees\foundation-protocol-service\.superpowers\markdown-localization\baseline.json
Markdown preservation verification passed.
```

- [ ] **Step 4：中文化 `README.md`**

保持项目名、命令和路径原样，中文化项目简介、目录说明、启动方式、测试方式和状态说明。标题可以改成中文叙述，但产品名及仓库名保持原样。确认用户按照中文说明仍能准确执行原有命令。

- [ ] **Step 5：中文化 ADR**

在 `docs/decisions/0001-local-ipc-and-protocol-v1.md` 中中文化标题、状态标签、背景、决策、影响和理由。保留 `ADR`、`Named Pipe`、`AF_UNIX`、`JSON Schema`、协议版本及 inline code 原样；不得改变已接受的架构决策。

- [ ] **Step 6：中文化安全设计规格**

在 `docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md` 中中文化问题陈述、目标、非目标、方案、平台行为、安全不变量、失败语义、测试策略和风险说明。保留 Windows API、POSIX、DACL、ACE、SID、`SYSTEM`、`Administrators`、error code 和函数/字段名原样。特别复核 Windows DACL 架构修复的含义没有退化成单纯依赖 `icacls /grant:r`。

- [ ] **Step 7：运行本任务验证**

运行通用验证命令，然后运行：

```powershell
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" README.md docs/decisions/0001-local-ipc-and-protocol-v1.md docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md
git diff -- README.md docs/decisions/0001-local-ipc-and-protocol-v1.md docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md
```

预期：没有未解释的完整英文叙述；diff 仅改变叙述文本，不改变 fenced code block、inline code、技术标识或架构语义。

- [ ] **Step 8：提交本任务**

```powershell
git add README.md docs/decisions/0001-local-ipc-and-protocol-v1.md docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md
git commit -m "docs: localize core Markdown guidance"
```

---

### Task 2：中文化产品设计与路线图

**文件：**

- 修改：`2026-07-18-code-oss-cpp-unit-test-ide-design.md`
- 修改：`docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**接口：**

- 输入：Task 1 建立的不可变内容基线，以及设计规格中已批准的平台与技术术语。
- 产出：完成中文化的产品设计文档和九阶段路线图；后续任务沿用其中的中文术语。

- [ ] **Step 1：中文化产品设计文档的残留英文叙述**

逐节审查 `2026-07-18-code-oss-cpp-unit-test-ide-design.md`。该文档已经以中文为主，重点中文化残留的英文标题、表头、标签和完整句子，不机械翻译 TypeScript、Go、Electron、Code-OSS、MSVC、GCC、Clang、clang-cl、llvm-cov 等技术名词。保持架构边界、平台矩阵、覆盖率策略和协议描述不变。

- [ ] **Step 2：中文化路线图的定位和仓库结构章节**

中文化 `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md` 的文档标题、agent 提示、目标、拆分理由和目标仓库结构。保持目录树、路径和技术名词原样。

- [ ] **Step 3：中文化路线图的九个阶段**

逐个中文化 `Phase 1` 至 `Phase 9` 的标题与说明。`Phase N` 作为路线图结构标记保持英文；每个阶段的范围、依赖、交付物和验收边界保持不变。

- [ ] **Step 4：中文化跨阶段规则和规格覆盖索引**

中文化 `Cross-phase rules` 与 `Source-specification coverage index`，保持规则强度、阶段引用、路径和技术标识不变。对照产品设计的 17 个章节，确认覆盖关系没有增删或错配。

- [ ] **Step 5：验证并人工复核**

运行通用验证命令，然后运行：

```powershell
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" 2026-07-18-code-oss-cpp-unit-test-ide-design.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git diff -- 2026-07-18-code-oss-cpp-unit-test-ide-design.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
```

预期：所有完整英文叙述已中文化；平台支持仍明确覆盖 Windows MSVC、Windows clang-cl/llvm-cov、Linux GCC 和 Linux Clang；不可变内容校验通过。

- [ ] **Step 6：提交本任务**

```powershell
git add 2026-07-18-code-oss-cpp-unit-test-ide-design.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git commit -m "docs: localize product design and roadmap"
```

---

### Task 3：中文化 foundation 实施计划的前半部分

**文件：**

- 修改：`docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`

**范围：** 文档开头、全局规则以及 `Task 1` 至 `Task 4`；以 `### Task 5` 标题为本任务的停止边界。

**接口：**

- 输入：Task 1 的不可变内容基线、Task 2 确立的术语，以及原计划前四个任务的代码与命令。
- 产出：完成中文化的 foundation 计划头部、全局规则及 `Task 1` 至 `Task 4`；Task 4 继续修改同一文件。

- [ ] **Step 1：记录任务标题基准**

运行：

```powershell
rg -n "^### Task [0-9]+" docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
```

预期得到 8 个按顺序排列的 `Task` 标题，编号为 1 至 8。

- [ ] **Step 2：中文化文档头部与全局规则**

中文化标题、agent 执行提示、目标、架构、技术栈、交付约束和通用测试规则。skill 名称、路径、命令及 code span 保持原样。

- [ ] **Step 3：中文化原计划的 `Task 1`**

中文化 polyglot workspace 的目标、文件职责、步骤、验证预期和提交说明。标题改为 `### Task 1：中文任务名`，步骤改为 `- [ ] **Step N：中文步骤名**`。保留全部代码块及 language tag 原样，不改 package 名、版本、命令或预期机器输出。

- [ ] **Step 4：中文化原计划的 `Task 2`**

中文化 protocol v1、contract validation、JSON Schema 和 generation check 的说明。协议类型、schema 约束、字段名和测试断言保持原样。

- [ ] **Step 5：中文化原计划的 `Task 3`**

中文化 TypeScript 与 Go capability model 生成任务的叙述。generator 行为、类型名、文件路径、签名、fixture 和测试期望保持原样。

- [ ] **Step 6：中文化原计划的 `Task 4`**

中文化 Go envelope、authenticated session dispatch 和错误映射任务的叙述。transport 接口、frame 语义、authentication 顺序、error code 和测试期望保持原样。

- [ ] **Step 7：验证前半部分及整份文档结构**

运行通用验证命令，然后运行：

```powershell
rg -n "^### Task [0-9]+" docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
git diff -- docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
```

预期：8 个 `Task` 标题仍完整且顺序不变；`Task 1` 至 `Task 4` 没有未解释的英文叙述；`Task 5` 之后尚未翻译的英文属于下一任务，不能在本任务中误判为遗漏。

- [ ] **Step 8：提交本任务**

```powershell
git add docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
git commit -m "docs: localize foundation plan tasks 1 through 4"
```

---

### Task 4：中文化 foundation 实施计划的后半部分

**文件：**

- 修改：`docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`

**范围：** `Task 5` 至 `Task 8` 以及文档结尾。

**接口：**

- 输入：Task 3 已中文化的同一计划前半部分、不可变内容基线和统一术语。
- 产出：完整中文化的 foundation 实施计划，供 secure token file 计划和最终审查引用。

- [ ] **Step 1：中文化原计划的 `Task 5`**

沿用前一任务的术语和格式，中文化 platform IPC 与 Go service executable 的叙述。Named Pipe、AF_UNIX、endpoint、permission、lifecycle、命令和测试期望保持原样。

- [ ] **Step 2：中文化原计划的 `Task 6`**

中文化 reusable TypeScript protocol client 的叙述。request dispatch、correlation、timeout、error mapping 与 protocol 行为不得改变。

- [ ] **Step 3：中文化原计划的 `Task 7`**

中文化 Windows/Linux E2E vertical slice 的叙述。extension/service integration、进程 lifecycle、日志、平台矩阵、环境变量和测试命令不得改变。

- [ ] **Step 4：中文化原计划的 `Task 8` 与文档结尾**

中文化 CI gates、developer handoff、README 示例、Phase 1 completion evidence 和 Version references 的叙述。CI YAML、README 示例代码块、验收命令、版本号和引用链接保持原样。

- [ ] **Step 5：做整份 foundation 计划的术语一致性复核**

从头到尾复核同一概念只使用一个中文表达；English technical term 保持原格式。重点检查“请求/响应”“传输”“帧”“握手”“认证”“权限”“调度”“生命周期”“验收”等术语，确保前后任务不存在含义漂移。

- [ ] **Step 6：验证整份文档**

运行通用验证命令，然后运行：

```powershell
rg -n "^### Task [0-9]+" docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
git diff HEAD^ -- docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
```

预期：8 个 `Task` 标题和全部 `Step` 标记仍可解析；不再存在完整英文叙述；代码及技术内容与基线一致。

- [ ] **Step 7：提交本任务**

```powershell
git add docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md
git commit -m "docs: localize remaining foundation plan"
```

---

### Task 5：中文化 secure token file 实施计划

**文件：**

- 修改：`docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md`

**接口：**

- 输入：Task 1 中文化的安全设计规格、Task 4 中文化的 foundation 计划和不可变内容基线。
- 产出：完整中文化且与已实施 DACL 架构修复一致的 secure token file 实施计划。

- [ ] **Step 1：记录任务标题基准**

运行：

```powershell
rg -n "^### Task [0-9]+" docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md
```

预期得到 5 个按顺序排列的 `Task` 标题，编号为 1 至 5。

- [ ] **Step 2：中文化文档头部、全局约束和文件映射**

中文化标题、agent 提示、目标、架构、技术栈、`Global Constraints` 和 `File Map`。路径、符号、platform 名称和版本保持原样。

- [ ] **Step 3：中文化原计划的 `Task 1`**

中文化 owner-only token file 原子创建的测试驱动步骤和平台实现说明。Windows API、POSIX API、SID、ACE、DACL、mode、函数名、错误码、测试名、代码和命令保持原样。

- [ ] **Step 4：中文化原计划的 `Task 2`**

中文化 preparation command mode 的输入、输出、失败语义和测试步骤。CLI 参数、JSON field、error code 和 command output 保持原样。

- [ ] **Step 5：中文化原计划的 `Task 3`**

中文化 TypeScript launcher 在写入 token 前调用 preparation mode 的数据流、失败清理和测试步骤。函数名、参数、环境变量、fixture、代码和命令保持原样。

- [ ] **Step 6：中文化原计划的 `Task 4`**

中文化安全准备契约的文档同步步骤。原计划引用的 README 与设计文档路径、验证命令和契约名称保持原样。

- [ ] **Step 7：中文化原计划的 `Task 5`**

中文化本地与 hosted acceptance gates、Windows/Linux 验证矩阵和完成条件。GitHub Actions 名称、runner label、命令和预期结果保持原样。

- [ ] **Step 8：复核安全不变量**

从头到尾确认下列安全语义明确且未弱化：

- Windows 必须通过显式 DACL 构造最终权限，而不是假设 `icacls /grant:r` 会删除全部显式 ACE。
- 允许的 Windows principals、继承控制、token 长度及失败清理策略保持原定义。
- Linux 的 owner-only mode、原子写入、rename 和 directory permission 语义保持原定义。
- 不支持的平台必须 fail closed，不能降级为普通文件写入。

保留 Windows API、POSIX API、`SYSTEM`、`Administrators`、SID、ACE、DACL、ACL、mode、所有函数名、错误码、测试名和命令原样。

- [ ] **Step 9：复核测试叙述与实现叙述一一对应**

逐个 `Task` 对照代码块前后的中文说明，确认“先写失败测试—观察预期失败—最小实现—观察通过—提交”的顺序仍然清楚。不能把 `Expected` 中的失败原因翻译成与代码块机器输出矛盾的说法。

- [ ] **Step 10：验证整份文档**

运行通用验证命令，然后运行：

```powershell
rg -n "^### Task [0-9]+" docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md
git diff -- docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md
```

预期：5 个 `Task` 标题和全部 `Step` 标记仍可解析；安全架构修复的约束清楚可见；不存在未解释的完整英文叙述；不可变内容校验通过。

- [ ] **Step 11：提交本任务**

```powershell
git add docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md
git commit -m "docs: localize secure token implementation plan"
```

---

### Task 6：执行全仓库最终验证并推送 Draft PR 分支

**文件：**

- 验证：全部已跟踪 `*.md`
- 不应修改：任何非 Markdown 文件

**接口：**

- 输入：Task 1 至 Task 5 的全部文档 commit、不可变内容基线和现有 Draft PR #1。
- 产出：本地与 hosted verification 证据、已推送的当前分支，以及干净的 worktree。

- [ ] **Step 1：确认所有 Markdown 文件仍在基线范围内**

运行：

```powershell
git ls-files -- "*.md"
node .superpowers/markdown-localization/verify-markdown.mjs verify
```

预期：文件列表与本计划“当前已跟踪 Markdown 文件”一致；校验器输出 `Markdown preservation verification passed.`。

- [ ] **Step 2：扫描残留英文叙述和占位符**

运行：

```powershell
rg -n "^[[:space:]#>*|+-]*[A-Za-z][A-Za-z0-9 ,.'():/+-]{20,}$" -g "*.md"
rg -n "TBD|TODO|FIXME|PLACEHOLDER|待翻译|稍后补充" -g "*.md"
```

逐项人工分类第一条命令的结果。允许技术名词、工具名、路径、URL、命令、代码标识及自动化标记；不允许完整英文叙述。第二条命令不得出现本次翻译新增的占位内容；原本位于不可变代码块或 inline code 中的匹配必须保持原样并记录为允许项。

- [ ] **Step 3：运行 workspace 回归测试**

运行：

```powershell
pnpm test:workspace
```

预期：命令以状态码 0 结束，README 契约、协议、extension 和 service 测试全部通过。

- [ ] **Step 4：检查 diff 完整性**

运行：

```powershell
git diff --check
git status --short
git diff --stat edb42df..HEAD
git diff --name-only edb42df..HEAD
```

预期：

- `git diff --check` 无输出。
- worktree 干净。
- 从中文化设计 commit 之后开始的变更只涉及本计划列出的 Markdown 文件。
- `.superpowers/markdown-localization/` 未出现在 Git diff 或 status 中。

如果最终复核产生小范围措辞修正，重新运行本任务全部验证后提交：

```powershell
git add -- 2026-07-18-code-oss-cpp-unit-test-ide-design.md README.md docs/decisions/0001-local-ipc-and-protocol-v1.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md docs/superpowers/plans/2026-07-21-secure-token-file-preparation-plan.md docs/superpowers/plans/2026-07-22-markdown-chinese-localization-plan.md docs/superpowers/specs/2026-07-21-secure-token-file-preparation-design.md docs/superpowers/specs/2026-07-22-markdown-chinese-localization-design.md
git commit -m "docs: complete Chinese Markdown localization"
```

如果没有额外修改，不创建空 commit。

- [ ] **Step 5：推送当前分支并核对 Draft PR**

运行：

```powershell
git push github codex/foundation-protocol-service
gh pr checks 1 --repo colayc/unitTest
```

预期：push 成功；Draft PR #1 的 checks 开始运行或显示通过。若 checks 尚未结束，等待完成后再次运行 `gh pr checks`；只有本地验证与 GitHub Actions 都通过时才能声明完成。

- [ ] **Step 6：清理临时校验资产**

确认最终验证证据已记录后，删除 `.superpowers/markdown-localization/`。该目录始终未被 Git 跟踪，删除后再次运行：

```powershell
git status --short
```

预期：无输出。

---

## 完成标准

- 所有已跟踪 Markdown 文档的叙述性内容均为中文。
- 英文技术名词保持原英文格式，没有被翻译成中文或改成中英括注。
- fenced code block、inline code、URL、`Task N`、`Step N` 和结构序列通过基线比较。
- 文件名、路径、命令、参数、字段、标识符、错误码和版本号未改变。
- Windows DACL 架构修复、Linux permission 行为及跨平台范围没有语义退化。
- `pnpm test:workspace`、`git diff --check` 和 GitHub Actions 全部通过。
- Git 只记录预期的 Markdown 修改，worktree 最终干净。
