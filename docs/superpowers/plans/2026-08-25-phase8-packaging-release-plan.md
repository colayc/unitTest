# Phase 8 Packaging and Release Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver reproducible Windows and Linux release artifacts that contain the Code-OSS runtime input, extension, Go service, verified CMake/coverage bundles, license notices, and bounded install/update/rollback/uninstall evidence.

**Architecture:** A Node.js ESM release tool creates a deterministic staging tree and a closed `release-manifest.json` before any platform packaging. Windows produces an MSIX package with `makeappx` and optional `signtool` signing; Linux produces an AppImage from an AppDir and publishes a SHA-256 manifest. The update/rollback harness operates on versioned directories with an atomic active pointer, and all release jobs fail closed when a required runtime, digest, license, or signing input is absent.

**Tech Stack:** Node.js 24.18.0, pnpm 11.4.0, JSON Schema, PowerShell 7, Bash, Windows SDK `makeappx`/`signtool`, pinned `appimagetool`, Go service binary, Code-OSS extension build output.

## Global Constraints

- Product runtime must not download CMake, Python, gcovr, LLVM, or other tools at runtime.
- Every packaged executable and bundle entry must be listed with a SHA-256 digest in `release-manifest.json`.
- Runtime IPC remains per-user local IPC with the existing token and workspace-trust boundaries.
- Release scripts reject absolute paths, symlinks/reparse points, unknown manifest keys, duplicate artifact IDs, and artifacts outside the staging root.
- A development artifact may be unsigned but must publish a digest; a release artifact must be signed when `RELEASE_SIGNING_REQUIRED=1`.
- Missing `CODE_OSS_EXECUTABLE`, prepared CMake bundle, prepared coverage bundle, or service binary is a hard `RELEASE_INPUT_MISSING` failure.
- Install, upgrade, rollback, and uninstall tests run against temporary roots and never mutate the developer profile or system-wide state.
- GitHub Actions release jobs run only after `verify-linux` and `verify-windows`; pull requests never receive signing credentials.

---

### Task 1: Freeze the release manifest contract

**Files:**
- Create: `tools/release/release-config.json`
- Create: `tools/release/manifest.schema.json`
- Create: `tools/release/manifest.mjs`
- Create: `tools/release/manifest.test.mjs`
- Modify: `package.json`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- `buildReleaseManifest(input)` consumes `{version, platform, architecture, stagingRoot, artifacts, licenses, sourceCommit}` and returns a closed manifest object.
- Each artifact is `{id, kind, relativePath, size, sha256, executable}`; `relativePath` is POSIX-style and relative to the staging root.
- The manifest contains only `schemaVersion`, `product`, `version`, `platform`, `architecture`, `sourceCommit`, `artifacts`, `licenses`, and `generatedAt`.

- [ ] **Step 1: Write the failing contract tests**

  Add tests for: valid deterministic ordering; rejection of absolute paths; rejection of `..` traversal; rejection of duplicate IDs; rejection of digest/size mismatch; rejection of unknown top-level keys; and omission of `generatedAt` from the deterministic digest input.

- [ ] **Step 2: Run the focused test and verify it fails**

  Run:

  ```powershell
  node --test tools/release/manifest.test.mjs
  ```

  Expected: FAIL because `tools/release/manifest.mjs` does not yet export `buildReleaseManifest`.

- [ ] **Step 3: Implement the minimal manifest builder and schema**

  Use `realpath`/`relative` checks to keep every artifact under `stagingRoot`, calculate SHA-256 with `crypto.createHash('sha256')`, sort artifacts by `id`, and validate the result against `manifest.schema.json` with the repository's existing AJV dependency. Use a fixed `schemaVersion` of `1` and reject a release version that is not semver-like.

- [ ] **Step 4: Run focused tests and workspace smoke**

  Run:

  ```powershell
  node --test tools/release/manifest.test.mjs
  pnpm test:workspace
  ```

  Expected: all focused tests pass and the workspace documentation smoke remains green.

- [ ] **Step 5: Commit the contract slice**

  ```powershell
  git add tools/release package.json tools/workspace-smoke/workspace-smoke.test.mjs
  git commit -m "feat: define release manifest contract"
  ```

### Task 2: Build a deterministic release staging tree

**Files:**
- Create: `tools/release/stage.mjs`
- Create: `tools/release/stage.test.mjs`
- Modify: `package.json`
- Modify: `README.md`

**Interfaces:**
- CLI: `node tools/release/stage.mjs --platform <windows|linux> --architecture <x64> --version <semver> --code-oss <file> --service <file> --cmake-root <dir> --coverage-root <dir> --out <dir>`.
- `stageRelease(input)` copies the Code-OSS runtime, extension `dist`, Go service, CMake bundle, coverage bundle, and license notices into `out/staging/<version>/<platform>-<architecture>/` and writes `release-manifest.json`.

