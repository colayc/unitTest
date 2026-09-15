# Task 1 implementation report

## Files changed

- `tools/phase9/performance.mjs` — deterministic, bounded six-scenario runner with strict validation and CLI output.
- `tools/phase9/performance.test.mjs` — closed-schema, scenario, stability, finite-value, bounded-input, and redaction contract tests.
- `package.json` — added `test:phase9:performance`.
- `tools/workspace-smoke/workspace-smoke.test.mjs` — added the fixed performance job contract assertions.

## Verification

Required RED check before implementation:

```text
$ node --test tools/phase9/performance.test.mjs
Could not find 'C:\\codex_project\\unitTest\\tools\\phase9\\performance.test.mjs'
```

Required performance command:

```text
$ pnpm test:phase9:performance
[ERR_PNPM_UNSUPPORTED_ENGINE] Unsupported environment (bad pnpm and/or Node.js version)
Expected version: 11.4.0
Got: 11.19.0
```

Equivalent direct test command (pass):

```text
$ node --test tools/phase9/performance.test.mjs
1..3
# tests 3
# pass 3
# fail 0
```

Required workspace smoke command (pass):

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs
1..21
# tests 21
# pass 21
# fail 0
```

The CLI was also exercised with `node tools/phase9/performance.mjs --out .superpowers/phase9/performance/baseline.json`.

## Commit

The original implementation commit was superseded by the reviewed round-4 code commit recorded below. `92c5d38e93ca1f15021cca6bd569fd9c5e4b0c4f` is retained only in history and is not the reviewed implementation.

## Concerns

- The required pnpm script cannot run until pnpm 11.4.0 is installed; the repository currently has pnpm 11.19.0.
- The workspace-smoke assertions preserve the fixed future job coordinates without modifying the workflow, as required for this task.

## Review-fix report

Addressed all review findings: Windows-safe `pathToFileURL` CLI detection; malformed-argument and successful subprocess coverage; instability now propagates instead of rewriting samples; scenarios perform bounded synthetic work and derive correctness; validation enforces closed per-scenario keys, one sample field, recomputed summaries, and correctness invariants; and workspace-smoke isolates the exact future `phase9-performance` job block.

Verification after fixes:

```text
$ node --test tools/phase9/performance.test.mjs
1..5
# tests 5
# pass 5
# fail 0

