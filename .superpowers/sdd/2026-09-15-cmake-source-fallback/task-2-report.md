# Task 2 report — Go CMake consumer source contract

## Status

Complete. The Go consumer now accepts only the locked Kitware GitHub release asset for Linux while retaining the exact cmake.org Windows source rule. Archive digests, installed-file digests, bundle layout, executable paths, version, and license policy are unchanged.

## Implementation

- Updated the production Linux URL to `https://github.com/Kitware/CMake/releases/download/v4.3.4/cmake-4.3.4-linux-x86_64.tar.gz`.
- Kept URL validation closed: exact literal HTTPS URL, exact host and path, no credentials, raw path, query, or fragment.
- Added coverage for the exact approved Linux URL and rejection of foreign repositories, tags, paths, credentials, query strings, fragments, non-HTTPS URLs, and unrelated hosts.
- Updated only the Linux URL in the valid Go manifest fixture.

## TDD evidence

RED command:

```text
go test ./apps/test-service/internal/cmake/... -run 'TestManifest(MatchesProductionBundleShape|AcceptsExactLinuxArchiveURL|RejectsUnapprovedLinuxArchiveURLs)' -count=1
```

The run failed in `TestManifestMatchesProductionBundleShape` because the fixture still contained the old cmake.org Linux URL and in `TestManifestAcceptsExactLinuxArchiveURL` because the consumer rejected the new GitHub URL. The negative URL variants were already rejected.

GREEN verification:

- `go test ./apps/test-service/internal/cmake/... -run 'TestManifest(MatchesProductionBundleShape|AcceptsExactLinuxArchiveURL|RejectsUnapprovedLinuxArchiveURLs)' -count=1` — PASS.
- `go test ./apps/test-service/internal/cmake/... -count=1` — PASS.
- Pinned Node/Corepack `pnpm --filter @unit-test-ide/service-probe build` — PASS.
- Pinned Node/Corepack `pnpm --filter @unit-test-ide/service-probe test` — PASS: 85 tests, 84 passed, 1 expected opt-in WFP integration skip, 0 failed.
- Pinned Node `tools/service-probe/build-service.mjs` — PASS.
- `git diff --check` — PASS.

## Commit

`fix: align cmake consumer source contract`

## Concerns

None in the implementation. The globally exposed Node.js 20.15.0/pnpm 11.19.0 pair does not satisfy the repository engines, so service-probe verification was run with the installed Codex Node.js 24.19.0 runtime and Corepack-resolved pnpm 11.4.0. No project files were changed for that environment adjustment.
