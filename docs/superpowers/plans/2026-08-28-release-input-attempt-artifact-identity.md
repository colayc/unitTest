# Release Input Attempt and Artifact Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the reviewed cross-attempt release-input trust gap by binding producer provenance and every consumer download to one GitHub Actions run attempt and immutable artifact IDs/digests.

**Architecture:** Extend the closed provenance schema with producer run identity and the immutable identity of all three input artifacts. The producer captures upload-action outputs and downloads by artifact ID; the foundation trust job bootstraps provenance from an attempt-qualified name, validates the complete API artifact listing, and exports only trusted IDs. Package jobs download only those IDs and revalidate the producer attempt before and after transport.

**Tech Stack:** Node.js 24.18.0 ESM, `node:test`, GitHub Actions, GitHub REST API, pinned `actions/upload-artifact` v7 commit, pinned `actions/download-artifact` v4.3.0 commit, PowerShell 7, Bash, pnpm 11.4.0.

**Spec:** `docs/superpowers/specs/2026-08-28-release-input-attempt-artifact-identity-design.md`

## Global Constraints

- Keep the current isolated worktree and branch `codex/trusted-release-input-producer`; do not modify the main checkout.
- Preserve the existing action pins and add no dependency.
- Keep logical artifact names fixed as `code-oss-windows-x64`, `code-oss-linux-x64`, `appimagetool-linux-x64`, and `release-input-provenance`.
- Transport names are exactly the fixed logical name, one hyphen, and the positive canonical decimal run attempt.
- Run IDs and artifact IDs are non-zero canonical decimal strings; attempts are positive safe integers.
- Repository artifact digests are lowercase 64-character hexadecimal values; GitHub API artifact digests must be exactly the literal `sha256:` followed by that lowercase digest.
- Query workflow-run artifacts with `per_page=100`; require `total_count === artifacts.length` and reject `total_count > 100`.
- Never accept caller-supplied attempts, artifact IDs, artifact names, or artifact digests.
- All producer artifact downloads use `artifact-ids`; no producer input artifact may be downloaded by name.
- Raw dispatch run IDs and content digests remain confined to `verify-release-input-run`; package jobs consume only that job's closed outputs.
- Package jobs fetch and validate fresh producer-run JSON before and after artifact download.
- Preserve all runtime inventory, Linux mode, launcher digest, appimagetool, staging, package-manifest, install-smoke, and qualification gates.
- Re-run drift fails closed and requires a new foundation dispatch; do not fall back to a previous attempt.
- Stable failure codes remain `RELEASE_PRODUCER_UNTRUSTED` for run/API state and `RELEASE_PRODUCER_PROVENANCE_INVALID` for provenance/artifact binding.
- Use TDD for every behavior change and commit each task only after focused and adjacent tests pass.
- Task 9 of the parent plan remains blocked until this plan, the Task 8 full branch review, and clean completion verification all pass.

---

### Task 1: Bind closed provenance to the producer attempt and immutable input artifacts

**Files:**
- Modify: `tools/release/producer/provenance.mjs`
- Modify: `tools/release/producer/provenance.test.mjs`

**Interfaces:**
- `createReleaseInputProvenance(input)` adds `producer.runId`, `producer.runAttempt`, and `artifactId`, `artifactDigest`, `transportName` to the Windows, Linux, and appimagetool records.
- `validateReleaseInputProvenance(value)` accepts only the extended closed schema and returns a deeply frozen normalized copy.
- CLI `create` adds `--producer-run-id`, `--producer-run-attempt`, `--windows-artifact-id`, `--windows-artifact-digest`, `--windows-artifact-transport-name`, `--linux-artifact-id`, `--linux-artifact-digest`, `--linux-artifact-transport-name`, `--appimagetool-artifact-id`, `--appimagetool-artifact-digest`, and `--appimagetool-artifact-transport-name`.
- Logical `artifactName` values remain unsuffixed; `transportName` must be the logical name plus the validated producer attempt.

- [ ] **Step 1: Extend the valid provenance fixture and exact expected shape**

Update the fixture passed to `createReleaseInputProvenance()` with exact identities:

