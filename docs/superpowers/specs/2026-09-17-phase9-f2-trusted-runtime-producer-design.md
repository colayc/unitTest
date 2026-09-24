# Phase 9 F2 Trusted Framework Runtime Producer Design

## Context

Phase 9 Batch F2 already has a closed platform report contract, a Service-driven 17-scenario runner, F1 fixture identity binding, platform executable evidence, and a deterministic P4 aggregator. Required native execution consumes these fixed inputs:

- `.native-e2e/framework-runtime/windows.json`
- `.native-e2e/framework-runtime/linux.json`
- `.native-e2e/framework-work/{platform}/{toolchain}/{framework}/{service,workspace}/...`

No current command produces that state. Static workflow wiring would therefore remain intentionally fail-closed and could not produce hosted evidence. Task 6 is expanded to add a trusted local producer before wiring the hosted matrix.

The three approved Phase 8 deferred gates remain unchanged: formal Windows signing, third-party license/legal approval, and roadmap status cleanup. This design does not publish a Release, enable signing, approve legal inventory, push a branch, create a pull request, or change `releaseReady=false`.

## Goals

1. Produce the fixed Windows and Linux framework runtime inputs from committed F1 and F2 contracts.
2. Bind each runtime manifest to the candidate commit, compiler identity, F1 provenance, real Service catalog, stable framework identity, and compiled executable bytes.
3. Make publication atomic so required consumers never observe partial state.
4. Run the Windows framework producer on a fixed hosted runner isolated from administrator/WFP execution.
5. Run Linux preparation and execution inside the existing offline boundary after all allowed network preparation finishes.
6. Preserve exact artifact names and the 136-record P4 aggregation contract.

## Non-goals

- Reworking the existing Windows WFP or coverage jobs.
- Adding administrator privileges to hosted framework jobs.
- Allowing caller-selected output directories, commands, hooks, shells, or executables.
- Running CMock, Ruby, or Docker during native matrix execution.
- Changing F1 fixture bytes, dependency locks, provenance values, release inputs, signing, legal status, or remote repository state.

## Architecture

### Trusted producer

A new `native-framework-prepare` module and CLI owns runtime preparation. Its public command accepts only:

- a closed platform enum: `win32` or `linux`;
- a lowercase 40-character candidate commit.

The command derives every input and output path from the repository root. It does not accept a workspace path, output path, Service binary, compiler path, shell command, hook, arbitrary environment mapping, or executable override.

The producer reuses the existing F1 validator, F2 matrix contract validator, native toolchain discovery, Service fixture lifecycle, and stable digest builder. The runtime loader remains a validator and consumer; it does not build or repair producer output.

### Fixed layout

The producer stages a complete platform tree below a fixed repository-controlled staging root, then publishes only these platform outputs:

```text
.native-e2e/framework-runtime/windows.json
.native-e2e/framework-work/windows/{msvc,clang-cl}/{cpputest,unity}/{service,workspace}/...
.native-e2e/framework-runtime/linux.json
.native-e2e/framework-work/linux/{gcc,clang}/{cpputest,unity}/{service,workspace}/...
```

Each workspace is assembled from the committed framework-matrix overlay and F1 fixture sources with a closed `.unit-test-ide/workspace.json`. The workspace configuration fixes the framework, generator profile, dependency roots, CMake helper, and Unity generator; it cannot reference content outside the staged workspace and verified prepared dependency roots.

### Evidence flow

For each platform, toolchain, and framework, the producer performs the following sequence:

1. Validate the candidate, F1 manifest/tree/cache/CMock provenance, F2 contract, Service binary, CMake bundle, and Unity generator.
2. Discover the required compiler family and validate its version and binary SHA-256.
3. Create a staging Service directory and workspace from the closed inputs.
4. Start the Service against the staging workspace and run discovery so the Service performs the real configure/build and publishes the catalog.
5. Validate the exact primary, malformed, and opaque containers required by the F2 contract.
6. Recompute `stableFrameworkIdDigest` from the real catalog and the complete F1 identity.
7. Locate the single regular, non-link compiled fixture executable in the fixed Service build root and hash its bytes.
8. Bind catalog, dependency, source, executable, CMock, compiler, platform, toolchain, and candidate evidence into the closed runtime manifest record.
9. Dispose the Service and verify that no process or temporary evidence remains active.

The completed manifest is passed through the same closed parser used by the runtime consumer before publication.

## Atomicity and failure handling

Preparation is platform-scoped. All new state is created in a fresh fixed staging directory containing a producer ownership record. The currently published platform runtime remains untouched until the staging tree has passed all validators. The producer removes only staging state whose ownership record matches the current invocation; an unknown pre-existing staging entry is preserved and causes a fail-closed error.