$ node --test --test-name-pattern="Phase 9 performance baseline job contract" tools/workspace-smoke/workspace-smoke.test.mjs
not ok 2 - Phase 9 performance baseline job contract is fixed and reviewed
error: phase9-performance job is missing
```

The smoke failure is intentional and remains until Task 2 adds the workflow job. The fix commit retains the required message; updated SHA is recorded by the parent task after amend.

## Review-fix round 2

Discovery now runs through a reusable JS-compatible identity fixture with a 10,000-entry map and refresh snapshots, checking stable object identity while deriving the observed count. Validation now rejects negative samples and any extra correctness properties, and checks exact correctness invariants. Added mutation cases for both conditions; the unused correctness helper was removed.

Covering command/output:

```text
$ node --test tools/phase9/performance.test.mjs
1..5
# tests 5
# pass 5
# fail 0
```

The workflow contract remains intentionally RED until Task 2 adds the job; no workflow was modified.

## Review-fix round 3

Discovery now imports the shared JS-compatible identity fixture used by `apps/code-oss-extension/test/testing-api-benchmark.test.ts`. It performs same-revision refreshes, verifies the fixture preserves object identity, and derives the observed 10,000-item correctness count. The helper’s dead refresh path was removed from the runner.

Covering command/output:

```text
$ node --test tools/phase9/performance.test.mjs
1..6
# tests 6
# pass 6
# fail 0
```

No workflow changes were made; the Task 2 workflow contract remains intentionally pending.

## Review-fix round 4

Reviewed code commit:

```text
cdd278967510dc4dc048dbd86fd4b8034692c092 — test: add phase9 performance baseline harness
```

### Changes

- Replaced the duplicate `Map`-only fixture with `apps/code-oss-extension/test/testing-api-benchmark-support.mjs`. TypeScript now emits that support module beside the compiled benchmark, so `dist/test/testing-api-benchmark.test.js` resolves its local import after build.
- The shared fixture instantiates the real `TestingApiAdapter`, performs two refreshes of the same 10,000-item catalog revision, snapshots all 10,000 item references, and verifies every identity plus the complete count and mutation totals.
- The Node performance runner imports the built adapter and the emitted shared fixture. Its discovery measurements therefore execute the same real adapter behavior as the compiled TypeScript benchmark.
- The memory scenario retains five separate 1 MiB `Buffer` allocations, probes `process.memoryUsage().rss` before and after each allocation, and records the allocation's exact `byteLength`. This avoids GC/page-accounting noise without rewriting observed samples. Validation still rejects negative, non-finite, malformed, or CV-over-0.20 samples.
- Timed warm-ups now use the same repeat count as measured samples. A committed-state standalone CLI check then correctly failed closed on short filter/cancel samples; those bounded workloads were lengthened and three subsequent standalone runs had maximum CVs `0.0922442076474254`, `0.11179821135254`, and `0.115839611487121`, all below `0.20` without rewriting samples.
- `test:phase9:performance` now builds the extension TypeScript target before running the Node contract tests, making the runner's built adapter dependency explicit.

### RED evidence

The original Task 1 RED record was only the absence of `performance.test.mjs`; it was not a written failing contract test. That history is stated here transparently and is not being relabeled as test-first evidence.

Round 4 added regression expectations before changing the implementation. The adapter-behavior expectation failed against the prior helper as follows:

```text
$ node --test --test-name-pattern="shared Testing API fixture" tools/phase9/performance.test.mjs
not ok 1 - shared Testing API fixture exercises two adapter refreshes and verifies every item identity
error: fixture.run is not a function
# pass 0
# fail 1
```

The deterministic memory expectation also failed against the prior RSS-delta implementation:

```text
$ node --test --test-name-pattern="exact closed schema" tools/phase9/performance.test.mjs
not ok 2 - performance baseline has the exact closed schema and six bounded scenarios
expected: [1048576, 1048576, 1048576, 1048576, 1048576]
actual:   [1056768, 1052672, 1052672, 1052672, 1052672]
# pass 0
# fail 1
```

The emitted benchmark failure was reproduced before the support-module move:

```text
$ node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
Error [ERR_MODULE_NOT_FOUND]: Cannot find module 'C:\codex_project\unitTest\apps\tools\phase9\testing-api-identity-fixture.mjs'
# pass 0
# fail 1
```

### Verification

Three consecutive focused runs passed during the main refactor; their total durations were `15962.4662 ms`, `15841.115 ms`, and `15630.8871 ms`. After final stability tuning, the pinned package run and an additional direct run both passed:

```text
$ node --test tools/phase9/performance.test.mjs
1..6
# tests 6
# pass 6
# fail 0
# pinned duration_ms 23423.635
# direct duration_ms 22855.9399
```

Pinned repository toolchain verification used bundled Node `v24.19.0` and pnpm `11.4.0`:

```text
$ pnpm test:phase9:performance
$ tsc -b apps/code-oss-extension/tsconfig.json && node --test tools/phase9/performance.test.mjs
tests 6
pass 6
fail 0
duration_ms 23423.635
```

Fresh TypeScript build and compiled benchmark:

```text
$ tsc -b apps/code-oss-extension/tsconfig.json --force
(no stdout; exit 0)

