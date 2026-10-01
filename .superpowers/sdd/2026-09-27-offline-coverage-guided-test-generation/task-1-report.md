# Task 1 implementation report

## Result

Code commit: `5ea895b16d3a8f4dd3ae3104fd2f732e6e4c8788` (`feat: pin linux llvm coverage toolset`). The report is committed separately.

The Linux Clang probe now retains separate `clang`, `clang++`, `llvm-profdata`, and `llvm-cov` executable evidence from one canonical installation, matches tool versions, and computes one role-bound, path-free digest. Linux pinning rechecks the four native file identities and SHA-256 digests, holds distinct C and C++ compiler handles, and rejects missing/mixed/mutable/synthetic evidence. The existing Windows three-tool `clang-cl` identity and toolset paths remain unchanged. Runtime no longer synthesizes a Linux Clang instrumentation fingerprint when verified coverage evidence is missing.

## Changed files

- `apps/test-service/internal/toolchain/model.go`, `model_test.go`: four-role identity and validation tests.
- `apps/test-service/internal/toolchain/gnu.go`, `gnu_test.go`: best-effort four-tool Linux probe and mutation/version/root tests.
- `apps/test-service/internal/coveragellvm/toolset_nonwindows.go`, `toolset_nonwindows_test.go`: Unix file and installation pinning, verification, and rejection tests.
- `apps/test-service/internal/coveragellvm/toolset.go`: necessary shared Toolset extension for a distinct Linux C++ compiler, while Windows keeps its three-tool view.
- `apps/test-service/internal/runtime/coverage_backend.go`, `coverage_backend_test.go`: require a verified four-tool identity for Linux Clang and remove the synthetic fallback.

## Test evidence

- Red phase: `go test ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime` failed because the new identity API did not exist and runtime accepted a Linux Clang snapshot without evidence.
- `go test -count=1 ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime`: PASS for all three packages on Windows.
- `go test ./apps/test-service/...`: PASS across the Go module on Windows (the root `go test ./...` pattern is not valid for this `go.work` layout).
- Cross-compiled Linux test binaries for all three focused packages. Under WSL, the complete `toolchain` and `coveragellvm` binaries returned PASS; the focused runtime capability test `TestCoverageSnapshotAcceptsOnlySupportedPlatformFamilies` returned PASS. The full Linux runtime binary had one unrelated source-location failure: `TestAnchorLinknameIsRestrictedToRuntimeProductionBridge` tries to open its Windows build path, unavailable in this WSL mount.
- `git diff --check`: clean before commit.

## Concerns / follow-up

- The existing `CoverageCapability` schema still stores absolute tool paths (as Windows and GCC already do). The *toolset identity* is a digest and serializes without paths; removing path fields from capability serialization would require a registry/storage redesign outside this task.
- At the initial commit, Linux Clang reused the retained `clang-cl`-specific instrumentation fingerprint. Review round 1 below corrects this by keeping Linux runtime coverage unavailable; a Linux-specific producer/contract remains necessary in a later Phase 10 task.
- A temporary `C:\codex_project\.phase10-linux-tests` directory containing three cross-compiled test binaries was created to execute under WSL. Shell cleanup was rejected by local policy, so it remains outside the worktree.

## Review round 1 fixes

Code commit: `efae65a0b2ae40e8b791f9126e65b6c498fcf160` (`fix: verify linux llvm versions and close runtime capability`).

- T1-01: `toolchain/model.go` now parses one unambiguous version anywhere in a bounded LLVM banner. `gnu.go` uses it for both utilities; the success fixture covers `llvm-profdata\r\nLLVM version ...` and `llvm-cov\r\nLLVM version ...`, and an ambiguous two-version banner is rejected.
- T1-02: Linux `PinToolset` now runs bounded, empty-environment `--version` probes for each of the four pinned executable paths, verifying file identity and digest before and after every probe. The Linux tests use executable scripts with real version banners and reject both a recomputed false version identity and a recomputed mixed-tool identity.
- T1-03: Runtime now returns `ErrInvalidToolchain` for Linux Clang even with four-tool evidence, since the only retained LLVM instrumentation contract targets `clang-cl`. The regression test confirms no snapshot or Windows-only fingerprint is advertised.

### Red/green verification

- Before these fixes, `go test -run TestCoverageSnapshotAcceptsOnlySupportedPlatformFamilies ./apps/test-service/internal/runtime` failed on Linux Clang advertising the Windows-only fingerprint.
- Before these fixes, cross-compiled Linux test binaries under WSL failed `TestLinuxClangProbeRetainsFourVerifiedCoverageTools` (multiline banner dropped coverage) and `TestLinuxPinToolsetRejectsSelfConsistentFalseAndMixedVersionClaims` (both false and mixed version claims accepted).
- After the fixes, `go test -count=1 ./apps/test-service/internal/toolchain ./apps/test-service/internal/coveragellvm ./apps/test-service/internal/runtime`: PASS for all three packages on Windows.
- After the fixes, `GOOS=linux go test -c` for each focused package, followed by WSL execution of complete `toolchain` and `coveragellvm` test binaries and the focused runtime `TestCoverageSnapshotAcceptsOnlySupportedPlatformFamilies` test: PASS for each.
- After the fixes, `go test ./apps/test-service/...`: PASS across the Windows Go module. `git diff --check`: clean.

The full Linux runtime test binary was not rerun because its unrelated `TestAnchorLinknameIsRestrictedToRuntimeProductionBridge` requires the source tree at the Windows build path, inaccessible inside the WSL mount; the focused runtime regression was run and passed. Linux coverage remains intentionally unavailable at runtime until a reviewed Linux instrumentation producer is supplied. The prior capability-path serialization and temporary WSL binary concerns remain unchanged.
