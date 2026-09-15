# Phase 9 Batch E — Task 1 Report

## Concise status

**BLOCKED at the external authorization boundary.** All requested local P1/P2 mapping and validation work is complete with zero final test failures. No missing executable product contract was found, so no product code or regression test was added. The nine P1/P2 matrix rows remain `MISSING` because there is no candidate-bound hosted `foundation.yml` receipt for them, and this task expressly prohibited push, workflow dispatch, and remote API verification.

## Candidate and environment

- Branch inspected: `codex/phase9-batch-d-performance`.
- Candidate HEAD tested: `b39023e8227d4a122ae5651f8e970319eb9be111` (`evidence: promote phase9 staging receipt`).
- Candidate parent: `b84c2281ca4f96874dbf27f686df375d7082b305`.
- `b39023e8227d4a122ae5651f8e970319eb9be111` is an evidence-only descendant of that parent: its four changed paths are all under `docs/superpowers/evidence/phase9/`.
- Local runtime used for Node/pnpm validation: Node `v24.19.0` from the Codex bundled runtime and repository-pinned pnpm `11.4.0`. The exact hosted pin is Node `24.18.0`; that patch version was not installed locally.
- Go runtime: `go1.26.6 windows/amd64`, matching the repository pin.
- CMake for the clean `pnpm verify` run: Visual Studio bundled CMake `3.31.6-msvc6`, added to `PATH` only for the test process.
- Pre-existing out-of-scope `.merge-stash-20260903/` was not read, changed, staged, or committed.
- The pre-existing untracked closeout plan was read for task instructions and otherwise left untouched.

## P1/P2 gate-to-command mapping

The exact registry command is quoted verbatim from `tools/phase9/gates.json`. The anchors listed below show that each requested behavior has an existing deterministic executable contract rather than only a broad umbrella command.

| Gate ID | Exact registry command | Existing executable contract anchors |
|---|---|---|
| `P1-IPC-PER-USER-AUTH` | `pnpm test:e2e` | `probe authenticates, reads capabilities, and shuts the service down`; `prepares the token file before writing the secret`; service-side `TestSessionRequiresHandshakeThenReturnsCapabilities` and `TestSessionRejectsWrongToken` are also covered by the broader verifier. |
| `P1-PROTOCOL-NO-SHELL` | `pnpm verify` | Protocol-schema tests `protocol 1.1 accepts controlled tasks and rejects shell input` and `protocol 1.1 tasks/start rejects every execution-plan injection field`; CLI `TestRunRejectsArbitraryProcessFlags`; strict unknown-method/payload Session tests. |
| `P1-PROTOCOL-VERSION-COMPAT` | `go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session` | `TestSupportedVersionRecognizesEveryShippedVersion`, `TestFailureFallsBackToV10ForUnknownVersion`, `TestSessionNegotiatesV11AndKeepsV10Shape`, `TestSessionRejectsUnsupportedAndMismatchedVersions`, plus v1.2/v1.3/v1.4 negotiation and projection tests. |
| `P1-TOKEN-FILE-SECURE` | `go test ./apps/test-service/cmd/unit-test-service` | `TestPrepareTokenFileCreatesEmptyValidatedFile`, `TestPrepareTokenFileRejectsExistingPathWithoutChangingIt`, `TestPreparedTokenFileIsOwnedByCurrentUser`, `TestConsumeTokenFileRejectsACLThatGrantsAnotherPrincipal`, permissive-mode/symlink/replacement/cleanup tests. |
| `P2-ARTIFACT-ATOMIC-CLEANUP` | `go test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/task` | `TestCommitJSONRollsBackPublicationWhenFinalizationFails`, `TestCommitJSONFlushesPublicationAndTemporaryRemoval`, `TestCleanupDeletesTempsAndOrphansButPreservesReferences`, substitution/ancestor-swap tests, artifact-sink abort/rollback tests, and close-before-terminalization outcome tests. |
| `P2-EVENT-REPLAY-PERSISTENCE` | `go test ./apps/test-service/internal/eventbroker ./apps/test-service/internal/session ./apps/test-service/internal/taskstore` | `TestSubscribeBridgesReplayAndLiveWithoutGap`, `TestReplayUsesFixedWatermarkAndPagesInGlobalOrder`, `TestEmptyReplayPageBeforeWatermarkFailsClosed`, `TestStoreCommitsSnapshotAndEventAtomically`, and `TestEventsAfterUsesExclusiveAfterInclusiveThrough`. |
| `P2-FAILURE-OWNERSHIP` | `go test ./apps/test-service/internal/task ./apps/test-service/internal/taskstore` | Prepared-lease persistence/ownership tests, `TestManagerPublisherFailureFailClosedAfterCommittedCreate`, publisher/store failure handoff tests, close-failure ownership tests, and atomic recovery/lease deletion tests. |
| `P2-PROCESS-TREE-TERMINATION` | `go test ./apps/test-service/internal/processcontrol ./apps/test-service/internal/processhost` | Windows job-object/grandchild, descendant cleanup, cancellation, EOF and termination convergence tests; shared host timeout/stop tests; platform-specific Linux group/tree cleanup tests execute on Linux builds. |
| `P2-TASK-CANCEL-TIMEOUT` | `go test ./apps/test-service/internal/task ./apps/test-service/internal/testrun` | Cancellation and total-budget timeout tests; `TestManagerFirstTerminationCauseWins`; `TestManagerProcessDoneUsesClaimedTimeoutBeforeTimeoutCommand`; `TestManagerCloseFailurePreservesCancellationCause`; `TestManagerCloseFailurePreservesTotalTimeoutCause`; interpreter timeout mapping. These separately cover cancellation timeout and first-cause preservation. |