$ node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
itemCount: 10000
verifiedIdentityCount: 10000
replacementCountAfterSameRevision: 0
tests 1
pass 1
fail 0
duration_ms 1280.4554
```

Canonical CLI run:

```text
$ 1..3 | ForEach-Object { node tools/phase9/performance.mjs --out ".superpowers/phase9/performance/baseline-$_.json" }
run 1: filter CV 0.0657549158605745; cancel CV 0.0594886863151849; maximum CV 0.0922442076474254
run 2: filter CV 0.0181643373745536; cancel CV 0.0579070626834118; maximum CV 0.11179821135254
run 3: filter CV 0.0719699448000002; cancel CV 0.0491398852347939; maximum CV 0.115839611487121
all memory samplesBytes: [1048576,1048576,1048576,1048576,1048576]
```

Workspace smoke remains intentionally RED until Task 2 adds the workflow job:

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs
tests 21
pass 20
fail 1
AssertionError: phase9-performance job is missing
```

Final whitespace verification before the code commit:

```text
$ git diff --check
(no stdout; exit 0)
```

### Concerns

- The runner intentionally requires the extension TypeScript build because discovery now measures the real emitted `TestingApiAdapter`; the package script performs that build before the contract tests.
- The Task 2 workflow contract remains the only expected workspace-smoke failure. No workflow file was modified in Task 1 round 4.

## Review-fix round 5 (final requested fix round)

### Changes and metric definition

- `tools/phase9/performance.mjs` now publishes the unmodified `process.memoryUsage().rss` reading immediately after each allocation as `memory.samplesBytes`. This is **absolute post-operation process RSS**, not an allocation-size proxy and not an allocation delta. The five 1 MiB buffers remain retained during sampling; one allocation/probe is performed as warm-up.
- Renamed the allocation-size constant to `MEMORY_ALLOCATION_BYTES`. Allocation size remains the correctness check, independent of the RSS performance metric.
- The RSS finite checks and shared non-negative/finite/five-sample/CV-at-most-0.20 validation remain fail-closed. No retry, sample replacement, clipping, normalization, or threshold relaxation was added.
- The validator now requires `samplesBytes` for memory and `samplesMs` for timed scenarios.
- `tools/phase9/performance.test.mjs` wraps the real RSS probe without altering its returned value and compares the five published samples with the actual post-allocation readings. It also checks allocation correctness and rejects wrong-unit, negative, NaN, infinite, and unstable memory samples. The former constant-value expectation was removed.
- No workflow or product-runtime file was modified. No agents were dispatched.

### RED evidence before the production fix

Pinned Node `v24.19.0` was used throughout. The new live-probe and wrong-unit regression checks failed against the round-4 code:

```text
$ node --test --test-name-pattern='memory samples|validator rejects' tools/phase9/performance.test.mjs
✖ memory samples publish the actual post-allocation process RSS readings (8262.5082ms)
✖ validator rejects mutated summaries, correctness, and scenario fields (1.9643ms)
tests 2
pass 0
fail 2
duration_ms 9428.0175
actual:   [1048576,1048576,1048576,1048576,1048576]
expected: [317947904,319000576,320053248,321105920,322158592]
validator wrong-unit assertion: true !== false
exit 1
```

### Repeated focused verification (including the observed failure)

```text
$ 1..3 | ForEach-Object { node --test tools/phase9/performance.test.mjs; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE } }
run 1: tests 7; pass 7; fail 0; duration_ms 23766.9198
run 2: tests 7; pass 7; fail 0; duration_ms 22659.8127
run 3: tests 7; pass 3; fail 4; duration_ms 19464.7521
run 3 error: discovery-10000: coefficient of variation exceeds 0.20
exit 1
```

The third run failed in the existing discovery timing workload before memory ran; the rejected cached baseline caused four dependent tests to fail. Its independent CLI subprocess passed. This failure was not suppressed, and no discovery workload change was made in this RSS-focused fix. It is evidence of environmental timing sensitivity, not proof of universal CI stability.

The subsequent pinned package verification passed:

```text
$ .superpowers/runtime/task10-fixed-bin/pnpm.cmd --version
11.4.0
$ .superpowers/runtime/task10-fixed-bin/pnpm.cmd test:phase9:performance
$ tsc -b apps/code-oss-extension/tsconfig.json && node --test tools/phase9/performance.test.mjs
tests 7
pass 7
fail 0
duration_ms 21911.1566
exit 0
```

