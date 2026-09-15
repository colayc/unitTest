# CMake 4.3.4 Source Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the unavailable CMake 4.3.4 Linux download origin with the matching Kitware GitHub Release asset while preserving immutable URL, redirect, archive digest, and installed-file verification.

**Architecture:** Keep the existing manifest as the single source of the locked archive identity, but point only the Linux archive at the official Kitware GitHub release asset whose published digest equals the current manifest digest. Extend URL validation narrowly for that exact release path and GitHub's signed `release-assets.githubusercontent.com` redirect host; all other hosts, paths, credentials, query tampering, and redirects remain rejected.

**Tech Stack:** Node.js `fetch`, JSON manifest, Node test runner, existing CMake bundle verifier.

**Spec:** `docs/superpowers/specs/2026-09-13-phase9-gate-evidence-matrix-design.md` supply-chain and fail-closed constraints; `tools/cmake-bundle/prepare.mjs` contract.

## Global Constraints

- Preserve CMake version `4.3.4`, archive SHA-256 `ca6f08ccbd5e6b0a9068d33317d0d1aff7278d08cccaed4529b8fbead7942a68`, installed-file digests, and archive layout.
- Allow only the exact official Kitware GitHub release URL and its HTTPS signed release-assets redirect; reject arbitrary GitHub repositories, tags, paths, hosts, credentials, and user-controlled URLs.
- Keep archive download, SHA-256, archive-entry, installed-file, capability, and license checks fail-closed.
- Do not modify product runtime, producer/foundation, signing, legal, release, or Phase 9 evidence files in this plan.

---

### Task 1: Lock the GitHub source and validation contract

**Files:**
- Modify: `tools/cmake-bundle/manifest.json`
- Modify: `tools/cmake-bundle/prepare.mjs`
- Modify: `tools/cmake-bundle/prepare.test.mjs`
- Modify: `tools/cmake-bundle/README.md`

**Interfaces:**
- Linux manifest URL: `https://github.com/Kitware/CMake/releases/download/v4.3.4/cmake-4.3.4-linux-x86_64.tar.gz`.
- `validateDistributionURL(value, lockedURL)` accepts only the exact locked URL or, for a GitHub locked URL, an HTTPS `release-assets.githubusercontent.com` redirect generated for that locked asset; it rejects all other origins/paths and credential/query/fragment tampering.

- [ ] **Step 1: Add failing tests** for the exact GitHub URL, signed redirect host, foreign repository/tag/path, non-HTTPS, credentials, and query/fragment tampering; assert the manifest's digest remains unchanged.
- [ ] **Step 2: Run the focused CMake tests** and confirm the new GitHub cases fail before implementation.
- [ ] **Step 3: Implement the exact manifest/validator change** with no fallback to unverified sources and no digest changes.
- [ ] **Step 4: Update README source coordinates** and run the full CMake bundle test suite plus existing workspace smoke.
- [ ] **Step 5: Commit** with `fix: use verified github cmake release asset` and write `.superpowers/sdd/2026-09-15-cmake-source-fallback/task-1-report.md`.

### Task 2: Synchronize the Go consumer URL contract

**Files:**
- Modify: `apps/test-service/internal/cmake/manifest.go`
- Modify: `apps/test-service/internal/cmake/manifest_test.go`
- Modify: `apps/test-service/internal/cmake/testdata/bundle-manifest.valid.json`

Update the Linux expected URL and URL validator to accept only the exact Kitware GitHub release path used by the manifest, while retaining the exact `cmake.org` Windows rule. Add rejection tests for foreign repositories/tags/paths, credentials, query/fragment, non-HTTPS, and unrelated hosts. Run the Go CMake tests and the native service-probe contract tests; write a report and commit `fix: align cmake consumer source contract`.

### Task 3: Re-run Phase 9 candidate evidence

After Tasks 1–2 review pass, return to `docs/superpowers/plans/2026-09-14-phase9-batch-d-performance.md` Task 3's staging sequence. Push the new implementation commits to both already-authorized remotes, dispatch the workflow, create the candidate receipt only after all required jobs succeed, and keep `releaseReady=false`.
