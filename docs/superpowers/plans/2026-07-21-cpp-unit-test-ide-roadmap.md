# C/C++ Unit Test IDE Implementation Roadmap

**Source specification:** `2026-07-18-code-oss-cpp-unit-test-ide-design.md`

**Chosen architecture:** Code-OSS desktop client with a TypeScript built-in extension and a separately packaged Go test-execution service. The TypeScript client and Go service exchange versioned JSON messages over a per-user local IPC endpoint. A future browser client reuses the TypeScript UI and protocol but connects to a remote service over HTTPS/WebSocket.

**First-release platform matrix:**

- Windows/MSVC for normal builds, tests, and diagnostics.
- Windows/clang-cl with llvm-profdata/llvm-cov for coverage builds.
- Linux/GCC with gcovr.
- Linux/Clang with llvm-cov.

## Why this is split into separate plans

The product contains independently reviewable protocol, process-control, toolchain, framework, coverage, IDE, persistence, packaging, and release-engineering subsystems. Each phase below ends with working, testable software and receives its own detailed implementation plan before execution.

## Target repository shape

```text
apps/
├── code-oss-extension/       TypeScript built-in extension
└── test-service/             Go execution service and platform adapters
packages/
├── protocol-schema/          Versioned JSON Schema source of truth
├── protocol-models/          Generated TypeScript protocol models
├── test-client/              Transport-independent TypeScript client
├── testing-domain/           Shared client-side test state and projections
└── report-ui/                Web-compatible report and coverage UI
tools/
├── protocol-gen/             Deterministic TypeScript/Go model generation
├── service-probe/            End-to-end local-service diagnostic client
└── code-oss-patches/         Recorded product-shell patch manifest
testdata/
├── toolchains/               Golden compiler and CMake outputs
├── frameworks/               CppUTest and Unity sample projects
└── coverage/                 gcovr and llvm-cov fixtures
docs/
├── decisions/                Architecture decision records
└── superpowers/plans/        Approved implementation plans
```

## Phase sequence

### Phase 1: Repository, protocol v1, and local-service vertical slice

Deliver a TypeScript probe that launches the Go service, authenticates over Windows Named Pipe or Linux Unix Socket, reads service capabilities, requests shutdown, and verifies generated protocol models on both sides.

Exit criteria:

- Deterministic protocol generation produces clean Git output.
- Contract, Go unit, TypeScript unit, and Windows/Linux end-to-end tests pass.
- Unauthenticated and malformed messages are rejected with stable error codes.

Detailed plan: `docs/superpowers/plans/2026-07-21-foundation-protocol-service-plan.md`

### Phase 2: Task engine, process control, and persistence

Add the task state machine, cancellation, timeouts, complete process-tree termination, structured event sequencing, reconnect/replay, SQLite history, artifact metadata, atomic writes, and startup cleanup.

Depends on: Phase 1 protocol and transport.

Exit criteria: a synthetic command can be started, observed, cancelled, recovered after client reconnect, and recorded without being misclassified as a test failure.

### Phase 3: Workspace, CMake, and toolchain adapters

Implement workspace configuration Schema, CMake Presets discovery, toolchain capability detection, parameter-array command planning, and structured MSVC/GCC/Clang diagnostics.

Depends on: Phase 2 task/process engine.

Exit criteria: Windows/MSVC, Linux/GCC, and Linux/Clang sample projects configure and build through the service with golden diagnostics.

### Phase 4: Test-framework discovery and execution

Implement CTest container discovery, CppUTest/CppUMock and Unity/CMock adapters, stable test IDs, filtering, single/all/failed reruns, result parsing, and source locations.

Depends on: Phase 3 build adapters.

Exit criteria: both frameworks discover and run individual tests on the supported matrix, including failure, crash, skip, timeout, and malformed-output fixtures.

### Phase 5: Coverage and report pipeline

Implement Windows clang-cl/llvm-cov, Linux GCC/gcovr, and Linux Clang/llvm-cov adapters; normalize paths; merge profiles; export versioned JSON, JUnit XML, and HTML artifacts; and enforce LLVM tool-version compatibility.

Depends on: Phase 4 framework execution.

Exit criteria: source, line, branch, and summary coverage are reproducible for all declared coverage configurations, with the actual toolchain displayed in every report.

### Phase 6: Code-OSS shell and built-in extension

Create the thin Code-OSS fork, product branding, update/telemetry configuration, extension-source policy, built-in extension registration, service lifecycle manager, Testing API integration, workspace-trust gate, and diagnostic/source navigation.

Depends on: stable Phase 1 protocol plus Phase 3/4 execution APIs.

Exit criteria: the desktop IDE opens a sample workspace, refuses execution while untrusted, discovers tests after trust, and runs them through the external Go service.

### Phase 7: Product test UX, Mock configuration, history, and reports

Add run profiles, filters, failure details, progress/log streaming, Mock/Stub configuration, coverage tree and source decorations, local history, artifact browsing, and Web-compatible report views.

Depends on: Phase 5 reporting and Phase 6 extension integration.

Exit criteria: the complete primary user journey works without terminal commands and remains responsive with 10,000 test items.

### Phase 8: Packaging, updates, licensing, and hardening

Produce signed or hash-verified Windows/Linux packages, bundle the Go service and approved tools, lock compatible component versions, secure per-user IPC/token handling, redact logs, verify artifact paths, generate third-party notices, and test rollback.

Depends on: Phases 1-7.

Exit criteria: clean-machine installs, upgrades, rollbacks, uninstall cleanup, license review, and high-risk security gates pass.

### Phase 9: Full matrix, performance, and release qualification

Run unit, contract, integration, end-to-end, fault-injection, performance, and Code-OSS-upstream-merge regression suites. Establish hardware-qualified baselines for discovery, filtering, memory, startup, cancellation, and report generation.

Depends on: all prior phases.

Exit criteria: every release gate and success criterion in the source specification has an automated result or an explicitly signed manual record.

## Cross-phase rules

- Every phase begins with a detailed plan and failing tests before production code.
- JSON Schema remains the protocol source of truth; generated files are committed and checked for drift in CI.
- The service never accepts shell command strings; adapters emit executable plus argument arrays.
- Platform-specific code remains behind Go or TypeScript interfaces with Windows/Linux implementations and contract tests.
- Workspaces use URIs at the client boundary; native paths are resolved only inside the service.
- Each task ends in a focused commit and an independent verification command.
- Browser/cloud capabilities are compatibility constraints, not first-release deliverables.

## Source-specification coverage index

| Specification area | Owning phases |
|---|---|
| Product shell, branding, extension policy, and upstream maintenance | 6, 8, 9 |
| Protocol, transports, version compatibility, and data models | 1, 2 |
| Task state, errors, cancellation, reconnect, history, and artifacts | 2 |
| Workspace, CMake, compilers, diagnostics, and configuration | 3 |
| CppUTest/CppUMock, Unity/CMock, discovery, IDs, Mock/Stub | 4, 7 |
| Windows/Linux coverage, source mapping, JUnit, HTML, and reports | 5, 7 |
| Workspace trust, IPC security, path validation, signing, and redaction | 1, 6, 8 |
| Browser/cloud compatibility constraints | 1, 6 |
| Unit, contract, integration, end-to-end, performance, and release gates | Every phase, consolidated in 9 |
