# Task 5 report — atomic install, rollback, uninstall, and license evidence

## Status

DONE_WITH_CONCERNS

## Files changed

- `tools/release/update.mjs`
- `tools/release/update.test.mjs`
- `tools/release/install-smoke.ps1`
- `tools/release/install-smoke.sh`
- `tools/release/license-audit.mjs`
- `tools/release/license-audit.test.mjs`
- `.github/workflows/foundation.yml`
- `docs/security.md`

## Implementation summary

- Added `installVersion(root, artifact)` using a package ownership marker, an exclusive transition lock, a temporary sibling under `versions/`, full closed-manifest verification before and after copying, manifest fsync where supported, rename publication, and an atomically renamed `current` pointer.
- Added downgrade rejection with non-lossy semver-like comparison, including arbitrarily large numeric identifiers.
- Added `rollbackVersion(root, version)` that re-verifies the retained target on every call, permits an explicit downgrade, keeps every known version, and supports repeated rollback without deleting the last known-good version.
- Added `uninstall(root)` that refuses unowned roots and removes only the marker-verified package root, preserving sibling workspace/user data.
- Rejected direct and intermediate symlink/junction redirection before package-root creation or removal.
- Added `auditLicenses(stagingRoot)` over digest-bearing `{path,size,sha256}` release records, with exact top-level license file closure, digest checks, coverage dependency/wheel lock closure, required Python/gcovr materials, and CMake license coverage.
- Consumed both source bundle manifests and the actual packaged layouts produced by Tasks 2–4: CMake install-root license layout and coverage `manifest.resolved.json`.
- Added a license-audit CLI that emits only closed JSON evidence tied to product/version/platform/source commit.
- Added PowerShell and Bash clean-machine smoke wrappers. They create a disposable root, execute first install and launch handshake, install a deliberately launch-failing upgrade, force rollback, repeat rollback, uninstall, verify user-data preservation and package residue absence, emit path-free JSON evidence, and remove the disposable root.
- Added protected Windows/Linux install-smoke jobs after the corresponding package jobs and wired digest-bearing license audit evidence into both package artifacts.
- Documented ownership, atomicity, rollback, license, user-data, and non-secret evidence boundaries in `docs/security.md`.

## TDD evidence

### Initial red run

Command:

```powershell
node --test tools/release/update.test.mjs tools/release/license-audit.test.mjs
```

Result: both suites failed with `ERR_MODULE_NOT_FOUND` for `update.mjs` and `license-audit.mjs`.

### Additional red/green slices

- Real platform smoke script: failed because `install-smoke.ps1` did not exist, then passed after the wrapper and lifecycle CLI were implemented.
- Existing packaged CMake install-root layout: failed because the audit required an absent outer manifest, then passed while still requiring a digest-listed CMake notice.
- Existing packaged coverage layout: failed because the audit required an absent source manifest, then passed by consuming the closed `manifest.resolved.json` wheel lock.
- License-audit CLI: failed because no evidence file was written, then passed with closed path-free JSON.
- Intermediate reparse parent: install unexpectedly followed the junction, then passed after canonical parent-chain rejection before any write.
- Large semver-like identifier downgrade: install was incorrectly accepted due to numeric rounding, then passed after `BigInt` comparison.

## Verification

### Complete release suite with pinned Node 24

Command:

```powershell
$node='C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin\node.exe'
$tests=rg --files tools/release -g '*.test.mjs'
& $node --test $tests
```

Result: `56` tests passed, `0` failed, `0` skipped. This includes manifest/staging, Windows MSIX with the real SDK path, Linux AppImage contracts, all Task 5 transitions, both license-layout contracts, the audit CLI, and the real Windows smoke wrapper.

### Workspace smoke

Command: pinned Node/pnpm `test:workspace` with the fixed CMake executable.

Result: `23/24` passed. The unrelated CMake-helper fixture failed in Node `fs.cp` with the managed environment denial `EPERM: operation not permitted, stat 'C:\Users\DELL'`, the same sandbox limitation recorded by earlier task reports.

### Static checks

