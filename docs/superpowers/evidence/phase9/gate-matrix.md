# Phase 9 Gate Matrix

- Candidate commit: `fe4e128478fc883c5280cedff1df5409c0498e91`
- Recorded by commit: `4dd38f6f9d28816462cbf3b7266bdf58b9c16499`
- Evaluation mode: `historical`
- Catalog complete: `true`
- Release ready: `false`

## Status summary

| Status | Count |
|---|---:|
| PASS | 41 |
| MISSING | 18 |
| FAILED | 0 |
| DEFERRED | 3 |

## Gates

| Gate | Phase | Category | Status | Evidence | Availability | Reason |
|---|---:|---|---|---|---|---|
| P1-IPC-PER-USER-AUTH | 1 | security | PASS | github-actions-35971050102-1-foundation |  |  |
| P1-PROTOCOL-NO-SHELL | 1 | security | PASS | github-actions-35971050102-1-foundation |  |  |
| P1-PROTOCOL-VERSION-COMPAT | 1 | compatibility | PASS | github-actions-35971050102-1-foundation |  |  |
| P1-TOKEN-FILE-SECURE | 1 | security | PASS | github-actions-35971050102-1-foundation |  |  |
| P2-ARTIFACT-ATOMIC-CLEANUP | 2 | artifacts | PASS | github-actions-35971050102-1-foundation |  |  |
| P2-EVENT-REPLAY-PERSISTENCE | 2 | persistence | PASS | github-actions-35971050102-1-foundation |  |  |
| P2-FAILURE-OWNERSHIP | 2 | reliability | PASS | github-actions-35971050102-1-foundation |  |  |
| P2-PROCESS-TREE-TERMINATION | 2 | process | PASS | github-actions-35971050102-1-foundation |  |  |
| P2-TASK-CANCEL-TIMEOUT | 2 | task-control | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-CMAKE-CONFIGURE-BUILD | 3 | build | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-DIAGNOSTIC-URI | 3 | diagnostics | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-TOOLCHAIN-LINUX-CLANG | 3 | toolchain | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-TOOLCHAIN-LINUX-GCC | 3 | toolchain | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-TOOLCHAIN-WINDOWS-CLANGCL | 3 | toolchain | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-TOOLCHAIN-WINDOWS-MSVC | 3 | toolchain | PASS | github-actions-35971050102-1-foundation |  |  |
| P3-WORKSPACE-TRUST-PATHS | 3 | workspace-security | PASS | github-actions-35971050102-1-foundation |  |  |
| P4-CPPUTEST-CPPUMOCK | 4 | framework | PASS | github-actions-35971050102-1-foundation | available |  |
| P4-DISCOVERY-CTEST | 4 | discovery | PASS | github-actions-35971050102-1-foundation | available |  |
| P4-RECOVERY-AND-10000-BACKEND | 4 | resilience | PASS | github-actions-35971050102-1-foundation | available |  |
| P4-SELECTION-AND-RERUN | 4 | execution | PASS | github-actions-35971050102-1-foundation | available |  |
| P4-UNITY-CMOCK | 4 | framework | PASS | github-actions-35971050102-1-foundation | available |  |
| P5-COVERAGE-FAULT-MAPPING | 5 | coverage | MISSING |  | missing |  |
| P5-COVERAGE-REPORTS | 5 | reports | MISSING |  | missing |  |
| P5-LINUX-CLANG-COVERAGE | 5 | toolchain | MISSING |  | missing |  |
| P5-LINUX-GCC-COVERAGE | 5 | toolchain | PASS | github-actions-35971050102-1-foundation | available |  |
| P5-PROTOCOL-V14-COMPAT | 5 | compatibility | PASS | github-actions-35971050102-1-foundation |  |  |
| P5-WINDOWS-LLVM-COVERAGE | 5 | toolchain | MISSING |  | missing |  |
| P6-BRANDING-AND-BUILTIN-REGISTRATION | 6 | product-shell | MISSING |  | missing |  |
| P6-CODEOSS-HOST-SMOKE | 6 | integration | MISSING |  | missing |  |
| P6-SERVICE-LIFECYCLE | 6 | lifecycle | PASS | github-actions-35971050102-1-foundation |  |  |
| P6-TESTING-API | 6 | testing-api | PASS | github-actions-35971050102-1-foundation |  |  |
| P6-TESTING-API-10000-ITEMS | 6 | performance | PASS | github-actions-35971050102-1-foundation |  |  |
| P6-WORKSPACE-TRUST-GATE | 6 | workspace-security | PASS | github-actions-35971050102-1-foundation |  |  |
| P7-COVERAGE-UI-AND-SOURCE-DECORATION | 7 | user-interface | MISSING |  | missing |  |
| P7-HISTORY-AND-ARTIFACT-BROWSER | 7 | user-interface | MISSING |  | missing |  |
| P7-LINUX-GCC-OFFLINE | 7 | offline | PASS | github-actions-35971050102-1-foundation |  |  |
| P7-MAIN-USER-JOURNEY | 7 | user-journey | MISSING |  | missing |  |
| P7-MOCK-CONFIGURATION-UX | 7 | user-interface | MISSING |  | missing |  |
| P7-WINDOWS-WFP-OFFLINE | 7 | offline | MISSING |  | missing |  |
| P8-DOCS-CLOSEOUT | 8 | documentation | DEFERRED |  |  |  |
| P8-INSTALL-LIFECYCLE-LINUX | 8 | install-lifecycle | MISSING |  | missing |  |
| P8-INSTALL-LIFECYCLE-WINDOWS | 8 | install-lifecycle | MISSING |  | missing |  |
| P8-LEGAL-THIRD-PARTY | 8 | legal | DEFERRED |  |  |  |
| P8-LICENSE-AUDIT | 8 | legal | MISSING |  | missing |  |
| P8-LINUX-APPIMAGE-PACKAGE | 8 | packaging | MISSING |  | missing |  |
| P8-QUALIFICATION-UNSIGNED | 8 | qualification | MISSING |  | missing |  |
| P8-RUNTIME-PRODUCER-PROVENANCE | 8 | supply-chain | MISSING |  | missing |  |
| P8-SIGN-WINDOWS | 8 | signing | DEFERRED |  |  |  |
| P8-WINDOWS-MSIX-PACKAGE | 8 | packaging | MISSING |  | missing |  |
| P9-MATRIX-CONTRACT | 9 | contract | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-MATRIX-E2E | 9 | end-to-end | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-MATRIX-FAULT-INJECTION | 9 | fault-injection | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-MATRIX-INTEGRATION | 9 | integration | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-MATRIX-UNIT | 9 | unit | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-CANCEL | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-DISCOVERY-10000 | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-FILTER | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-HARDWARE-BASELINE | 9 | performance | PASS | github-actions-35971050104-1-phase9 | available |  |
| P9-PERF-MEMORY | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-REPORT | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-PERF-STARTUP | 9 | performance | PASS | github-actions-35971050104-1-phase9 |  |  |
| P9-UPSTREAM-CODEOSS | 9 | upstream | PASS | github-actions-35971050104-1-phase9 |  |  |
