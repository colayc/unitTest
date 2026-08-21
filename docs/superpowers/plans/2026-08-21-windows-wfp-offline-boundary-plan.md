# Windows WFP Offline Boundary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Windows 原生 WFP dynamic session guardian 替换 PowerShell 持久防火墙 guardian，为 verified LLVM coverage smoke 提供可审计、崩溃自动清理、无 marker 信任依赖的 outbound network boundary。

**Architecture:** `coverage-toolset-preflight` 先复用现有 `toolchain.NewWindowsAdapters` 与 `coveragellvm.PinToolset` 生成闭集 verified result；通过受控 IPC 启动独立 `native-offline-guardian.exe`，guardian 在 WFP dynamic session 中建立 IPv4/IPv6 ALE outbound block filters，并用 owner PID + creation time 验证生命周期。TypeScript adapter 在收到 ready 后才启动 Service/native coverage，关闭时等待 release、guardian exit 和 session close；旧 PowerShell 脚本只保留一次性 legacy cleanup。

**Tech Stack:** Go 1.26.6；`golang.org/x/sys/windows`；现有 `github.com/Microsoft/go-winio`；Windows WFP `fwpmu.dll`/`fwpuclnt.dll`；Node 24.18.0；pnpm 11.4.0；TypeScript 6.0.3；GitHub Actions Windows 2025 runner。

## Global Constraints

- WFP 管理权限必需；required CI 无法打开 WFP engine、创建 dynamic session 或完成 filter audit 时 FAIL。
- local/non-required 只有在 toolchain preflight 未通过且尚未创建 WFP session/filter 前可 SKIP；boundary 创建后任何失败均 FAIL。
- 不使用 marker、PID 文件、registry、PersistentStore firewall rule 或可写 state directory 作为安全依据。
- WFP filters 覆盖 `FWPM_LAYER_ALE_AUTH_CONNECT_V4` 与 `FWPM_LAYER_ALE_AUTH_CONNECT_V6`，不写 PersistentStore。
- Guardian ready 前不启动 Service/native coverage；`Close`/release 幂等且必须等待 guardian exit。
- Linux 编译必须通过并返回 `ErrUnsupported`，不执行 native side effect。
- 报告字段闭集且不得写绝对路径、命令行、token、环境变量或原始网络地址。
- 产品运行时不依赖 GitHub/Gitee；本计划只修改本地代码与 CI。

---

### Task 1: 建立 Go offlineboundary 接口与 Windows WFP dynamic session

**Files:**
- Create: `apps/test-service/internal/offlineboundary/model.go`
- Create: `apps/test-service/internal/offlineboundary/errors.go`
- Create: `apps/test-service/internal/offlineboundary/boundary.go`
- Create: `apps/test-service/internal/offlineboundary/boundary_nonwindows.go`
- Create: `apps/test-service/internal/offlineboundary/wfp_windows.go`
- Create: `apps/test-service/internal/offlineboundary/wfp_windows_test.go`
- Test: `apps/test-service/internal/offlineboundary/boundary_test.go`
- Modify: `apps/test-service/go.mod` only if an existing Windows syscall dependency is insufficient; prefer existing `golang.org/x/sys` and `github.com/Microsoft/go-winio`.

**Interfaces:**
- Produces `OfflineBoundary`, `Lease`, `OwnerIdentity`, `ErrUnsupported`, `ToolchainUnavailable`, `WFPAccessDenied`, `GuardianStartFailed`, `FilterAuditFailed`, `OwnerIdentityMismatch`, `GuardianTimeout`, `SessionCloseFailed`.
- `Start(context.Context, OwnerIdentity) (Lease, error)` must not spawn a process or open WFP on non-Windows builds.
- Internal Windows engine seam:

```go
type wfpEngine interface {
    AddOutboundBlockFilters(context.Context, []byte) error
    AuditOutboundBlockFilters(context.Context, []byte) error
    Close() error
}
```

- [ ] **Step 1: Write failing tests for the public contract**

