# Phase 6 Code-OSS Extension 首个 Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 构建一个可在 `Extension Development Host` 中运行的独立 `Code-OSS Extension`，在 trusted 单根 workspace 中安全启动并连接现有 Go `unit-test-service`，在 untrusted 或 multi-root workspace 中完全拒绝执行，并提供 `workspace/inspect` 命令。

**Architecture:** 新增 `apps/code-oss-extension` workspace package。Extension Host 负责 `Workspace Trust`、Service lifecycle、命令和状态反馈；Go Service 继续负责 workspace/CMake/test domain；通信复用 `@unit-test-ide/test-client`、Windows Named Pipe 和 Linux Unix Socket。首个 slice 不 vendoring Code-OSS、不实现 Testing API UI。

**Tech Stack:** TypeScript 6.0.3、Node.js 24.18.0、pnpm 11.4.0、Code-OSS Extension API、现有 `@unit-test-ide/test-client`、Node `child_process`/`fs/promises`/`net`、Go `unit-test-service`。

## Global Constraints

- 最低运行环境保持 Node.js `24.18.0`、pnpm `11.4.0`、Go `1.26.5`。
- 只支持 trusted 单根 workspace；无 workspace 或 multi-root workspace 不启动 Service。
- untrusted workspace 不创建 token、endpoint、data directory，不 spawn Go Service。
- Windows 使用 per-user Named Pipe；Linux 使用 owner-only Unix Socket。
- Service 启动必须依次完成 `--prepare-token-file`、`READY <endpoint>`、token `handshake`、`capabilities/get`。
- token、完整环境变量、原始命令行和敏感绝对路径不得写入日志或错误消息。
- Extension 复用 `@unit-test-ide/test-client`，不复制 JSON envelope、schema validation 或 Go internal package。
- Protocol request 不暴露 executable、raw args、environment 或 cwd。
- 每个任务必须先写 failing test，再实现最小代码，再运行 focused test，最后单独 commit。
- 所有 Markdown 文档使用中文，`Code-OSS`、`Extension Host`、`Workspace Trust`、`Protocol Client`、`Named Pipe`、`Unix Socket` 等技术名词保留英文。
- 每个完成的提交都必须同步推送到 GitHub 和 Gitee；推送前执行 `git diff --check` 和对应 focused/full tests。

---

### Task 1: 建立 Extension workspace package 与 manifest contract

**Files:**
- Create: `apps/code-oss-extension/package.json`
- Create: `apps/code-oss-extension/tsconfig.json`
- Create: `apps/code-oss-extension/src/contracts.ts`
- Create: `apps/code-oss-extension/test/manifest.test.ts`
- Modify: `pnpm-workspace.yaml`

**Interfaces:**
- Produces legal Code-OSS manifest name `code-oss-extension`；与 publisher `unit-test-ide` 组成 Extension ID `unit-test-ide.code-oss-extension`。
- Produces `ExtensionState`, `TrustState`, `ServiceState` 和 `ServiceStatus` 类型，后续 Task 只能依赖这些稳定类型。

- [ ] **Step 1: Write the failing manifest and contract tests**

在 `test/manifest.test.ts` 写真实断言：

```ts
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import test from "node:test";

const root = resolve(dirname(import.meta.dirname), "..");

test("extension manifest declares workspace extension and safe commands", async () => {
  const manifest = JSON.parse(await readFile(resolve(root, "package.json"), "utf8")) as Record<string, unknown>;
  assert.equal(manifest.name, "code-oss-extension");
  assert.equal(manifest.main, "./dist/src/extension.js");
  assert.deepEqual(manifest.extensionKind, ["workspace"]);
  const contributes = manifest.contributes as { commands: Array<{ command: string }> };
  assert.deepEqual(
    contributes.commands.map((command) => command.command),
    ["unitTestIde.startService", "unitTestIde.stopService", "unitTestIde.inspectWorkspace"]
  );
});

test("contracts expose explicit lifecycle states", async () => {
  const contracts = await import("../dist/src/contracts.js");
  assert.deepEqual(contracts.TRUST_STATES, ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"]);
  assert.deepEqual(contracts.SERVICE_STATES, ["stopped", "starting", "running", "stopping", "failed"]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```powershell
