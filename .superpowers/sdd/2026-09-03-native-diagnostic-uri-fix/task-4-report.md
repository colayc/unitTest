# Task 4 Qualification Report

**Status:** DONE_WITH_CONCERNS

Portable command transcriptions in this report use `<worktree>` for the required worktree root. Location checks are a separate audit control and are intentionally recorded verbatim below, including the absolute worktree path they printed. No claim is made that those location-check lines are redacted. Every command was run with the shell located at that exact worktree.

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
```

## Pinned environment

- Node: `v24.19.0` from the Codex primary runtime (repository requirement: `24.18.0`).
- pnpm: `11.4.0`, invoked via the fixed Node executable and system Corepack JavaScript; ambient pnpm was not used.
- Go: `go1.26.6 windows/amd64`, matching `.go-version`.
- Go cache: `<worktree>/.superpowers/runtime/gocache`.
- CMake: pinned bundle at `<worktree>/.bundled-tools/cmake/4.3.4/win32-x64/cmake-4.3.4-windows-x86_64/bin`.

## Focused Go packages

Command (PowerShell; `<worktree>` denotes the exact required absolute worktree path):

```powershell
Set-Location '<worktree>'
Set-Location 'apps\test-service'
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
go test ./internal/diagnostic ./internal/task -count=1
```

Exit code: `0`.

```text
ok  	unit-test-ide.local/test-service/internal/diagnostic	13.220s
ok  	unit-test-ide.local/test-service/internal/task	0.637s
```

## Native-fixture tests

Required Vitest command through pinned pnpm:

```powershell
Set-Location '<worktree>'
& <pinned-node> <corepack-js> pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
```

Exit code: `1`.

```text
'vitest' is not recognized as an internal or external command,
operable program or batch file.
undefined
[ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL] Command "vitest" not found
```

Vitest is therefore unavailable in this repository environment and this required command is not recorded as a pass. The brief-mandated diagnostic fallback used the repository-installed TypeScript compiler and pinned Node.

TypeScript build command:

```powershell
Set-Location '<worktree>'
& <pinned-node> node_modules\typescript\bin\tsc -b tools\service-probe\tsconfig.json
```

Exit code: `0`. Output: empty.

Node fallback command:

```powershell
Set-Location '<worktree>'
& <pinned-node> --test tools\service-probe\dist\native-fixture.test.js
```

Exit code: `0`.

```text
✔ copyNativeFixture creates an isolated workspace without modifying the repository fixture (20.3874ms)
✔ copyNativeFixture supports a new workspace directory containing spaces and Unicode (9.7474ms)
✔ safe fixture copy rejects symlink or junction content without following it (6.3636ms)
✔ copyNativeFixture rejects names and fixture keys that could escape the destination (3.6336ms)
✔ fixture entry ordering is deterministic by Unicode code point (0.7341ms)
✔ normalizeNativeDiagnostic handles Windows paths before normalizing separators (1.2101ms)
✔ normalizeNativeDiagnostic handles POSIX paths and prioritizes the build root (0.4084ms)
✔ normalizeNativeDiagnostic maps strict workspace URIs to the workspace root (1.219ms)
✔ normalizeNativeDiagnostic leaves invalid workspace URIs unchanged (0.3412ms)
✔ normalizeNativeDiagnostic maps explicitly trusted external roots without exposing host paths (0.4697ms)
✔ normalization preserves diagnostic identity and unrelated source text (0.318ms)
✔ normalization does not treat a sibling path as a child root (0.5859ms)
✔ normalization keeps a foreign file URI authority outside local roots (0.214ms)
✔ tracked golden diagnostics keep the cross-compiler minimum contract (5.1086ms)
✔ MSVC linker fixture uses a supported unresolved entrypoint option (0.7287ms)
ℹ tests 15
ℹ suites 0
ℹ pass 15
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 158.0926
```

## Release update tests

Command:

```powershell
Set-Location '<worktree>'
& <pinned-node> --test tools/release/update.test.mjs
```

Exit code: `0`.

```text
✔ installVersion publishes a verified first install before switching current (106.4852ms)
✔ installVersion compares the closed artifact file set independently of recursive traversal order (65.9063ms)
✔ package-backed production smoke executes the installed CLI before and after rollback (4316.9153ms)
✔ package-backed smoke fails closed when the installed CLI script is missing (1888.1419ms)
✔ package-backed smoke fails closed when the installed CLI exits nonzero (1954.1524ms)
✔ package-backed smoke reports a timed out launch handshake usefully (79.377ms)
✔ package-backed smoke reports whitespace-only first-launch stdout usefully (73.3272ms)
✔ install-smoke completes the full lifecycle for real makeappx packages with a spaced launcher (23962.7274ms)
✔ installVersion leaves current unchanged and publishes no version when verification fails (53.0224ms)
✔ installVersion upgrades atomically and rejects a downgrade outside rollback (117.4596ms)
✔ installVersion compares arbitrarily large semver-like numeric identifiers without rounding (69.3535ms)
✔ installVersion compares prerelease identifiers with case-sensitive SemVer ASCII ordering (67.8367ms)
✔ rollbackVersion recovers after a simulated launch failure and is repeatable (129.0503ms)
✔ rollbackVersion refuses a tampered installed version without changing current (102.3787ms)
✔ uninstall removes only the package-owned root and preserves sibling user data (54.2683ms)
✔ uninstall refuses to remove a root that is not package-owned (3.5263ms)
✔ installVersion rejects an intermediate reparse parent before creating package data (16.2944ms)
✔ installVersion rejects a redirected package-owned versions directory (63.6286ms)
✔ package-backed smoke lifecycle emits digest-bound non-secret evidence (184.2162ms)
✔ smoke CLI fails closed when package input is absent (194.4664ms)
✔ smoke CLI fails closed when package digest is absent (192.8453ms)
✔ smoke CLI fails closed when baseline package is absent (190.3738ms)
✔ smoke CLI fails closed when baseline package digest is absent (195.0601ms)
✔ smoke CLI fails closed when baseline manifest digest is absent (193.6309ms)
✔ smoke CLI fails closed when evidence output is absent (190.2967ms)
✔ install smoke workflow downloads digest-bearing produced packages before exercising wrappers (1.936ms)
✔ Linux packaging keeps trusted coordinates and transported mode validation ahead of staging (1.0021ms)
﹣ Linux install smoke restores both AppImage execute bits before either verifier runs (0.6332ms) # SKIP
ℹ tests 28
ℹ suites 0
ℹ pass 27
ℹ fail 0
ℹ cancelled 0
ℹ skipped 1
ℹ todo 0
ℹ duration_ms 34773.4403
```

The sole skip is the test's declared Linux-only case on this Windows host.

## Interim diff and environment audit

Command:

```powershell
Set-Location '<worktree>'
git diff --check 4f2b8a3..HEAD
```

Exit code: `0`. Output: empty.

Command:

```powershell
Set-Location '<worktree>'
git status --short
```

Exit code: `0`.

```text
?? .release/
```

The retained `.release/` evidence directory predates this task and remains untracked.

Process scan command checked `unit-test-service`, `Code - OSS`, `cmake`, `ctest`, `cl`, `MSBuild`, and `VCTIP`. Exit code: `0`.

```text
NONE
```

## Remaining gates

- Repository pinned `pnpm verify`: passed after an environment-permission retry; evidence below.
- Windows native E2E using `CMAKE_GENERATOR=Visual Studio 17 2022` and `CMAKE_GENERATOR_TOOLSET=version=14.44.35207`: failed after the complete MSVC family when the required global toolset variable reached the clang-cl Ninja preset; exact evidence below.
- Final process/diff/status audit: passed after removing only generated residue. The full-matrix report/no-leak gate remains unqualified because the runner writes that report only after both toolchain families complete.

## Repository verification

Exact pinned command shape (the absolute paths represented by placeholders are the fixed values listed in the pinned environment section):

```powershell
Set-Location '<worktree>'
Get-Location | Select-Object -ExpandProperty Path
$node='<pinned-node>'
$corepack='<corepack-js>'
$cmake=(Resolve-Path '.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin').Path
$wrapper=(Resolve-Path '.release\evidence\cli-handshake-local').Path
$env:PATH="$cmake;$wrapper;<pinned-node-directory>;$env:PATH"
$env:GOCACHE=(Resolve-Path '.superpowers\runtime\gocache').Path
& $node $corepack pnpm verify
```

Location output:

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
```

