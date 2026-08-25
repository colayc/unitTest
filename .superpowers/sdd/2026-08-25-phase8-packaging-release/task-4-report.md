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
