# Packaged Extension Dependency Closure Design

## Context

The Windows Foundation 730 package contains the Unit Test IDE extension under
`app/extensions/unit-test-ide`, but release staging currently copies only the
extension `package.json` and `dist/` tree. The compiled extension imports
`@unit-test-ide/test-client` at runtime. That package and its transitive runtime
dependencies are available through the repository workspace during development,
but are absent from the installed application.

The installed extension therefore fails during activation with
`Cannot find package '@unit-test-ide/test-client'`. Activation stops before the
extension creates the `Unit Test IDE` output channel.

## Goals

- Produce a self-contained Code OSS extension runtime that does not resolve
  packages from repository `node_modules`.
- Keep `vscode` external because it is supplied by the Code OSS extension host.
- Preserve the existing source-oriented TypeScript build and unit-test outputs.
- Make release staging fail if it receives an extension that is not deployable.
- Exercise the same staged extension layout that is copied into the application.

## Non-goals

- Changing extension behavior, commands, protocol semantics, or service paths.
- Fixing other acceptance issues discovered after successful activation.
- Shipping a general-purpose production `node_modules` tree.
- Publishing a Release or enabling signing.

## Selected approach

Add an explicit production bundle step for `apps/code-oss-extension`. The step
uses esbuild to bundle `src/extension.ts` and all JavaScript and JSON runtime
dependencies into one ESM implementation. The existing CommonJS entry remains
the Code OSS `main` module and dynamically imports that bundled implementation.
Only the host-provided `vscode` module is external.

`test-client` currently loads protocol schema JSON through `createRequire()`.
Those dynamic calls are not a sound bundle boundary. Replace them with a
statically imported schema registry so esbuild can include every protocol
schema and the isolated test can prove that no schema package is needed at
runtime.

The ordinary TypeScript build remains responsible for type checking and the
compiled files used by the existing unit tests. The production bundle is a
separate deterministic artifact in `dist`, and the extension manifest points
`main` at that artifact.

This approach is preferred over copying production `node_modules` because it
keeps the packaged surface small, avoids pnpm symlink/layout assumptions, and
makes the dependency closure explicit. Manual dependency copying is rejected
because it would be fragile whenever `test-client`, AJV, or their dependency
graphs change.

## Build and staging flow

1. TypeScript compiles the workspace packages and extension as it does today.
2. The extension production bundle step emits an ESM implementation with
   runtime workspace, npm, and protocol-schema dependencies included. The
   CommonJS host entry loads this implementation.
3. Release staging copies the extension manifest and `dist/` tree.
4. Release staging validates that the manifest entry exists inside the copied
   extension and that it does not require repository package resolution.
5. The release manifest hashes the same files that are ultimately packaged.

The bundle must use a pinned build dependency and deterministic options. Source
maps may be retained for diagnostics, but source files and absolute build paths
must not become runtime requirements.

## Verification strategy

### Red test: isolated packaged entry

Add a test that constructs the release-shaped extension directory in a fresh
temporary parent that has no ancestor `node_modules`. The test loads the
manifest entry while providing only a controlled `vscode` substitute. On the
current implementation this test must fail with the missing
`@unit-test-ide/test-client` package.

After bundling, the same test must load the entry successfully, initialize the
bundled protocol validators, and prove that the exported `activate` and
`deactivate` functions are present.

### Release staging contract

Extend the staging tests to assert that the copied extension contains the
manifest-declared production entry. The fixture must model the final bundle,
not merely an arbitrary compiled source file.

### Extension-host smoke

Update the Code OSS host smoke path so it can target a staged, release-shaped
extension root. CI should launch that isolated extension rather than the source
workspace directory when validating packaging. A durable activation marker
remains the success criterion.

### Regression checks

- Run the full extension unit suite.
- Run release staging tests.
- Run an isolated package-load test with repository `node_modules` inaccessible.
- Run the real Code OSS Extension Host smoke when a Code OSS executable is
  available; otherwise report the explicit environment skip.

## Failure behavior

- Bundle failure stops the extension build.
- A missing manifest entry stops release staging before a release tree is
  published.
- Any unresolved non-host runtime import fails the isolated package-load test.
- Extension-host activation timeout or early process exit remains a hard smoke
  failure when the executable is configured.

## Security and release considerations

Only `vscode` is intentionally external. Node built-ins remain runtime-provided
platform modules. Bundled third-party code remains subject to the existing
release license inventory and final manual legal approval; this change does not
waive that Phase 8 gate.

No release publication, signing, remote push, pull request, or merge is part of
this implementation unless separately authorized.