### Build and compiled benchmark

```text
$ node node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --force
(no stdout; exit 0)
$ node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
✔ 10,000 item catalog keeps every Test Item identity for the same revision (116.099ms)
{"runtime":"node-24.19.0","platform":"win32-x64","itemCount":10000,"verifiedIdentityCount":10000,"revision":"benchmark-r1","elapsedMs":112.44,"replacementCountAfterSameRevision":0}
tests 1
pass 1
fail 0
duration_ms 1159.2203
exit 0
```

### Three consecutive standalone RSS baselines

```text
$ 1..3 | ForEach-Object { node tools/phase9/performance.mjs --out ".superpowers/phase9/performance/round5-baseline-$_.json" }
run 1 samplesBytes: [297832448,298885120,299937792,300990464,302043136]
run 1 memory CV: 0.004963372602044129; maximum scenario CV: 0.0795476452742814
run 2 samplesBytes: [314146816,315199488,316252160,317304832,318357504]
run 2 memory CV: 0.004707329174069232; maximum scenario CV: 0.0522579508926273
run 3 samplesBytes: [297189376,298242048,299294720,300347392,301400064]
run 3 memory CV: 0.004974037026548315; maximum scenario CV: 0.073685861495377
all runs: sampleCount 5; warmupCount 1
all memory correctness: {"expected":1048576,"observed":1048576,"passed":1048576,"failed":0}
all runs: exit 0
```

### Workspace smoke and final checks

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs
tests 21
pass 20
fail 1
duration_ms 1496.3505
AssertionError [ERR_ASSERTION]: phase9-performance job is missing
exit 1

