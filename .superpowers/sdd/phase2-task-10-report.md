# Phase 2 Task 10 Report

## Status

Implemented `TaskManager` as the single owner of task state changes, output buffering, termination causes, persistence, publication, and process cleanup. Public start requests contain only idempotency key, built-in scenario, and timeout; the manager constructs the trusted internal process specification.

## TDD evidence

- RED: `go test ./apps/test-service/internal/task -run Manager -v` failed to build because `task.Manager`, `ProcessSpec`, `ManagedProcess`, `ProcessOutput`, and `ProcessResult` were undefined.
- GREEN: focused manager tests pass, including `go test -race ./apps/test-service/internal/task -run 'Manager|NewManager' -count=20`.
- REFACTOR: process termination and close were moved out of the command loop so a blocked `Terminate` cannot block terminal persistence or getters; close still waits for termination completion.

## Implemented behavior

- One typed command queue serializes Start/Get/List/Cancel/timeout/output/process completion/cleanup/Shutdown.
- Start is idempotent by canonical scenario/timeout SHA-256 and rejects conflicting replays.
- Create and every event/state mutation commit before publication; terminal artifact metadata plus `artifact.created` and `task.finished` share one `Store.Apply` transaction.
- Prepared lease is committed with `task.started` before target start, refreshed after start, and deleted only in the terminal transaction.
- Cancel, timeout, shutdown, and process completion use a first-cause-wins outcome; Phase 2 outcomes never include `test_failed`.
- Output preserves arrival order, converts invalid UTF-8, limits each text block to 16 KiB, uses a minimum 25 ms timed flush, caps persisted task output at 4 MiB, and emits one truncation event.
- Store/publisher circuit failures reject new starts and terminate active processes without fabricating terminal persistence. Artifact-file success followed by DB failure deliberately leaves an orphan for startup cleanup and publishes no uncommitted event.
- Shutdown rejects starts immediately, records `interrupted` when it wins, terminates active work, waits for terminal persistence and cleanup, and remains unhealthy/closing after caller timeout.
- Public errors and persisted messages use fixed safe values; process/store/artifact details are not propagated.

## Verification

- PASS: `go test ./apps/test-service/...`
- PASS: `go test -race ./apps/test-service/...`
- PASS: focused manager race test repeated 20 times.
- PASS: Windows-native processcontrol, processhost, and command tests.
- PASS: Windows Go build and Linux amd64 CGO-disabled cross-build.
- PASS: task package vet, protocol generated check, workspace smoke, recursive TypeScript build, and recursive workspace tests.
- Known unrelated warning: full `go vet ./apps/test-service/...` reports only the pre-existing Task 6 cancel-path warnings in `internal/eventbroker/broker_test.go:579` and `:594`. No Task 6 file was changed.

## Self-review

- The loop alone mutates active-task state; async workers only copy output, terminate/close processes, and enqueue typed results.
- Watcher/timer stop channels close once; stale flush tokens are ignored; cleanup waits for termination before close and removes the active entry through the command queue.
- Terminal publication uses committed global sequence order. DB failure after artifact commit retains the orphan and trips health.
- No transport, server/session lifecycle, platform process code, or public executable/path/shell/env/cwd input was added.

## Review fixes

- RED evidence: focused review tests initially failed because a Terminate error left the manager healthy, a later Store fault after a Close error made zero termination calls to other active processes, and ArtifactWriter failure still committed a terminal task.
- GREEN evidence: the focused review tests pass, and `go test -race ./apps/test-service/internal/task -run 'Manager|NewManager' -count=20` passes after the fixes.
- Terminate and Close workers now capture immutable values and return generation-tagged typed commands to the command loop. Only the loop mutates active-task termination/cleanup state. A Terminate error immediately makes the manager unhealthy, rejects Start, starts Close without waiting silently for Done, keeps Get/List responsive, and retains the active entry so Shutdown remains context-bounded if Done never arrives.
- The loop-owned one-shot `storageFailed` circuit is independent of general health. Its first storage, publisher, artifact, or terminal-persistence fault terminates all active processes exactly once even if a prior Terminate/Close error already made the manager unhealthy.
- Cancellation cause is assigned only after the `cancelling` snapshot and `task.cancellation_requested` event commit. A failed Apply leaves no cancelled cause, row, or event.
- ArtifactWriter failure is now storage-fatal: no artifact metadata, `artifact.created`, or `task.finished` is committed/published; the nonterminal row and lease remain for startup recovery, the completed process closes, and other active processes terminate.
- DB failure after a successfully committed artifact retains the intentional orphan, publishes no uncommitted terminal event, and trips the same recovery-required storage circuit.

## Review-fix verification

- PASS: focused manager tests repeated 20 times and focused manager race tests repeated 20 times.
- PASS: full `go test ./apps/test-service/...` and full `go test -race ./apps/test-service/...`.
- PASS: Windows Go build, native processcontrol/processhost/command regressions, and Linux amd64 CGO-disabled cross-build.
- PASS: protocol generated check, workspace smoke, recursive TypeScript build, recursive workspace tests, and task-package vet.
- Unchanged known warning: full Go vet still reports only `internal/eventbroker/broker_test.go:579` and `:594`; no Task 6 file was edited.
