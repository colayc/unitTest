# Task 4 report — Linux AppImage packaging

## Status

DONE_WITH_CONCERNS

## Files changed

- `.github/workflows/foundation.yml`
- `tools/release/linux/AppRun`
- `tools/release/linux/package-appimage.mjs`
- `tools/release/linux/package-appimage.test.mjs`
- `tools/release/linux/unit-test-ide.desktop`
- `tools/release/linux/verify-appimage.mjs`

## Implementation summary

- Added offline Linux AppImage packaging in `packageAppImage(...)` with:
  - fail-closed staging-root and tool/template validation
  - pinned `appimagetool` SHA-256 verification from release configuration (`RELEASE_APPIMAGETOOL_SHA256` or explicit test input)
  - deterministic AppDir layout rooted at `usr/lib/unit-test-ide`
  - copied staged `release-manifest.json` preserved inside the AppDir
  - generated sidecar digest manifest `${output}.sha256.json` with only relative internal paths and SHA-256 metadata
  - in-process verification before success via `verifyAppImage(...)`

- Added `verifyAppImage(...)` to validate:
  - AppImage file digest against the sidecar manifest
  - embedded release-manifest digest
  - `AppRun` presence/executable bit
  - desktop `Exec=` target against the staged launcher
  - offline runtime layout for every artifact and license listed by the embedded release manifest

- Added `AppRun` launcher script that:
  - unsets proxy environment variables
  - exports offline/runtime-download guard variables
  - rejects explicit network-dependent setup through stable `RELEASE_NETWORK_DEPENDENCY`
  - fails closed if the packaged launcher is missing

- Added `package-linux` workflow job gated the same way as `package-windows`, using repository release vars for:
  - `RELEASE_APPIMAGETOOL_PATH`
  - `RELEASE_APPIMAGETOOL_SHA256`

## TDD record

### Red run

Command:

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Initial result before implementation:

```text
TAP version 13
# Error [ERR_MODULE_NOT_FOUND]: Cannot find module '...\\tools\\release\\linux\\package-appimage.mjs'
not ok 1 - ...\\tools\\release\\linux\\package-appimage.test.mjs
# pass 0
# fail 1
```

### Green run

Command:

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Result:

```text
TAP version 13
# Subtest: packageAppImage fails closed when appimagetool is missing
ok 1 - packageAppImage fails closed when appimagetool is missing
# Subtest: packageAppImage fails closed when AppRun is missing
ok 2 - packageAppImage fails closed when AppRun is missing
# Subtest: packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher
ok 3 - packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher
1..3
# tests 3
# pass 3
# fail 0
```

### Formatting check

Command:

```powershell
git diff --check
```

Result:

```text
warning: LF will be replaced by CRLF in .github/workflows/foundation.yml.
The file will have its original line endings in your working directory
```

## Self-review

- The sidecar digest manifest is intentionally closed and does not emit host-absolute paths or network URLs.
- The fake AppImage envelope in the tests exercises the real verifier logic without depending on a Linux host or a real `appimagetool`.
- The workflow job is scoped to manual/tag packaging only and keeps the tool path/digest in repository release configuration rather than hard-coding a host path.
- The packaging module verifies the pinned `appimagetool` digest before execution and re-verifies the produced AppImage before returning success.

## Concerns

- I did not exercise a real Linux `appimagetool` binary or native `--appimage-extract` flow in this Windows workspace; the focused suite covers the fail-closed and digest/layout contract through a deterministic fake tool.
- The new `package-linux` GitHub Actions job was updated statically but not executed end-to-end here; it depends on repository-side `CODE_OSS_EXECUTABLE`, `RELEASE_APPIMAGETOOL_PATH`, and `RELEASE_APPIMAGETOOL_SHA256` being provisioned correctly.

---

## Fix round 1 — review findings addressed

### Review findings closed

