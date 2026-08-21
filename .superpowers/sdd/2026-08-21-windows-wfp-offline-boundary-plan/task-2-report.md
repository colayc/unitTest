# Task 2 report: guardian process and owner lifecycle

Date: 2026-08-21

Status: committed

## Summary

Implemented a Windows-only guardian-process boundary flow that moves WFP session ownership out of the caller process. The parent now starts a dedicated `native-offline-guardian` process, speaks a closed and length-limited IPC protocol (`hello`, `ready`, `release`, `error`, `bye`), waits for `ready` before exposing a ready lease, enforces owner PID + creation-time validation, and makes `Close()` idempotent while waiting for both `bye` and guardian process exit.

The previous in-process WFP lease path was removed from `Start()` on Windows; the WFP engine code remains as the native guardian’s implementation detail. Non-Windows behavior remains compile-safe and side-effect free.

## Tests / verification evidence

Focused RED/GREEN target:

- `go test ./apps/test-service/internal/offlineboundary -run 'Guardian|Protocol|Owner|Release' -count=1`
  - RED evidence: compile failed first because guardian protocol/session symbols were undefined.
  - Final GREEN: `ok  	unit-test-ide.local/test-service/internal/offlineboundary	0.078s`

Required compile/vet gates:

- `$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go test -c ./apps/test-service/cmd/native-offline-guardian`
  - Result: `?   	unit-test-ide.local/test-service/cmd/native-offline-guardian	[no test files]`

- `go vet ./apps/test-service/internal/offlineboundary ./apps/test-service/cmd/native-offline-guardian`
  - Result: exit 0, no output

Covered behaviors in `guardian_windows_test.go`:

- oversized / malformed protocol frames rejected
- owner PID reuse / creation-time mismatch rejected
- `Ready()` stays blocked until `ready`
- wrong message order rejected
- ready timeout surfaces as `GuardianTimeout`
- guardian crash before ready surfaces as `GuardianStartFailed`
- `Close()` sends `release`, waits for `bye` + process exit, and is idempotent
- guardian closes WFP session and emits `bye` when owner termination is observed

## Files changed

- Modified:
  - `apps/test-service/internal/offlineboundary/boundary.go`
  - `apps/test-service/internal/offlineboundary/wfp_windows.go`

- Added:
  - `apps/test-service/internal/offlineboundary/protocol.go`
  - `apps/test-service/internal/offlineboundary/guardian_windows.go`
  - `apps/test-service/internal/offlineboundary/guardian_windows_test.go`
  - `apps/test-service/cmd/native-offline-guardian/main_windows.go`
  - `apps/test-service/cmd/native-offline-guardian/main_nonwindows.go`

## Design notes

- Parent startup now performs owner PID + creation-time validation before launching the guardian; the guardian re-validates against the opened owner handle before installing filters.
- Guardian IPC uses a strict 4-byte length prefix and a closed frame schema with bounded payload size.
- The native guardian owns the dynamic WFP session and closes it before sending `bye`.
- The parent waits for `bye` and guardian process exit before reporting `Close()` success.

## Self-review / concerns

- Guardian executable discovery currently assumes `native-offline-guardian.exe` is deployed beside the caller executable. That keeps the runtime simple, but packaging/runtime integration still needs to ensure the sibling binary is present.
- Lifecycle coverage is strong at the package level, but there is not yet an end-to-end Windows integration test that spawns the compiled guardian binary through the real named-pipe path.

## Commit

- Local commit created for this task: `feat: add WFP guardian process lifecycle` (final HEAD at handoff)

## Fix round1 (review findings)

### Root cause summary

- The first Task 2 implementation used a raw hand-rolled Windows named pipe instead of one of the accepted IPC mechanisms from the brief/review.
- Protocol parsing validated frame kinds but did not close the `error` frame code space.
- Public errors still allowed raw launcher / verifier / process / IPC causes to escape through `Start`, `Close`, or guardian stderr.
- The initial report did not lock the release-timeout / no-`bye` / no-exit close path in tests.
- Guardian executable discovery was still an implicit sibling-binary assumption with no caller-supplied contract.

### Fixes applied

- Replaced the raw pipe setup with `go-winio` pipe listener/dial transport.
- Tightened protocol validation so `guardianFrameError` accepts only explicit enum members (`guardianErrorStartup` today); unknown codes are rejected as malformed.
- Collapsed public/API/main-visible failures to canonical sentinels:
  - owner validation → `ErrOwnerIdentityMismatch`
  - guardian start / child bootstrap failures → `GuardianStartFailed`
  - release/close failures → `SessionCloseFailed` and timeout classification via `GuardianTimeout`
- Added release-timeout regression coverage to ensure `Close()` does not report success early and repeated `Close()` returns the same canonical result.
- Added an explicit caller-provided guardian executable path contract through config-based resolution; runtime still defaults to sibling-binary lookup when the caller does not override it.

### Additional RED/GREEN evidence

Focused RED for review-fix tests:

- `go test ./apps/test-service/internal/offlineboundary -run 'Guardian|Protocol|Owner|Release' -count=1`
  - RED evidence: compile failed first because the new explicit executable-path contract test referenced missing config/resolve symbols.

Focused GREEN after fixes:

- `go test ./apps/test-service/internal/offlineboundary -run 'Guardian|Protocol|Owner|Release' -count=1`
  - Result: `ok  	unit-test-ide.local/test-service/internal/offlineboundary	0.108s`

Re-run required gates after fix round1:

- `$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go test -c ./apps/test-service/cmd/native-offline-guardian`
  - Result: `?   	unit-test-ide.local/test-service/cmd/native-offline-guardian	[no test files]`

- `go vet ./apps/test-service/internal/offlineboundary ./apps/test-service/cmd/native-offline-guardian`
  - Result: exit 0, no output

### Additional behaviors now covered

- malformed `error` frame code is rejected
- release timeout with no `bye` / no guardian exit returns a canonical close failure and repeated `Close()` returns the same result
- owner-verifier and guardian-launcher failures do not leak absolute paths
- guardian executable path can be supplied explicitly by the caller config

### Updated concerns

- The executable-path contract is now explicit and test-covered, but the default production wiring still falls back to sibling-binary discovery when callers do not override it. Task 3 or higher-level integration still needs to decide the final deployment handoff.
- Lifecycle coverage is still package-level. There is not yet an end-to-end Windows test that spawns the compiled guardian binary over the real `go-winio` transport in CI.