```js
producer: {
  repository: "colayc/unitTest",
  workflowPath: ".github/workflows/release-inputs.yml",
  sourceCommit: "a".repeat(40),
  event: "workflow_dispatch",
  ref: "refs/heads/master",
  runId: "123456789",
  runAttempt: 2,
},
windows: {
  ...windowsSummary,
  artifactId: "1001",
  artifactDigest: "1".repeat(64),
  transportName: "code-oss-windows-x64-2",
},
linux: {
  ...linuxSummary,
  artifactId: "1002",
  artifactDigest: "2".repeat(64),
  transportName: "code-oss-linux-x64-2",
},
appimagetool: {
  repository: manifest.appimagetool.repository,
  assetId: manifest.appimagetool.assetId,
  assetName: manifest.appimagetool.assetName,
  size: manifest.appimagetool.size,
  sha256: manifest.appimagetool.sha256,
  artifactId: "1003",
  artifactDigest: "3".repeat(64),
  transportName: "appimagetool-linux-x64-2",
},
```

Require the normalized producer object to have exactly:

```js
["event", "ref", "repository", "runAttempt", "runId", "sourceCommit", "workflowPath"]
```

Require each transported artifact record to contain exactly its existing keys plus `artifactId`, `artifactDigest`, and `transportName`.

- [ ] **Step 2: Add one-field mutation and CLI rejection coverage**

Add table-driven mutations for:

```js
[
  ["producer.runId", "0"],
  ["producer.runId", "01"],
  ["producer.runAttempt", 0],
  ["producer.runAttempt", Number.MAX_SAFE_INTEGER + 1],
  ["runtimes.windows.artifactId", "01"],
  ["runtimes.windows.artifactDigest", "A".repeat(64)],
  ["runtimes.windows.transportName", "code-oss-windows-x64-1"],
  ["runtimes.linux.transportName", "code-oss-linux-x64-3"],
  ["appimagetool.transportName", "appimagetool-linux-x64"],
]
```

Repeat ID/digest mutations for Linux and appimagetool. Add an extra key at the producer and each artifact-bearing object level. Extend CLI tests to require every new flag exactly once, reject newline/percent-bearing values, and prove failure emits only `RELEASE_PRODUCER_PROVENANCE_INVALID` without an absolute path.

- [ ] **Step 3: Run the provenance test and preserve RED**

```powershell
node --test tools/release/producer/provenance.test.mjs
```

Expected RED: current creation ignores or rejects the new keys, and the CLI does not recognize the new arguments.

- [ ] **Step 4: Implement canonical run and artifact identity validation**

Add validators with these exact contracts:

```js
function canonicalDecimalString(value) {
  return typeof value === "string" && /^[1-9][0-9]*$/u.test(value) ? value : undefined;
}

function canonicalAttempt(value) {
  return Number.isSafeInteger(value) && value > 0 ? value : undefined;
}

function validateArtifactIdentity(value, logicalName, runAttempt) {
  // Snapshot exact keys at the caller's object level.
  // Require artifactId, artifactDigest, and
  // `${logicalName}-${runAttempt}` as transportName.
}
```

Keep `schemaVersion: 1` because the parent branch has not shipped. Extend the current exact-key arrays, creation normalization, CLI argument allowlist, and canonical serializer. Parse `--producer-run-attempt` as a canonical positive safe integer before creation; do not coerce whitespace, signs, exponent notation, or leading zeroes.

- [ ] **Step 5: Run focused and adjacent provenance tests**