$ git diff --check
(no whitespace errors; exit 0; Git emitted LF-to-CRLF conversion warnings)
```

Workspace smoke remains intentionally RED until Task 2 adds the job.

### Commit and remaining concerns

- Final round-5 code SHA: `b38b96c99c4cb4c914cca0fb1772cfa2d4496e44` — `test: add phase9 performance baseline harness`.
- This report is committed separately with the same required message; its resulting commit SHA is supplied in the completion handoff, since a commit cannot contain its own SHA.
- Absolute process RSS includes the Node runtime and earlier in-process workloads; it is not an estimate of isolated buffer cost. The fixed runner/toolchain is important for comparisons.
- Three consecutive standalone baselines demonstrated stable raw RSS on this Windows host; Ubuntu CI has not been executed here. One repeated focused run demonstrated the unchanged discovery timing gate can still reject a noisy run.

## Final whole-branch schema fix (2026-09-15)

### Scope and final code SHA

- Final code commit: `61cb0d7d4658319098b0f6a5cfc2bb24993ac849` — `test: add phase9 performance baseline harness`.
- Code changes are limited to `tools/phase9/performance.mjs` and `tools/phase9/performance.test.mjs`. This report is committed separately with the same required message, so it can record the immutable code SHA.
- No workflow, evidence, CMake, producer/foundation, signing, Release, or PR behavior was changed. No agents were dispatched. The pre-existing untracked `.merge-stash-20260903/` directory was left alone.

### Changes

- Every scenario must have the existing exact correctness keys and successful counts: non-negative safe-integer `expected === observed === passed`, with `failed === 0`. Internally consistent but unsuccessful records are now rejected.
- Runtime is a closed object containing only `node`, `platform`, and `arch`. The validator accepts canonical three-component Node release versions and known platform/architecture strings; missing values, primitives, arrays, extra keys, malformed values, secret strings, and paths are rejected. Platform/architecture allowlists follow the repository's installed `@types/node` declarations.
- Runtime validation is intentionally portable: a baseline recorded on Node 24.18/Linux/arm64 may be validated on Node 24.19/Windows/x64. Builder tests separately require exact current-process runtime values.
- Hardware is a closed object containing only `cpus` and `totalMemoryBytes`, both positive safe integers. Validation checks recorded values, not equality to the validating host. The builder now uses the unaltered results of `os.cpus().length` and `os.totalmem()` rather than a constant CPU count.
- The builder test wraps the real OS calls, records their unmodified results, and checks emitted equality plus positive integer values. It also verifies the probes occurred, so a literal constant fails even on a single-CPU host. Existing RSS probes, five-sample and warm-up checks, scenario/summary checks, CV-at-most-0.20 rejection, unit checks, CLI behavior, and path/secret-redaction tests remain intact.
- New deterministic mutation fixtures exercise all six correctness records, both metadata objects, each required key, extra keys, wrong types, malformed strings, invalid numbers, and cross-host acceptance independently of benchmark timing.

### Toolchain and host readings

All verification below prepended `C:\Users\DELL\.cache\codex-runtimes\codex-primary-runtime\dependencies\node\bin` to PATH; the system default Node is older and was not used.

```text
$ node --version
v24.19.0
$ .superpowers/runtime/task10-fixed-bin/pnpm.cmd --version
11.4.0
$ node -e 'const os=require("node:os"); console.log(JSON.stringify({runtime:{node:process.versions.node,platform:process.platform,arch:process.arch},hardware:{cpus:os.cpus().length,totalMemoryBytes:os.totalmem()}}))'
{"runtime":{"node":"24.19.0","platform":"win32","arch":"x64"},"hardware":{"cpus":12,"totalMemoryBytes":16949751808}}
```

### RED then GREEN evidence

The four regression groups were written and run before the production fix. Relevant exact output:

```text
$ node --test --test-name-pattern='validator requires successful|validator closes|baseline publishes actual' tools/phase9/performance.test.mjs
✖ validator requires successful exact correctness records in every scenario (2.4739ms)
✖ validator closes runtime metadata and rejects injected or non-runtime strings (0.3403ms)
✖ validator closes hardware metadata and requires positive safe integer measurements (0.893ms)
✖ baseline publishes actual runtime and OS-probed CPU and total memory measurements (7657.8064ms)
ℹ tests 4
ℹ pass 0
ℹ fail 4
ℹ duration_ms 8742.7016
AssertionError [ERR_ASSERTION]: {"expected":10000,"observed":9999,"passed":9999,"failed":1}
true !== false
runtime: true !== false
hardware: true !== false
CPU probe count: 0 !== 1
exit 1
```

The same command after the initial production fix:

```text
✔ validator requires successful exact correctness records in every scenario (4.68ms)
✔ validator closes runtime metadata and rejects injected or non-runtime strings (0.6545ms)
✔ validator closes hardware metadata and requires positive safe integer measurements (0.6261ms)
✔ baseline publishes actual runtime and OS-probed CPU and total memory measurements (8140.483ms)
ℹ tests 4
ℹ pass 4
ℹ fail 0
ℹ duration_ms 9248.546
exit 0
```

The parent clarified that runtime validation, like hardware validation, must remain portable across audit hosts. The cross-host regression was added before replacing host equality with canonical-format validation:

```text
$ node --test --test-name-pattern='different host' tools/phase9/performance.test.mjs
✖ validator accepts canonical runtime metadata recorded on a different host (2.209ms)
ℹ tests 1
ℹ pass 0
ℹ fail 1
ℹ duration_ms 1128.1471
false !== true
exit 1

