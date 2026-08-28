# Task 2 report — runtime and Linux mode inventories

## Implementation

- Added `tools/release/producer/runtime-inventory.mjs` with a safe recursive runtime scanner, streaming SHA-256 file hashing, closed full inventories, canonical record-byte tree digests, closed summaries, Linux mode-record validation, and a `create` CLI that writes one-line JSON artifacts ending in one newline.
- Extended `tools/release/linux/runtime-mode-inventory.mjs` with `createRuntimeModeInventory()` and a `create --root --launcher-sha256 --out` CLI while preserving `restore` behavior. Creation is explicitly limited to an effective Linux platform and derives execute state from real file modes.
- Added focused coverage in the matching runtime inventory tests for strict ordering/completeness, Windows executable normalization, Linux mode record mismatches, tree-digest sensitivity, time/directory-mode insensitivity, and the non-Linux Linux-mode creation rejection.

## RED / GREEN evidence

RED command:

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Before implementation it failed as intended: `createRuntimeModeInventory` was not exported and `tools/release/producer/runtime-inventory.mjs` did not exist (2 failing test files).

GREEN command:

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Result: 16 tests, 8 passed, 0 failed, 8 skipped.

Post-commit adjacent verification:

```powershell
node --test tools/release/code-oss-runtime.test.mjs tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Result: 47 tests, 33 passed, 0 failed, 14 skipped.

The 14 skips are truthful host/capability skips: Linux execute-mode behavior (including the newly added Linux creation/restore round trip), Linux special-file tests, case-alias fixtures unavailable on this filesystem, unavailable direct symlink privileges, and the platform representation of junction/reparse fixtures. The existing Windows directory-junction rejection test ran and passed.

## Commit and diff check

- Commit: `0bcf923 feat: inventory trusted Code-OSS runtimes`
- Only the four Task 2 implementation/test files were committed.
- `git diff --check` completed cleanly before commit; `git show --check --stat --oneline HEAD` completed cleanly after commit.

## Self-review

- Full inventory objects are closed, sorted, portable-path-only, case-alias-free, path-free, and use safe integer byte totals.
- Every input file is verified through a stream hash; Linux executable state comes only from the validated mode inventory, and Windows records are always `false`.
- Both serializers use `JSON.stringify(value) + "\n"`; output failures surface as `RELEASE_PRODUCER_OUTPUT_INVALID` and runtime/mode validation preserves `RELEASE_INPUT_*` codes.
- `restoreRuntimeModes()` retains its validation-before-chmod flow and exact `0755`/`0644` normalization.

## Concerns

Linux-only creation and restoration branches cannot be executed on this Windows runner. They are explicitly platform-gated and need one Linux hosted-run confirmation; this is a coverage limitation, not a claimed validation result.

## Fix Round 1

Addressed findings:

1. Descriptor-bound stream hashing now opens with no-follow where supported, checks `fstat` identity/type/size before and after reading, and verifies byte count for both producer inventory and Linux mode creation/verification.
2. `createRuntimeModeInventory()` no longer accepts a platform dependency override and gates only on `process.platform`.
3. Both public creation requests now require exact plain-object shapes using `Reflect.ownKeys`, rejecting null, extras, non-enumerable properties, and symbols with stable errors.
4. The producer CLI rejects equivalent output destinations and stages JSON in exclusive unique temporary directories before pair commit; failed pair commits restore staged pre-existing targets.
5. The shared producer scanner now invokes the same concrete Windows reparse-point inspection before any scan/hash, including Windows-hosted pure Linux inventory evaluation.
6. Added covering public-shape and collision/path-leak regressions while retaining existing record mismatch, portable path, alias, link/junction, special-entry, digest, and Linux mode coverage.

Covering tests: `tools/release/producer/runtime-inventory.test.mjs` and `tools/release/linux/runtime-mode-inventory.test.mjs`; adjacent validation: `tools/release/code-oss-runtime.test.mjs`.

RED:

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Result before fixes: 2 failures — null requests produced native destructuring `TypeError` rather than `RELEASE_INPUT_INVALID`; the collision assertion was not reached after that first expected failure.

GREEN focused result: 18 tests, 10 passed, 0 failed, 8 Linux-only skips.

Adjacent GREEN command:

```powershell
node --test tools/release/code-oss-runtime.test.mjs tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Result: 49 tests, 35 passed, 0 failed, 14 truthful platform/capability skips. `git diff --check` and `git show --check` were clean.