The first restricted attempt exited `1` in `tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs`. CMake selected Visual Studio 17 2022, but MSBuild could not read `C:\Users\DELL\AppData\Local\Microsoft SDKs` (`Access is denied`). Before that failure, generated protocol and coverage checks, all TypeScript/Go builds, 94 producer tests, 4 coverage-generator tests, 28 CMake-bundle tests, and 35 coverage-bundle tests had no failures. This was treated as a sandbox blocker, not a product failure.

The same command was rerun with access to the installed Windows SDK/MSBuild metadata. Exit code: `0`.

Key exact suite summaries from the successful rerun:

```text
workspace smoke: tests 27, pass 27, fail 0
service probe package: tests 79, pass 78, fail 0, skipped 1
Code-OSS extension: tests 139, pass 139, fail 0
focused native-fixture cases within package suite: all reported PASS
go test ./apps/test-service/...: all listed packages ok / no test files
go test -race ./apps/test-service/...: all listed packages ok / no test files
service E2E:
ℹ tests 20
ℹ suites 0
ℹ pass 20
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 198987.4843
```

The successful rerun included no generated-file drift diagnostic and completed the full repository `verify` script.

## Windows native E2E

Command:

```powershell
Set-Location '<worktree>'
Get-Location | Select-Object -ExpandProperty Path
$node='<pinned-node>'
$corepack='<corepack-js>'
$cmake=(Resolve-Path '.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin').Path
$wrapper=(Resolve-Path '.release\evidence\cli-handshake-local').Path
$env:PATH="$cmake;$wrapper;<pinned-node-directory>;$env:PATH"
$env:GOCACHE=(Resolve-Path '.superpowers\runtime\gocache').Path
$env:CMAKE_GENERATOR='Visual Studio 17 2022'
$env:CMAKE_GENERATOR_TOOLSET='version=14.44.35207'
& $node $corepack pnpm test:e2e:native
```