$ node --test --test-name-pattern='validator' tools/phase9/performance.test.mjs
✔ validator requires successful exact correctness records in every scenario (4.3513ms)
✔ validator closes runtime metadata and rejects injected or non-runtime strings (0.6788ms)
✔ validator accepts canonical runtime metadata recorded on a different host (0.3408ms)
✔ validator closes hardware metadata and requires positive safe integer measurements (0.6247ms)
✔ validator rejects mutated summaries, correctness, and scenario fields (8552.8653ms)
ℹ tests 5
ℹ pass 5
ℹ fail 0
ℹ duration_ms 9719.5366
exit 0
```

An initial repeated suite overlapped that deliberate RED portability addition: runs 1 and 2 passed 11/11 (`23157.625 ms`, `22347.133 ms`), and run 3 loaded the new test before the implementation change and failed only that test (12 tests, 11 pass, 1 fail, `22431.8948 ms`). This was an intermediate test-first state, not a final-code verification result. The final unchanged code was then verified afresh as follows.

### Final repeated performance and pinned package verification

```text
$ 1..3 | ForEach-Object { Write-Output "Final performance verification run $_"; node --test tools/phase9/performance.test.mjs; Write-Output "Exit code: $LASTEXITCODE" }
Final performance verification run 1
ℹ tests 12
ℹ pass 12
ℹ fail 0
ℹ duration_ms 22633.7896
Exit code: 0
Final performance verification run 2
ℹ tests 12
ℹ pass 12
ℹ fail 0
ℹ duration_ms 22386.236
Exit code: 0
Final performance verification run 3
ℹ tests 12
ℹ pass 12
ℹ fail 0
ℹ duration_ms 22695.6063
Exit code: 0

$ .superpowers/runtime/task10-fixed-bin/pnpm.cmd test:phase9:performance
$ tsc -b apps/code-oss-extension/tsconfig.json && node --test tools/phase9/performance.test.mjs
ℹ tests 12
ℹ pass 12
ℹ fail 0
ℹ duration_ms 22563.8818
exit 0
```

Every full performance run above includes a successful standalone CLI subprocess that writes JSON, then reads it back through `validateBaseline`; its temporary output is removed by the existing test. No checked-in evidence was regenerated.

### Forced TypeScript build and compiled benchmark

```text
$ node node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --force
(no stdout; exit 0)
$ node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
✔ 10,000 item catalog keeps every Test Item identity for the same revision (120.9387ms)
ℹ {"runtime":"node-24.19.0","platform":"win32-x64","itemCount":10000,"verifiedIdentityCount":10000,"revision":"benchmark-r1","elapsedMs":117.264,"replacementCountAfterSameRevision":0}
ℹ tests 1
ℹ pass 1
ℹ fail 0
ℹ duration_ms 1169.1388
exit 0
```

### Workspace smoke, gate/renderer, and diff verification

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs
✔ Phase 9 performance baseline job contract is fixed and reviewed (1.3698ms)
ℹ tests 21
ℹ pass 21
ℹ fail 0
ℹ duration_ms 1211.4248
exit 0

$ node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
ℹ tests 98
ℹ pass 98
ℹ fail 0
ℹ duration_ms 11679.1678
exit 0

$ node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md --check
(no stdout; exit 0)

$ git diff --check
(no whitespace errors; exit 0; Git emitted LF-to-CRLF conversion warnings)
```

The additional broader workspace suite was also attempted; its CMake prerequisite is absent from this command environment:

```text
$ node --test tools/workspace-smoke/workspace-smoke.test.mjs tools/workspace-smoke/workspace-config-schema.test.mjs tools/workspace-smoke/unit-test-ide-cmake-helper.test.mjs
✖ UnitTestIDE CMake helper has strict deterministic framework registration (1446.2695ms)
ℹ tests 31
ℹ pass 30
ℹ fail 1
ℹ duration_ms 1718.9692
Error: spawnSync cmake ENOENT
exit 1
```

### Remaining concerns

- The requested verification set passes; the optional broader workspace run cannot pass with CMake missing from PATH. No CMake files or toolchain configuration were changed to address an out-of-scope environment prerequisite.
- Timing gates are unchanged and remain fail-closed; four full final-code performance runs passed on this host, but this does not guarantee stability under arbitrary CI contention. This fix did not run Ubuntu CI.
- Canonical runtime validation accepts stable three-component Node release versions. A future policy permitting prerelease/nightly runtime strings or newly introduced platform/architecture values would need an explicit schema update.
- The historical checked-in performance evidence was intentionally not rewritten by this code-only fix; publishing replacement candidate evidence remains separate authorized work.

## Final-fix continuation — reject intermittent operation failures (2026-09-15)

### Final code SHA and scope