pnpm --filter code-oss-extension test
```

Expected: FAIL because the workspace package, manifest and contract module do not exist.

- [ ] **Step 3: Add the package to the pnpm workspace**

Modify `pnpm-workspace.yaml` to add the explicit application path without broadening unrelated packages:

```yaml
packages:
  - "packages/*"
  - "tools/service-probe"
  - "apps/code-oss-extension"
```

- [ ] **Step 4: Add the manifest and TypeScript project**

`apps/code-oss-extension/package.json` must contain:

```json
{
  "name": "code-oss-extension",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "main": "./dist/src/extension.js",
  "engines": { "vscode": ">=1.90.0" },
  "activationEvents": ["onStartupFinished"],
  "extensionKind": ["workspace"],
  "contributes": {
    "commands": [
      { "command": "unitTestIde.startService", "title": "Unit Test: Start Service" },
      { "command": "unitTestIde.stopService", "title": "Unit Test: Stop Service" },
      { "command": "unitTestIde.inspectWorkspace", "title": "Unit Test: Inspect Workspace" }
    ],
    "configuration": {
      "title": "Unit Test IDE",
      "properties": {
        "unitTestIde.serviceExecutable": { "type": "string", "default": "" },
        "unitTestIde.serviceStartupTimeoutMs": { "type": "number", "default": 10000, "minimum": 1000, "maximum": 60000 },
        "unitTestIde.autoStart": { "type": "boolean", "default": true }
      }
    }
  },
  "dependencies": { "@unit-test-ide/test-client": "workspace:*" },
  "devDependencies": {
    "@types/node": "24.13.3",
    "@types/vscode": "1.105.0",
    "typescript": "6.0.3"
  },
  "scripts": {
    "build": "tsc -b tsconfig.json",
    "test": "pnpm run build && node --test dist/test/*.test.js"
  }
}
```

`tsconfig.json` 必须使用 `../../tsconfig.base.json`，`rootDir` 为项目根目录，`outDir` 为 `dist`，`types` 包含 `node` 和 `vscode`，并引用 `../../packages/test-client/tsconfig.json`。

- [ ] **Step 5: Define explicit contracts**

`src/contracts.ts` 定义并导出：

```ts
export const TRUST_STATES = ["no-workspace", "blocked-untrusted", "blocked-multi-root", "trusted"] as const;
export type TrustState = typeof TRUST_STATES[number];
export const SERVICE_STATES = ["stopped", "starting", "running", "stopping", "failed"] as const;
export type ServiceState = typeof SERVICE_STATES[number];
export interface ServiceStatus { state: ServiceState; detail?: string }
```

- [ ] **Step 6: Run focused tests and build**

Run:

```powershell
pnpm --filter code-oss-extension test
pnpm --filter code-oss-extension build
```

Expected: manifest and contract tests PASS，TypeScript build PASS。

- [ ] **Step 7: Commit**

```powershell
git add -- pnpm-workspace.yaml apps/code-oss-extension
git diff --cached --check
git commit -m "feat: scaffold Code-OSS extension package"
```

完成后同步推送到 GitHub 和 Gitee。

### Task 2: 实现 Workspace Trust gate

**Files:**
- Create: `apps/code-oss-extension/src/trust-gate.ts`
- Create: `apps/code-oss-extension/test/trust-gate.test.ts`
- Modify: `apps/code-oss-extension/src/contracts.ts`

**Interfaces:**
- Consumes `TrustState` from `contracts.ts`。
- Produces `WorkspaceSnapshot`、`evaluateWorkspace(snapshot)`、`canStartService(state)` 和 `TrustGate`。

- [ ] **Step 1: Write failing tests**

```ts
import assert from "node:assert/strict";
import test from "node:test";
import { canStartService, evaluateWorkspace, TrustGate } from "../src/trust-gate.js";

test("no workspace and multi-root are not executable", () => {
  assert.equal(evaluateWorkspace({ folderCount: 0, isTrusted: false }), "no-workspace");
  assert.equal(evaluateWorkspace({ folderCount: 2, isTrusted: true }), "blocked-multi-root");
  assert.equal(canStartService("blocked-multi-root"), false);
});

test("untrusted single-root never becomes executable", () => {
  assert.equal(evaluateWorkspace({ folderCount: 1, isTrusted: false }), "blocked-untrusted");
  assert.equal(canStartService("blocked-untrusted"), false);
});

