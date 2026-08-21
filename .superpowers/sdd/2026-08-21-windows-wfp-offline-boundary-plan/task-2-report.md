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
