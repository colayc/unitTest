# Phase 9 Gate Matrix

- Candidate commit: `e25f9c180a702901319bacded043be36a87bec72`
- Recorded by commit: `e25f9c180a702901319bacded043be36a87bec72`
- Evaluation mode: `candidate`
- Catalog complete: `true`
- Release ready: `false`

## Status summary

| Status | Count |
|---|---:|
| PASS | 6 |
| MISSING | 53 |
| FAILED | 0 |
| DEFERRED | 3 |

## Gates

| Gate | Phase | Category | Status | Evidence | Availability | Reason |
|---|---:|---|---|---|---|---|
| P1-IPC-PER-USER-AUTH | 1 | security | MISSING |  |  |  |
| P1-PROTOCOL-NO-SHELL | 1 | security | MISSING |  |  |  |
| P1-PROTOCOL-VERSION-COMPAT | 1 | compatibility | MISSING |  |  |  |
| P1-TOKEN-FILE-SECURE | 1 | security | MISSING |  |  |  |
| P2-ARTIFACT-ATOMIC-CLEANUP | 2 | artifacts | MISSING |  |  |  |
| P2-EVENT-REPLAY-PERSISTENCE | 2 | persistence | MISSING |  |  |  |
| P2-FAILURE-OWNERSHIP | 2 | reliability | MISSING |  |  |  |
| P2-PROCESS-TREE-TERMINATION | 2 | process | MISSING |  |  |  |
| P2-TASK-CANCEL-TIMEOUT | 2 | task-control | MISSING |  |  |  |
| P3-CMAKE-CONFIGURE-BUILD | 3 | build | MISSING |  |  |  |
| P3-DIAGNOSTIC-URI | 3 | diagnostics | MISSING |  |  |  |
| P3-TOOLCHAIN-LINUX-CLANG | 3 | toolchain | MISSING |  |  |  |
| P3-TOOLCHAIN-LINUX-GCC | 3 | toolchain | MISSING |  |  |  |
| P3-TOOLCHAIN-WINDOWS-CLANGCL | 3 | toolchain | MISSING |  |  |  |
| P3-TOOLCHAIN-WINDOWS-MSVC | 3 | toolchain | MISSING |  |  |  |
| P3-WORKSPACE-TRUST-PATHS | 3 | workspace-security | MISSING |  |  |  |
| P4-CPPUTEST-CPPUMOCK | 4 | framework | MISSING |  |  |  |
| P4-DISCOVERY-CTEST | 4 | discovery | MISSING |  |  |  |
| P4-RECOVERY-AND-10000-BACKEND | 4 | resilience | MISSING |  |  |  |
| P4-SELECTION-AND-RERUN | 4 | execution | MISSING |  |  |  |
| P4-UNITY-CMOCK | 4 | framework | MISSING |  |  |  |
| P5-COVERAGE-FAULT-MAPPING | 5 | coverage | MISSING |  |  |  |
| P5-COVERAGE-REPORTS | 5 | reports | MISSING |  |  |  |
| P5-LINUX-CLANG-COVERAGE | 5 | toolchain | MISSING |  | missing |  |
| P5-LINUX-GCC-COVERAGE | 5 | toolchain | MISSING |  |  |  |
| P5-PROTOCOL-V14-COMPAT | 5 | compatibility | MISSING |  |  |  |
| P5-WINDOWS-LLVM-COVERAGE | 5 | toolchain | MISSING |  |  |  |
| P6-BRANDING-AND-BUILTIN-REGISTRATION | 6 | product-shell | MISSING |  |  |  |
| P6-CODEOSS-HOST-SMOKE | 6 | integration | MISSING |  | missing |  |
| P6-SERVICE-LIFECYCLE | 6 | lifecycle | MISSING |  |  |  |
| P6-TESTING-API | 6 | testing-api | MISSING |  |  |  |
| P6-TESTING-API-10000-ITEMS | 6 | performance | MISSING |  |  |  |
| P6-WORKSPACE-TRUST-GATE | 6 | workspace-security | MISSING |  |  |  |
| P7-COVERAGE-UI-AND-SOURCE-DECORATION | 7 | user-interface | MISSING |  | missing |  |
| P7-HISTORY-AND-ARTIFACT-BROWSER | 7 | user-interface | MISSING |  | missing |  |
| P7-LINUX-GCC-OFFLINE | 7 | offline | MISSING |  |  |  |
| P7-MAIN-USER-JOURNEY | 7 | user-journey | MISSING |  | missing |  |
| P7-MOCK-CONFIGURATION-UX | 7 | user-interface | MISSING |  | missing |  |
| P7-WINDOWS-WFP-OFFLINE | 7 | offline | MISSING |  |  |  |
| P8-DOCS-CLOSEOUT | 8 | documentation | DEFERRED |  |  |  |
| P8-INSTALL-LIFECYCLE-LINUX | 8 | install-lifecycle | MISSING |  |  |  |
| P8-INSTALL-LIFECYCLE-WINDOWS | 8 | install-lifecycle | MISSING |  |  |  |
| P8-LEGAL-THIRD-PARTY | 8 | legal | DEFERRED |  |  |  |
| P8-LICENSE-AUDIT | 8 | legal | MISSING |  |  |  |
| P8-LINUX-APPIMAGE-PACKAGE | 8 | packaging | MISSING |  |  |  |
| P8-QUALIFICATION-UNSIGNED | 8 | qualification | MISSING |  |  |  |
| P8-RUNTIME-PRODUCER-PROVENANCE | 8 | supply-chain | MISSING |  |  |  |
| P8-SIGN-WINDOWS | 8 | signing | DEFERRED |  |  |  |
| P8-WINDOWS-MSIX-PACKAGE | 8 | packaging | MISSING |  |  |  |
| P9-MATRIX-CONTRACT | 9 | contract | PASS | github-actions-34830228205-1 |  |  |
| P9-MATRIX-E2E | 9 | end-to-end | PASS | github-actions-34830228205-1 |  |  |
| P9-MATRIX-FAULT-INJECTION | 9 | fault-injection | PASS | github-actions-34830228205-1 |  |  |
| P9-MATRIX-INTEGRATION | 9 | integration | PASS | github-actions-34830228205-1 |  |  |
| P9-MATRIX-UNIT | 9 | unit | PASS | github-actions-34830228205-1 |  |  |
| P9-PERF-CANCEL | 9 | performance | MISSING |  |  |  |
| P9-PERF-DISCOVERY-10000 | 9 | performance | MISSING |  |  |  |
| P9-PERF-FILTER | 9 | performance | MISSING |  |  |  |
| P9-PERF-HARDWARE-BASELINE | 9 | performance | MISSING |  | missing |  |
| P9-PERF-MEMORY | 9 | performance | MISSING |  |  |  |
| P9-PERF-REPORT | 9 | performance | MISSING |  |  |  |
| P9-PERF-STARTUP | 9 | performance | MISSING |  |  |  |
| P9-UPSTREAM-CODEOSS | 9 | upstream | PASS | github-actions-34830228205-1 |  |  |
