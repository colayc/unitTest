# Formal Packaging Blockers Design

## Context

The first free, unsigned formal qualification run (`33347132685`) accepted trusted
producer run `33343776919` and passed provenance, Windows verification, and Linux
verification. Packaging then exposed two independent blockers:

- Windows job `99357165234` failed with
  `RELEASE_INPUT_MISSING: release manifest generatedAt does not match SOURCE_DATE_EPOCH`.
  PowerShell 7 converts the canonical JSON timestamp to `System.DateTime`; converting
  that value back with the default string formatter produces a culture-specific value
  that cannot equal the original UTC ISO string. Windows PowerShell 5.1 leaves the
  same JSON value as a string, which is why the existing tests did not catch the CI
  behavior.
- Linux job `99357165173` failed because `unit-test-ide.desktop` declares
  `Icon=unit-test-ide`, but the AppDir contains no matching `.png`, `.svg`, or `.xpm`
  file. The real pinned `appimagetool` rejects that incomplete AppDir.

These failures are packaging defects. They do not weaken or invalidate the trusted
producer evidence, but the repository policy requires a fresh producer run and a new
foundation dispatch after the fixes are merged.

## Goals

- Preserve exact `generatedAt` equality with the canonical value derived from
  `SOURCE_DATE_EPOCH` under both Windows PowerShell 5.1 and PowerShell 7.
- Supply a deterministic temporary product icon that satisfies the AppImage desktop
  contract without starting final branding work.
- Keep the AppImage payload closed: the icon must be fixed, byte-verified, and covered
  by the verifier's expected-path set.
- Prove both production failures with regression tests and complete a fresh unsigned
  cross-platform qualification after merge.

## Non-goals

- No release-manifest schema change.
- No relaxation or removal of `generatedAt` validation.
- No change to producer trust, artifact identity, signing, or publication policy.
- No final product logo or broader Code-OSS branding work.
- No GitHub Release and no paid signing inputs.

## Selected Approach

Use two small platform-local fixes rather than adding a new cross-platform metadata
layer.

For Windows, keep Node.js as the authority that validates the release manifest's
closed schema and canonical UTC ISO timestamp. After that validation, normalize only
PowerShell's in-memory representation: preserve a string value from Windows
PowerShell 5.1, or format a `DateTime` value from PowerShell 7 as invariant UTC
`yyyy-MM-dd'T'HH:mm:ss.fff'Z'`. Compare the resulting text with the exact ISO value
derived from `SOURCE_DATE_EPOCH`. Unsupported values fail closed.

For Linux, add a repository-owned deterministic SVG at
`tools/release/linux/unit-test-ide.svg`. The AppImage packager copies it to the AppDir
root as `unit-test-ide.svg`, matching `Icon=unit-test-ide`, and normalizes its
timestamp with the rest of the AppDir. The verifier requires that fixed path, compares
its bytes with the repository asset, requires a non-executable file, and includes it
in the expected-path set.

Two alternatives were rejected:

- A new Node.js metadata extraction interface would avoid PowerShell JSON type
  conversion, but adds a larger interface and subprocess contract for one already
  validated field.
- Switching CI to legacy `powershell.exe` and removing the desktop `Icon` entry would
  hide the compatibility bug and degrade the package contract.

## Components and Data Flow

### Windows MSIX

1. `package-msix.ps1` resolves and validates `SOURCE_DATE_EPOCH`.
2. The existing Node.js manifest validator validates the file's schema, platform,
   version, and canonical timestamp syntax.
3. PowerShell reads the validated manifest.
4. A focused helper returns canonical timestamp text from either the PowerShell 5.1
   string form or the PowerShell 7 `DateTime` form.
5. The script performs an ordinal exact comparison with the source epoch ISO value.
6. Any mismatch or unsupported representation remains `RELEASE_INPUT_MISSING`.

This preserves the existing defense in depth: Node.js validates the original JSON
semantics, while PowerShell independently binds packaging to the workflow epoch.

### Linux AppImage

1. `package-appimage.mjs` validates the fixed SVG source as a real regular file.
2. It copies the SVG to `<AppDir>/unit-test-ide.svg` before timestamp normalization.
3. The real pinned `appimagetool` sees the icon referenced by the desktop file.
4. `verify-appimage.mjs` requires `unit-test-ide.svg`, compares it byte-for-byte with
   the fixed source, rejects executable mode, and keeps it in the closed path set.
5. Missing, altered, redirected, executable, or aliased icon content fails
   verification instead of being ignored.

The icon is package metadata outside `usr/lib/unit-test-ide`; it is not added to the
release-manifest artifact list. Its integrity is enforced by the AppImage verifier in
the same way as `AppRun` and the desktop entry.

## Error Handling

- A missing fixed SVG source is `RELEASE_TEMPLATE_MISSING` before `appimagetool`
  executes.
- A real `appimagetool` failure remains `RELEASE_PACKAGING_FAILED` with bounded tool
  diagnostics.
- A missing or altered embedded icon is `RELEASE_VERIFICATION_FAILED`.
- A Windows timestamp that is not a supported string or `DateTime` representation
  fails closed; it is never coerced to the expected value.
- A genuinely different canonical timestamp continues to fail exact equality.
- No failure path may include signing secrets, tokens, or new environment dumps.

## Testing

Implementation follows test-driven development.

### Windows regression

- Add a test that invokes the package script with `pwsh.exe` and a valid canonical
  manifest. It must fail before the fix and pass afterward.
- Retain Windows PowerShell 5.1 package coverage.
- Exercise matching and mismatching timestamps under both representations.
- Keep existing unsigned and signed-path tests unchanged except for shared helpers
  needed to select the shell host.

### Linux regression

- Require the generated AppDir and fake AppImage envelope to contain the exact fixed
  SVG at `unit-test-ide.svg`.
- Verify its bytes and non-executable mode.
- Add missing, tampered, executable, and unexpected-alias rejection cases.
- Keep the existing closed-payload test so the new path is explicit rather than
  widening the verifier generally.

### Verification sequence

1. Run the two new regression tests in RED state.
2. Implement the minimal fixes and rerun them to GREEN.
3. Run the full Windows package test file, Linux AppImage test file, release contract
   tests, and `pnpm verify`.
4. Push the branch to GitHub and Gitee and open a GitHub PR.
5. Require Linux and Windows PR checks to pass before requesting merge approval.
6. After merge, run a fresh trusted producer workflow on the new `master` commit.
7. Validate the new producer provenance and dispatch a new foundation run with
   `release_version=0.1.0` and `release_signing_required=0`.
8. Require package, install-smoke, and release-qualification jobs to succeed, then
   inspect the short-lived evidence before expiry.

The failed foundation run is retained as diagnostic evidence and is not reused as the
formal qualification.

## Acceptance Criteria

- PowerShell 5.1 and PowerShell 7 accept the same valid manifest and reject a real
  `generatedAt` mismatch.
- The pinned real `appimagetool` accepts the AppDir because the referenced SVG exists.
- AppImage verification rejects missing, modified, executable, or extra icon paths.
- Both repository remotes contain the reviewed fix commit.
- A fresh producer run and fresh unsigned foundation run complete successfully on the
  merged commit.
- No release is published and no signing secret is required.