1. Tightened `verifyAppImage(...)` so every extracted artifact file is validated against the embedded `release-manifest.json` entry by exact fixed payload path, size, SHA-256, and executable bit; tampering now fails even if the sidecar digest file is regenerated.
2. Removed the fake JSON envelope from the public verifier path. The production CLI now rejects `marker: UNIT_TEST_IDE_FAKE_APPIMAGE`; tests use an explicit injected extractor hook that is only reachable through the module API.
3. Enforced the fixed AppDir contract instead of trusting sidecar layout redirects:
   - fixed `AppRun`, desktop file, launcher, and embedded manifest paths
   - exact `AppRun` script bytes from the repository template
   - exact desktop file bytes plus explicit `Exec` / `TryExec` launcher checks
   - embedded release-manifest identity checks for `schemaVersion`, `product`, `version`, `platform`, and `architecture`
   - rejection of unexpected payload files outside the closed AppDir file set

### Additional regression tests added

- tampered launcher payload still fails after sidecar digest regeneration
- public CLI fake-envelope rejection
- sidecar launcher-path substitution rejection
- embedded release-manifest identity drift for wrong `product`, `version`, and `schemaVersion`
- desktop `Exec` / `TryExec` mismatch rejection
- unexpected extra payload file rejection

### Red run for the review fixes

Command:

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Observed failing output before the verifier fixes:

```text
not ok 3 - packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher
  error: 'RELEASE_PACKAGING_FAILED: AppImage package input has unexpected key: verificationExtractor'
...
not ok 9 - verifyAppImage rejects unexpected payload files outside the closed AppDir contract
# pass 2
# fail 7
```

### Green run after the fixes

Command:

```powershell
node --test tools/release/linux/package-appimage.test.mjs
```

Result:

```text
TAP version 13
# Subtest: packageAppImage fails closed when appimagetool is missing
ok 1 - packageAppImage fails closed when appimagetool is missing
# Subtest: packageAppImage fails closed when AppRun is missing
ok 2 - packageAppImage fails closed when AppRun is missing
# Subtest: packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher
ok 3 - packageAppImage emits a closed digest manifest and a desktop entry that points at the staged launcher
# Subtest: verifyAppImage rejects a tampered launcher even when the sidecar digest is regenerated
ok 4 - verifyAppImage rejects a tampered launcher even when the sidecar digest is regenerated
# Subtest: public verify CLI rejects a fake AppImage envelope marker
ok 5 - public verify CLI rejects a fake AppImage envelope marker
# Subtest: verifyAppImage rejects sidecar path substitution instead of trusting redirected layout paths
ok 6 - verifyAppImage rejects sidecar path substitution instead of trusting redirected layout paths
# Subtest: verifyAppImage rejects embedded release-manifest identity drift
ok 7 - verifyAppImage rejects embedded release-manifest identity drift
# Subtest: verifyAppImage rejects desktop or launcher contract mismatches
ok 8 - verifyAppImage rejects desktop or launcher contract mismatches
# Subtest: verifyAppImage rejects unexpected payload files outside the closed AppDir contract
ok 9 - verifyAppImage rejects unexpected payload files outside the closed AppDir contract
1..9
# tests 9
# pass 9
# fail 0
```

### Formatting check after the fixes

Command:

```powershell
git diff --check
```

Result:

```text
warning: LF will be replaced by CRLF in tools/release/linux/package-appimage.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/linux/package-appimage.test.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/linux/verify-appimage.mjs.
The file will have its original line endings in your working directory
```

### Self-review for the fix round

- The verifier now trusts the embedded release manifest and fixed repository templates, not mutable sidecar path redirects.
- The fake-envelope path is still available for deterministic tests, but only through an injected extractor supplied by direct module callers; the public CLI rejects it.
- The new negative tests mutate only one contract surface at a time, so each review finding is covered by a distinct failure mode.

### Remaining concerns after the fix round

- License files are now part of the exact closed payload set and must exist at the embedded fixed paths, but the frozen Task 1 release-manifest contract still lists licenses as paths rather than digest-bearing artifact entries, so license-byte verification cannot be as strong as artifact-byte verification without widening that earlier contract.
- I still did not exercise a real Linux `appimagetool` binary or native extraction path in this Windows workspace; the focused suite validates the hard-fail and payload-contract logic through the injected extractor path and the public fake-envelope rejection.

---

## Fix round 2 — cross-task license integrity hardening

### Review ruling implemented

The earlier license-integrity concern is now closed by widening the shared release-manifest contract itself:

- `tools/release/manifest.schema.json` now requires every manifest `licenses[]` entry to be a closed object with:
  - `path`
  - `size`
  - `sha256`
- `tools/release/manifest.mjs` now measures and hashes license bytes under the same guarded path validation used for artifacts.
- `buildReleaseManifest(...)` accepts either legacy input paths or explicit license records, but always emits the widened closed license-record form and rejects duplicate license paths.
- Task 2 staging expectations were updated so staged manifests assert license `path/size/sha256`, not only path presence.

### Packaging verifiers hardened

- `tools/release/windows/verify-msix.ps1`
  - validates each embedded license record against the staged manifest path, size, and SHA-256
  - compares packaged license bytes against the staged bytes and reports license-specific size/hash failures

- `tools/release/linux/verify-appimage.mjs`
  - validates each embedded license record for closed shape and safe path
  - compares extracted packaged license bytes against the embedded `size` and `sha256`
  - keeps the fixed-path closed payload contract from fix round 1

### Additional files changed in this round

- `tools/release/manifest.schema.json`
- `tools/release/manifest.mjs`
- `tools/release/manifest.test.mjs`
- `tools/release/stage.test.mjs`
- `tools/release/windows/verify-msix.ps1`
- `tools/release/windows/package-msix.test.mjs`
- `tools/release/linux/verify-appimage.mjs`
- `tools/release/linux/package-appimage.test.mjs`

### Red run for the cross-task hardening

Command:

```powershell
node --test tools/release/manifest.test.mjs tools/release/stage.test.mjs tools/release/windows/package-msix.test.mjs tools/release/linux/package-appimage.test.mjs
```

Observed failures before the contract/verifier updates included:

```text
RELEASE_VERIFICATION_FAILED: license [object Object] is missing from the AppImage: usr/lib/unit-test-ide/[object Object]
...
release manifest input has unexpected keys: architecture,artifacts,expectedLicenses,licenses,platform,sourceCommit,stagingRoot,version
...
RELEASE_VERIFICATION_FAILED: license is missing from the staged root: @{path=licenses/NOTICE.txt; size=7; sha256=...}
```

These failures confirmed all three old assumptions still existed:

- Linux verifier still treated license entries as path strings
- Windows verifier still treated license entries as path strings
- manifest tests/fixtures still assumed the old path-only output shape

### Green run after the hardening

Command:

```powershell
node --test tools/release/manifest.test.mjs tools/release/stage.test.mjs tools/release/windows/package-msix.test.mjs tools/release/linux/package-appimage.test.mjs
```

Result:

```text
TAP version 13
...
1..39
# tests 39
# pass 39
# fail 0
```

Covered suites:

- `tools/release/manifest.test.mjs`
- `tools/release/stage.test.mjs`
- `tools/release/windows/package-msix.test.mjs`
- `tools/release/linux/package-appimage.test.mjs`

New or widened license-specific coverage now includes:

- manifest license size/hash mismatch rejection
- staged manifest license record assertions
- MSIX packaged license tamper rejection
- AppImage packaged license tamper rejection even after sidecar digest regeneration

### Formatting check after the hardening

Command:

```powershell
git diff --check
```

Result:

```text
warning: LF will be replaced by CRLF in tools/release/linux/package-appimage.test.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/linux/verify-appimage.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/manifest.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/manifest.test.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/stage.test.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/windows/package-msix.test.mjs.
The file will have its original line endings in your working directory
warning: LF will be replaced by CRLF in tools/release/windows/verify-msix.ps1.
The file will have its original line endings in your working directory
```

### Self-review for the hardening round

- The license contract is now symmetric with artifact integrity in every packaging flow: manifest measurement, staging expectations, Windows verification, and Linux verification all work from the same closed record shape.
- I kept the widened contract backward-compatible at the manifest-builder input boundary so the existing stage pipeline did not need a separate intermediate migration layer.
- The new tests mutate only the packaged license payload or manifest-license contract, so failures are attributable to the license-integrity path rather than unrelated package layout checks.

### Remaining concerns after the hardening round

- The focused suites fully cover the widened manifest/license contract, but I still did not run the packaging flows on a native Linux host with a real `appimagetool` extraction path from this Windows workspace.
- `git diff --check` remains clean apart from line-ending warnings from the Windows checkout.