All ten behaviors named by the brief—per-user auth, no-shell protocol, version compatibility, secure token files, atomic cleanup, replay gaps, failure ownership, process-tree termination, cancellation timeout, and first-cause preservation—are therefore mapped. There was no unmapped requirement and no basis for a new failing test.

## RED→GREEN evidence

No product RED→GREEN cycle was performed because inspection demonstrated existing executable coverage for every requested requirement. Adding another test would have duplicated a mapped contract and violated the instruction to write failing tests only for genuinely unmapped requirements.

There were two local harness corrections, neither involving repository product code:

1. The first pinned `pnpm test:e2e` attempt was rejected before tests because nested scripts resolved a PATH pnpm `11.19.0` instead of the required `11.4.0`. A temporary task-local command shim made all nested invocations use pnpm `11.4.0`; the shim was deleted after validation.
2. The first `pnpm verify` attempt reached workspace smoke after all preceding checks had passed, then failed with `spawnSync cmake ENOENT`. Adding the already-installed Visual Studio CMake directory to that process's `PATH` made the focused `pnpm test:workspace` rerun pass `31/31`, after which a fresh complete `pnpm verify` run passed.

These were environment setup failures, not failing application contracts, and caused no tracked product change.

## Focused commands and outputs

### Exact P1/P2 Go registry commands

All commands exited `0`:

```text
go test ./apps/test-service/internal/protocol ./apps/test-service/internal/session
ok  unit-test-ide.local/test-service/internal/protocol (cached)
ok  unit-test-ide.local/test-service/internal/session (cached)

go test ./apps/test-service/cmd/unit-test-service
ok  unit-test-ide.local/test-service/cmd/unit-test-service 0.595s

go test ./apps/test-service/internal/artifactstore ./apps/test-service/internal/task
ok  unit-test-ide.local/test-service/internal/artifactstore (cached)
ok  unit-test-ide.local/test-service/internal/task (cached)

go test ./apps/test-service/internal/eventbroker ./apps/test-service/internal/session ./apps/test-service/internal/taskstore
ok  unit-test-ide.local/test-service/internal/eventbroker (cached)
ok  unit-test-ide.local/test-service/internal/session (cached)
ok  unit-test-ide.local/test-service/internal/taskstore (cached)

go test ./apps/test-service/internal/task ./apps/test-service/internal/taskstore
ok  unit-test-ide.local/test-service/internal/task (cached)
ok  unit-test-ide.local/test-service/internal/taskstore (cached)

go test ./apps/test-service/internal/processcontrol ./apps/test-service/internal/processhost
ok  unit-test-ide.local/test-service/internal/processcontrol (cached)
ok  unit-test-ide.local/test-service/internal/processhost (cached)

go test ./apps/test-service/internal/task ./apps/test-service/internal/testrun
ok  unit-test-ide.local/test-service/internal/task (cached)
ok  unit-test-ide.local/test-service/internal/testrun (cached)
```

The later clean `pnpm verify` also reran the complete Go suite and race suite. Relevant fresh race results included `internal/artifactstore 2.677s`, `internal/eventbroker 1.318s`, `internal/processcontrol 42.150s`, `internal/processhost 1.556s`, `internal/session 1.392s`, `internal/task 2.639s`, `internal/taskstore 93.173s`, and `internal/testrun 1.501s`, all `ok`.

### Exact E2E registry command

