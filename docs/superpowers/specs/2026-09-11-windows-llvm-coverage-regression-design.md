# Windows LLVM Coverage Regression Repair Design

**Date:** 2026-09-11
**Status:** Approved for implementation
**Baseline:** GitHub `master` at `8f194f92863673211bde911ec1439425d6a50926`

## Problem statement

The post-merge Foundation workflow run `34548483020` failed in `verify-windows` while the Linux verification and Linux GCC coverage jobs succeeded. The required Windows LLVM coverage smoke returned Protocol v1.4 error `INVALID_TASK_SPEC` (`task specification is invalid`) from `startCoverage`. The same failure was reproduced interactively on the self-hosted runner host with administrator rights, Node.js 24.18.0, and pnpm 11.4.0. This rules out the interactive shell's original Node.js/pnpm mismatch as the cause.

The always-run repository cleanliness check exposed a separate defect: `tools/coverage-bundle/runner/__pycache__/contract.cpython-310.pyc` is tracked and changes when Python imports the runner contract. The existing `-B` child-process flags do not make an already tracked bytecode artifact safe.

## Goals

1. Identify the first concrete invariant that rejects the Windows coverage run; do not infer the fix from the public generic error.
2. Add a regression test that fails for that invariant before changing production behavior.
3. Make the smallest platform-safe correction while preserving Linux GCC coverage and the public Protocol v1.4 error contract.
4. Remove tracked Python bytecode and prevent future Python cache files from entering the repository.
5. Prove the exact Windows administrator/WFP smoke and the full repository verification pass without leaving tracked changes.

## Non-goals

- Publishing a GitHub Release.
- Enabling Windows signing.
- Weakening, skipping, or making the Windows native coverage gate optional.
- Running untrusted pull-request code automatically on the privileged self-hosted runner.
- Broad coverage architecture refactoring unrelated to the demonstrated invariant.

## Diagnostic design

Investigation runs in the isolated branch/worktree `codex/fix-windows-llvm-coverage-regression`. The first diagnostic step captures only a bounded stage identifier and sanitized error category around coverage start preparation. It must not place workspace paths, tokens, environment values, command lines, or native process output on the public protocol wire.

If stage instrumentation does not identify one invariant, the existing native smoke becomes the predicate for a bounded commit bisection across the shared coverage-platform changes. Diagnostic-only logging is removed before the final change unless it is independently useful, tested, sanitized, and accepted as a stable internal diagnostic.

## Repair design

The production repair is selected only after the failing stage is known. A focused Go test will construct the Windows-specific input that currently fails and assert the intended invariant. The implementation then changes only the owning component (coverage coordinator, Windows LLVM adapter/toolset ownership, build preparation, or task resumption). Platform-neutral contracts remain strict; Windows exceptions are not introduced merely to make the smoke pass.

The public client continues to receive the existing closed error taxonomy. Detailed internal causes remain test/service diagnostics and are redacted at the extension boundary.

Repository hygiene is repaired independently:

- remove `tools/coverage-bundle/runner/__pycache__/contract.cpython-310.pyc` from version control;
- ignore `__pycache__/` and `*.py[cod]` globally;
- retain `-B` for bundled Python invocations as defense in depth;
- verify coverage preparation/execution does not alter tracked files.

## Verification

Verification is cumulative:

1. focused regression test demonstrates red then green;
2. affected Go package tests, including Windows-specific tests;
3. TypeScript coverage smoke support tests and repository hygiene checks;
4. Windows administrator/WFP `test:coverage-service-smoke` using Node.js 24.18.0 and pnpm 11.4.0;
5. repeated focused runs when the defect is timing- or ownership-sensitive;
6. full `pnpm verify`;
7. `git diff --exit-code` and `git status --short` prove a clean tracked worktree.

## Delivery

Keep the repair in `codex/fix-windows-llvm-coverage-regression` with reviewable commits separating repository hygiene from the behavioral fix where practical. Push to GitHub and Gitee and create a GitHub pull request only after verification and code review. Do not merge the pull request without explicit authorization. Do not publish a Release or enable signing.
