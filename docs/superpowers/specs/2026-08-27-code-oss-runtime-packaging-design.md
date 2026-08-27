# Complete Code-OSS Runtime Packaging Design

## Goal

Replace the Phase 8 release pipeline's single-file Code-OSS input with a complete, validated Code-OSS runtime directory on Windows and Linux. The packaged MSIX and AppImage must preserve the upstream runtime layout, bind every runtime file into `release-manifest.json`, and launch the real platform executable on a clean machine.

## Problem

The current staging contract accepts `--code-oss <file>` and copies only that file to `app/code-oss.exe` or `app/code-oss`. A packaged Code-OSS build is not a standalone executable: it also requires its sibling `resources`, `locales`, DLL, PAK, data, and other runtime files. The isolated executable can start a process but cannot provide reliable `--version` output or clean-machine runtime evidence.

The confirmed Windows build demonstrates the required layout:

- runtime root: `D:\project\VSCode-win32-x64`
- launcher: `Code - OSS.exe`
- version: `1.92.0`
- source commit: `b1c0a14de1414fcdaa400695b4db1c0799bc3124`
- architecture: `x64`
- launcher SHA-256: `1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672`

The complete runtime succeeds through `bin/code-oss.cmd --version`, while the copied executable alone does not produce the required version evidence.

## Selected Approach

Consume a complete runtime directory and retain the existing platform launcher SHA-256 variables as the external trust anchor. GitHub Actions verifies the launcher digest in the downloaded trusted artifact. The local staging command independently requires the same digest, validates the runtime identity, copies the complete tree, and verifies the staged launcher digest again.

This approach is preferred over a separately hashed archive because it preserves the current workflow inputs and avoids adding archive creation and safe extraction as a second packaging system. It is preferred over selecting individual dependencies because the upstream Code-OSS runtime layout changes across versions.

## Scope

This change covers both Windows and Linux runtime consumption:

- validate a complete Code-OSS runtime directory;
- copy the complete runtime into deterministic staging;
- bind all runtime files and Code-OSS license notices;
- update MSIX and AppImage launch paths and verification;
- update install, upgrade, rollback, uninstall, and qualification-facing smoke behavior;
- update GitHub Actions and documentation.

Producing or uploading the trusted Code-OSS artifacts is a separate immediately-following task. This design does not download Code-OSS, build Code-OSS, or upload local files to GitHub.

## Runtime Input Contract

The staging CLI replaces:

```text
--code-oss <file>
```

with two required arguments:

```text
--code-oss-root <directory>
--code-oss-sha256 <64 lowercase hexadecimal characters>
```

The programmatic `stageRelease(input)` contract replaces `codeOss` with:

```js
{
  codeOssRoot: string,
  codeOssSha256: string
}
```

The old `--code-oss` argument is rejected as an unknown flag. There is no silent fallback to the unsafe single-file behavior.

### Windows runtime root

The downloaded artifact root must directly contain:

```text
Code - OSS.exe
resources/app/product.json
resources/app/package.json
```

It must also retain the complete set of sibling runtime files and directories created by the upstream `vscode-win32-x64` build.

### Linux runtime root

The downloaded artifact root must directly contain:

```text
code-oss
resources/app/product.json
resources/app/package.json
```

The `code-oss` file must be executable. The artifact must retain the complete sibling runtime layout created by the upstream `vscode-linux-x64` build.

## Staged Layout

The runtime tree is copied without renaming its contents:

```text
app/code-oss-runtime/<complete upstream runtime>
```

The fixed launchers are:

```text
Windows: app/code-oss-runtime/Code - OSS.exe
Linux:   app/code-oss-runtime/code-oss
```

All paths beneath `app/code-oss-runtime/` are classified as `runtime` artifacts. Every regular file is recorded in the closed release manifest with its relative path, size, SHA-256, and executable flag.

## Runtime Validator