```powershell
node --test tools/release/producer/source-manifest.test.mjs tools/release/producer/runtime-inventory.test.mjs tools/release/producer/provenance.test.mjs
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 6: Commit Task 1**

```powershell
git add tools/release/producer/provenance.mjs tools/release/producer/provenance.test.mjs
git commit -m "fix: bind provenance to immutable artifacts"
```

---

### Task 2: Validate GitHub attempt and artifact API metadata as a closed trusted projection

**Files:**
- Modify: `tools/release/producer/trusted-run.mjs`
- Modify: `tools/release/producer/trusted-run.test.mjs`

**Interfaces:**
- `validateProducerRunMetadata({ run, expectedRunId, expectedConsumerCommit, expectedRunAttempt? })` returns `{runId, runAttempt}`.
- `selectProvenanceArtifact({ artifacts, runId, runAttempt })` returns `{provenanceArtifactId, provenanceArtifactDigest, provenanceTransportName}`.
- `validateTrustedReleaseInputs(request)` validates the second run/artifact snapshot and returns run identity, the three content digests, and immutable ID/digest pairs for Windows, Linux, and appimagetool.
- CLI `validate-run` consumes `--run-json` and `--artifacts-json`, and emits the four bootstrap outputs.
- CLI `validate-provenance` consumes the second run/artifact snapshot plus the bootstrap tuple and emits the eleven final trusted outputs.
- CLI `validate-attempt` consumes a fresh run JSON plus expected run ID, attempt, and consumer commit; it emits no output.

- [ ] **Step 1: Add realistic run-attempt and artifact-list fixtures**

Extend the run fixture with:

```js
run_attempt: 2,
```

Create this artifact-list fixture using numeric API IDs and API-prefixed digests:

```js
{
  total_count: 4,
  artifacts: [
    { id: 1001, name: "code-oss-windows-x64-2", expired: false, digest: `sha256:${"1".repeat(64)}`, workflow_run: { id: 123456789 } },
    { id: 1002, name: "code-oss-linux-x64-2", expired: false, digest: `sha256:${"2".repeat(64)}`, workflow_run: { id: 123456789 } },
    { id: 1003, name: "appimagetool-linux-x64-2", expired: false, digest: `sha256:${"3".repeat(64)}`, workflow_run: { id: 123456789 } },
    { id: 1004, name: "release-input-provenance-2", expired: false, digest: `sha256:${"4".repeat(64)}`, workflow_run: { id: 123456789 } },
  ],
}
```

Preserve acceptance of unrelated extra keys inside external GitHub API objects while requiring every projected field to be an enumerable data property.

- [ ] **Step 2: Add pure RED tests for run attempt and artifact-list closure**

Require rejection for:

- missing, zero, negative, fractional, unsafe, string, or changed `run_attempt` in API run JSON;
- `total_count !== artifacts.length`, `total_count > 100`, a non-array `artifacts`, or duplicate provenance transport names;
- missing/expired artifacts, zero/unsafe IDs, leading-zero string IDs, uppercase/malformed API digests, wrong `workflow_run.id`, wrong attempt suffix, or a non-data-property projection;
- a provenance artifact ID/digest that changes between the pre-download bootstrap tuple and the second API snapshot;
- any runtime/tool artifact ID, digest, logical name, transport name, run ID, or attempt that disagrees between API metadata and provenance.

Require this exact successful bootstrap result:

```js
{
  runId: "123456789",
  runAttempt: 2,
  provenanceArtifactId: "1004",
  provenanceArtifactDigest: "4".repeat(64),
}
```

Require the final trusted result to expose only:

```js
{
  runId: "123456789",
  runAttempt: 2,
  windowsLauncherSha256: digests.windows,
  linuxLauncherSha256: digests.linux,
  appimagetoolSha256: digests.appimagetool,
  windowsArtifactId: "1001",
  windowsArtifactDigest: "1".repeat(64),
  linuxArtifactId: "1002",
  linuxArtifactDigest: "2".repeat(64),
  appimagetoolArtifactId: "1003",
  appimagetoolArtifactDigest: "3".repeat(64),
}
```

- [ ] **Step 3: Add CLI RED tests and output-injection coverage**

Update CLI fixtures to write both API JSON files. Require:

```text
validate-run:
  run_id
  run_attempt
  provenance_artifact_id
  provenance_artifact_digest

validate-provenance:
  run_id
  run_attempt
  windows_launcher_sha256
  linux_launcher_sha256
  appimagetool_sha256
  windows_artifact_id
  windows_artifact_digest
  linux_artifact_id
  linux_artifact_digest
  appimagetool_artifact_id
  appimagetool_artifact_digest
