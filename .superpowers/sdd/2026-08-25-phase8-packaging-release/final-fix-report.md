# Phase 8 Final Fix Report

Date: 2026-08-25

Branch: `codex/phase8-packaging-release`

Commit: the single final-fix commit containing this report; its immutable hash is supplied in the parent handoff because a commit cannot embed its own hash.

## Findings closed

1. Release manifest generation and package staging now require an explicit canonical `SOURCE_DATE_EPOCH`, use its UTC ISO timestamp everywhere, and normalize staging/AppDir/MSIX input timestamps where supported. Regressions compare complete manifest bytes and complete normalized staging snapshots across identical builds.
2. Qualification now validates closed current and rollback manifests, closed lifecycle evidence, canonical identity fields, package filenames/digests, and a strictly older rollback version bound to baseline package and manifest digests. The baseline retains its own valid source commit and generation timestamp, as a real older release should.
3. Windows staging preserves `.exe`; the MSIX declares `app\code-oss.exe` as a full-trust `<Application>`, includes the required visual assets/capability, and smoke launches the installed executable directly. The `.smoke.exe` alias workaround is removed.
4. Release jobs now materialize fixed-name Code-OSS and appimagetool artifacts with a commit-pinned `actions/download-artifact`, narrowly scoped `actions: read`, caller-supplied SHA-256 pins, and ordering tests that require validation before build/package steps. Missing coordinates or mismatched files fail with `RELEASE_INPUT_MISSING`. Signing secrets remain confined to release packaging.
5. Linux and Windows packaging/verifiers reuse one release-manifest semantic validator before payload verification. It rejects unknown keys, noncanonical versions/timestamps, empty or malformed artifact records, unsafe sizes, duplicate IDs/paths, unsorted records, invalid licenses, and identity drift.
6. The plan ends with exactly one newline; `git diff --check` is clean.

## Verification

- Node runtime: `v24.19.0`.
- Complete explicit release enumeration:
  `node --test tools/release/license-audit.test.mjs tools/release/linux/package-appimage.test.mjs tools/release/manifest.test.mjs tools/release/qualification.test.mjs tools/release/stage.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs`
  — 95 passed, 0 failed. This includes the real Windows SDK `makeappx.exe` test.
- Focused qualification/update run — 37 passed, 0 failed.
- Focused malformed-manifest regressions passed for both AppImage and MSIX verifiers.
- PowerShell parsing passed for `install-smoke.ps1`, `package-msix.ps1`, and `verify-msix.ps1`.
- `git diff --check` passed.

## Remaining external evidence constraints

- This repository still does not materialize Code-OSS runtimes itself. A trusted producing workflow run must publish `code-oss-windows-x64/code-oss.exe` and `code-oss-linux-x64/code-oss`, and release dispatch/tag configuration must provide that run ID and the reviewed digests. The workflow now fails closed instead of claiming a runnable package when these inputs are unavailable.
- A trusted `appimagetool-linux-x64/appimagetool-x86_64.AppImage` artifact and reviewed digest are required for a real hosted Linux package run. The Windows environment could exercise the AppImage contract tests but not a native Linux AppImage build.
- Production certificate signing and verification still require the protected release secrets and a real release-environment run; no signing material is exposed to pull requests.
- Legal approval of packaged third-party notices remains an external release gate.

## Blockers

None for the branch fix wave or commit. The constraints above are expected release evidence/materialization prerequisites and are fail-closed in automation.
