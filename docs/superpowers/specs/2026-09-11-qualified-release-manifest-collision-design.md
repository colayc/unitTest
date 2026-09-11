# Qualified Release Manifest Collision Design

## Context

Trusted producer run `34569459822` and unsigned foundation run `34574201783`
both completed successfully from GitHub `master` commit
`33806efdcda20453f0dfdd69b97016913336cb09`. Package qualification proved that
the Windows and Linux packages, install-smoke evidence, and license audits were
valid. Manual inspection of `qualified-release-0.1.0-1` then found that its flat
file set contained only seven files and only one platform release manifest.

The two package jobs intentionally produce different platform-specific manifest
bytes with the same basename:

- Windows: `unit-test-ide-0.1.0.release-manifest.json`, SHA-256
  `acceafe8fc642f9581ee3f9be46350c61fb5a17a6ae3f9e3e526a0655dba5253`;
- Linux: `unit-test-ide-0.1.0.release-manifest.json`, SHA-256
  `e1e2425154db421c4e6a464548c44e810aac724f5a9dd51f30c15b6d4495aa85`.

The qualification job validates these files in separate platform directories.
Its final staging step then copies both into one flat directory using their
original basenames. The later Linux copy deterministically overwrites the Windows
copy. The qualification report remains true because the overwrite happens after
qualification, and the existing workflow test checks only that a
`qualified-release-*` artifact is uploaded, not its exact contents.

## Goals

- Preserve both qualified platform release manifests in the final flat artifact.
- Give each manifest a stable, platform- and architecture-qualified release asset
  name suitable for eventual direct GitHub Release upload.
- Verify manifest bytes against the package-job SHA-256 outputs before publishing
  the qualified artifact.
- Enforce an exact eight-file top-level qualified release set with no symlinks,
  directories, missing files, extra files, or filename collisions.
- Cover the real assembly behavior with a failing regression test before changing
  the workflow.

## Non-goals

- No change to either package artifact's existing filenames or contents.
- No change to the `release-manifest.json` schema or to the manifest embedded in an
  MSIX or AppImage payload.
- No change to producer trust, run-attempt identity, package qualification,
  install-smoke, license-audit, signing, or publication policy.
- No GitHub Release, Git tag, signing certificate, or paid signing input.
- No attempt to resolve the deferred third-party legal/license manual approval.

## Selected Approach

Add a focused Node.js staging command at
`tools/release/stage-qualified-release.mjs`. The qualification workflow passes the
already qualified package, manifest, license-audit, and qualification files to this
command. It copies them through a temporary sibling directory and publishes the
completed directory only after all validations pass.

The two platform release manifests retain their bytes but receive unique final
asset names:

- `unit-test-ide-<version>.windows-x64.release-manifest.json`;
- `unit-test-ide-<version>.linux-x64.release-manifest.json`.

The Windows MSIX, Linux AppImage, Linux AppImage SHA sidecar, both license audits,
and qualification report retain their existing basenames. The resulting artifact
contains exactly eight top-level regular files.

Two alternatives are rejected:

- Platform subdirectories preserve the original manifest basenames but are not a
  directly uploadable flat GitHub Release asset set.
- Renaming manifest outputs in the package jobs would propagate changes through
  install-smoke and qualification interfaces even though the collision exists only
  in final aggregation.

## Components and Interfaces

### Qualified release staging command

`tools/release/stage-qualified-release.mjs` exports
`stageQualifiedRelease(input) -> Promise<{ outputRoot: string, files: string[] }>`
and exposes the same behavior through a CLI. The input contains:

- canonical release version and output directory;
- Windows package, release manifest, expected manifest SHA-256, and license audit;
- Linux package, AppImage SHA sidecar, release manifest, expected manifest
  SHA-256, and license audit;
- release qualification report.

The command accepts only the canonical source basenames produced by the current
package jobs: `unit-test-ide-<version>.msix`,
`unit-test-ide-<version>.AppImage`,
`unit-test-ide-<version>.AppImage.sha256.json`, two source files named
`unit-test-ide-<version>.release-manifest.json`, `license-audit-windows.json`,
`license-audit-linux.json`, and `release-qualification.json`. Every input must be a
real regular file and not a symbolic link. The two supplied manifest hashes must be
lowercase 64-character hexadecimal values and must match the source bytes before
copying.

