# Task 5 report — Protocol v1.5 test generation

## Status

Implemented in the task worktree. Protocol v1.5 adds closed offline test-generation requests, responses, task/event surfaces, fixtures, generated TypeScript/Go models, and typed client methods. No Go runtime routes or Code-OSS UI were changed.

## Contract

- Added `testGeneration/targets/list`, `testGeneration/start`, `testGeneration/runs/get`, `testGeneration/candidates/list`, and `testGeneration/accept` as v1.5-only methods. Generic `tasks/cancel` remains available and decodes a v1.5 generation task.
- Closed scope, framework, candidate-kind, run-state, budgets, percentages, digest formats, and state-change transitions. Start requests carry goals and wall-time/candidate/memory/concurrency budgets.
- Candidate summaries are metadata only: code/artifact/evidence digests, assertion provenance, baseline/delta coverage, relative planned-edit paths, diagnostics, and characterization confirmation state. Raw source and absolute path fields are rejected.
- Accept requests carry explicit `confirmCharacterization` consent. The service must resolve candidate kind from the authoritative `(runId, candidateId)` record and reject characterization candidates unless consent is true; the caller cannot supply candidate kind.
- Added v1.5 package exports, generator model registrations, client envelope/connection validation, typed decoders, and public model/client exports. The schema package export map and connection/client index files were additionally necessary to make the listed contracts callable.

## Red/green evidence

- Schema test was first observed failing because `v1.5/capabilities.schema.json` was absent, then passed after the schemas were registered. A second response/state test was observed failing on the missing state-change event, then passed after the event contract was added. A capability/task test was observed failing before those surfaces were added.
- Model test was first observed failing with missing `TestGenerationStartRequestV15`, `TestGenerationRunV15`, and `TestGenerationCandidatePageV15` exports, then passed after generator and index changes.
- Client test was first observed failing with missing generation methods. The generic cancellation test then failed against the v1.1 task validator before its v1.5 branch was added. A common artifact-list test failed against the legacy artifact-page validator before the v1.5 branch was added.
- Existing v1.0–v1.4 generated files were not modified. The v1.5 generator additions append new model targets and leave older target/template branches unchanged.

## Verification

- `pnpm generate:protocol` — passed using Node 24.19.0 and pinned pnpm 11.4.0.
- `pnpm check:protocol-generated` — passed with the same pinned runtime.
- `node --test packages/protocol-schema/test/schema.test.mjs` — 26/26 passed.
- `pnpm --filter @unit-test-ide/protocol-models test` — 8/8 passed.
- `pnpm --filter @unit-test-ide/test-client test` — 92/92 passed.
- `go test ./apps/test-service/internal/protocolmodel/v1_5/...` — passed.
- `git diff --check` — passed.
- Repository-wide `pnpm test` was attempted, but stopped before running tests: the nested `pnpm` command resolves to 11.19.0 in this host while the repository pins 11.4.0. The scoped test commands above were invoked with the pinned pnpm CLI and all passed.

## Review notes

- The v1.5 generated Go event name and protocol-version constants were checked after regeneration.
- New v1.5 client decoders build typed objects after schema validation and do not add `unknown as` casts. Older decoder casts were left unchanged.
- No signing, tagging, release publishing, remote synchronization, branch-protection changes, runtime routes, or UI work was performed.

## Round 1 review fixes (T5-R1–R4)

- **T5-R1:** Removed caller-supplied `candidateKind` from `testGeneration/accept`. The request is now only `(runId, candidateId, confirmCharacterization)`. Schema and generated TypeScript/Go types explicitly require the future service to look up the authoritative candidate kind by run/candidate IDs and reject a characterization candidate without true confirmation. The client validates and transmits no classification field. This deliberately cannot be decided by wire schema alone because candidate state lives on the service.
- **T5-R2:** Candidate diagnostic messages and target display names now use closed path-free/assignment-free character patterns. Structured `targetId`, diagnostic `code`, and `plannedEdit.path` remain available for useful identifiers. V1.5 artifact URIs are limited to the `unit-test-ide://artifact[s]/...` service scheme, rejecting `file:` and network URIs. The producer must still redact sensitive words before formatting a permitted summary; no character pattern can recognize every possible secret value.
- **T5-R3:** Generation target-list/start requests and runs now use the same 64-character project ID limit as the v1.5 task schema.
- **T5-R4:** The start request uses five exclusive scope branches. Symbol/file/target/coverage-gap each require exactly their own selector; workspace accepts none.

Regression evidence: schema tests were red at 26/28 before the fix (accept required caller classification; mixed selectors were accepted), and the generated-model test failed to compile without `candidateKind`. After regeneration: schema 28/28, model 8/8, client 93/93. Client tests cover spoofed classification rejected before writing, path/secret-bearing candidate diagnostics and target names rejected on receipt, and `file:` artifact metadata rejected. Mutation tests cover Windows drive/backslash, slash-prefixed, and drive/slash paths, all five valid scopes and conflicting selectors, and 65-character project IDs across generation and task schemas. `go test ./apps/test-service/internal/protocolmodel/v1_5/...` passed. Existing v1.0–v1.4 generated files remain byte-stable in the working diff.

`pnpm generate:protocol`, `pnpm check:protocol-generated`, and `git diff --check` also passed with the pinned runtime. Self-review found no Go routes or UI changes; the only client implementation edit is a comment documenting the service-side acceptance obligation already carried by the generated type.
