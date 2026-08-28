# Release Input Attempt and Artifact Identity Design

**Status:** Approved design addendum for the trusted Code-OSS release-input producer

**Parent spec:** `docs/superpowers/specs/2026-08-28-trusted-code-oss-release-input-production-design.md`

## Problem

The parent design authenticates a producer workflow by GitHub Actions run ID and downloads artifacts by name. GitHub keeps the same run ID when a workflow is re-run, while incrementing the run attempt. A re-run can therefore create a second artifact set with the same logical names under the same run ID. Name-based resolution does not bind the consumer to the exact artifact instances attested by the producer.

The release trust chain must prevent an attestation from one attempt from being combined with runtime or tool artifacts from another attempt.

## Goals

- Bind every accepted producer result to one exact `(run ID, run attempt)` pair.
- Bind every runtime and tool input to an immutable GitHub artifact ID and upload digest.
- Download all producer artifacts by immutable ID rather than by name.
- Detect a producer re-run before and after package-job downloads and fail closed.
- Preserve the existing runtime inventory, executable-mode, launcher digest, appimagetool, package-manifest, and qualification checks.
- Keep raw workflow-dispatch values isolated to `verify-release-input-run`.

## Non-goals

- Supporting GitHub Enterprise Server or artifact actions older than the already pinned v4-compatible actions.
- Publishing signed production releases.
- Accepting a caller-supplied artifact ID, attempt, artifact name, or artifact digest.
- Recovering automatically from a producer re-run. A new producer attempt requires a new foundation dispatch.

## Considered Approaches

### 1. Run attempt plus immutable artifact identities — selected

Capture upload action outputs, carry them through provenance, validate them against GitHub's API, and download by ID. This closes both name ambiguity and cross-attempt mixing while retaining re-run support.

### 2. Attempt-suffixed names without artifact IDs

Adding the attempt to artifact names removes same-name ambiguity, but a name remains a locator rather than a complete immutable identity. It does not independently bind the GitHub artifact object or its upload digest.

### 3. Reject every producer re-run

Allowing only attempt 1 is simpler, but it makes transient hosted-run failures operationally expensive and still leaves the trust model dependent on names. It also conflicts with the documented requirement to use a fresh artifact set after a re-run.

## Trusted Identity Model

The trusted producer identity is:

```text
repository
workflow path
event
branch
source commit
run ID
run attempt
```

Each transported runtime or tool adds:

```text
logical artifact name
attempt-qualified transport name
immutable artifact ID
upload artifact SHA-256 digest
```

Artifact IDs and run IDs are canonical non-zero decimal strings. Run attempts are positive safe integers. Artifact digests are canonical lowercase 64-character hexadecimal values in repository data; GitHub API values are accepted only as the exact `sha256:<digest>` representation.

The logical artifact names remain fixed:

```text
code-oss-windows-x64
code-oss-linux-x64
appimagetool-linux-x64
release-input-provenance
```

Transport names append the decimal run attempt, for example `code-oss-windows-x64-2`. This gives the consumer a safe bootstrap locator for the provenance artifact before it has read provenance. The immutable artifact ID, not the transport name, is used for every download.

## Producer Data Flow

1. Each build job validates `GITHUB_RUN_ID` and `GITHUB_RUN_ATTEMPT` as canonical values.
2. Each upload step has an explicit step ID and uses the attempt-qualified transport name.
3. The build job exposes the upload action's `artifact-id` and `artifact-digest` as job outputs together with the existing runtime coordinates.
4. The attestation job validates the run identity and every received artifact identity before transport.
5. The attestation job downloads the Windows runtime, Linux runtime, and appimagetool only through `artifact-ids` from the build-job outputs.
6. Existing post-transport inventory, executable-mode, launcher, tool, and version checks run unchanged.
7. Provenance records the producer run ID and attempt plus the logical name, transport name, immutable ID, and upload digest for all three input artifacts.
8. The provenance artifact is uploaded under `release-input-provenance-<attempt>`. Its upload ID and digest are not self-referential provenance fields; the consumer validates this bootstrap tuple directly from GitHub's API.

No producer job accepts artifact identity from workflow inputs, environment configuration, repository variables, or local files.

## Consumer Bootstrap and Validation

