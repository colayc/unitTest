# Native Diagnostic URI Privacy and Workspace Mapping Design

## Context

The reviewed CLI install-smoke change is complete.  Its native Windows E2E
qualification is still blocked by one independent protocol defect: an MSVC
diagnostic for `src/main.cpp` is published as `workspace:///` instead of a URI
that identifies the workspace-relative file.

The failure is reproducible in the service path:

```text
MSVC output
  -> diagnostic parser: file:///C:/.../src/main.cpp
  -> task.Manager.persistDiagnostics
  -> publicDiagnosticURI: workspace:///
  -> service event: sourceUri=workspace:///
  -> native-fixture: cannot recover the relative path
```

`publicDiagnosticURI` was introduced to prevent leaking a Windows drive and
absolute local path.  Removing it would regress that privacy boundary.  The
fix must therefore preserve the relative path for files inside the active
workspace while continuing to hide paths outside that workspace.

## Goals

- Publish an in-workspace diagnostic as `workspace:///` followed by its
  workspace-relative path, for example `workspace:///src/main.cpp`.
- Preserve the existing internal diagnostic model: parsers continue to retain
  canonical absolute `file:` URIs for identity, range handling, and storage.
- Keep absolute Windows paths and UNC paths out of public task events when the
  file is outside the workspace; those values collapse to `workspace:///`.
- Keep non-file URIs unchanged and preserve existing POSIX public behavior.
- Apply the same mapping to primary and related diagnostics.
- Make the native service probe normalize `workspace:///relative` into the
  existing `<workspace>/relative` golden representation without accepting
  traversal, a host, a query, or a fragment.
- Add focused unit tests and restore the Windows MSVC compiler-diagnostic E2E.

## Non-goals

- No change to compiler parsing, diagnostic IDs, ranges, severity, or message
  redaction.
- No change to artifact-store schemas or protocol version declarations.
- No exposure of the workspace's native root, drive letter, UNC server, or
  user name.
- No change to the CLI handshake, packaging, producer, signing, or release
  workflows.
- No broad redesign of workspace URI semantics for unrelated protocol data.

## Selected architecture

Keep absolute URIs inside `diagnostic.Diagnostic` and add a narrow optional
public-URI capability to the concrete parser:

```go
type PublicURIProvider interface {
    PublicURI(value string) string
}
```

The parser already owns the validated `workspace.Root`, so it is the only
component with enough context to distinguish an in-workspace Windows path from
an external path.  `task.Manager.persistDiagnostics` type-asserts the current
step parser to `PublicURIProvider` for both `FileURI` and related `FileURI`
values.  Test-only parsers that do not provide the capability retain the
existing conservative fallback (`workspace:///` for a Windows drive URI).

`(*parser).PublicURI` follows this policy:

1. Empty, `workspace:`, non-file, malformed, or already-public values are
   returned unchanged.
2. A canonical `file:` URI that resolves inside `Options.Root` is converted to
   `workspace:///` plus a URL-escaped, slash-separated relative path.  The
   root itself maps to `workspace:///`.
3. On Windows, a file URI outside the root is reduced to `workspace:///` so no
   drive, UNC host, or absolute path is exposed.  On non-Windows, external
   file URIs retain current behavior.
4. The mapper never uses string-prefix checks alone; it reuses the workspace
   path/containment rules and rejects a path-boundary sibling.

The public URI remains an absolute URI from the protocol's perspective, while
its path is intentionally workspace-relative and portable.

## Native service-probe normalization

Extend `parseDiagnosticPath` in `tools/service-probe/src/native-fixture.ts` to
recognize only `workspace:///` URIs with an empty host, query, and fragment.
Decode the path, require a non-empty relative path whose normalized segments
do not escape with `..`, and return a portable relative native path.  The
existing root mapper then emits `<workspace>/src/main.cpp`.  Invalid or
traversal workspace URIs remain unchanged and therefore fail the golden test
rather than being silently remapped.

## Compatibility and security boundaries

- Existing parser `FileURI` assertions remain absolute `file:` URIs.
- Existing manager tests using a static parser continue to exercise the
  fallback path and keep their current payload expectations.
- Public task events from real build parsers gain the missing relative path,
  but never gain an absolute native path.
- Related diagnostics follow exactly the same mapping as primary diagnostics.
- No root path is serialized, logged, or included in a diagnostic event.

## Verification strategy

1. Add parser tests for an in-workspace child, workspace root, path-boundary
   sibling, external Windows drive/UNC URI, non-file URI, and related values.
2. Add manager tests proving the provider path is used and the legacy fallback
   remains conservative.
3. Add service-probe unit tests for valid, encoded, host/query/fragment, and
   traversal `workspace:` URIs.
4. Run focused Go and TypeScript tests, then all release tests and the full
   repository verification gate.
5. Run the Windows native E2E with the pinned MSVC 14.44 generator and require
   the compiler-diagnostic scenario to report `<workspace>/src/main.cpp`.

## Acceptance criteria

- The MSVC native E2E compiler-diagnostic scenario passes with
  `<workspace>/src/main.cpp`.
- In-workspace public events contain `workspace:///relative/path`.
- External Windows paths remain `workspace:///` with no leaked absolute suffix.
- Parser internals and diagnostic IDs remain unchanged.
- Focused tests, all release tests, full verification, and native E2E pass.
- No files outside the approved diagnostic/task/service-probe/test scope are
  modified.
