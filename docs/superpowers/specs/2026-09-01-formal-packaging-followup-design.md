# Formal Packaging Follow-up Design

## Context

Unsigned foundation run `33457835747` was dispatched from merged commit
`d27c6cae9c864810acee7e2c6924894b8ccb4ece` with trusted producer run
`33453419983`. Provenance verification and both platform input-verification jobs
passed, but the two packaging jobs exposed independent closed-set verification
defects:

- Windows packaging failed with
  `RELEASE_LICENSE_AUDIT_FAILED: license file set is not closed; missing=; unlisted=`.
  The empty difference lists prove that the manifest and staging tree contain the
  same paths. The manifest paths are globally sorted, while the actual paths are
  emitted in depth-first directory traversal order and compared positionally. A
  sibling directory/file prefix collision can therefore reject an identical set.
- Linux packaging failed with
  `RELEASE_VERIFICATION_FAILED: unsupported AppImage entry: .DirIcon`. The pinned
  real `appimagetool` creates the conventional root `.DirIcon` symbolic link after
  seeing `unit-test-ide.svg`. The verifier currently rejects every non-regular file,
  including that one required AppImage metadata link.

The AppImage AppDir convention permits `.DirIcon` as a link to the root icon. This
design accepts only the exact link produced for this product; it does not generally
allow symbolic links in the payload.

References:

- [AppImage AppDir specification](https://docs.appimage.org/reference/appdir.html)
- [AppImage format specification](https://github.com/AppImage/AppImageSpec/blob/master/draft.md)

## Goals

- Compare the Windows license inventory independently of traversal order while
  preserving an exact, duplicate-free, digest-verified closed set.
- Model AppImage entries explicitly as regular files or the one fixed `.DirIcon`
  symbolic link.
- Require `.DirIcon` to target the repository-owned `unit-test-ide.svg` by one exact
  relative link value.
- Keep all other symbolic links, aliases, unsupported entry types, and unexpected
  paths fail-closed.
- Prove both observed failures with focused regression tests before changing the
  implementation.
- Complete a fresh producer and unsigned foundation qualification after review and
  merge.

## Non-goals

- No release-manifest or AppImage sidecar schema change.
- No general symbolic-link support inside AppImages or release staging trees.
- No change to producer trust, artifact identity, source pinning, signing, or
  publication policy.
- No replacement of the temporary product SVG or broader branding work.
- No GitHub Release, paid signing input, or enabled signing path.
- No reuse of producer run `33453419983` after the source commit changes.

## Selected Approach

### Windows: normalize order, not membership

`collectLicenseFiles` continues to reject linked directories, symbolic links, and
unsupported filesystem entries. After collection, its complete path list is sorted
globally with the same `path.localeCompare(otherPath, "en")` comparator used for the
manifest license records. The two sorted arrays are then compared by exact length and
exact positional equality.

The existing missing/unlisted diagnostics remain in place, and every accepted file
is still independently checked for its manifest size and SHA-256 digest. This changes
only the accidental dependency on depth-first traversal order; it does not convert
the audit into a subset check or ignore duplicates.

A regression fixture introduces a prefix-colliding sibling directory and file, such
as `licenses/code-oss/a/z.txt` and `licenses/code-oss/a-b.txt`. Recursive traversal
visits the directory child first, while global full-path collation orders the sibling
file first. The fixture must fail before the fix and pass after it, with both paths
still bound by the release manifest.

### Linux: one typed metadata symlink

The AppImage verifier gains an explicit fixed path:

```text
.DirIcon -> unit-test-ide.svg
```

Directory extraction produces a typed entry record instead of assuming every entry
is a regular file:

- Regular files contain `kind: "file"`, bytes, executable state, size, and SHA-256.
- The only symbolic-link record permitted is root `.DirIcon` with
  `kind: "symlink"` and raw target `unit-test-ide.svg`.

The verifier reads the link with `readlink` and validates the raw target without
following it for link validation. Exact string equality rejects absolute targets,
`./unit-test-ide.svg`, traversal, slash or backslash variants, drive-qualified
targets, and every other filename. The separate root `unit-test-ide.svg` regular file
continues to be byte-compared with the repository asset and required to be
non-executable.

Every other symbolic link remains an unsupported AppImage entry. Regular-file
callers use a `requireFile` boundary that rejects a symlink record, and `.DirIcon`
uses a dedicated symlink requirement. The closed expected-path set explicitly
includes `.DirIcon`, so a missing link and any extra alias both fail verification.

The public verifier still rejects the fake AppImage envelope. Its injected test
extractor and the fake `appimagetool` are updated only to represent the same typed
entry union and to model the `.DirIcon` link created by the real pinned tool. No
production change is planned for `package-appimage.mjs`: it already supplies the
correct root SVG, and the real tool owns creation of `.DirIcon`.

## Components and Data Flow

### License audit

1. Validate the release manifest and reject duplicate manifest license paths.
2. Audit CMake and coverage notice relationships as today.
3. Traverse the real `licenses/` tree, rejecting links and unsupported entries.
4. Globally sort the complete actual path list with the manifest comparator.
5. Require exact count and exact sorted path equality.
6. Re-open each accepted license as a real file and verify exact size and SHA-256.

### AppImage verification

1. The packager creates the fixed AppDir with root `unit-test-ide.svg`.
2. The pinned `appimagetool` creates `.DirIcon -> unit-test-ide.svg` in the image.
3. Native extraction records regular files and reads the one fixed link target.
4. The common extraction boundary validates typed entry records, including injected
   test records.
5. The verifier requires exact AppRun, desktop entry, root SVG, `.DirIcon`, launcher,
   embedded manifest, artifacts, and licenses.
6. The expected-path audit rejects every path not explicitly derived from that
   contract.

If a future `appimagetool` version changes the link name or target syntax, verification
must fail and require a reviewed contract update rather than silently accepting new
filesystem behavior.

## Error Handling and Security Boundaries

- An actual license path mismatch retains `RELEASE_LICENSE_AUDIT_FAILED` with the
  existing missing/unlisted summaries.
- License links, reparse points, unsupported entries, size changes, and digest changes
  remain failures.
- Missing `.DirIcon`, a regular file substituted at that path, a wrong target, or an
  extra symbolic link is `RELEASE_VERIFICATION_FAILED`.
- The verifier never follows `.DirIcon` to decide whether its target is acceptable;
  it validates the raw target and independently validates the target file bytes.
- Extractor test hooks cannot widen production semantics: injected entries must use
  the same closed typed record model.
- Failure output must not expose tokens, signing material, or new absolute host paths.

## Testing

Implementation follows test-driven development.

### License ordering regression

- Add a valid fixture containing the prefix-colliding sibling directory/file paths.
- Demonstrate the current positional comparison fails with empty missing/unlisted
  differences.
- After the fix, require a sorted result containing both manifest-bound files.
- Retain rejection tests for missing, unlisted, duplicate, linked, size-mismatched,
  and digest-mismatched license entries.

### `.DirIcon` regressions

- Make the fake tool emit a typed `.DirIcon` link to `unit-test-ide.svg` and require
  the normal package/verify test to pass.
- Reject a missing `.DirIcon`.
- Reject a regular file substituted for `.DirIcon`.
- Reject wrong relative, `./`-prefixed, parent-traversal, absolute, backslash, and
  drive-qualified targets.
- Reject a symlink at any other path and an extra icon alias.
- Retain the root SVG missing, tampered, executable, and alias rejection cases.
- Retain all launcher, embedded manifest, artifact, license, digest, and closed-path
  tests.

### Verification sequence

1. Run the new focused cases in RED state.
2. Implement only the minimal license-order and typed-`.DirIcon` changes.
3. Run the focused files:
   `tools/release/license-audit.test.mjs` and
   `tools/release/linux/package-appimage.test.mjs`.
4. Run release contract tests and the complete `pnpm verify` gate with the repository's
   fixed Node, pnpm, Go, and CMake toolchain requirements.
5. After explicit authorization, push the review branch to GitHub and Gitee and open
   a GitHub PR. Do not merge it without a separate explicit confirmation.
6. After merge authorization and merge, synchronize GitHub `master` to Gitee
   `master`.
7. Dispatch a fresh trusted producer from the new merged commit and validate its
   provenance.
8. Dispatch a fresh foundation run with `release_version=0.1.0` and
   `release_signing_required=0`; require packaging, install smoke, and release
   qualification to succeed.

Failed foundation run `33457835747` remains diagnostic evidence and is never promoted
or reused as formal qualification evidence.

## Expected Implementation Scope

Production changes are limited to:

- `tools/release/license-audit.mjs`
- `tools/release/linux/verify-appimage.mjs`

Regression and fake-tool changes are limited to:

- `tools/release/license-audit.test.mjs`
- `tools/release/linux/package-appimage.test.mjs`

No `package-appimage.mjs`, workflow, manifest schema, signing, or release publication
change is expected. Any discovered need outside this scope pauses implementation for
design review.

## Reviewed Scope Expansion: Install Artifact Closed-Set Ordering

Fresh unsigned foundation run `33476360967` proved that both formal packages now
build successfully, then exposed the same downstream install-smoke failure on Windows
and Linux:

```text
RELEASE_VERIFICATION_FAILED: release artifact file set is not closed
```

The four release manifests extracted from artifacts `9788732178` and `9788796559`
prove exact membership: Windows has 9,739 declared files with no missing or unlisted
paths, while Linux has 10,324 declared files with no missing or unlisted paths. The
first mismatch on both platforms is the same prefix collision: recursive traversal
enters `app/code-oss-runtime/resources/...` before visiting the sibling file
`app/code-oss-runtime/resources.pak`, while global full-path collation orders
`resources.pak` first. A four-file fixture reproduces the exact CI failure code and
message.

The reviewed expansion permits production changes only in
`tools/release/update.mjs` and regression changes only in
`tools/release/update.test.mjs`. The actual file list is globally sorted with the
same English collation already used for the expected manifest paths before their
existing exact length and positional comparison. This is not a subset or
order-insensitive shortcut: every path must still be unique and present, and every
file must still pass the existing real-file, reparse-point, root-containment, size,
and SHA-256 checks.

The regression uses the real `installVersion` boundary with a valid manifest that
contains both `app/code-oss-runtime/resources/inside.txt` and the sibling
`app/code-oss-runtime/resources.pak`. It must fail before the production change with
the observed closed-set error, then install both files successfully after the fix.
No workflow, schema, signing, publication, producer-trust, or package-format change
is permitted by this expansion.

## Acceptance Criteria

- The prefix-collision license fixture passes only when actual and expected paths are
  compared as the same globally sorted, exact closed set.
- Missing, extra, duplicate, linked, size-mismatched, and digest-mismatched licenses
  still fail.
- A real AppImage containing exactly `.DirIcon -> unit-test-ide.svg` passes.
- Missing or mistyped `.DirIcon`, every alternate target, and every other symlink or
  alias fails.
- The root SVG remains a byte-verified, non-executable regular file.
- Focused release tests and the complete repository verification gate pass.
- The reviewed commit is synchronized to both remotes only through the authorized PR
  and merge flow.
- A fresh producer and fresh unsigned foundation run succeed on the merged commit.
- Signing stays disabled and no GitHub Release is published.
