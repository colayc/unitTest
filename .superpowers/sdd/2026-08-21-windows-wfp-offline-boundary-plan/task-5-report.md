# Task 5 report: privileged Windows integration and handoff

## Status

`DONE_WITH_CONCERNS`

The native Windows boundary, fail-closed lifecycle controls, process launch
registration, integration/control tests, and required CI path are implemented.
All executable local gates that do not require unavailable host capabilities are
green. This machine cannot honestly claim the positive privileged WFP or
verified clang-cl/LLVM execution: WFP management returns `WFPAccessDenied`, the
verified coverage toolset is unavailable, and `cmake` is absent from `PATH`.

## Final design and behavior

- Each WFP dynamic-session block filter is scoped to a registered executable's
  exact `ALE_APP_ID`. V4 and V6 filters also require
  `FLAGS_NONE_SET(IS_LOOPBACK)`, so unrelated executables and loopback/local-host
  resources remain usable while registered applications' non-loopback outbound
  connections are blocked. There is no machine-wide zero-condition filter.
- The guardian audits the exact closed provider, sublayer, filter keys, action,
  non-persistent flags, APP_ID blobs, and loopback condition before reporting
  `Ready`. Executable registration adds the matching V4/V6 pair to the same
  dynamic session and repeats the audit.
- WFP has no process-tree condition. The boundary therefore uses an explicit
  pre-launch application plan: the Service executable is registered after
  guardian `Ready`; the Service plan carries CMake, Ninja, compiler, linker,
  test and LLVM executable identities; and the Windows processhost registers
  the direct executable plus the plan before job allocation or `CreateProcess`.
  Missing capability, malformed registration, rejection, or an unregistered
  planned launch fails closed before the native process starts.
- Registration uses a private per-run named-pipe capability and HMAC nonce.
  The capability reaches the Service/processhost only; service-owned variables
  are removed from the final target environment and no path or nonce is emitted
  in reports or diagnostics.
- Guardian control-pipe authentication precedes `Hello`/`Ready`. Go checks the
  OS named-pipe peer PID plus guardian and owner PID/creation identities and a
  32-byte per-run HMAC nonce. TypeScript binds the same nonce proof to the
  spawned guardian PID/creation identity and owner identity. A rejected first
  same-user client is closed while bounded accept continues for the real
  guardian.
- Every Go Lease timeout, protocol, send, and close-abort path kills the
  guardian, waits within a bound for `Wait()`/process exit, and only then closes
  transport. Unknown exit or cleanup failure maps to session-close failure.
  Registration shutdown is also bounded so a release-racing request cannot
  block the guardian.
- TypeScript continuously monitors the guardian and socket after `Ready`.
  Active guardian loss prevents or aborts the native callback and suppresses
  evidence. Pending frame reads reject on socket/child loss, termination is
  bounded, and normal close exclusively proves `Release` -> `Bye` -> clean
  guardian exit even if child-exit observation races ahead of buffered `Bye`.
- Coverage teardown is Service stop -> guardian close -> fixture removal ->
  atomic publication, keeping the fixture-owned guardian executable available
  until exit is proved.
- Post-preflight `WFPAccessDenied` is a failure in both local and required
  modes. Only preflight `ToolchainUnavailable` may be a local skip. Verified
  preflight output has a closed schema and includes a path-free SHA-256 digest
  over the exact clang-cl, llvm-profdata, and llvm-cov binary identities rather
  than only their version. Failure to obtain cryptographic randomness fails
  closed; there are no fallback lease bytes.

## TDD evidence

The final review wave began with focused regressions against the prior code:

- WFP unit tests observed zero filter conditions and a machine-wide scope.
  They now prove exact APP_ID/loopback conditions, V4/V6 pairs, unchanged
  unrelated applications, child registration, and closed-set re-audit.
- A same-user rogue pipe client was accepted before the guardian. Go now rejects
  the rogue and accepts the later authenticated peer; the TypeScript proof test
  rejects a forged nonce/identity payload.
- Lease `Close` returned while a killed guardian was still alive. It now waits
  for exit and does not close transport early; timeout and residue-unknown
  controls fail closed.
- A processhost registration rejection still reached the job/CreateProcess
  path. It now stops before either side effect, while the exact executable and
  child launch plan are acknowledged in the green control.
- Local post-preflight WFP denial incorrectly skipped, a version-only verified
  preflight was accepted, and random-source failure produced fallback bytes.
  The new focused tests are green with FAIL, schema rejection, and fail-closed
  entropy behavior respectively.
- Full Service Probe initially found two older verified-preflight fixtures
  without `toolchainDigest`; both were RED and then updated to the closed schema.
- The root network audit initially rejected the new registration file and the
  additional guardian pipe connection type. Its final tests explicitly allow
  only `net.Listener`/`net.Conn` and named-pipe listen/dial selectors; no generic
  transport exception was added.

Earlier review regressions remain green for post-Ready crash abort, pending
read rejection, bounded termination, access-denied cleanup, Release/Bye event
ordering, engine-close error joining, and fixture teardown/publication order.

The final pagination/launch-declaration review began with two additional RED
controls:

- Nine registered applications create 18 V4/V6 filters. The prior production
  enumeration returned only its first 16-entry page and the exact audit failed.
  The Windows API wrapper now retains one enumeration handle, advances a
  monotonic cursor through bounded 16-entry pages, audits the complete closed
  set, and always destroys the handle. Repeated/backward cursors, count/cursor
  disagreement, oversized pages, more than 256 entries, duplicate/extra
  filters, and enumeration-handle cleanup errors all fail closed. The nine-app
  test observes cursors `0, 16, 18` and exactly one enumerator close.