- `node --check tools/release/update.mjs`
- `node --check tools/release/license-audit.mjs`
- PowerShell parser validation for `install-smoke.ps1`
- `git diff --check`

## Self-review

- A failed artifact verification happens before the package-owned root is claimed, and never changes `current`.
- A copy or post-copy verification failure removes only the temporary version directory.
- Publication and pointer replacement use sibling rename boundaries; supported filesystems receive manifest, pointer, and directory fsync calls.
- Update and rollback serialize through a fail-closed exclusive lock; a stale lock blocks mutation instead of allowing concurrent state changes.
- Installed artifacts are exact closed file sets and reject unknown files, duplicate paths, digest/size drift, and reparse entries.
- Uninstall requires a closed ownership marker and cannot claim a non-empty foreign directory.
- Smoke and audit evidence contain no absolute paths, environment, token, user name, or workspace content.

## Concerns

- Linux Bash execution and the GitHub Actions jobs cannot be run natively from this Windows workspace; Bash syntax is simple and the Linux job executes it explicitly with `bash`, but native runner evidence remains CI-owned.
- Full workspace smoke is blocked at one pre-existing sandbox-sensitive `fs.cp` test by `EPERM` on `C:\Users\DELL`; the directly relevant complete release suite is green.

## Review remediation — 2026-08-25

### Status

DONE_WITH_CONCERNS

### Findings resolved

- Coverage license expectations are now selected from the release platform. Source manifests filter wheels by `platforms`; packaged layouts consume `manifest.resolved.json`. Every selected wheel must have a complete dependency notice, catalog entries unknown to the tracked cross-platform source lock are rejected, and valid notices for another platform do not make that wheel mandatory. The regression builds the Linux resolved wheel list from the real coverage lock and proves that omitting Windows-only `colorama` passes.
- Prerelease identifiers now follow SemVer precedence using numeric/non-numeric rules and case-sensitive ASCII/code-unit lexical comparison. The uppercase/lowercase downgrade regression is green.
- Every package-owned `versions` path component is checked for symlink/junction/reparse redirection and canonical containment before use, after creation, and after version publication. Install refuses a `versions` junction redirected outside the package root without writing the new version there; rollback and uninstall share the guard.
- Smoke no longer creates synthetic package versions. Windows and Linux package jobs now produce a lower baseline package plus the requested package, export filename/version/package SHA-256/manifest SHA-256 outputs, and upload both. Smoke jobs download that exact artifact first. The wrappers verify and extract the MSIX/AppImage, materialize the exact closed payload, run the real `app/code-oss --version` handshake, upgrade, rollback twice, uninstall, and preserve sibling user data under disposable roots.
- Smoke evidence is closed and path-free while binding `packageFilename`, `version`, `packageSha256`, and `manifestSha256`. CLI regressions reject missing package input, package digest, or evidence output, and a workflow contract test requires artifact download before either platform wrapper.

### TDD evidence

- The Linux real-lock regression initially failed because `colorama@0.4.6` was treated as a mandatory bundled Linux dependency.
- The case-sensitive prerelease regression initially reached a Windows case-colliding version directory instead of rejecting the downgrade.
- The internal `versions` junction regression initially wrote `2.0.0` through the redirected path.
- The unknown dependency notice regression initially passed and then failed closed after the tracked source catalog check was added.

### Verification

- Focused Task 5 suite: `25` passed, `0` failed, `0` skipped.
- Complete `tools/release/**/*.test.mjs` suite with pinned Node 24.18.0: `64` passed, `0` failed, `0` skipped.
- `node --check` passed for both Task 5 modules.
- PowerShell AST parsing passed for `install-smoke.ps1`.
- The workflow parsed successfully with the installed YAML 2.9.0 parser.
- `git diff --check` passed.

### Remaining concerns

- Native AppImage wrapper execution and the GitHub Actions packaging/smoke jobs remain CI-owned because this workspace is Windows. The complete cross-platform release contract suite is green, including native Windows SDK MSIX packaging/verifier tests.
- The earlier unrelated workspace `fs.cp` sandbox `EPERM` concern is unchanged; no workspace-wide rerun was required for these review-specific changes.
