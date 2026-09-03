# Code-OSS CLI Install-Smoke Handshake Design

## Context

GitHub PR #20 merged into `master` as
`13d46b9e4ba9677a557b375c35d186412e8635af`. A fresh trusted producer run,
`33494864475`, succeeded from that exact commit and produced independently
validated Windows and Linux Code-OSS runtimes. Fresh unsigned foundation run
`33499922707` then passed producer provenance verification, both platform
verification jobs, and both packaging jobs, but failed both install-smoke jobs:

- Windows timed out while executing the installed root
  `app/code-oss-runtime/Code - OSS.exe --version`.
- Linux aborted while executing the installed root
  `app/code-oss-runtime/code-oss --version` because the copied
  `chrome-sandbox` was not root-owned with mode `4755`.

The earlier diagnostic foundation run `33476360967` had stopped before launch
because of the now-fixed release artifact ordering defect. Run `33499922707`
therefore supplied the first formal evidence for the deeper launch-handshake
defect.

The two failures have one root cause: install smoke invokes the graphical
Electron application entry point as though it were the Code-OSS CLI entry
point. The trusted producer does not do that. Its Windows check uses
`bin/code-oss.cmd`, and its Linux check uses `bin/code-oss`. Both upstream
wrappers set `ELECTRON_RUN_AS_NODE=1`, pass
`resources/app/out/cli.js` as the first Electron argument, and then pass the CLI
arguments.

The exact Windows runtime from producer run `33494864475` confirmed the
difference:

- The wrapper returned status `0` with the expected three version lines.
- Direct `Code - OSS.exe --version` launched the graphical main process and
  timed out after 30 seconds.
- Direct `Code - OSS.exe resources/app/out/cli.js --version --user-data-dir
  <isolated-root>` with `ELECTRON_RUN_AS_NODE=1` returned status `0` with the
  expected version output.

The fixed CLI invocation also avoids the Linux Chromium SUID sandbox path: the
Electron executable runs as Node for the CLI script rather than starting the
graphical browser process. No `--no-sandbox` exception is needed.

## Goals

- Keep install and rollback smoke as a deterministic, headless Code-OSS CLI
  `--version` handshake.
- Reproduce the common semantic core of the upstream Windows and Linux CLI
  wrappers without invoking a platform shell.
- Continue to prove that the installed manifest-bound root Electron binary is
  usable, that corrupting the upgraded binary causes a real failure, and that
  rollback restores a usable baseline.
- Preserve the existing 30-second bound, isolated user-data environment,
  fail-closed result predicate, and qualification evidence schema.
- Add a regression that fails unless the manifest-bound CLI script actually
  executes with the required arguments and environment.
- Complete a fresh trusted producer and fresh unsigned foundation run after the
  reviewed fix is merged.

## Non-goals

- No graphical UI readiness or end-to-end desktop automation.
- No Linux `--no-sandbox` flag, SUID permission change, privilege escalation,
  or root-owned installation requirement.
- No change to the MSIX application entry point, AppImage `AppRun`, desktop
  entry, runtime layout, or launcher trust digest.
- No change to release manifest, sidecar, provenance, or qualification evidence
  schemas.
- No retry, relaxed timeout, or weaker launch success condition.
- No workflow, producer, signing, secret, or GitHub Release behavior change.
- No reuse of failed foundation run `33499922707` as qualification evidence.

## Considered Approaches

### Selected: invoke the CLI semantic core directly

Spawn the installed root Electron binary with the fixed installed CLI script as
its first argument, set `ELECTRON_RUN_AS_NODE=1`, and pass `--version` plus an
explicit isolated `--user-data-dir`.

This is cross-platform, avoids shell parsing, keeps the root binary as the
actual process under test, and matches the behavior proven by the trusted
producer. It fixes Windows GUI startup and Linux sandbox failure at their common
source.

### Rejected: invoke the platform wrapper scripts

Linux can execute `bin/code-oss` directly, but Node cannot directly
`spawnSync` the Windows `.cmd` wrapper; it returns `EINVAL`. Windows would need
an explicit `cmd.exe` layer with platform-specific quoting and exit propagation.
That adds shell parsing and platform branches without improving the handshake.

