# Task 1 report — release manifest contract

## Status

DONE_WITH_CONCERNS

## Files changed

- `tools/release/release-config.json`
- `tools/release/manifest.schema.json`
- `tools/release/manifest.mjs`
- `tools/release/manifest.test.mjs`
- `package.json`
- `tools/workspace-smoke/workspace-smoke.test.mjs`

## What changed

- Added a closed release-manifest schema pinned to `schemaVersion: 1` and `product: unit-test-ide`.
- Added `buildReleaseManifest(input)` and `toDeterministicManifestBytes(manifest)`.
- Enforced:
  - semver-like release versions
  - lowercase 40-hex `sourceCommit`
  - POSIX-style relative artifact and license paths
  - rejection of absolute paths and `..` traversal
  - rejection of duplicate artifact IDs
  - rejection of digest and size mismatches
  - rejection of symlink/reparse-like artifact roots/files via `lstat` + `realpath` containment checks
  - deterministic artifact ordering by `id`
- Added the package script:
  - `prepare:release-manifest`
- Extended workspace smoke so the package script and pinned release product identity are covered.

## TDD evidence

### Red run

Command:

```powershell
node --test tools/release/manifest.test.mjs
```

Output:

```text
TAP version 13
# node:internal/modules/esm/resolve:265
#     throw new ERR_MODULE_NOT_FOUND(
#           ^
# Error [ERR_MODULE_NOT_FOUND]: Cannot find module 'C:\\codex_project\\unitTest\\.worktrees\\phase8-packaging-release\\tools\\release\\manifest.mjs' imported from C:\\codex_project\\unitTest\\.worktrees\\phase8-packaging-release\\tools\\release\\manifest.test.mjs
...
not ok 1 - C:\\codex_project\\unitTest\\.worktrees\\phase8-packaging-release\\tools\\release\\manifest.test.mjs
...
# fail 1
```

### Green run

Command:

```powershell
node --test tools/release/manifest.test.mjs
```

Output:

```text
TAP version 13
# Subtest: buildReleaseManifest sorts artifacts deterministically and emits only the closed contract
ok 1 - buildReleaseManifest sorts artifacts deterministically and emits only the closed contract
# Subtest: buildReleaseManifest rejects absolute artifact paths and parent traversal
ok 2 - buildReleaseManifest rejects absolute artifact paths and parent traversal
# Subtest: buildReleaseManifest rejects duplicate artifact ids
ok 3 - buildReleaseManifest rejects duplicate artifact ids
# Subtest: buildReleaseManifest rejects size and digest mismatches
ok 4 - buildReleaseManifest rejects size and digest mismatches
# Subtest: schema rejects unknown top-level keys
ok 5 - schema rejects unknown top-level keys
# Subtest: deterministic manifest bytes omit generatedAt
ok 6 - deterministic manifest bytes omit generatedAt
1..6
# tests 6
# pass 6
# fail 0
```

## Workspace smoke

`pnpm test:workspace` could not be executed with the currently installed global pnpm because the repository pins `11.4.0` and the environment has `11.19.0`.

Observed output:

```text
[ERR_PNPM_UNSUPPORTED_ENGINE] Unsupported environment (bad pnpm and/or Node.js version)
Expected version: 11.4.0
Got: 11.19.0
```

Attempting to bootstrap `pnpm@11.4.0` through corepack also failed in this environment because corepack reported a signature-key mismatch while fetching pnpm:

```text
Internal Error: Cannot find matching keyid: ...
```

To still verify the workspace smoke slice, I ran the exact underlying test command directly, with the bundled CMake path required by the existing CMake-helper smoke:

Command:

```powershell
$env:CMAKE='C:\codex_project\unitTest\.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin\cmake.exe'
node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/workspace-smoke/workspace-config-schema.test.mjs tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs
```

Output:

```text
TAP version 13
# Subtest: UnitTestIDE CMake helper has strict deterministic framework registration
ok 1 - UnitTestIDE CMake helper has strict deterministic framework registration
...
# Subtest: workspace pins supported toolchains
ok 11 - workspace pins supported toolchains
# Subtest: release manifest contract stays pinned to the repository product identity
ok 12 - release manifest contract stays pinned to the repository product identity
...
1..24
# tests 24
# pass 24
# fail 0
```

## Self-review