test("trust transition emits trusted only for one trusted folder", () => {
  const gate = new TrustGate();
  assert.equal(gate.update({ folderCount: 1, isTrusted: false }), "blocked-untrusted");
  assert.equal(gate.update({ folderCount: 1, isTrusted: true }), "trusted");
  assert.equal(gate.update({ folderCount: 0, isTrusted: false }), "no-workspace");
});
```

- [ ] **Step 2: Run the focused tests and verify failure**

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "workspace|trust"`。

Expected: FAIL because `trust-gate.ts` is missing.

- [ ] **Step 3: Implement the pure gate**

Implement exact types:

```ts
export interface WorkspaceSnapshot { folderCount: number; isTrusted: boolean }
export function evaluateWorkspace(snapshot: WorkspaceSnapshot): TrustState {
  if (snapshot.folderCount === 0) return "no-workspace";
  if (snapshot.folderCount !== 1) return "blocked-multi-root";
  return snapshot.isTrusted ? "trusted" : "blocked-untrusted";
}
export function canStartService(state: TrustState): state is "trusted" { return state === "trusted"; }
```

`TrustGate` 必须保存上一次状态，并在重复 update 时不产生重复 transition；它不调用 VS Code API，不执行进程，也不创建文件。

- [ ] **Step 4: Add transition tests**

补充测试验证：`trusted -> blocked-untrusted`、`trusted -> blocked-multi-root`、`failed` 不能由 gate 直接产生，以及 listener 只在状态变化时触发一次。

- [ ] **Step 5: Run tests**

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "workspace|trust"`。

Expected: all Trust gate tests PASS。

- [ ] **Step 6: Commit**

```powershell
git add -- apps/code-oss-extension/src/contracts.ts apps/code-oss-extension/src/trust-gate.ts apps/code-oss-extension/test/trust-gate.test.ts
git diff --cached --check
git commit -m "feat: enforce Code-OSS workspace trust gate"
```

完成后同步推送到 GitHub 和 Gitee。

### Task 3: 实现跨平台 ServiceManager

**Files:**
- Create: `apps/code-oss-extension/src/service-manager.ts`
- Create: `apps/code-oss-extension/src/service-resources.ts`
- Create: `apps/code-oss-extension/test/service-manager.test.ts`
- Create: `apps/code-oss-extension/test/service-resources.test.ts`

**Interfaces:**
- Consumes `ServiceState`、`ServiceStatus` 和 `canStartService`。
- Produces `ServiceManagerOptions`、`ServiceManager`、`ServiceOperations` 和 `ServiceSession`。

`ServiceOperations` 必须允许测试替换外部状态：

```ts
export interface ServiceOperations {
  prepareTokenFile(binary: string, tokenFile: string, token: string): Promise<void>;
  spawnService(binary: string, args: string[]): ChildProcessWithoutNullStreams;
  connect(endpoint: string): Promise<ProtocolClient>;
}
export interface ServiceManagerOptions {
  serviceExecutable: string;
  workspaceRoot: string;
  dataDirectory: string;
  timeoutMs: number;
  trusted: () => boolean;
  operations?: Partial<ServiceOperations>;
}
```

`ServiceSession` 与 `ServiceManager` 的公开 contract 固定为：

```ts
export interface ServiceSession {
  readonly endpoint: string;
  readonly tokenFile: string;
  readonly sessionDirectory: string;
  readonly client: ProtocolClient;
}
export class ServiceManager {
  constructor(options: ServiceManagerOptions);
  get status(): ServiceStatus;
  get session(): ServiceSession | undefined;
  start(): Promise<ServiceSession>;
  stop(): Promise<void>;
  restart(): Promise<ServiceSession>;
}
```

- [ ] **Step 1: Write failing resource tests**

测试必须断言：

- Windows endpoint 匹配 `^\\\\\\.\\pipe\\unit-test-ide-<uuid>$` 且不创建 Unix directory；
- Linux endpoint 放在 owner-only 临时目录内，socket path 不超过 `sockaddr_un` 108-byte 限制；
- token 使用 `randomBytes(32).toString("base64url")`，不出现在返回的诊断字符串中；
- timeout error 只含命名操作和毫秒数，不含 token、workspace root 或 binary 完整路径。

- [ ] **Step 2: 写 failing lifecycle tests**

使用 fake child process、fake `ProtocolClient` 和可控 `EventEmitter`，覆盖：

1. start 顺序为 prepare → spawn → READY → connect → handshake → capabilities；
2. 非 trusted 时 `prepareTokenFile`、`spawnService`、`connect` 调用次数均为 0；
3. READY 超时、handshake 失败、capabilities 失败都进入 `failed` 并清理；
4. `stop()` 两次只关闭一次连接和子进程；
5. `restart` 生成不同 token、endpoint 和 client；
6. child exit/connection close 使状态进入 `failed`；
7. 诊断只保留脱敏 stdout/stderr。

- [ ] **Step 3: Run focused tests and verify failure**

Run:

```powershell
pnpm --filter code-oss-extension test -- --test-name-pattern "resource|lifecycle|service"
```

Expected: FAIL because `service-resources.ts` and `ServiceManager` are undefined。

- [ ] **Step 4: Implement platform resource helpers**

`service-resources.ts` 必须提供：

```ts
export interface EndpointResource { path: string; directory?: string }
export async function createEndpointResource(platform: NodeJS.Platform): Promise<EndpointResource>;
export async function createSessionDirectory(prefix: string): Promise<string>;
export function createToken(): string;
export function redactServiceError(error: unknown, sensitive: readonly string[]): Error;
```

Linux directory 使用 `mkdtemp` 后 `chmod 0o700`；Unix endpoint 文件最终由 Go `transport.Listen` 设置 `0o600`。Windows endpoint 使用 `\\.\pipe\unit-test-ide-${randomUUID()}`。不得用 `icacls /grant:r` 修复 ACL。

- [ ] **Step 5: Implement ServiceManager state machine**

`ServiceManager.start()` 必须拒绝 `trusted() === false`，并按以下伪代码实现：

```ts
if (!this.options.trusted()) throw new Error("workspace is not trusted");
const token = createToken();
const resources = await createSessionResources(...);
await withTimeout("token file preparation", prepareTokenFile(...));
const child = spawnService(binary, ["--endpoint", endpoint, "--token-file", tokenFile,
  "--data-dir", dataDirectory, "--workspace-root", workspaceRoot, "--trusted-workspace=true"]);