- [ ] **Step 1: Write failing staging tests**

  Build a temporary fixture tree and assert that the staged layout contains `app/code-oss`, `app/extensions/unit-test-ide`, `service/unit-test-service`, `bundles/cmake`, `bundles/coverage`, and `licenses`; assert that a missing input returns `RELEASE_INPUT_MISSING` before creating a partial manifest.

- [ ] **Step 2: Run the focused test and verify it fails**

  ```powershell
  node --test tools/release/stage.test.mjs
  ```

  Expected: FAIL because the staging module and CLI do not exist.

- [ ] **Step 3: Implement staging with fail-closed input checks**

  Resolve each input once, reject reparse points and paths outside the supplied roots, copy with byte-for-byte preservation, include `tools/cmake-bundle/manifest.json`, `tools/coverage-bundle/manifest.json`, and all existing license files, then call `buildReleaseManifest` over the final tree.

- [ ] **Step 4: Add package scripts and documentation**

  Add `release:stage` and document that `CODE_OSS_EXECUTABLE` is a required build input; do not make the normal test or runtime scripts download anything.

- [ ] **Step 5: Run focused tests and commit**

  ```powershell
  node --test tools/release/stage.test.mjs tools/release/manifest.test.mjs
  git add tools/release package.json README.md
  git commit -m "feat: stage reproducible release inputs"
  ```

### Task 3: Package and verify Windows MSIX

**Files:**
- Create: `tools/release/windows/AppxManifest.xml.template`
- Create: `tools/release/windows/package-msix.ps1`
- Create: `tools/release/windows/verify-msix.ps1`
- Create: `tools/release/windows/package-msix.test.mjs`
- Modify: `.github/workflows/foundation.yml`

**Interfaces:**
- `package-msix.ps1 -StagingRoot <dir> -Output <file> -Version <semver> -Publisher <subject>` creates an MSIX with the staged application and manifest.
- `verify-msix.ps1 -Package <file> -Manifest <file> [-RequireSignature]` checks package identity, embedded manifest, file list, SHA-256 release manifest, and signature policy.

- [ ] **Step 1: Write failing packaging tests**

  Test that missing `makeappx.exe` returns `RELEASE_TOOL_MISSING`, unsigned development output is accepted only when `RELEASE_SIGNING_REQUIRED=0`, and `RELEASE_SIGNING_REQUIRED=1` without a certificate returns `RELEASE_SIGNING_REQUIRED`.

- [ ] **Step 2: Run focused tests and verify failure**

  ```powershell
  node --test tools/release/windows/package-msix.test.mjs
  ```

  Expected: FAIL because the packaging scripts do not exist.

- [ ] **Step 3: Implement MSIX packaging and verification**

  Generate the manifest from the staged version, call the Windows SDK tools by resolved absolute path, sign only when the certificate and password environment variables are present, and verify the resulting package before publishing it.

- [ ] **Step 4: Add a protected release workflow job**

  Add a `package-windows` job that needs `verify-windows` and `verify-linux`, runs only on a version tag or an explicit manual dispatch, never exposes signing secrets to pull requests, uploads the MSIX and release manifest, and fails if verification does not pass.

- [ ] **Step 5: Run tests and commit**

  ```powershell
  node --test tools/release/windows/package-msix.test.mjs
  git add tools/release/windows .github/workflows/foundation.yml
  git commit -m "feat: add verified Windows release packaging"
  ```

### Task 4: Package and verify Linux AppImage

**Files:**
- Create: `tools/release/linux/AppRun`
- Create: `tools/release/linux/unit-test-ide.desktop`
- Create: `tools/release/linux/package-appimage.mjs`
- Create: `tools/release/linux/verify-appimage.mjs`
- Create: `tools/release/linux/package-appimage.test.mjs`
- Modify: `.github/workflows/foundation.yml`

**Interfaces:**
- `packageAppImage({stagingRoot, output, appimagetool, version, architecture})` creates an AppDir and AppImage without network access.
- `verifyAppImage({image, manifest, requireDigest})` checks the AppImage digest, embedded release manifest, executable launcher, and offline runtime layout.

- [ ] **Step 1: Write failing AppImage tests**

  Test missing `appimagetool` and missing `AppRun` as hard failures, verify the generated desktop entry points at the staged launcher, and verify that the produced manifest has no network URL or absolute host path.

- [ ] **Step 2: Run focused tests and verify failure**

  ```powershell
  node --test tools/release/linux/package-appimage.test.mjs
  ```

  Expected: FAIL because the AppImage tooling does not exist.