```

Require `validate-attempt` to exit zero for the exact current attempt and nonzero after replacing `run_attempt: 2` with `3`, `status: completed` with `queued`, or `conclusion: success` with `failure`. Pass newline, percent, sign, exponent, and leading-zero variants through every new CLI scalar and prove `GITHUB_OUTPUT` remains byte-for-byte unchanged on failure.

- [ ] **Step 4: Run the trusted-run test and preserve RED**

```powershell
node --test tools/release/producer/trusted-run.test.mjs
```

Expected RED: run attempts, artifact lists, bootstrap identities, new outputs, and `validate-attempt` are absent.

- [ ] **Step 5: Implement projected API validation and three CLI commands**

Use external-schema projection rather than closing whole API objects. Implement exact internal snapshots and these invariants:

```js
api.total_count === api.artifacts.length
api.total_count <= 100
artifact.workflow_run.id === run.id
artifact.digest === `sha256:${provenanceDigest}`
artifact.name === `${logicalName}-${runAttempt}`
```

Canonicalize numeric API IDs only when they are positive safe integers; canonicalize repository/provenance IDs only when they are digit strings without leading zeroes. Require exactly one provenance bootstrap match for the selected attempt. Extend the atomic `GITHUB_OUTPUT` allowlists to the exact output orders in Step 3. Keep the existing linked-file, TOCTOU, rollback, output-size, and path-leak protections unchanged.

- [ ] **Step 6: Run focused and adjacent trust tests**

```powershell
node --test tools/release/producer/provenance.test.mjs tools/release/producer/trusted-run.test.mjs
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 7: Commit Task 2**

```powershell
git add tools/release/producer/trusted-run.mjs tools/release/producer/trusted-run.test.mjs
git commit -m "fix: validate release artifact identities"
```

---

### Task 3: Capture immutable identities and use ID-only transport in the producer workflow

**Files:**
- Modify: `.github/workflows/release-inputs.yml`
- Modify: `tools/release/producer/workflow-contract.test.mjs`

**Interfaces:**
- `build-windows` outputs `windows_artifact_id` and `windows_artifact_digest` from its upload step.
- `build-linux` outputs `linux_artifact_id`, `linux_artifact_digest`, `appimagetool_artifact_id`, and `appimagetool_artifact_digest`.
- `attest` passes validated run/attempt and artifact identities to `provenance.mjs create`.
- All three attestation input downloads use `artifact-ids` and `merge-multiple: true` into the existing fixed transport roots.

- [ ] **Step 1: Add producer workflow RED assertions for upload identity**

Require upload steps with exact IDs and transport names:

```yaml
id: upload-windows-runtime
name: code-oss-windows-x64-${{ github.run_attempt }}

id: upload-linux-runtime
name: code-oss-linux-x64-${{ github.run_attempt }}

id: upload-appimagetool
name: appimagetool-linux-x64-${{ github.run_attempt }}

id: upload-provenance
name: release-input-provenance-${{ github.run_attempt }}
```

Require each build job output to be a direct expression from its upload step's `artifact-id` or `artifact-digest`. Preserve `retention-days: 1`, `if-no-files-found: error`, and the existing hidden-file settings.

- [ ] **Step 2: Add producer workflow RED assertions for ID-only attestation downloads**

For the Windows, Linux, and appimagetool attestation download steps, require:

```yaml
artifact-ids: ${{ needs.build-windows.outputs.windows_artifact_id }}
merge-multiple: true
path: .release/transport/windows

artifact-ids: ${{ needs.build-linux.outputs.linux_artifact_id }}
merge-multiple: true
path: .release/transport/linux

artifact-ids: ${{ needs.build-linux.outputs.appimagetool_artifact_id }}
merge-multiple: true
path: .release/transport/appimagetool
```

Reject `name:` on those steps. Require a pre-download shell validation step that checks canonical `GITHUB_RUN_ID`, canonical `GITHUB_RUN_ATTEMPT`, all three artifact IDs, and all three upload digests before any download action.

- [ ] **Step 3: Add provenance-command and summary RED assertions**

Require `provenance.mjs create` to receive these direct trusted values:

```text
--producer-run-id "$GITHUB_RUN_ID"
--producer-run-attempt "$GITHUB_RUN_ATTEMPT"
--windows-artifact-id "$WINDOWS_ARTIFACT_ID"
--windows-artifact-digest "$WINDOWS_ARTIFACT_DIGEST"
--windows-artifact-transport-name "code-oss-windows-x64-$GITHUB_RUN_ATTEMPT"
--linux-artifact-id "$LINUX_ARTIFACT_ID"
--linux-artifact-digest "$LINUX_ARTIFACT_DIGEST"
--linux-artifact-transport-name "code-oss-linux-x64-$GITHUB_RUN_ATTEMPT"
--appimagetool-artifact-id "$APPIMAGETOOL_ARTIFACT_ID"
--appimagetool-artifact-digest "$APPIMAGETOOL_ARTIFACT_DIGEST"
--appimagetool-artifact-transport-name "appimagetool-linux-x64-$GITHUB_RUN_ATTEMPT"
```

Require the validated summary to print run ID, run attempt, and the three artifact IDs only after provenance validation. Continue rejecting tokens and absolute local paths in the summary.

- [ ] **Step 4: Run the workflow contract test and preserve RED**

```powershell
node --test tools/release/producer/workflow-contract.test.mjs
```

Expected RED: uploads have no IDs/identity outputs, attestation downloads by name, and provenance lacks the new arguments.

- [ ] **Step 5: Implement producer upload outputs and strict attestation transport**

Add upload step IDs, attempt-qualified names, and job outputs. In the attestation preflight, require:

```bash
[[ "$GITHUB_RUN_ID" =~ ^[1-9][0-9]*$ ]]
[[ "$GITHUB_RUN_ATTEMPT" =~ ^[1-9][0-9]*$ ]]
[[ "$WINDOWS_ARTIFACT_ID" =~ ^[1-9][0-9]*$ ]]
[[ "$WINDOWS_ARTIFACT_DIGEST" =~ ^[0-9a-f]{64}$ ]]
```

Apply the same ID/digest rules to Linux and appimagetool. Replace all three name-based downloads with the pinned action's `artifact-ids` input and `merge-multiple: true`. Pass the exact values from Step 3 to provenance creation. Do not change build targets, staging layouts, post-transport validators, or action commits.

- [ ] **Step 6: Run producer and workspace-adjacent tests**

