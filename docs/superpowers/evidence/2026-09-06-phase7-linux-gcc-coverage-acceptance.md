# Phase 7 Linux GCC coverage acceptance

## Acceptance state

Overall status: **NOT-RUN/CI-only**. The Task 10 gate is implemented locally,
but final acceptance requires a user-authorized GitHub run, successful required
checks on both native platforms, archived artifacts, and branch protection.
No Windows result is inferred for Linux and no static check is counted as a
native PASS.

## Source and CI coordinates

| Coordinate | Status | Value |
|---|---|---|
| Task 10 base | PASS | `a2f7159` with prerequisite `75dfa23` in history |
| Task 10 candidate | PASS | the immutable commit containing this report |
| GitHub workflow run | NOT-RUN/CI-only | pending explicit remote authorization |
| Linux evidence artifact ID/digest | NOT-RUN/CI-only | pending `coverage-linux-gcc` |
| Windows evidence artifact ID/digest | NOT-RUN/CI-only | pending trusted `master` push |
| GitHub/Gitee synchronized commit | NOT-RUN/CI-only | no push or merge in Task 10 local implementation |

## Gate outcomes

| Gate | Status | Evidence boundary |
|---|---|---|
| Workflow/static contract | PASS | unique `coverage-linux-gcc`; no `continue-on-error`; bootstrap precedes native entry; exact evidence validator precedes upload |
| Linux evidence schema | PASS | canonical one-line JSON; closed keys; GCC/bundle/framework identities; known CppUTest/Unity metrics; five failure mappings; artifact SHA-256; byte/digest determinism; timestamps; path/env/secret rejection |
| Windows evidence schema | PASS | canonical one-line JSON; closed WFP schema; toolchain digest; `passed/released/passed` required; timestamps; path/env/secret rejection |
| Linux production native smoke | NOT-RUN/CI-only | must run on `ubuntu-24.04` inside the fail-closed network namespace |
| Windows Named Pipe/WFP smoke | NOT-RUN/CI-only | must run on the dedicated privileged Windows runner on trusted `master` push |
| Evidence archive and immutable artifact IDs | NOT-RUN/CI-only | populated only from a successful GitHub Actions run |
| `master` required-check protection | NOT-RUN/CI-only | configure only after the new check is stable and successful |

The Linux native command owns namespace entry. Its descendants include the
production Service, Unix socket, CMake, GCC/G++, CppUTest/Unity binaries, gcov,
and the pinned Python/gcovr collector. Boundary setup failure is a job failure,
not a success-class SKIP. The Windows trusted-master path likewise requires the
real Named Pipe/WFP guardian and validates the exact report bytes before upload.

## Deferred post-acceptance work

| Item | Status | Reason |
|---|---|---|
| GitHub Release publication | SKIP | explicitly outside Task 10 scope |
| Production Windows signing | SKIP | deferred until real public release |
| Third-party license/legal human approval | SKIP | deferred until real public release |
| Gitee synchronization | SKIP | requires later explicit authorization after GitHub `master` checks pass |

`SKIP` above means intentionally deferred work, not a passing release gate.
Until the CI-only rows become PASS with recorded run and artifact identities,
Phase 7 Batch A remains open.