- [ ] **Step 3: Implement offline AppImage packaging**

  Use a pinned `appimagetool` digest supplied by the repository release configuration; construct `AppDir/usr/lib/unit-test-ide`, preserve the staged release manifest, and make `AppRun` reject network-dependent setup.

- [ ] **Step 4: Add the Linux package job**

  Add `package-linux` with the same `verify-linux`/`verify-windows` dependency and release trigger as Windows, then upload the AppImage and manifest.

- [ ] **Step 5: Run tests and commit**

  ```powershell
  node --test tools/release/linux/package-appimage.test.mjs
  git add tools/release/linux .github/workflows/foundation.yml
  git commit -m "feat: add verified Linux release packaging"
  ```

### Task 5: Add atomic install, upgrade, rollback, and uninstall evidence

**Files:**
- Create: `tools/release/update.mjs`
- Create: `tools/release/update.test.mjs`
- Create: `tools/release/install-smoke.ps1`
- Create: `tools/release/install-smoke.sh`
- Create: `tools/release/license-audit.mjs`
- Create: `tools/release/license-audit.test.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/security.md`

**Interfaces:**
- `installVersion(root, artifact)` installs into `versions/<version>` and atomically updates `current` only after manifest verification.
- `rollbackVersion(root, version)` switches `current` to a previously verified version and never deletes the last known-good version.
- `uninstall(root)` removes only the package-owned root and preserves user workspace data.
- `auditLicenses(stagingRoot)` returns a sorted closed list of license files and fails on an unlisted bundled dependency.

- [ ] **Step 1: Write failing state-transition tests**

  Cover first install, failed verification with no pointer change, upgrade, rollback after a simulated launch failure, repeated rollback, uninstall cleanup, preservation of user data, and missing license notice failure.

- [ ] **Step 2: Run focused tests and verify failure**

  ```powershell
  node --test tools/release/update.test.mjs tools/release/license-audit.test.mjs
  ```

  Expected: FAIL because the update and license-audit modules do not exist.

- [ ] **Step 3: Implement atomic state transitions and license audit**

  Use a temporary sibling directory plus rename for publication, fsync the manifest and pointer where supported, reject downgrade unless explicitly called by rollback, and preserve user data outside the package-owned root.

- [ ] **Step 4: Add clean-machine smoke jobs**

  Run PowerShell on Windows and Bash on Linux against disposable roots; verify install, launch handshake, upgrade, forced rollback, uninstall, and absence of package-owned residue. Upload only non-secret JSON evidence.

- [ ] **Step 5: Run tests, update security documentation, and commit**

  ```powershell
  node --test tools/release/update.test.mjs tools/release/license-audit.test.mjs
  git add tools/release .github/workflows/foundation.yml docs/security.md
  git commit -m "feat: verify release rollback and license boundaries"
  ```

### Task 6: Phase 8 release qualification gate

**Files:**
- Create: `tools/release/qualification.mjs`
- Create: `tools/release/qualification.test.mjs`
- Modify: `.github/workflows/foundation.yml`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`

**Interfaces:**
- `qualifyRelease({linuxEvidence, windowsEvidence, manifests, licenseAudit, signatures})` returns `qualified: true` only when every required evidence record is present and internally consistent.

- [ ] **Step 1: Write failing qualification tests**

  Reject missing platform evidence, skipped install/rollback tests, unsigned required Windows packages, digest mismatches, and license omissions; accept only a complete Windows/Linux evidence set tied to one source commit.

- [ ] **Step 2: Run focused tests and verify failure**

  ```powershell
  node --test tools/release/qualification.test.mjs
  ```

  Expected: FAIL because the qualification module does not exist.

- [ ] **Step 3: Implement the closed qualification report**

  Emit `release-qualification.json` containing only schema version, source commit, package digests, signature outcomes, install/upgrade/rollback/uninstall outcomes, license outcome, and final qualification outcome.

- [ ] **Step 4: Make the release job publish qualification evidence**

  Require `qualification.mjs` to return `qualified: true` before publishing release artifacts; leave the roadmap Phase 8 checkbox unchanged until clean-machine evidence and legal review are attached.

- [ ] **Step 5: Run the complete release test set and commit**

  ```powershell
  node --test tools/release/**/*.test.mjs
  git diff --check
  git add tools/release .github/workflows/foundation.yml docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md
  git commit -m "chore: add Phase 8 release qualification gate"
  ```

## Self-review

- The plan covers the roadmap requirements for signed or digest-verified Windows/Linux packages, bundled tools, license notices, install, upgrade, rollback, uninstall, and high-risk security gates.
- The plan keeps signing credentials out of pull requests and treats missing runtime inputs as hard failures.
- The plan deliberately does not mark Phase 8 complete until clean-machine evidence and final license/legal review exist.