await withTimeout("service startup readiness", waitForReady(child.stdout, endpoint));
const client = await withTimeout("service connection", connect(endpoint));
await withTimeout("task protocol handshake", client.handshake(token, "code-oss-extension", version));
await withTimeout("service capabilities", client.getCapabilities());
```

实现必须保留 `ServiceSession` 中的 child、client、endpoint、tokenFile、sessionDirectory 和敏感值集合；任何失败路径都不把 token 写入异常。`stop()` 先调用 `client.shutdown()`（失败时继续关闭），再 `client.close()`，等待 child exit，最后清理资源。

- [ ] **Step 6: Run focused tests**

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "resource|lifecycle|service"`。

Expected: resource/lifecycle tests PASS。

- [ ] **Step 7: Run race-like repeated lifecycle test**

在测试中连续执行 50 次 `start -> stop -> start -> stop`，断言无重复 cleanup、无旧 token 复用、无未处理 Promise rejection。

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "repeated|restart"`。

- [ ] **Step 8: Commit**

```powershell
git add -- apps/code-oss-extension/src/service-manager.ts apps/code-oss-extension/src/service-resources.ts apps/code-oss-extension/test/service-manager.test.ts apps/code-oss-extension/test/service-resources.test.ts
git diff --cached --check
git commit -m "feat: manage trusted Code-OSS service lifecycle"
```

完成后同步推送到 GitHub 和 Gitee。

### Task 4: 接入 Extension activation、commands 和 Protocol adapter

**Files:**
- Create: `apps/code-oss-extension/src/protocol-client.ts`
- Create: `apps/code-oss-extension/src/commands.ts`
- Create: `apps/code-oss-extension/src/extension.ts`
- Create: `apps/code-oss-extension/test/extension.test.ts`

**Interfaces:**
- Consumes `TrustGate`、`ServiceManager`、`ServiceStatus`、`ProtocolClient`。
- Produces exported `activate(context: vscode.ExtensionContext): Promise<void>` 和 `deactivate(): Promise<void>`。

- [ ] **Step 1: Write failing command/activation tests**

测试使用最小 fake VS Code host，不导入真实 `vscode` runtime：

```ts
test("untrusted activation publishes blocked status and does not start service", async () => {
  const manager = new FakeServiceManager();
  const host = createExtensionHost({ folderCount: 1, isTrusted: false, manager });
  await host.activate();
  assert.equal(manager.startCalls, 0);
  assert.equal(host.statusText, "Unit Test: Untrusted Workspace");
});