```powershell
pnpm test:release-producer
node --test tools/workspace-smoke/workspace-smoke.test.mjs
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 7: Commit Task 3**

```powershell
git add .github/workflows/release-inputs.yml tools/release/producer/workflow-contract.test.mjs
git commit -m "fix: transport producer artifacts by identity"
```

---

### Task 4: Bootstrap provenance safely and isolate immutable IDs in foundation packaging

**Files:**
- Modify: `.github/workflows/foundation.yml`
- Modify: `tools/release/producer/workflow-contract.test.mjs`
- Modify: `tools/release/qualification.test.mjs`
- Modify: `tools/release/update.test.mjs`
- Modify: `tools/release/windows/package-msix.test.mjs`

**Interfaces:**
- `verify-release-input-run` exposes the eleven final trusted outputs defined in Task 2.
- `package-windows` consumes only Windows artifact ID/digest, run ID/attempt, and Windows launcher digest from the trust job.
- `package-linux` consumes only Linux/appimagetool artifact IDs/digests, run ID/attempt, and Linux/tool content digests from the trust job.
- Both package jobs call `trusted-run.mjs validate-attempt` against fresh API JSON immediately before and after downloads.

- [ ] **Step 1: Add trust-job RED assertions for two API snapshots and ID bootstrap**

Require the trust job to fetch before-download files using:

```bash
gh api "repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID" > .release/producer-run-before.json
gh api "repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID/artifacts?per_page=100" > .release/producer-artifacts-before.json
```

Require `GH_TOKEN: ${{ github.token }}` on every step that invokes `gh api`; do not copy the token into a command argument, file, output, or summary.

Require `validate-run` to consume both files and emit the bootstrap tuple. Require the provenance download to contain `artifact-ids: ${{ steps.precheck.outputs.provenance_artifact_id }}`, `merge-multiple: true`, the validated run ID, and `github-token`, with no `name:` input.

After the download, require fresh `producer-run-after.json` and `producer-artifacts-after.json` API files. Require `validate-provenance` to receive `run_id`, `run_attempt`, `provenance_artifact_id`, and `provenance_artifact_digest` from `steps.precheck.outputs`, never from dispatch inputs.

- [ ] **Step 2: Add closed-output and raw-input-isolation RED assertions**

Require exactly these trust-job outputs:

```text
run_id
run_attempt
windows_launcher_sha256
linux_launcher_sha256
appimagetool_sha256
windows_artifact_id
windows_artifact_digest
linux_artifact_id
linux_artifact_digest
appimagetool_artifact_id
appimagetool_artifact_digest
```

Slice each package job independently and reject every occurrence of:

```text
inputs.release_input_run_id
inputs.windows_code_oss_sha256
inputs.linux_code_oss_sha256
inputs.linux_appimagetool_sha256
vars.RELEASE_INPUT_RUN_ID
vars.RELEASE_CODE_OSS_WINDOWS_SHA256
vars.RELEASE_CODE_OSS_LINUX_SHA256
vars.RELEASE_APPIMAGETOOL_SHA256
```

- [ ] **Step 3: Add package transport RED assertions**

Require package jobs to download by exact trusted IDs:

```yaml
artifact-ids: ${{ needs.verify-release-input-run.outputs.windows_artifact_id }}
```

and on Linux:

```yaml
artifact-ids: ${{ needs.verify-release-input-run.outputs.linux_artifact_id }}
artifact-ids: ${{ needs.verify-release-input-run.outputs.appimagetool_artifact_id }}
```

Require `merge-multiple: true`, the validated run ID, and `github-token` on each. Reject `name:`. Require `validate-attempt` before the first download and after the final platform download, with a fresh `gh api` fetch before each call. Assert the second validation precedes mode restoration, staging, or execution.

- [ ] **Step 4: Run focused workflow consumers and preserve RED**

```powershell
node --test tools/release/producer/workflow-contract.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs
```

Expected RED: the trust job has only run-ID outputs, downloads provenance by name, and package jobs have no attempt revalidation or artifact-ID inputs.

- [ ] **Step 5: Implement the two-snapshot trust job**

Keep the existing raw coordinate syntax gate before interpolating the run ID into an API path. Fetch both pre-download API objects, call the new `validate-run`, download provenance by ID, then fetch both API objects again and call `validate-provenance`. Map only the eleven final validation outputs to the job outputs.

When the v4.3 download action receives a single `artifact-ids` value, set `merge-multiple: true` so the artifact contents remain at the existing fixed destination root. Preserve exact-root checks before reading provenance.

- [ ] **Step 6: Implement package pre/post-attempt gates and ID downloads**

In `package-windows`, use these exact files and arguments around the Windows ID download:

```powershell
gh api "repos/colayc/unitTest/actions/runs/$env:RELEASE_INPUT_RUN_ID" > .release/producer-run-windows-before.json
node tools/release/producer/trusted-run.mjs validate-attempt `
  --run-json .release/producer-run-windows-before.json `
  --run-id $env:RELEASE_INPUT_RUN_ID `
  --run-attempt $env:RELEASE_INPUT_RUN_ATTEMPT `
  --consumer-commit $env:GITHUB_SHA

# The pinned download action retrieves WINDOWS_ARTIFACT_ID here.

gh api "repos/colayc/unitTest/actions/runs/$env:RELEASE_INPUT_RUN_ID" > .release/producer-run-windows-after.json
node tools/release/producer/trusted-run.mjs validate-attempt `
  --run-json .release/producer-run-windows-after.json `
  --run-id $env:RELEASE_INPUT_RUN_ID `
  --run-attempt $env:RELEASE_INPUT_RUN_ATTEMPT `
  --consumer-commit $env:GITHUB_SHA
```

In `package-linux`, use these exact files and arguments around the Linux runtime and appimagetool ID downloads:

```bash
gh api "repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID" > .release/producer-run-linux-before.json
node tools/release/producer/trusted-run.mjs validate-attempt \
  --run-json .release/producer-run-linux-before.json \
  --run-id "$RELEASE_INPUT_RUN_ID" \
  --run-attempt "$RELEASE_INPUT_RUN_ATTEMPT" \
  --consumer-commit "$GITHUB_SHA"