### Rejected: continue launching the GUI with `--no-sandbox`

`--no-sandbox` would weaken the Linux runtime and would not make the Windows GUI
process a bounded CLI version probe. A real GUI readiness check would require a
separate readiness protocol and desktop test design, which is outside this
fix.

## Selected Architecture

The packaging application launcher and the smoke CLI handshake are distinct
concepts:

- The application launcher remains the root
  `app/code-oss-runtime/Code - OSS.exe` on Windows and
  `app/code-oss-runtime/code-oss` on Linux. Packaging manifests, external trust
  digests, and the forced-corruption target continue to use these paths.
- The CLI script has the fixed cross-platform installed path
  `app/code-oss-runtime/resources/app/out/cli.js`.
- An internal handshake constructor in `tools/release/update.mjs` resolves both
  paths beneath the already manifest-verified installed version root and
  constructs one shell-free spawn request.

The request has this logical form:

```text
<root Electron binary>
<installed resources/app/out/cli.js>
--version
--user-data-dir
<isolated user-data root>
```

The child environment retains the existing process environment and existing
isolated `HOME`, `USERPROFILE`, `XDG_CACHE_HOME`, and `XDG_CONFIG_HOME` values.
It additionally sets:

```text
ELECTRON_RUN_AS_NODE=1
VSCODE_DEV=
```

The empty `VSCODE_DEV` value follows the packaged Windows wrapper and prevents a
developer-mode environment from changing CLI behavior.

## Components and Data Flow

### CLI handshake construction

`launchHandshake(packageRoot, version, userDataRoot)` continues to own the
bounded synchronous child process. It resolves the version root, application
binary, and CLI script using fixed portable relative paths. It calls the root
binary directly; it does not call `.cmd`, a shell, `AppRun`, or the graphical
desktop entry.

The child arguments are the CLI script, `--version`, `--user-data-dir`, and the
isolated user-data root. The explicit user-data flag matches the producer checks
instead of relying only on home-directory environment overrides.

### First install

1. Package extraction and `readVerifiedManifest` validate the complete baseline
   artifact, including every declared file's path, size, and SHA-256.
2. `installVersion` copies the manifest-bound artifact and re-verifies the
   installed tree.
3. The CLI handshake executes the installed baseline root binary and installed
   CLI script.
4. The existing result predicate requires status `0` and non-whitespace stdout.

### Failed upgrade and rollback

1. The target version is installed and selected as current.
2. The smoke harness re-verifies the target, then overwrites only the installed
   target root Electron binary with invalid bytes.
3. The same CLI handshake attempts to execute the corrupted binary and must
   fail.
4. Rollback selects the manifest-verified baseline.
5. The same CLI handshake executes the restored baseline binary and CLI script
   successfully.
6. Repeated rollback, uninstall, package-residue checks, and user workspace
   preservation continue unchanged.

This preserves the original corruption proof: the handshake does not bypass
the corrupted root binary by starting a separate Node executable or wrapper
process.

## Error Handling and Security Boundaries

- The timeout remains exactly 30 seconds. A timeout remains
  `RELEASE_SMOKE_FAILED`; no retry is added.
- A missing or unreadable CLI script causes the Electron-as-Node process to exit
  unsuccessfully and the handshake to fail closed.
- A nonzero status, signal, spawn error, or empty/whitespace-only stdout remains
  a failed handshake.
- Existing diagnostic precedence remains: a nonempty spawn error first, then
  nonempty stderr, then exit/signal classification plus stdout availability.
- Diagnostics do not include stdout contents, environment values, credentials,
  signing material, or tokens.
- The process receives no network-enabling configuration and no sandbox bypass.
- No platform shell interprets installed paths or user-data paths.
- Manifest verification, real-file checks, reparse/symlink rejection,
  containment checks, file-size checks, SHA-256 checks, and exact closed-set
  checks remain unchanged.

## Testing Strategy

Implementation follows test-driven development.