```go
func TestStartRequiresOwnerIdentity(t *testing.T) {
    _, err := New(Config{}).Start(context.Background(), OwnerIdentity{})
    if !errors.Is(err, ErrOwnerIdentityMismatch) { t.Fatalf("error = %v", err) }
}

func TestNonWindowsBoundaryHasNoNativeSideEffect(t *testing.T) {
    if runtime.GOOS == "windows" { t.Skip("non-Windows contract") }
    _, err := New(Config{}).Start(context.Background(), OwnerIdentity{PID: 1, CreationTime: 1})
    if !errors.Is(err, ErrUnsupported) { t.Fatalf("error = %v", err) }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./apps/test-service/internal/offlineboundary -count=1`

Expected: FAIL because the package and `New`/error symbols do not yet exist.

- [ ] **Step 3: Implement the contract and non-Windows stub**

Define:

```go
type OwnerIdentity struct { PID uint32; CreationTime uint64 }
type OfflineBoundary interface { Start(context.Context, OwnerIdentity) (Lease, error) }
type Lease interface { Ready() <-chan struct{}; Close() error; Wait() error }
```

Validate `PID != 0` and `CreationTime != 0`; return `ErrUnsupported` from the non-Windows implementation before opening handles or starting children.

- [ ] **Step 4: Implement Windows WFP ABI wrapper**

Use lazy-loaded Windows procedures for `FwpmEngineOpen0`, `FwpmFilterAdd0`, `FwpmFilterGetByKey0`, `FwpmFilterDeleteByKey0`, and `FwpmEngineClose0`. Open a `FWPM_SESSION0` with `FWPM_SESSION_FLAG_DYNAMIC`, create one provider/sublayer identity per lease, add V4/V6 block filters at `FWPM_LAYER_ALE_AUTH_CONNECT_V4` and `FWPM_LAYER_ALE_AUTH_CONNECT_V6`, and retain the engine handle until `Close`.

Map access-denied status to `WFPAccessDenied`; map missing/extra/mismatched filters to `FilterAuditFailed`; never fall back to `netsh`, PowerShell, or PersistentStore.

- [ ] **Step 5: Add WFP seam tests and run GREEN**

Test exact V4/V6 keys, dynamic-session flag, block action, no persistent store calls, duplicate `Close`, audit rejection for missing/extra filters, and access-denied mapping. Run:

```bash
gofmt -w apps/test-service/internal/offlineboundary
go test ./apps/test-service/internal/offlineboundary -count=1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./apps/test-service/internal/offlineboundary
```

Expected: all focused tests PASS and Linux compile succeeds with `ErrUnsupported` behavior.

- [ ] **Step 6: Commit**

```bash
git add apps/test-service/internal/offlineboundary apps/test-service/go.mod apps/test-service/go.sum
git commit -m "feat: add Windows WFP offline boundary core"
```

### Task 2: Implement the independent guardian process and owner lifecycle

**Files:**
- Create: `apps/test-service/cmd/native-offline-guardian/main_windows.go`
- Create: `apps/test-service/cmd/native-offline-guardian/main_nonwindows.go`
- Create: `apps/test-service/internal/offlineboundary/protocol.go`
- Create: `apps/test-service/internal/offlineboundary/guardian_windows.go`
- Create: `apps/test-service/internal/offlineboundary/guardian_windows_test.go`
- Modify: `apps/test-service/internal/offlineboundary/boundary.go`

**Interfaces:**
- Guardian executable consumes `--owner-pid`, `--owner-creation-time`, and an inherited IPC handle; it never accepts a state-directory or firewall-rule path.
- IPC frames are `hello`, `ready`, `release`, `error`, `bye`; each frame is length-limited and validated against a closed schema.
- `startGuardian(ctx, OwnerIdentity) (Lease, error)` returns only after process creation; `Ready()` closes only after WFP filter audit.

- [ ] **Step 1: Write RED tests for guardian protocol and lifecycle**

Cover malformed/oversized frames, wrong message order, owner PID reuse, ready timeout, guardian crash, release timeout, repeated `Close`, and owner termination. Assert no marker/state files are consulted.

- [ ] **Step 2: Run focused RED**

Run: `go test ./apps/test-service/internal/offlineboundary -run 'Guardian|Protocol|Owner|Release' -count=1`

Expected: FAIL because guardian protocol/client symbols are missing.

- [ ] **Step 3: Implement strict protocol and Windows process start**