test("inspect command delegates only to workspace/inspect", async () => {
  const client = new FakeProtocolClient({ workspace: { generation: "a" } });
  const host = createExtensionHost({ folderCount: 1, isTrusted: true, client });
  await host.activate();
  await host.execute("unitTestIde.inspectWorkspace");
  assert.equal(client.inspectCalls, 1);
  assert.equal(client.buildCalls, 0);
});
```

- [ ] **Step 2: Run tests and verify failure**

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "activation|command|inspect"`。

Expected: FAIL because activation, commands and protocol adapter do not exist。

- [ ] **Step 3: Implement protocol adapter**

`protocol-client.ts` 只封装现有 client：

```ts
export interface ExtensionProtocolClient {
  inspectWorkspace(): Promise<WorkspaceSnapshot>;
  close(): void;
}
export function createProtocolClient(endpoint: string): Promise<ProtocolClient>;
```

`createProtocolClient` 调用 `ProtocolClient.connect(endpoint)`，认证由 `ServiceManager` 完成；不得复制 `Connection`、AJV 或 envelope logic。

- [ ] **Step 4: Implement commands and status projection**

`commands.ts` 提供 `registerCommands(context, manager, clientProvider, output, status)`：

- `unitTestIde.startService` 调用 manager.start；
- `unitTestIde.stopService` 调用 manager.stop；
- `unitTestIde.inspectWorkspace` 只调用 `client.inspectWorkspace()`，JSON 摘要写入 Output Channel；
- 所有错误使用 `window.showErrorMessage`，内容来自 manager 的脱敏错误；
- untrusted、multi-root 和 no-workspace 状态的命令必须返回明确提示且不触发 manager。

- [ ] **Step 5: Implement activation/deactivation**

`extension.ts` 必须：

1. 从 `vscode.workspace.workspaceFolders` 和 `vscode.workspace.isTrusted` 构造 `WorkspaceSnapshot`；
2. 创建 `TrustGate`、`ServiceManager`、Output Channel 和 Status Bar Item；
3. 监听 `onDidChangeWorkspaceFolders` 与 `onDidGrantWorkspaceTrust`；
4. trusted 单根且 `unitTestIde.autoStart === true` 时启动 manager；
5. workspace close 时调用幂等 stop；Code-OSS trust revoke 通过 Extension Host reload/teardown 进入 `deactivate`，不依赖不存在的 revoke event；
6. 将 `stopped/starting/running/stopping/failed` 投影到规定状态栏文本；
7. 在 `deactivate` 中等待 manager.stop 的有限超时，不阻塞 Extension Host 无限等待。

- [ ] **Step 6: Run focused tests**

Run `pnpm --filter code-oss-extension test -- --test-name-pattern "activation|command|inspect"`。

Expected: activation/command tests PASS。

- [ ] **Step 7: Commit**

```powershell
git add -- apps/code-oss-extension/src/protocol-client.ts apps/code-oss-extension/src/commands.ts apps/code-oss-extension/src/extension.ts apps/code-oss-extension/test/extension.test.ts
git diff --cached --check
git commit -m "feat: connect Code-OSS commands to service protocol"
```

完成后同步推送到 GitHub 和 Gitee。

### Task 5: 真实 Service smoke、Extension Host 验收与文档接线

