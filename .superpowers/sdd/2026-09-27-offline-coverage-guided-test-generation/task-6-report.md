# Task 6 report — closed generation domain and durable coordinator

## Status

Implemented the Task 6 domain, request validation, candidate model, revisioned coordinator checkpoints, cancellation, bounded event replay, and restart validation. No analyzer, solver, renderer, validator, publisher, routes, or UI were added.

The generation run is a typed relation to the existing SQLite task row; progress uses the existing task-event table, and staged source bytes use the existing artifact store. Each checkpoint updates generation state, task lifecycle, candidate metadata, artifact metadata, and closed events in one SQLite transaction. A revision compare-and-swap permits one writer per run. Before every new stage checkpoint and on resume, the coordinator verifies current workspace/compile/coverage identities and rereads all previously persisted staged artifact bytes through an injected verifier. A missing verifier fails closed when artifacts exist. Generic task creation, mutation, event append, and interrupted-task recovery cannot bypass or overwrite generation checkpoints.

## Additional files and rationale

- `task/model.go` and `task/plan.go`: add only the generation task kind and the v1.5 generation-state event name; prior kinds remain unchanged.
- `taskstore/migrations/010_test_generation.sql`: the existing task-kind CHECK constraint otherwise rejects generation tasks. This additive migration preserves prior rows and introduces task-owned generation metadata/candidates.
- `taskstore/tasks.go`, `events.go`, `recovery.go`, `sqlite_test.go`: protect the new relation from generic task APIs and restart interruption, and update migration-count expectations while continuing to test migration 9 unchanged.
- `artifactstore/store.go` and `artifactstore/test_generation.go`: add one bounded, closed staged-source artifact descriptor/write/verify seam. Source bytes remain in the existing artifact store, not task/event/protocol state.

## Verification

- Red test run before implementation: `go test ./apps/test-service/internal/testgendomain ./apps/test-service/internal/testgencoord ./apps/test-service/internal/taskstore` failed because the new packages/types were absent.
- `go test ./apps/test-service/internal/testgendomain ./apps/test-service/internal/testgencoord ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore` — passed.
- `go test -race ./apps/test-service/internal/testgendomain ./apps/test-service/internal/testgencoord ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore` — passed.
- `go test ./apps/test-service/...` — passed.
- `git diff --check` — passed.
- P1 regression: `TestCheckpointRejectsExistingArtifactChangedBetweenStages` failed before the fix for both modified and deleted staged source; after the fix, both cases pass and confirm the persisted state, revision, and event sequence do not advance.
- P1 verification rerun: `go test -race ./apps/test-service/internal/testgencoord ./apps/test-service/internal/testgendomain ./apps/test-service/internal/taskstore ./apps/test-service/internal/artifactstore` and `go test ./apps/test-service/...` — passed.

Tests cover closed enums, scope/target/budget/digest rejection, task/run mismatch, duplicate case IDs, artifact ownership, impossible transitions, cancellation idempotency, one-writer CAS, restart after every stage, stale workspace and artifact bytes, migration preservation, generic-API bypass rejection, and bounded replay.

## Concerns / deferred work

- Task 6 provides a durable coordinator skeleton, not live stage execution or process-tree cancellation. The runtime stage runner and publisher remain later tasks.
- The runtime must supply a trusted snapshot verifier and `artifactstore.VerifyGenerationSource`; the coordinator deliberately refuses artifact-bearing resume without byte verification.
- The same artifact-byte verifier now gates every non-idempotent `Coordinator.Checkpoint` advancement, including checkpoints with no new artifacts. Cancellation remains available when staged bytes are stale; it does not publish or reuse those bytes.
- This task does not expose the new state event through session routes; v1.5 route projection belongs to the later runtime/routes task.
