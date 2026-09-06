# Task 4 report — GCC instrumentation and evidence lifecycle

## Scope delivered

- Added a shared atomic, exclusive, read-only CMake instrumentation publisher.
- Moved LLVM publication to that publisher while preserving its exact bytes,
  SHA-256 and fingerprint contract.
- Added the GCC/G++ CMake contract (`--coverage`, `-O0`, `-g`, and coverage
  link option) and a stable contract fingerprint.
- Added a GCC allocator that never creates per-test profiles or sets
  `GCOV_PREFIX`/`GCOV_PREFIX_STRIP`; it removes only case-sensitive `GCOV_` and
  `GCOVR_` hostile variables and validates the unmodified process target.
- Added object-scoped `.gcno`/`.gcda` evidence sealing with retained Unix root
  descriptors, `openat`/`fstatat` traversal, bounded entries/bytes/depth,
  regular-file-only checks, digesting, root replacement detection, and
  crash/timeout-only completeness reasons. Windows instrumentation and
  evidence return unsupported before filesystem interaction.

## RED → GREEN record

The initial targeted test command failed as expected because
`PublishInstrumentation`, `WriteInstrumentation`, `NewAllocator`, and
`SealEvidence` were undefined. The implementation then made that same command
green.

## Verification

All commands were run from `apps/test-service` with `GOENV=off`,
`GOTOOLCHAIN=local`, and a workspace-local `.gocache-task4`:

- `go test ./internal/coverageplatform ./internal/coveragegcc -run 'Instrumentation|Allocator|Evidence|Manifest|Cleanup' -count=1` — PASS
- `go test ./internal/coverageplatform ./internal/coveragegcc ./internal/coveragellvm -count=1` — PASS
- `go test -race ./internal/coveragegcc -run 'Evidence|Close|Replace|Cancel' -count=1` — PASS
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o '.gocache-task4/coveragegcc-linux.test' ./internal/coveragegcc` — PASS
- `GOOS=windows GOARCH=amd64 go test ./internal/coverageplatform ./internal/coveragegcc -run '^$'` — PASS
- `git diff --check` — PASS

## Host limitation

This worker is Windows, so Linux-only evidence behavior was cross-compiled but
not executed locally. Windows runtime behavior is intentionally unsupported;
its compile-only check passed. The cache directory was removed after testing.

## Preserved state

The parked Task 2A debt and user-owned `.merge-stash-20260903/` were not
modified. No remote, pull request, merge, release, or signing action occurred.