Use `go-winio` or an inherited anonymous pipe for local IPC. Parent passes an owner creation-time value obtained from a Windows process handle. Guardian opens WFP, rechecks owner identity before filter creation, sends `ready`, then waits for `release` or owner disappearance. It closes the dynamic session before `bye`; parent waits for both `bye` and process exit.

- [ ] **Step 4: Add failure-safe lifecycle behavior**

On any pre-ready error, guardian sends one fixed reason code and closes the session. On release failure, parent keeps the in-process HTTP guard installed and returns `SessionCloseFailed`; no retry may start another guardian. Repeated `Close` returns the original result.

- [ ] **Step 5: Run Windows focused and Linux compile gates**

```bash
go test ./apps/test-service/internal/offlineboundary -run 'Guardian|Protocol|Owner|Release' -count=1
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./apps/test-service/cmd/native-offline-guardian
go vet ./apps/test-service/internal/offlineboundary ./apps/test-service/cmd/native-offline-guardian
```

- [ ] **Step 6: Commit**

```bash
git add apps/test-service/internal/offlineboundary apps/test-service/cmd/native-offline-guardian
git commit -m "feat: add WFP guardian process lifecycle"
```

### Task 3: Replace the TypeScript PowerShell boundary adapter

**Files:**
- Modify: `tools/service-probe/src/native-network-guard.ts`
- Modify: `tools/service-probe/src/native-network-guard.test.ts`
- Create: `tools/service-probe/src/wfp-offline-boundary.ts`
- Create: `tools/service-probe/src/wfp-offline-boundary.test.ts`
- Modify: `tools/service-probe/src/coverage-bundle.ts` only where the coverage smoke sequencing invokes the boundary.
- Modify: `tools/service-probe/package.json` if a `build:guardian` or test script is required.

**Interfaces:**
- `installWindowsNativeOfflineBoundary(options): Promise<WindowsNativeOfflineBoundary>` remains the public adapter signature so existing smoke callers do not change.
- New internal adapter invokes `coverage-toolset-preflight` first, then `native-offline-guardian.exe`; it exposes `close(): Promise<void>` and preserves HTTP(S) guard restoration.
- Local unavailable result is `{ outcome: "skipped", reason: "ToolchainUnavailable" }` only before guardian start; required mode throws.

- [ ] **Step 1: Write RED tests for sequencing and side effects**

Add tests asserting: unavailable preflight performs zero guardian/process calls; WFP denied after verified preflight fails; ready is required before Service launch; release waits for `bye` and process exit; HTTP guard remains installed after close failure; malformed guardian frames fail closed.

- [ ] **Step 2: Run service-probe RED**

Run: `pnpm --filter @unit-test-ide/service-probe build`

Expected: FAIL with missing WFP adapter/operations symbols.

- [ ] **Step 3: Implement the adapter**

Use `execFile` for the Go preflight and guardian binary with sanitized environment. Parse only the closed JSON/protocol schema; reject paths, command lines, extra keys and unknown reason codes. Keep `installNativeHttpNetworkGuard()` as a complement, not a fallback for WFP.

- [ ] **Step 4: Remove production PowerShell start path**

Delete the default PowerShell guardian launch from `native-network-guard.ts`. Keep the legacy script reference only in a one-time cleanup helper used by CI migration, never by the coverage execution path.

- [ ] **Step 5: Run TypeScript focused and existing smoke tests**

```bash
pnpm --filter @unit-test-ide/service-probe build
node --test tools/service-probe/dist/wfp-offline-boundary.test.js tools/service-probe/dist/native-network-guard.test.js
pnpm --filter @unit-test-ide/service-probe test:coverage-bundle
```

- [ ] **Step 6: Commit**

```bash
git add tools/service-probe/src tools/service-probe/package.json
git commit -m "feat: switch coverage smoke to WFP guardian"
```