Commits: `3410fb2 fix: harden runtime inventory production`; `00fb162 fix: reject reparse points in runtime scanner`.

Residual concern: native Linux-only create/restore tests remain intentionally skipped on this Windows runner and must execute on the hosted Ubuntu workflow; no claim of native Linux execution is made here.

## Fix Round 2

- Descriptor-bound hashing now compares file identity, safe size, type, full mode, and nanosecond modification/change timestamps before and after stream reads. Linux executable state is taken from that descriptor snapshot. Product metadata is parsed from descriptor-bound captured bytes (bounded to 1 MiB).
- Linux-host behavior test coverage now uses actual-host conditional execution rather than an ignored platform override.
- Focused RED exposed the prior native `TypeError`/collision coverage gaps; focused GREEN: 18 tests, 10 passed, 0 failed, 8 truthful Linux-only skips.
- Commit: `3e210c5 fix: bind runtime inventory descriptor snapshots`; `git show --check` clean.
- Residual concern: native Linux tests still require hosted Ubuntu execution; they were not claimed on this Windows host.

## Fix Round 2 completion

### Completion status and open-item matrix

1. **Exact record bytes and safe totals completed.** `tools/release/producer/runtime-inventory.test.mjs` now contains `Windows inventory binds exact path, decimal size, digest, and executable record bytes`, `tree digest independently binds path, decimal size, SHA, and executable bytes`, and `inventory validation rejects unsafe file integers and aggregate total overflow`. Expected tree digests are independently constructed from literal UTF-8 `path + NUL + decimal size + NUL + SHA-256 + NUL + executable bit + NUL` bytes. Unsafe per-file integers and aggregate overflow are rejected.
2. **Descriptor snapshots and identity binding completed.** Producer regressions `descriptor snapshots reject a deterministic same-object same-size content mutation`, Linux-hosted `descriptor snapshots reject a deterministic same-object mode mutation`, and `descriptor-captured product bytes and launcher digest are the inventory identity source` use the closed `__testOnlyRuntimeInventory` hook surface. `tools/release/linux/runtime-mode-inventory.test.mjs` adds Linux-hosted `create rejects deterministic same-object same-size content and mode mutations` through the separate closed `__testOnlyRuntimeModeInventory` surface. Production hashes each producer file once; product identity is parsed from the bytes captured during that descriptor read, and the expected launcher SHA is compared to the launcher record generated by that same read.
3. **Producer and mode-record rejection matrix completed.** `Linux inventory accepts only complete exact sorted size and digest mode records` covers missing, extra, reordered, case-aliased, unsafe, size-drifted, digest-drifted, launcher-mode-drifted, and valid non-launcher executable-state changes. `producer rejects missing required files and directory link or junction entries` runs the Windows junction/reparse path. Linux-hosted `producer rejects direct symbolic links, unsafe paths, case aliases, and special files` covers the remaining filesystem types. Existing mode-schema tests retain closed-schema, unsafe-path, alias, order, and launcher-record validation.
4. **Producer CLI bytes and rollback matrix completed.** `CLI writes both success artifacts as canonical path-free one-line JSON` compares the exact full-inventory and summary bytes to `JSON.stringify(value) + "\\n"`, rejects CRLF/double-newline output, and proves neither output contains the runtime or fixture absolute path. `CLI rejects identical and hard-link-equivalent destinations before writing` covers lexical and inode-equivalent destinations. `parallel second-stage failure removes the successful peer stage and creates no output`, `second-commit failure removes new outputs and restores both pre-existing targets`, and `post-publish failure restores both pre-existing targets` use closed stage/commit hooks and verify exact rollback. `staging rejects an output ancestor replaced by a link after inspection` and `CLI refuses a symlinked output ancestor without touching its referent` cover observed link replacement and pre-existing junction/symlink targets. Parallel stages now use `Promise.allSettled()` so a fulfilled peer is always cleaned after the other stage rejects.
5. **Linux mode creation negatives completed.** `tools/release/linux/runtime-mode-inventory.test.mjs` adds Linux-hosted `mode creation rejects unsafe paths, case aliases, links, special files, and a non-executable launcher`, `mode create CLI writes canonical output and rejects malformed, stale-temp, and symlink targets`, and `mode create CLI rejects a genuinely unwritable output directory`. The permission test truthfully skips only when run as root. Unique exclusive staging makes an attacker-created legacy PID temp file irrelevant and preserves it unchanged.
6. **Implementation defects proved by the matrix fixed.** Besides the orphaned successful stage, the tests/review exposed and fixed hard-link-equivalent output acceptance, symlinked output-ancestor acceptance, duplicate producer identity reads, post-publish rollback state recorded too late, and the native-Linux `hashed` block-scope error. Output target, stage-file, and output-directory identities are revalidated around backup/publication; cleanup refuses recursive removal after an observed output-directory identity change.
7. **Actual-host Linux gating corrected.** The off-Linux test is declared as `(process.platform === "linux" ? test.skip : test)`, while every Linux behavior uses `linuxOnly`, so hosted Ubuntu runs the positive/negative creation cases. Review also found that the Linux CLI passed the whole parsed options object into the exact two-key creation API; `runCli()` now narrows it to `{root, expectedLauncherSha256}`, preventing a guaranteed hosted-Linux closed-shape failure.

