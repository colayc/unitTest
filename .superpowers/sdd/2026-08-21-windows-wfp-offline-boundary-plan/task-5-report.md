# Task 5 report: privileged Windows integration and handoff

## Status

`DONE_WITH_CONCERNS`

The privileged integration and its required-mode controls are implemented, the
non-privileged and cross-platform gates pass, and CI now runs the privileged Go
and TypeScript commands before the coverage Service/native smoke. This host
cannot claim a native WFP or verified LLVM PASS: WFP management returns
`WFPAccessDenied`, the verified coverage toolset is unavailable, and the final
root workspace command cannot spawn `cmake`.

## RED evidence

- The first Go integration compile failed because the new access-denied frame
  used the not-yet-defined `guardianErrorWFPAccessDenied` protocol code.
- The first TypeScript integration compile failed because `WFPAccessDenied`
  was not an accepted guardian error code and the default bridge had no
  executable anchor for fixture siblings.
- The first workflow control failed because no required privileged Go or
  TypeScript WFP command ran before the coverage Service/native smoke.
- The first Linux cross-compile failed because `guardianSession` and
  `guardianOwnerVerifier` were declared only in the Windows implementation.
- The first real engine probe failed at `FwpmEngineOpen0` with
  `ERROR_NOT_SUPPORTED`. A test-only call trace isolated the failure to engine
  open; the Windows API requires `RPC_C_AUTHN_WINNT` or
  `RPC_C_AUTHN_DEFAULT`, while the implementation passed zero. After using
  `RPC_C_AUTHN_WINNT`, the same probe reached the host's real permission
  boundary and returned the canonical `WFPAccessDenied` result.

## Delivered GREEN behavior

- The Go privileged test uses a real dynamic WFP session and unique provider,
  sublayer, V4 and V6 filter keys. It proves outbound loopback connectivity
  before installation, blocked V4/V6 connects while the filters are live,
  non-persistent dynamic object flags, normal-release removal, and restored
  connectivity.
- Guardian-crash and real-owner-termination cases prove that a fresh WFP
  observer cannot find the run's provider, sublayer or filters after the
  dynamic session owner disappears. The PID-reuse control supplies a matching
  PID with a mismatched creation time and proves fail-closed rejection before a
  provider is created.
- Local access denial skips with the exact `WFPAccessDenied` reason. Setting
  `UNIT_TEST_IDE_WFP_INTEGRATION_REQUIRED=1` converts the same boundary into a
  test failure; it never skips in required mode.
- The TypeScript end-to-end control uses the default sibling resolution:
  coverage preflight completes first, guardian `Ready` is required next, and
  only then may the Service/native side effect run. Local unavailable-toolchain
  and access-denied controls prove zero Service side effects; required mode
  fails instead of skipping.
- Guardian startup error code 2 is the fixed `WFPAccessDenied` code in Go and
  TypeScript. It is preserved through sanitization and mapped to the stable
  sentinel rather than an uncontrolled Windows error string.
- Pure guardian session and owner-verifier interfaces now live in the common Go
  file, so non-Windows packages compile while runtime behavior remains explicit
  `ErrUnsupported`.
- Required Windows CI steps run the privileged Go lifecycle and TypeScript WFP
  integration before the existing coverage Service/native smoke.

## Current-host integration boundary

- Privileged Go, local mode: **SKIP**, exactly
  `WFP management permission unavailable (WFPAccessDenied); required mode would FAIL`.
- Privileged Go, required control: **expected FAIL**, exactly
  `required WFP integration FAIL: WFPAccessDenied`.
- TypeScript WFP end-to-end, local mode: two injected controls PASS and the
  real default-sibling test **SKIP** with
  `ToolchainUnavailable; required mode would FAIL`.
- TypeScript WFP end-to-end, required control: two injected controls PASS and
  the real default-sibling test **expected FAIL** because the verified coverage
  toolset is unavailable.
- Therefore this report makes no native privileged WFP PASS and no verified
  LLVM coverage PASS claim. The required CI job is the authoritative executor
  for those host capabilities.

## Final verification

Pinned commands used Node 24.19.0, pnpm 11.4.0, Go 1.26.6,
`GOENV=off`, `GOTOOLCHAIN=local`, and private worktree Go caches.

- `go test ./apps/test-service/... -count=1`: PASS.
- `go test -race ./apps/test-service/... -count=1`: PASS.
- `go vet ./apps/test-service/...`: PASS.
- Linux `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` offlineboundary test compile:
  PASS.
- Linux `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` native guardian build: PASS.
- `pnpm --filter @unit-test-ide/service-probe build`: PASS.
- `pnpm --filter @unit-test-ide/service-probe test`: PASS, 66 pass / 1 honest
  toolchain skip.