`verify-release-input-run` performs these operations in order:

1. Read the raw manual run ID and three existing manual content digests inside the trust job only.
2. Fetch fresh workflow-run JSON and the workflow-run artifact list through GitHub's API with `per_page=100`. Require `total_count` to equal the returned array length and reject a count above 100, so a partial or unbounded listing cannot hide conflicting artifacts.
3. Validate repository, workflow, event, branch, commit, completion, success, run ID, and positive run attempt.
4. Derive the exact provenance transport name from the validated attempt.
5. Require exactly one non-expired provenance artifact with that name, the expected run ID, a canonical immutable ID, and a canonical upload digest. Duplicate or malformed matches fail closed.
6. Export only the validated bootstrap tuple, then download provenance by immutable artifact ID.
7. Fetch fresh run and artifact metadata again after the download.
8. Validate provenance as a closed schema and require its run ID and attempt to match both API snapshots.
9. Resolve each provenance-declared runtime/tool artifact by immutable ID in the API list and require exact logical name, attempt-qualified transport name, upload digest, non-expired state, and producer run ID.
10. Bind the existing manual launcher/tool digests to the corresponding provenance content fields.

The trust job exposes only closed, validated outputs: run ID, run attempt, the three launcher/tool digests, and the immutable ID/digest pair needed by each package job. It does not expose raw dispatch values or name-selected artifact identities.

## Package Job Transport

Each package job uses only `verify-release-input-run` outputs.

Before downloading, it fetches fresh producer run JSON and verifies that the run is still the same completed successful attempt. It then downloads its required artifact IDs with the pinned download action. Linux downloads both the runtime and appimagetool IDs. After download, it fetches the run JSON again and repeats the attempt check before staging or executing any downloaded file.

The existing Windows launcher validation and Linux runtime/mode/tool validation remain mandatory. Immutable artifact IDs prevent cross-attempt substitution; the surrounding attempt checks additionally make a concurrent re-run fail closed rather than silently completing against an older attempt.

## Failure Semantics

The workflow rejects:

- missing, zero, unsafe, malformed, or changed run attempts;
- a run that becomes queued, in progress, unsuccessful, or a different attempt;
- duplicate provenance bootstrap names for the selected attempt, or an incomplete/oversized artifact listing;
- missing, expired, malformed, or wrong-run artifacts;
- artifact ID, transport-name, or upload-digest disagreement between API data and provenance;
- name-based producer downloads;
- package jobs that reference raw run IDs, artifact identities, or manual SHA inputs;
- a re-run that begins before package transport validation completes.

Run-identity failures use `RELEASE_PRODUCER_UNTRUSTED`. Provenance or artifact-binding disagreements use `RELEASE_PRODUCER_PROVENANCE_INVALID`. Errors must not print tokens, absolute paths, untrusted JSON, or attacker-controlled artifact names.

## Testing

Implementation uses TDD and adds focused regression coverage before production changes:

- provenance tests mutate run ID, attempt, logical name, transport name, artifact ID, and artifact digest independently and add extra keys at every new object level;
- trusted-run tests cover numeric/string boundaries, duplicate artifacts, expired artifacts, wrong run IDs, malformed API digests, attempt drift between snapshots, and every API/provenance/manual binding;
- CLI tests prove hostile values cannot inject `GITHUB_OUTPUT` entries or leak local paths;
- workflow-contract tests reject name-based downloads, missing upload step IDs/outputs, missing attempt-qualified names, raw-input leakage into package jobs, and missing pre/post-download attempt checks;
- existing release producer, qualification, update, Windows MSIX, workspace smoke, root test, and complete verification gates remain required;
- the full branch security review is repeated after the fix. Task 9 remains blocked until that review reports no Critical or Important findings and the tree is clean.

## Documentation and Qualification Procedure

README, security guidance, roadmap status, and Task 9 commands must describe attempt-qualified transport names and immutable-ID validation. Evidence collection selects the exact provenance artifact for the validated attempt rather than downloading the unsuffixed logical name.

If the producer is re-run, any previous foundation dispatch is discarded even if it had begun successfully. Qualification uses one completed producer attempt from bootstrap through both package jobs, and records its run ID, run attempt, immutable artifact IDs, and common source commit.