- The manifest builder validates both input structure and output schema.
- Artifact verification is bound to the supplied staging root through both lexical and canonical path checks.
- Deterministic digest input deliberately omits `generatedAt` and preserves the sorted artifact list.
- The CLI is minimal and currently intended for config validation plus JSON-in/file-out manifest generation.

## Concerns

- I could not complete the exact `pnpm test:workspace` invocation under `pnpm@11.4.0` because the environment’s global pnpm is `11.19.0`, and corepack failed to fetch `11.4.0` due to a signature-key mismatch. The underlying workspace smoke tests themselves passed when run directly with Node and the provided bundled CMake path.

---

## Fix report — 2026-08-25 review follow-up

### Status

DONE

### Issues fixed

1. Added component-by-component path walking from `stagingRoot` to each artifact and license, rejecting intermediate symlinks/junctions before canonical containment checks.
2. Made `--config` drive real manifest generation by adding validated `inputPath` and `outputPath` support, resolving them relative to the config file, and generating the configured output artifact.
3. Re-ran the exact reviewer-specified `pnpm test:workspace` command with pinned Node 24, pnpm 11.4.0, and bundled CMake.
4. Removed the silent default-config reload by passing the same validated config object/path through CLI manifest generation into `buildReleaseManifest`.

### Additional files changed

- `tools/release/release-input.json`
- `tools/release/staging/bin/unit-test-ide.exe`
- `tools/release/staging/share/doc/readme.txt`
- `tools/release/staging/licenses/Python-3.14.6.txt`
- `tools/release/staging/licenses/gcovr-8.6.txt`

### TDD red run for the follow-up fixes

Command:

```powershell
node --test tools/release/manifest.test.mjs
```

Output:

```text
TAP version 13
# Subtest: buildReleaseManifest rejects intermediate junction or symlink parents
not ok 4 - buildReleaseManifest rejects intermediate junction or symlink parents
...
  error: 'Missing expected rejection.'
...
# Subtest: release manifest CLI with --config writes the configured output artifact
not ok 6 - release manifest CLI with --config writes the configured output artifact
...
  error: |
    Command failed: ...tools\release\manifest.mjs --config ...\release-config.json
    release config has unexpected keys: inputPath,outputPath,product,schemaVersion
...
# pass 6
# fail 2
```

### Focused verification with pinned Node 24

Command:

```powershell
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' --test tools/release/manifest.test.mjs
```

Output:

```text
✔ buildReleaseManifest sorts artifacts deterministically and emits only the closed contract (106.8551ms)
✔ buildReleaseManifest rejects absolute artifact paths and parent traversal (37.6205ms)
✔ buildReleaseManifest rejects duplicate artifact ids (20.5481ms)
✔ buildReleaseManifest rejects intermediate junction or symlink parents (11.6454ms)
✔ buildReleaseManifest rejects size and digest mismatches (33.4437ms)
✔ release manifest CLI with --config writes the configured output artifact (406.1638ms)
✔ schema rejects unknown top-level keys (83.4114ms)
✔ deterministic manifest bytes omit generatedAt (16.8875ms)
ℹ tests 8
ℹ pass 8
ℹ fail 0
```

### Exact reviewer-required workspace verification

Command:

```powershell
$env:CMAKE='C:\codex_project\unitTest\.bundled-tools\cmake\4.3.4\win32-x64\cmake-4.3.4-windows-x86_64\bin\cmake.exe'
& 'C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe' 'C:\Users\DELL\AppData\Local\node\corepack\v1\pnpm\11.4.0\dist\pnpm.mjs' test:workspace
```

Output:

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/workspace-smoke/workspace-config-schema.test.mjs tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs
TAP version 13
# Subtest: UnitTestIDE CMake helper has strict deterministic framework registration
ok 1 - UnitTestIDE CMake helper has strict deterministic framework registration
...
# Subtest: release manifest contract stays pinned to the repository product identity
ok 12 - release manifest contract stays pinned to the repository product identity
...
1..24
# tests 24
# pass 24
# fail 0
```

### Self-review

- The intermediate-path guard now covers both artifact paths and license paths, not only final file targets.
- Config validation and resolution now happen once per config path and are reused consistently by CLI generation and manifest building.
- The repo package script now points at a config that actually produces an output manifest artifact from checked-in fixture inputs.
- The exact pinned workspace command now passes without relying on the global Node or pnpm installation.

### Concerns

- None.
