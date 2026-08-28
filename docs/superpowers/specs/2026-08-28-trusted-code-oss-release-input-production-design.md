# Trusted Code-OSS Release Input Production Design

## Goal

Produce complete, trusted Windows and Linux x64 Code-OSS runtime artifacts on standard GitHub-hosted runners, bind them to a closed provenance record, and let the existing Phase 8 packaging workflow consume them for a free unsigned test release.

The first real release run uses version `0.1.0` and `release_signing_required=0`. It must execute Windows MSIX and Linux AppImage packaging, both install lifecycle smoke jobs, and the release qualification gate. This work does not claim that Phase 8 is complete: formal Windows signing and final license/legal review remain separate requirements.

## Problem

The repository can already validate, stage, package, install, upgrade, roll back, uninstall, and qualify complete Code-OSS runtimes. Pull-request and master verification pass, but release jobs are skipped because no trusted workflow run currently publishes all three fixed release inputs:

- `code-oss-windows-x64`;
- `code-oss-linux-x64`;
- `appimagetool-linux-x64`.

The confirmed local runtime at `D:\project\VSCode-win32-x64` proves that the Windows consumer works, but a local persistent directory is not cross-platform producer evidence. It provides neither a Linux runtime nor a GitHub run whose workflow identity, branch, source commit, conclusion, artifacts, and digests can be verified by the packaging workflow.

## Selected Approach

Add a dedicated manual producer workflow that builds both Code-OSS runtimes from one fixed upstream commit on fresh standard GitHub-hosted runners. A final attestation job downloads the produced artifacts, independently revalidates them, and publishes one closed provenance document. The existing `foundation.yml` remains the packaging consumer, but gains a fail-closed producer-run verification job before either platform package job can download runtime inputs.

This approach is selected over a hybrid local/GitHub build because both platforms receive the same fresh-run provenance boundary. It is selected over fully self-hosted production because the public repository can use standard hosted runners without paid runner minutes or new machines. Producer and release artifacts use one-day retention to keep hourly artifact-storage accrual within the free workflow's intended short-lived test-release boundary.

## Scope

This design includes:

- a closed upstream source and tool manifest;
- a manual trusted producer workflow on `master`;
- hosted Windows and Linux x64 Code-OSS builds;
- a digest-pinned official x86_64 appimagetool input;
- platform runtime validation and Linux mode inventory production;
- cross-job artifact attestation and closed provenance;
- producer-run identity verification in `foundation.yml`;
- automated workflow, manifest, provenance, and trust-contract tests;
- one unsigned `0.1.0` packaging and qualification run.

This design excludes:

- uploading the existing local runtime or `release-inputs/code-oss.exe`;
- accepting a user-selected upstream repository, commit, URL, command, or output path;
- Windows production signing credentials or a signing certificate;
- publishing a GitHub Release or presenting the unsigned test package as a production release;
- final third-party license/legal approval.

## Fixed Supply-Chain Coordinates

A repository-owned closed JSON manifest records the only accepted producer inputs:

```json
{
  "schemaVersion": 1,
  "codeOss": {
    "repository": "https://github.com/microsoft/vscode.git",
    "commit": "b1c0a14de1414fcdaa400695b4db1c0799bc3124",
    "version": "1.92.0",
    "nodeVersion": "20.14.0",
    "yarnVersion": "1.22.22",
    "windowsTarget": "vscode-win32-x64",
    "windowsOutput": "VSCode-win32-x64",
    "linuxTarget": "vscode-linux-x64",
    "linuxOutput": "VSCode-linux-x64"
  },
  "appimagetool": {
    "repository": "AppImage/appimagetool",
    "assetId": 324406882,
    "assetName": "appimagetool-x86_64.AppImage",
    "size": 15092216,
    "sha256": "a6d71e2b6cd66f8e8d16c37ad164658985e0cf5fcaa950c90a482890cb9d13e0"
  }
}
```

The manifest parser rejects additional fields, malformed or non-lowercase digests, non-40-character commits, non-HTTPS repositories, alternate output directories, unsupported platforms, and any change to the expected upstream product coordinates. Updating a pin requires a reviewed repository commit and a fresh producer run.

