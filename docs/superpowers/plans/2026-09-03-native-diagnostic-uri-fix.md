# Native Diagnostic URI Privacy and Workspace Mapping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve privacy for Windows diagnostic paths while publishing enough workspace-relative information for native compiler diagnostics to identify their source file.

**Architecture:** Keep canonical absolute `file:` URIs inside the diagnostic parser and expose a parser-owned `PublicURIProvider` for publication. The task manager uses that capability when present, while legacy test parsers retain the conservative fallback. The service probe accepts strictly validated `workspace:///relative` URIs and maps them to its existing `<workspace>/...` golden form.

**Tech Stack:** Go 1.24+, Go `testing`, TypeScript, Node test runner, URL/path normalization, existing service-probe native E2E.

**Spec:** `docs/superpowers/specs/2026-09-03-native-diagnostic-uri-design.md`

## Global Constraints

- Keep parser-internal `FileURI` values as canonical absolute `file:` URIs.
- In-workspace public values use `workspace:///` plus a URL-escaped relative path.
- External Windows file paths collapse to exactly `workspace:///`; do not leak drive letters or UNC hosts.
- Non-file URIs and existing non-Windows external-file behavior remain unchanged.
- Apply the mapping to both primary and related diagnostics.
- Reject workspace URI hosts, queries, fragments, absolute paths, and traversal segments in the service probe.
- Do not modify CLI handshake, packaging, producer, signing, release workflow, or protocol schemas.

---

### Task 1: Add parser-owned public URI mapping

**Files:**
- Modify: `apps/test-service/internal/diagnostic/model.go`
- Modify: `apps/test-service/internal/diagnostic/parser.go`
- Test: `apps/test-service/internal/diagnostic/parser_test.go`
- Test: `apps/test-service/internal/diagnostic/parser_windows_test.go`

**Interfaces:**
- Consumes: `workspace.Root` and existing canonical `Diagnostic.FileURI` values.
- Produces: `diagnostic.PublicURIProvider` and `(*parser).PublicURI(value string) string`.

- [ ] **Step 1: Define the capability without changing parser output**

Add this internal interface next to `Parser` in `model.go`:

```go
type PublicURIProvider interface {
    PublicURI(value string) string
}
```

Do not add the method to `Parser`; parser fakes that only implement `Feed` and
`Close` must remain valid.

- [ ] **Step 2: Write failing mapper tests**

Add tests that construct a parser with a real `workspace.Root` and assert:

```go
if got := parser.PublicURI(root.URI + "/src/main.cpp"); got != "workspace:///src/main.cpp" { ... }
if got := parser.PublicURI(root.URI); got != "workspace:///" { ... }
if got := parser.PublicURI(root.URI + "-sibling/src/main.cpp"); got != root.URI + "-sibling/src/main.cpp" { ... }
if got := parser.PublicURI("https://example.invalid/src/main.cpp"); got != "https://example.invalid/src/main.cpp" { ... }
```

In the Windows-only test file, cover a drive-root child, a drive-root
external URI, a UNC external URI, and case-insensitive path containment. Assert
that external values are exactly `workspace:///` and contain no drive or host.

- [ ] **Step 3: Implement strict root-aware conversion**

Implement `(*parser).PublicURI` in `parser.go` using URL parsing and the same
workspace path-boundary rules used by `Root.Contains` and the parser's existing
identity logic. Preserve URL escaping for relative components; do not derive
containment from a raw string prefix. Return the input unchanged for malformed
or non-file values. On Windows, reduce a valid external local file URI to
`workspace:///`; on non-Windows, return that external file URI unchanged.

- [ ] **Step 4: Run focused parser tests**

Run:

```text
go test ./internal/diagnostic -run 'PublicURI|WindowsWorkspace|WindowsUNC' -count=1
```

Expected: PASS on the host platform; Windows-only cases execute in the Windows
job.

- [ ] **Step 5: Commit the parser capability**

```text
git add apps/test-service/internal/diagnostic/model.go apps/test-service/internal/diagnostic/parser.go apps/test-service/internal/diagnostic/parser_test.go apps/test-service/internal/diagnostic/parser_windows_test.go
git commit -m "fix: preserve workspace-relative diagnostic URIs"
```

### Task 2: Publish mapped URIs from task manager

**Files:**
- Modify: `apps/test-service/internal/task/manager.go`
- Modify: `apps/test-service/internal/task/manager_output_redaction_test.go`
- Modify: `apps/test-service/internal/task/manager_execution_test.go`

**Interfaces:**
- Consumes: `diagnostic.PublicURIProvider` from the current step parser.
- Produces: task diagnostic events whose `sourceUri` retains an in-workspace relative path without exposing an absolute Windows path.

- [ ] **Step 1: Write manager regression tests**

Add a parser fake that implements both `Feed`/`Close` and:

```go
func (p *publicURIParser) PublicURI(value string) string {
    return "workspace:///src/main.cpp"
}
```

Publish a primary diagnostic and a related diagnostic and assert both event
payloads contain the mapped URI. Keep the existing static parser test and add
an assertion that a parser without `PublicURI` still applies the conservative
legacy result to `file:///E:/private/workspace/test.cpp`.

- [ ] **Step 2: Run the new manager tests to verify RED**

Run:

```text
go test ./internal/task -run 'PublicURI|StructuredDiagnostics|RedactsNative' -count=1
```

Expected: the provider-backed assertion fails before manager integration.

- [ ] **Step 3: Integrate the provider with both persistence paths**

In `persistDiagnostics`, obtain the current step parser once. For `value.FileURI`
and every `value.Related[index].FileURI`, type-assert
`diagnostic.PublicURIProvider`; call `PublicURI` when available and otherwise
call the existing `publicDiagnosticURI` fallback. Keep event ordering,
artifact writes, diagnostics precedence, and redaction regex unchanged.

- [ ] **Step 4: Run manager tests to verify GREEN**

Run:

```text
go test ./internal/task -run 'PublicURI|StructuredDiagnostics|RedactsNative' -count=1
```

Expected: PASS, including the fallback test and related-diagnostic mapping.

- [ ] **Step 5: Commit manager integration**

```text
git add apps/test-service/internal/task/manager.go apps/test-service/internal/task/manager_output_redaction_test.go apps/test-service/internal/task/manager_execution_test.go
git commit -m "fix: publish workspace-relative task diagnostics"
```

### Task 3: Normalize workspace URIs in the native service probe

**Files:**
- Modify: `tools/service-probe/src/native-fixture.ts`
- Test: `tools/service-probe/src/native-fixture.test.ts`

**Interfaces:**
- Consumes: protocol `sourceUri` values including `workspace:///relative`.
- Produces: normalized `<workspace>/relative` source URIs used by native golden comparisons.

- [ ] **Step 1: Add failing workspace-scheme tests**

Add cases to the existing fixture normalization tests:

```ts
expect(normalizeNativeDiagnostic({ sourceUri: "workspace:///src/main.cpp", message: "" }, roots).sourceUri)
  .toBe("<workspace>/src/main.cpp");
expect(normalizeNativeDiagnostic({ sourceUri: "workspace:///src/%E6%B5%8B.cpp", message: "" }, roots).sourceUri)
  .toBe("<workspace>/src/测.cpp");
for (const value of ["workspace://host/src/main.cpp", "workspace:///../secret.cpp", "workspace:///C:/secret.cpp"]) {
  expect(normalizeNativeDiagnostic({ sourceUri: value, message: "" }, roots).sourceUri).toBe(value);
}
```

- [ ] **Step 2: Run the focused TypeScript tests to verify RED**

Run:

```text
pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
```

Expected: the new workspace URI cases fail because the parser currently leaves
the `workspace:` scheme untouched.

- [ ] **Step 3: Implement strict workspace URI parsing**

Extend `parseDiagnosticPath` to accept only `workspace:` URLs with empty host,
query, and fragment. Decode the pathname, require a relative path, reject
empty components, drive prefixes, and any normalized `..` escape, and return a
portable relative path for the existing root mapper. Leave invalid values
unchanged so a golden comparison fails closed.

- [ ] **Step 4: Run focused TypeScript tests to verify GREEN**

Run:

```text
pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
```

Expected: PASS, including encoded names and all rejection cases.

- [ ] **Step 5: Commit service-probe normalization**

```text
git add tools/service-probe/src/native-fixture.ts tools/service-probe/src/native-fixture.test.ts
git commit -m "test: normalize workspace diagnostic URIs"
```

### Task 4: Run the complete qualification gate

**Files:**
- Modify: `docs/superpowers/` only if verification evidence needs an addendum.
- Test: existing Go, release, service-probe, and Windows native E2E suites.

**Interfaces:**
- Consumes: commits from Tasks 1–3.
- Produces: reproducible evidence that the MSVC compiler-diagnostic scenario reports `<workspace>/src/main.cpp` and that no absolute path leaks.

- [ ] **Step 1: Run all focused and package tests**

Run:

```text
go test ./internal/diagnostic ./internal/task -count=1
pnpm exec vitest run tools/service-probe/src/native-fixture.test.ts
node --test tools/release/update.test.mjs
```

Expected: PASS.

- [ ] **Step 2: Run repository verification**

Run the repository's pinned `pnpm verify` gate with the approved Node, pnpm,
Go, CMake bundle, race, and coverage settings. Expected: PASS with no generated
source changes.

- [ ] **Step 3: Run Windows native E2E with pinned MSVC**

Set `CMAKE_GENERATOR=Visual Studio 17 2022` and
`CMAKE_GENERATOR_TOOLSET=version=14.44.35207`, run the existing native E2E, and
require all scenarios to pass, especially `msvc compiler-diagnostic` with
`sourceUri=<workspace>/src/main.cpp`. Confirm cleanup leaves no service,
Code-OSS, CMake, CTest, or compiler processes.

- [ ] **Step 4: Review the diff and record evidence**

Run:

```text
git diff --check HEAD~3..HEAD
git status --short
```

Record test commands, the native E2E result, and the no-leak assertions in a
new `docs/superpowers/.superpowers/sdd/` report if required by the active SDD
ledger. Do not include absolute workspace paths in public evidence.

- [ ] **Step 5: Commit only evidence documentation, if needed**

```text
git add docs/superpowers
git commit -m "docs: record native diagnostic URI qualification"
```

Do not push, create a PR, merge, sync remotes, publish a release, or enable
signing as part of this plan; those remain separately authorized operations.