- Final code SHA for this continuation: `20535d6a60f660a72e11688c0cd7bcd840439ae5` — `test: add phase9 performance baseline harness`. This supersedes `61cb0d7d4658319098b0f6a5cfc2bb24993ac849` as the latest reviewed harness code state and retains its portable runtime/hardware validation.
- Only `tools/phase9/performance.mjs`, `tools/phase9/performance.test.mjs`, and this appended report changed. Workflow, evidence, CMake, release, producer/foundation, and signing were not modified; no agents were dispatched.
- `measured()` previously discarded warm-up results and retained only the final measured result. It now collects every operation result from the complete warm-up and all repeats in all five samples, then requires every result to equal the fixed expected value before summarization or publishing a correctness record. Any mismatch rejects the scenario with a fixed, scenario-prefixed error that does not echo the observed value.
- The memory scenario likewise checks each allocation's byte length instead of allowing an early incorrect allocation to be overwritten by a later successful one. Its existing warm-up check remains intact.
- Existing `timedScenario` and `memoryScenario` functions were exposed as named exports without changing their behavior before running RED tests. This permits focused regression testing through the real scenario implementations without adding test-only runtime configuration. The timed unit tests use a deterministic clock only to isolate correctness from timing noise; full baseline/CLI tests still use the actual clock and unmodified stability checks.

### RED evidence before the behavioral change

The initial two grouped regressions failed for the expected missing rejection/exception, while the success-case characterization passed (3 tests, 1 pass, 2 fail, `1091.0362 ms`). The cases were then split into subtests so every first/middle failure was independently demonstrated against the old behavior:

```text
$ node --test --test-name-pattern='timed scenarios|memory scenario rejects' tools/phase9/performance.test.mjs
▶ timed scenarios reject intermittent incorrect results throughout warm-up and samples
  ✖ incorrect operation 0 (2.7654ms)
  ✖ incorrect operation 1 (0.3924ms)
  ✖ incorrect operation 3 (0.3032ms)
  ✖ incorrect operation 7 (0.3108ms)
  ✖ incorrect operation 17 (0.3516ms)
✖ timed scenarios reject intermittent incorrect results throughout warm-up and samples (5.8874ms)
✔ timed scenarios preserve the fixed expected count when all repetitions succeed (1.0135ms)
▶ memory scenario rejects an incorrect first or middle allocation before a later success
  ✖ incorrect allocation 1 (1.6828ms)
  ✖ incorrect allocation 3 (1.4628ms)
✖ memory scenario rejects an incorrect first or middle allocation before a later success (3.6239ms)
ℹ tests 10
ℹ pass 1
ℹ fail 9
ℹ duration_ms 1072.9405
AssertionError [ERR_ASSERTION]: Missing expected rejection.
AssertionError [ERR_ASSERTION]: Missing expected exception.
exit 1
```

Indices are zero-based: timed operations 0 and 1 are early/middle warm-up; 3 and 7 are early/middle measured operations; 17 is the final operation. Each case injects exactly one incorrect result. Memory allocation 0 is warm-up; 1 and 3 are first/middle measured allocations, with subsequent allocations configured to succeed.

### GREEN focused evidence

```text
$ node --test --test-name-pattern='timed scenarios|memory scenario rejects' tools/phase9/performance.test.mjs
▶ timed scenarios reject intermittent incorrect results throughout warm-up and samples
  ✔ incorrect operation 0 (1.6769ms)
  ✔ incorrect operation 1 (0.3079ms)
  ✔ incorrect operation 3 (0.246ms)
  ✔ incorrect operation 7 (0.268ms)
  ✔ incorrect operation 17 (0.3281ms)
✔ timed scenarios reject intermittent incorrect results throughout warm-up and samples (4.3625ms)
✔ timed scenarios preserve the fixed expected count when all repetitions succeed (1.168ms)
▶ memory scenario rejects an incorrect first or middle allocation before a later success
  ✔ incorrect allocation 1 (0.7583ms)
  ✔ incorrect allocation 3 (0.9616ms)
✔ memory scenario rejects an incorrect first or middle allocation before a later success (2.1436ms)
ℹ tests 10
ℹ pass 10
ℹ fail 0
ℹ duration_ms 1081.502
exit 0
```