- `pnpm build`: PASS.
- `pnpm check:protocol-generated`: PASS.
- `pnpm check:coverage-generated`: PASS.
- `pnpm -r --if-present test`: PASS; Service Probe is 66 pass / 1 skip and the
  Extension is 136/136.
- `pnpm test:workspace`: 21 pass / 1 environment failure,
  `spawnSync cmake ENOENT`.
- `pnpm test`: generator and bundle stages PASS, including 34 pass / 1 existing
  Python `EPERM` skip in the coverage bundle; it then stops at the same sole
  workspace `cmake ENOENT` failure. Package tests were run separately and PASS.
- `git diff --check`: PASS.

## Security and lifecycle review

- Production source contains no call to
  `windows-offline-boundary.ps1 -Action Guard`; the remaining script references
  are legacy-cleanup tests and the cleanup workflow.
- Both TypeScript startup and the coverage smoke place the Service/native start
  after preflight and guardian `Ready`.
- Live object enumeration checks provider, sublayer and both filters are marked
  non-persistent. Normal close and abnormal owner loss are then checked through
  both the existing observer and a newly opened dynamic observer, which is the
  native WFP equivalent of proving no persistent-policy residue.
- Session close is idempotent, owns each WFP handle exactly once, and propagates
  close/guardian failures. Go and TypeScript fail-closed controls remain green.
- Closed-report-schema validators remain green; no new open-ended report fields
  or raw native error values were introduced.

## Concerns and handoff

- A Windows runner with WFP management permission is still required to execute
  the real block/release/crash lifecycle as PASS.
- A runner with the repository-verified clang-cl/LLVM toolset is still required
  to execute the positive default-sibling TypeScript path as PASS.
- This host has no `cmake` on `PATH`, so the exact root `pnpm test` command is
  not green here even though the affected package suites and all generated-file
  checks pass independently.
- Temporary caches, Linux binaries and the pnpm shim are removed before commit.
  No push is performed.

## Review fix round 1 — lifecycle closure

The first Task 5 review rejected four lifecycle gaps. Each correction was
developed from an observed RED regression before production changes:

- Coverage smoke teardown previously ran Service stop, fixture deletion and
  guardian close in that order. The new ownership-order regression failed
  until the flow became Service stop, guardian close, fixture deletion and
  only then atomic evidence publication. The guardian executable therefore
  remains present until the guardian has exited.
- TypeScript previously stopped reading after `Ready`, so a guardian crash was
  discovered only during teardown. The new post-Ready tests failed until the
  boundary continuously raced the already-pending next frame and child exit.
  `runGuarded` now checks liveness before starting native work, aborts an
  in-flight callback through `AbortSignal`, invokes bounded Service cleanup,
  and prevents evidence publication on boundary loss.
- `GuardianFrameReader.fail` previously recorded failure without rejecting its
  pending read. A real connected-socket regression hung until the reader began
  rejecting its waiter on error, end and close. Startup, Release/Bye, child
  exit and termination waits are now bounded; termination waits for child exit
  rather than returning immediately.
- Go Add, Audit and Ready startup failures previously ignored `engine.Close`
  errors. Fault injection failed until each path joined the primary error,
  `SessionCloseFailed` and the close error, closed exactly once, and used fixed
  session-close protocol code 3. A joined access-denied primary can therefore
  never become a local `WFPAccessDenied` skip when cleanup was not proved.

Fresh review-round verification used the same pinned Node 24.19.0, pnpm
11.4.0 and Go 1.26.6 setup:

- Focused WFP lifecycle/public-wrapper tests: PASS, 16/16.
- Focused Extension sequencing, teardown and publication tests: PASS, 14/14.
- Full Service Probe: PASS, 72 pass / 1 honest `ToolchainUnavailable` skip.
- Full Extension: PASS, 138/138.
- Full workspace package tests: PASS, including the same 72/1 Service Probe
  result and Extension 138/138.
- Full Go Service, race and vet: PASS.
- Linux offlineboundary test compile and native guardian build: PASS.
- Protocol/coverage generated checks and root build: PASS.
- Exact root test: generator 4/4, CMake bundle 28/28 and coverage bundle
  34 pass / 1 existing Python `EPERM` skip, then the same environment-only
  workspace failure `spawnSync cmake ENOENT` (21/22). Package and Go suites
  were run separately and passed.
- `git diff --check`: PASS.

The current host still has no WFP management permission, verified LLVM or
`cmake` on `PATH`; no native privileged PASS was inferred from the review
controls. The required Windows CI commands remain the authoritative positive
executor. No push was performed.
