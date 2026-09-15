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

`92c5d38e93ca1f15021cca6bd569fd9c5e4b0c4f` — `test: add phase9 performance baseline harness`

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
