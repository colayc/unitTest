# Post-remediation defect fix report

## Scope and result

Corrected the four final Task 2A remediation defects without widening into
Task 5, remote operations, release, or signing:

1. Unix retained cleanup now selects `AT_REMOVEDIR` for directories and keeps
   `0` for regular files. Focused Unix tests cover both kinds.
2. Windows has no `os.Remove` fresh-child fallback. Fresh cleanup retains the
   original child identity and only deletes through a DELETE-capable pinned
   handle after native identity comparison; replacement is rejected.
3. `WriteAtomic` retains the just-created `gcovr` identity before obtaining
   the cleanup pin. A cleanup-pin failure joins retained cleanup and closes
   into the returned error; the injected `gcovr` failure test observes no
   residue.
4. The final `owned.Verify()` failure now returns `errors.Join(err,
   owned.Close())`, so cleanup failures are visible to the caller.

## Tests and checks

All commands used workspace-local `GOCACHE` directories.

- Focused regression tests: PASS
  `go test ./apps/test-service/internal/coveragebundle -run '^(TestDescriptorGcovrCleanupPinFailureRemovesFreshTaskRoot|TestDescriptorFinalVerifyReturnsCleanupFailure|TestFreshPinnedDirectoryCleanupRejectsReplacement)$' -count=1`
- Full coveragebundle package: PASS
  `go test ./apps/test-service/internal/coveragebundle -count=1`
- Race coveragebundle package: PASS
  `go test -race ./apps/test-service/internal/coveragebundle -count=1`
- Linux amd64 compile-only: PASS
  `GOOS=linux GOARCH=amd64 go test -c ./apps/test-service/internal/coveragebundle`
- Windows amd64 compile-only: PASS
  `GOOS=windows GOARCH=amd64 go test -c ./apps/test-service/internal/coveragebundle`
- `git diff --check`: PASS.

The initial Linux `go test -run '^$'` attempt could not execute a Linux test
binary on this Windows host (`%1 is not a valid Win32 application`); it was
replaced with the successful compile-only command above.

## Boundaries preserved

- No remote push, PR, merge, release, or signing action.
- No Task 5 implementation.
- User-owned `.merge-stash-20260903/` remains untouched.

## Round 1 follow-up: first pin and runner cleanup propagation

The first post-remediation review found two remaining ownership gaps. This
round closes both without extending into Task 5.

1. Task-child creation now returns an immediately usable retained cleanup pin.
   On Unix, `createPinnedCleanupDirectory` creates the private child and, if
   the first DELETE-pin acquisition fails, deletes it relative to the retained
   private `0700` parent with `AT_REMOVEDIR`. On Windows it creates the child
   relative to the retained parent handle using `NtCreateFile` and receives the
   DELETE-capable handle atomically; no mutable child pathname fallback is
   used. The shared post-create validation seam removes through that exact pin
   before returning its injected failure.
2. `PrepareRunner` now joins cleanup failures on both ownership-transfer error
   paths: descriptor parse/mismatch joins `owned.Close()`, and final execution
   verification joins `execution.Close()`. Regression tests inject and assert
   both cleanup failures.

Additional verification (all with workspace-local `GOCACHE`):

- Focused first-pin and runner cleanup regressions: PASS.
- Full `coveragebundle` package: PASS.
- `coveragebundle` race package: PASS.
- Linux amd64 compile-only: PASS.
- Windows amd64 compile-only: PASS.
- `git diff --check`: PASS.