Location output:

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
```

Exit code: `1`.

Scenario/output sequence (the final pnpm path is redacted as `<worktree>` only to retain the public no-path rule):

```text
$ pnpm --filter @unit-test-ide/service-probe test:e2e:native
$ node build-service.mjs && pnpm run build && node dist/native-run.js
$ tsc -b tsconfig.json
native-e2e: msvc default-build
native-e2e: msvc secondary-target
native-e2e: msvc configure-invalidation
native-e2e: msvc typed-rejections
native-e2e: msvc cancellation-reconnect
native-e2e: msvc cancellation-recovery
native-e2e: msvc timeout
native-e2e: msvc preset-build
native-e2e: msvc compiler-diagnostic
native-e2e: msvc linker-diagnostic
native-e2e: msvc configure-diagnostic
native-e2e: msvc workspace-boundaries
native-e2e: msvc service-recovery
native-e2e: clang-cl default-build
native-e2e: clang-cl secondary-target
native-e2e: clang-cl configure-invalidation
native-e2e: clang-cl typed-rejections
native-e2e: clang-cl cancellation-reconnect
native-e2e: clang-cl cancellation-recovery
native-e2e: clang-cl timeout
native-e2e: clang-cl preset-build
native-e2e: native task 5af4e478fc458d282a661f6c01b19e7f finished with command_failed, want succeeded; steps=configure:failed:command_failed; diagnostics=CMAKE_ERROR,CMAKE_CONFIGURE_FAILED; messages=Generator | CMake configure failed; native-errors=; output=CMake Error at CMakeLists.txt:2 (project):
  Generator

    Ninja

  does not support toolset specification, but toolset

    version=14.44.35207

  was specified.

-- Configuring incomplete, errors occurred!

<worktree>\tools\service-probe:
[ERR_PNPM_RECURSIVE_RUN_FIRST_FAIL] @unit-test-ide/service-probe@0.1.0 test:e2e:native: `node build-service.mjs && pnpm run build && node dist/native-run.js`
Exit status 1
[ELIFECYCLE] Command failed with exit code 1.
```

This was not a timeout. The process produced current scenario output throughout and terminated itself after the clang-cl preset configuration failure. The mandated environment is internally incompatible with that scenario: the repository's clang-cl preset uses Ninja, while CMake rejects any generator toolset specification for Ninja. The environment variables were not removed or narrowed because doing so would weaken the prescribed gate.

### MSVC compiler-diagnostic URI evidence

The MSVC compiler-diagnostic scenario completed and the runner advanced to `msvc linker-diagnostic`, then completed the remainder of the MSVC family. In this runner, advancement is possible only after `runFailureScenario` normalizes each emitted diagnostic and `assertGoldenDiagnostics` satisfies the tracked `compiler-msvc-clang-cl.json` minimum. The exact tracked requirement exercised here is:

```json
{
  "kind": "compiler",
  "severity": "error",
  "file": "<workspace>/src/main.cpp",
  "line": 3,
  "codePattern": "^(C[0-9]+|COMPILER_ERROR)$",
  "messageContains": "UNIT_TEST_IDE_UNKNOWN_IDENTIFIER"
}
```

Therefore the MSVC scenario provides evidence that its normalized diagnostic used `sourceUri=<workspace>/src/main.cpp` and contained no absolute native source path. The required full matrix did not finish, so `.native-e2e/artifacts/windows/toolchain-report.json` was not written (`NO_TOOLCHAIN_REPORT`). Consequently, the full-matrix report/no-leak acceptance criterion is not claimed as passed.

## Final cleanup and audit

The first post-run scoped process scan found only one orphaned `vctip.exe` (PID 4116), created during the qualification run from MSVC `14.43.34808`; its recorded parent PID no longer existed. It was stopped explicitly and a subsequent fresh scan checked `unit-test-service`, `Code - OSS`, `cmake`, `ctest`, `cl`, `clang-cl`, `MSBuild`, `ninja`, `link`, `lld-link`, and `VCTIP`.

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
NONE
```