The appimagetool download uses GitHub's public release-asset API with asset ID `324406882`, not the mutable `continuous` browser URL. The downloaded file must have the exact recorded size and SHA-256 before it becomes an artifact.

## Producer Workflow Trust Boundary

Create `.github/workflows/release-inputs.yml` with only `workflow_dispatch`. It has no user inputs, no `pull_request` trigger, no ordinary `push` trigger, no release signing secrets, and no write permission to repository contents. Every action reference is pinned to a full commit SHA.

The first job fails unless all of these values match exactly:

- event: `workflow_dispatch`;
- repository: `colayc/unitTest`;
- ref: `refs/heads/master`;
- workflow path: `.github/workflows/release-inputs.yml`.

GitHub permits selecting a ref for a manual workflow, so checking the ref inside the workflow is required; a non-master dispatch must fail rather than complete with all producer jobs skipped.

The workflow uses only standard GitHub-hosted runners: fixed `windows-2022` for the Windows producer and fixed `ubuntu-24.04` for authorization, Linux production, and attestation. `windows-2022` is required because the fixed Code-OSS checkout and its Node.js 20.14.0/node-gyp 10.1 native dependency path require Visual Studio 2022 rather than the Visual Studio 2026-only hosted image. The workflow does not run on the persistent administrator/WFP runner and never reads self-hosted workspace paths. Build jobs receive `contents: read`; the workflow receives only the minimum artifact permissions required by the pinned upload/download actions.

## Windows Producer

The Windows x64 job performs these ordered operations:

1. Check out the exact `unitTest` producer commit from `master`.
2. Parse the closed producer manifest and export only its validated fields.
3. Fetch `microsoft/vscode` at the exact 40-character commit into a new runner-local directory and verify `git rev-parse HEAD` byte-for-byte.
4. Verify the upstream `.nvmrc`, `package.json` version, Electron target, product identity, and expected Gulp target names against the producer manifest.
5. Install Node.js `20.14.0` and Yarn `1.22.22`.
6. Before dependency installation, run the hosted runner's `vswhere.exe` fail-closed and require one 17.x installation that simultaneously contains `Microsoft.VisualStudio.Component.VC.Tools.x86.x64` and `Microsoft.VisualStudio.Component.VC.Runtimes.x86.x64.Spectre`; then fix both node-gyp Visual Studio selectors to `2022`.
7. Run `yarn install --frozen-lockfile`, then `yarn gulp vscode-win32-x64`.
8. Require the sole expected output root `VSCode-win32-x64` and reject redirected, linked, partial, or additional candidate roots.
9. Run the repository's `code-oss-runtime.mjs` validator for Windows and execute the fixed launcher with `--version` using an isolated runner-local user-data directory.
10. Enumerate the complete runtime into a sorted, portable path/size/SHA-256 inventory and calculate its tree digest.
11. Upload the complete runtime root as `code-oss-windows-x64` with one-day retention only after every validation succeeds.

The job never installs, downgrades, or falls back to another compiler toolchain. A missing, linked, ambiguous, non-17.x, or incomplete Visual Studio installation fails with one fixed preflight error before `yarn install`. Missing Visual C++ or Spectre libraries, insufficient disk, dependency-install failure, native-module failure, output drift, launcher failure, or validator failure terminates the job before artifact upload.

## Linux Producer

The Linux x64 job performs the equivalent ordered operations on `ubuntu-24.04`:

1. Check out the exact `unitTest` producer commit and parse the same closed manifest.
2. Fetch and verify the same Code-OSS commit.
3. Install a closed list of Ubuntu packages required by that upstream commit, Node.js `20.14.0`, and Yarn `1.22.22`.
4. Run `yarn install --frozen-lockfile`, then `yarn gulp vscode-linux-x64`.
5. Require `VSCode-linux-x64` as the sole candidate output root.
6. Run the Linux Code-OSS validator and launcher `--version` check.
7. Generate `code-oss-runtime-mode.json` from the validated tree before artifact transport.
8. Re-run the mode-inventory validator against the source tree and calculate the complete portable tree digest.
9. Upload an artifact named `code-oss-linux-x64` whose root contains exactly `runtime/` and `code-oss-runtime-mode.json`, with one-day retention.
10. Download appimagetool through the fixed release-asset API coordinate, verify its size and SHA-256, and upload exactly one file named `appimagetool-x86_64.AppImage` as `appimagetool-linux-x64`, also with one-day retention.

