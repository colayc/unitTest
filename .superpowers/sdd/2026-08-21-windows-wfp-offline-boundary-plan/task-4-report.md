# Task 4 Report — CI / reports / legacy cleanup

Date: 2026-08-21

Status: DONE_WITH_CONCERNS

Scope delivered:

- Migrated `.github/workflows/foundation.yml` from legacy `CleanupAll` semantics to WFP-era coverage smoke + bounded `LegacyCleanup`.
- Switched Windows coverage evidence to a closed WFP report shape and validated it through `tools/service-probe/src/coverage-bundle.ts`.
- Added closed-schema fixtures under `tools/service-probe/testdata/`.
- Updated Windows coverage smoke/support tests and workflow text assertions to enforce:
  - preflight before boundary/service side effects
  - no production `-Action Guard`
  - `if: always()` upload for the required Windows evidence artifact
  - fail-closed artifact absence on required verified runs
- Replaced the PowerShell script with a legacy-residue-only seam that audits known historical state/rule group residue and fails closed on unknown/live residue.
- Updated README + current roadmap/development/native-e2e notes to describe WFP guardian + legacy cleanup behavior.

Verification run:

- PASS: `pnpm --filter @unit-test-ide/service-probe test`
- PASS: `pnpm --filter code-oss-extension build`
- PASS: `pnpm --filter code-oss-extension test`
- PASS: `pnpm check:protocol-generated`
- PASS: `git diff --check`
- PASS: targeted workflow text gate inside `tools/workspace-smoke/workspace-smoke.test.mjs`
- PARTIAL: `pnpm test:workspace`
  - all workspace schema / workflow / import-policy gates passed
  - only remaining failure was `tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs` with `spawnSync cmake ENOENT`
  - local worktree had no prepared `.bundled-tools/cmake` and no host `cmake` on PATH, so this is an environment boundary rather than a Task 4 regression

Notable implementation choices:

- `toolchainDigest` is derived from the closed preflight JSON object, so the report stays path-free without widening the existing preflight wire contract.
- WFP report validation allows only explicit closed fields and enum values; timestamps must be canonical UTC ISO-8601 and monotonic.
- Legacy cleanup intentionally refuses unknown files/reparse points/live legacy creators instead of guessing ownership.

Concerns:

1. `pnpm test:workspace` is not fully green on this host because `cmake` is unavailable locally.
2. Historical spec/testdata files still mention old `Guard` / `CleanupAll` names as archival context; production code/workflow no longer invoke them.

## Fix round1 — 2026-08-21

Status: DONE_WITH_CONCERNS

Delta delivered:

- Hardened `tools/service-probe/scripts/windows-offline-boundary.ps1` so `LegacyCleanup` now audits a closed historical residue contract before deleting anything:
  - exact historical rule names only, in both `ActiveStore` and `PersistentStore`
  - exact `Name/DisplayName/Enabled/Direction/Action/Profile/Group`
  - exact closed `application/address/port/service/interface/interfaceType` filters
  - exact marker set only: `rule-name`, `owner.pid`, `guardian.nonce`, `guardian.pid`, `release`, `ready`, `removed`
  - strict UTF-8/no-BOM/single-line/size-bounded marker decoding
  - canonical rule/owner/pid/nonce content checks
  - live legacy creator detection still fail-closes cleanup
  - deletion happens only after full audit, followed by a second absent audit
- Added real Windows fixture coverage for legacy cleanup:
  - valid canonical cleanup succeeds
  - wrong nonce / wrong owner PID / wrong rule marker reject without deletion
  - extra marker / unknown state root entry reject without deletion
  - extra rule / wrong firewall action reject without deletion
- Tightened `.github/workflows/foundation.yml` so `coverage-execution-windows-*` uploads only when the required verified `windows-coverage-smoke` step succeeded; it no longer uses unconditional `always()` evidence upload.
- Updated `docs/development.md` to remove the old production PowerShell/marker wording and record the closed WFP execution report schema.
- Extended workspace import-policy verification for `guardian_windows.go` with selector-level checks, not just file-level allowlisting.

Fresh verification:

- PASS: `pnpm --filter @unit-test-ide/service-probe build`
- PASS: `node --test tools/service-probe/dist/windows-offline-boundary-legacy.test.js`
- PASS: `node --test tools/workspace-smoke/workspace-smoke.test.mjs`
- PASS: `pnpm --filter @unit-test-ide/service-probe test`
- PASS: `pnpm check:protocol-generated`
- PASS: `git diff --check`
- FAIL (pre-existing / unrelated to this patch): `go test ./apps/test-service/internal/offlineboundary/...`
  - exact failure on 2026-08-21: `TestWindowsBoundaryStartUsesInjectedEngineLeaseAndIdempotentClose` returned `offline boundary owner identity mismatch`
  - this round did not modify Go boundary logic; only workflow/docs/PowerShell/test coverage changed

Round1 concerns:

1. The targeted Go package test above is not green in this host/worktree state; I am carrying it forward as an existing concern instead of masking it.

## Fix round2 — 2026-08-21

Status: DONE

Delta delivered:

- Closed the legacy-cleanup TOCTOU gap in `tools/service-probe/scripts/windows-offline-boundary.ps1`.
- `LegacyCleanup` no longer calls blind `Remove-Item -Recurse -Force` on an already-audited state directory.
- Before any state deletion, the script now immediately re-runs closed root/directory audit after exact known historical rules are removed.
- Exact deletion now uses the audit-returned canonical marker list only:
  - each marker is revalidated for path/snapshot/content before deletion
  - the state directory itself must still be a plain non-reparse directory
  - any late extra leaf keeps the directory non-empty and fail-closes cleanup
  - directory removal is non-recursive and happens only after exact marker deletion leaves it empty
- Unknown or extra rules still remain undeleted because rule removal is still limited to the audited historical rule set.
- Added real late-mutation fixture coverage:
  - late unknown marker injection
  - late extra root leaf injection
  - late reparse replacement of the audited state directory
  - valid canonical residue still converges

Fresh verification:

- PASS: `node --test tools/service-probe/dist/windows-offline-boundary-legacy.test.js`
- PASS: `pnpm --filter @unit-test-ide/service-probe test`
- PASS: `node --test tools/workspace-smoke/workspace-smoke.test.mjs`
- PASS: `git diff --check`

Round2 concerns:

- None beyond the previously recorded host-level `cmake` / unrelated Go-package concerns from earlier rounds.