```text
pnpm test:e2e
tests 20
pass 20
fail 0
cancelled 0
skipped 0
todo 0
duration_ms 205387.8033
```

The clean full verifier repeated the same 20-test E2E suite and again exited `0` with `pass 20`, `fail 0`, including `task survives reconnect, cancels its tree, persists history and artifact`.

### Exact no-shell registry command

```text
pnpm verify
exit 0
```

The clean run passed protocol and coverage generation checks, Phase 9 renderer `--check`, all builds, all Node/package tests, workspace smoke `31/31`, protocol schema `23/23`, test client `86/86`, service probe `84 passed / 1 supported-environment skip`, Code-OSS extension `146/146`, all Go packages, all Go race packages, and final E2E `20/20`. The protocol-schema output explicitly included the no-shell and execution-plan-injection rejection tests.

### Required Phase 9 validator/audit tests

The brief's exact command was run under Node 24 and exited `0`:

```text
node tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
tests 48
pass 48
fail 0
duration_ms 11482.4471
```

Because plain `node` executes only the first script and treats the second pathname as an argument, the clean `pnpm verify` additionally ran the canonical two-file command:

```text
node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
tests 98
pass 98
fail 0
```

### Final local integrity checks

- `node tools/phase9/render.mjs ... --check`: passed as part of the clean `pnpm verify`; no matrix files were rewritten.
- `git diff --check`: passed with no output before report creation.
- Workflow inspection: `.github/workflows/phase9-gates.yml` retains only `actions: read` and `contents: read`, uses the already-reviewed full action SHAs, and contains no `secrets.*` reference.
- Matrix inspection: `releaseReady=false`; exactly `P8-DOCS-CLOSEOUT`, `P8-LEGAL-THIRD-PARTY`, and `P8-SIGN-WINDOWS` are `DEFERRED`; every P1/P2 row remains `MISSING` rather than being falsely promoted.

## Hosted dispatch preparation and authorization boundary

No push, network lookup, workflow dispatch, artifact download, `gh api` call, receipt creation, matrix regeneration, merge, tag, release, or signing action was performed.

The candidate is locally associated with existing remote-tracking refs for `github/codex/phase9-batch-d-performance` and `origin/codex/phase9-batch-d-performance`, both at `b39023e8227d4a122ae5651f8e970319eb9be111`; these refs were not freshly fetched and are not claimed as live remote proof.

The gate registry requires hosted evidence from `.github/workflows/foundation.yml` jobs `verify-linux` and `verify-windows`. A candidate-bound run and its exact run ID/attempt, job conclusions, and any required artifact identities must be independently verified before a receipt can exist. That work requires external authorization.

There is an additional dispatch-safety concern to resolve before authorization is used: `foundation.yml`'s manual dispatch requires release/package inputs and enables package jobs, so it is not a narrow P1/P2-only dispatch. It also contains mutable action tags such as `actions/checkout@v6`, while this Batch E task's constraint permits only fixed action SHAs already accepted by `.github/workflows/phase9-gates.yml`. Therefore this report does not present a supposedly ready-to-run `gh workflow run foundation.yml` command. The safe hosted path needs an explicitly authorized, fixed-SHA workflow/ref that produces the registry-required `verify-linux` and `verify-windows` identities without widening into release behavior.

Existing checked-in receipts do not solve this: the selected baseline names only the performance receipt for `b84c2281ca4f96874dbf27f686df375d7082b305`, and no checked-in receipt covers any P1/P2 gate for candidate `b39023e8227d4a122ae5651f8e970319eb9be111`.

## Files changed

- `.superpowers/sdd/2026-09-15-phase9-batch-e-candidate-closeout/task-1-report.md` — this report only.

No application, test, workflow, baseline, receipt, or matrix file was changed.

## Local commit SHAs

- Tested candidate: `b39023e8227d4a122ae5651f8e970319eb9be111`.
- New product/test commit: none (no missing contract was demonstrated).
- The evidence-only report commit SHA is returned to the orchestrating task after this report is committed; a Git commit cannot contain its own SHA.

## Remaining blockers

1. Fresh explicit authorization for hosted workflow dispatch and subsequent GitHub API/snapshot verification.
2. A fixed-SHA, non-release-expanding hosted route that yields the registry-required `foundation.yml` `verify-linux` and `verify-windows` job identities for the exact candidate.
3. Only after that run succeeds: exact run/job/artifact verification, canonical P1/P2 receipt creation, evidence-only matrix regeneration, and renderer `--check`.

Until those blockers are resolved, all P1/P2 rows correctly remain `MISSING` and this task must not claim them `PASS`.