Publication uses same-volume renames. The producer first moves any existing complete platform state to a bounded backup name, moves the verified staging state into place, and removes the backup only after both final paths revalidate. If publication fails, it restores the previous complete state. It never merges new files into an existing runtime tree.

The producer fails closed on:

- an invalid or substituted candidate;
- missing, duplicated, unsupported, or path-bearing compiler identity;
- manifest, dependency tree, cache, fixture, generated CMock, or contract drift;
- a symlink, junction, non-regular required file, or path escaping a fixed root;
- missing or extra framework/toolchain workspace records;
- catalog/container/scenario mismatch;
- stable ID, compiler, source, executable, or provenance digest mismatch;
- duplicate executable build profiles;
- Service deadline, reconnect, cleanup, or process termination failure;
- partial prior staging state that cannot be proven to belong to the current producer.

On failure, the current verified published state is preserved and staging state owned by the current invocation is removed. Unknown state is never deleted automatically. Logs and stdout contain only closed identifiers and digests; they do not expose absolute paths, environment values, credentials, or raw test output.

## Workflow design

### Windows framework producer

Add `verify-framework-windows` with fixed `runs-on: windows-2022`. It performs pinned checkout/setup actions, installs locked dependencies, prepares verified CMake and F1 caches, builds the Service and Unity generator, runs the trusted producer for `github.sha`, and then runs the Windows native matrix with:

```text
UNIT_TEST_IDE_NATIVE_REQUIRED_TOOLCHAINS=msvc,clang-cl
UNIT_TEST_IDE_P4_FRAMEWORK_MATRIX_REQUIRED=1
```

Only a successful required matrix may upload `native-framework-windows`. The framework upload is not guarded by `always()`.

The existing `verify-windows`, `verify-windows-wfp`, `unit-test-wfp`, privileged WFP lifecycle, and coverage evidence paths remain unchanged. Pull-request code does not gain access to an administrator runner through this design.

### Linux producer

The existing `verify-linux` completes all allowed dependency and cache preparation before entering its offline namespace. Inside that boundary it builds the Service and generator, runs the trusted producer for `github.sha`, runs the required native matrix with `gcc,clang`, and uploads `native-framework-linux` only after success.

No new network-capable step is introduced after the offline boundary begins.

### Matrix aggregation

`verify-framework-matrix` depends on `verify-framework-windows` and `verify-linux`, downloads the exact platform artifacts to fixed paths, invokes the sole P4 aggregator command, and uploads `native-framework-matrix-report` with 14-day retention and `if-no-files-found: error`.

The aggregate remains deterministic and path-free with exactly:

- two platform reports;
- four platform toolchains;
- eight toolchain/framework blocks;
- seventeen ordered scenarios per block;
- 136 scenario records total.

## Testing strategy

Implementation follows red-green-refactor. No producer implementation is added until a focused test fails for the intended missing behavior.

### Producer tests

Tests cover:

- closed CLI arguments and candidate validation;
- exact platform/toolchain/framework layout;
- F1 and F2 identity drift;
- compiler and executable substitution;
- real catalog stable ID binding;
- missing, duplicate, linked, or path-escaping files;
- incomplete 2-by-2 workspace state;
- Service timeout and cleanup failure;
- atomic publication, rollback, and stale staging ownership;
- path-free, credential-free diagnostics.

The integration fixture uses the real Service protocol and verifies that compiled executable bytes, catalog evidence, and runtime manifest fields agree. Dependency injection is limited to bounded process/filesystem fault simulation; contract assertions target the real producer API and serialized output.

### Workflow tests

Static contract tests require:

- fixed `windows-2022` and `ubuntu-24.04` framework runners;
- immutable action SHAs;
- no secrets or administrator/WFP privileges in framework jobs;
- producer-before-required-matrix ordering;
- Linux producer and matrix execution inside the offline wrapper;
- exact environment variables, artifact names, paths, retention, and `if-no-files-found: error`;
- no `always()` on framework report uploads;
- exact matrix job dependencies and aggregation command.

### Verification boundary

Before a local commit, run the focused producer tests, the complete service-probe suite, framework-bundle tests, workspace smoke tests, Linux offline tests, Phase 9 gate checks, and `git diff --check`.

Real hosted four-toolchain execution occurs only after separate user authorization to push the branch and dispatch the workflow. This design does not claim hosted success from local or mocked evidence.

## Deliverables

- Trusted runtime producer module, CLI, package entry, and tests.
- Fixed hosted Windows framework producer job.
- Linux offline producer integration.
- Exact platform artifact uploads and P4 aggregation dependency update.
- Updated native E2E documentation.
- Local verification report and independent scoped review.
