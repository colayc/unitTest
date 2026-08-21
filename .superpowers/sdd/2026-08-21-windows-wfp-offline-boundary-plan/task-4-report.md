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