The verification-generated `tools/coverage-bundle/runner/__pycache__/` directory was resolved and checked to be exactly inside `<worktree>` before recursive removal. No source file was removed or modified.

Final diff check command and output:

```powershell
Set-Location '<worktree>'
Get-Location | Select-Object -ExpandProperty Path
git diff --check 4f2b8a3..HEAD
```

Exit code: `0`.

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
```

Final status command and output:

```powershell
Set-Location '<worktree>'
Get-Location | Select-Object -ExpandProperty Path
git status --short
```

Exit code: `0`.

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
?? .release/
```

No generated tracked source change remains. The retained untracked `.release/` directory predates this task and was not modified as source. The evidence report exists only at the required target-worktree path; the accidentally created main-checkout copy was moved away and confirmed absent.

## Final qualification decision

`DONE_WITH_CONCERNS`

- Focused Go: PASS.
- Required Vitest invocation: unavailable (`vitest` not installed); mandated TypeScript/Node fallback: PASS, 15/15.
- Release update tests: PASS, 27 passed / 0 failed / 1 declared Linux-only skip.
- Full pinned repository `pnpm verify`: PASS after rerunning with access to installed Windows SDK metadata.
- Required Windows native E2E: FAIL at clang-cl Ninja preset because the mandated global MSVC toolset environment is unsupported by Ninja.
- MSVC compiler-diagnostic: passed its exact `<workspace>/src/main.cpp` golden assertion before the later unrelated family failure.
- Full matrix report/no-leak acceptance: unqualified because report publication occurs only after both families succeed.
- Final residual-process scan: NONE.
- `git diff --check 4f2b8a3..HEAD`: PASS.
- Final tracked worktree state: no new tracked or generated-source changes; only retained untracked `.release/`.

No remote write, release, signing, packaging, producer, protocol, or broad source change was made.

## Final-review fix qualification addendum

**Status:** `DONE_WITH_CONCERNS` only because the required Vitest executable is absent. The source fix, pinned repository verification, and split Windows native matrix passed.

The final review found that `PublicURI` used canonical/final-path containment but computed its relative path from the lexical input. The old implementation was reproduced before modification:

```text
=== RUN   TestWindowsPublicURIUsesCanonicalPathForJunctionAlias
PublicURI("file:///.../source-alias/main.cpp") = "workspace:///../source-alias/main.cpp", want canonical workspace path
--- FAIL: TestWindowsPublicURIUsesCanonicalPathForJunctionAlias
```

`workspace.Root.RelativePath` now performs final-path resolution, containment, and `filepath.Rel` over the same canonical path. `PublicURI` uses that one operation. The Windows test also confirms that the parser's ordinary `FileURI` remains the lexical alias URI. POSIX symlink coverage asserts the same contract; it was cross-compiled on this Windows host.

### Focused path tests

Command:

```powershell
Set-Location '<worktree>\apps\test-service'
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
go test ./internal/diagnostic -run 'TestWindowsPublicURIUsesCanonicalPath' -count=1 -v
```

Exit code: `0`. This run had access to writable D:/E: scratch space, so the cross-volume case executed rather than skipped.

```text
=== RUN   TestWindowsPublicURIUsesCanonicalPathForJunctionAlias
--- PASS: TestWindowsPublicURIUsesCanonicalPathForJunctionAlias
=== RUN   TestWindowsPublicURIUsesCanonicalPathAcrossVolumeAlias
--- PASS: TestWindowsPublicURIUsesCanonicalPathAcrossVolumeAlias
PASS
ok  unit-test-ide.local/test-service/internal/diagnostic  0.104s
```

Command:

```powershell
Set-Location '<worktree>\apps\test-service'
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
go test ./internal/diagnostic ./internal/task -count=1
```

Exit code: `0`.

```text
ok  unit-test-ide.local/test-service/internal/diagnostic  25.988s
ok  unit-test-ide.local/test-service/internal/task        0.605s
```

POSIX compile command:

```powershell
$env:GOOS='linux'
$env:GOARCH='amd64'
go test -c ./internal/diagnostic -o '<worktree>\.superpowers\runtime\diagnostic-linux.test'
```

Exit code: `0`; the temporary test binary was removed immediately afterward.

### Vitest status and fallback

The exact pinned invocation remained unavailable:

```powershell
& <pinned-node> <corepack-js> pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
```

Exit code: `1`.

```text
11.4.0
'vitest' is not recognized as an internal or external command,
operable program or batch file.
undefined
[ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL] Command "vitest" not found
```

Vitest is not recorded as passing. The requested diagnostic fallback was rerun:

```powershell
& <pinned-node> node_modules\typescript\bin\tsc -b tools\service-probe\tsconfig.json
& <pinned-node> --test tools\service-probe\dist\native-fixture.test.js
```

Exit code: `0`; `15` passed, `0` failed, `0` skipped.

### Pinned full verification

Command shape:

```powershell
Set-Location '<worktree>'
$env:PATH='<pinned-cmake>;<existing-cli-wrapper>;<pinned-node-directory>;' + $env:PATH
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
Remove-Item Env:CMAKE_GENERATOR -ErrorAction SilentlyContinue
Remove-Item Env:CMAKE_GENERATOR_TOOLSET -ErrorAction SilentlyContinue
& <pinned-node> <corepack-js> pnpm verify
```

Versions: Node `v24.19.0`; pnpm `11.4.0`. Exit code: `0`.

Key final summaries:

```text
service probe package: tests 79, pass 78, fail 0, skipped 1
Code-OSS extension: tests 139, pass 139, fail 0
go test ./apps/test-service/...: all listed packages ok / no test files
go test -race ./apps/test-service/...: all listed packages ok / no test files
service E2E: tests 20, pass 20, fail 0, skipped 0
```

### Split Windows native matrix

MSVC was run alone with only its compatible pinned generator environment:

```powershell
$env:CMAKE_GENERATOR='Visual Studio 17 2022'
$env:CMAKE_GENERATOR_TOOLSET='version=14.44.35207'
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc'
& <pinned-node> --input-type=module -e '<invoke runNativeMatrix for requiredFamilies:["msvc"]>'
```

Exit code: `0`.

```json
{"family":"msvc","version":"19.44.35228.0","generator":"Visual Studio 17 2022","scenarioCount":16,"failed":0}
```

The MSVC `compiler-diagnostic` scenario passed its tracked `<workspace>/src/main.cpp` golden requirement before the matrix reported success.

clang-cl was run alone after explicitly removing both global CMake generator variables:

```powershell
Remove-Item Env:CMAKE_GENERATOR -ErrorAction SilentlyContinue
Remove-Item Env:CMAKE_GENERATOR_TOOLSET -ErrorAction SilentlyContinue
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='clang-cl'
& <pinned-node> --input-type=module -e '<invoke runNativeMatrix for requiredFamilies:["clang-cl"]>'
```

The first attempt exited `1` at `clang-cl preset-build`: Service selected Clang `22.1.8`, while the ambient PATH resolved the preset's `clang-cl` to a Swift-bundled Clang `19.1.5`. This was not recorded as a product pass:

```text
Error: clang-cl preset build did not identify Clang 22.1.8
The CXX compiler identification is Clang 19.1.5 with MSVC-like command-line
```

The matching discovered executable was present in `<llvm-22-bin>`. A single corrected environment run placed that directory first on PATH, retained the explicit absence checks for `CMAKE_GENERATOR` and `CMAKE_GENERATOR_TOOLSET`, and reran the same clang-cl family matrix. Exit code: `0`.

```json
{"family":"clang-cl","version":"22.1.8","generator":"Ninja","scenarioCount":16,"failed":0}
```

The two successful family reports were combined through the repository's existing `writeNativeToolchainReport` implementation. The standard Windows report contains both families, the same pinned CMake `4.3.4` identity, and `32` passed scenario entries with no failures or skips. A report scan for drive-qualified paths, UNC paths, `file:` URIs, usernames, and the worktree name returned:

```text
REPORT_PATH_SCAN_NONE
```

### Final cleanup evidence

The verification-created `tools/coverage-bundle/runner/__pycache__/` was resolved to an absolute path and verified to be a descendant of the required worktree before removal. No source path was removed.

A fresh process scan checked `unit-test-service`, `Code - OSS`, `cmake`, `ctest`, `cl`, `clang-cl`, `MSBuild`, `ninja`, `link`, `lld-link`, and `VCTIP`:

```text
PROCESS_SCAN_NONE
```

The final source commit is `759f5c9` (`fix: canonicalize diagnostic public URI paths`). No remote write was performed.