The Linux runtime artifact intentionally preserves the existing closed two-entry consumer contract. Producer metadata is not inserted into the upstream runtime and is published separately.

## Artifact Attestation and Provenance

A final Ubuntu attestation job depends on both producer jobs. It downloads the three fixed-name artifacts from the current run and fails unless the artifact root sets are exact. It then:

- validates the transported Linux mode inventory's closed structure and binds both its digest and launcher digest to the Linux producer-job outputs before applying any transported mode;
- restores the already validated Linux modes, then revalidates and inventories both runtimes without executing either launcher;
- rechecks appimagetool name, size, and digest and recomputes both complete runtime inventories and tree digests after artifact transport;
- compares every recomputed runtime, mode-inventory, and appimagetool value with the producer-job outputs;
- only after all producer-job output comparisons pass, executes the transported Linux launcher with the fixed 30-second version check; the Windows launcher is never executed on Ubuntu;
- writes `release-input-provenance.json` only after all comparisons pass.

The closed provenance shape contains:

- schema version;
- producer repository, workflow path, unitTest commit, event, and ref;
- Code-OSS repository, commit, version, Node version, and Yarn version;
- for each platform: artifact name, launcher relative path, launcher SHA-256, file count, total byte size, and tree digest;
- Linux mode-inventory SHA-256;
- appimagetool repository, asset ID, asset name, size, and SHA-256.

It contains no timestamp, absolute path, token, environment block, runner name, or mutable download URL. The attestation job uploads exactly one `release-input-provenance` artifact with one-day retention. The entire producer workflow must conclude successfully; the existence of artifacts from an earlier failed job is never sufficient.

The tree digest is SHA-256 over the sorted inventory records. Each record binds the portable relative path, decimal size, lowercase content SHA-256, and executable state with unambiguous NUL separators. Windows records use the fixed non-executable file state; Linux records use the mode inventory's executable boolean. Directory metadata and host timestamps are excluded.

## Packaging Consumer Verification

Add a `verify-release-input-run` job to `.github/workflows/foundation.yml`. It runs only for a manual or version-tag release and is a dependency of both package jobs. Before package jobs download any runtime input, it queries the GitHub Actions run API for `release_input_run_id` and validates all of these fields:

- repository is `colayc/unitTest`;
- workflow path is `.github/workflows/release-inputs.yml`;
- event is `workflow_dispatch`;
- head branch is `master`;
- head SHA equals the packaging workflow's checked-out source commit;
- status is `completed` and conclusion is `success`.

The job downloads `release-input-provenance`, validates its closed schema, and requires it to match the API run record plus all three manually supplied SHA-256 inputs. It exposes only validated run ID and digests as job outputs. `package-windows` and `package-linux` use those outputs and may not read the raw workflow-dispatch values directly.

Each package job retains its existing independent verification after download:

- Windows recomputes the root `Code - OSS.exe` digest and validates the complete runtime during staging.
- Linux requires exactly `runtime/` plus `code-oss-runtime-mode.json`, verifies the root launcher and inventory, restores modes, and validates the complete runtime.
- Linux recomputes the appimagetool digest before execution.

This gives four boundaries: trusted run identity, provenance, post-transport input verification, and staged/package manifest verification.

## Error Behavior

Every failure is fail-closed and path-safe:

- `RELEASE_PRODUCER_CONFIG_INVALID`: producer manifest or fixed upstream coordinate is invalid;
- `RELEASE_PRODUCER_UNTRUSTED`: event, repository, workflow, ref, branch, commit, status, or conclusion is not trusted;
- `RELEASE_PRODUCER_BUILD_FAILED`: dependency or upstream build failed;
- `RELEASE_PRODUCER_OUTPUT_INVALID`: output root, product identity, runtime entry, version, mode, file count, size, or tree digest is invalid;
- `RELEASE_PRODUCER_TOOL_INVALID`: appimagetool coordinate, size, or digest is invalid;
- `RELEASE_PRODUCER_PROVENANCE_INVALID`: provenance is open, malformed, incomplete, or inconsistent with the run or artifacts.