### Task 4: Migrate CI, reports, and legacy cleanup

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/service-probe/scripts/windows-offline-boundary.ps1` to a legacy cleanup-only entry point, or delete it after its callers are removed.
- Modify: `tools/service-probe/src/coverage-bundle.ts` and relevant tests for closed WFP report fields.
- Create: `tools/service-probe/testdata/wfp-offline-report.valid.json`
- Create: `tools/service-probe/testdata/wfp-offline-report.invalid.json`
- Modify: `README.md` and `docs/superpowers/roadmaps/2026-08-03-phase5-coverage-bundle-roadmap.md` with Chinese operational notes and retained English technical names.

**Interfaces:**
- Windows required job invokes preflight → WFP guardian smoke → Service/native coverage; no unconditional PowerShell cleanup step.
- Linux job invokes only unsupported/native contract checks for WFP and does not create Windows boundary state.
- Artifact report keeps the closed schema from the design: schema/version, outcome, reason, toolchain digest, guardian outcome, filter audit outcome, timestamps.

- [ ] **Step 1: Write RED CI/report tests**

Assert workflow text contains the preflight-before-guardian ordering, required toolchain mode, no production `-Action Guard`, and no artifact path containing command line/token/state-root data. Assert invalid report fields are rejected.

- [ ] **Step 2: Run RED**

Run: `pnpm --filter @unit-test-ide/service-probe build` and the new report test. Expected: missing WFP workflow/report contract causes failures.

- [ ] **Step 3: Update workflow and legacy path**

Replace the Windows cleanup step with a bounded legacy cleanup invocation that only removes known historical PowerShell rule groups after read-only audit. Unknown state or audit failure exits nonzero. Add `if: always()` artifact upload for the closed report, with `if-no-files-found: error` only after a required verified run.

- [ ] **Step 4: Run CI contract and local unavailable controls**

```bash
pnpm --filter @unit-test-ide/service-probe build
pnpm --filter @unit-test-ide/service-probe test
pnpm test:workspace
```

On a machine without verified clang-cl, expected native coverage result is exactly one local SKIP with no WFP session; required control is exactly one FAIL. Do not convert either result into PASS.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/foundation.yml tools/service-probe README.md docs/superpowers/roadmaps
git commit -m "ci: migrate Windows coverage boundary to WFP"
```

### Task 5: Privileged Windows integration, full verification, and handoff

**Files:**
- Create: `apps/test-service/internal/offlineboundary/integration_windows_test.go`
- Create: `tools/service-probe/src/wfp-offline-boundary.e2e.test.ts`
- Modify: `.github/workflows/foundation.yml` for the required privileged test command if Task 4 does not already add it.
- Modify: `docs/superpowers/sdd/2026-08-20-phase8-windows-llvm-coverage-execution-plan/task-9-report.md` with migration evidence.

**Interfaces:**
- Integration tests use the real WFP engine only on a privileged Windows runner; they never skip an access-denied result in required mode.
- Linux and unprivileged Windows tests use explicit `ErrUnsupported`/`WFPAccessDenied` controls and assert zero native side effects.

- [ ] **Step 1: Add privileged integration RED controls**

Create tests that open the real dynamic session, verify both V4/V6 filters, attempt a blocked outbound connection, terminate the guardian, and query both policy stores for absence of the run’s dynamic keys. Add owner PID reuse and guardian crash cases.

- [ ] **Step 2: Run controls on the current host**

Run the full test command. If WFP management permission or verified LLVM is unavailable, record the exact SKIP/FAIL boundary and do not claim native PASS.

- [ ] **Step 3: Run final gates with pinned tools**

```bash
go test ./apps/test-service/... -count=1
go test -race ./apps/test-service/... -count=1
go vet ./apps/test-service/...
pnpm --filter @unit-test-ide/service-probe build
pnpm --filter @unit-test-ide/service-probe test
pnpm build
pnpm test
git diff --check
```

Also compile the non-Windows guardian and offlineboundary packages with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`.

- [ ] **Step 4: Review security and lifecycle invariants**

Check that no code path starts Service before `Ready`, no fallback calls PowerShell for a new run, all WFP handles close exactly once, all report fields are closed, and `Close` errors keep the process fail-closed. Verify `rg` finds no production call to `windows-offline-boundary.ps1 -Action Guard`.

- [ ] **Step 5: Commit and prepare remote handoff**

```bash
git add apps/test-service tools/service-probe .github/workflows/foundation.yml README.md docs
git diff --cached --check
git commit -m "feat: replace PowerShell boundary with native WFP guardian"
git status --short --branch
```

Do not push to GitHub/Gitee until the final review approves the complete implementation and the user explicitly requests the remote push.
