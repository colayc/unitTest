# Final Fix Report

## Status

`DONE_WITH_CONCERNS`: the canonical/final path mapping defect is fixed and the full split Windows native matrix passed. The only remaining qualification concern is that this repository does not install Vitest, so the required Vitest command still exits `1`; the pinned TypeScript + Node fallback passes and is not mislabeled as Vitest.

## Commits

- `759f5c9` — `fix: canonicalize diagnostic public URI paths`
- Evidence/report changes are in the commit containing this report.

No remote write, push, PR, merge, release, packaging, signing, producer, protocol, or schema action was performed.

## Source fix

Root cause: `PublicURI` accepted a path through `Root.Contains`, which resolves symlinks/junctions to a final path, but then called `filepath.Rel` with the original lexical path. An out-of-root alias targeting an in-root child therefore produced `workspace:///../...`; an alias on another volume could make `filepath.Rel` fail and return the original absolute `file:` URI.

`workspace.Root.RelativePath` now resolves the final path once, checks containment against that path, and computes the relative path from that same canonical value. `PublicURI` consumes that result directly. Ordinary diagnostic parser `FileURI` generation is unchanged.

Regression coverage:

- POSIX symlink alias outside root to an in-root child.
- Windows junction alias outside root to an in-root child.
- Windows junction on a writable second volume targeting an in-root child.
- Ordinary parser `FileURI` remains the lexical alias while only `PublicURI` becomes `workspace:///src/main.cpp`.

The pre-fix RED run produced `workspace:///../source-alias/main.cpp`. The post-fix Windows junction and cross-volume tests both passed. POSIX-specific tests cross-compiled successfully on Windows.

## Exact qualification results

All commands ran from the required worktree. Portable command transcriptions use `<worktree>`, `<pinned-node>`, `<corepack-js>`, `<pinned-cmake>`, and `<llvm-22-bin>`; the separate location audit intentionally printed the absolute worktree path and is not described as redacted.

### Location

```powershell
Get-Location | Select-Object -ExpandProperty Path
```

Exit code: `0`.

```text
C:\codex_project\unitTest\.worktrees\fix-cli-launch-handshake
```

### Go path regression and focused packages

```powershell
Set-Location '<worktree>\apps\test-service'
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
go test ./internal/diagnostic -run 'TestWindowsPublicURIUsesCanonicalPath' -count=1 -v
go test ./internal/diagnostic ./internal/task -count=1
```

Both commands exited `0`. The first executed, rather than skipped, both the same-volume junction and writable-second-volume junction cases. The focused package result was:

```text
ok  unit-test-ide.local/test-service/internal/diagnostic  25.988s
ok  unit-test-ide.local/test-service/internal/task        0.605s
```

POSIX build-tag compilation:

```powershell
$env:GOOS='linux'
$env:GOARCH='amd64'
go test -c ./internal/diagnostic -o '<worktree>\.superpowers\runtime\diagnostic-linux.test'
```

Exit code: `0`; temporary binary removed.

### Vitest and diagnostic fallback

```powershell
& <pinned-node> <corepack-js> pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
```

Exit code: `1`.

```text
'vitest' is not recognized as an internal or external command,
operable program or batch file.
undefined
[ERR_PNPM_RECURSIVE_EXEC_FIRST_FAIL] Command "vitest" not found
```

This command is not claimed as a pass.

```powershell
& <pinned-node> node_modules\typescript\bin\tsc -b tools\service-probe\tsconfig.json
& <pinned-node> --test tools\service-probe\dist\native-fixture.test.js
```

Exit code: `0`; tests `15`, pass `15`, fail `0`, skipped `0`.

Release update fallback-adjacent regression command:

```powershell
& <pinned-node> --test tools\release\update.test.mjs
```

Exit code: `0`; tests `28`, pass `27`, fail `0`, skipped `1` (declared Linux-only case).

### Full pinned repository verification

```powershell
$env:PATH='<pinned-cmake>;<existing-cli-wrapper>;<pinned-node-directory>;' + $env:PATH
$env:GOCACHE='<worktree>\.superpowers\runtime\gocache'
Remove-Item Env:CMAKE_GENERATOR -ErrorAction SilentlyContinue
Remove-Item Env:CMAKE_GENERATOR_TOOLSET -ErrorAction SilentlyContinue
& <pinned-node> <corepack-js> pnpm verify
```

Node `v24.19.0`, pnpm `11.4.0`; exit code `0`. The final service E2E summary was `20` passed, `0` failed. All listed Go and Go race packages completed as `ok` or `[no test files]`.

### Windows native matrix

MSVC environment and invocation:

```powershell
$env:CMAKE_GENERATOR='Visual Studio 17 2022'
$env:CMAKE_GENERATOR_TOOLSET='version=14.44.35207'
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='msvc'
& <pinned-node> --input-type=module -e '<runNativeMatrix: win32, requiredFamilies=["msvc"]>'
```

Exit code: `0`. Result: MSVC `19.44.35228.0`, Visual Studio 17 2022, `16/16` report scenarios passed. `compiler-diagnostic` passed its `<workspace>/src/main.cpp` tracked golden assertion.

clang-cl environment and invocation:

```powershell
Remove-Item Env:CMAKE_GENERATOR -ErrorAction SilentlyContinue
Remove-Item Env:CMAKE_GENERATOR_TOOLSET -ErrorAction SilentlyContinue
$env:UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS='clang-cl'
$env:PATH='<llvm-22-bin>;' + $env:PATH
& <pinned-node> --input-type=module -e '<runNativeMatrix: win32, requiredFamilies=["clang-cl"]>'
```

The first run without the LLVM 22 PATH prefix stopped at `clang-cl preset-build`: selected Clang `22.1.8`, executed Clang `19.1.5`. It was recorded as exit `1`, not a pass. After placing the matching installed LLVM `22.1.8` bin first on PATH, while continuing to assert that both CMake generator variables were absent, the corrected run exited `0`: clang-cl `22.1.8`, Ninja, `16/16` report scenarios passed.

The existing report writer combined the two successful family reports into the standard Windows report: two families, `32` passed scenario fields, no failed or skipped scenario. The JSON contains no drive-qualified path, UNC path, `file:` URI, username, or worktree-name match (`REPORT_PATH_SCAN_NONE`).

## Cleanup and final audits

The generated `__pycache__` directory was removed only after its resolved absolute path was checked to remain under the required worktree.

Fresh residual-process scan names:

```text
unit-test-service, Code - OSS, cmake, ctest, cl, clang-cl,
MSBuild, ninja, link, lld-link, VCTIP
```

Result: `PROCESS_SCAN_NONE`.

## Self-review

- Canonical containment and relative mapping use one final-path value; there is no accepted-inside/lexical-relative split.
- The regression test would fail if `PublicURI` returned a `..` workspace URI or the original cross-volume absolute `file:` URI.
- Parser `FileURI` behavior is explicitly asserted unchanged.
- Cross-volume coverage actually executed with extended test permissions; it was not reported from a skipped run.
- MSVC toolset pinning is scoped only to MSVC. clang-cl/Ninja explicitly runs without the toolset variable.
- Vitest remains an exact blocker and is not conflated with the successful fallback.
- Report wording distinguishes portable placeholder transcription from the intentionally absolute location audit.

## Concerns

- Vitest is absent from workspace binaries, so the required Vitest command remains unavailable.
- The machine PATH contains a Swift-bundled clang-cl `19.1.5` ahead of the independently discovered LLVM `22.1.8`; reproducible clang-cl native qualification must keep the selected LLVM bin ahead of that ambient entry.