Messages may include a stable platform, artifact name, or field name, but never dump environment variables, tokens, an absolute workspace path, or a runtime path inventory. Artifact upload steps are absent from failed execution paths. Package jobs cannot downgrade a producer verification failure to a skip.

## Testing Strategy

Implementation follows test-driven development.

### Producer manifest tests

- accept the exact fixed manifest;
- reject extra fields, alternate repositories, floating refs, malformed commits, non-fixed versions, changed targets or outputs, malformed sizes, and digest drift;
- prove CLI output contains only the validated closed coordinates and no local paths.

### Runtime inventory and provenance tests

- produce stable Windows and Linux tree digests from complete fixture trees;
- prove path, size, digest, and executable-state changes alter or invalidate the tree digest;
- reject duplicates, aliases, links, special files, unsafe paths, unsorted records, missing records, extra records, unsafe integers, and open provenance objects;
- verify provenance contains no timestamp, host path, environment, token, or mutable URL;
- prove Linux artifact transport restoration yields the same tree digest.

### Trusted-run tests

- accept only the exact producer workflow metadata tied to the current packaging commit;
- reject pull-request, push, alternate workflow, alternate repository, non-master branch, different head SHA, incomplete, cancelled, skipped, or failed runs;
- reject provenance/API disagreement and any disagreement with the manual launcher/tool digests;
- prove raw dispatch inputs cannot flow directly into package jobs.

### Workflow contract tests

- producer workflow has only `workflow_dispatch`, no arbitrary inputs, no secrets, and minimal permissions;
- non-master dispatch reaches a failing authorization step rather than a successful all-skipped run;
- Windows and Linux use standard hosted runners and the fixed Gulp targets;
- all actions are commit-pinned;
- runtime artifacts upload only after platform validation;
- attestation depends on both build jobs and uploads provenance only after independent verification;
- all producer and intermediate release artifacts use one-day retention;
- package jobs depend on trusted-run verification.

### Repository verification

The implementation must pass the focused producer tests, all `tools/release/**/*.test.mjs` tests, workspace workflow assertions, `pnpm test`, `pnpm verify`, `git diff --check`, and clean-status checks. Linux-only mode and special-file tests must execute natively in GitHub Actions; Windows-only runtime and workflow tests must execute on Windows.

## First Unsigned Test Release

After implementation review, merge, and dual-remote synchronization:

1. Dispatch `release-inputs.yml` on `master`.
2. Require Windows build, Linux build, and attestation to succeed.
3. Read the run ID and the three fixed SHA-256 values from the closed provenance artifact and workflow summary.
4. Dispatch `foundation.yml` on the same `master` commit with:
   - `release_version=0.1.0`;
   - `release_signing_required=0`;
   - the trusted producer run ID;
   - the Windows launcher SHA-256;
   - the Linux launcher SHA-256;
   - the appimagetool SHA-256.
5. Require both verify jobs, both package jobs, both install-smoke jobs, and `release-qualification` to complete successfully.
6. Require `release-qualification.json` to report `qualificationOutcome.qualified=true` and signature outcome `not-required`.
7. Download and inspect the unsigned MSIX, AppImage, manifests, license audits, lifecycle evidence, and qualification evidence before their one-day retention expires.

No unsigned artifact is published as a GitHub Release. A failure at any stage leaves the test release unqualified and must be fixed through a reviewed repository change followed by a fresh producer run.

## Success Criteria

This feature is complete when:

- one trusted producer run builds and attests complete Windows and Linux Code-OSS runtime artifacts from the fixed upstream commit;
- the packaging consumer proves the producer run identity before downloading inputs;
- the complete runtime trees, Linux modes, launcher digests, appimagetool digest, and provenance all agree after artifact transport;
- the free unsigned `0.1.0` workflow produces both packages and passes install, upgrade, rollback, uninstall, license, and qualification gates;
- GitHub and Gitee `master` contain the same reviewed implementation commit;
- no local Code-OSS runtime or generated package is committed or uploaded outside the trusted workflow;
- documentation continues to state that formal Windows signing and final license/legal approval remain outstanding Phase 8 work.