**Files:**
- Create: `apps/code-oss-extension/test/service-smoke.test.ts`
- Create: `apps/code-oss-extension/test/extension-host-smoke.mjs`
- Modify: `apps/code-oss-extension/package.json`
- Modify: `docs/development.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- Consumes final `ServiceManager`、`activate/deactivate` 和 existing Go Service binary。
- Produces package scripts `test:service-smoke`、`test:host` 以及 Phase 6 progress evidence。

- [ ] **Step 1: Write failing real-service smoke tests**

`service-smoke.test.ts` 必须：

- 从 `UNIT_TEST_IDE_SERVICE_BINARY` 读取 binary；未设置时用仓库 `build/unit-test-service(.exe)`；
- trusted fixture 启动真实 Service，等待 handshake/capabilities，执行 `inspectWorkspace`；
- untrusted fixture 在调用 `start` 前后断言 service spawn count 为 0、token/endpoint/data directory 不存在；
- 将 live trust state 置为 false 后显式调用 `controller.deactivate()`，模拟 Code-OSS trust-loss reload/teardown，并断言 child 退出且旧 endpoint 不可再次连接；不得伪造 revoke callback；
- Windows 与 Linux 使用同一测试 contract，platform-specific endpoint assertion 分别执行。

- [ ] **Step 2: Write failing Extension Host smoke harness**

`extension-host-smoke.mjs` 使用 `CODE_OSS_EXECUTABLE` 环境变量启动 Code-OSS/Code-OSS compatible executable，传入：

```text
--extensionDevelopmentPath=<repository>/apps/code-oss-extension
--extensionDevelopmentKind=workspace
<fixture-workspace>
```

生产 `activate()` 只在 activation 成功完成后输出固定、无敏感信息的 `UNIT_TEST_IDE_EXTENSION_ACTIVATED` marker；脚本等待该 marker。activation reject 不得输出 marker；没有 `CODE_OSS_EXECUTABLE` 时退出码为 0 并输出 `SKIP: CODE_OSS_EXECUTABLE is not configured`，不得伪造 PASS。

- [ ] **Step 3: Run RED smoke tests**

Run:

```powershell
pnpm --filter code-oss-extension test:service-smoke
pnpm --filter code-oss-extension test:host
```

Expected: service smoke 在未完成接线前 FAIL；host smoke 在无 executable 时只产生明确 SKIP。

- [ ] **Step 4: Implement real-service and host harness**

复用 pinned Go/Node runtime，不下载依赖，不调用 shell。Service smoke 的 spawn 参数必须与 Task 3 的精确顺序一致，并复用 `--prepare-token-file`；所有失败输出使用同一脱敏函数。Host harness 只验证 activation marker 和进程退出，不把真实 token 写入 artifact。

- [ ] **Step 5: Add scripts and docs**

`package.json` 增加：

```json
{
  "scripts": {
    "test:service-smoke": "pnpm run build && node --test dist/test/service-smoke.test.js",
    "test:host": "pnpm run build && node test/extension-host-smoke.mjs"
  }
}
```

`docs/development.md` 增加独立 Extension 的构建、`CODE_OSS_EXECUTABLE` host smoke、trusted/untrusted 验收命令；roadmap 将 Phase 6 首个 Vertical Slice 标记为已实现，并明确 Testing API UI 仍为后续子阶段。

- [ ] **Step 6: Run final verification**

在 pinned 环境运行：

```powershell
pnpm --filter code-oss-extension test
pnpm --filter code-oss-extension test:service-smoke
pnpm --filter code-oss-extension test:host
pnpm build
pnpm test:go
git diff --check
```

Windows 必须实际验证 Named Pipe；Linux 必须由 Linux CI 实际验证 Unix Socket。若当前主机缺少 Code-OSS executable，只允许 host smoke 明确 SKIP，并在报告中说明；不得将 cross-compile 当作 Linux runtime evidence。

- [ ] **Step 7: Commit and push both remotes**

```powershell
git add -- apps/code-oss-extension docs/development.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
git diff --cached --check
git commit -m "feat: deliver Code-OSS service lifecycle vertical slice"
git push github HEAD:codex/workspace-cmake-toolchains
git push origin HEAD:codex/workspace-cmake-toolchains
```

## Self-review checklist

- Spec §4 architecture → Task 1、Task 3、Task 4。
- Spec §5 Trust gate → Task 2、Task 4、Task 5。
- Spec §6 lifecycle/security → Task 3、Task 5。
- Spec §7 manifest/config/commands → Task 1、Task 4。
- Spec §8 Protocol boundary → Task 4。
- Spec §9 tests/acceptance → Task 2、Task 3、Task 5。
- Spec §10 follow-up/documentation → Task 5。
- Plan contains no unspecified tasks or unspecified error-handling steps。
- All cross-task types and method names are fixed before implementation。
