# Task 1 Report

Status: DONE_WITH_CONCERNS

## RED

- Command: `go test ./apps/test-service/internal/offlineboundary -count=1`
- Result: FAIL as expected after test-first setup. The package built far enough to show missing implementation symbols, including `New`, `Config`, `OwnerIdentity`, `ErrOwnerIdentityMismatch`, `fwpmSession0`, and `fwpmFilter0`.

## GREEN

- Command: `gofmt -w apps/test-service/internal/offlineboundary`
- Result: PASS

- Command: `go test ./apps/test-service/internal/offlineboundary -count=1`
- Result: `ok  	unit-test-ide.local/test-service/internal/offlineboundary	0.057s`

## Linux cross-compile

- Command: `$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'; go test -c ./apps/test-service/internal/offlineboundary`
- Result: PASS

## Verification

- Command: `go vet ./apps/test-service/internal/offlineboundary`
- Result: PASS

- Command: `git diff --check -- apps/test-service/internal/offlineboundary apps/test-service/go.mod apps/test-service/go.sum`
- Result: PASS

## Changed files

- `apps/test-service/internal/offlineboundary/model.go`
- `apps/test-service/internal/offlineboundary/errors.go`
- `apps/test-service/internal/offlineboundary/boundary.go`
- `apps/test-service/internal/offlineboundary/boundary_nonwindows.go`
- `apps/test-service/internal/offlineboundary/wfp_windows.go`
- `apps/test-service/internal/offlineboundary/boundary_test.go`
- `apps/test-service/internal/offlineboundary/wfp_windows_test.go`

No `go.mod` or `go.sum` changes were required.

## Self-review

- Kept the non-Windows path side-effect free and returning `ErrUnsupported` only after owner validation.
- Added a Windows-only WFP ABI wrapper around lazy-loaded `fwpuclnt.dll` entry points used by this step.
- Added seam-focused tests for dynamic-session open, V4/V6 block filters, access-denied mapping, audit rejection, and idempotent close.
- Removed the transient `offlineboundary.test` artifact produced by the Linux cross-compile check.

## Unresolved concerns

1. `FilterAuditFailed` is enforced against the exact expected V4/V6 keyed filters, but this core wrapper does not yet enumerate all WFP filters for the lease, so “extra filter” detection is bounded to duplicate/mismatched keyed responses rather than exhaustive store inspection.
2. The core wrapper uses deterministic per-lease filter keys and `FWPM_SUBLAYER_UNIVERSAL`; it does not yet register a dedicated per-lease provider/sublayer object model.

## Commit

Implementation commit: `558ad40` — `feat: add Windows WFP offline boundary core`