### Cross-platform CLI execution regression

Extend the package-backed production smoke fixture with a manifest-bound
`app/code-oss-runtime/resources/app/out/cli.js`. Continue to copy
`process.execPath` into the fixed platform root binary path so the test launches
a real relocatable native executable on Windows and Linux.

The fixture CLI script must:

- exit nonzero unless `ELECTRON_RUN_AS_NODE` is exactly `1`;
- exit nonzero unless `VSCODE_DEV` is the empty string;
- require `--version` and exactly one explicit `--user-data-dir` value;
- require that the user-data path equals the isolated smoke workspace root;
- append a marker outside the package-owned installation root; and
- print non-whitespace version output.

Before the production change, the root executable handles `--version` itself,
so the CLI marker is absent and the new regression fails. After the change, the
test requires exactly two successful CLI markers: the first baseline launch and
the rollback launch. The corrupted target launch must not add a successful
marker.

### Failure regressions

- A missing CLI script must produce `RELEASE_SMOKE_FAILED`.
- A fixture CLI nonzero exit must fail the handshake.
- The existing whitespace-only stdout regression must retain its exact useful
  diagnostic.
- The existing corrupted-upgrade assertion must continue to prove that only the
  target root binary changes and that the restored baseline really launches.
- Existing repeated rollback, uninstall boundary, package residue, and user-data
  preservation assertions remain unchanged.

### Verification sequence

1. Add and run the new focused regression in RED state.
2. Implement only the CLI handshake construction and invocation change.
3. Run `node --test tools/release/update.test.mjs`.
4. Run all release tests and the repository's complete `pnpm verify` gate with
   its fixed Node, pnpm, Go, CMake, and coverage-bundle requirements.
5. Run Go race verification and native E2E verification required by the current
   repository plan.
6. Re-run the shell-free CLI invocation against a real downloaded Windows
   runtime and require status `0` with the expected three version lines.
7. Require clean diff checks and independent code review before any push.
8. After explicit authorization, push the branch to GitHub and Gitee and create
   a GitHub PR without merging it.
9. After separate merge authorization, merge the PR and synchronize GitHub
   `master` to Gitee `master`.
10. Dispatch a fresh trusted producer from the merged commit, validate its
    provenance, and never reuse producer run `33494864475` as the new formal
    producer.
11. Dispatch a fresh foundation run with `release_version=0.1.0` and
    `release_signing_required=0`. Require producer verification, Windows/Linux
    verification, Windows/Linux packaging, Windows/Linux install smoke, and
    release qualification all to succeed.
12. Confirm that signing remains disabled and no GitHub Release or `0.1.0` tag
    is published.

## Expected Implementation Scope

Production changes are limited to:

- `tools/release/update.mjs`

Regression changes are limited to:

- `tools/release/update.test.mjs`

Planning and evidence documentation may change only under
`docs/superpowers/`. No workflow, packaging, producer, schema, signing, or
publication file change is expected. Any discovered need outside this scope
pauses implementation for another design review.

## Acceptance Criteria

- The first-install and rollback handshakes execute the installed manifest-bound
  CLI script through the installed root Electron binary.
- Both handshakes use `ELECTRON_RUN_AS_NODE=1`, empty `VSCODE_DEV`, explicit
  `--version`, explicit isolated `--user-data-dir`, and a 30-second timeout.
- The corrupted target root binary produces a real failed upgrade launch, while
  the restored baseline succeeds.
- Windows does not start a graphical Code-OSS main process during the handshake.
- Linux does not enter the Chromium SUID sandbox path and receives no
  `--no-sandbox` exception.
- Missing CLI, nonzero exit, timeout, and empty output remain fail-closed.
- Existing evidence schemas, package application entry points, trust digests,
  and security checks remain unchanged.
- Focused tests, all release tests, the full repository gate, Go race, native
  E2E, diff checks, and independent review pass.
- A fresh producer and fresh unsigned foundation succeed from the new merged
  commit, including both install-smoke jobs and release qualification.
- Signing stays disabled and no GitHub Release is published.