- The supported Windows coverage build declaration previously contained only
  compiler, Ninja, and linker identities. Its green closed plan now also
  contains CMake, CTest, the Unity generator, the test executable, exact LLVM
  tools, the family-specific archiver (`llvm-lib.exe` for clang-cl), and the
  validated `cmd.exe` shell. The fixture builds a static library and runs a
  CMake-owned post-build custom command, proving that its archiver and custom
  command executor are declared before processhost can create CMake. Unknown
  Windows toolchain families, a non-absolute/non-`cmd.exe` shell, malformed
  registration, or any rejected declared identity stop before `CreateProcess`.
- Final critical regression: a real nested CMake fixture with
  `add_custom_command(... COMMAND unknown.exe ...)` previously returned a valid
  plan even though that executable had no APP_ID filter. The planner now uses a
  bounded CMake tokenizer and follows only literal `add_subdirectory` and
  literal, already-materialized `include` edges under approved roots. Every
  `COMMAND` in `add_custom_command`, `add_custom_target`, and `add_test` must
  resolve exactly to a LaunchPlan path, a unique executable target artifact, or
  a closed CMake variable. Unresolved dynamic variables/generator expressions,
  missing or escaping graph edges, ambiguous targets, 129+ files, 8193+
  commands, 65537+
  arguments, direct `cmd.exe`, and process-spawning `cmake -E` wrappers fail
  before an execution plan exists. Validation is scoped to coverage plans or a
  Service carrying the WFP registration capability (including a malformed
  partial capability), so ordinary Windows builds outside this boundary are
  unchanged. The retained positive fixture proves nested `${CMAKE_COMMAND} -E
  touch` and a literal pre-generated include.

## Verification

Final commands used Node 24.18.0, pnpm 11.4.0, Go 1.26.6 and worktree-private
Go caches.

- Unexcluded Windows Go full run: all packages pass except
  `TestPrivilegedWindowsWFPDynamicLifecycle`, which fails exactly
  `WFP integration FAIL after test start: WFPAccessDenied (local and required modes fail closed)`.
- `go test ./... -count=1 -skip '^TestPrivilegedWindowsWFPDynamicLifecycle$'`:
  PASS for the complete Service tree.
- `go test -race ./... -count=1 -skip '^TestPrivilegedWindowsWFPDynamicLifecycle$'`:
  PASS for the complete Service tree. After the final bounded registration
  shutdown edit, focused race on `internal/offlineboundary` and
  `internal/processhost` also PASS.
- `go vet ./...`: PASS.
- Linux `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` full-package compile through a
  no-exec wrapper: PASS, including unsupported offlineboundary and guardian
  command compilation.
- `pnpm --filter @unit-test-ide/service-probe test`: 76 PASS / 1 exact
  `ToolchainUnavailable; required mode would FAIL` SKIP.
- `pnpm --filter code-oss-extension test`: 138/138 PASS.
- `pnpm --filter code-oss-extension test:coverage-service-smoke`: 1 honest SKIP,
  `verified clang-cl coverage toolset is unavailable`; it does not publish a
  report.
- Final pagination/launch-declaration wave: focused nine-application pagination,
  malformed cursor, closed launch-plan, offlineboundary, build, and processhost
  controls PASS; the complete Service, race, vet, Linux compile-only, Service
  Probe, and Extension commands above were rerun PASS. Protocol and coverage
  generated-source checks were rerun PASS.
- Final unknown-command wave: focused `internal/build` and `internal/processhost`
  PASS; Service full, vet, Linux compile-only, Service Probe (76 PASS / one
  exact toolchain SKIP), and Extension (138/138) PASS. The first full race run
  hit one unrelated existing SQLite fault-injection timing failure in
  `internal/coverageexec`; that test passed immediately in isolation, and a
  second complete race run passed every package.
- `pnpm check:protocol-generated`, `pnpm check:coverage-generated`, and
  `pnpm build` with private `GOCACHE`: PASS.
- Exact `pnpm test`: coverage generator 4/4, CMake bundle 28/28, and coverage
  bundle 34 PASS / 1 existing Python `EPERM` SKIP. The workspace gate's two new
  local-IPC audit failures were fixed and rerun green; its final result is
  22 PASS / 1 environment failure, solely `spawnSync cmake ENOENT`.
- `git diff --check`: PASS before report/cleanup and rerun before commit.

## Host boundary and handoff

- This report makes no privileged native WFP PASS claim. A Windows runner with
  WFP management permission must execute the real APP_ID-scoped block,
  unrelated/loopback allow, normal release, guardian crash, owner termination,
  PID-reuse and dynamic-object disappearance evidence. The required CI job is
  the authoritative positive executor.
- This report makes no verified LLVM or coverage-publication PASS claim. A
  runner with the repository-verified clang-cl/LLVM siblings must execute the
  positive default-sibling path.
- `cmake` is not installed on this host's `PATH`; the exact root suite therefore
  stops at that one environment boundary after its earlier stages pass.
- Windows WFP has no PID-tree primitive, and this user-mode implementation does
  not claim interception of arbitrary dynamically discovered grandchildren.
  The supported CMake/Ninja/clang-cl fixture is a closed launch declaration:
  its known executors, archiver, shell, test, and LLVM tools are registered
  before the direct CMake launch. The planner now rejects undeclared custom/test
  executables and unprovable dynamic CMake graph edges before process creation;
  projects outside this explicitly supported grammar must extend the closed
  declaration rather than be treated as covered. Privileged CI must still
  supply the positive native evidence for that declared shape.
- Temporary Go caches, the pnpm shim and generated runtime artifacts are removed
  before commit. The change is committed locally and is not pushed.