# The two pinned download steps retrieve LINUX_ARTIFACT_ID and APPIMAGETOOL_ARTIFACT_ID here.

gh api "repos/colayc/unitTest/actions/runs/$RELEASE_INPUT_RUN_ID" > .release/producer-run-linux-after.json
node tools/release/producer/trusted-run.mjs validate-attempt \
  --run-json .release/producer-run-linux-after.json \
  --run-id "$RELEASE_INPUT_RUN_ID" \
  --run-attempt "$RELEASE_INPUT_RUN_ATTEMPT" \
  --consumer-commit "$GITHUB_SHA"
```

The pinned actions sit between the two validation blocks. The second validation precedes Windows staging and Linux mode restoration, staging, or execution.
Both platform API-validation steps set `GH_TOKEN: ${{ github.token }}` only in the step environment.

Keep artifact digest values as trusted environment coordinates for logging/contract checks; do not replace existing content/inventory checks with upload digests.

- [ ] **Step 7: Run focused and adjacent release tests**

```powershell
pnpm test:release-producer
node --test tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs tools/workspace-smoke/workspace-smoke.test.mjs
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 8: Commit Task 4**

```powershell
git add .github/workflows/foundation.yml tools/release/producer/workflow-contract.test.mjs tools/release/qualification.test.mjs tools/release/update.test.mjs tools/release/windows/package-msix.test.mjs
git commit -m "fix: gate packaging on immutable artifact IDs"
```

---

### Task 5: Update operational documentation and the parent qualification commands

**Files:**
- Modify: `README.md`
- Modify: `docs/security.md`
- Modify: `docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md`
- Modify: `docs/superpowers/plans/2026-08-28-trusted-code-oss-release-input-production.md`
- Modify: `tools/workspace-smoke/workspace-smoke.test.mjs`

**Interfaces:**
- Documentation names attempt-qualified transport artifacts and immutable-ID validation without embedding an expiring real run ID or artifact ID.
- Parent Task 9 evidence commands derive the run attempt from API metadata and select/download the exact provenance artifact by immutable ID.

- [ ] **Step 1: Add documentation RED assertions**

Require README, security guidance, and roadmap text to state together:

```text
run ID alone is not a complete artifact identity
run attempt is bound end to end
runtime/tool downloads use immutable artifact IDs and upload digests
transport names append the run attempt
a producer re-run requires a new foundation dispatch
package jobs revalidate the attempt before and after download
```

Reject text that still instructs users to download `release-input-provenance` by its unsuffixed name.