### Fresh full verification

All commands used the same pinned Node `v24.19.0` PATH described above and pnpm `11.4.0` wrapper. Counts include nested subtests.

```text
$ 1..3 | ForEach-Object { Write-Output "Intermittent-fix performance run $_"; node --test tools/phase9/performance.test.mjs; Write-Output "Exit code: $LASTEXITCODE" }
Intermittent-fix performance run 1
ℹ tests 22
ℹ pass 22
ℹ fail 0
ℹ duration_ms 21901.5668
Exit code: 0
Intermittent-fix performance run 2
ℹ tests 22
ℹ pass 22
ℹ fail 0
ℹ duration_ms 22018.8848
Exit code: 0
Intermittent-fix performance run 3
ℹ tests 22
ℹ pass 22
ℹ fail 0
ℹ duration_ms 22309.1028
Exit code: 0

$ .superpowers/runtime/task10-fixed-bin/pnpm.cmd test:phase9:performance
$ tsc -b apps/code-oss-extension/tsconfig.json && node --test tools/phase9/performance.test.mjs
ℹ tests 22
ℹ pass 22
ℹ fail 0
ℹ duration_ms 21876.4565
exit 0

$ node node_modules/typescript/bin/tsc -b apps/code-oss-extension/tsconfig.json --force
(no stdout; exit 0)

$ node --test apps/code-oss-extension/dist/test/testing-api-benchmark.test.js
✔ 10,000 item catalog keeps every Test Item identity for the same revision (130.0672ms)
ℹ {"runtime":"node-24.19.0","platform":"win32-x64","itemCount":10000,"verifiedIdentityCount":10000,"revision":"benchmark-r1","elapsedMs":126.49,"replacementCountAfterSameRevision":0}
ℹ tests 1
ℹ pass 1
ℹ fail 0
ℹ duration_ms 1210.7666
exit 0

$ node --test tools/workspace-smoke/workspace-smoke.test.mjs
ℹ tests 21
ℹ pass 21
ℹ fail 0
ℹ duration_ms 1525.2577
exit 0

$ node --test tools/phase9/validate.test.mjs tools/phase9/audit.test.mjs
ℹ tests 98
ℹ pass 98
ℹ fail 0
ℹ duration_ms 11856.3903
exit 0

$ git diff --check
(no whitespace errors; exit 0; LF-to-CRLF conversion warnings only)
```

### Renderer check: expected evidence-lineage drift remains unresolved

```text
$ node tools/phase9/render.mjs --registry tools/phase9/gates.json --baseline docs/superpowers/evidence/phase9/baseline.json --receipts docs/superpowers/evidence/phase9/receipts --repository-root . --json-out docs/superpowers/evidence/phase9/gate-matrix.json --markdown-out docs/superpowers/evidence/phase9/gate-matrix.md --check
PHASE9_MATRIX_DRIFT: rendering failed
exit 1
```

The earlier schema-wave renderer check ran before its code commit and passed. After that commit, the checked-in candidate `a3e8a844479983a4ba1f803babd4001ead0422c6` has a descendant with tested-content changes, so the existing candidate matrix no longer matches. A read-only evaluation using `loadPhase9Inputs`, Git's candidate-to-HEAD changed paths, and `evaluateRecordedMatrix` confirmed:

```text
{"counts":{"pass":0,"missing":46,"failed":13,"deferred":3},"reasons":["candidate-descendant-changed-tested-content"]}
exit 0
```

No evidence was rewritten to clear this failure. Replacement candidate evidence requires the separately authorized evidence workflow. The prior optional expanded workspace-suite CMake/PATH limitation also remains; that out-of-scope suite was not rerun in this continuation. No timing gates, sample handling, portable schema fields, or CI authority boundaries were relaxed.
