# Cross-platform framework inputs

`manifest.json` is the closed schema-v2 source lock for CppUTest 4.0, Unity
2.6.1, and CMock 2.7.0 on Linux and Windows. Bootstrap code must reject
missing, mismatched, redirected, non-canonical, or path-escaping inputs before
a native process tree is launched.

`licenses/dependencies.json` is the immutable evidence input for the three
test dependencies: it records the pinned revision, archive license path and
digest, SPDX identifier, and revision-pinned GitHub blob URL. It is not a
substitute for the deferred human third-party license/legal approval.

The lock is an input boundary, not a runtime network exception: CI may populate
the immutable archive cache before the native offline boundary is entered.

`prepare.mjs` is the only publisher. It accepts only the locked GitHub download
chain, validates a bounded `tar` listing before extraction, and stores each
archive under its SHA-256. It then publishes a fully audited, digest-keyed
bundle at `.superpowers/runtime/framework-bundle/v2/<manifest SHA-256>/`.
Existing ready bundles are re-verified and reused; they are never deleted or
replaced. `READY` is written only after source markers, license bytes, and all
three source-tree digests have been verified.

Archive cache publication uses an atomic no-clobber hard link. Prepared-directory
publication serializes cooperating publishers with an exclusive lock, rechecks
the target, and preserves the whole-directory rename. Existing incomplete or
unsafe targets fail closed rather than being replaced. Losing publishers clean
their own staging, including EEXIST/ENOTEMPTY races; a stale lock is not deleted
by another publisher. This is not a guarantee against an adversarial local writer
that bypasses the publisher lock.

## Maintainer preparation and acceptance

Run from the repository root in a Windows developer shell with MSVC, clang-cl
and Ninja available. Docker must support Linux containers for the explicit
maintainer update only:

```powershell
pnpm prepare:cmake-bundle
pnpm prepare:framework-bundle
pnpm update:cmock-fixture
pnpm check:framework-bundle
go -C apps/test-service build -trimpath -o ../../build/unity-runner-generator.exe ./cmd/unity-runner-generator
$cmake = (node tools/cmake-bundle/prepare.mjs | ConvertFrom-Json).executable
$generator = (Resolve-Path build\unity-runner-generator.exe).Path
pnpm verify:framework-fixtures -- --cmake $cmake --generator $generator --toolchains msvc,clang-cl --frameworks cpputest,unity
```

`update:cmock-fixture` is maintainer-only: it uses the pinned container without
network access to regenerate the committed `MockDependency.c`,
`MockDependency.h`, and provenance. Review and commit their changes together.
Generated mock sources are never created by the product or ordinary CI.
`check:framework-bundle` verifies the lock, license inventory, provenance, and
any existing archive/prepared cache offline; an absent cache does not trigger a download.
Existing archive entries must be locked, regular, and digest-correct; unknown
entries or redirected cache components fail closed.
The ordinary `test` and `verify` chains are generator-free: they never invoke
Docker, Ruby, or Ceedling. The updater's unit tests use controlled executors,
not the container. Preparation and native compilation are explicit operations,
outside `verify`.

For Linux GCC acceptance, build `build/unity-runner-generator` from
`apps/test-service/cmd/unity-runner-generator`, prepare the same bundles, then run:

```sh
pnpm verify:framework-fixtures -- --cmake /absolute/path/to/cmake --generator "$PWD/build/unity-runner-generator" --toolchains gcc --frameworks cpputest,unity
```

The native verifier requires every requested compiler; missing compilers fail
rather than skip. Its stdout summary contains only framework/toolchain IDs and
contracted scenario outcomes, not local paths or credentials. It does not write
P4 `framework-report.json`, receipts, or gate status. F2 owns hosted four-toolchain
evidence (MSVC, clang-cl, GCC, Clang); F1 local acceptance is not that evidence.
`releaseReady` remains false. Windows signing, third-party license/legal human
approval, and Phase 8 documentation closeout remain the three approved deferrals.

The Unity fixture gives only the CMock library target the
`UNITY_EXCLUDE_STDDEF_H=1` compatibility definition on MSVC-compatible compilers:
the MSVC C11 standard library lacks `max_align_t`, so CMock uses its supported
alignment fallback. No vendored source, archive hash, or generated mock is changed.

Crash acceptance requires an observed crash signal or supported abnormal process
exit, not a spawn failure, an ordinary failure exit, or a verifier deadline kill.
The CppUTest fixture runs the crash directly without its fork-only `-p` option;
both Windows crash fixtures suppress interactive CRT error reporting before abort.
Public native errors expose stable codes and redacted messages, never raw child
stderr or local paths; raw output is used only internally to classify outcomes.