The command stages into a new temporary sibling of the requested output directory,
copies each source once, verifies the copied manifest hashes, checks the exact
sorted eight-name file set, and then renames the temporary directory to the final
output. The requested output must not already exist. A failure removes only the
command-owned temporary directory and never publishes a partial final directory.

### Foundation workflow

`.github/workflows/foundation.yml` replaces the inline flat copy sequence with one
call to the staging command. It passes:

- `needs.package-windows.outputs.manifest_sha256` as the Windows expected digest;
- `needs.package-linux.outputs.release_manifest_sha256` as the Linux expected
  digest;
- the existing package-job filenames and qualification evidence paths.

The existing `qualified-release-<version>-<run_attempt>` upload remains unchanged
and runs only after the staging command succeeds.

### Tests

`tools/release/stage-qualified-release.test.mjs` exercises the exported function
with small real files. Its primary regression test expects both renamed manifests,
their distinct bytes, and the exact eight-file closed set. Before implementation,
this test fails because the module does not exist.

Negative tests prove that a wrong manifest digest, a symlinked input, an unexpected
basename, an existing output directory, or a staging failure publishes no final
directory. CLI tests prove unknown and missing arguments fail closed without native
paths or input contents in diagnostics.

`tools/release/qualification.test.mjs` retains qualification behavior tests and
adds a workflow contract assertion that the final job calls the staging command
with both platform digest bindings before uploading `qualified-release-*`.

## Data Flow

1. Platform package jobs produce and hash their existing release inputs.
2. Install-smoke jobs validate install, launch, corrupt-upgrade failure, rollback,
   repeated rollback, uninstall, user-data preservation, and residue absence.
3. The qualification command validates both platform inputs in isolated source
   directories and emits `release-qualification.json`.
4. The workflow requires `qualificationOutcome.qualified === true` and equal
   platform versions.
5. The new staging command independently re-hashes both platform manifests,
   assembles eight uniquely named files, validates the closed file set, and
   atomically publishes `.release/qualified`.
6. `actions/upload-artifact` uploads that complete directory.

## Error Handling

- Invalid CLI arguments, unsafe versions, unexpected basenames, non-regular files,
  symbolic links, digest mismatches, output collisions, or closed-set violations
  fail with the stable prefix `RELEASE_QUALIFIED_STAGING_FAILED`.
- Diagnostics identify only controlled field labels or basenames; they do not emit
  absolute paths, environment variables, file contents, tokens, or secrets.
- No validation failure may leave the requested final output directory.
- Cleanup is restricted to the unique temporary sibling created by the command.
- A staging failure prevents the qualified artifact upload and does not weaken the
  preceding package or qualification result.

## Verification

Implementation follows test-driven development:

1. Add the primary staging regression test and run it to observe the expected RED
   failure because the staging module is absent.
2. Implement the minimal staging module and CLI, then run the focused test GREEN.
3. Add negative staging cases one at a time and keep the focused suite GREEN.
4. Add the workflow contract assertion, observe it fail against the old inline copy
   sequence, update the workflow, and rerun it GREEN.
5. Run `tools/release/stage-qualified-release.test.mjs`,
   `tools/release/qualification.test.mjs`, the broader release test set, and
   `pnpm verify` with Node `24.18.0` and pnpm `11.4.0`.
6. Review the diff, commit the implementation, push the branch to GitHub and Gitee,
   and create an unmerged GitHub PR.

A merge, new producer run, and new unsigned foundation qualification require
separate authorization after the PR checks and review succeed.

## Acceptance Criteria

- The final qualified release contains exactly eight regular top-level files.
- Both platform release manifests exist under their unique final names and retain
  the SHA-256 values supplied by their package jobs.
- No unqualified `unit-test-ide-<version>.release-manifest.json` survives in the
  final flat directory.
- A failed staging validation uploads no qualified artifact and publishes no partial
  output directory.
- Existing package artifact names, embedded manifests, qualification evidence,
  signing policy, and publication policy remain unchanged.
- All focused and repository verification commands pass on the PR branch.
