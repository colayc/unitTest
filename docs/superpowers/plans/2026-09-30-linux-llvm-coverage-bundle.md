# Linux LLVM Coverage Bundle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Provide a reproducible, verified Linux LLVM bundle for the required `coverage-linux-clang` job without relying on system PATH or an unpopulated GitHub variable.

**Architecture:** Add a small Linux-only bundle contract that reuses the already reviewed LLVM 22.1.8 archive coordinates, atomically prepares a full extracted tree, canonicalizes the four required tool entry points as regular files, and writes a closed READY record. The workflow prepares and checks this bundle before entering the offline namespace, then exports its absolute runner-local root to the existing native fixture.

**Tech Stack:** Node.js 24 ESM, Node test runner, Ajv JSON Schema, GitHub Actions Bash, pinned LLVM 22.1.8 archive.

## Global Constraints

- The required tools are `clang`, `clang++`, `llvm-profdata`, and `llvm-cov` under `bin/`.
- The bundle source is the immutable official LLVM 22.1.8 Linux x64 archive already locked in `tools/testgen-bundle/manifest.json`.
- The archive SHA-256, source commit, license digest, platform, and archive root are fixed inputs; no `latest`, PATH fallback, apt installation, or skip is allowed.
- Network is allowed only during the preparation step before `tools/linux-offline/run.mjs`; all later verification and execution are offline.
- Publication is atomic; a failed or interrupted preparation must not leave a READY final bundle.
- Only focused bundle and workflow contract tests are required locally for this change.

---

### Task 1: Add the immutable Linux LLVM bundle contract

**Files:**
- Create: `tools/llvm-coverage-bundle/manifest.schema.json`
- Create: `tools/llvm-coverage-bundle/manifest.json`
- Create: `tools/llvm-coverage-bundle/manifest.test.mjs`

**Interfaces:**
- Produces a schema-valid manifest with `schemaVersion`, `llvmVersion`, `sourceCommit`, `platform`, `archive`, `archiveRoot`, `tools`, and `license`.
- The four tool values are exact archive-relative paths and the license path is under `licenses/`.

- [x] **Step 1: Write the failing manifest tests** asserting schema validity, exact LLVM 22.1.8 URL/SHA-256/source commit, four tool roles, safe paths, and the fixed license digest.
- [x] **Step 2: Run the focused test and verify it fails** because the new manifest files do not exist.
- [x] **Step 3: Add the JSON schema and manifest** using the coordinates already present in `tools/testgen-bundle/manifest.json`.
- [x] **Step 4: Run the focused test and verify it passes.**
- [x] **Step 5: Commit** with `build: lock Linux LLVM coverage bundle`.

### Task 2: Implement atomic prepare/check and tests

**Files:**
- Create: `tools/llvm-coverage-bundle/prepare.mjs`
- Create: `tools/llvm-coverage-bundle/check.mjs`
- Create: `tools/llvm-coverage-bundle/prepare.test.mjs`
- Create: `tools/llvm-coverage-bundle/check.test.mjs`
- Modify: `package.json`

**Interfaces:**
- `prepareBundle({ manifest, outputRoot, downloadArchive }) -> { root, manifestSha256 }` downloads and verifies the locked archive, extracts to staging, canonicalizes required tool entry points, writes `manifest.json` and `READY`, verifies the candidate, and atomically publishes it.
- `checkBundle({ root, manifest }) -> { root, manifestSha256, toolDigests }` performs read-only verification and never downloads or mutates.
- CLI: `node tools/llvm-coverage-bundle/prepare.mjs --platform linux-x64 --output-root <path>` and `node tools/llvm-coverage-bundle/check.mjs --platform linux-x64 --root <path>`.

- [x] **Step 1: Write failing tests** for regular required tools, manifest/READY identity, archive digest rejection, symlink canonicalization, missing tool rejection, and atomic no-READY-on-failure behavior.
- [x] **Step 2: Run only the new tests and verify the expected failures.**
- [x] **Step 3: Implement the minimal manifest validation, archive preparation, tool canonicalization, READY writing, and read-only checker.**
- [x] **Step 4: Run the new tests and verify they pass.**
- [x] **Step 5: Add package scripts** `prepare:llvm-coverage-bundle`, `check:llvm-coverage-bundle`, and `test:llvm-coverage-bundle`.
- [x] **Step 6: Run the focused package test command and commit** with `build: prepare verified Linux LLVM coverage bundle`.

### Task 3: Wire the required hosted job

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/llvm-coverage-bundle/manifest.test.mjs`
- Modify: `docs/native-e2e.md`

**Interfaces:**
- `coverage-linux-clang` prepares and checks the bundle before the offline namespace step.
- The preparation step exports `UTIDE_NATIVE_LLVM_BUNDLE` through `GITHUB_ENV`; the existing fail-closed four-tool check and native fixture remain authoritative.
- The job no longer reads `vars.UTIDE_NATIVE_LLVM_BUNDLE` on a GitHub-hosted runner.

- [x] **Step 1: Add a failing workflow contract assertion** for prepare/check ordering, `GITHUB_ENV` export, and removal of the empty repository-variable dependency.
- [x] **Step 2: Run the focused contract test and verify it fails.**
- [x] **Step 3: Add the preparation/check steps before the offline boundary and document the boundary.**
- [x] **Step 4: Run the focused contract test and verify it passes.**
- [x] **Step 5: Run `git diff --check` and the complete focused bundle test command.**
- [x] **Step 6: Commit** with `ci: prepare approved Linux LLVM coverage bundle`.

## Review Focus

- No network access after the offline namespace begins.
- No symlink or PATH-based tool substitution.
- No stale repository variable can override the prepared bundle path.
- A failed download, extraction, digest, or tool check cannot publish READY output.
- Existing Windows and release packaging jobs are unchanged.
