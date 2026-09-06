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

## Fix round 1 — evidence and publication invariants

- Manifest verification now binds its private sealed snapshot to the public
  `Notes`, `Data`, and `PartialReasons` views, and independently rechecks each
  retained root-relative file identity, size, and SHA-256. Public-slice edits,
  content edits, and same-path ABA replacement are rejected.
- Preparation rejects a zero-note tree, seals the exact pre-test `.gcno` set,
  removes only sealed stale `.gcda` entries, and rejects every later added or
  missing `.gcno`/`.gcda` entry. Manifest close removes only its sealed current
  run `.gcda` entries by a retained-root relative, no-replace staging move and
  identity check; unlisted files are never selected for deletion.
- GCC allocator validation now reconstructs the sole allowed environment
  transformation and compares it exactly, while cloning and preserving the
  launch plan and launch inputs as well as the executable, arguments, and
  directory.
- The shared instrumentation publisher now uses an opened Unix root directory,
  `openat`, `renameat2(RENAME_NOREPLACE)`, and root identity checks around
  publication. The Windows implementation uses no-replace `MoveFileEx` and
  rechecks the retained root identity. LLVM’s input bytes/fingerprint contract
  is unchanged.

### Additional RED coverage

Added negative coverage for zero notes; added prepared notes/data; public
manifest and partial-reason mutation; same-path file replacement; FIFO,
hard-link, and case-collision evidence; stale/current/unlisted cleanup;
allocator environment/launch-plan/launch-input tampering and aliasing; and
Windows GCC unsupported calls without filesystem side effects.

### Fix-round verification

From the repository root with `GOENV=off`, `GOTOOLCHAIN=local`, and a
workspace-local `.gocache-task4-fix`:

- targeted `coverageplatform`/`coveragegcc` test selection — PASS
- full `coverageplatform`, `coveragegcc`, and `coveragellvm` tests — PASS
- `go test -race ./apps/test-service/internal/coveragegcc -run 'Evidence|Close|Replace|Cancel|Allocator' -count=1` — PASS
- Linux amd64 `coveragegcc` and `coverageplatform` test-binary compilation — PASS
- Windows amd64 compile-only `coverageplatform`/`coveragegcc` check — PASS
- `git diff --check` — PASS

The host is Windows, so Linux-only `openat`/`renameat2` behavior is compiled
but not executed on this host. No external or remote action occurred.

## Fix round 2 — publication and execution edge cases

- Windows publication now opens the root through `NtCreateFile` without
  delete-sharing, creates the temporary file relative to that retained handle,
  and uses `NtSetInformationFile` with `RootDirectory` and no replacement flag
  for the final rename. A live root handle blocks replacement throughout the
  operation; failed paths mark only the retained temporary handle for deletion.
- GCC allocator keeps every pre-existing `EnvUnset` entry byte-for-byte,
  including order and duplicates, then appends precisely the hostile entries
  removed from `Env`. Validation recomputes this exact result.
- Evidence traversal now checks cancellation at each directory entry and each
  hash chunk. New tests exercise cancellation after traversal and hashing have
  started, idempotent close, and replacement during cleanup (which is rejected
  without deleting the replacement).
- Publication coverage now proves platform-specific read-only modes, one-winner
  concurrent creation, root replacement protection, and destination-symlink
  rejection. Windows uses a native retained-root replacement test.

### Fix-round verification

- targeted `coverageplatform`/`coveragegcc` selection — PASS
- full `coverageplatform`, `coveragegcc`, and `coveragellvm` tests — PASS
- GCC evidence/allocator race selection — PASS
- Linux amd64 `coveragegcc` and `coverageplatform` test-binary compilation — PASS
- Windows amd64 compile-only check — PASS
- `git diff --check` — PASS