### TDD RED evidence

Initial matrix RED command:

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Initial result: 31 tests, 12 passed, 7 failed, 12 skipped. One failure was a hand-counted fixture-total mistake (corrected from 100 to the independently verified 101 bytes before implementation). The six implementation-relevant failures were missing closed descriptor/fault hooks, duplicate product/launcher descriptor reads, accepted hard-link-equivalent destinations, absent deterministic parallel-stage/commit injection, and accepted symlinked output ancestors.

Review-driven RED command:

```powershell
node --test tools/release/producer/runtime-inventory.test.mjs
```

Result before the follow-up fixes: 18 tests, 14 passed, 2 failed, 2 skipped. `post-publish failure restores both pre-existing targets` failed because the post-publish hook/state did not exist; `staging rejects an output ancestor replaced by a link after inspection` failed because the swap was accepted. Static review additionally proved the Linux `create` CLI passed extra closed-request keys and would fail on its actual host.

### Final GREEN evidence

Focused command:

```powershell
node --test tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Final result: 36 tests, 22 passed, 0 failed, 14 skipped. The skips are the narrow Linux creation/mode/special-file/unwritable-output cases plus existing Linux restore cases on this Windows host; all are selected by the actual host and will run on hosted Ubuntu.

Adjacent command:

```powershell
node --test tools/release/code-oss-runtime.test.mjs tools/release/linux/runtime-mode-inventory.test.mjs tools/release/producer/runtime-inventory.test.mjs
```

Final result: 67 tests, 47 passed, 0 failed, 20 skipped. The six additional skips are existing validator capability/platform skips (native Linux execute checks, unavailable direct Windows file-symlink privilege, special-file/path representation, reparse representation, and case-alias representation).

`git diff --check` and the staged diff check completed with exit code 0; Git emitted only the repository's LF-to-CRLF checkout warnings.

### CLI rollback and byte evidence

- Both producer success files are asserted byte-for-byte as one compact JSON line plus exactly one LF, with no CR, second newline, or absolute runtime/fixture path.
- The Linux mode CLI success file and stdout are asserted identical canonical bytes on Linux.
- A deterministic second parallel-stage failure leaves no output and no surviving stage directory after the peer succeeds.
- A deterministic second publish failure leaves no new partial pair; with pre-existing files it restores exact `previous-full\\n` and `previous-summary\\n` bytes.
- A deterministic failure after the second rename exercises the previously missing post-publication state and restores both exact pre-existing files.
- Existing linked ancestors, an ancestor swapped after inspection, identical targets, and hard-linked equivalent targets fail closed without modifying the linked referent.

### Commits

- Prior Fix Round 2 descriptor baseline: `3e210c5 fix: bind runtime inventory descriptor snapshots`.
- Completion implementation and deterministic matrix: `29bed4b fix: complete runtime inventory hardening`.
- This report is committed separately so it can cite the immutable implementation commit.

### Residual concerns

- Native Linux execution was not available on this Windows host; `wsl.exe --list --quiet` was denied with `Wsl/EnumerateDistros/Service/E_ACCESSDENIED`. Linux-only tests are therefore structured and statically reviewed for hosted Ubuntu but are not claimed as locally executed.
- Node exposes pathname-based `mkdtemp`/`writeFile`/`rename`, not portable directory-handle-relative `openat`/`renameat`. The implementation rejects pre-existing and deterministically observed ancestor swaps, revalidates directory identity/realpath around each stage and publish boundary, and refuses cleanup after an observed directory identity change. A malicious actor capable of swapping the runner-owned output directory in the remaining syscall-width check/use window is outside the trusted runner-local output-directory contract; eliminating that micro-TOCTOU would require a native handle-relative helper.
