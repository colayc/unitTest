# Windows LLVM Coverage Regression Repair Implementation Plan

> **Execution note:** Work sequentially in `codex/fix-windows-llvm-coverage-regression`; do not merge, release, or enable signing.

**Goal:** Restore the required Windows clang-cl/LLVM coverage smoke and keep coverage tooling from changing tracked Python bytecode.

**Architecture:** Preserve Protocol v1.4 and the strict common coverage contracts. Use opt-in, local-only service diagnostics to identify the first failing internal stage, then encode that exact case as a focused Go regression test and repair only the owning component. Treat Python cache cleanup as an independent repository-hygiene change.

**Tech stack:** Go 1.26.6 service/tests, TypeScript/Node.js 24.18.0, pnpm 11.4.0, Windows clang-cl/LLVM, native WFP guardian, GitHub Actions.

---

## Task 1: Capture the first internal rejection

**Files (temporary diagnostic edits only):**

- Modify: `apps/test-service/internal/runtime/coverage_backend.go`
- Modify: `apps/test-service/internal/coverageexec/coordinator.go`
- Modify: `apps/test-service/internal/session/session.go`
- Modify: `apps/code-oss-extension/test/coverage-service-smoke.test.ts`

1. Restore a minimal subset of the prior `UT_DEBUG_PROCESS_HOST_FAILURES` hooks: start resolver/queue/resume/reload, coverage preparation phase, and session backend category.
2. Capture a bounded tail of service stderr in the Windows smoke and append it only after `redactServiceError` processing.
3. Run the exact administrator/WFP smoke with the Actions Node.js and pinned pnpm:

   ```powershell
   $env:PATH='C:\actions-runner\_work\_tool\node\24.18.0\x64;'+$env:PATH
   $env:UT_DEBUG_PROCESS_HOST_FAILURES='1'
   corepack pnpm --filter code-oss-extension test:coverage-service-smoke
   ```

4. Record the first failing internal stage and error category. If still ambiguous, use the native smoke as a bounded `git bisect` predicate across the shared coverage-platform changes.
5. Remove all temporary diagnostic edits and confirm `git diff --check`.

## Task 2: Reproduce the invariant with a focused test

**Expected owner files (select the exact pair after Task 1):**

- Modify test: `apps/test-service/internal/coverageexec/coordinator_test.go`
- Or modify test: `apps/test-service/internal/coverageexec/orchestration_windows_test.go`
- Or modify test: `apps/test-service/internal/runtime/coverage_execution_windows_test.go`
- Modify implementation only in the corresponding package identified by Task 1.

1. Add the smallest Windows-specific or platform-contract test that constructs the rejected input.
2. Run only that test and verify it fails for the diagnosed reason, not for fixture setup.
3. Implement the minimum correction while retaining strict identity, ownership, and path validation.
4. Rerun the focused test and its package; verify green.
5. Run the same test repeatedly if ownership, cleanup, or timing is involved.

## Task 3: Remove tracked Python bytecode

**Files:**

- Modify: `.gitignore`
- Delete: `tools/coverage-bundle/runner/__pycache__/contract.cpython-310.pyc`
- Verify: `tools/coverage-bundle/prepare.test.mjs`

1. Prove the artifact is currently tracked with `git ls-files`.
2. Add global `__pycache__/` and `*.py[cod]` ignore rules.
3. Remove the tracked `.pyc`; keep existing Python `-B` invocation flags.
4. Run the coverage-bundle preparation tests and exercise the runner import path.
5. Verify no Python cache file is tracked and no tracked file changes after the test.

## Task 4: Run cumulative verification

**Files:** all changed implementation, tests, ignore rules, design, and plan.

1. Run `gofmt` on changed Go files and the affected Go package tests.
2. Run the extension coverage smoke support tests.
3. Run the exact Windows administrator/WFP native smoke at least once after the final code state.
4. Run full `pnpm verify` with Node.js 24.18.0 and pnpm 11.4.0.
5. Run `git diff --check`, inspect the complete diff, and prove only intended changes remain.
6. Commit repository hygiene separately from the behavioral repair where the dependency order permits.

## Task 5: Review and deliver

1. Request a focused code review of the final branch diff and address supported findings.
2. Repeat verification required by any review changes.
3. Push `codex/fix-windows-llvm-coverage-regression` to GitHub and Gitee without force.
4. Create a GitHub pull request with root cause, red/green evidence, native smoke result, and full verification result.
5. Observe required PR checks and report the outcome. Do not merge without explicit user authorization.