A focused module, `tools/release/code-oss-runtime.mjs`, owns runtime-specific validation. It exports a function equivalent to:

```js
validateCodeOssRuntime({ root, platform, expectedLauncherSha256 })
```

and returns the canonical root, canonical launcher, launcher-relative path, and validated product identity needed by staging.

It also exposes a CLI:

```powershell
node tools/release/code-oss-runtime.mjs `
  --platform windows `
  --root 'D:\project\VSCode-win32-x64' `
  --launcher-sha256 1c777e2ee43bacf066ae344142c25adabd21cfa09ba7e7a9dc9da6d0185a8672
```

Successful CLI output is closed JSON containing only the platform, launcher-relative path, launcher SHA-256, application name, product name, and license name. It does not expose absolute host paths.

## Validation and Security Boundaries

Validation fails closed unless all of the following hold:

1. The runtime root is a real directory and not a symbolic link, junction, or reparse point.
2. Every descendant is a regular file or real directory; symbolic links, junctions, reparse points, devices, sockets, and other special entries are rejected.
3. Every relative path is portable: no absolute path, drive or colon path, backslash, empty segment, `.` segment, or `..` segment is accepted.
4. The tree has no case-insensitive path aliases, so a Linux artifact cannot create ambiguous Windows or ZIP paths.
5. The platform launcher exists at the exact case-sensitive root-relative path and is a real regular file.
6. The Linux launcher has at least one executable mode bit.
7. The expected launcher digest is a lowercase SHA-256 and matches the source launcher.
8. `resources/app/product.json` and `resources/app/package.json` are real regular files within the validated root.
9. `product.json` is a closed identity check for `nameShort: "Code - OSS"`, `applicationName: "code-oss"`, and `licenseName: "MIT"`.
10. After staging, the copied launcher digest is recomputed and must still match the expected digest.

The staging operation continues to build in a temporary sibling directory. Any validation, copy, digest, manifest, license, or normalization failure removes the temporary directory and never publishes the final staging root.

## License Handling

The runtime tree remains byte-for-byte under `app/code-oss-runtime/`. In addition, every file whose basename matches the existing license rule (`LICENSE*`, `NOTICE*`, or `COPYING*`) is copied into the same relative location beneath:

```text
licenses/code-oss/
```

Those copies join the existing CMake and coverage notices in the release manifest's closed `licenses` list and in `license-audit.mjs`. This includes the Code-OSS MIT notice, Chromium notices, and bundled extension notices present in the upstream build.

## Windows Data Flow

1. `package-windows` downloads `code-oss-windows-x64` into `.release/inputs/windows-code-oss`.
2. The artifact root itself is the runtime root; a wrapper directory is not accepted.
3. The workflow requires exactly one root-level `Code - OSS.exe` and verifies it against `RELEASE_CODE_OSS_WINDOWS_SHA256`.
4. The workflow exports `CODE_OSS_RUNTIME_ROOT` and passes `--code-oss-root` plus `--code-oss-sha256` to staging.
5. Staging publishes the complete tree under `app/code-oss-runtime/`.
6. `AppxManifest.xml` launches `app\code-oss-runtime\Code - OSS.exe` as `Windows.FullTrustApplication`.
7. MSIX verification requires exactly one executable manifest artifact at that launcher path and verifies every additional runtime payload entry against the release manifest.

## Linux Data Flow

1. `package-linux` downloads `code-oss-linux-x64` into `.release/inputs/linux-code-oss`.
2. The artifact root itself is the runtime root; a wrapper directory is not accepted.
3. The workflow requires exactly one root-level `code-oss`, verifies its executable bit, and verifies its digest against `RELEASE_CODE_OSS_LINUX_SHA256`.
4. The workflow exports `CODE_OSS_RUNTIME_ROOT` and passes `--code-oss-root` plus `--code-oss-sha256` to staging.
5. Staging publishes the complete tree under `app/code-oss-runtime/`.
6. AppRun, desktop `Exec`, desktop `TryExec`, AppImage digest metadata, and AppImage verification use `usr/lib/unit-test-ide/app/code-oss-runtime/code-oss`.
7. AppImage verification rejects a missing, non-executable, unmanifested, or digest-mismatched launcher and rejects any tampered runtime dependency.

## Install and Rollback Smoke

The update harness derives the platform launcher path from the new fixed layout. First-install and rollback launches execute the installed runtime launcher with `--version` and an isolated user-data root. The forced-upgrade failure test corrupts only the installed target version's fixed launcher, observes a real launch failure, rolls back to the manifest-verified baseline, and launches the restored baseline successfully.

Package extraction and installation continue to verify every payload entry against the release manifest before launch. No runtime file is downloaded or repaired during installation or launch.

## Error Behavior

Runtime validation uses stable release error categories:

- `RELEASE_INPUT_MISSING`: required root, launcher, product metadata, or package metadata is absent;
- `RELEASE_INPUT_INVALID`: product identity, path shape, entry type, executable mode, or digest syntax is invalid;
- `RELEASE_INPUT_DIGEST_MISMATCH`: source or staged launcher bytes do not match the pinned digest.

CLI failures print the stable error code and a non-secret message, never an environment dump or absolute path inventory. Workflow checks use the same categories in their failure messages.

## Testing Strategy

Implementation follows test-driven development.

### Runtime validator tests

- accept complete Windows and Linux fixture roots;
- reject a single launcher file passed as a root;
- reject missing launcher, `resources/app/product.json`, and `resources/app/package.json`;
- reject incorrect Code-OSS product identity;
- reject malformed and mismatched SHA-256 values;
- reject a non-executable Linux launcher;
- reject symbolic links, junctions/reparse points where supported, special entries, unsafe paths, and case-insensitive aliases;
- prove CLI output is closed and path-free.

### Staging tests

- prove the complete runtime tree is copied under `app/code-oss-runtime/`;
- prove every runtime file is present in `release-manifest.json` with the correct digest;
- prove Code-OSS notices are copied into `licenses/code-oss/` and accepted by the license audit;
- prove a post-copy launcher mismatch removes temporary output and publishes no final root;
- prove identical inputs produce byte-identical normalized staging trees.

### Platform packaging tests

- update Windows MSIX entry-point and verification expectations;
- reject a tampered runtime resource or locale in MSIX, not only a tampered launcher;
- update Linux AppRun, desktop, manifest, and verification expectations;
- reject a tampered runtime resource in AppImage, not only a tampered launcher.

### Smoke and workflow tests

- update install and rollback tests to the new launcher paths;
- prove the forced-failure smoke corrupts only the installed target launcher;
- assert workflow jobs verify the root-level platform launcher and pass both new staging arguments;
- assert the old single-file staging flag is absent and rejected.

### Verification commands

The final implementation must pass:

```powershell
node --test tools/release/code-oss-runtime.test.mjs
node --test tools/release/stage.test.mjs tools/release/license-audit.test.mjs
node --test tools/release/windows/package-msix.test.mjs
node --test tools/release/linux/package-appimage.test.mjs
node --test tools/release/update.test.mjs tools/release/qualification.test.mjs
node --test (rg --files tools/release -g '*.test.mjs')
pnpm test
git diff --check
```

After automated verification, the real Windows input is validated with the independent runtime CLI using the confirmed root and launcher digest. Linux real-runtime validation remains required when the trusted Linux runtime is available.

## Success Criteria

The change is complete when:

- no release path accepts a single-file Code-OSS runtime;
- Windows and Linux staging preserve their complete upstream runtime directories;
- platform packages launch the fixed runtime paths;
- all runtime and license files are digest-bound in release evidence;
- all focused and full project tests pass;
- the confirmed Windows runtime passes the independent validator;
- the repository remains fail-closed until a trusted Linux runtime and clean-machine package evidence are supplied.