- [ ] **Step 2: Run the documentation test and preserve RED**

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs
```

Expected RED: current documentation describes run/name validation but not attempt/immutable-ID binding.

- [ ] **Step 3: Update documentation and exact Task 9 evidence commands**

Replace the parent plan's provenance evidence selection with this data flow:

```powershell
$producerRun = gh api "repos/colayc/unitTest/actions/runs/$producerRunId" | ConvertFrom-Json
$producerRunAttempt = [int64]$producerRun.run_attempt
$provenanceTransportName = "release-input-provenance-$producerRunAttempt"
$artifactPage = gh api "repos/colayc/unitTest/actions/runs/$producerRunId/artifacts?per_page=100" | ConvertFrom-Json
$provenanceMatches = @($artifactPage.artifacts | Where-Object { $_.name -ceq $provenanceTransportName -and $_.expired -eq $false })
if ($artifactPage.total_count -ne $artifactPage.artifacts.Count -or $artifactPage.total_count -gt 100 -or $provenanceMatches.Count -ne 1) { throw 'producer provenance artifact identity is ambiguous' }
$provenanceArtifactId = [string]$provenanceMatches[0].id
$provenanceZip = Join-Path $producerEvidence 'release-input-provenance.zip'
$downloadHeaders = @{
  Accept = 'application/vnd.github+json'
  Authorization = "Bearer $(gh auth token)"
  'X-GitHub-Api-Version' = '2022-11-28'
  'User-Agent' = 'colayc-unitTest-release-evidence'
}
Invoke-WebRequest -Uri "https://api.github.com/repos/colayc/unitTest/actions/artifacts/$provenanceArtifactId/zip" -Headers $downloadHeaders -OutFile $provenanceZip
Expand-Archive -LiteralPath $provenanceZip -DestinationPath $producerEvidence -Force
Remove-Item -LiteralPath $provenanceZip -Force
```

Use the authenticated REST download endpoint for the exact artifact ID because `gh run download` exposes only name/pattern selectors. Do not fall back to name selection. Keep real IDs and digests out of committed documentation.

- [ ] **Step 4: Run documentation and producer contract tests**

```powershell
node --test tools/workspace-smoke/workspace-smoke.test.mjs
pnpm test:release-producer
git diff --check
```

Expected: all tests pass and no whitespace errors.

- [ ] **Step 5: Commit Task 5**

```powershell
git add README.md docs/security.md docs/superpowers/plans/2026-07-21-cpp-unit-test-ide-roadmap.md docs/superpowers/plans/2026-08-28-trusted-code-oss-release-input-production.md tools/workspace-smoke/workspace-smoke.test.mjs
git commit -m "docs: describe attempt-bound release inputs"
```

---

### Task 6: Repeat Task 8 completion verification and full branch security review

**Files:**
- Modify only files for defects proved by the commands or review below.

**Interfaces:**
- Completion evidence records current HEAD, exact test pass/skip/fail counts, clean-tree state, and review disposition.
- Task 9 is unblocked only with zero Critical and zero Important findings.

- [ ] **Step 1: Run every release test from a clean tree**

```powershell
$releaseTests = rg --files tools/release -g '*.test.mjs'
node --test $releaseTests
```

Expected: zero failures. Record exact totals and explicit platform skips.

- [ ] **Step 2: Run the complete repository gate**

```powershell
pnpm test
pnpm verify
git diff --check
git status --short
```

Expected: zero failures, no generated drift, and no uncommitted files.

- [ ] **Step 3: Request a fresh full-branch review**

Use `superpowers:requesting-code-review` on `ec1b64f..HEAD`. Require explicit review of:

1. hostile manifest, provenance, run-attempt, artifact-list, path, and schema mutations;
2. workflow authorization, exact action pins, and output provenance;
3. hidden-file and artifact-root completeness;
4. executable/mode inventory binding and Linux restore round trip;
5. pre-download provenance bootstrap and package-job raw-input isolation;
6. immutable artifact ID/digest binding across both API snapshots;
7. producer and package downloads contain `artifact-ids` and no name-based input selection;
8. a midstream producer re-run fails before staging or execution;
9. no secret, local runtime, public-release, signing, or license-status regression.

The previously reported Important finding is resolved only if the reviewer can trace one exact run attempt and immutable artifact tuple from upload outputs through provenance, trust outputs, downloads, and package validation.

- [ ] **Step 4: Fix only proved review findings with TDD**

For each accepted finding:

```text
add one focused regression test
run it and record the expected RED
make the smallest production change
run focused plus adjacent tests
commit one independently reviewable fix
```

Do not weaken attempt, artifact-list, ID, digest, inventory, or raw-input isolation checks to satisfy hosted behavior.

- [ ] **Step 5: Repeat fresh completion verification**

```powershell
$releaseTests = rg --files tools/release -g '*.test.mjs'
node --test $releaseTests
pnpm test
pnpm verify
git diff --check
git status --short
git rev-parse HEAD
```

Expected: zero failures, clean worktree, zero Critical findings, and zero Important findings. Record exact pass/skip counts and HEAD from this final run rather than earlier output.

- [ ] **Step 6: Continue to parent Task 9 only after the gate passes**

Invoke `superpowers:finishing-a-development-branch`, then follow the updated parent Task 9 to push GitHub/Gitee, create and merge the PR, dispatch a fresh producer attempt, and run the unsigned `0.1.0` qualification. Any hosted defect returns to focused TDD, full review, merge, and a completely fresh producer attempt.
